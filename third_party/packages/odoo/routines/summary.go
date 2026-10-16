package routines

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Label is a value as a person reads it: a relation by its name, nothing as "-".
func Label(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return "-"
	case bool:
		if !typed {
			return "-"
		}
		return "True"
	case map[string]interface{}:
		for _, key := range []string{"display_name", "value", "id"} {
			if item, ok := typed[key]; ok && truthy(item) {
				return text(item)
			}
		}
		return text(typed["id"])
	case []interface{}:
		if len(typed) == 2 {
			if name, isText := typed[1].(string); isText {
				return name
			}
		}
	case string:
		return typed
	}
	return text(value)
}

func truthy(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	}
	if n, ok := number(value); ok {
		return n != 0
	}
	return true
}

// Cell is a value in a summary: numbers with thousands separators, relations by name.
func Cell(value interface{}) string {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case string:
		return typed
	}
	if isWhole(value) {
		n, _ := integer(value)
		return formatInt(n)
	}
	if f, ok := number(value); ok {
		return formatFloat(f)
	}
	return Label(value)
}

// formatFloat writes a number to the cent without trailing zeros, as Python's "{:,.2f}" stripped does.
func formatFloat(value float64) string {
	rounded := math.Round(value*100) / 100
	whole := int64(rounded)
	fraction := strconv.FormatFloat(math.Abs(rounded-float64(whole)), 'f', 2, 64)[1:] // ".25"
	sign := ""
	if rounded < 0 && whole == 0 {
		sign = "-"
	}
	out := sign + formatInt(whole) + fraction
	out = strings.TrimRight(out, "0")
	return strings.TrimSuffix(out, ".")
}

// rowDetails are the fields a summary line shows after a record's name, when the record has them.
var rowDetails = []string{"partner", "project", "user_id", "total_amount", "amount", "amount_residual", "expected_revenue",
	"invoice_date_due", "due_on", "scheduled_date", "date", "payment_status", "state", "source_document"}

var dateBuckets = map[string]bool{"day": true, "week": true, "month": true, "quarter": true, "year": true}

