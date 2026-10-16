package upgradecheck

import (
	"fmt"
	"sort"
	"strings"
)

// SeverityMeanings say what each severity asks of the person who reads a report.
var SeverityMeanings = map[string]string{
	"BREAKS":  "stops working on the target series: fix it before or right after the upgrade",
	"RENAME":  "stops working on the target series; the field was probably renamed, to the name given",
	"CHECK":   "still runs on the target series, but may now mean something else: read it",
	"REVIEW":  "could not be read: a person reads it",
	"UNKNOWN": "outside what the map covers: an Enterprise or third-party module, or one the target lab did not install",
	"INFO":    "already broken on the source, before any upgrade",
}

// NotChecked is what a check does not look at.
var NotChecked = []string{
	"views without a model: QWeb report templates and website pages; ir.actions.report",
	"whether the view an inheriting view extends still exists on the target series",
	"Python code that builds field or model names at run time",
	"behaviour: a computed field or a workflow step whose meaning changed",
	"data: records that hold a selection value the target series dropped (the migration converts them)",
	"modules' own code, which OpenUpgrade and Odoo's upgrade scripts migrate",
}

var kindsVietnamese = map[string]string{
	"saved filter": "Bộ lọc đã lưu", "automation rule": "Quy tắc tự động", "server action": "Hành động máy chủ",
	"custom field": "Field tự tạo", "email template": "Mẫu email", "view": "Giao diện (view)", "record rule": "Quy tắc bản ghi",
	"access right": "Quyền truy cập", "export template": "Mẫu xuất dữ liệu", "default value": "Giá trị mặc định",
	"window action": "Hành động cửa sổ",
}

var keysVietnamese = map[string]string{
	"filters": "bộ lọc đã lưu", "automations": "quy tắc tự động", "server_actions": "hành động máy chủ", "custom_fields": "field tự tạo",
	"templates": "mẫu email", "views": "giao diện", "rules": "quy tắc bản ghi", "access_rights": "quyền truy cập",
	"exports": "mẫu xuất dữ liệu", "defaults": "giá trị mặc định", "window_actions": "hành động cửa sổ",
}

func code(value string) string { return "`" + value + "`" }

func quoted(values []string) string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = code(value)
	}
	return strings.Join(out, ", ")
}

// Explain says, in Vietnamese, what a finding means.
func Explain(f Finding, from, to string) string {
	field, model := "", ""
	if f.At != nil {
		field, model = f.At.Field, f.At.Model
	}
	switch f.Problem {
	case "FIELD_MISSING":
		text := fmt.Sprintf("field %s không còn trên model %s ở %s", code(field), code(model), to)
		if len(f.Candidates) > 0 {
			text += "; field gần giống: " + quoted(f.Candidates) + " (đã đổi cả giá trị hoặc cách ghi, cần xem)"
		}
		return text
	case "MODEL_MISSING":
		return fmt.Sprintf("model %s không còn ở %s", code(model), to)
	case "NOT_RELATIONAL":
		return fmt.Sprintf("%s không còn là field quan hệ ở %s", code(field), to)
	case "RELATION_CHANGED":
		return fmt.Sprintf("field %s giờ trỏ tới model khác: %s thành %s", code(field), code(f.Was), code(f.Now))
	case "NO_LONGER_SEARCHABLE":
		return fmt.Sprintf("ở %s không còn lọc được theo %s", to, code(field))
	case "NO_LONGER_STORED":
		return fmt.Sprintf("ở %s không còn nhóm được theo %s: field không còn lưu trong cơ sở dữ liệu", to, code(field))
	case "ORDER_NOT_STORED":
		return fmt.Sprintf("sắp xếp theo %s, field không còn lưu trong cơ sở dữ liệu ở %s; Odoo vẫn chạy, cần xem thứ tự", code(field), to)
	case "DEFAULT_VALUE_REMOVED":
		return fmt.Sprintf("giá trị mặc định %s không còn trong danh sách lựa chọn của %s ở %s", quoted(f.Values), code(field), to)
	case "RELATED_TYPE_CHANGED":
		return fmt.Sprintf("field liên kết tới %s, field này đổi kiểu %s thành %s; Odoo không nhận field liên kết khác kiểu", code(field), f.Was, f.Now)
	case "CONTAINER_REPLACED":
		return fmt.Sprintf("%s không còn ở %s, thay bằng %s: phải viết lại", code(f.Path), to, code(f.ReplacedBy))
	case "MODULE_REMOVED":
		return fmt.Sprintf("module %s không còn ở %s", quoted(f.Modules), to)
	case "FIELD_RENAMED":
		return fmt.Sprintf("field %s của %s có lẽ đã đổi tên thành %s", code(field), code(model), code(f.To))
	case "SELECTION_VALUE_REMOVED":
		return fmt.Sprintf("giá trị %s của %s không còn ở %s: điều kiện này sẽ không khớp bản ghi nào", quoted(f.Values), code(field), to)
	case "TYPE_CHANGED":
		return fmt.Sprintf("field %s đổi kiểu %s thành %s", code(field), f.Was, f.Now)
	case "UNPARSABLE":
		return "không đọc được " + strings.Trim(f.Path, "()") + ": cần người xem"
	case "NOT_IN_TARGET_LAB":
		text := fmt.Sprintf("lab %s chưa cài module định nghĩa %s", to, code(strings.TrimSuffix(model+"."+field, ".")))
		if len(f.Modules) > 0 {
			text += " (" + quoted(f.Modules) + ")"
		}
		return text
	case "NOT_COVERED":
		return fmt.Sprintf("module %s nằm ngoài bản đồ (Enterprise hoặc bên thứ ba): bản kiểm tra không kết luận được", quoted(f.Modules))
	case "ALREADY_BROKEN_ON_SOURCE":
		return fmt.Sprintf("ngay trên %s đã không tìm thấy %s", from, code(strings.TrimSuffix(model+"."+field, ".")))
	}
	return f.Problem
}

