package upgradecheck

import (
	"testing"

	"github.com/asao-platform/atoc/packages/odoo"
)

// A pack's operations are checked like any customization: a field the target renamed, a model it
// dropped, and a model of the module itself, which only the module's own next version can answer for.
func TestAPackOperationIsCheckedLikeACustomization(t *testing.T) {
	pair := loadPair(t)
	text := map[string]Field{"name": {Type: "char", Store: true, Searchable: true}}
	source := fixtureSource{
		fields: map[string]map[string]Field{
			"hr.leave":      {"name": {Type: "char", Store: true, Searchable: true}, "holiday_status_id": {Type: "many2one", Relation: "hr.leave.type", Store: true, Searchable: true}},
			"hr.leave.type": text,
			"stock.scrap":   {"name": {Type: "char", Store: true, Searchable: true}, "scrap_qty": {Type: "float", Store: true, Searchable: true}},
			"res.partner":   {"name": {Type: "char", Store: true, Searchable: true}, "phone": {Type: "char", Store: true, Searchable: true}, "parent_id": {Type: "many2one", Relation: "res.partner", Store: true, Searchable: true}},
			"salon.booking": {"customer_id": {Type: "many2one", Relation: "res.partner", Store: true, Searchable: true}, "seats": {Type: "integer", Store: true, Searchable: true}},
		},
		modules: map[string][]string{"salon.booking": {"salon_demo"}, "salon.booking.seats": {"salon_demo"}, "salon.booking.customer_id": {"salon_demo"}},
	}
	packs := []odoo.Pack{{Module: "leave_tools", Operations: []odoo.PackOperation{
		{ID: "leave_tools.leave.list", Model: "hr.leave", ReadFields: []string{"name", "holiday_status_id"}, DefaultSort: "name desc"},
		{ID: "leave_tools.scrap.list", Model: "stock.scrap", ReadFields: []string{"scrap_qty"}},
		{ID: "leave_tools.partner.update", Model: "res.partner", Fields: []string{"phone", "parent_id"},
			Relations: map[string]odoo.PackRelation{"parent_id": {Model: "res.partner", SearchField: "name"}}},
	}}, {Module: "salon_demo", Operations: []odoo.PackOperation{
		{ID: "salon_demo.booking.update", Model: "salon.booking", Fields: []string{"customer_id", "seats"}, Method: "action_confirm"},
	}}}
	report := Scan(source, Customizations{Records: map[string][]map[string]interface{}{PackKey: PackRecords(packs)}}, pair)
	for name, want := range map[string]string{
		"leave_tools.leave.list":     "FIELD_RENAMED holiday_status_id",
		"leave_tools.scrap.list":     "MODEL_MISSING stock.scrap",
		"leave_tools.partner.update": "",
		"salon_demo.booking.update":  "NOT_COVERED salon.booking",
	} {
		if got := problems(findingsOf(report, name)); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	for _, f := range findingsOf(report, "leave_tools.leave.list") {
		if f.Kind != "pack operation" || f.To != "work_entry_type_id" {
			t.Errorf("finding %+v", f)
		}
	}
	if report.Scanned[PackKey] != 4 {
		t.Errorf("scanned %v", report.Scanned)
	}
}
