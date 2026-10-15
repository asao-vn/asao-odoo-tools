package controls_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/controls"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/odoo/review"
	"github.com/asao-platform/atoc/packages/odoo/spec"
)

// fakeOdoo answers the reads controls and the spec engine make, from rows kept by operation; a
// describe answers the row whose id is the target.
type fakeOdoo struct {
	rows     map[string][]map[string]interface{}
	fail     map[string]error
	requests []direct.Request
}

func (f *fakeOdoo) Execute(_ context.Context, r direct.Request) (direct.Outcome, error) {
	f.requests = append(f.requests, r)
	if err := f.fail[r.Operation]; err != nil {
		return direct.Outcome{}, err
	}
	rows := f.rows[r.Operation]
	if r.ProviderTargetID != 0 {
		var one []map[string]interface{}
		for _, row := range rows {
			if fmt.Sprint(row["id"]) == fmt.Sprint(r.ProviderTargetID) {
				one = append(one, row)
			}
		}
		rows = one
	}
	if r.Operation == "structure.company.list" && len(r.Domain) == 1 {
		// The spec engine finds the company by id.
		want := fmt.Sprint(r.Domain[0].([]interface{})[2])
		var one []map[string]interface{}
		for _, row := range rows {
			if fmt.Sprint(row["id"]) == want {
				one = append(one, row)
			}
		}
		rows = one
	}
	data := make([]interface{}, len(rows))
	for i, row := range rows {
		data[i] = row
	}
	return direct.Outcome{Operation: r.Operation, Status: "OK", Data: map[string]interface{}{"data": data}}, nil
}

func settingsRow() map[string]interface{} {
	return map[string]interface{}{
		"po_order_approval": true, "po_double_validation_amount": 5000.0, "lock_confirmed_po": false,
		"group_auto_done_setting": false, "restrictive_audit_trail": true, "account_use_credit_limit": false,
		"account_default_credit_limit": 0.0, "auth_totp_policy": "all_required",
	}
}

func lab() *fakeOdoo {
	return &fakeOdoo{rows: map[string][]map[string]interface{}{
		"structure.settings.describe": {settingsRow()},
		"structure.company.list":      {{"id": 1.0, "name": "Minh An"}},
		"structure.company.describe": {{"id": 1.0, "name": "Minh An", "sale_lock_date": "2026-08-31",
			"purchase_lock_date": "2026-08-31", "tax_lock_date": false, "fiscalyear_lock_date": false, "hard_lock_date": false}},
		"finance.journal.list": {{"id": 1.0, "code": "INV", "name": "Bán hàng", "type": "sale"},
			{"id": 2.0, "code": "BILL", "name": "Mua hàng", "type": "purchase"}, {"id": 3.0, "code": "MISC", "name": "Tổng hợp", "type": "general"}},
		"finance.journal.describe": {{"id": 1.0, "restrict_mode_hash_table": true}, {"id": 2.0, "restrict_mode_hash_table": true},
			{"id": 3.0, "restrict_mode_hash_table": false}},
	}}
}

var today = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

func surface(t *testing.T, id string) odoo.Odoo19Operation {
	t.Helper()
	for _, op := range odoo.SurfaceCatalog() {
		if op.SemanticID == id {
			return op
		}
	}
	t.Fatalf("no surface operation %s", id)
	return odoo.Odoo19Operation{}
}

