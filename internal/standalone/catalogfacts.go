package standalone

import "github.com/asao-platform/atoc/packages/odoo/direct"

// CATALOG FACTS, DERIVED — NEVER DECLARED.
//
// Everything this file returns is computed from the direct package at call time. Nothing here caches, and
// nothing here writes down an operation id or a count. That is the §18 rule made structural: if the
// canonical catalog gains, loses or renames an operation, this package's view of it changes in the
// same build with no edit here, and a checker that compares the two surfaces compares two
// projections of one source rather than two lists.

// operationIDs is the canonical id list, straight from the catalog.
func operationIDs() []string { return direct.AllOperationIDs() }

// firstTargetlessRead picks a READ operation that needs no existing record, for use as a liveness
// probe. It is chosen by CONTRACT — strategy READ, and a target requirement the executor will not
// demand — rather than by name, so no operation id appears in this codebase's diagnostic path.
//
// direct.Operations() is deterministically ordered, so this returns the same operation on every
// run of a given build; a probe that silently changed which operation it used between runs would
// make a failure impossible to compare against the last one.
func firstTargetlessRead() (string, bool) {
	for _, operation := range direct.Operations() {
		if operation.Strategy == "READ" && operation.TargetRequirement != string(direct.TargetRequirementRequired) && operation.Readiness != "UNVERIFIED" {
			return operation.ID, true
		}
	}
	return "", false
}
