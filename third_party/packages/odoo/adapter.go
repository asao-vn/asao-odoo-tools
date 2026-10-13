package odoo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
)

// OperationInput is the caller-supplied payload shape for every Odoo19
// catalog operation, regardless of strategy. It is intentionally the SAME
// shape for all ~40 catalog entries — this is what makes the executor
// generic: strategy-specific behavior is driven by catalog data, not by the
// input shape.
type OperationInput struct {
	TargetID int64                  `json:"target_id,omitempty"`
	Values   map[string]interface{} `json:"values,omitempty"`
	Domain   []interface{}          `json:"domain,omitempty"`
	Fields   []string               `json:"fields,omitempty"`
	Limit    int                    `json:"limit,omitempty"`
	Order    string                 `json:"order,omitempty"`
	// Offset skips that many records (or groups) of a read: paging.
	Offset int `json:"offset,omitempty"`
	// GroupBy and Aggregates make a read answer groups instead of records, aggregated by the
	// provider: GroupBy fields ("partner_id", "date_order:month"), Aggregates "field:function"
	// ("amount_total:sum"). Only fields the read already shows can be named.
	GroupBy         []string               `json:"group_by,omitempty"`
	Aggregates      []string               `json:"aggregates,omitempty"`
	ConfirmImpact   bool                   `json:"confirm_impact,omitempty"`
	ProviderContext map[string]interface{} `json:"provider_context,omitempty"`
}

func parseInput(raw string) (OperationInput, error) {
	var in OperationInput
	if raw == "" {
		return in, nil
	}
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return in, fmt.Errorf("invalid operation input JSON: %w", err)
	}
	return in, nil
}

// Adapter is the generic Odoo 19 execution engine. It has exactly one
// handler per ExecutionStrategy (five total) and dispatches to them
// based on the catalog entry looked up by intent.Operation — it contains NO
// per-semantic-ID business logic. Adding a new operation to
// Odoo19Catalog never requires touching this file.
type Adapter struct {
	Transport   Transport
	RetryPolicy RetryPolicy
	// Surface lets this adapter execute surface-catalog operations (surface_catalog.go) as well as
	// curated ones. Only the standalone executor sets it; a governed adapter never does, so a
	// surface operation cannot be executed through the governed product at all.
	Surface bool
	// GovernedSurface lets a governed adapter execute the surface operations the governed product
	// admits (LookupGovernedOperation): every READ, under its family's read capability, and the
	// verified writes, actions and dialogs, under its family's write capability. No other surface
	// operation (WhyNotGoverned). A high-risk one also needs an approval, which the governed
	// executor (aerpbridge) checks before it reaches this adapter.
	GovernedSurface bool
	// ModuleBusyWaits are the waits before each new attempt of a module install that Odoo refused
	// while a scheduled action ran (execModuleInstall); nil means defaultModuleBusyWaits.
	ModuleBusyWaits []time.Duration
}

func NewAdapter(t Transport) *Adapter { return &Adapter{Transport: t} }

func (a *Adapter) effectiveRetryPolicy() RetryPolicy {
	if a.RetryPolicy.MaxAttempts > 0 {
		return a.RetryPolicy
	}
	return DefaultRetryPolicy()
}

func (a *Adapter) lookup(intent *ExecutionIntent) (Odoo19Operation, error) {
	entry, ok := lookupOperation(intent.Operation, a.Surface)
	if !ok && a.GovernedSurface {
		entry, ok = LookupGovernedOperation(intent.Operation)
	}
	if !ok {
		if why := WhyNotGoverned(intent.Operation); a.GovernedSurface && why != "" {
			return Odoo19Operation{}, fmt.Errorf("unknown odoo19 semantic operation %q: %s", intent.Operation, why)
		}
		return Odoo19Operation{}, fmt.Errorf("unknown odoo19 semantic operation %q", intent.Operation)
	}
	return entry, nil
}