func has(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// Every control says, in both languages, what it prevents, what Odoo does and what changes; names
// only checks, patterns and fields that exist; and a control Odoo never lets be undone is never one
// odoo-cli applies.
func TestCatalogueIsSound(t *testing.T) {
	all, err := controls.Load()
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]bool{}
	for _, c := range review.Catalog() {
		checks[c.ID] = true
	}
	patterns, err := review.Patterns()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, p := range patterns.Patterns {
		known[p.ID] = true
	}
	settingsWrite, settingsRead := surface(t, "structure.settings.update"), surface(t, "structure.settings.describe")
	companyWrite, companyRead := surface(t, "structure.company.update"), surface(t, "structure.company.describe")
	journalRead := surface(t, "finance.journal.describe")
	for _, curated := range []string{"structure.company.list", "finance.journal.list"} {
		if _, ok := odoo.CatalogByID[curated]; !ok {
			t.Fatalf("controls read through %s, which the catalog does not have", curated)
		}
	}
	if !has(companyRead.OptionalReadFields, "hard_lock_date") || !has(journalRead.OptionalReadFields, "restrict_mode_hash_table") {
		t.Fatal("the company and journal reads must carry the hard lock date and the hash switch")
	}
	if has(companyWrite.AllowedFields, "hard_lock_date") || has(surface(t, "finance.journal.update").AllowedFields, "restrict_mode_hash_table") {
		t.Fatal("the catalog must never write the hard lock date or a journal's hash switch: Odoo never lets them be undone")
	}
	id := regexp.MustCompile(`^K\d\d$`)
	previous := ""
	for _, c := range all.Controls {
		where := c.ID
		if !id.MatchString(c.ID) || c.ID <= previous {
			t.Fatalf("control ids must be K01, K02, ... in order, got %s after %s", c.ID, previous)
		}
		previous = c.ID
		for name, text := range map[string]controls.Text{"title": c.Title, "prevents": c.Prevents, "odoo": c.Odoo, "changes": c.Changes} {
			if strings.TrimSpace(text["vi"]) == "" || strings.TrimSpace(text["en"]) == "" {
				t.Fatalf("%s: %s needs vi and en", where, name)
			}
		}
		if c.Enforced != controls.EnforcedServer && c.Enforced != controls.EnforcedWebClient && c.Enforced != controls.EnforcedWarning {
			t.Fatalf("%s: enforced %q", where, c.Enforced)
		}
		if len(c.Modules) == 0 || len(c.Source) == 0 {
			t.Fatalf("%s: modules and source are required", where)
		}
		if !c.Reversible && c.Applies {
			t.Fatalf("%s cannot be undone in Odoo, so odoo-cli must not apply it", where)
		}
		if !c.Applies && (c.Manual["vi"] == "" || c.Manual["en"] == "") {
			t.Fatalf("%s is not applied: say how to do it in Odoo", where)
		}
		if c.Applies && len(c.Settings) == 0 && len(c.CompanyFields) == 0 {
			t.Fatalf("%s applies but writes nothing", where)
		}
		for _, check := range c.Checks {
			if !checks[check] {
				t.Fatalf("%s names check %s, which the review does not have", where, check)
			}
		}
		for _, pattern := range c.Patterns {
			if !known[pattern] {
				t.Fatalf("%s names pattern %s, which the catalogue does not have", where, pattern)
			}
		}
		params := map[string]bool{}
		for _, p := range c.Params {
			params[p.Name] = true
			if p.Kind != "date" && p.Kind != "amount" || p.Description["vi"] == "" || p.Description["en"] == "" {
				t.Fatalf("%s: parameter %s needs a kind (date, amount) and a description", where, p.Name)
			}
		}
		for _, s := range c.Settings {
			if !has(settingsWrite.WizardAllowedFields, s.Field) || !has(settingsRead.OptionalReadFields, s.Field) {
				t.Fatalf("%s: setting %s is not one structure.settings.update and .describe offer", where, s.Field)
			}
			if (s.Param == "") == (s.Set == nil) || s.Param != "" && !params[s.Param] {
				t.Fatalf("%s: setting %s needs a value to set or a declared parameter", where, s.Field)
			}
		}
		for _, field := range c.CompanyFields {
			if !has(companyWrite.AllowedFields, field) || !has(companyRead.OptionalReadFields, field) {
				t.Fatalf("%s: company field %s is not one structure.company.update and .describe offer", where, field)
			}
		}
	}
}