// Markdown is the report a person reads, in Vietnamese.
func Markdown(report Report, pair *Pair, serverVersion string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Kiểm tra trước khi nâng cấp Odoo %s lên %s\n\n", report.From, report.To)
	fmt.Fprintf(&b, "Cơ sở dữ liệu: Odoo %s. Bản đồ nâng cấp lập từ hai lab Odoo Community có dữ liệu mẫu: %s và %s.\n\n",
		serverVersion, pair.SourceLab.ServerVersion, pair.TargetLab.ServerVersion)
	var keys []string
	total := 0
	for key, count := range report.Scanned {
		if key == "export_lines" || count == 0 {
			continue
		}
		keys = append(keys, key)
		total += count
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", report.Scanned[key], keysVietnamese[key]))
	}
	fmt.Fprintf(&b, "Đã đọc %d tùy biến do người tạo", total)
	if len(parts) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(parts, ", "))
	}
	b.WriteString(".\n\n| Mức | Số | Nghĩa |\n|---|---:|---|\n")
	meanings := map[string]string{
		"BREAKS": "Sẽ hỏng sau khi nâng cấp", "RENAME": "Sẽ hỏng; field có lẽ đã đổi tên (có ghi tên mới)",
		"CHECK": "Vẫn chạy nhưng có thể đổi nghĩa", "REVIEW": "Không đọc được, cần người xem",
		"UNKNOWN": "Ngoài phạm vi bản đồ", "INFO": "Đã hỏng từ trước khi nâng cấp",
	}
	for _, name := range Severities {
		fmt.Fprintf(&b, "| %s | %d | %s |\n", name, report.BySeverity[name], meanings[name])
	}
	sections := []struct {
		title      string
		severities []string
	}{
		{"Sẽ hỏng", []string{"BREAKS", "RENAME"}},
		{"Cần xem lại", []string{"CHECK", "REVIEW"}},
		{"Không đủ dữ liệu để kết luận", []string{"UNKNOWN"}},
		{"Đã hỏng từ trước", []string{"INFO"}},
	}
	for _, section := range sections {
		var lines []string
		for _, f := range report.Findings {
			if !contains(section.severities, f.Severity) {
				continue
			}
			title := kindsVietnamese[f.Kind]
			if title == "" {
				title = f.Kind
			}
			// A default value has no name of its own; its id and field say which one it is.
			if f.Name != "" {
				title += fmt.Sprintf(" \"%s\"", f.Name)
			}
			where := ""
			if f.Model != "" {
				where = ", model " + code(f.Model)
			}
			// The explanation names the field; the path is added when it says more, as a chain does.
			explain := Explain(f, report.From, report.To)
			if f.Path != "" && !strings.Contains(explain, code(f.Path)) {
				explain += " (đường dẫn " + code(f.Path) + ")"
			}
			lines = append(lines, fmt.Sprintf("- **%s** (id %d%s): %s.", title, f.ID, where, explain))
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", section.title, strings.Join(lines, "\n"))
	}
	if len(report.Findings) == 0 {
		b.WriteString("\nKhông thấy tham chiếu nào sẽ hỏng.\n")
	}
	b.WriteString("\n## Bản kiểm tra này không xem\n\n")
	for _, item := range []string{
		"Giao diện không gắn model: mẫu báo cáo QWeb, trang website; các báo cáo (ir.actions.report).",
		"View gốc mà một view kế thừa có còn ở bản mới không.",
		"Code Python ghép tên field hay model lúc chạy.",
		"Hành vi: field tính toán hay bước quy trình đổi nghĩa.",
		"Dữ liệu: bản ghi đang giữ giá trị lựa chọn đã bị bỏ (quá trình nâng cấp chuyển đổi).",
		"Code của các module: việc của OpenUpgrade và script nâng cấp của Odoo.",
	} {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	b.WriteString("\nĐây là dấu hiệu cần xem lại, không phải phán quyết. Hãy thử nâng cấp trên một bản sao của cơ sở dữ liệu trước.\n")
	return b.String()
}
