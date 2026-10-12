// Package aerpsurface is the ONE CLI-level rendering of the canonical AERP operation surface,
// shared by both products that expose it.
//
// P5 gives this platform two executables with two deliberately different trust models: the governed
// `asao` control plane and the standalone `aerp` tool. They must present the SAME business
// operation vocabulary — same semantic ids, same input contract, same field typing, same catalog
// discovery document, same operation and provider error codes — because a caller that learned an
// operation on one must be able to run it on the other and mean the same thing. The only honest way
// to guarantee that is for there to be exactly one implementation of it, which is this package.
//
// WHAT LIVES HERE: the catalog projection and its filters, the operation-input flags, the flag →
// direct.Request translation with contract-driven value typing, and the error mapping
// for the canonical AERP errors — the ones about the OPERATION and about what the PROVIDER said.
//
// WHAT DOES NOT LIVE HERE: anything about how a surface decides WHICH Odoo to reach. Governed
// routing errors (ProviderConnection not found, route ambiguous, wrong organization) and standalone
// profile errors (profile not found, credential unavailable) are each their own surface's business,
// because neither product has any concept of the other's. See §17's dependency direction: command
// PRESENTATION composes the core, and the core never reaches back up into it.
//
// WHAT IS ALSO NOT HERE, AND MUST NEVER BE: an operation list. Every operation this package renders
// is projected at call time from direct, which projects it from packages/odoo. There is
// no second catalog, no cached copy, and no embedded id list anywhere in this package — which is
// what makes "delete an operation from the catalog and BOTH surfaces lose it" a structural fact
// rather than a test that has to remember to check. The one asymmetry is declared, not cached: a
// surface with IncludeSurface also renders packages/odoo's generated surface catalog, which only
// the standalone product does.
package aerpsurface

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// Surface is the small set of facts that differ between the two products' renderings of the same
// catalog. Everything else in this package is identical for both by construction.
type Surface struct {
	// CatalogListCommand is how a caller of THIS executable discovers the catalog, e.g.
	// "asao aerp catalog list" or "aerp catalog list". It appears in remediation text, which is the
	// one place a shared error must speak the caller's own command line.
	CatalogListCommand string
	// CatalogDescribeCommand is the same, for one operation's full contract.
	CatalogDescribeCommand string
	// DoctorCommand reports the provider fit of the Odoo a caller reaches (DEC-2026-0157), e.g.
	// "odoo-cli doctor --probe". Empty for a surface without a provider-fit check.
	DoctorCommand string
	// TargetHint states how THIS surface lets a caller name an existing record. The governed surface
	// offers canonical identity or an unmediated provider id; the standalone surface has no
	// canonical state and offers only the latter. It is remediation text, never policy — the policy
	// is derived from the shared parent catalog.
	TargetHint string
	// EntityMediation reports whether this surface exposes canonical-entity targeting at all. When
	// false, no entity input is exposed. The governed composition adds entity mediation.
	EntityMediation bool
	// BoundableEntityKinds is the closed list published as context on an unboundable-entity-kind
	// refusal. Empty on a surface with no entity mediation, where that error is unreachable.
	BoundableEntityKinds []string
	// AccessDeniedRemediation is what to tell a caller whose PROVIDER refused the credentials. The
	// two products point at different things because the credential lives in different places: a
	// ProviderConnection's credentialRef, resolved by the privileged runtime, versus a local
	// profile's own credential reference on the operator's machine.
	AccessDeniedRemediation string
	// IncludeSurface makes this surface list, describe and accept the generated surface catalog's
	// operations (direct.SurfaceOperations) as well as the curated ones. Only the standalone product
	// sets it.
	IncludeSurface bool
	// IncludeGovernedSurface makes this surface list, describe and accept the surface operations the
	// governed product executes (direct.GovernedOperations): every READ, under its family's read
	// capability (DEC-2026-0156), and the verified low- and medium-risk writes, actions and dialogs,
	// under its family's write capability (DEC-2026-0160).
	IncludeGovernedSurface bool
}

func (s Surface) operations() []direct.Operation {
	switch {
	case s.IncludeSurface:
		return direct.AllOperations()
	case s.IncludeGovernedSurface:
		return direct.GovernedOperations()
	}
	return direct.Operations()
}