// Summary is the run in figures: per step its count, totals and first rows. Computed, never generated.
func Summary(result *Result, language string) string {
	vi := language != "en"
	lines := []string{"# " + result.Title, "",
		fmt.Sprintf("%s: %s · %s %s · %s", pick(vi, "Lịch", "Slot"), result.Slot, pick(vi, "chạy lúc", "ran at"), result.RanAt, result.Source), ""}
	for _, step := range result.Steps {
		heading := "## " + step.Name + " (" + step.Kind
		if step.Operation != nil {
			heading += " " + *step.Operation
		}
		lines = append(lines, heading+")")
		if step.Asked != "" {
			lines = append(lines, "_"+step.Asked+"_")
		}
		if step.Status == "ERROR" && step.Error != nil {
			lines = append(lines, fmt.Sprintf("**%s** %s: %s", pick(vi, "Lỗi", "Error"), step.Error.Code, step.Error.Message), "")
			continue
		}
		switch step.Kind {
		case "group":
			lines = append(lines, totalsLine(step.Totals))
			if step.Groups != nil && len(*step.Groups) > 0 {
				groups := *step.Groups
				keys := groupColumns(groups[0], step.By)
				if len(keys) > 0 {
					lines = append(lines, "", "| "+strings.Join(append(append([]string{}, keys...), "count"), " | ")+" |",
						"|"+strings.Repeat("---|", len(keys)+1))
					for index, group := range groups {
						if index == 20 {
							break
						}
						cells := make([]string, 0, len(keys))
						for _, key := range keys {
							cells = append(cells, Cell(group[key]))
						}
						lines = append(lines, "| "+strings.Join(cells, " | ")+fmt.Sprintf(" | %v |", text(group["count"])))
					}
				}
			}
		case "read":
			more := ""
			if step.Complete != nil && !*step.Complete {
				more = pick(vi, " (hoặc hơn)", " (or more)")
			}
			count := 0
			if step.Count != nil {
				count = *step.Count
			}
			lines = append(lines, fmt.Sprintf("%d %s%s", count, pick(vi, "bản ghi", "records"), more))
			if step.Rows != nil {
				for index, row := range *step.Rows {
					if index == 10 {
						break
					}
					var details []string
					for _, key := range rowDetails {
						if value, ok := row[key]; ok && present(value) {
							details = append(details, Cell(value))
						}
					}
					name := row["name"]
					if !truthy(name) {
						name = row["display_name"]
					}
					line := fmt.Sprintf("- #%s %s", text(row["id"]), Label(name))
					if len(details) > 0 {
						line += " · " + strings.Join(details[:min(len(details), 5)], " · ")
					}
					lines = append(lines, line)
				}
			}
		case "review":
			counts, _ := encodeJSON(step.Report["counts"], false)
			findings, _ := asList(step.Report["findings"])
			lines = append(lines, fmt.Sprintf("%s → %s: %d %s %s", step.Period["from"], step.Period["to"], len(findings),
				pick(vi, "phát hiện", "findings"), strings.TrimSpace(string(counts))))
			for index, raw := range findings {
				if index == 10 {
					break
				}
				finding, _ := raw.(map[string]interface{})
				lines = append(lines, fmt.Sprintf("- [%s] %s %s", text(finding["severity"]), text(finding["check"]), text(finding["title"])))
			}
		case "ensure_task":
			if step.Task != nil {
				state := pick(vi, "Đã có sẵn", "Already there")
				if step.Task.Created {
					state = pick(vi, "Đã tạo", "Created")
				}
				lines = append(lines, fmt.Sprintf("%s #%s", state, text(step.Task.ID)))
			}
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func present(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case []interface{}:
		return len(typed) > 0
	}
	return true
}

func totalsLine(totals map[string]interface{}) string {
	keys := sortedKeys(totals)
	// count first, then the aggregates in order.
	sort.SliceStable(keys, func(a, b int) bool { return keys[a] == "count" && keys[b] != "count" })
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if per, isPer := totals[key].(map[string]interface{}); isPer {
			var amounts []string
			for _, currency := range sortedKeys(per) {
				amounts = append(amounts, Cell(per[currency])+" "+currency)
			}
			parts = append(parts, key+": "+strings.Join(amounts, " + "))
			continue
		}
		parts = append(parts, key+": "+Cell(totals[key]))
	}
	return strings.Join(parts, ", ")
}

// groupColumns are a group's columns: the fields and date buckets it is grouped by, in the step's
// order, then its aggregates; count has its own column.
func groupColumns(group map[string]interface{}, by []string) []string {
	var grouped, aggregated []string
	for _, key := range by {
		if _, ok := group[key]; ok {
			grouped = append(grouped, key)
		}
	}
	for _, key := range sortedKeys(group) {
		if key == "count" || indexOf(grouped, key) >= 0 {
			continue
		}
		_, bucket, hasColon := strings.Cut(key, ":")
		if !hasColon || dateBuckets[bucket] {
			grouped = append(grouped, key)
		} else {
			aggregated = append(aggregated, key)
		}
	}
	return append(grouped, aggregated...)
}

// Prompt is what the agent is asked before the run's data.
func Prompt(routine *Routine, result *Result, meanings map[string][]Meaning, language string) string {
	vi := language != "en"
	ask := routine.Prompt
	if ask == "" {
		ask = pick(vi, "Tóm tắt điều đáng chú ý.", "Summarise what deserves attention.")
	}
	area := routine.Area
	if area == "" {
		area = "-"
	}
	head := strings.NewReplacer("{title}", routine.Title, "{area}", area, "{schedule}", routine.Schedule(language),
		"{slot}", result.Slot, "{ran}", result.RanAt, "{source}", result.Source, "{ask}", ask).Replace(pick(vi, promptVI, promptEN))
	var steps []string
	for _, step := range routine.Steps {
		var fields []string
		for _, meaning := range meanings[step.Operation] {
			field := meaning.Name
			if meaning.ProviderField != "" && meaning.ProviderField != meaning.Name {
				field += " (= " + meaning.ProviderField + ")"
			}
			fields = append(fields, field+": "+meaning.Description)
		}
		line := fmt.Sprintf("- %s: %s %s. %s", step.Name, step.Kind, step.Operation, step.Note)
		if len(fields) > 0 {
			line += "\n  " + strings.Join(fields, "; ")
		}
		steps = append(steps, line)
	}
	return head + strings.Join(steps, "\n") + "\n\n" + pick(vi, "Dữ liệu", "Data") + " (JSON):\n"
}

// fit is the run as JSON within budget bytes. Rows are cut from the longest step first, and the cut is
// said, so the agent never mistakes a page for the whole.
func fit(result *Result, budget int, vi bool) string {
	copied := *result
	copied.Steps = append([]StepResult(nil), result.Steps...)
	copied.Agent = nil
	encode := func() string {
		data, _ := encodeJSON(&copied, false)
		return strings.TrimSuffix(string(data), "\n")
	}
	encoded := encode()
	for len(encoded) > budget {
		longest, size := -1, 0
		for index, step := range copied.Steps {
			if n := stepLength(step); n > size {
				longest, size = index, n
			}
		}
		if longest < 0 {
			cut := max(budget, 0) / 4
			if cut > len(encoded) {
				cut = len(encoded)
			}
			return encoded[:cut] + "\n[...]"
		}
		keep := size / 2
		step := &copied.Steps[longest]
		if step.Rows != nil && len(*step.Rows) > 0 {
			rows := (*step.Rows)[:keep]
			step.Rows = &rows
		} else {
			groups := (*step.Groups)[:keep]
			step.Groups = &groups
		}
		step.CutForPrompt = fmt.Sprintf(pick(vi, "chỉ giữ %d dòng đầu; xem result.json", "only the first %d kept; see result.json"), keep)
		encoded = encode()
	}
	return encoded + "\n"
}

func stepLength(step StepResult) int {
	if step.Rows != nil && len(*step.Rows) > 0 {
		return len(*step.Rows)
	}
	if step.Groups != nil {
		return len(*step.Groups)
	}
	return 0
}

// Normalize gives Odoo's answer the shapes a script reads from the command line's JSON: maps, lists,
// texts, booleans and json.Number, which keeps a whole number apart from one with a fraction.
func Normalize(value interface{}) (interface{}, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var out interface{}
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// Records reads normalized data as records: a list as is, one record as a list of one, nothing as none.
func Records(data interface{}) ([]map[string]interface{}, error) {
	switch typed := data.(type) {
	case nil:
		return []map[string]interface{}{}, nil
	case map[string]interface{}:
		return []map[string]interface{}{typed}, nil
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(typed))
		for _, item := range typed {
			record, isRecord := item.(map[string]interface{})
			if !isRecord {
				return nil, fmt.Errorf("routines: a record is an object, got %T", item)
			}
			out = append(out, record)
		}
		return out, nil
	}
	return nil, fmt.Errorf("routines: records are a list, got %T", data)
}

const promptVI = `Bạn là trợ lý phân tích vận hành của một doanh nghiệp dùng Odoo. Dưới đây là kết quả của việc định kỳ "{title}" (lĩnh vực: {area}; lịch: {schedule}; kỳ: {slot}; chạy lúc {ran}), lấy từ Odoo qua {source}.

Chủ doanh nghiệp muốn biết: {ask}

Quy tắc:
- Chỉ dựa trên dữ liệu bên dưới. Không bịa số, không đoán phần dữ liệu không có. Nhắc tới bản ghi nào thì ghi số chứng từ hoặc id của nó.
- Bước nào lỗi hoặc dữ liệu bị cắt (cutForPrompt), hoặc "complete" là false (còn bản ghi chưa lấy), thì nói rõ.
- Không đề nghị tự sửa Odoo. Việc cần làm thì liệt kê để người duyệt.
- Trả lời bằng tiếng Việt, Markdown, ngắn gọn: "Tóm tắt" (tối đa 3 dòng), "Cần xem ngay", "Việc đề xuất".

Các bước và ý nghĩa các trường Odoo trả về:
`

const promptEN = `You are the operations analyst of a business that runs Odoo. Below is the result of the routine "{title}" (area: {area}; schedule: {schedule}; slot: {slot}; ran at {ran}), read from Odoo through {source}.

The owner wants to know: {ask}

Rules:
- Use only the data below. Never invent a figure or guess at data that is not there. Name a record by its number or id.
- Say so when a step failed, when data was cut (cutForPrompt), or when "complete" is false (more records exist).
- Never propose changing Odoo yourself. List what should be done, for a person to approve.
- Answer in English, Markdown, briefly: "Summary" (3 lines at most), "Look at now", "Suggested actions".

The steps, and what the fields Odoo returns mean:
`
