package standalone_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// A team's own check: refunds piling up on one customer, three credit notes within 30 days. It is a groups
// match the built-in checks do not have, written as a file, with its cases.
const refundsPilingUp = `{
  "schema": "odoo-cli.check/v1",
  "id": "X-CLAIMS-PILING-UP",
  "version": 1,
  "name": "Chi phí dồn dập",
  "question": "Có ai kê nhiều khoản chi trong vài ngày không?",
  "kind": "dấu hiệu gian lận",
  "formula": "min_count claims or more of one employee within window_days",
  "params": {"window_days": 7, "min_count": 3},
  "source": {"operation": "people.expense.list", "period": "date", "where": [["state", "not in", ["draft", "refused"]]]},
  "match": {"kind": "groups", "key": ["employee_id.id"], "date": "date", "order": ["date", "id"], "within": "window_days",
            "aggregate": "count", "threshold": "min_count"},
  "severity": [{"if": "count >= 5", "then": "NEN_XU_LY"}, {"then": "THEO_DOI"}],
  "finding": {"title": "Nhiều khoản chi trong một tuần",
              "subject": {"type": "employee", "id": "{first.employee_id.id}", "name": "{first.employee_id.name}"},
              "why": "{first.employee_id.name}: {count} khoản chi từ {from} đến {to}, tổng {money(total)}.",
              "amount": "total", "facts": {"claims": "count"}},
  "cases": [
    {"name": "three claims in a week", "records": {"people.expense.list": [
      {"id": 1, "name": "a", "employee_id": [7, "Hùng"], "date": "2026-07-01", "total_amount": 100000, "state": "approved"},
      {"id": 2, "name": "b", "employee_id": [7, "Hùng"], "date": "2026-07-03", "total_amount": 200000, "state": "approved"},
      {"id": 3, "name": "c", "employee_id": [7, "Hùng"], "date": "2026-07-06", "total_amount": 300000, "state": "approved"}]},
     "expect": [{"evidence": [1, 2, 3], "severity": "THEO_DOI"}]},
    {"name": "three claims over a month", "records": {"people.expense.list": [
      {"id": 4, "name": "d", "employee_id": [7, "Hùng"], "date": "2026-07-01", "total_amount": 100000, "state": "approved"},
      {"id": 5, "name": "e", "employee_id": [7, "Hùng"], "date": "2026-07-15", "total_amount": 100000, "state": "approved"},
      {"id": 6, "name": "f", "employee_id": [7, "Hùng"], "date": "2026-07-30", "total_amount": 100000, "state": "approved"}]},
     "expect": []}
  ]
}`