func (s Surface) operationIDs() []string {
	switch {
	case s.IncludeSurface:
		return direct.AllOperationIDs()
	case s.IncludeGovernedSurface:
		return direct.GovernedOperationIDs()
	}
	return direct.OperationIDs()
}

func (s Surface) describe(operationID string) (direct.Operation, error) {
	switch {
	case s.IncludeSurface:
		return direct.DescribeAnyOperation(operationID)
	case s.IncludeGovernedSurface:
		return direct.DescribeGovernedOperation(operationID)
	}
	return direct.DescribeOperation(operationID)
}

// Provenance republishes the catalog's evidence lineage. Both surfaces publish the identical
// document because it is a property of the CATALOG, not of who is calling it.
func Provenance() direct.Provenance { return direct.CatalogProvenance() }

// UnknownOperationError names the closest catalog ids, so a caller that mistyped an operation can
// correct itself from the error alone rather than re-listing the whole catalog.
func (s Surface) UnknownOperationError(operationID string, cause error) error {
	suggestions := []string{}
	prefix, _, _ := strings.Cut(operationID, ".")
	for _, candidate := range s.operationIDs() {
		if prefix != "" && strings.HasPrefix(candidate, prefix+".") {
			suggestions = append(suggestions, candidate)
		}
	}
	err := clicore.NewError(clicore.CategoryUsage, "AERP_UNKNOWN_OPERATION",
		"no catalog operation named "+strconv.Quote(operationID)).
		WithResource("aerp.operation", operationID).
		WithCause(cause).
		WithRemediation("Run `" + s.CatalogListCommand + " --output json` for every operation this platform defines.")
	if len(suggestions) > 0 {
		return err.WithContextValue("operationsInSameDomain", suggestions)
	}
	return err
}

// CatalogListFlags are the catalog filters. Both surfaces publish them identically: a filter is a
// property of the catalog, and an agent that learned `--domain people` on one must not find it
// missing on the other.
func CatalogListFlags() []clicore.Flag {
	return []clicore.Flag{
		{Name: "domain", Kind: clicore.KindString, Description: "Filter by the operation id's leading segment, e.g. `people`, `finance`, `inventory`."},
		{Name: "strategy", Kind: clicore.KindString, Enum: []string{"READ", "FIELD_WRITE", "METHOD_CALL", "WIZARD", "DESTRUCTIVE_GUARDED", "REPORT"}, Description: "Filter by execution strategy."},
		{Name: "evidence", Kind: clicore.KindString, Enum: []string{"HISTORICAL_LIVE_EVIDENCE", "NEWER_UNVERIFIED", "SURFACE_GENERATED"}, Description: "Filter by evidence class."},
		{Name: "origin", Kind: clicore.KindString, Enum: []string{"CURATED", "SURFACE"}, Description: "Filter by origin: CURATED (researched by hand) or SURFACE (generated from Odoo's own UI, standalone-only)."},
		{Name: "readiness", Kind: clicore.KindString, Enum: []string{"CURRENT_VERIFIED", "HISTORICAL_ONLY", "UNVERIFIED"}, Description: "Filter by current release readiness."},
		{Name: "detail", Kind: clicore.KindBool, Description: "Include each operation's full input/output field contract. Verbose; prefer `catalog describe` for one operation."},
	}
}

