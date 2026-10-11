package odoo

import (
	"testing"
)

func TestConformanceContract_All101OperationsCovered(t *testing.T) {
	if len(CatalogByID) != 105 {
		t.Fatalf("catalog operations = %d, want 105", len(CatalogByID))
	}
	for id, op := range CatalogByID {
		if op.Strategy == StrategyRead {
			continue
		}
		if IsSafeConformanceMutation(id) {
			continue
		}
		reason := ConformanceNotRunReason(id)
		if reason == "" {
			t.Fatalf("operation %s has empty not-run reason", id)
		}
	}
}

func TestConformanceContract_SafeMutationsExistInCatalog(t *testing.T) {
	for id := range SafeConformanceMutations {
		op, ok := CatalogByID[id]
		if !ok {
			t.Fatalf("safe conformance mutation %s not found in catalog", id)
		}
		if op.Strategy == StrategyRead {
			t.Fatalf("safe conformance mutation %s has strategy READ", id)
		}
	}
}

func TestConformanceContract_DestructiveOperationsHaveGuardReason(t *testing.T) {
	destructive := []string{"people.department.delete", "work.project.delete"}
	for _, id := range destructive {
		op, ok := CatalogByID[id]
		if !ok {
			t.Fatalf("operation %s not in catalog", id)
		}
		if op.Strategy != StrategyDestructiveGuarded {
			t.Fatalf("operation %s strategy = %s, want DESTRUCTIVE_GUARDED", id, op.Strategy)
		}
		reason := ConformanceNotRunReason(id)
		if reason != ReasonDestructiveGuardedDisabled {
			t.Fatalf("operation %s reason = %q, want %q", id, reason, ReasonDestructiveGuardedDisabled)
		}
	}
}

func TestConformanceContract_ModuleInstallHasSchemaReason(t *testing.T) {
	op, ok := CatalogByID["admin.module.install"]
	if !ok {
		t.Fatal("admin.module.install not in catalog")
	}
	if op.Strategy != StrategyModuleInstall {
		t.Fatalf("admin.module.install strategy = %s, want %s", op.Strategy, StrategyModuleInstall)
	}
	reason := ConformanceNotRunReason("admin.module.install")
	if reason != ReasonModuleInstallDisabled {
		t.Fatalf("admin.module.install reason = %q, want %q", reason, ReasonModuleInstallDisabled)
	}
}

func TestConformanceContract_ProductSemanticFields(t *testing.T) {
	createContract, ok := Odoo19Contracts["catalog.product.create"]
	if !ok {
		t.Fatal("catalog.product.create contract missing from Odoo19Contracts")
	}
	salePriceField, ok := createContract.InputByName("sale_price")
	if !ok {
		t.Fatal("catalog.product.create must accept sale_price in caller vocabulary")
	}
	if salePriceField.ProviderField != "list_price" {
		t.Fatalf("catalog.product.create sale_price providerField = %q, want list_price", salePriceField.ProviderField)
	}
	if _, ok := createContract.InputByName("list_price"); ok {
		t.Fatal("catalog.product.create must not expose provider field list_price as caller input")
	}

	updateContract, ok := Odoo19Contracts["catalog.product.update"]
	if !ok {
		t.Fatal("catalog.product.update contract missing from Odoo19Contracts")
	}
	updateSalePrice, ok := updateContract.InputByName("sale_price")
	if !ok {
		t.Fatal("catalog.product.update must accept sale_price in caller vocabulary")
	}
	if updateSalePrice.ProviderField != "list_price" {
		t.Fatalf("catalog.product.update sale_price providerField = %q, want list_price", updateSalePrice.ProviderField)
	}
	if _, ok := updateContract.InputByName("list_price"); ok {
		t.Fatal("catalog.product.update must not expose provider field list_price as caller input")
	}

	if !IsSafeConformanceMutation("catalog.product.create") {
		t.Fatal("catalog.product.create must be recognized as safe conformance mutation")
	}
	if !IsSafeConformanceMutation("catalog.product.update") {
		t.Fatal("catalog.product.update must be recognized as safe conformance mutation")
	}

	if StatusLabSetupFailed != "LAB_SETUP_FAILED" {
		t.Fatalf("StatusLabSetupFailed = %q, want LAB_SETUP_FAILED", StatusLabSetupFailed)
	}
}

