package upgradecheck

import (
	"strings"
	"testing"
)

func TestMarkdownSaysEachFindingOnce(t *testing.T) {
	report := Report{From: "19.0", To: "20.0", BySeverity: map[string]int{"BREAKS": 3}, Scanned: map[string]int{"filters": 2, "defaults": 1},
		Findings: []Finding{
			{Kind: "saved filter", ID: 15, Name: "Tasks", Model: "project.task", Path: "allow_task_dependencies",
				Problem: "NO_LONGER_SEARCHABLE", Severity: "BREAKS", At: &Place{Model: "project.task", Field: "allow_task_dependencies"}},
			{Kind: "saved filter", ID: 16, Name: "Companies", Model: "res.partner", Path: "parent_id.company_type",
				Problem: "FIELD_MISSING", Severity: "BREAKS", At: &Place{Model: "res.partner", Field: "company_type"}},
			{Kind: "default value", ID: 49, Model: "res.company", Path: "company_registry",
				Problem: "FIELD_MISSING", Severity: "BREAKS", At: &Place{Model: "res.company", Field: "company_registry"}},
		}}
	text := Markdown(report, loadPair(t), "19.0-20260803")
	for _, want := range []string{
		"- **Bộ lọc đã lưu \"Tasks\"** (id 15, model `project.task`): ở 20.0 không còn lọc được theo `allow_task_dependencies`.\n",
		"- **Bộ lọc đã lưu \"Companies\"** (id 16, model `res.partner`): field `company_type` không còn trên model `res.partner` ở 20.0 (đường dẫn `parent_id.company_type`).\n",
		"- **Giá trị mặc định** (id 49, model `res.company`): field `company_registry` không còn trên model `res.company` ở 20.0.\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the report lacks %q:\n%s", want, text)
		}
	}
}