// ValidateInvocation performs the catalog checks that require no provider
// round trip. CLI surfaces use it before opening a transport so missing or
// contradictory mutation inputs fail deterministically without weakening the
// checks Execute performs immediately before a write.
func (a *Adapter) ValidateInvocation(intent *ExecutionIntent) error {
	entry, err := a.lookup(intent)
	if err != nil {
		return err
	}
	in, err := parseInput(intent.Input)
	if err != nil {
		return err
	}
	switch entry.Strategy {
	case StrategyFieldWrite:
		switch entry.WriteMode {
		case WriteModeUpdate:
			if in.TargetID == 0 {
				return &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false}
			}
		case WriteModeCreate:
			if in.TargetID != 0 {
				return &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeCreate), TargetSupplied: true}
			}
		}
		values, err := remapFields(entry, in.Values)
		if err != nil {
			return err
		}
		for providerField := range entry.ForcedValues {
			values[providerField] = entry.ForcedValues[providerField]
		}
		if err := checkLines(entry, values); err != nil {
			return err
		}
		if in.TargetID == 0 {
			if err := missingOnCreate(entry, values); err != nil {
				return err
			}
		}
	case StrategyReport:
		if in.TargetID == 0 {
			return &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false}
		}
	case StrategyMethodCall, StrategyWizard, StrategyDestructiveGuarded:
		switch {
		case entry.WizardStandalone && in.TargetID != 0:
			return &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeCreate), TargetSupplied: true}
		case !entry.WizardStandalone && in.TargetID == 0:
			return &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false}
		}
		if entry.Strategy == StrategyWizard {
			if _, err := wizardCallerValues(entry, in.Values); err != nil {
				return err
			}
		}
		if entry.Strategy == StrategyMethodCall && (len(entry.MethodKwargs) > 0 || len(entry.MethodInputs) > 0) {
			if _, err := methodInputs(entry, in.Values); err != nil {
				return err
			}
		}
	}
	return nil
}

// Plan reports what would happen without touching the provider. Every field
// is sourced from real catalog data, never a static stub.
func (a *Adapter) Plan(ctx context.Context, intent *ExecutionIntent) (*ExecutionPlan, error) {
	entry, err := a.lookup(intent)
	if err != nil {
		return nil, err
	}
	effect := fmt.Sprintf("%s on %s via %s", entry.SemanticID, entry.ProviderModel, entry.Strategy)
	verificationStrategy := "NONE"
	if entry.Verification.Field != "" {
		verificationStrategy = fmt.Sprintf("readback:%s", entry.Verification.Field)
	}
	return &ExecutionPlan{
		InvocationID:         intent.ID,
		Provider:             "odoo19",
		Resource:             entry.ProviderModel,
		Operation:            intent.Operation,
		ProposedInput:        intent.Input,
		ExpectedEffect:       effect,
		RequiredCapability:   entry.RequiredCapability,
		Risk:                 entry.Risk,
		ConfirmationRequired: entry.Risk == RiskHigh || entry.Risk == RiskCritical || entry.Strategy == StrategyDestructiveGuarded,
		VerificationStrategy: verificationStrategy,
		DiffAvailable:        entry.Strategy == StrategyFieldWrite || entry.Strategy == StrategyDestructiveGuarded,
	}, nil
}

func fail(intent *ExecutionIntent, class ProviderErrorClass, err error) (*ExecutionResult, error) {
	classStr := string(class)
	return &ExecutionResult{
		ID:                 uuid.NewString(),
		InvocationID:       intent.ID,
		Provider:           "odoo19",
		Operation:          intent.Operation,
		Status:             "FAILED",
		VerificationStatus: "FAILED",
		ErrorClass:         &classStr,
	}, err
}

