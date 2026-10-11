package odoo

import (
	"sort"
	"strings"
)

// Conformance status codes recorded during conformance test runs.
const (
	StatusCliReadPass            = "CLI_READ_PASS"
	StatusCliReadFailed          = "CLI_READ_FAILED"
	StatusSemanticReadbackPass   = "SEMANTIC_READBACK_PASS"
	StatusSemanticReadbackFailed = "SEMANTIC_READBACK_FAILED"
	StatusCliMutationFailed      = "CLI_MUTATION_FAILED"
	StatusUnsupportedModule      = "UNSUPPORTED_MODULE"
	StatusNotRunNoNamedFixture   = "NOT_RUN_NO_NAMED_FIXTURE"
	StatusRoutingIsolationPass   = "ROUTING_ISOLATION_PASS"
	StatusRoutingIsolationFailed = "ROUTING_ISOLATION_FAILED"
	StatusRoutingOracleInvalid   = "ROUTING_ORACLE_INVALID"
	StatusLabSetupFailed         = "LAB_SETUP_FAILED"
)

// Stable reasons for operations that cannot be safely exercised without
// dedicated business fixtures or provider runtime modifications.
const (
	ReasonDestructiveGuardedDisabled     = "destructive operation disabled: requires explicit safe fixture and impact proof"
	ReasonModuleInstallDisabled          = "module installation mutates runtime schema; disabled in conformance runner"
	ReasonStockPickingFixtureNeeded      = "requires an upstream stock.picking business fixture and postcondition"
	ReasonSalesWorkflowFixtureNeeded     = "requires upstream sales order business workflow fixture"
	ReasonPurchaseWorkflowFixtureNeeded  = "requires upstream purchase order business workflow fixture"
	ReasonInvoiceWorkflowFixtureNeeded   = "requires upstream accounting invoice/journal configuration fixture"
	ReasonLeadOpportunityFixtureNeeded   = "requires isolated lost-reason business fixture"
	ReasonOpportunityPipelineStageNeeded = "requires upstream CRM pipeline stage business fixture"
	ReasonMilestoneWorkflowFixtureNeeded = "requires project milestone business workflow fixture"
	ReasonDefaultBusinessFixtureNeeded   = "mutation or target-specific execution requires an explicit business workflow fixture and provider-side oracle"
	ReasonPendingSemanticOracle          = "safe read completed; pending independent semantic oracle"
)

// BaselineSafeConformanceMutations lists the 8 initial reviewed safe mutation operations.
var BaselineSafeConformanceMutations = map[string]bool{
	"identity.contact.create":  true,
	"identity.contact.update":  true,
	"people.department.create": true,
	"people.employee.create":   true,
	"people.employee.update":   true,
	"people.employee.offboard": true,
	"catalog.product.create":   true,
	"catalog.product.update":   true,
}

// ExpandedSafeConformanceMutations lists the non-destructive mutation workflows
// added in the expanded conformance runner slice.
var ExpandedSafeConformanceMutations = map[string]bool{
	// CRM lead create/update
	"crm.lead.create": true,
	"crm.lead.update": true,

	// Sales quotation/order draft create/update
	"sales.quotation.create":   true,
	"sales.quotation.line.add": true,
	"sales.quotation.update":   true,
	"sales.order.line.update":  true,

	// Procurement RFQ/order draft create/update
	"procurement.rfq.create":        true,
	"procurement.rfq.line.add":      true,
	"procurement.rfq.update":        true,
	"procurement.order.line.update": true,

	// Work project/task/milestone create/update/reach
	"work.project.create":   true,
	"work.project.update":   true,
	"work.task.create":      true,
	"work.task.update":      true,
	"work.milestone.create": true,
	"work.milestone.reach":  true,

	// Schedule event create/update
	"schedule.event.create": true,
	"schedule.event.update": true,
}

// SafeConformanceMutations lists all operation IDs that have deterministic named fixtures
// and independent provider-side postcondition readback implemented in the conformance suite.
var SafeConformanceMutations = func() map[string]bool {
	m := make(map[string]bool, len(BaselineSafeConformanceMutations)+len(ExpandedSafeConformanceMutations))
	for k, v := range BaselineSafeConformanceMutations {
		m[k] = v
	}
	for k, v := range ExpandedSafeConformanceMutations {
		m[k] = v
	}
	return m
}()

// IsSafeConformanceMutation reports whether an operation has a deterministic named fixture
// and independent semantic readback implemented in the conformance runner.
func IsSafeConformanceMutation(operationID string) bool {
	return SafeConformanceMutations[operationID]
}

// IsBaselineSafeConformanceMutation reports whether an operation belongs to the initial 8 reviewed records.
func IsBaselineSafeConformanceMutation(operationID string) bool {
	return BaselineSafeConformanceMutations[operationID]
}

// IsExpandedSafeConformanceMutation reports whether an operation belongs to the expanded conformance runner slice.
func IsExpandedSafeConformanceMutation(operationID string) bool {
	return ExpandedSafeConformanceMutations[operationID]
}

