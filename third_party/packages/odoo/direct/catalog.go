// Package direct composes direct Odoo execution without ASAO service or database dependencies.
package direct

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/asao-platform/atoc/packages/odoo"
)

// ErrUnknownOperation is returned by DescribeOperation for a semantic id the catalog does not
// define. It carries no default behaviour: an unknown operation is never treated as a known one.
var ErrUnknownOperation = errors.New("odoo direct: unknown catalog operation")

// TargetRequirement says whether an operation acts on an EXISTING provider-native record, and is
// derived entirely from catalog data (strategy + write mode) — never from the operation's name.
//
// It is what lets a caller discover, before executing anything, whether it must first name a
// canonical business entity (whose BusinessEntityBinding resolves the provider target) or whether
// the operation has no existing target at all.
type TargetRequirement = odoo.TargetRequirement

const (
	// TargetRequirementNone: the operation has no existing provider-native target. Creates and
	// unfiltered reads. Supplying a target is a caller error the executor refuses.
	TargetRequirementNone = odoo.TargetRequirementNone
	// TargetRequirementRequired: the operation cannot execute without one. Every METHOD_CALL,
	// WIZARD and DESTRUCTIVE_GUARDED operation, and every update-only FIELD_WRITE.
	TargetRequirementRequired = odoo.TargetRequirementRequired
	// TargetRequirementOptional: the operation executes either way and the target narrows it —
	// READ operations (a target collapses the search domain to that one record) and UPSERT
	// FIELD_WRITE operations (the target is what selects create vs update).
	TargetRequirementOptional = odoo.TargetRequirementOptional
)

// targetRequirementOf derives the requirement from catalog data alone. The mapping mirrors what
// the shared package's strategy handlers actually enforce (see strategies.go's TargetRequirementError
// sites and execRead's domain narrowing) — this function must never disagree with them, which is
// what TestTargetRequirementMatchesExecutorBehaviour pins.
var targetRequirementOf = odoo.TargetRequirementOf

// Relation is the discoverable half of the catalog's RelationSpec: it tells a caller that a field
// points at another provider record and, when NameField is set, that a human-facing name may be
// supplied instead of an opaque id.
//
// Publishing this is what closes the historical "relation-field discoverability" debt — before it,
// the only way to learn that `--department Engineering` was accepted (rather than a numeric id)
// was to read the executor's source.
type Relation struct {
	// TargetProviderModel is the provider model the referenced record lives in. It is provider
	// metadata, published for discovery; it is not canonical business identity.
	TargetProviderModel string `json:"targetProviderModel"`
	// ResolvableByName reports whether a non-numeric value is accepted for this field.
	ResolvableByName bool `json:"resolvableByName"`
	// NameField is the provider field a supplied name is matched against, when resolvable.
	NameField string `json:"nameField,omitempty"`
	// Multi reports whether the field holds many references rather than one.
	Multi bool `json:"multi"`
}

// Field is one discoverable input or output of an operation.
type Field struct {
	// Name is the stable, caller-facing name. This is what a caller passes; it is NOT always the
	// provider's own field name.
	Name string `json:"name"`
	// ProviderField is the provider field Name resolves to. Published so a caller can correlate
	// results with provider documentation; never required as input.
	ProviderField string `json:"providerField"`
	Type          string `json:"type"`
	Required      bool   `json:"required"`
	// ProviderNative marks a name that is still just the provider's own, because the concept has
	// no researched normalization yet. Explicit, so "this is provider-shaped" is discoverable
	// rather than something a caller finds out by failing.
	ProviderNative bool     `json:"providerNative"`
	Description    string   `json:"description,omitempty"`
	EnumValues     []string `json:"enumValues,omitempty"`
	// Prefix is what every value of this input must start with (a website media's url under
	// /media/); the adapter refuses any other value before the provider is contacted.
	Prefix string `json:"prefix,omitempty"`
	// ProviderValues maps a semantic enum value to the provider's own spelling where they differ
	// (goods -> consu); a value without an entry is the provider's as it is.
	ProviderValues map[string]string `json:"providerValues,omitempty"`
	// Relation is set when this field references another provider record.
	Relation *Relation `json:"relation,omitempty"`
}

