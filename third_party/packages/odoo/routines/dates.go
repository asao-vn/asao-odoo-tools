package routines

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A day is a calendar date, held as midnight UTC so that adding days never meets a clock change.

const dayLayout = "2006-01-02"

func dayOf(moment time.Time) time.Time {
	return time.Date(moment.Year(), moment.Month(), moment.Day(), 0, 0, 0, 0, time.UTC)
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// addMonths moves a day by whole months, keeping its day of the month where the month has it and
// taking the month's last day where it does not (31 January + 1 month is 28 or 29 February).
func addMonths(day time.Time, months int) time.Time {
	index := day.Year()*12 + int(day.Month()) - 1 + months
	year, month := index/12, time.Month(index%12+1)
	return time.Date(year, month, min(day.Day(), daysIn(year, month)), 0, 0, 0, 0, time.UTC)
}

// anchors are the dates a filter may name, seen from day.
func anchors(day time.Time) map[string]time.Time {
	monthStart := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	weekStart := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	return map[string]time.Time{
		"today":            day,
		"yesterday":        day.AddDate(0, 0, -1),
		"week_start":       weekStart,
		"last_week_start":  weekStart.AddDate(0, 0, -7),
		"month_start":      monthStart,
		"last_month_start": addMonths(monthStart, -1),
		"next_month_start": addMonths(monthStart, 1),
	}
}

// Render writes the {today}-style dates in filters and values out for day.
func Render(value interface{}, day time.Time) interface{} {
	day = dayOf(day)
	switch typed := value.(type) {
	case string:
		marks := anchors(day)
		return placeholder.ReplaceAllStringFunc(typed, func(match string) string {
			parts := placeholder.FindStringSubmatch(match)
			moved := marks[parts[1]]
			if parts[2] != "" {
				amount, _ := strconv.Atoi(parts[3])
				if parts[2] == "-" {
					amount = -amount
				}
				if parts[4] == "m" {
					moved = addMonths(moved, amount)
				} else {
					moved = moved.AddDate(0, 0, amount)
				}
			}
			return moved.Format(dayLayout)
		})
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = Render(item, day)
		}
		return out
	default:
		items, ok := asList(value)
		if !ok {
			return value
		}
		out := make([]interface{}, len(items))
		for index, item := range items {
			out[index] = Render(item, day)
		}
		return out
	}
}

// Period is the first and last day of a review period, seen from day.
func Period(name string, day time.Time) (time.Time, time.Time) {
	day = dayOf(day)
	marks := anchors(day)
	switch name {
	case "yesterday":
		return marks["yesterday"], marks["yesterday"]
	case "last_7_days":
		return day.AddDate(0, 0, -7), marks["yesterday"]
	case "last_week":
		return marks["last_week_start"], marks["week_start"].AddDate(0, 0, -1)
	case "this_month":
		return marks["month_start"], day
	}
	return marks["last_month_start"], marks["month_start"].AddDate(0, 0, -1)
}

func (r *Routine) clock() (int, int) {
	hour, minute, _ := strings.Cut(r.At, ":")
	h, _ := strconv.Atoi(hour)
	m, _ := strconv.Atoi(minute)
	return h, m
}

func (r *Routine) monthDay(year int, month time.Month) int {
	last := daysIn(year, month)
	switch on := r.On.(type) {
	case int:
		return min(on, last)
	case string:
		if on == "last" {
			return last
		}
	}
	return 1
}

// Slot is the latest moment at or before now this routine was scheduled for, in now's time zone.
func (r *Routine) Slot(now time.Time) time.Time {
	hour, minute := r.clock()
	today := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	switch r.Every {
	case "daily":
		if today.After(now) {
			return today.AddDate(0, 0, -1)
		}
		return today
	case "weekdays":
		candidate := today
		if today.After(now) {
			candidate = today.AddDate(0, 0, -1)
		}
		for candidate.Weekday() == time.Saturday || candidate.Weekday() == time.Sunday {
			candidate = candidate.AddDate(0, 0, -1)
		}
		return candidate
	case "weekly":
		weekday := indexOf(Weekdays, text(r.On))
		if weekday < 0 {
			weekday = 0
		}
		monday := (int(today.Weekday()) + 6) % 7
		candidate := today.AddDate(0, 0, -(((monday-weekday)%7)+7)%7)
		if candidate.After(now) {
			return candidate.AddDate(0, 0, -7)
		}
		return candidate
	}
	year, month := now.Year(), now.Month()
	for range 2 {
		candidate := time.Date(year, month, r.monthDay(year, month), hour, minute, 0, 0, now.Location())
		if !candidate.After(now) {
			return candidate
		}
		if month == time.January {
			year, month = year-1, time.December
		} else {
			month--
		}
	}
	panic("routines: no monthly slot in two months")
}

// Next is the first slot after now.
func (r *Routine) Next(now time.Time) time.Time {
	probe, slot := now, now
	for !slot.After(now) {
		probe = probe.AddDate(0, 0, 1)
		slot = r.Slot(probe)
	}
	for {
		earlier := r.Slot(slot.Add(-time.Minute))
		if !earlier.After(now) {
			return slot
		}
		slot = earlier
	}
}

var weekdayNamesVI = []string{"thứ 2", "thứ 3", "thứ 4", "thứ 5", "thứ 6", "thứ 7", "chủ nhật"}

// Schedule says when a routine runs, in the file's language.
func (r *Routine) Schedule(language string) string {
	vi := language != "en"
	switch r.Every {
	case "daily":
		return pick(vi, "hằng ngày ", "daily ") + r.At
	case "weekdays":
		return pick(vi, "thứ 2-6 ", "weekdays ") + r.At
	case "weekly":
		index := max(indexOf(Weekdays, text(r.On)), 0)
		if vi {
			return "hằng tuần " + weekdayNamesVI[index] + " " + r.At
		}
		return "weekly " + Weekdays[index] + " " + r.At
	}
	on := r.On
	if on == nil {
		on = 1
	}
	if vi {
		if on == "last" {
			return "hằng tháng ngày cuối " + r.At
		}
		return fmt.Sprintf("hằng tháng ngày %v %s", on, r.At)
	}
	if on == "last" {
		return "monthly on the last day " + r.At
	}
	return fmt.Sprintf("monthly on day %v %s", on, r.At)
}

func pick(vi bool, inVietnamese, inEnglish string) string {
	if vi {
		return inVietnamese
	}
	return inEnglish
}

func indexOf(list []string, value string) int {
	for index, item := range list {
		if item == value {
			return index
		}
	}
	return -1
}
