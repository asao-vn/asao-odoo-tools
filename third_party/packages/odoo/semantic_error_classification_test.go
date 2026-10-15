package odoo

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
)

// The shared semantic core promises callers that a refusal names WHY it refused: a caller that
// cannot tell "you named a field this operation does not accept" from "the record you named is not
// eligible for this verb" from "the provider itself broke" cannot react to any of them. The
// executor therefore raises typed semantic errors, each carrying an ErrorClass, and collapsing any
// of them into a generic PROVIDER_ERROR is a regression these tests exist to catch.
//
// Every refusal below must also be proven to have refused BEFORE touching the provider: the fake
// transport records each mutating call, and a refusal that recorded one has already had its effect.

type recordedCall struct {
	Op     string
	Model  string
	Fields []string
}

type fakeTransport struct {
	searchRead  func(model string, domain interface{}, fields []string) ([]map[string]interface{}, error)
	searchCount func(model string) (int64, error)
	read        func(model string, ids []int64) ([]map[string]interface{}, error)
	callMethod  func(model, method string) (interface{}, error)

	calls     []recordedCall
	mutations []recordedCall
	// countContexts are the provider contexts SearchCount was called with, in order.
	countContexts []map[string]interface{}
	// createdValues and createdContexts are the values and provider contexts Create was called with.
	createdValues   []map[string]interface{}
	createdContexts []map[string]interface{}
	// searchContexts are the provider contexts SearchRead was called with, in order.
	searchContexts []map[string]interface{}
}

func (f *fakeTransport) record(op, model string, fields []string) {
	f.calls = append(f.calls, recordedCall{Op: op, Model: model, Fields: fields})
}

func (f *fakeTransport) recordMutation(op, model string) {
	f.mutations = append(f.mutations, recordedCall{Op: op, Model: model})
}

func (f *fakeTransport) SearchRead(_ context.Context, model string, domain interface{}, fields []string, _ int, _ string, providerCtx map[string]interface{}) ([]map[string]interface{}, error) {
	f.record("SearchRead", model, fields)
	f.searchContexts = append(f.searchContexts, providerCtx)
	if f.searchRead != nil {
		return f.searchRead(model, domain, fields)
	}
	return nil, nil
}

func (f *fakeTransport) Read(_ context.Context, model string, ids []int64, _ []string, _ map[string]interface{}) ([]map[string]interface{}, error) {
	f.record("Read", model, nil)
	if f.read != nil {
		return f.read(model, ids)
	}
	return nil, nil
}

func (f *fakeTransport) SearchCount(_ context.Context, model string, _ interface{}, providerContext map[string]interface{}) (int64, error) {
	f.record("SearchCount", model, nil)
	f.countContexts = append(f.countContexts, providerContext)
	if f.searchCount != nil {
		return f.searchCount(model)
	}
	return 0, nil
}

func (f *fakeTransport) Write(_ context.Context, model string, _ []int64, _ map[string]interface{}, _ map[string]interface{}) error {
	f.recordMutation("Write", model)
	return nil
}

func (f *fakeTransport) Create(_ context.Context, model string, values map[string]interface{}, providerContext map[string]interface{}) (int64, error) {
	f.recordMutation("Create", model)
	f.createdValues = append(f.createdValues, values)
	f.createdContexts = append(f.createdContexts, providerContext)
	return 1, nil
}

func (f *fakeTransport) CallMethod(_ context.Context, model string, _ []int64, method string, _ []interface{}, _ map[string]interface{}) (interface{}, error) {
	f.recordMutation("CallMethod:"+method, model)
	if f.callMethod != nil {
		return f.callMethod(model, method)
	}
	return nil, nil
}

func (f *fakeTransport) Unlink(_ context.Context, model string, _ []int64, _ map[string]interface{}) error {
	f.recordMutation("Unlink", model)
	return nil
}

// classOf runs one invocation and returns the semantic class the executor assigned to its failure.
func classOf(t *testing.T, tr *fakeTransport, operation, input string) (string, error) {
	t.Helper()
	res, err := NewAdapter(tr).Execute(context.Background(), &ExecutionIntent{
		ID: "test", Operation: operation, Input: input,
	})
	if err == nil {
		t.Fatalf("%s: expected a refusal, got success", operation)
	}
	var semantic interface{ ErrorClass() string }
	if errors.As(err, &semantic) {
		return semantic.ErrorClass(), err
	}
	if res != nil && res.ErrorClass != nil {
		return *res.ErrorClass, err
	}
	return string(ErrClassProviderError), err
}

