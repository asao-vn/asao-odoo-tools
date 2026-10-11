package odoo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func settingsOperation(t *testing.T, id string) Odoo19Operation {
	t.Helper()
	for _, op := range SurfaceCatalog() {
		if op.SemanticID == id {
			return op
		}
	}
	t.Fatalf("no surface operation %s", id)
	return Odoo19Operation{}
}

// The Settings page also installs modules (module_*), loads a chart of accounts (chart_template) and
// holds credentials. structure.settings.update reaches none of them: each field it sets is a switch,
// a number or a closed choice, so no free text, and no secret, can travel through a spec.
func TestSettingsSetOnlySwitchesNumbersAndChoices(t *testing.T) {
	update := settingsOperation(t, "structure.settings.update")
	for _, field := range update.WizardAllowedFields {
		typed := update.InputTypes[field] == "boolean" || update.InputTypes[field] == "integer" || len(update.InputEnums[field]) > 0
		if strings.HasPrefix(field, "module_") || field == "chart_template" || field == "company_id" || !typed {
			t.Fatalf("structure.settings.update sets %s (type %q)", field, update.InputTypes[field])
		}
	}
	read := settingsOperation(t, "structure.settings.describe")
	if strings.Join(read.OptionalReadFields, ",") != strings.Join(update.WizardAllowedFields, ",") {
		t.Fatalf("the settings read shows %v but the write sets %v", read.OptionalReadFields, update.WizardAllowedFields)
	}
}

// Saving settings is the Settings page's Save: a new res.config.settings with only the caller's
// values, then execute. A field outside the allowlist is refused before Odoo is contacted.
func TestSettingsWriteIsThePagesSave(t *testing.T) {
	surface := func(tr Transport) *Adapter {
		a := NewAdapter(tr)
		a.Surface = true
		return a
	}
	tr := &fakeTransport{}
	if _, err := surface(tr).Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "structure.settings.update", Input: `{"values":{"group_uom":true}}`}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(tr.mutations); got != "[{Create res.config.settings []} {CallMethod:execute res.config.settings []}]" {
		t.Fatalf("mutations %s", got)
	}
	if got := fmt.Sprint(tr.createdValues); got != "[map[group_uom:true]]" {
		t.Fatalf("the settings were made with %s: want only what the caller set", got)
	}
	for _, input := range []string{`{"values":{"module_crm_iap_enrich":true}}`, `{"values":{"chart_template":"vn"}}`, `{"values":{}}`, `{"target_id":1,"values":{"group_uom":true}}`} {
		refused := &fakeTransport{}
		if _, err := surface(refused).Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "structure.settings.update", Input: input}); err == nil {
			t.Fatalf("%s was accepted", input)
		}
		assertNoMutation(t, refused, input)
	}
}

// Reading settings asks Odoo for a new record's values (onchange), as the page does when it opens:
// related values and configuration parameters are there, where default_get alone leaves them out.
// Nothing else Odoo answers is passed on.
func TestSettingsReadIsWhatThePageShows(t *testing.T) {
	tr := &fakeTransport{
		searchRead: func(model string, _ interface{}, _ []string) ([]map[string]interface{}, error) {
			return []map[string]interface{}{{"name": "group_uom"}, {"name": "auth_signup_uninvited"}, {"name": "module_crm_iap_enrich"}}, nil
		},
		callMethod: func(model, method string) (interface{}, error) {
			if method != "onchange" {
				return nil, errors.New("unexpected " + method)
			}
			return map[string]interface{}{"value": map[string]interface{}{"group_uom": true, "auth_signup_uninvited": "b2b", "module_crm_iap_enrich": false}}, nil
		},
	}
	a := NewAdapter(tr)
	a.Surface = true
	res, err := a.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "structure.settings.describe", Input: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(res.Data); got != "[map[auth_signup_uninvited:b2b group_uom:true]]" {
		t.Fatalf("read %s", got)
	}
	if _, err := a.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "structure.settings.describe", Input: `{"target_id":3}`}); err == nil {
		t.Fatal("a settings read took a target")
	}
}
