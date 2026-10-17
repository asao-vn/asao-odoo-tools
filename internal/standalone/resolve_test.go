package standalone

import (
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// resolve finds by plain fields only: a filter on personal data or free text would tell whether a value
// is there, which is reading it.
func TestResolveRefusesAFilterOnDataItMayNotRead(t *testing.T) {
	for _, domain := range [][]interface{}{
		{[]interface{}{"phone", "=", "0905 111 222"}},
		{[]interface{}{"partner_id.email", "ilike", "@example.com"}},
		{[]interface{}{"comment", "ilike", "trợ lý"}},
		{[]interface{}{"vat", "=", "0101"}},
		{"|", []interface{}{"name", "=", "a"}, []interface{}{"street", "ilike", "Lê Duẩn"}},
		{[]interface{}{"name", "child_of", 1}},
		{"drop table"},
	} {
		if err := checkResolveFilter(domain); err == nil {
			t.Errorf("%v was accepted", domain)
		}
	}
	for _, domain := range [][]interface{}{
		{[]interface{}{"partner_id.name", "ilike", "Lê Thành"}, []interface{}{"state", "=", "draft"}},
		{"|", []interface{}{"name", "=", "S00012"}, []interface{}{"amount_total", ">", 1000000}},
		{[]interface{}{"invoice_date", ">=", "2026-07-01"}},
	} {
		if err := checkResolveFilter(domain); err != nil {
			t.Errorf("%v: %v", domain, err)
		}
	}
}

// At operate, resolve is offered beside the discovery commands, and what it answers passes untouched: ids.
func TestResolveIsOfferedAtOperateAndRefusesBeforeOdoo(t *testing.T) {
	kept := toolsAtLevel([]agentsurface.Tool{
		{Name: "resolve", Command: "odoo-cli resolve", AgentAccess: clicore.AgentAccessRead},
		{Name: "identity_contact_list", Command: "odoo-cli identity contact list", AgentAccess: clicore.AgentAccessRead},
	}, accessOperate)
	if len(kept) != 1 || kept[0].Name != "resolve" {
		t.Fatalf("tools at operate: %+v", kept)
	}
	t.Setenv("AERP_PROFILE_FILE", t.TempDir()+"/none.json")
	_, out, _ := runTrace(t, "resolve", "--operation", "identity.contact.list", "--filter", `[["phone","=","0905"]]`, "--output", "json")
	if !strings.Contains(string(out), "RESOLVE_FILTER_REFUSED") {
		t.Fatalf("a filter on phone: %s", out)
	}
	_, out, _ = runTrace(t, "resolve", "--operation", "sales.order.confirm", "--output", "json")
	if !strings.Contains(string(out), "RESOLVE_NOT_A_LIST") {
		t.Fatalf("a write named: %s", out)
	}
}
