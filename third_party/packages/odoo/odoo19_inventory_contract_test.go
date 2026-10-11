package odoo

import (
	"strings"
	"testing"
)

func TestInventoryQuantListContract(t *testing.T) {
	op, ok := CatalogByID["inventory.quant.list"]
	if !ok {
		t.Fatal("inventory.quant.list is not declared in canonical catalog")
	}

	if op.Kind != KindRead {
		t.Errorf("inventory.quant.list kind = %q, want %q", op.Kind, KindRead)
	}
	if op.Strategy != StrategyRead {
		t.Errorf("inventory.quant.list strategy = %q, want %q", op.Strategy, StrategyRead)
	}
	if op.ProviderModel != "stock.quant" {
		t.Errorf("inventory.quant.list provider model = %q, want stock.quant", op.ProviderModel)
	}
	if op.Confidence != "SOURCE_DISCOVERED" {
		t.Errorf("inventory.quant.list confidence = %q, want SOURCE_DISCOVERED", op.Confidence)
	}
	if len(op.RequiredModules) != 1 || op.RequiredModules[0] != "stock" {
		t.Errorf("inventory.quant.list required modules = %v, want [stock]", op.RequiredModules)
	}
	if op.DefaultSort != "id asc" {
		t.Errorf("inventory.quant.list default sort = %q, want 'id asc'", op.DefaultSort)
	}

	// Verified Odoo 19 fields for stock.quant (source-discovered projection only):
	// quantity, reserved_quantity, product_uom_id, company_id, location_id, product_id, id
	expectedFields := []string{"id", "product_id", "location_id", "quantity", "reserved_quantity", "product_uom_id", "company_id"}
	fieldSet := make(map[string]bool, len(op.ReadFields))
	for _, f := range op.ReadFields {
		fieldSet[f] = true
	}
	for _, expected := range expectedFields {
		if !fieldSet[expected] {
			t.Errorf("inventory.quant.list missing required read field %q in %v", expected, op.ReadFields)
		}
	}

	// Evidence & readiness checks
	evidenceClass, ok := Odoo19EvidenceClassOf("inventory.quant.list")
	if !ok || evidenceClass != Odoo19EvidenceNewerUnverified {
		t.Errorf("inventory.quant.list evidence class = %q, %v, want %q", evidenceClass, ok, Odoo19EvidenceNewerUnverified)
	}
	readiness, ok := ReadinessOf("inventory.quant.list")
	if !ok || readiness != ReadinessCurrentVerified {
		t.Errorf("inventory.quant.list readiness = %q, %v, want %q (oracle-agreed read run)", readiness, ok, ReadinessCurrentVerified)
	}
}

func TestInventoryMoveLineListContract(t *testing.T) {
	op, ok := CatalogByID["inventory.move_line.list"]
	if !ok {
		t.Fatal("inventory.move_line.list is not declared in canonical catalog")
	}

	if op.Kind != KindRead {
		t.Errorf("inventory.move_line.list kind = %q, want %q", op.Kind, KindRead)
	}
	if op.Strategy != StrategyRead {
		t.Errorf("inventory.move_line.list strategy = %q, want %q", op.Strategy, StrategyRead)
	}
	if op.ProviderModel != "stock.move.line" {
		t.Errorf("inventory.move_line.list provider model = %q, want stock.move.line", op.ProviderModel)
	}
	if op.Confidence != "SOURCE_DISCOVERED" {
		t.Errorf("inventory.move_line.list confidence = %q, want SOURCE_DISCOVERED", op.Confidence)
	}
	if len(op.RequiredModules) != 1 || op.RequiredModules[0] != "stock" {
		t.Errorf("inventory.move_line.list required modules = %v, want [stock]", op.RequiredModules)
	}
	if op.DefaultSort != "id asc" {
		t.Errorf("inventory.move_line.list default sort = %q, want 'id asc'", op.DefaultSort)
	}

	// Verified Odoo 19 fields for stock.move.line (source-discovered projection only):
	// quantity, quantity_product_uom, product_uom_id, move_id, picking_id, company_id, location_id, state, picked (NOT reserved_uom_qty)
	expectedFields := []string{"id", "move_id", "picking_id", "product_id", "location_id", "quantity", "quantity_product_uom", "product_uom_id", "company_id", "state", "picked"}
	fieldSet := make(map[string]bool, len(op.ReadFields))
	for _, f := range op.ReadFields {
		fieldSet[f] = true
		if f == "reserved_uom_qty" {
			t.Errorf("inventory.move_line.list must not include deprecated Odoo 18 field reserved_uom_qty")
		}
	}
	for _, expected := range expectedFields {
		if !fieldSet[expected] {
			t.Errorf("inventory.move_line.list missing required read field %q in %v", expected, op.ReadFields)
		}
	}

	// Evidence & readiness checks
	evidenceClass, ok := Odoo19EvidenceClassOf("inventory.move_line.list")
	if !ok || evidenceClass != Odoo19EvidenceNewerUnverified {
		t.Errorf("inventory.move_line.list evidence class = %q, %v, want %q", evidenceClass, ok, Odoo19EvidenceNewerUnverified)
	}
	readiness, ok := ReadinessOf("inventory.move_line.list")
	if !ok || readiness != ReadinessCurrentVerified {
		t.Errorf("inventory.move_line.list readiness = %q, %v, want %q (oracle-agreed read run)", readiness, ok, ReadinessCurrentVerified)
	}
}

