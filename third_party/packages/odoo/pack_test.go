package odoo

import (
	"strings"
	"testing"
)

const demoPack = `{
  "schema": "odoo-cli.pack/v1",
  "module": "x_salon",
  "title": "Salon bookings",
  "author": "A partner",
  "series": ["19.0"],
  "operations": [
    {"id": "x_salon.booking.list", "kind": "READ", "strategy": "READ", "model": "x_salon.booking",
     "summary": "Bookings", "readFields": ["name", "partner_id", "start", "state"], "defaultSort": "start desc"},
    {"id": "x_salon.booking.create", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "x_salon.booking",
     "writeMode": "CREATE", "fields": ["partner_id", "start", "seats", "vip"],
     "relations": {"partner_id": {"model": "res.partner", "searchField": "name"}},
     "types": {"partner_id": "reference", "seats": "integer", "vip": "boolean"}},
    {"id": "x_salon.booking.confirm", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "x_salon.booking",
     "method": "action_confirm", "risk": "LOW"},
    {"id": "x_salon.booking.reschedule", "kind": "ACTION", "strategy": "WIZARD", "model": "x_salon.booking",
     "wizard": {"model": "x_salon.reschedule", "method": "action_apply", "fields": ["start"]}},
    {"id": "x_salon.booking.print", "kind": "READ", "strategy": "REPORT", "model": "x_salon.booking",
     "report": "x_salon.report_booking"},
    {"id": "x_salon.deposit.register", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "account.payment",
     "method": "action_x_salon_deposit", "risk": "MEDIUM"}
  ]
}`