// Operation is the complete discoverable contract of one catalog operation — everything an agent
// needs to call it correctly without reading any source.
type Operation struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Strategy string `json:"strategy"`
	// Readiness is current release evidence: CURRENT_VERIFIED, HISTORICAL_ONLY or UNVERIFIED.
	Readiness string `json:"readiness"`
	// RequiredCapability is the CapabilityGrant key this operation's execution corresponds to.
	RequiredCapability string `json:"requiredCapability"`
	Risk               string `json:"risk"`
	// ResearchConfidence is how the operation was RESEARCHED (LIVE_WORKFLOW_VERIFIED /
	// LIVE_READ_VERIFIED). It is deliberately separate from EvidenceClass: research confidence is
	// not conformance evidence, and conflating them is how "58/58 live verified" gets claimed.
	ResearchConfidence string `json:"researchConfidence"`
	// EvidenceClass is where this operation stands relative to the accepted 55-operation
	// historical conformance record: HISTORICAL_LIVE_EVIDENCE or NEWER_UNVERIFIED.
	EvidenceClass string `json:"evidenceClass"`
	// ProviderModel is the provider-native model this operation acts on. Provider metadata for
	// discovery only.
	ProviderModel     string `json:"providerModel,omitempty"`
	TargetRequirement string `json:"targetRequirement"`
	// WriteMode is set for FIELD_WRITE operations (CREATE / UPDATE / UPSERT).
	WriteMode string `json:"writeMode,omitempty"`
	// RequiresImpactConfirmation is true for DESTRUCTIVE_GUARDED operations — the operations for
	// which --confirm-impact is meaningful at all.
	//
	// It is NOT "you must always pass --confirm-impact". The executor demands confirmation only
	// when the record actually has dependents; a zero-impact delete proceeds without it and records
	// impact_count_at_time_of_delete: 0. ImpactConfirmationCondition publishes that condition, so a
	// caller planning from this catalog does not learn to pass --confirm-impact unconditionally —
	// which would blunt the guard exactly where it was supposed to make someone stop and think.
	RequiresImpactConfirmation bool `json:"requiresImpactConfirmation"`
	// ImpactConfirmationCondition qualifies RequiresImpactConfirmation:
	// WHEN_DEPENDENT_RECORDS_EXIST for guarded operations, NEVER for everything else.
	ImpactConfirmationCondition string `json:"impactConfirmationCondition"`
	// HasVerifiablePostcondition reports whether success can be proven by reading provider state
	// back, rather than only by the provider accepting the call.
	HasVerifiablePostcondition bool     `json:"hasVerifiablePostcondition"`
	Inputs                     []Field  `json:"inputs"`
	Outputs                    []Field  `json:"outputs"`
	KnownSideEffects           []string `json:"knownSideEffects,omitempty"`
	RequiredModules            []string `json:"requiredModules,omitempty"`
	// Surface is true for an operation generated from Odoo's UI surface (SURFACE_GENERATED):
	// standalone-only, provider-native field names, UNVERIFIED until a conformance record names it.
	Surface bool `json:"surface,omitempty"`
	// Report is the PDF report a REPORT operation renders (sale.report_saleorder).
	Report string `json:"report,omitempty"`
	// ProviderDomain is the fixed provider domain every read of the operation is ANDed with (an
	// attachment list's res_model, a journal entry's move_type). Provider metadata, and what a
	// conformance oracle applies on its own when it selects the same records.
	ProviderDomain []interface{} `json:"providerDomain,omitempty"`
	// DefinitionHash fingerprints what the operation asks of the provider
	// (odoo.OperationDefinitionHash). A conformance run records it, and a promoted record is bound to it.
	DefinitionHash string `json:"definitionHash"`
	// ProviderCall is what an action or wizard asks of the provider: the method it calls, or how it
	// opens and confirms a dialog. Provider metadata for discovery, and what a conformance oracle
	// redoes independently on a twin record, curated operations included.
	ProviderCall *ProviderCall `json:"providerCall,omitempty"`
}

