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
	var rows []map[string]interface{}
	for _, row := range f.rows[r.Operation] {
		if (r.ProviderTargetID == 0 || fmt.Sprint(row["id"]) == fmt.Sprint(r.ProviderTargetID)) && matches(row, r.Domain) {
			rows = append(rows, row)
		}
	}
	data := make([]interface{}, len(rows))
	for i, row := range rows {
		data[i] = row
	}
	return direct.Outcome{Operation: r.Operation, Status: "OK", Data: map[string]interface{}{"data": data}}, nil
}

// matches applies the domain leaves the controls and the spec engine send: =, != and in, on a value
// or on a many2one's id.
func matches(row map[string]interface{}, domain []interface{}) bool {
	for _, item := range domain {
		leaf, ok := item.([]interface{})
		if !ok || len(leaf) != 3 {
			continue
		}
		have := row[fmt.Sprint(leaf[0])]
		if pair, isPair := have.([]interface{}); isPair && len(pair) == 2 {
			have = pair[0]
		}
		switch leaf[1] {
		case "=":
			if fmt.Sprint(have) != fmt.Sprint(leaf[2]) {
				return false
			}
		case "!=":
			if fmt.Sprint(have) == fmt.Sprint(leaf[2]) {
				return false
			}
		case "in":
			found := false
			values := reflect.ValueOf(leaf[2])
			for i := 0; i < values.Len(); i++ {
				found = found || fmt.Sprint(values.Index(i).Interface()) == fmt.Sprint(have)
			}
			if !found {
				return false
			}
		}
	}
	return true
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
		"finance.journal.list": {{"id": 1.0, "code": "INV", "name": "Bán hàng", "type": "sale", "company_id": []interface{}{1.0, "Minh An"}},
			{"id": 2.0, "code": "BILL", "name": "Mua hàng", "type": "purchase", "company_id": []interface{}{1.0, "Minh An"}},
			{"id": 3.0, "code": "MISC", "name": "Tổng hợp", "type": "general", "company_id": []interface{}{1.0, "Minh An"}}},
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
		if c.Enforced != controls.EnforcedServer && c.Enforced != controls.EnforcedWebClient && c.Enforced != controls.EnforcedWarning &&
			c.Enforced != controls.EnforcedNotify {
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
		if (c.Alert != nil) != (c.Enforced == controls.EnforcedNotify) {
			t.Fatalf("%s: an alert, and only an alert, is enforced as NOTIFY", where)
		}
		if a := c.Alert; a != nil && (a.Model == "" || len(a.Fields) == 0 || a.Summary["vi"] == "" || a.Summary["en"] == "" ||
			!strings.HasPrefix(c.RuleName(), "odoo-cli: ")) {
			t.Fatalf("%s: an alert needs a model, its fields, a summary in vi and en, and a name odoo-cli's operations reach", where)
		}
		if c.Applies && len(c.Settings) == 0 && len(c.CompanyFields) == 0 && c.Alert == nil {
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
			if p.Kind != "date" && p.Kind != "amount" && p.Kind != "user" && p.Kind != "choice" || p.Description["vi"] == "" || p.Description["en"] == "" {
				t.Fatalf("%s: parameter %s needs a kind (date, amount, user, choice) and a description", where, p.Name)
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
	want := map[string]string{"K01": "ON", "K02": "OFF", "K03": "OFF", "K04": "PARTIAL", "K05": "ON", "K06": "PARTIAL", "K07": "OFF", "K08": "ON",
		"K09": "OFF", "K10": "OFF", "K11": "OFF"}
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

// alertLab adds what the alert reads find: the watched models and fields (20.0's account_number for a
// bank account's number), the activity types, and the rules and actions it may hold.
func alertLab() *fakeOdoo {
	f := lab()
	f.rows["structure.registry_model.list"] = []map[string]interface{}{
		{"id": 81.0, "model": "res.partner.bank", "is_mail_activity": true},
		{"id": 82.0, "model": "account.move", "is_mail_activity": true},
		{"id": 83.0, "model": "res.company", "is_mail_activity": false}}
	f.rows["structure.registry_field.list"] = []map[string]interface{}{
		{"id": 901.0, "model": "res.partner.bank", "name": "account_number"}, {"id": 902.0, "model": "res.partner.bank", "name": "partner_id"},
		{"id": 903.0, "model": "res.partner.bank", "name": "allow_out_payment"}, {"id": 904.0, "model": "account.move", "name": "state"}}
	f.rows["structure.activity_type.list"] = []map[string]interface{}{{"id": 4.0, "name": "To-Do", "category": "default", "res_model": false}}
	return f
}

func TestAlertsPlanARuleAndItsActivity(t *testing.T) {
	ctx := context.Background()
	document, chosen, _, _, err := controls.Render(ctx, alertLab(), controls.Selection{IDs: []string{"K09"},
		Params: map[string]string{"K09.user": "Ngọc Lan", "K09.lang": "en"}}, controls.Options{Today: today})
	if err != nil || len(chosen) != 1 || len(document.Resources) != 2 {
		t.Fatalf("render: %v %v %+v", err, chosen, document.Resources)
	}
	rule, action := document.Resources[0], document.Resources[1]
	if rule.Entity != "structure.alert_rule" || rule.Values["model_id"] != int64(81) || rule.Values["trigger"] != "on_create_or_write" ||
		!reflect.DeepEqual(rule.Values["trigger_field_ids"], []interface{}{int64(901), int64(902), int64(903)}) || rule.Values["active"] != true {
		t.Fatalf("rule %+v", rule)
	}
	if !strings.HasPrefix(rule.Values["name"].(string), "odoo-cli: K09 ") || action.Values["base_automation_id"] != rule.Values["name"] {
		t.Fatalf("names %v %v", rule.Values["name"], action.Values["base_automation_id"])
	}
	if action.Entity != "structure.alert_action" || action.Values["activity_type_id"] != int64(4) || action.Values["activity_user_id"] != "Ngọc Lan" ||
		!strings.HasPrefix(action.Values["activity_summary"].(string), "A bank account was added or changed") {
		t.Fatalf("action %+v", action)
	}
	if document.Resources[0].Values["filter_domain"] != nil {
		t.Fatal("K09 watches every bank account: no filter")
	}
	// A user given by id is an id; K10 carries the before and after filters.
	document, _, _, _, err = controls.Render(ctx, alertLab(), controls.Selection{IDs: []string{"K10"},
		Params: map[string]string{"K10.user": "7"}}, controls.Options{Today: today})
	if err != nil || document.Resources[1].Values["activity_user_id"] != int64(7) ||
		document.Resources[0].Values["filter_pre_domain"] != "[('state', '=', 'posted')]" || document.Resources[0].Values["filter_domain"] != "[('state', '=', 'draft')]" {
		t.Fatalf("K10: %v %+v", err, document.Resources)
	}
	for _, tc := range []struct {
		f      *fakeOdoo
		params map[string]string
		want   string
	}{
		{alertLab(), map[string]string{}, "needs the person"},
		{alertLab(), map[string]string{"K09.user": "x", "K09.lang": "fr"}, "must be vi or en"},
		{func() *fakeOdoo {
			f := alertLab()
			f.rows["structure.registry_field.list"] = f.rows["structure.registry_field.list"][1:]
			return f
		}(),
			map[string]string{"K09.user": "x"}, "acc_number or account_number"},
		{func() *fakeOdoo {
			f := alertLab()
			f.rows["structure.registry_model.list"][0]["is_mail_activity"] = false
			return f
		}(),
			map[string]string{"K09.user": "x"}, "cannot carry activities"},
	} {
		_, _, _, _, err := controls.Render(ctx, tc.f, controls.Selection{IDs: []string{"K09"}, Params: tc.params}, controls.Options{Today: today})
		if !errors.Is(err, controls.ErrSelection) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.params, err, tc.want)
		}
	}
}

func TestAlertStatusAndAPlanOfAnAlertAlreadyOn(t *testing.T) {
	ctx := context.Background()
	f := alertLab()
	name := "odoo-cli: K09 Alert when a bank account is added or changed"
	f.rows["structure.alert_rule.list"] = []map[string]interface{}{{"id": 5.0, "name": name, "active": true, "model_id": []interface{}{81.0, "Bank Accounts"}}}
	f.rows["structure.alert_rule.describe"] = []map[string]interface{}{{"id": 5.0, "name": name, "active": true, "model_id": []interface{}{81.0, "Bank Accounts"},
		"trigger": "on_create_or_write", "trigger_field_ids": []interface{}{903.0, 901.0, 902.0}}}
	action := map[string]interface{}{"id": 9.0, "name": name, "model_id": []interface{}{81.0, "Bank Accounts"}, "base_automation_id": []interface{}{5.0, name},
		"activity_type_id": []interface{}{4.0, "To-Do"}, "activity_user_id": []interface{}{7.0, "Ngọc Lan"},
		"activity_summary":             "Tài khoản ngân hàng vừa được thêm hay đổi: gọi đối tác xác nhận trước khi chi",
		"activity_date_deadline_range": 0.0, "activity_date_deadline_range_type": "days"}
	f.rows["structure.alert_action.list"] = []map[string]interface{}{action}
	f.rows["structure.alert_action.describe"] = []map[string]interface{}{action}
	report, err := controls.Read(ctx, f, controls.Options{Today: today})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range report.Controls {
		if s.ID == "K09" && (s.State != controls.StateOn || fmt.Sprint(s.Detail["user"]) != "[7 Ngọc Lan]") {
			t.Fatalf("K09 %s %v", s.State, s.Detail)
		}
	}
	plan, err := controls.PlanControls(ctx, f, controls.Selection{IDs: []string{"K09"}, Params: map[string]string{"K09.user": "Ngọc Lan"}}, controls.Options{Today: today})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plan.Plan.Steps {
		if step.Action != spec.ActionNoChange {
			t.Fatalf("an alert already on plans %s %s: %v %v", step.Action, step.Entity, step.Changes, step.Blind)
		}
	}
	// Archived, or without its action, it is not on; without base_automation it is not available.
	f.rows["structure.alert_rule.list"][0]["active"] = false
	report, _ = controls.Read(ctx, f, controls.Options{Today: today})
	if s := report.Controls[8]; s.ID != "K09" || s.State != controls.StateOff || s.Reason != "the rule is archived" {
		t.Fatalf("archived: %+v", s)
	}
	f.rows["structure.alert_rule.list"][0]["active"] = true
	f.rows["structure.alert_action.list"] = nil
	report, _ = controls.Read(ctx, f, controls.Options{Today: today})
	if s := report.Controls[8]; s.State != controls.StatePartial {
		t.Fatalf("no action: %+v", s)
	}
	f.fail = map[string]error{"structure.alert_rule.list": &direct.OperationError{Operation: "structure.alert_rule.list", Class: string(odoo.ErrClassModuleNotInstalled), Message: "base_automation"}}
	report, _ = controls.Read(ctx, f, controls.Options{Today: today})
	if s := report.Controls[8]; s.State != controls.StateNotAvailable || !strings.Contains(s.Reason, "base_automation") {
		t.Fatalf("no module: %+v", s)
	}
}
