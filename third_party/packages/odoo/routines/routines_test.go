package routines

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func day(value string) time.Time {
	parsed, err := time.Parse(dayLayout, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func at(value string) time.Time {
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
	if err != nil {
		panic(err)
	}
	return parsed
}

// The dates a filter names are counted from the day of the slot, by days or by months.
func TestRenderWritesTheDatesOut(t *testing.T) {
	friday := day("2026-10-16")
	for mark, want := range map[string]string{
		"{today}": "2026-10-16", "{yesterday}": "2026-10-15", "{today-14}": "2026-10-02",
		"{week_start}": "2026-10-12", "{week_start+4}": "2026-10-16", "{last_week_start}": "2026-10-05",
		"{month_start}": "2026-10-01", "{last_month_start}": "2026-09-01", "{next_month_start}": "2026-11-01",
		"{month_start-3m}": "2026-07-01", "{next_month_start+4}": "2026-11-05", "Báo cáo {today}": "Báo cáo 2026-10-16",
	} {
		if got := Render(mark, friday); got != want {
			t.Errorf("%s: %v, want %s", mark, got, want)
		}
	}
	filter := Render([]interface{}{[]interface{}{"date", "<", "{today}"}, []interface{}{"x", "in", []interface{}{"{yesterday}", 3}}}, friday)
	encoded, _ := encodeJSON(filter, false)
	if strings.TrimSpace(string(encoded)) != `[["date","<","2026-10-16"],["x","in",["2026-10-15",3]]]` {
		t.Errorf("filter: %s", encoded)
	}
	for _, c := range []struct {
		from, want string
		months     int
	}{
		{"2026-01-31", "2026-02-28", 1}, {"2024-01-31", "2024-02-29", 1}, {"2026-03-31", "2026-02-28", -1}, {"2026-01-15", "2025-10-15", -3},
	} {
		if got := addMonths(day(c.from), c.months).Format(dayLayout); got != c.want {
			t.Errorf("%s %+d months: %s, want %s", c.from, c.months, got, c.want)
		}
	}
}

func TestPeriods(t *testing.T) {
	friday := day("2026-10-16")
	for name, want := range map[string]string{
		"yesterday": "2026-10-15..2026-10-15", "last_7_days": "2026-10-09..2026-10-15", "last_week": "2026-10-05..2026-10-11",
		"this_month": "2026-10-01..2026-10-16", "last_month": "2026-09-01..2026-09-30",
	} {
		from, to := Period(name, friday)
		if got := from.Format(dayLayout) + ".." + to.Format(dayLayout); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}

// A slot is the latest scheduled moment at or before now; Next is the first one after it.
func TestSlotsAndNextRuns(t *testing.T) {
	cases := []struct {
		routine    Routine
		now        string
		slot, next string
	}{
		{Routine{Every: "daily", At: "07:30"}, "2026-10-16 08:00", "2026-10-16 07:30", "2026-10-17 07:30"},
		{Routine{Every: "daily", At: "07:30"}, "2026-10-16 07:00", "2026-10-15 07:30", "2026-10-16 07:30"},
		{Routine{Every: "weekdays", At: "07:30"}, "2026-10-17 09:00", "2026-10-16 07:30", "2026-10-19 07:30"},
		{Routine{Every: "weekdays", At: "07:30"}, "2026-10-19 07:00", "2026-10-16 07:30", "2026-10-19 07:30"},
		{Routine{Every: "weekly", At: "08:00", On: "mon"}, "2026-10-16 12:00", "2026-10-12 08:00", "2026-10-19 08:00"},
		{Routine{Every: "weekly", At: "08:00", On: "mon"}, "2026-10-12 07:00", "2026-10-05 08:00", "2026-10-12 08:00"},
		{Routine{Every: "weekly", At: "08:00", On: "sun"}, "2026-10-16 12:00", "2026-10-11 08:00", "2026-10-18 08:00"},
		{Routine{Every: "monthly", At: "08:00", On: 1}, "2026-10-16 12:00", "2026-10-01 08:00", "2026-11-01 08:00"},
		{Routine{Every: "monthly", At: "08:00", On: 1}, "2026-10-01 07:00", "2026-09-01 08:00", "2026-10-01 08:00"},
		{Routine{Every: "monthly", At: "08:00", On: "last"}, "2026-10-16 12:00", "2026-09-30 08:00", "2026-10-31 08:00"},
		{Routine{Every: "monthly", At: "08:00", On: 31}, "2026-10-16 12:00", "2026-09-30 08:00", "2026-10-31 08:00"},
		{Routine{Every: "monthly", At: "08:00"}, "2026-10-16 12:00", "2026-10-01 08:00", "2026-11-01 08:00"},
	}
	for _, c := range cases {
		now := at(c.now)
		if got := c.routine.Slot(now).Format("2006-01-02 15:04"); got != c.slot {
			t.Errorf("%s %v at %s: slot %s, want %s", c.routine.Every, c.routine.On, c.now, got, c.slot)
		}
		if got := c.routine.Next(now).Format("2006-01-02 15:04"); got != c.next {
			t.Errorf("%s %v at %s: next %s, want %s", c.routine.Every, c.routine.On, c.now, got, c.next)
		}
	}
}

func file(t *testing.T, routines ...map[string]interface{}) *File {
	t.Helper()
	items := make([]interface{}, len(routines))
	for index := range routines {
		items[index] = routines[index]
	}
	loaded, err := FromMap(map[string]interface{}{"profile": "lab", "output": t.TempDir(), "routine": items})
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func readStep(name, operation string) map[string]interface{} {
	return map[string]interface{}{"name": name, "read": operation}
}

// A file that cannot run as written is refused whole, naming the routine and the field.
func TestFromMapRefusesWhatCannotRun(t *testing.T) {
	good := func(change func(map[string]interface{})) map[string]interface{} {
		routine := map[string]interface{}{"id": "r", "step": []interface{}{readStep("s", "finance.invoice.list")}}
		change(routine)
		return map[string]interface{}{"profile": "lab", "routine": []interface{}{routine}}
	}
	for want, data := range map[string]map[string]interface{}{
		"say which Odoo":          {"routine": []interface{}{}},
		"lower-case letters":      good(func(r map[string]interface{}) { r["id"] = "Bad Id" }),
		"every = \"hourly\"":      good(func(r map[string]interface{}) { r["every"] = "hourly" }),
		"a time as HH:MM":         good(func(r map[string]interface{}) { r["at"] = "7:30" }),
		"a weekday":               good(func(r map[string]interface{}) { r["every"], r["on"] = "weekly", "monday" }),
		"a day of the month":      good(func(r map[string]interface{}) { r["every"], r["on"] = "monthly", int64(32) }),
		"needs at least one step": good(func(r map[string]interface{}) { r["step"] = []interface{}{} }),
		"exactly one of": good(func(r map[string]interface{}) {
			r["step"] = []interface{}{map[string]interface{}{"read": "a.list", "group": "a.list"}}
		}),
		`read = "<operation id>"`: good(func(r map[string]interface{}) { r["step"] = []interface{}{map[string]interface{}{"read": true}} }),
		"unknown date {tomorrow}": good(func(r map[string]interface{}) {
			r["step"] = []interface{}{map[string]interface{}{"read": "a.list", "filter": []interface{}{[]interface{}{"d", "=", "{tomorrow}"}}}}
		}),
		"say writes = true": good(func(r map[string]interface{}) {
			r["step"] = []interface{}{map[string]interface{}{"execute": "work.task.create", "values": map[string]interface{}{"name": "x"}}}
		}),
		`ensure_task = { name = "..."`: good(func(r map[string]interface{}) {
			r["writes"], r["step"] = true, []interface{}{map[string]interface{}{"ensure_task": map[string]interface{}{"name": "x"}}}
		}),
		"period = \"last_year\"": good(func(r map[string]interface{}) {
			r["step"] = []interface{}{map[string]interface{}{"review": true, "period": "last_year"}}
		}),
		"language = \"fr\"":       {"profile": "lab", "language": "fr"},
		"an agent's name":         {"profile": "lab", "agent": []interface{}{}},
		"two routines are called": {"profile": "lab", "routine": []interface{}{map[string]interface{}{"id": "r", "step": []interface{}{readStep("s", "a.list")}}, map[string]interface{}{"id": "r", "step": []interface{}{readStep("s", "a.list")}}}},
	} {
		_, err := FromMap(data)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}

// Every starter routine loads as written, and only the one that writes says so.
func TestTemplatesLoadAndRoundTrip(t *testing.T) {
	var routines []interface{}
	ids := map[string]bool{}
	for _, template := range Templates() {
		if ids[template.ID] {
			t.Fatalf("two templates are called %s", template.ID)
		}
		ids[template.ID] = true
		if Areas[template.Area] == nil {
			t.Errorf("%s: area %s has no name", template.ID, template.Area)
		}
		routines = append(routines, template.AsRoutine("vi"))
	}
	loaded, err := FromMap(map[string]interface{}{"profile": "lab", "routine": routines})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Routines) != 10 {
		t.Fatalf("%d routines", len(loaded.Routines))
	}
	for _, routine := range loaded.Routines {
		if routine.Writes != (routine.ID == "len-viec-dinh-ky") {
			t.Errorf("%s: writes %v", routine.ID, routine.Writes)
		}
	}
	monthly, _ := loaded.Get("ra-soat-thang")
	if monthly.On != 1 || monthly.Schedule("vi") != "hằng tháng ngày 1 08:00" {
		t.Errorf("monthly: on %v, %s", monthly.On, monthly.Schedule("vi"))
	}
	written, err := TOML(map[string]interface{}{"profile": "lab", "agent": "claude", "routine": routines}, "Việc định kỳ")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{`profile = "lab"`, "[[routine]]", `id = "cong-no-qua-han"`, "  [[routine.step]]",
		`  group = "finance.invoice.list"`, `  limit = 30`, `on = 1`, `on = "mon"`,
		`  ensure_task = { name = "Chốt quỹ tiền mặt", project = "Vận hành", recurring = true, repeat_every = 1, repeat_unit = "day", due_on = "{today}" }`,
		`  filter = [["state", "=", "sale"], ["date_order", ">=", "{yesterday}"], ["date_order", "<", "{today}"]]`} {
		if !strings.Contains(written, line+"\n") {
			t.Errorf("the TOML lacks %q", line)
		}
	}
}

// fakeERP answers a routine from memory and records what it was asked.
type fakeERP struct {
	asked   []string
	tasks   []map[string]interface{}
	created map[string]interface{}
}

func (f *fakeERP) Read(_ context.Context, operation string, domain []interface{}, limit int, order string, id *int64) ([]map[string]interface{}, error) {
	encoded, _ := json.Marshal(domain)
	f.asked = append(f.asked, "read "+operation+" "+string(encoded))
	switch operation {
	case "work.task.list":
		return f.tasks, nil
	case "missing.list":
		return nil, &Refusal{Code: "AERP_UNKNOWN_OPERATION", Message: "no operation missing.list"}
	}
	rows := []map[string]interface{}{}
	for index := 0; index < 3; index++ {
		rows = append(rows, map[string]interface{}{"id": json.Number(string(rune('1' + index))), "name": "INV/" + string(rune('1'+index)),
			"partner": map[string]interface{}{"id": json.Number("7"), "display_name": "Khách A"}, "amount_residual": json.Number("1250000.5")})
	}
	return rows[:min(limit, 3)], nil
}

func (f *fakeERP) Group(_ context.Context, operation string, by, aggregate []string, domain []interface{}, limit int, order string) ([]map[string]interface{}, error) {
	f.asked = append(f.asked, "group "+operation+" "+strings.Join(by, ",")+" "+strings.Join(aggregate, ","))
	return []map[string]interface{}{
		{"partner_id": []interface{}{json.Number("7"), "Khách A"}, "amount_residual:sum": json.Number("3000000"), "count": json.Number("2")},
		{"partner_id": []interface{}{json.Number("8"), "Khách B"}, "amount_residual:sum": json.Number("500000"), "count": json.Number("1")},
	}, nil
}

func (f *fakeERP) Review(_ context.Context, from, to, asOf time.Time) (map[string]interface{}, error) {
	f.asked = append(f.asked, "review "+from.Format(dayLayout)+" "+to.Format(dayLayout)+" "+asOf.Format(dayLayout))
	return map[string]interface{}{"counts": map[string]interface{}{"XEM_NGAY": json.Number("1")},
		"findings": []interface{}{map[string]interface{}{"severity": "XEM_NGAY", "check": "O01", "title": "Hoá đơn nhập trùng"}}}, nil
}

func (f *fakeERP) Execute(_ context.Context, operation string, id *int64, values map[string]interface{}) (interface{}, error) {
	encoded, _ := json.Marshal(values)
	f.asked = append(f.asked, "execute "+operation+" "+string(encoded))
	f.created = values
	return map[string]interface{}{"id": json.Number("41")}, nil
}

func (f *fakeERP) Describe(operation string) []Meaning {
	return []Meaning{{Name: "partner", ProviderField: "partner_id", Description: "the customer"}}
}

// A run asks each step, keeps what Odoo answered, sums what it groups, counts what it refused, and
// hands the data to the agent, whose answer is kept beside it.
func TestRunKeepsEveryStepAndTheAgentsReading(t *testing.T) {
	routines := file(t, map[string]interface{}{
		"id": "cong-no", "title": "Công nợ", "area": "finance", "every": "daily", "at": "07:30", "writes": true,
		"prompt": "Ai nợ nhiều nhất?", "agent": []interface{}{"reader", "--answer", "{answer_file}"},
		"step": []interface{}{
			map[string]interface{}{"name": "theo-khach", "group": "finance.invoice.list", "by": []interface{}{"partner_id"},
				"aggregate": []interface{}{"amount_residual:sum"}, "filter": []interface{}{[]interface{}{"invoice_date_due", "<", "{today}"}}},
			map[string]interface{}{"name": "cu-nhat", "read": "finance.invoice.list", "limit": int64(2)},
			map[string]interface{}{"name": "hong", "read": "missing.list"},
			map[string]interface{}{"name": "ra-soat", "review": true, "period": "last_week"},
			map[string]interface{}{"name": "chot-quy", "ensure_task": map[string]interface{}{"name": "Chốt quỹ", "project": "Vận hành", "due_on": "{today}"}},
		},
	})
	erp := &fakeERP{}
	var ran []string
	var given string
	runner := func(_ context.Context, command []string, stdin, folder string, _ time.Duration) (string, string, int, error) {
		ran, given = command, stdin
		return "", "", 0, os.WriteFile(command[2], []byte("## Tóm tắt\nKhách A nợ nhiều nhất.\n"), 0o600)
	}
	routine, _ := routines.Get("cong-no")
	slot := at("2026-10-16 07:30")
	result, err := routines.Run(context.Background(), erp, routine, slot, Options{RunAgent: runner, Now: func() time.Time { return at("2026-10-16 07:31") }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Errors != 1 || result.Steps[2].Error.Code != "AERP_UNKNOWN_OPERATION" {
		t.Fatalf("errors %d, %+v", result.Errors, result.Steps[2])
	}
	if result.Steps[0].Totals["amount_residual:sum"] != int64(3500000) || result.Steps[0].Totals["count"] != int64(3) {
		t.Errorf("totals %v", result.Steps[0].Totals)
	}
	if *result.Steps[1].Count != 2 || *result.Steps[1].Complete {
		t.Errorf("a full page is not complete: %+v", result.Steps[1])
	}
	if !result.Steps[4].Task.Created || erp.created["due_on"] != "2026-10-16" {
		t.Errorf("task %+v, values %v", result.Steps[4].Task, erp.created)
	}
	if !strings.Contains(strings.Join(erp.asked, "\n"), "review 2026-10-05 2026-10-11 2026-10-16") {
		t.Errorf("asked %v", erp.asked)
	}
	if result.Agent.Status != "OK" || ran[0] != "reader" || !strings.Contains(given, "Ai nợ nhiều nhất?") || !strings.Contains(given, `"amount_residual:sum":3000000`) {
		t.Fatalf("agent %+v, command %v", result.Agent, ran)
	}
	folder := filepath.Join(routines.Output, "cong-no", "2026-10-16")
	for name, want := range map[string]string{
		"summary.md":  "count: 3, amount_residual:sum: 3,500,000",
		"analysis.md": "Khách A nợ nhiều nhất.",
		"prompt.md":   "partner (= partner_id): the customer",
		"result.json": `"slot": "2026-10-16T07:30"`,
	} {
		data, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("%s lacks %q:\n%s", name, want, data)
		}
	}
	summary, _ := os.ReadFile(filepath.Join(folder, "summary.md"))
	for _, want := range []string{"| partner_id | amount_residual:sum | count |", "| Khách A | 3,000,000 | 2 |", "- #1 INV/1 · Khách A · 1,250,000.5",
		"**Lỗi** AERP_UNKNOWN_OPERATION", "2026-10-05 → 2026-10-11: 1 phát hiện", "Đã tạo #41"} {
		if !strings.Contains(string(summary), want) {
			t.Errorf("summary lacks %q:\n%s", want, summary)
		}
	}
	index, _ := os.ReadFile(filepath.Join(routines.Output, "index.md"))
	if !strings.Contains(string(index), "[2026-10-16T07:30](cong-no/2026-10-16/summary.md)") {
		t.Errorf("index:\n%s", index)
	}
	if guide, err := os.ReadFile(filepath.Join(routines.Output, "CLAUDE.md")); err != nil || !strings.Contains(string(guide), "odoo-cli routines run") {
		t.Errorf("guide: %v", err)
	}

	// The task is there now: a second run finds it and creates nothing.
	erp.tasks, erp.created = []map[string]interface{}{{"id": json.Number("41")}}, nil
	result, err = routines.Run(context.Background(), erp, routine, slot, Options{Agent: "none"})
	if err != nil || result.Steps[4].Task.Created || erp.created != nil || result.Agent.Status != "SKIPPED" {
		t.Fatalf("second run: %v, %+v, %v, %+v", err, result.Steps[4].Task, erp.created, result.Agent)
	}
}

// A run that reached Odoo at no step keeps its folder for diagnosis but leaves the slot open, and
// no agent is asked to read errors.
func TestARunThatReachedNothingStaysDue(t *testing.T) {
	routines := file(t, map[string]interface{}{"id": "daily", "every": "daily", "at": "07:30", "agent": "claude",
		"step": []interface{}{readStep("s", "a.list"), readStep("t", "b.list")}})
	now := at("2026-10-16 09:00")
	routine, _ := routines.Get("daily")
	result, err := routines.Run(context.Background(), &unreachableERP{}, routine, routine.Slot(now), Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Retry || result.Agent.Status != "SKIPPED" || result.Errors != 2 {
		t.Fatalf("retry %v, agent %+v, errors %d", result.Retry, result.Agent, result.Errors)
	}
	if due := routines.Due(now); len(due) != 1 {
		t.Fatalf("the slot was taken as run: %+v", due)
	}
}

type unreachableERP struct{ fakeERP }

func (*unreachableERP) Read(context.Context, string, []interface{}, int, string, *int64) ([]map[string]interface{}, error) {
	return nil, &Refusal{Code: "PROVIDER_UNREACHABLE", Message: "connection refused", Retryable: true}
}

// A routine is due when its slot has come, it has not run for that slot, and it is not too late.
func TestDueAndTheLock(t *testing.T) {
	routines := file(t,
		map[string]interface{}{"id": "daily", "every": "daily", "at": "07:30", "step": []interface{}{readStep("s", "a.list")}},
		map[string]interface{}{"id": "monthly", "every": "monthly", "on": int64(1), "at": "08:00", "step": []interface{}{readStep("s", "a.list")}},
		map[string]interface{}{"id": "off", "enabled": false, "step": []interface{}{readStep("s", "a.list")}},
	)
	now := at("2026-10-16 09:00")
	due := routines.Due(now)
	if len(due) != 1 || due[0].Routine.ID != "daily" {
		t.Fatalf("due %+v (the monthly slot of 1 October is 15 days late)", due)
	}
	runs, err := routines.RunDue(context.Background(), func(*Routine) ERP { return &fakeERP{} }, Options{Agent: "none", Now: func() time.Time { return now }})
	if err != nil || len(runs) != 1 || runs[0].Result == nil {
		t.Fatalf("run due: %v %+v", err, runs)
	}
	if again := routines.Due(now); len(again) != 0 {
		t.Fatalf("the slot ran, yet due again: %+v", again)
	}
	lock := filepath.Join(routines.Output, ".due.lock")
	if err := os.WriteFile(lock, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := routines.RunDue(context.Background(), func(*Routine) ERP { return &fakeERP{} }, Options{Now: func() time.Time { return now }}); err != ErrDueRunning {
		t.Fatalf("a held lock: %v", err)
	}
	old := time.Now().Add(-7 * time.Hour)
	_ = os.Chtimes(lock, old, old)
	if _, err := routines.RunDue(context.Background(), func(*Routine) ERP { return &fakeERP{} }, Options{Agent: "none"}); err != nil {
		t.Fatalf("a lock left by a run that died is taken: %v", err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the lock is released: %v", err)
	}
}

func TestTotalsPerCurrencyAndCells(t *testing.T) {
	totals := Totals([]map[string]interface{}{
		{"currency_id": []interface{}{json.Number("1"), "VND"}, "amount:sum": json.Number("1000"), "count": json.Number("1")},
		{"currency_id": []interface{}{json.Number("2"), "USD"}, "amount:sum": json.Number("10.255"), "count": json.Number("2")},
		{"currency_id": []interface{}{json.Number("2"), "USD"}, "amount:sum": json.Number("5"), "count": json.Number("1")},
	}, []string{"amount:sum", "amount:avg"})
	per, _ := totals["amount:sum"].(map[string]interface{})
	if totals["count"] != int64(4) || per["VND"] != int64(1000) || per["USD"] != 15.26 || totals["amount:avg"] != nil {
		t.Errorf("totals %v", totals)
	}
	for value, want := range map[interface{}]string{json.Number("1234567"): "1,234,567", json.Number("1234.5"): "1,234.5",
		0.25: "0.25", -1234.567: "-1,234.57", 2.0: "2", json.Number("-12"): "-12", true: "True"} {
		if got := Cell(value); got != want {
			t.Errorf("Cell(%v) = %q, want %q", value, got, want)
		}
	}
}

// A run too large for the agent is cut from its longest step, and the cut is said.
func TestFitCutsTheLongestStepAndSaysSo(t *testing.T) {
	rows := make([]map[string]interface{}, 400)
	for index := range rows {
		rows[index] = map[string]interface{}{"id": index, "name": strings.Repeat("x", 40)}
	}
	result := &Result{Routine: "r", Steps: []StepResult{{Name: "s", Kind: "read", Rows: &rows, Status: "OK"}}}
	text := fit(result, 4000, true)
	if len(text) > 4001 || !strings.Contains(text, "chỉ giữ") {
		t.Fatalf("%d bytes: %.200s", len(text), text)
	}
	if len(*result.Steps[0].Rows) != 400 {
		t.Fatal("fitting changed the run itself")
	}
}
