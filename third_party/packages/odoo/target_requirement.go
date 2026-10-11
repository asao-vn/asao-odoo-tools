package odoo

// TargetRequirement says whether an operation acts on an EXISTING provider-native record, and is
// derived entirely from catalog data (strategy + write mode) — never from the operation's name.
//
// It is what lets a caller discover, before executing anything, whether it must first name a
// canonical business entity (whose BusinessEntityBinding resolves the provider target) or whether
// the operation has no existing target at all.
type TargetRequirement string

const (
	// TargetRequirementNone: the operation has no existing provider-native target. Creates and
	// unfiltered reads. Supplying a target is a caller error the executor refuses.
	TargetRequirementNone TargetRequirement = "NONE"
	// TargetRequirementRequired: the operation cannot execute without one. Every METHOD_CALL,
	// WIZARD and DESTRUCTIVE_GUARDED operation, and every update-only FIELD_WRITE.
	TargetRequirementRequired TargetRequirement = "REQUIRED"
	// TargetRequirementOptional: the operation executes either way and the target narrows it —
	// READ operations (a target collapses the search domain to that one record) and UPSERT
	// FIELD_WRITE operations (the target is what selects create vs update).
	TargetRequirementOptional TargetRequirement = "OPTIONAL"
)

// TargetRequirementOf derives the requirement from catalog data alone. The mapping mirrors what
// the strategy handlers actually enforce (see strategies.go's TargetRequirementError sites and
// execRead's domain narrowing) — this function must never disagree with them.
func TargetRequirementOf(op Odoo19Operation) TargetRequirement {
	if op.WizardStandalone || op.ReadNewRecord {
		return TargetRequirementNone
	}
	switch op.Strategy {
	case StrategyRead:
		return TargetRequirementOptional
	case StrategyFieldWrite:
		switch op.WriteMode {
		case WriteModeCreate:
			return TargetRequirementNone
		case WriteModeUpdate:
			return TargetRequirementRequired
		default: // WriteModeUpsert
			return TargetRequirementOptional
		}
	default: // METHOD_CALL, WIZARD, DESTRUCTIVE_GUARDED
		return TargetRequirementRequired
	}
}