func TestConformanceContract_BaselineAndExpandedCounts(t *testing.T) {
	if len(BaselineSafeConformanceMutations) != 8 {
		t.Fatalf("BaselineSafeConformanceMutations count = %d, want 8", len(BaselineSafeConformanceMutations))
	}
	if len(ExpandedSafeConformanceMutations) != 18 {
		t.Fatalf("ExpandedSafeConformanceMutations count = %d, want 18", len(ExpandedSafeConformanceMutations))
	}
	if len(SafeConformanceMutations) != 26 {
		t.Fatalf("SafeConformanceMutations count = %d, want 26", len(SafeConformanceMutations))
	}
	for id := range BaselineSafeConformanceMutations {
		if !IsBaselineSafeConformanceMutation(id) {
			t.Fatalf("IsBaselineSafeConformanceMutation(%q) = false, want true", id)
		}
		if !IsSafeConformanceMutation(id) {
			t.Fatalf("IsSafeConformanceMutation(%q) = false, want true", id)
		}
	}
	for id := range ExpandedSafeConformanceMutations {
		if !IsExpandedSafeConformanceMutation(id) {
			t.Fatalf("IsExpandedSafeConformanceMutation(%q) = false, want true", id)
		}
		if !IsSafeConformanceMutation(id) {
			t.Fatalf("IsSafeConformanceMutation(%q) = false, want true", id)
		}
	}
}

func TestConformanceContract_ExpandedSafeMutationsContractValidity(t *testing.T) {
	prioritizedGroups := map[string][]string{
		"crm":         {"crm.lead.create", "crm.lead.update"},
		"sales":       {"sales.quotation.create", "sales.quotation.line.add", "sales.quotation.update", "sales.order.line.update"},
		"procurement": {"procurement.rfq.create", "procurement.rfq.line.add", "procurement.rfq.update", "procurement.order.line.update"},
		"work":        {"work.project.create", "work.project.update", "work.task.create", "work.task.update", "work.milestone.create", "work.milestone.reach"},
		"schedule":    {"schedule.event.create", "schedule.event.update"},
	}

	for group, ops := range prioritizedGroups {
		for _, opID := range ops {
			if !IsExpandedSafeConformanceMutation(opID) {
				t.Fatalf("operation %s in group %s not in ExpandedSafeConformanceMutations", opID, group)
			}
			op, ok := CatalogByID[opID]
			if !ok {
				t.Fatalf("operation %s not found in CatalogByID", opID)
			}
			if op.Strategy != StrategyFieldWrite {
				t.Fatalf("operation %s strategy = %s, want StrategyFieldWrite", opID, op.Strategy)
			}
			contract, ok := Odoo19Contracts[opID]
			if !ok {
				t.Fatalf("operation %s missing contract in Odoo19Contracts", opID)
			}
			if len(contract.Inputs) == 0 {
				t.Fatalf("operation %s contract has no inputs", opID)
			}
			if len(op.RequiredModules) == 0 {
				t.Fatalf("operation %s has no declared required modules", opID)
			}
		}
	}
}