// ListCatalog renders the filtered catalog of this surface's operations: the curated catalog, plus
// the surface catalog when IncludeSurface is set, or its governed part when IncludeGovernedSurface is.
//
// authorization is the surface's own honest statement about what governs execution of these
// operations — an ENFORCED capability on the governed surface, and the local-credential trust
// boundary on the standalone one. It is published in the same document as each operation's
// requiredCapability precisely so a machine reader can never take that field for an enforcement
// claim without also reading who, if anyone, enforces it.
func (s Surface) ListCatalog(invocation *clicore.Invocation, authorization map[string]interface{}) (interface{}, error) {
	detail, err := invocation.Bool("detail")
	if err != nil {
		return nil, err
	}
	domainFilter := invocation.String("domain")
	strategyFilter := invocation.String("strategy")
	evidenceFilter := invocation.String("evidence")
	readinessFilter := invocation.String("readiness")
	originFilter := invocation.String("origin")

	items := []interface{}{}
	for _, operation := range s.operations() {
		if (originFilter == "SURFACE" && !operation.Surface) || (originFilter == "CURATED" && operation.Surface) {
			continue
		}
		if domainFilter != "" && !strings.HasPrefix(operation.ID, domainFilter+".") {
			continue
		}
		if strategyFilter != "" && operation.Strategy != strategyFilter {
			continue
		}
		if evidenceFilter != "" && operation.EvidenceClass != evidenceFilter {
			continue
		}
		if readinessFilter != "" && operation.Readiness != readinessFilter {
			continue
		}
		if detail {
			items = append(items, operation)
			continue
		}
		item := map[string]interface{}{
			"id":                 operation.ID,
			"kind":               operation.Kind,
			"strategy":           operation.Strategy,
			"requiredCapability": operation.RequiredCapability,
			"risk":               operation.Risk,
			"targetRequirement":  operation.TargetRequirement,
			"evidenceClass":      operation.EvidenceClass,
			"readiness":          operation.Readiness,
		}
		if operation.Surface {
			item["surface"] = true
			item["providerModel"] = operation.ProviderModel
		}
		items = append(items, item)
	}
	return map[string]interface{}{
		"items":                 items,
		"count":                 len(items),
		"provenance":            direct.CatalogProvenance(),
		"capabilityEnforcement": authorization,
	}, nil
}

// CatalogDescribeFlags is the single-operation selector.
func CatalogDescribeFlags() []clicore.Flag {
	return []clicore.Flag{
		{Name: "operation", Kind: clicore.KindString, Required: true, Description: "Semantic operation id, e.g. people.employee.create."},
	}
}

// CatalogDescribe publishes one operation's complete contract.
func (s Surface) CatalogDescribe(invocation *clicore.Invocation) (interface{}, error) {
	operationID, err := invocation.RequiredString("operation")
	if err != nil {
		return nil, err
	}
	operation, describeErr := s.describe(operationID)
	if describeErr != nil {
		return nil, s.UnknownOperationError(operationID, describeErr)
	}
	return operation, nil
}

// OperationFlags are the flags that describe WHAT to run and on WHICH record — the half of an
// invocation that is pure canonical AERP semantics. They are shared verbatim so `--set`, `--values`,
// `--filter`, `--limit`, `--order`, `--confirm-impact` and target selection cannot drift between the
// two products.
//
// Deliberately absent: --organization and --connection (governed routing) and --profile (standalone
// routing). Which Odoo a call reaches is the one thing the two products decide differently, so it is
// the one thing each contributes itself.
func (s Surface) OperationFlags() []clicore.Flag {
	flags := []clicore.Flag{
		{Name: "operation", Kind: clicore.KindString, Required: true, Description: "Semantic operation id. See `" + s.CatalogListCommand + "`."},
	}
	if s.EntityMediation {
		flags = append(flags,
			clicore.Flag{Name: "entity-kind", Kind: clicore.KindString, Description: "Canonical entity kind, for the binding-mediated path. Supply with --entity-id and --purpose."},
			clicore.Flag{Name: "entity-id", Kind: clicore.KindString, Description: "Canonical entity ID. The provider-native target is resolved from its binding — you never name it."},
			clicore.Flag{Name: "purpose", Kind: clicore.KindString, Description: "Binding purpose. Part of the binding's canonical key. REQUIRED together with --entity-kind and --entity-id; not used on the --provider-target-id path."},
			clicore.Flag{
				Name: "provider-target-id", Kind: clicore.KindInt,
				Description: "UNMEDIATED provider-native record id. This is not canonical business identity; it is the preserved direct-call path for entity kinds whose canonical ownership is not yet settled. Refused together with --entity-kind/--entity-id.",
			})
	} else {
		flags = append(flags, clicore.Flag{
			Name: "provider-target-id", Kind: clicore.KindInt,
			Description: "Provider-native record id of the record to act on. This surface has no canonical business identity to mediate a target with, so this is how an existing record is named; see `--help` on the root command for what that means about this tool's trust model.",
		})
	}
	flags = append(flags,
		clicore.Flag{
			Name: "set", Kind: clicore.KindKeyValue, Repeatable: true,
			Description: "Set one semantic field, as name=value. Repeat per field. Values are typed from the operation's contract, so booleans and numbers reach the provider as booleans and numbers. Relation fields accept a name when the contract reports resolvableByName.",
		},
		clicore.Flag{
			Name: "values", Kind: clicore.KindJSON,
			Description: "Whole values object as one JSON document. Merged under --set, which wins per key. Pass the document itself, never a string containing JSON.",
		},
		clicore.Flag{Name: "filter", Kind: clicore.KindJSON, Description: "Provider search domain, as a JSON array. READ operations only."},
		clicore.Flag{Name: "fields", Kind: clicore.KindString, Repeatable: true, Description: "ACCEPTED BUT NOT APPLIED in this version: the returned field projection is fixed by the operation's catalog contract (see `" + s.CatalogDescribeCommand + "`), and passing this flag does not narrow it. Repeatable, or comma-separated."},
		clicore.Flag{Name: "limit", Kind: clicore.KindInt, Description: "Maximum records to return. READ operations only."},
		clicore.Flag{Name: "order", Kind: clicore.KindString, Description: "Provider sort order. READ operations only."},
	)
	flags = append(flags, ReadPagingFlags()...)
	return append(flags,
		clicore.Flag{
			Name: "confirm-impact", Kind: clicore.KindBool,
			Description: "Accept the dependent-record impact a destructive operation reports. Without it, a DESTRUCTIVE_GUARDED operation refuses and returns the impact it would have had.",
		},
		clicore.Flag{Name: "provider-context", Kind: clicore.KindJSON, Description: "Provider execution context, as a JSON object. Advanced; most calls need none."},
	)
}