// ConformanceNotRunReason returns the stable, deterministic reason why an operation
// is recorded as NOT_RUN_NO_NAMED_FIXTURE when it cannot be exercised without
// a complex business fixture or runtime modification.
func ConformanceNotRunReason(operationID string) string {
	op, ok := CatalogByID[operationID]
	if !ok {
		return "unknown catalog operation"
	}
	if op.Strategy == StrategyDestructiveGuarded {
		return ReasonDestructiveGuardedDisabled
	}
	if op.Strategy == StrategyModuleInstall {
		return ReasonModuleInstallDisabled
	}
	switch {
	case strings.HasPrefix(operationID, "inventory.transfer."):
		return ReasonStockPickingFixtureNeeded
	case operationID == "sales.invoice.create" || operationID == "procurement.bill.create":
		return ReasonInvoiceWorkflowFixtureNeeded
	case strings.HasPrefix(operationID, "sales."):
		return ReasonSalesWorkflowFixtureNeeded
	case strings.HasPrefix(operationID, "procurement."):
		return ReasonPurchaseWorkflowFixtureNeeded
	case strings.HasPrefix(operationID, "finance."):
		return ReasonInvoiceWorkflowFixtureNeeded
	case operationID == "crm.opportunity.convert" || operationID == "crm.opportunity.win":
		return ReasonOpportunityPipelineStageNeeded
	case operationID == "crm.opportunity.lose":
		return ReasonLeadOpportunityFixtureNeeded
	case strings.HasPrefix(operationID, "work.milestone."):
		return ReasonMilestoneWorkflowFixtureNeeded
	default:
		return ReasonDefaultBusinessFixtureNeeded
	}
}

// ConformanceCategory classifies how an operation is exercised or gated in conformance tests.
type ConformanceCategory string

const (
	CategoryRead                 ConformanceCategory = "READ"
	CategoryBaselineSafeMutation ConformanceCategory = "BASELINE_SAFE_MUTATION"
	CategoryExpandedSafeMutation ConformanceCategory = "EXPANDED_SAFE_MUTATION"
	CategoryBlockedMutation      ConformanceCategory = "BLOCKED_MUTATION"
)

// ConformanceCategoryOf returns the testing category for any canonical catalog operation.
func ConformanceCategoryOf(operationID string) (ConformanceCategory, bool) {
	op, ok := CatalogByID[operationID]
	if !ok {
		return "", false
	}
	if op.Strategy == StrategyRead {
		return CategoryRead, true
	}
	if IsBaselineSafeConformanceMutation(operationID) {
		return CategoryBaselineSafeMutation, true
	}
	if IsExpandedSafeConformanceMutation(operationID) {
		return CategoryExpandedSafeMutation, true
	}
	return CategoryBlockedMutation, true
}

// SafeConformanceMutationIDs returns the sorted list of all 26 operation IDs that have
// deterministic named fixtures and independent provider-side semantic readback.
func SafeConformanceMutationIDs() []string {
	ids := make([]string, 0, len(SafeConformanceMutations))
	for id := range SafeConformanceMutations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// BlockedMutationIDs returns the sorted list of all 22 mutation operation IDs that
// are intentionally kept NOT_RUN_NO_NAMED_FIXTURE or guarded during automated conformance runs.
func BlockedMutationIDs() []string {
	ids := make([]string, 0, len(CatalogByID))
	for id, op := range CatalogByID {
		if op.Strategy == StrategyRead || IsSafeConformanceMutation(id) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SafeConformanceReads lists all READ operation IDs that have deterministic provider-side
// oracles implemented in the conformance runner suite.
var SafeConformanceReads = map[string]bool{
	// Structural & System reads (base, stock)
	"structure.company.list":   true,
	"structure.location.list":  true,
	"structure.module.list":    true,
	"structure.warehouse.list": true,

	// Website reads (website)
	"structure.website.list": true,
	"website.site.describe":  true,
	"website.page.list":      true,
	"website.menu.list":      true,

	// Product variant reads (product)
	"catalog.product_variant.list":     true,
	"catalog.product_variant.describe": true,

	// CRM lead reads (crm)
	"crm.lead.list":     true,
	"crm.lead.describe": true,

	// Department reads (hr)
	"people.department.list":     true,
	"people.department.describe": true,

	// Calendar event reads (calendar)
	"schedule.event.list":     true,
	"schedule.event.describe": true,

	// Work project/task/milestone reads (project)
	"work.project.list":       true,
	"work.project.describe":   true,
	"work.task.list":          true,
	"work.task.describe":      true,
	"work.milestone.list":     true,
	"work.milestone.describe": true,

	// Procurement RFQ/order reads (purchase)
	"procurement.rfq.list":       true,
	"procurement.order.describe": true,

	// Sales quotation/order reads (sale)
	"sales.quotation.list": true,
	"sales.order.describe": true,
}

// IsSafeConformanceRead reports whether an operation has a deterministic provider-side
// oracle implemented in the conformance runner.
func IsSafeConformanceRead(operationID string) bool {
	return SafeConformanceReads[operationID]
}

// SafeConformanceReadIDs returns the sorted list of all READ operation IDs that have
// deterministic provider-side oracles.
func SafeConformanceReadIDs() []string {
	ids := make([]string, 0, len(SafeConformanceReads))
	for id := range SafeConformanceReads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