func TestStatusReadsEachControl(t *testing.T) {
	report, err := controls.Read(context.Background(), lab(), controls.Options{Today: today})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	byID := map[string]controls.Status{}
	for _, s := range report.Controls {
		got[s.ID] = s.State
		byID[s.ID] = s
	}
	want := map[string]string{"K01": "ON", "K02": "OFF", "K03": "OFF", "K04": "PARTIAL", "K05": "ON", "K06": "PARTIAL", "K07": "OFF", "K08": "ON"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states %v, want %v", got, want)
	}
	if byID["K01"].Detail["po_double_validation_amount"] != 5000.0 {
		t.Fatalf("K01 detail %v: the amount is shown", byID["K01"].Detail)
	}
	if byID["K04"].Detail["lockedThrough"] != "2026-08-31" || byID["K04"].Reason != "" {
		t.Fatalf("K04 detail %v reason %q", byID["K04"].Detail, byID["K04"].Reason)
	}
	if report.Company == nil || report.Company.ID != 1 || report.Summary["ON"] != 3 {
		t.Fatalf("company %v summary %v", report.Company, report.Summary)
	}
	// A lock older than the month before last leaves months open: said, not hidden behind ON.
	old := lab()
	old.rows["structure.company.describe"][0]["sale_lock_date"] = "2026-05-31"
	report, _ = controls.Read(context.Background(), old, controls.Options{Today: today})
	for _, s := range report.Controls {
		if s.ID == "K04" && !strings.Contains(s.Reason, "every month since is open") {
			t.Fatalf("K04 reason %q", s.Reason)
		}
	}
}

func TestStatusWithoutTheModuleOrWithSeveralCompanies(t *testing.T) {
	f := lab()
	delete(f.rows["structure.settings.describe"][0], "po_order_approval")
	f.rows["structure.company.list"] = append(f.rows["structure.company.list"], map[string]interface{}{"id": 2.0, "name": "Chi nhánh"})
	report, err := controls.Read(context.Background(), f, controls.Options{Today: today})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range report.Controls {
		switch s.ID {
		case "K01":
			if s.State != controls.StateNotAvailable || !strings.Contains(s.Reason, "purchase") {
				t.Fatalf("K01 without purchase: %s %q", s.State, s.Reason)
			}
		case "K04":
			if s.State != controls.StateUnread || !strings.Contains(s.Reason, "--company") {
				t.Fatalf("K04 with two companies: %s %q", s.State, s.Reason)
			}
		}
	}
	if report.Company != nil || len(report.Companies) != 2 {
		t.Fatalf("company %v companies %v", report.Company, report.Companies)
	}
	report, _ = controls.Read(context.Background(), f, controls.Options{Today: today, CompanyID: 1})
	if report.Company == nil || report.Company.ID != 1 {
		t.Fatalf("--company 1 read %v", report.Company)
	}
	// A refused read is reported on the controls that need it; it does not end the status.
	refused := lab()
	refused.fail = map[string]error{"structure.settings.describe": &direct.OperationError{Operation: "structure.settings.describe", Class: "NOT_RELEASE_READY", Message: "UNVERIFIED"}}
	report, err = controls.Read(context.Background(), refused, controls.Options{Today: today})
	if err != nil || report.Summary[controls.StateUnread] != 6 {
		t.Fatalf("refused settings read: %v %v", err, report.Summary)
	}
}