// ProviderCall is the provider-native definition of a METHOD_CALL or WIZARD operation.
type ProviderCall struct {
	Method       string `json:"method,omitempty"`
	WizardModel  string `json:"wizardModel,omitempty"`
	WizardMethod string `json:"wizardMethod,omitempty"`
	// WizardLinkField names the wizard field the target is written to (a curated wizard such as
	// sale.advance.payment.inv's sale_order_ids), and WizardLinkSourceModel the model the linked
	// records come from when they are not the target itself (account.payment.register's lines).
	WizardLinkField       string                 `json:"wizardLinkField,omitempty"`
	WizardLinkMulti       bool                   `json:"wizardLinkMulti,omitempty"`
	WizardLinkSourceModel string                 `json:"wizardLinkSourceModel,omitempty"`
	WizardOpenerMethod    string                 `json:"wizardOpenerMethod,omitempty"`
	WizardOpenerActs      bool                   `json:"wizardOpenerActs,omitempty"`
	WizardAlsoAnswers     map[string]string      `json:"wizardAlsoAnswers,omitempty"`
	WizardActiveContext   bool                   `json:"wizardActiveContext,omitempty"`
	WizardContext         map[string]interface{} `json:"wizardContext,omitempty"`
	WizardContextTarget   map[string]string      `json:"wizardContextTarget,omitempty"`
	WizardValues          map[string]interface{} `json:"wizardValues,omitempty"`
}

// FreshnessStatusNotApplicableSameProcess states that a compatibility handshake is not
// meaningful on this path: caller, bridge and semantic core are linked into the same
// binary from the same source tree, so there are not two independently-built parties whose
// identities could diverge. This is a deliberate declaration, NOT a passed check. A remote
// caller crossing a process boundary MUST use the gRPC path, which enforces
// EnsureProviderCompatible before execution.
const FreshnessStatusNotApplicableSameProcess = "NOT_APPLICABLE_SAME_PROCESS"

// ProviderCompatibilityIdentity aliases odoo.ProviderCompatibilityIdentity.
type ProviderCompatibilityIdentity = odoo.ProviderCompatibilityIdentity

// Provenance is the accepted evidence split, published verbatim.
type Provenance struct {
	CatalogOperations           int                           `json:"catalogOperations"`
	HistoricalLiveEvidence      int                           `json:"historicalLiveEvidence"`
	NewerUnverified             int                           `json:"newerUnverified"`
	NewerUnverifiedOperations   []string                      `json:"newerUnverifiedOperations"`
	HistoricalProviderVersion   string                        `json:"historicalProviderVersion"`
	HistoricalEvidenceLineage   string                        `json:"historicalEvidenceLineage"`
	CanonicalLiveReverification string                        `json:"canonicalLiveReverification"`
	ProviderFamily              string                        `json:"providerFamily"`
	ProviderEngine              string                        `json:"providerEngine"`
	FreshnessStatus             string                        `json:"freshnessStatus"`
	CompatibilityIdentity       ProviderCompatibilityIdentity `json:"compatibilityIdentity"`
	Readiness                   odoo.CatalogReadinessReport   `json:"readiness"`
}