// Execute dispatches to exactly one of the five generic strategy handlers.
func (a *Adapter) Execute(ctx context.Context, intent *ExecutionIntent) (*ExecutionResult, error) {
	entry, err := a.lookup(intent)
	if err != nil {
		return fail(intent, ErrClassValidationFailed, err)
	}
	in, err := parseInput(intent.Input)
	if err != nil {
		return fail(intent, ErrClassValidationFailed, err)
	}

	var (
		data        interface{}
		providerRef string
	)

	switch entry.Strategy {
	case StrategyRead:
		err = Retry(ctx, a.effectiveRetryPolicy(), entry.Strategy, func(callCtx context.Context) error {
			var readErr error
			data, readErr = a.execRead(callCtx, entry, in)
			return readErr
		})
	case StrategyFieldWrite:
		err = Retry(ctx, a.effectiveRetryPolicy(), entry.Strategy, func(callCtx context.Context) error {
			var writeErr error
			data, providerRef, writeErr = a.execFieldWrite(callCtx, entry, in)
			return writeErr
		})
	case StrategyMethodCall:
		err = Retry(ctx, a.effectiveRetryPolicy(), entry.Strategy, func(callCtx context.Context) error {
			var callErr error
			data, callErr = a.execMethodCall(callCtx, entry, in)
			return callErr
		})
	case StrategyWizard:
		err = Retry(ctx, a.effectiveRetryPolicy(), entry.Strategy, func(callCtx context.Context) error {
			var wizErr error
			data, providerRef, wizErr = a.execWizard(callCtx, entry, in)
			return wizErr
		})
	case StrategyDestructiveGuarded:
		err = Retry(ctx, a.effectiveRetryPolicy(), entry.Strategy, func(callCtx context.Context) error {
			var destErr error
			data, destErr = a.execDestructiveGuarded(callCtx, entry, in)
			return destErr
		})
	case StrategyReport:
		data, err = a.execReport(ctx, entry, in)
	case StrategyModuleInstall:
		err = Retry(ctx, a.effectiveRetryPolicy(), entry.Strategy, func(callCtx context.Context) error {
			var modErr error
			data, modErr = a.execModuleInstall(callCtx, entry, in)
			return modErr
		})
	default:
		err = fmt.Errorf("unhandled execution strategy %q", entry.Strategy)
	}

	if err != nil {
		var se StructuredError
		if errors.As(err, &se) {
			return fail(intent, ProviderErrorClass(se.ErrorClass()), err)
		}
		if de, ok := err.(*destructiveImpactError); ok {
			classStr := string(ErrClassDestructiveImpactRequiresConfirm)
			dj, _ := json.Marshal(de)
			return &ExecutionResult{
				ID: uuid.NewString(), InvocationID: intent.ID, Provider: "odoo19", Operation: intent.Operation,
				Status: "FAILED", VerificationStatus: "FAILED", ErrorClass: &classStr,
				ProviderReadbackJSON: string(dj), Data: de,
			}, err
		}
		return fail(intent, ClassifyOdooError(err.Error()), err)
	}

	dj, _ := json.Marshal(data)
	return &ExecutionResult{
		ID:                   uuid.NewString(),
		InvocationID:         intent.ID,
		Provider:             "odoo19",
		Operation:            intent.Operation,
		Status:               "SUCCESS",
		ProviderReference:    providerRef,
		VerificationStatus:   "UNVERIFIED",
		ProviderReadbackJSON: string(dj),
		Data:                 data,
	}, nil
}

// HasVerifiablePostcondition reports whether this intent has a semantic
// postcondition that can actually be proven by reading provider state back.
//
// This is what separates "the provider accepted the call" from "the declared
// business outcome is true". Only the latter earns VERIFIED.
func (a *Adapter) HasVerifiablePostcondition(intent *ExecutionIntent) bool {
	entry, err := a.lookup(intent)
	if err != nil {
		return false
	}
	if entry.Verification.Field == "" {
		return false
	}
	in, err := parseInput(intent.Input)
	if err != nil {
		return false
	}
	// FIELD_WRITE operations like work.milestone.reach double as create and
	// update: the single Verification spec describes the UPDATE call's expected
	// post-state. A create that never set that field has nothing to assert, so
	// asserting anyway would report a false MISMATCH on a write that did
	// exactly what was asked.
	if entry.Strategy == StrategyFieldWrite && in.TargetID == 0 {
		if _, touched := in.Values[entry.Verification.Field]; !touched {
			// A forced value counts as touched: the binding supplied it, so the
			// postcondition it implies is genuinely assertable.
			if _, forced := entry.ForcedValues[entry.Verification.Field]; !forced {
				return false
			}
		}
	}
	return true
}