func assertNoMutation(t *testing.T, tr *fakeTransport, what string) {
	t.Helper()
	if len(tr.mutations) != 0 {
		t.Fatalf("%s: refusal must not reach the provider, but recorded %v", what, tr.mutations)
	}
}

// A field the operation does not accept is a caller mistake, not a provider failure.
func TestErrorClass_UnknownFieldIsValidationFailed(t *testing.T) {
	tr := &fakeTransport{}
	got, err := classOf(t, tr, "identity.contact.create", `{"values":{"name":"x","vat":"BE123"}}`)
	if got != string(ErrClassValidationFailed) {
		t.Fatalf("want VALIDATION_FAILED, got %s (%v)", got, err)
	}
	assertNoMutation(t, tr, "unknown field")
}

// An update with no target is refused deterministically, without a provider round trip.
func TestErrorClass_MissingTargetIsValidationFailed(t *testing.T) {
	tr := &fakeTransport{}
	got, err := classOf(t, tr, "crm.lead.update", `{"values":{"name":"x"}}`)
	if got != string(ErrClassValidationFailed) {
		t.Fatalf("want VALIDATION_FAILED, got %s (%v)", got, err)
	}
	assertNoMutation(t, tr, "missing target")
}

// A caller-supplied relation value that identifies no record is NOT_FOUND naming the field --
// never an opaque provider error, which was the defect RelationNotFoundError was introduced for.
func TestErrorClass_UnresolvableRelationIsNotFound(t *testing.T) {
	tr := &fakeTransport{searchRead: func(string, interface{}, []string) ([]map[string]interface{}, error) {
		return nil, nil // no res.partner matches
	}}
	got, err := classOf(t, tr, "crm.lead.create", `{"values":{"name":"x","type":"lead","partner_id":"NO SUCH PARTNER"}}`)
	if got != string(ErrClassNotFound) {
		t.Fatalf("want NOT_FOUND, got %s (%v)", got, err)
	}
	assertNoMutation(t, tr, "unresolvable relation")
}

// ProviderDomain is the declarative subset an operation is allowed to act on. Pointing a verb at a
// record outside it is a business-precondition refusal, decided before the method is ever called.
func TestErrorClass_ProviderDomainRefusalIsBusinessPrecondition(t *testing.T) {
	tr := &fakeTransport{searchRead: func(model string, _ interface{}, _ []string) ([]map[string]interface{}, error) {
		return nil, nil // the target is a lead, so it does not match opportunityDomain
	}}
	got, err := classOf(t, tr, "crm.opportunity.win", `{"target_id":42}`)
	if got != string(ErrClassBusinessPreconditionFailed) {
		t.Fatalf("want BUSINESS_PRECONDITION_FAILED, got %s (%v)", got, err)
	}
	assertNoMutation(t, tr, "ineligible target")
}

// An archived record is still in an operation's subset: the eligibility search sees archived records,
// so an update can bring back an alert rule odoo-cli archived (found by the alerts runner, 2026-10-16).
func TestEligibilityOfATargetSeesArchivedRecords(t *testing.T) {
	tr := &fakeTransport{}
	surface := NewAdapter(tr)
	surface.Surface = true
	_, err := surface.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "structure.alert_rule.update",
		Input: `{"target_id":12,"values":{"active":true}}`})
	if err == nil {
		t.Fatal("the fake holds no record: the update must be refused")
	}
	if len(tr.searchContexts) == 0 || tr.searchContexts[0]["active_test"] != false {
		t.Fatalf("eligibility searched with %v, want active_test false", tr.searchContexts)
	}
	assertNoMutation(t, tr, "ineligible archived target")
}

// An alert action created without its rule would fall outside the domain its own family reads and
// updates by (the public conformance run of 2026-10-16 created one through the generic write runner):
// the create is refused before the provider is contacted.
func TestAlertActionCreateNeedsItsRule(t *testing.T) {
	tr := &fakeTransport{}
	surface := NewAdapter(tr)
	surface.Surface = true
	_, err := surface.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "structure.alert_action.create",
		Input: `{"values":{"name":"odoo-cli: orphan","activity_summary":"x"}}`})
	var missing *MissingFieldError
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), "base_automation_id") {
		t.Fatalf("want a missing base_automation_id, got %v", err)
	}
	assertNoMutation(t, tr, "an alert action without its rule")
}

