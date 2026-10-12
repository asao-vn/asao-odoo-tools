package odoo

import (
	"context"
	"strings"
	"testing"
)

// The governed product reads what the surface reaches, under each family's read capability
// (DEC-2026-0156), and changes it through the verified low- and medium-risk writes, actions and
// dialogs, under each family's write capability (DEC-2026-0160). High-risk, unverified, delete and
// print operations stay standalone-only, and the refusal says why.
func TestTheGovernedProductReadsTheSurfaceAndWritesOnlyItsSafeVerifiedPart(t *testing.T) {
	read, ok := LookupGovernedOperation("crm.stage.list")
	if !ok || read.RequiredCapability != "crm.records.read" {
		t.Fatalf("crm.stage.list governed as %v (%v)", read.RequiredCapability, ok)
	}
	if curated, ok := LookupGovernedOperation("crm.lead.list"); !ok || curated.RequiredCapability != "crm.lead.read" {
		t.Fatalf("a curated read keeps its own capability, got %v", curated.RequiredCapability)
	}
	write, ok := LookupGovernedOperation("crm.stage.create")
	if !ok || write.RequiredCapability != "crm.records.write" {
		t.Fatalf("crm.stage.create governed as %v (%v), want crm.records.write", write.RequiredCapability, ok)
	}
	refusals := map[string]string{
		"finance.journal_entry.post": "approval step", // high risk: posts to the books
		"finance.payment.post":       "approval step", // reviewed high risk
		"structure.settings.update":  "approval step",
		"crm.stage.delete":           "standalone tool",
		"finance.invoice.print":      "standalone tool",
	}
	for id, reason := range refusals {
		if _, ok := LookupGovernedOperation(id); ok {
			t.Fatalf("%s is governed", id)
		}
		if why := WhyNotGoverned(id); !strings.Contains(why, reason) {
			t.Fatalf("%s refused for %q, want it to name the %s", id, why, reason)
		}
	}
	unverified := ""
	for _, op := range SurfaceCatalog() {
		if readiness, _ := ReadinessOf(op.SemanticID); op.Strategy == StrategyFieldWrite && (op.Risk == RiskLow || op.Risk == RiskMedium) && readiness != ReadinessCurrentVerified {
			unverified = op.SemanticID
			break
		}
	}
	if unverified == "" {
		t.Fatal("test premise: no unverified low- or medium-risk surface write left to refuse")
	}
	if _, ok := LookupGovernedOperation(unverified); ok || !strings.Contains(WhyNotGoverned(unverified), "not verified") {
		t.Fatalf("unverified %s is governed or refused for %q", unverified, WhyNotGoverned(unverified))
	}

	governed := NewAdapter(&fakeTransport{})
	governed.GovernedSurface = true
	if _, err := governed.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "crm.stage.list", Input: `{}`}); err != nil {
		t.Fatalf("a governed adapter refused a surface read: %v", err)
	}
	refused := &fakeTransport{}
	governed = NewAdapter(refused)
	governed.GovernedSurface = true
	_, err := governed.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "finance.journal_entry.post", Input: `{"provider_target_id":1}`})
	if err == nil || !strings.Contains(err.Error(), "approval step") {
		t.Fatalf("a governed adapter ran a high-risk surface action, or did not say why: %v", err)
	}
	assertNoMutation(t, refused, "governed high-risk surface action")
	if _, err := NewAdapter(&fakeTransport{}).Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "crm.stage.list", Input: `{}`}); err == nil {
		t.Fatal("an adapter without GovernedSurface executed a surface read")
	}
}