func TestConformanceContract_BlockedMutationsCategorized(t *testing.T) {
	blockedMutations := []struct {
		id         string
		wantReason string
	}{
		{"admin.module.install", ReasonModuleInstallDisabled},
		{"people.department.delete", ReasonDestructiveGuardedDisabled},
		{"work.project.delete", ReasonDestructiveGuardedDisabled},
		{"inventory.transfer.reserve", ReasonStockPickingFixtureNeeded},
		{"inventory.transfer.validate", ReasonStockPickingFixtureNeeded},
		{"inventory.transfer.cancel", ReasonStockPickingFixtureNeeded},
		{"sales.order.confirm", ReasonSalesWorkflowFixtureNeeded},
		{"sales.order.cancel", ReasonSalesWorkflowFixtureNeeded},
		{"sales.order.draft", ReasonSalesWorkflowFixtureNeeded},
		{"sales.invoice.create", ReasonInvoiceWorkflowFixtureNeeded},
		{"procurement.order.confirm", ReasonPurchaseWorkflowFixtureNeeded},
		{"procurement.order.cancel", ReasonPurchaseWorkflowFixtureNeeded},
		{"procurement.order.draft", ReasonPurchaseWorkflowFixtureNeeded},
		{"procurement.bill.create", ReasonInvoiceWorkflowFixtureNeeded},
		{"finance.invoice.update", ReasonInvoiceWorkflowFixtureNeeded},
		{"finance.invoice.post", ReasonInvoiceWorkflowFixtureNeeded},
		{"finance.invoice.cancel", ReasonInvoiceWorkflowFixtureNeeded},
		{"finance.invoice.draft", ReasonInvoiceWorkflowFixtureNeeded},
		{"finance.payment.register", ReasonInvoiceWorkflowFixtureNeeded},
		{"crm.opportunity.convert", ReasonOpportunityPipelineStageNeeded},
		{"crm.opportunity.win", ReasonOpportunityPipelineStageNeeded},
		{"crm.opportunity.lose", ReasonLeadOpportunityFixtureNeeded},
	}

	if len(blockedMutations) != 22 {
		t.Fatalf("blocked mutations count = %d, want 22", len(blockedMutations))
	}

	for _, tc := range blockedMutations {
		if IsSafeConformanceMutation(tc.id) {
			t.Fatalf("blocked operation %s is marked safe", tc.id)
		}
		reason := ConformanceNotRunReason(tc.id)
		if reason != tc.wantReason {
			t.Fatalf("operation %s reason = %q, want %q", tc.id, reason, tc.wantReason)
		}
	}
}

func TestConformanceContract_ConformanceCategoryOf(t *testing.T) {
	counts := map[ConformanceCategory]int{}
	for id := range CatalogByID {
		category, ok := ConformanceCategoryOf(id)
		if !ok {
			t.Fatalf("ConformanceCategoryOf(%q) returned ok=false", id)
		}
		counts[category]++
	}

	if counts[CategoryRead] != 57 {
		t.Fatalf("CategoryRead count = %d, want 57", counts[CategoryRead])
	}
	if counts[CategoryBaselineSafeMutation] != 8 {
		t.Fatalf("CategoryBaselineSafeMutation count = %d, want 8", counts[CategoryBaselineSafeMutation])
	}
	if counts[CategoryExpandedSafeMutation] != 18 {
		t.Fatalf("CategoryExpandedSafeMutation count = %d, want 18", counts[CategoryExpandedSafeMutation])
	}
	if counts[CategoryBlockedMutation] != 22 {
		t.Fatalf("CategoryBlockedMutation count = %d, want 22", counts[CategoryBlockedMutation])
	}
	total := counts[CategoryRead] + counts[CategoryBaselineSafeMutation] + counts[CategoryExpandedSafeMutation] + counts[CategoryBlockedMutation]
	if total != 105 {
		t.Fatalf("total categorized operations = %d, want 105", total)
	}

	if _, ok := ConformanceCategoryOf("unknown.nonexistent.op"); ok {
		t.Fatal("ConformanceCategoryOf for unknown operation should return ok=false")
	}
}

func TestConformanceContract_SafeConformanceMutationIDs(t *testing.T) {
	ids := SafeConformanceMutationIDs()
	if len(ids) != 26 {
		t.Fatalf("SafeConformanceMutationIDs len = %d, want 26", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("SafeConformanceMutationIDs not strictly sorted: %s <= %s", ids[i], ids[i-1])
		}
	}
	for _, id := range ids {
		if !IsSafeConformanceMutation(id) {
			t.Fatalf("id %s in SafeConformanceMutationIDs is not marked safe", id)
		}
	}
}

func TestConformanceContract_BlockedMutationIDs(t *testing.T) {
	ids := BlockedMutationIDs()
	if len(ids) != 22 {
		t.Fatalf("BlockedMutationIDs len = %d, want 22", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("BlockedMutationIDs not strictly sorted: %s <= %s", ids[i], ids[i-1])
		}
	}
	for _, id := range ids {
		if IsSafeConformanceMutation(id) {
			t.Fatalf("id %s in BlockedMutationIDs is marked safe", id)
		}
		reason := ConformanceNotRunReason(id)
		if reason == "" {
			t.Fatalf("id %s in BlockedMutationIDs has empty reason", id)
		}
	}
}

