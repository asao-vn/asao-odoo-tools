package standalone

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func reportTestHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AERP_PROFILE_FILE", "")
	dir := filepath.Join(home, ".config", "aerp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	profiles := `{"version":"aerp.profiles/v1","defaultProfile":"shop","profiles":{"shop":{"endpoint":"https://erp.example-shop.vn","database":"shop_live","login":"owner@example-shop.vn","credentialRef":"shop-key"}}}`
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(profiles), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runReport(t *testing.T, args ...string) map[string]interface{} {
	t.Helper()
	var out, diag bytes.Buffer
	code := clicore.Run(context.Background(), RootFor("odoo-cli"), append(args, "--output", "json"), clicore.Streams{Out: &out, Diagnostics: &diag})
	var envelope map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("exit %d, not JSON: %s %s", code, out.String(), diag.String())
	}
	return envelope
}

func TestReportDraftScrubsAndSendsNothing(t *testing.T) {
	reportTestHome(t)
	envelope := runReport(t, "report", "draft", "--kind", "BUG",
		"--command", "odoo-cli sales order list --partner Nguyen Van A",
		"--error-code", "INTERNAL_ERROR",
		"--summary", "panics on shop_live",
		"--observed", "GET https://erp.example-shop.vn/json/2 as owner@example-shop.vn password=hunter2 from 192.168.1.20, customer 0912 345 678, key 0123456789abcdef0123456789abcdef",
		"--workaround", "used explore")
	if envelope["status"] != "OK" {
		t.Fatalf("draft refused: %v", envelope["error"])
	}
	data := envelope["data"].(map[string]interface{})
	if data["sent"] != false || data["status"] != "DRAFT_NOT_SENT" {
		t.Fatalf("a draft must say it sent nothing: %v", data)
	}
	text := data["title"].(string) + data["body"].(string) + data["fileIssueURL"].(string)
	for _, leaked := range []string{"example-shop", "shop_live", "hunter2", "192.168.1.20", "0912", "0123456789abcdef", "Nguyen", "--partner"} {
		if strings.Contains(text, leaked) {
			t.Errorf("report leaks %q:\n%s", leaked, text)
		}
	}
	if !strings.Contains(data["body"].(string), "`odoo-cli sales order list`") {
		t.Errorf("command path lost: %s", data["body"])
	}
	consent := data["consent"].(map[string]interface{})
	if consent["required"] != true {
		t.Errorf("consent must be required: %v", consent)
	}
}

func TestReportFingerprintIsStableAcrossReporters(t *testing.T) {
	reportTestHome(t)
	first := runReport(t, "report", "draft", "--kind", "BUG", "--command", "sales order list", "--error-code", "INTERNAL_ERROR", "--summary", "one wording")
	second := runReport(t, "report", "draft", "--kind", "BUG", "--command", "sales order list", "--error-code", "INTERNAL_ERROR", "--summary", "another wording", "--agent", "codex")
	a := first["data"].(map[string]interface{})["fingerprint"]
	b := second["data"].(map[string]interface{})["fingerprint"]
	if a != b {
		t.Fatalf("same command and code must share a fingerprint: %v vs %v", a, b)
	}
}

func TestReportRefusesAMessageAsErrorCode(t *testing.T) {
	reportTestHome(t)
	envelope := runReport(t, "report", "draft", "--kind", "BUG", "--summary", "x", "--error-code", "connection to erp.example-shop.vn refused")
	if envelope["status"] != "ERROR" || envelope["error"].(map[string]interface{})["code"] != "REPORT_ERROR_CODE_INVALID" {
		t.Fatalf("want REPORT_ERROR_CODE_INVALID, got %v", envelope)
	}
}

func TestInternalErrorsPointAtReportDraft(t *testing.T) {
	advise := reportAdvisor("odoo-cli")
	invocation := &clicore.Invocation{Command: &clicore.Command{Name: "list"}}

	internal := clicore.NewError(clicore.CategoryInternal, "INTERNAL_ERROR", "boom")
	advise(invocation, internal)
	hint, ok := internal.Context["report"].(map[string]interface{})
	if !ok || !strings.Contains(hint["draft"].(string), "report draft --kind BUG") {
		t.Fatalf("an internal error must carry the report hint: %v", internal.Context)
	}
	if internal.Code != "INTERNAL_ERROR" || internal.Category != clicore.CategoryInternal {
		t.Fatalf("the advisor changed the refusal: %+v", internal)
	}

	usage := clicore.UsageError("BAD_FLAG", "no")
	advise(invocation, usage)
	if _, ok := usage.Context["report"]; ok {
		t.Fatal("a usage refusal is the caller's to fix, not a tool fault to report")
	}
}

func TestReportIsOfferedToAgentsReadOnly(t *testing.T) {
	leaf := findPath(RootFor("odoo-cli"), []string{"report", "draft"})
	if leaf == nil || leaf.AgentAccess != clicore.AgentAccessRead || leaf.SideEffect != clicore.EffectNone {
		t.Fatalf("report draft must be a READ tool with no side effect: %+v", leaf)
	}
	if findPath(RootFor(Program), []string{"report"}) != nil {
		t.Fatal("the historical aerp tree does not file reports against the odoo-cli repository")
	}
}