// A destructive operation with live dependents refuses until the caller confirms the impact, and
// reports what would have been destroyed rather than destroying it.
func TestErrorClass_DestructiveGuardRefusesUnconfirmed(t *testing.T) {
	tr := &fakeTransport{
		searchCount: func(string) (int64, error) { return 3, nil },
		searchRead: func(string, interface{}, []string) ([]map[string]interface{}, error) {
			return []map[string]interface{}{{"id": 7, "display_name": "live task"}}, nil
		},
	}
	got, err := classOf(t, tr, "work.project.delete", `{"target_id":42}`)
	if got != string(ErrClassDestructiveImpactRequiresConfirm) {
		t.Fatalf("want DESTRUCTIVE_IMPACT_REQUIRES_CONFIRMATION, got %s (%v)", got, err)
	}
	assertNoMutation(t, tr, "unconfirmed destructive delete")
}

// Module install against an id the provider does not have is NOT_FOUND, not a provider error.
func TestErrorClass_ModuleInstallMissingTargetIsNotFound(t *testing.T) {
	tr := &fakeTransport{read: func(string, []int64) ([]map[string]interface{}, error) {
		return nil, nil
	}}
	got, err := classOf(t, tr, "admin.module.install", `{"target_id":999999}`)
	if got != string(ErrClassNotFound) {
		t.Fatalf("want NOT_FOUND, got %s (%v)", got, err)
	}
	assertNoMutation(t, tr, "module install on missing target")
}

// The anti-collapse assertion. Each refusal above must carry its OWN class: if a change makes the
// executor report one class for everything -- generic PROVIDER_ERROR being the usual way this
// happens -- this test fails even if every individual expectation above were relaxed.
func TestErrorClass_ClassesAreDistinctAndNeverCollapseToProviderError(t *testing.T) {
	cases := []struct {
		operation string
		input     string
		transport *fakeTransport
	}{
		{"identity.contact.create", `{"values":{"name":"x","vat":"BE123"}}`, &fakeTransport{}},
		{"crm.lead.create", `{"values":{"name":"x","type":"lead","partner_id":"NO SUCH"}}`, &fakeTransport{}},
		{"crm.opportunity.win", `{"target_id":42}`, &fakeTransport{}},
		{"work.project.delete", `{"target_id":42}`, &fakeTransport{
			searchCount: func(string) (int64, error) { return 3, nil },
		}},
	}

	seen := map[string]string{}
	for _, c := range cases {
		got, err := classOf(t, c.transport, c.operation, c.input)
		if got == string(ErrClassProviderError) {
			t.Fatalf("%s collapsed into generic PROVIDER_ERROR (%v)", c.operation, err)
		}
		if prior, dup := seen[got]; dup {
			t.Fatalf("%s and %s both report %s: distinct refusal reasons have collapsed into one class",
				prior, c.operation, got)
		}
		seen[got] = c.operation
	}
	if len(seen) != len(cases) {
		t.Fatalf("expected %d distinct error classes, got %d: %v", len(cases), len(seen), seen)
	}
}

func TestErrorClass_ConnectionRefusedIsProviderUnreachable(t *testing.T) {
	tr := &fakeTransport{searchRead: func(string, interface{}, []string) ([]map[string]interface{}, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}}

	result, err := NewAdapter(tr).Execute(context.Background(), &ExecutionIntent{
		ID: "unreachable-test", Operation: "identity.contact.list", Input: `{}`,
	})
	if err == nil {
		t.Fatal("expected provider transport failure")
	}
	if result == nil || result.ErrorClass == nil {
		t.Fatalf("expected a classified failure result, got result=%+v err=%v", result, err)
	}
	if got := *result.ErrorClass; got != string(ErrClassProviderUnreachable) {
		t.Fatalf("error class = %q, want %q (err=%v)", got, ErrClassProviderUnreachable, err)
	}
}

// ---- OptionalReadFields ------------------------------------------------------------------------

// V2_16 F5: `stock` is an optional Odoo 19 module, so fields it introduces cannot be requested
// unconditionally -- doing so makes an otherwise valid read fail outright on an Odoo without it.
// OptionalReadFields are therefore probed against ir.model.fields and included only when present.

func productListFieldsRequested(t *testing.T, providerHasField bool) []string {
	t.Helper()
	tr := &fakeTransport{}
	tr.searchRead = func(model string, _ interface{}, _ []string) ([]map[string]interface{}, error) {
		if model == "ir.model.fields" {
			rows := []map[string]interface{}{{"name": "name"}, {"name": "list_price"}}
			if providerHasField {
				rows = append(rows, map[string]interface{}{"name": "is_storable"})
			}
			return rows, nil
		}
		return []map[string]interface{}{}, nil
	}
	if _, err := NewAdapter(tr).Execute(context.Background(), &ExecutionIntent{
		ID: "test", Operation: "catalog.product.list", Input: `{}`,
	}); err != nil {
		t.Fatalf("catalog.product.list: %v", err)
	}
	for _, c := range tr.calls {
		if c.Op == "SearchRead" && c.Model == "product.template" {
			return c.Fields
		}
	}
	t.Fatalf("catalog.product.list never read product.template; calls=%v", tr.calls)
	return nil
}