func TestConformanceContract_SafeReadsContractValidity(t *testing.T) {
	if len(SafeConformanceReads) != 26 {
		t.Fatalf("SafeConformanceReads count = %d, want 26", len(SafeConformanceReads))
	}
	for id := range SafeConformanceReads {
		op, ok := CatalogByID[id]
		if !ok {
			t.Fatalf("safe read %s not found in CatalogByID", id)
		}
		if op.Strategy != StrategyRead {
			t.Fatalf("safe read %s strategy = %s, want StrategyRead", id, op.Strategy)
		}
		if len(op.RequiredModules) == 0 {
			t.Fatalf("safe read %s has no declared required modules", id)
		}
		if !IsSafeConformanceRead(id) {
			t.Fatalf("IsSafeConformanceRead(%q) = false, want true", id)
		}
	}
}

func TestConformanceContract_SafeConformanceReadIDs(t *testing.T) {
	ids := SafeConformanceReadIDs()
	if len(ids) != 26 {
		t.Fatalf("SafeConformanceReadIDs len = %d, want 26", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("SafeConformanceReadIDs not strictly sorted: %s <= %s", ids[i], ids[i-1])
		}
	}
	for _, id := range ids {
		if !IsSafeConformanceRead(id) {
			t.Fatalf("id %s in SafeConformanceReadIDs is not marked safe read", id)
		}
	}
}

func TestConformanceContract_StructureReadContractSemantics(t *testing.T) {
	locOp, ok := CatalogByID["structure.location.list"]
	if !ok {
		t.Fatal("structure.location.list missing from CatalogByID")
	}
	if locOp.ProviderModel != "stock.location" {
		t.Fatalf("structure.location.list provider model = %q, want stock.location", locOp.ProviderModel)
	}
	if locOp.DefaultSort != "id asc" {
		t.Fatalf("structure.location.list default sort = %q, want 'id asc'", locOp.DefaultSort)
	}
	hasCompleteName := false
	hasActive := false
	for _, f := range locOp.ReadFields {
		if f == "complete_name" {
			hasCompleteName = true
		}
		if f == "active" {
			hasActive = true
		}
	}
	if !hasCompleteName {
		t.Fatalf("structure.location.list read fields %v missing complete_name", locOp.ReadFields)
	}
	if !hasActive {
		t.Fatalf("structure.location.list read fields %v missing active-state evidence", locOp.ReadFields)
	}

	modOp, ok := CatalogByID["structure.module.list"]
	if !ok {
		t.Fatal("structure.module.list missing from CatalogByID")
	}
	if modOp.ProviderModel != "ir.module.module" {
		t.Fatalf("structure.module.list provider model = %q, want ir.module.module", modOp.ProviderModel)
	}
	hasName := false
	for _, f := range modOp.ReadFields {
		if f == "name" {
			hasName = true
			break
		}
	}
	if !hasName {
		t.Fatalf("structure.module.list read fields %v missing name", modOp.ReadFields)
	}

	whOp, ok := CatalogByID["structure.warehouse.list"]
	if !ok {
		t.Fatal("structure.warehouse.list missing from CatalogByID")
	}
	if whOp.ProviderModel != "stock.warehouse" {
		t.Fatalf("structure.warehouse.list provider model = %q, want stock.warehouse", whOp.ProviderModel)
	}
	hasLotStock := false
	hasOutType := false
	for _, f := range whOp.ReadFields {
		if f == "lot_stock_id" {
			hasLotStock = true
		}
		if f == "out_type_id" {
			hasOutType = true
		}
	}
	if !hasLotStock {
		t.Fatalf("structure.warehouse.list read fields %v missing lot_stock_id", whOp.ReadFields)
	}
	if !hasOutType {
		t.Fatalf("structure.warehouse.list read fields %v missing out_type_id", whOp.ReadFields)
	}

	pickOp, ok := CatalogByID["structure.operation_type.list"]
	if !ok {
		t.Fatal("structure.operation_type.list missing from CatalogByID")
	}
	if pickOp.ProviderModel != "stock.picking.type" {
		t.Fatalf("structure.operation_type.list provider model = %q, want stock.picking.type", pickOp.ProviderModel)
	}
	requiredPickFields := []string{"code", "reservation_method", "warehouse_id", "company_id", "active"}
	for _, reqField := range requiredPickFields {
		found := false
		for _, f := range pickOp.ReadFields {
			if f == reqField {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("structure.operation_type.list read fields %v missing %s", pickOp.ReadFields, reqField)
		}
	}
}