func TestRenderGuardsTheLockDate(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		params map[string]string
		want   string
	}{
		{map[string]string{}, "needs the lock date"},
		{map[string]string{"K04.date": "30/09/2026"}, "must be a day"},
		{map[string]string{"K04.date": "2026-10-15"}, "not a past day"},
		{map[string]string{"K04.date": "2026-07-31"}, "never moves a lock date back"},
		{map[string]string{"K04.date": "2026-09-30", "K04.amount": "1"}, "has no parameter amount"},
		{map[string]string{"K04.date": "2026-09-30", "K01.amount": "1"}, "control not chosen"},
	} {
		_, _, _, _, err := controls.Render(ctx, lab(), controls.Selection{IDs: []string{"K04"}, Params: tc.params}, controls.Options{Today: today})
		if !errors.Is(err, controls.ErrSelection) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.params, err, tc.want)
		}
	}
	document, chosen, _, company, err := controls.Render(ctx, lab(), controls.Selection{IDs: []string{"k04"}, Params: map[string]string{"k04.date": "2026-09-30"}}, controls.Options{Today: today})
	if err != nil || len(chosen) != 1 || company == nil || company.ID != 1 {
		t.Fatalf("render: %v %v %v", err, chosen, company)
	}
	if len(document.Resources) != 1 || document.Resources[0].Entity != "structure.company" || document.Resources[0].Match["id"] != int64(1) {
		t.Fatalf("resources %+v", document.Resources)
	}
	for _, field := range []string{"sale_lock_date", "purchase_lock_date", "tax_lock_date", "fiscalyear_lock_date"} {
		if document.Resources[0].Values[field] != "2026-09-30" {
			t.Fatalf("%s = %v", field, document.Resources[0].Values[field])
		}
	}
	if _, has := document.Resources[0].Values["hard_lock_date"]; has {
		t.Fatal("the hard lock date is never set")
	}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRenderSettingsNeverWeakenAndHashIsManual(t *testing.T) {
	ctx := context.Background()
	document, chosen, manual, _, err := controls.Render(ctx, lab(), controls.Selection{IDs: []string{"K08", "K06", "K01", "K07"},
		Params: map[string]string{"K01.amount": "20_000_000"}}, controls.Options{Today: today})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{"auth_totp_policy": "all_required", "po_order_approval": true,
		"po_double_validation_amount": 2e7, "account_use_credit_limit": true}
	if !reflect.DeepEqual(document.Settings, want) {
		t.Fatalf("settings %v, want %v", document.Settings, want)
	}
	if len(chosen) != 3 || len(manual) != 1 || manual[0].ID != "K06" || manual[0].Steps["vi"] == "" {
		t.Fatalf("chosen %v manual %v", chosen, manual)
	}
	if len(document.Resources) != 0 {
		t.Fatal("K06 writes nothing")
	}
	if _, _, _, _, err := controls.Render(ctx, lab(), controls.Selection{IDs: []string{"K01"}, Params: map[string]string{"K01.amount": "-5"}}, controls.Options{}); !errors.Is(err, controls.ErrSelection) {
		t.Fatalf("a negative amount: %v", err)
	}
	if _, _, _, _, err := controls.Render(ctx, lab(), controls.Selection{IDs: []string{"K99"}}, controls.Options{}); !errors.Is(err, controls.ErrSelection) {
		t.Fatalf("an unknown control: %v", err)
	}
	f := lab()
	delete(f.rows["structure.settings.describe"][0], "lock_confirmed_po")
	if _, _, _, _, err := controls.Render(ctx, f, controls.Selection{IDs: []string{"K02"}}, controls.Options{}); err == nil || !strings.Contains(err.Error(), "needs module purchase") {
		t.Fatalf("K02 without purchase: %v", err)
	}
}

func TestPlanGoesThroughTheSpecEngine(t *testing.T) {
	plan, err := controls.PlanControls(context.Background(), lab(), controls.Selection{IDs: []string{"K01", "K02", "K04", "K06"},
		Params: map[string]string{"K04.date": "2026-09-30"}}, controls.Options{Today: today})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]spec.Action{}
	for _, step := range plan.Plan.Steps {
		actions[step.Entity] = step.Action
		if step.Entity == "settings" && (step.Changes["lock_confirmed_po"].To != true || len(step.Changes) != 1) {
			t.Fatalf("settings changes %v: only K02's switch differs", step.Changes)
		}
	}
	if actions["settings"] != spec.ActionUpdate || actions["structure.company"] != spec.ActionUpdate || plan.Plan.Hash == "" {
		t.Fatalf("plan %+v", plan.Plan)
	}
	if len(plan.Manual) != 1 || plan.Company == nil {
		t.Fatalf("manual %v company %v", plan.Manual, plan.Company)
	}
	// Nothing but the manual control: no step, nothing to apply.
	plan, err = controls.PlanControls(context.Background(), lab(), controls.Selection{IDs: []string{"K06"}}, controls.Options{Today: today})
	if err != nil || len(plan.Plan.Steps) != 0 || len(plan.Manual) != 1 {
		t.Fatalf("K06 alone: %v %+v", err, plan)
	}
}

func TestReviewChecksKnowTheirControls(t *testing.T) {
	if got := controls.PreventedBy("O16"); !reflect.DeepEqual(got, []string{"K01"}) {
		t.Fatalf("O16 prevented by %v", got)
	}
	if got := controls.PreventedBy("O26"); !reflect.DeepEqual(got, []string{"K04"}) {
		t.Fatalf("O26 prevented by %v", got)
	}
	if got := controls.PreventedBy("O01"); got != nil {
		t.Fatalf("O01 prevented by %v", got)
	}
}