// Verify re-reads the target/result record via the SAME transport and
// compares the catalog-declared field against its expected value. This is
// real per-operation verification, not a static "return true".
func (a *Adapter) Verify(ctx context.Context, intent *ExecutionIntent, result *ExecutionResult) (bool, error) {
	entry, err := a.lookup(intent)
	if err != nil {
		return false, err
	}
	if !a.HasVerifiablePostcondition(intent) {
		// Nothing to assert (a plain read, a WIZARD whose effect lands on a
		// separate record, or a create that never touched the verified field).
		// ExecutionService keeps such an operation at SUCCEEDED rather than
		// calling it VERIFIED; returning true here only preserves the
		// non-failure outcome for adapters/callers that ignore that signal.
		return true, nil
	}
	in, _ := parseInput(intent.Input)
	targetID := in.TargetID
	if targetID == 0 && result != nil && result.ProviderReference != "" {
		fmt.Sscanf(result.ProviderReference, "%d", &targetID)
	}
	if targetID == 0 {
		return false, fmt.Errorf("no target id available to verify %s", entry.SemanticID)
	}
	// Every field either Verification or AdditionalVerifications reads back
	// is fetched in ONE call, so a mutation that hand-builds a provider
	// side-effect (a WIZARD) cannot be reported VERIFIED on a single
	// downstream field that happens to compute true regardless of whether
	// the operation's actual invariant held — see finance.payment.register,
	// where "payment_state == paid" alone was trivially true even against a
	// document that was never posted, because a draft move's payment_state
	// is computed the same way.
	fields := []string{entry.Verification.Field}
	for _, v := range entry.AdditionalVerifications {
		fields = append(fields, v.Field)
	}
	var recs []map[string]interface{}
	err = Retry(ctx, a.effectiveRetryPolicy(), StrategyRead, func(callCtx context.Context) error {
		var readErr error
		recs, readErr = a.Transport.Read(callCtx, entry.ProviderModel, []int64{targetID}, fields, in.ProviderContext)
		return readErr
	})
	if err != nil {
		return false, err
	}
	if len(recs) == 0 {
		return false, fmt.Errorf("verification read found no record %s(%d)", entry.ProviderModel, targetID)
	}
	rec := recs[0]
	if !entry.Verification.ExpectChange && entry.Verification.Expect != nil {
		if !valuesMatch(rec[entry.Verification.Field], entry.Verification.Expect) {
			return false, nil
		}
	}
	for _, v := range entry.AdditionalVerifications {
		if v.ExpectChange || v.Expect == nil {
			continue
		}
		if !valuesMatch(rec[v.Field], v.Expect) {
			return false, nil
		}
	}
	return true, nil
}

// valuesMatch compares an Odoo-decoded JSON value (float64 for numbers, or
// `false` for an unset many2one/empty value) against the catalog's expected
// Go literal, coercing common Odoo <-> Go JSON shape differences.
func valuesMatch(got, expect interface{}) bool {
	switch e := expect.(type) {
	case float64:
		if g, ok := got.(float64); ok {
			return g == e
		}
		return false
	case bool:
		if g, ok := got.(bool); ok {
			return g == e
		}
		return false
	case string:
		if g, ok := got.(string); ok {
			return g == e
		}
		return false
	default:
		return reflect.DeepEqual(got, expect)
	}
}

// missingOnCreate names what a create lacks: the contract's required inputs, and every field the
// operation's InputPrefixes scope. A record created without the latter falls outside the provider
// domain its own family reads, updates and deletes by (a website.media without a url under /media/
// could never be listed or removed).
func missingOnCreate(entry Odoo19Operation, values map[string]interface{}) error {
	contract := Odoo19Contracts[entry.SemanticID]
	var missing []string
	for _, field := range contract.Inputs {
		if field.Required {
			if _, exists := values[field.ProviderField]; !exists {
				missing = append(missing, field.Name)
			}
		}
	}
	for _, field := range sortedKeys(entry.InputPrefixes) {
		if _, exists := values[field]; !exists {
			missing = append(missing, field)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	accepted := contract.InputNames()
	if len(accepted) == 0 {
		accepted = append([]string(nil), entry.AllowedFields...)
	}
	return &MissingFieldError{Operation: entry.SemanticID, Missing: missing, Accepted: accepted}
}

// sortedKeys lists a map's keys in order, so a refusal names them deterministically.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