func contains(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// Without the optional module the field must be omitted, so the read still succeeds.
func TestOptionalReadFields_OmittedWhenProviderLacksField(t *testing.T) {
	fields := productListFieldsRequested(t, false)
	if contains(fields, "is_storable") {
		t.Fatalf("is_storable was requested from an Odoo that does not declare it: %v", fields)
	}
	if !contains(fields, "name") {
		t.Fatalf("mandatory ReadFields must still be requested: %v", fields)
	}
}

// With the module present the field must be requested, or the projection silently loses data.
func TestOptionalReadFields_IncludedWhenProviderDeclaresField(t *testing.T) {
	fields := productListFieldsRequested(t, true)
	if !contains(fields, "is_storable") {
		t.Fatalf("is_storable must be requested when the provider declares it: %v", fields)
	}
}

// catalog.product_variant.describe requested qty_available -- a field the `stock` module computes
// onto product.product -- as a MANDATORY read field, so on any Odoo without `stock` the whole
// operation failed with VALIDATION_FAILED rather than returning the product. Found live in the
// V2.5 Gate #3 managed conformance run, where its sibling catalog.product_variant.list, which never
// requested the field, succeeded against the same provider.
func TestOptionalReadFields_ProductVariantDescribeSurvivesMissingStockModule(t *testing.T) {
	tr := &fakeTransport{}
	tr.searchRead = func(model string, _ interface{}, _ []string) ([]map[string]interface{}, error) {
		if model == "ir.model.fields" {
			// An Odoo without `stock`: product.product has no qty_available.
			return []map[string]interface{}{{"name": "name"}, {"name": "default_code"}}, nil
		}
		return []map[string]interface{}{}, nil
	}
	if _, err := NewAdapter(tr).Execute(context.Background(), &ExecutionIntent{
		ID: "test", Operation: "catalog.product_variant.describe", Input: `{"target_id":1}`,
	}); err != nil {
		t.Fatalf("catalog.product_variant.describe must succeed without the stock module: %v", err)
	}
	for _, c := range tr.calls {
		if c.Op == "SearchRead" && c.Model == "product.product" {
			if contains(c.Fields, "qty_available") {
				t.Fatalf("qty_available must not be requested from an Odoo that does not declare it: %v", c.Fields)
			}
			if !contains(c.Fields, "name") {
				t.Fatalf("mandatory ReadFields must still be requested: %v", c.Fields)
			}
			return
		}
	}
	t.Fatalf("catalog.product_variant.describe never read product.product; calls=%v", tr.calls)
}

// Odoo 19 no longer declares customer_rank and supplier_rank on res.partner in the base module.
// They remain useful on older/provider-customized deployments, so the canonical contact reads
// keep them as optional fields and must still succeed when the provider omits them.
func TestOptionalReadFields_IdentityContactSurvivesOdoo19RankFieldRemoval(t *testing.T) {
	tr := &fakeTransport{}
	tr.searchRead = func(model string, _ interface{}, fields []string) ([]map[string]interface{}, error) {
		if model == "ir.model.fields" {
			return []map[string]interface{}{
				{"name": "id"}, {"name": "name"}, {"name": "is_company"},
				{"name": "company_type"}, {"name": "email"}, {"name": "phone"},
				{"name": "parent_id"}, {"name": "child_ids"}, {"name": "commercial_partner_id"},
			}, nil
		}
		return []map[string]interface{}{{"id": int64(1), "name": "Acme"}}, nil
	}

	result, err := NewAdapter(tr).Execute(context.Background(), &ExecutionIntent{
		ID: "identity-contact-optional-fields", Operation: "identity.contact.list", Input: `{}`,
	})
	if err != nil {
		t.Fatalf("identity.contact.list must succeed without Odoo 19 rank fields: %v", err)
	}
	if result == nil || result.Status != "SUCCESS" {
		t.Fatalf("unexpected result: %+v", result)
	}

	for _, call := range tr.calls {
		if call.Op != "SearchRead" || call.Model != "res.partner" {
			continue
		}
		if contains(call.Fields, "customer_rank") || contains(call.Fields, "supplier_rank") {
			t.Fatalf("removed rank field was requested from res.partner: %v", call.Fields)
		}
		if !contains(call.Fields, "name") {
			t.Fatalf("mandatory contact field was omitted: %v", call.Fields)
		}
		return
	}
	t.Fatalf("identity.contact.list never read res.partner; calls=%v", tr.calls)
}