func TestPackOperationsJoinTheStandaloneCatalogOnly(t *testing.T) {
	ForgetPacks()
	t.Cleanup(ForgetPacks)
	pack, err := ParsePack([]byte(demoPack))
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterPack(pack, PackOrigin{SHA256: "abc", Source: "file"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(PackCatalog()) != 6 {
		t.Fatalf("pack catalog: %d", len(PackCatalog()))
	}
	op, ok := LookupStandaloneOperation("x_salon.booking.create")
	if !ok || op.ProviderModel != "x_salon.booking" || op.WriteMode != WriteModeCreate || op.RequiredModules[0] != "x_salon" {
		t.Fatalf("create: %+v %v", op, ok)
	}
	if op.InputTypes["seats"] != "integer" || op.InputTypes["vip"] != "boolean" || op.InputTypes["partner_id"] != "reference" ||
		op.Relations["partner_id"].TargetModel != "res.partner" || op.Relations["partner_id"].SearchField != "name" {
		t.Fatalf("inputs: %+v %+v", op.InputTypes, op.Relations)
	}
	if _, governed := lookupOperation("x_salon.booking.create", false); governed {
		t.Fatal("a pack operation reached the governed lookup")
	}
	if evidence, _ := Odoo19EvidenceClassOf("x_salon.booking.create"); evidence != Odoo19EvidencePackDeclared {
		t.Fatalf("evidence: %s", evidence)
	}
	if readiness, _ := ReadinessOf("x_salon.booking.create"); readiness != ReadinessUnverified {
		t.Fatalf("readiness: %s", readiness)
	}
	if readiness, _ := ReadinessFor("20.0", "x_salon.booking.list"); readiness != ReadinessUnverified {
		t.Fatalf("20.0 readiness: %s", readiness)
	}
	// The floors: a button is MEDIUM at least, a write to a model that moves money HIGH.
	if confirm, _ := LookupStandaloneOperation("x_salon.booking.confirm"); confirm.Risk != RiskMedium {
		t.Fatalf("button risk: %s", confirm.Risk)
	}
	if deposit, _ := LookupStandaloneOperation("x_salon.deposit.register"); deposit.Risk != RiskHigh {
		t.Fatalf("payment risk: %s", deposit.Risk)
	}
	if list, _ := LookupStandaloneOperation("x_salon.booking.list"); list.Risk != RiskLow || len(list.OptionalReadFields) != 4 {
		t.Fatalf("list: %+v", list)
	}
	if wizard, _ := LookupStandaloneOperation("x_salon.booking.reschedule"); wizard.WizardModel != "x_salon.reschedule" || !wizard.WizardActiveContext ||
		TargetRequirementOf(wizard) != TargetRequirementRequired {
		t.Fatalf("wizard: %+v", wizard)
	}
	if origin, ok := PackOriginOf("x_salon.booking.print"); !ok || origin.Module != "x_salon" || origin.Author != "A partner" || origin.SHA256 != "abc" {
		t.Fatalf("origin: %+v", origin)
	}
	// The same operation from another pack is refused, and nothing of that pack is registered.
	again := strings.Replace(demoPack, `"title": "Salon bookings"`, `"title": "Again"`, 1)
	second, err := ParsePack([]byte(again))
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterPack(second, PackOrigin{}, nil); err == nil || !strings.Contains(err.Error(), "already declared") {
		t.Fatalf("a second registration: %v", err)
	}
	ForgetPacks()
	if IsPackOperation("x_salon.booking.list") {
		t.Fatal("ForgetPacks kept an operation")
	}
}

func TestPackRefusesWhatOnlyAPersonMayDo(t *testing.T) {
	ForgetPacks()
	t.Cleanup(ForgetPacks)
	op := func(body string) string {
		return `{"schema": "odoo-cli.pack/v1", "module": "x_salon", "operations": [` + body + `]}`
	}
	cases := map[string]struct{ document, want string }{
		"another schema": {strings.Replace(op(`{"id": "x_salon.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["name"]}`), "v1", "v9", 1), "schema"},
		"an unknown key": {op(`{"id": "x_salon.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["name"], "code": "x"}`), "unknown field"},
		"a family name": {strings.Replace(op(`{"id": "sales.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["name"]}`),
			`"module": "x_salon"`, `"module": "sales"`, 1), "catalog family"},
		"an id of another module":           {op(`{"id": "x_other.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["name"]}`), "begins with the pack's module"},
		"a delete":                          {op(`{"id": "x_salon.a.delete", "kind": "COMMAND", "strategy": "DESTRUCTIVE_GUARDED", "model": "x_a"}`), "never deletes"},
		"a module install":                  {op(`{"id": "x_salon.a.install", "kind": "ACTION", "strategy": "module_install", "model": "x_a"}`), "never deletes, installs"},
		"a private method":                  {op(`{"id": "x_salon.a.go", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "x_a", "method": "_compute_total"}`), "private"},
		"the ORM's write":                   {op(`{"id": "x_salon.a.go", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "x_a", "method": "write"}`), "ORM's own"},
		"the ORM's unlink":                  {op(`{"id": "x_salon.a.go", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "x_a", "method": "unlink"}`), "ORM's own"},
		"a scheduled action":                {op(`{"id": "x_salon.a.go", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "ir.cron", "method": "method_direct_trigger"}`), "credentials or system configuration"},
		"a user":                            {op(`{"id": "x_salon.a.go", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "res.users", "writeMode": "UPDATE", "fields": ["login"]}`), "refused to packs"},
		"an automation rule":                {op(`{"id": "x_salon.a.go", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "base.automation", "writeMode": "CREATE", "fields": ["name"]}`), "refused to packs"},
		"a mail template":                   {op(`{"id": "x_salon.a.go", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "mail.template", "writeMode": "UPDATE", "fields": ["body_html"]}`), "refused to packs"},
		"a secret field":                    {op(`{"id": "x_salon.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["x_api_token"]}`), "secret"},
		"a dotted field":                    {op(`{"id": "x_salon.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["partner_id.name"]}`), "plain field"},
		"a kind that is not its strategy's": {op(`{"id": "x_salon.a.go", "kind": "READ", "strategy": "METHOD_CALL", "model": "x_a", "method": "action_go"}`), "of kind ACTION"},
		"a key of another strategy":         {op(`{"id": "x_salon.a.go", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "x_a", "method": "action_go", "fields": ["name"]}`), "does not take"},
		"a reference without a relation": {op(`{"id": "x_salon.a.make", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "x_a", "writeMode": "CREATE",
			"fields": ["partner_id"], "types": {"partner_id": "reference"}}`), "needs a relation"},
		"a binary-like type": {op(`{"id": "x_salon.a.make", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "x_a", "writeMode": "CREATE",
			"fields": ["photo"], "types": {"photo": "binary"}}`), "not string, integer"},
		"an id twice": {op(`{"id": "x_salon.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["name"]},
			{"id": "x_salon.a.list", "kind": "READ", "strategy": "READ", "model": "x_a", "readFields": ["name"]}`), "declared twice"},
		"a wizard on its own model": {op(`{"id": "x_salon.a.go", "kind": "ACTION", "strategy": "WIZARD", "model": "x_a", "wizard": {"model": "x_a", "method": "action_apply"}}`), "model of its own"},
		"no operation":              {`{"schema": "odoo-cli.pack/v1", "module": "x_salon", "operations": []}`, "no operation"},
	}
	for name, c := range cases {
		_, err := ParsePack([]byte(c.document))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", name, err, c.want)
		}
	}
	pack, err := ParsePack([]byte(demoPack))
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterPack(pack, PackOrigin{}, []string{"x_salon"}); err == nil || !strings.Contains(err.Error(), "command of its own") {
		t.Fatalf("a module named like a command: %v", err)
	}
	if len(PackCatalog()) != 0 {
		t.Fatal("a refused pack registered operations")
	}
}

func TestPackRiskFloorsNeverLowerADeclaration(t *testing.T) {
	cases := []struct {
		op   PackOperation
		want RiskClassification
	}{
		{PackOperation{Strategy: "READ", Model: "account.move"}, RiskLow},
		{PackOperation{Strategy: "FIELD_WRITE", Model: "x_a"}, RiskLow},
		{PackOperation{Strategy: "FIELD_WRITE", Model: "x_a", Risk: "CRITICAL"}, RiskCritical},
		{PackOperation{Strategy: "METHOD_CALL", Model: "x_a", Risk: "LOW"}, RiskMedium},
		{PackOperation{Strategy: "FIELD_WRITE", Model: "res.partner.bank"}, RiskHigh},
		{PackOperation{Strategy: "WIZARD", Model: "x_a", Wizard: &PackWizard{Model: "account.payment"}}, RiskHigh},
	}
	for _, c := range cases {
		if got := PackRisk(c.op); got != c.want {
			t.Errorf("%+v: %s, want %s", c.op, got, c.want)
		}
	}
}