func TestATeamsOwnCheckIsTestedAddedRunAndJudged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "X-CLAIMS-PILING-UP.json")
	if err := os.WriteFile(path, []byte(refundsPilingUp), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, diag := runOdoo(t, "review", "test-check", "--file", path, "--output", "json"); code != clicore.ExitOK || decode(t, out)["passed"] != true {
		t.Fatalf("test-check: %d %s %s", code, out, diag)
	}
	broken := filepath.Join(dir, "broken.json")
	_ = os.WriteFile(broken, []byte(strings.Replace(refundsPilingUp, `"severity": "THEO_DOI"}]}`, `"severity": "XEM_NGAY"}]}`, 1)), 0o600)
	if code, out, _ := runOdoo(t, "review", "test-check", "--file", broken, "--output", "json"); code == clicore.ExitOK || !strings.Contains(out.String(), "CHECK_CASES_FAILED") {
		t.Fatalf("a failing case passed: %d %s", code, out)
	}

	server := httptest.NewServer(expenseOdoo(t))
	defer server.Close()
	writeTestProfile(t, server.URL)
	if code, out, diag := runOdoo(t, "review", "add-check", "--profile", "lab-a", "--file", path, "--output", "json"); code != clicore.ExitOK {
		t.Fatalf("add-check: %d %s %s", code, out, diag)
	}
	code, out, diag := runOdoo(t, "review", "run", "--profile", "lab-a", "--from", "2026-07-01", "--to", "2026-07-31", "--check", "X-CLAIMS-PILING-UP", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("review run: %d %s %s", code, out, diag)
	}
	report := decode(t, out)
	findings := report["findings"].([]interface{})
	if len(findings) != 1 {
		t.Fatalf("findings: %s", out)
	}
	key := findings[0].(map[string]interface{})["key"].(string)
	if !strings.HasPrefix(key, "X-CLAIMS-PILING-UP-") {
		t.Fatalf("key %q", key)
	}
	if code, out, _ := runOdoo(t, "review", "verdict", "--profile", "lab-a", "--finding", key, "--false-alarm", "--note", "a site trip", "--output", "json"); code != clicore.ExitOK {
		t.Fatalf("verdict: %d %s", code, out)
	}
	_, out, _ = runOdoo(t, "review", "run", "--profile", "lab-a", "--from", "2026-07-01", "--to", "2026-07-31", "--check", "X-CLAIMS-PILING-UP", "--output", "json")
	if verdict := decode(t, out)["findings"].([]interface{})[0].(map[string]interface{})["verdict"]; verdict != "false" {
		t.Fatalf("the verdict is not on the finding: %v", verdict)
	}
	_, out, _ = runOdoo(t, "review", "precision", "--profile", "lab-a", "--output", "json")
	tally := decode(t, out)["checks"].([]interface{})[0].(map[string]interface{})
	if tally["check"] != "X-CLAIMS-PILING-UP" || tally["falseAlarms"] != 1.0 || tally["precision"] != 0.0 {
		t.Fatalf("precision: %v", tally)
	}
	if code, _, _ := runOdoo(t, "review", "remove-check", "--profile", "lab-a", "--check", "X-CLAIMS-PILING-UP"); code != clicore.ExitOK {
		t.Fatalf("remove-check: %d", code)
	}
}

// expenseOdoo answers a 19.0 login, an expense module, and four claims of one employee in July.
func expenseOdoo(t *testing.T) http.HandlerFunc {
	claims := []interface{}{
		map[string]interface{}{"id": 11, "name": "Xăng", "employee_id": []interface{}{7, "Lê Văn Hùng"}, "date": "2026-07-02", "total_amount": 210000, "state": "approved"},
		map[string]interface{}{"id": 12, "name": "Gửi xe", "employee_id": []interface{}{7, "Lê Văn Hùng"}, "date": "2026-07-03", "total_amount": 30000, "state": "approved"},
		map[string]interface{}{"id": 13, "name": "Tiếp khách", "employee_id": []interface{}{7, "Lê Văn Hùng"}, "date": "2026-07-05", "total_amount": 900000, "state": "approved"},
		map[string]interface{}{"id": 14, "name": "Xăng", "employee_id": []interface{}{8, "Trần Văn Bình"}, "date": "2026-07-20", "total_amount": 180000, "state": "approved"},
	}
	return odoo19(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Params struct {
				Service string        `json:"service"`
				Args    []interface{} `json:"args"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&call)
		answer := func(result interface{}) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": result})
		}
		if call.Params.Service == "common" {
			answer(1)
			return
		}
		model, method := call.Params.Args[3].(string), call.Params.Args[4].(string)
		switch {
		case model == "ir.module.module":
			answer([]interface{}{map[string]interface{}{"name": "base", "state": "installed", "latest_version": "19.0.1.3"},
				map[string]interface{}{"name": "hr_expense", "state": "installed", "latest_version": "19.0.2.0"}})
		case model == "hr.expense" && method == "search_read":
			offset := 0.0
			if kwargs, ok := call.Params.Args[6].(map[string]interface{}); ok {
				offset, _ = kwargs["offset"].(float64)
			}
			if offset > 0 {
				answer([]interface{}{})
				return
			}
			answer(claims)
		case method == "fields_get":
			answer(map[string]interface{}{})
		case method == "search_count":
			answer(0)
		default:
			answer([]interface{}{})
		}
	})
}