// CatalogProvenance returns the accepted provenance record. Consumers must publish it as-is; it
// is the only statement about live verification this package makes.
func CatalogProvenance() Provenance {
	p := odoo.Odoo19CatalogProvenance
	ops := make([]string, len(p.NewerUnverifiedOperations))
	copy(ops, p.NewerUnverifiedOperations)
	return Provenance{
		CatalogOperations:           p.CatalogOperations,
		HistoricalLiveEvidence:      p.HistoricalLiveEvidence,
		NewerUnverified:             p.NewerUnverified,
		NewerUnverifiedOperations:   ops,
		HistoricalProviderVersion:   p.HistoricalProviderVersion,
		HistoricalEvidenceLineage:   p.HistoricalEvidenceLineage,
		CanonicalLiveReverification: p.CanonicalLiveReverification,
		ProviderFamily:              "aerp",
		ProviderEngine:              "odoo",
		FreshnessStatus:             FreshnessStatusNotApplicableSameProcess,
		CompatibilityIdentity:       odoo.ComputeProviderCompatibilityIdentity(),
		Readiness:                   odoo.CurrentConformanceReport(),
	}
}

// Operations projects the whole catalog, sorted by semantic id so a listing is stable across runs
// (the catalog's own slice order is authoring order, which is not a contract).
func Operations() []Operation {
	out := make([]Operation, 0, len(odoo.Catalog()))
	for _, op := range odoo.Catalog() {
		out = append(out, projectOperation(op))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// DescribeOperation projects one operation, or ErrUnknownOperation.
func DescribeOperation(semanticID string) (Operation, error) {
	op, ok := odoo.CatalogByID[semanticID]
	if !ok {
		return Operation{}, fmt.Errorf("%w: %q", ErrUnknownOperation, semanticID)
	}
	return projectOperation(op), nil
}

// GovernedOperations are the operations the governed product executes: the curated catalog, the
// surface's reads and its verified low- and medium-risk writes, actions and dialogs, each under the
// capability the governed product enforces for it (odoo.LookupGovernedOperation).
func GovernedOperations() []Operation {
	out := Operations()
	for _, op := range odoo.SurfaceCatalog() {
		if governed, ok := odoo.LookupGovernedOperation(op.SemanticID); ok {
			out = append(out, projectOperation(governed))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// DescribeGovernedOperation describes an operation the governed product executes.
func DescribeGovernedOperation(semanticID string) (Operation, error) {
	op, ok := odoo.LookupGovernedOperation(semanticID)
	if !ok {
		if why := odoo.WhyNotGoverned(semanticID); why != "" {
			return Operation{}, fmt.Errorf("%w: %q: %s", ErrUnknownOperation, semanticID, why)
		}
		return Operation{}, fmt.Errorf("%w: %q", ErrUnknownOperation, semanticID)
	}
	return projectOperation(op), nil
}

// GovernedOperationIDs lists the ids of GovernedOperations, sorted.
func GovernedOperationIDs() []string {
	operations := GovernedOperations()
	out := make([]string, len(operations))
	for i, op := range operations {
		out[i] = op.ID
	}
	return out
}

// SurfaceOperations projects the surface catalog (packages/odoo/surface_catalog.go): operations
// generated from Odoo's UI surface, which only the standalone product may list or run. Operations
// and DescribeOperation never include them, because the governed product shares those two.
func SurfaceOperations() []Operation {
	surface := odoo.SurfaceCatalog()
	out := make([]Operation, 0, len(surface))
	for _, op := range surface {
		out = append(out, projectOperation(op))
	}
	return out
}

// AllOperations is the standalone product's whole catalog: curated and surface, sorted by id.
func AllOperations() []Operation {
	out := append(Operations(), SurfaceOperations()...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// DescribeAnyOperation projects one operation of either catalog, for the standalone product.
func DescribeAnyOperation(semanticID string) (Operation, error) {
	op, ok := odoo.LookupStandaloneOperation(semanticID)
	if !ok {
		return Operation{}, fmt.Errorf("%w: %q", ErrUnknownOperation, semanticID)
	}
	return projectOperation(op), nil
}

// AllOperationIDs lists the ids of AllOperations, sorted.
func AllOperationIDs() []string {
	out := OperationIDs()
	for _, op := range odoo.SurfaceCatalog() {
		out = append(out, op.SemanticID)
	}
	sort.Strings(out)
	return out
}

// OperationIDs lists every catalog id, sorted. Useful for "did you mean" style caller correction
// without the caller having to pull the whole catalog.
func OperationIDs() []string {
	out := make([]string, 0, len(odoo.Catalog()))
	for _, op := range odoo.Catalog() {
		out = append(out, op.SemanticID)
	}
	sort.Strings(out)
	return out
}

func projectOperation(op odoo.Odoo19Operation) Operation {
	evidence, _ := odoo.Odoo19EvidenceClassOf(op.SemanticID)
	readiness, _ := odoo.ReadinessOf(op.SemanticID)
	contract := odoo.Odoo19Contracts[op.SemanticID]

	projected := Operation{
		ID:                          op.SemanticID,
		Kind:                        string(op.Kind),
		Strategy:                    string(op.Strategy),
		Readiness:                   string(readiness),
		RequiredCapability:          string(op.RequiredCapability),
		Risk:                        string(op.Risk),
		ResearchConfidence:          op.Confidence,
		EvidenceClass:               string(evidence),
		ProviderModel:               op.ProviderModel,
		TargetRequirement:           string(targetRequirementOf(op)),
		WriteMode:                   string(op.WriteMode),
		RequiresImpactConfirmation:  op.Strategy == odoo.StrategyDestructiveGuarded,
		ImpactConfirmationCondition: impactConfirmationConditionOf(op),
		HasVerifiablePostcondition:  op.Verification.Field != "" || len(op.AdditionalVerifications) > 0,
		Inputs:                      projectFields(contract.Inputs, op),
		Outputs:                     projectFields(contract.Outputs, op),
	}
	projected.Surface = odoo.IsSurfaceOperation(op.SemanticID)
	projected.Report = op.ReportName
	if len(op.ProviderDomain) > 0 {
		projected.ProviderDomain = append([]interface{}(nil), op.ProviderDomain...)
	}
	projected.DefinitionHash = odoo.OperationDefinitionHash(op)
	if op.Strategy == odoo.StrategyMethodCall || op.Strategy == odoo.StrategyWizard {
		projected.ProviderCall = &ProviderCall{
			Method: op.Method, WizardModel: op.WizardModel, WizardMethod: op.WizardMethod,
			WizardLinkField: op.WizardLinkField, WizardLinkMulti: op.WizardLinkMulti, WizardLinkSourceModel: op.WizardLinkSourceModel,
			WizardOpenerMethod: op.WizardOpenerMethod, WizardOpenerActs: op.WizardOpenerActs, WizardAlsoAnswers: op.WizardAlsoAnswers,
			WizardActiveContext: op.WizardActiveContext, WizardContext: op.WizardContext,
			WizardContextTarget: op.WizardContextTarget, WizardValues: op.WizardValues,
		}
	}
	if len(op.KnownSideEffects) > 0 {
		projected.KnownSideEffects = append([]string(nil), op.KnownSideEffects...)
	}
	if len(op.RequiredModules) > 0 {
		projected.RequiredModules = append([]string(nil), op.RequiredModules...)
	}
	// A FIELD_WRITE operation whose contract declares no inputs still accepts its catalog
	// allowlist by provider field name. Publishing the allowlist as inputs is what stops a caller
	// having to read the catalog source to learn what it may write.
	if len(projected.Inputs) == 0 && op.Strategy == odoo.StrategyFieldWrite {
		projected.Inputs = projectAllowedFields(op)
	}
	// Likewise a WIZARD operation that accepts caller values publishes them.
	if len(projected.Inputs) == 0 && op.Strategy == odoo.StrategyWizard && len(op.WizardAllowedFields) > 0 {
		projected.Inputs = projectProviderInputs(op, op.WizardAllowedFields)
	}
	// And a METHOD_CALL that takes the caller's values as keywords (a note's body).
	if len(projected.Inputs) == 0 && op.Strategy == odoo.StrategyMethodCall && len(op.MethodInputs) > 0 {
		projected.Inputs = projectProviderInputs(op, op.MethodInputs)
	}
	return projected
}

// impactConfirmationConditionOf reports WHEN --confirm-impact is actually demanded, which is what
// the bare RequiresImpactConfirmation boolean could not say.
func impactConfirmationConditionOf(op odoo.Odoo19Operation) string {
	if op.Strategy == odoo.StrategyDestructiveGuarded {
		return "WHEN_DEPENDENT_RECORDS_EXIST"
	}
	return "NEVER"
}

func projectFields(fields []odoo.SemanticField, op odoo.Odoo19Operation) []Field {
	if len(fields) == 0 {
		return []Field{}
	}
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		projected := Field{
			Name:           f.Name,
			ProviderField:  f.ProviderField,
			Type:           f.Type,
			Required:       f.Required,
			ProviderNative: f.ProviderNative,
			Description:    f.Description,
		}
		if len(f.EnumValues) > 0 {
			projected.EnumValues = append([]string(nil), f.EnumValues...)
		}
		if len(f.ProviderEnum) > 0 {
			projected.ProviderValues = make(map[string]string, len(f.ProviderEnum))
			for semantic, provider := range f.ProviderEnum {
				projected.ProviderValues[semantic] = provider
			}
		}
		projected.Relation = relationFor(op, f.ProviderField)
		out = append(out, projected)
	}
	return out
}

// projectAllowedFields is the fallback for operations whose generated contract carries no input
// list: the catalog's own AllowedFields are the accepted vocabulary, published by provider name
// and marked provider-native because that is exactly what they are.
func projectAllowedFields(op odoo.Odoo19Operation) []Field {
	return projectProviderInputs(op, op.AllowedFields)
}

// projectProviderInputs publishes provider-native inputs, typed from the operation's InputTypes and
// InputEnums when it declares them and as strings otherwise.
func projectProviderInputs(op odoo.Odoo19Operation, names []string) []Field {
	out := make([]Field, 0, len(names))
	for _, name := range names {
		fieldType := op.InputTypes[name]
		if fieldType == "" {
			fieldType = "string"
		}
		description := "Provider field accepted by this operation's catalog allowlist."
		if spec, lines := op.LineInputs[name]; lines {
			fields := make([]string, 0, len(spec.AllowedFields))
			for _, field := range spec.AllowedFields {
				if rel, related := spec.Relations[field]; related && rel.SearchField != "" {
					field += " (a " + rel.TargetModel + ", by " + rel.SearchField + ")"
				}
				fields = append(fields, field)
			}
			description = "Lines: a JSON list of objects, each with " + strings.Join(fields, ", ") + "."
		}
		out = append(out, Field{
			Name:           name,
			ProviderField:  name,
			Type:           fieldType,
			Required:       containsName(op.MethodRequiredInputs, name) || requiredOnCreate(op, name),
			ProviderNative: true,
			Description:    description,
			Relation:       relationFor(op, name),
			EnumValues:     append([]string(nil), op.InputEnums[name]...),
			Prefix:         op.InputPrefixes[name],
		})
	}
	return out
}

// requiredOnCreate reports a create input the operation holds to a prefix (InputPrefixes): the
// adapter refuses a create without it, since the record would fall outside the operation's scope.
func requiredOnCreate(op odoo.Odoo19Operation, name string) bool {
	_, scoped := op.InputPrefixes[name]
	return scoped && op.Strategy == odoo.StrategyFieldWrite && op.WriteMode == odoo.WriteModeCreate
}

func relationFor(op odoo.Odoo19Operation, providerField string) *Relation {
	spec, ok := op.Relations[providerField]
	if !ok {
		return nil
	}
	return &Relation{
		TargetProviderModel: spec.TargetModel,
		ResolvableByName:    spec.SearchField != "",
		NameField:           spec.SearchField,
		Multi:               spec.Multi,
	}
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}