func TestCatalogUOMDescribeContract(t *testing.T) {
	op, ok := CatalogByID["catalog.uom.describe"]
	if !ok {
		t.Fatal("catalog.uom.describe is not declared in canonical catalog")
	}

	if op.Kind != KindRead {
		t.Errorf("catalog.uom.describe kind = %q, want %q", op.Kind, KindRead)
	}
	if op.Strategy != StrategyRead {
		t.Errorf("catalog.uom.describe strategy = %q, want %q", op.Strategy, StrategyRead)
	}
	if op.ProviderModel != "uom.uom" {
		t.Errorf("catalog.uom.describe provider model = %q, want uom.uom", op.ProviderModel)
	}
	if op.Confidence != "SOURCE_DISCOVERED" {
		t.Errorf("catalog.uom.describe confidence = %q, want SOURCE_DISCOVERED", op.Confidence)
	}

	// Verified Odoo 19 fields for uom.uom (source-discovered projection only):
	// id, name, rounding
	expectedFields := []string{"id", "name", "rounding"}
	fieldSet := make(map[string]bool, len(op.ReadFields))
	for _, f := range op.ReadFields {
		fieldSet[f] = true
	}
	for _, expected := range expectedFields {
		if !fieldSet[expected] {
			t.Errorf("catalog.uom.describe missing required read field %q in %v", expected, op.ReadFields)
		}
	}

	// Evidence & readiness checks
	evidenceClass, ok := Odoo19EvidenceClassOf("catalog.uom.describe")
	if !ok || evidenceClass != Odoo19EvidenceNewerUnverified {
		t.Errorf("catalog.uom.describe evidence class = %q, %v, want %q", evidenceClass, ok, Odoo19EvidenceNewerUnverified)
	}
	readiness, ok := ReadinessOf("catalog.uom.describe")
	if !ok || readiness != ReadinessCurrentVerified {
		t.Errorf("catalog.uom.describe readiness = %q, %v, want %q (oracle-agreed read run)", readiness, ok, ReadinessCurrentVerified)
	}
}

func TestInventoryContractKeysetOrderingAndBoundedReads(t *testing.T) {
	for _, id := range []string{"inventory.quant.list", "inventory.move_line.list"} {
		op, ok := CatalogByID[id]
		if !ok {
			t.Fatalf("operation %s not declared in catalog", id)
		}
		if op.DefaultSort != "id asc" {
			t.Errorf("operation %s must enforce DefaultSort 'id asc' for bounded keyset pagination, got %q", id, op.DefaultSort)
		}
		if strings.TrimSpace(op.ProviderModel) == "" {
			t.Errorf("operation %s has empty provider model", id)
		}
	}
}

// TestInventoryOperationsDoNotManufactureLiveEvidence: the records these reads carry come from the
// oracle-backed read conformance run, not from catalog presence.
func TestInventoryOperationsDoNotManufactureLiveEvidence(t *testing.T) {
	records, err := currentConformanceRecords()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.OperationID == "inventory.quant.list" || record.OperationID == "inventory.move_line.list" || record.OperationID == "catalog.uom.describe" {
			if record.EvidenceRef != "docs/architecture/provider-research/odoo-surface/evidence/read-conformance-19.0-20261010.json" {
				t.Fatalf("conformance record for %s cites %q, not the read conformance run", record.OperationID, record.EvidenceRef)
			}
		}
	}

	for _, id := range []string{"inventory.quant.list", "inventory.move_line.list", "catalog.uom.describe"} {
		var foundInUnverified bool
		for _, uID := range Odoo19NewerUnverifiedOperations {
			if uID == id {
				foundInUnverified = true
				break
			}
		}
		if !foundInUnverified {
			t.Errorf("operation %s must be explicitly listed in Odoo19NewerUnverifiedOperations", id)
		}
	}
}