// VerifyFlag is the post-mutation readback selector, shared so both products spell it the same way.
func VerifyFlag() clicore.Flag {
	return clicore.Flag{
		Name: "verify", Kind: clicore.KindBool,
		Description: "After a successful mutation, read the catalog-declared postcondition back from the provider. Operations with no declared postcondition report NOT_APPLICABLE — the provider accepting a call is not the same as the declared outcome being true.",
	}
}

// BuildRequest types provider-native inputs from the shared contract. Governed
// identity and organization are added by the governed command composition.
func (s Surface) BuildRequest(invocation *clicore.Invocation) (direct.Request, error) {
	operationID, err := invocation.RequiredString("operation")
	if err != nil {
		return direct.Request{}, err
	}
	if operationID == "admin.module.install" {
		return direct.Request{}, clicore.NewError(clicore.CategoryUsage, "AERP_OPERATION_REFUSED", "admin.module.install is executed only through a capability pack").
			WithRemediation("Run: asao odoo pack plan public-business, then asao odoo pack apply public-business --apply --confirm-impact")
	}
	operation, describeErr := s.describe(operationID)
	if describeErr != nil {
		return direct.Request{}, s.UnknownOperationError(operationID, describeErr)
	}

	request := direct.Request{Operation: operationID}

	targetID, err := invocation.Int64("provider-target-id", 0)
	if err != nil {
		return direct.Request{}, err
	}
	request.ProviderTargetID = targetID

	values := map[string]interface{}{}
	if err := invocation.JSON("values", &values); err != nil {
		return direct.Request{}, err
	}
	pairs, err := invocation.KeyValues("set", func(key, value string) (interface{}, error) {
		return s.coerceContractValue(operation, key, value)
	})
	if err != nil {
		return direct.Request{}, err
	}
	for key, value := range pairs {
		values[key] = value
	}
	if len(values) > 0 {
		request.Values = values
	}

	if invocation.Has("filter") {
		var filter []interface{}
		if err := invocation.JSON("filter", &filter); err != nil {
			return direct.Request{}, err
		}
		request.Domain = filter
	}
	if invocation.Has("provider-context") {
		providerContext := map[string]interface{}{}
		if err := invocation.JSON("provider-context", &providerContext); err != nil {
			return direct.Request{}, err
		}
		request.ProviderContext = providerContext
	}
	request.Fields = invocation.StringSlice("fields")
	if request.Limit, err = invocation.Int("limit", 0); err != nil {
		return direct.Request{}, err
	}
	request.Order = invocation.String("order")
	if request.Offset, err = invocation.Int("offset", 0); err != nil {
		return direct.Request{}, err
	}
	request.GroupBy = invocation.StringSlice("group-by")
	request.Aggregates = invocation.StringSlice("aggregate")
	if request.ConfirmImpact, err = invocation.Bool("confirm-impact"); err != nil {
		return direct.Request{}, err
	}
	return request, nil
}

