package odoo

import (
	"reflect"
	"sort"
	"testing"
)

func TestCatalogRequiredModules(t *testing.T) {
	catalog := Catalog()
	if len(catalog) == 0 {
		t.Fatal("expected catalog to not be empty")
	}

	for _, op := range catalog {
		if len(op.RequiredModules) == 0 {
			t.Errorf("operation %q must declare at least one required Odoo module", op.SemanticID)
		}
		if !sort.StringsAreSorted(op.RequiredModules) {
			t.Errorf("operation %q required modules are not sorted: %v", op.SemanticID, op.RequiredModules)
		}
		for i := 1; i < len(op.RequiredModules); i++ {
			if op.RequiredModules[i] == op.RequiredModules[i-1] {
				t.Errorf("operation %q required modules contain duplicate %q", op.SemanticID, op.RequiredModules[i])
			}
		}
	}

	expectedModules := map[string]string{
		"identity.contact.list":    "base",
		"people.employee.list":     "hr",
		"work.project.list":        "project",
		"crm.lead.list":            "crm",
		"sales.quotation.list":     "sale",
		"procurement.rfq.list":     "purchase",
		"inventory.transfer.list":  "stock",
		"finance.invoice.list":     "account",
		"schedule.event.list":      "calendar",
		"catalog.product.list":     "product",
		"structure.company.list":   "base",
		"structure.warehouse.list": "stock",
		"structure.website.list":   "website",
		"workforce.leave.list":     "hr_holidays",
	}
	for id, expectedModule := range expectedModules {
		op, ok := CatalogByID[id]
		if !ok {
			t.Errorf("operation %q not found in CatalogByID", id)
			continue
		}
		if len(op.RequiredModules) != 1 || op.RequiredModules[0] != expectedModule {
			t.Errorf("operation %q expected RequiredModules [%q], got %v", id, expectedModule, op.RequiredModules)
		}
	}

	confirmOp, ok := CatalogByID["sales.order.confirm"]
	if !ok {
		t.Fatal("operation sales.order.confirm not found in CatalogByID")
	}
	expected := []string{"sale"}
	if !reflect.DeepEqual(confirmOp.RequiredModules, expected) {
		t.Errorf("expected sales.order.confirm RequiredModules to be %v, got %v", expected, confirmOp.RequiredModules)
	}

	opsCopy := make([]Odoo19Operation, len(catalog))
	copy(opsCopy, catalog)

	id1 := ComputeSemanticContractIdentityFromOperationsAndContracts(opsCopy, nil)

	opsModified := make([]Odoo19Operation, len(opsCopy))
	copy(opsModified, opsCopy)
	for i := range opsModified {
		if opsModified[i].SemanticID == "sales.order.confirm" {
			opsModified[i].RequiredModules = []string{"sale", "custom_module"}
			break
		}
	}

	id2 := ComputeSemanticContractIdentityFromOperationsAndContracts(opsModified, nil)
	if id1 == id2 {
		t.Errorf("expected identity to change when RequiredModules is modified, but both were %s", id1)
	}
}