// coerceContractValue types one --set value using the operation's declared contract, and refuses a
// field the operation does not accept BEFORE the provider is contacted — naming what it does
// accept, which is the difference between an agent that can correct itself and one that cannot.
func (s Surface) coerceContractValue(operation direct.Operation, key, value string) (interface{}, error) {
	var field *direct.Field
	accepted := make([]string, 0, len(operation.Inputs))
	for index := range operation.Inputs {
		accepted = append(accepted, operation.Inputs[index].Name)
		if operation.Inputs[index].Name == key {
			field = &operation.Inputs[index]
		}
	}
	if field == nil {
		return nil, clicore.UsageError("AERP_UNKNOWN_FIELD", "%s does not accept a field named %q", operation.ID, key).
			WithResource("aerp.operation", operation.ID).
			WithContextValue("acceptedFields", accepted).
			WithRemediation("Run `" + s.CatalogDescribeCommand + " --operation " + operation.ID + " --output json` for the full contract.")
	}
	if len(field.EnumValues) > 0 && !containsString(field.EnumValues, value) {
		return nil, clicore.UsageError("AERP_INVALID_FIELD_VALUE", "%s does not accept %q for %q", operation.ID, value, key).
			WithResource("aerp.operation", operation.ID).
			WithContextValue("field", key).
			WithContextValue("acceptedValues", field.EnumValues)
	}
	switch field.Type {
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return nil, clicore.UsageError("AERP_INVALID_FIELD_VALUE", "%s expects true or false for %q, got %q", operation.ID, key, value).
				WithContextValue("field", key)
		}
		return parsed, nil
	case "integer":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, clicore.UsageError("AERP_INVALID_FIELD_VALUE", "%s expects an integer for %q, got %q", operation.ID, key, value).
				WithContextValue("field", key)
		}
		return parsed, nil
	case "number":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, clicore.UsageError("AERP_INVALID_FIELD_VALUE", "%s expects a number for %q, got %q", operation.ID, key, value).
				WithContextValue("field", key)
		}
		return parsed, nil
	case "reference", "reference_list":
		// A relation value is left as the caller wrote it: the execution layer resolves a numeric
		// id or, when the contract declares a name field, an exact name match. Coercing it here
		// would take that choice away.
		if numeric, err := strconv.ParseInt(value, 10, 64); err == nil {
			return numeric, nil
		}
		return value, nil
	case "json":
		// A structured value, such as a journal entry's lines: the JSON document itself.
		var parsed interface{}
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return nil, clicore.UsageError("AERP_INVALID_FIELD_VALUE", "%s expects a JSON document for %q: %v", operation.ID, key, err).
				WithContextValue("field", key)
		}
		return parsed, nil
	default:
		return value, nil
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// ReadPagingFlags page a read and group it on the provider. Both surfaces and every READ operation
// command publish them identically.
func ReadPagingFlags() []clicore.Flag {
	return []clicore.Flag{
		{Name: "offset", Kind: clicore.KindInt, Description: "Skip that many records (or groups) first, to page through a read. READ operations only."},
		{Name: "group-by", Kind: clicore.KindString, Repeatable: true, Description: "Group the records on the provider by a field the read shows, or by a date field and a bucket (date_order:month, :week, :year, ...). Repeatable. The result is groups, each with its record count. READ operations only."},
		{Name: "aggregate", Kind: clicore.KindString, Repeatable: true, Description: "Aggregate a field the read shows, as field:function (amount_total:sum). Functions: sum, avg, min, max, count, count_distinct, bool_and, bool_or. Repeatable; without --group-by it aggregates over every record the filter matches. READ operations only."},
	}
}
