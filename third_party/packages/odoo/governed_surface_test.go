package odoo

import (
	"context"
	"strings"
	"testing"
)

// The governed product reads what the surface reaches, under each family's read capability
// (DEC-2026-0156), and changes it through the verified writes, actions and dialogs, under each
// family's write capability (DEC-2026-0160). A high-risk one is governed too and requires a
// colleague's approval (DEC-2026-0161). Unverified, delete and print operations stay
// standalone-only, and the refusal says why.
func TestTheGovernedProductReadsTheSurfaceAndWritesOnlyItsSafeVerifiedPart(t *testing.T) {
	read, ok := LookupGovernedOperation("crm.stage.list")
	if !ok || read.RequiredCapability != "crm.records.read" {
		t.Fatalf("crm.stage.list governed as %v (%v)", read.RequiredCapability, ok)
	}
	if curated, ok := LookupGovernedOperation("crm.lead.list"); !ok || curated.RequiredCapability != "crm.lead.read" {
		t.Fatalf("a curated read keeps its own capability, got %v", curated.RequiredCapability)
	}
	write, ok := LookupGovernedOperation("crm.stage.create")
	if !ok || write.RequiredCapability != "crm.records.write" || RequiresApproval(write) {
		t.Fatalf("crm.stage.create governed as %v (%v, approval %v), want crm.records.write without approval", write.RequiredCapability, ok, RequiresApproval(write))
	}
	for id, capability := range map[string]string{
		"finance.journal_entry.post": "finance.records.write",   // posts to the books
		"finance.payment.post":       "finance.records.write",   // reviewed high risk
		"structure.company.update":   "structure.records.write", // identity and fiscal settings
	} {
		op, ok := LookupGovernedOperation(id)
		if !ok || string(op.RequiredCapability) != capability || !RequiresApproval(op) {
			t.Fatalf("high-risk %s governed=%v as %v, approval %v; want %s behind an approval", id, ok, op.RequiredCapability, RequiresApproval(op), capability)
		}
		if why := WhyNotGoverned(id); why != "" {
			t.Fatalf("high-risk %s is governed but WhyNotGoverned says %q", id, why)
		}
	}
	if curated, ok := LookupGovernedOperation("finance.invoice.post"); !ok || !RequiresApproval(curated) {
		t.Fatalf("the curated high-risk finance.invoice.post must require an approval too (governed %v)", ok)
	}
	for id, reason := range map[string]string{
		"crm.stage.delete":      "standalone tool",
		"finance.invoice.print": "standalone tool",
	} {
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
	if _, err := NewAdapter(&fakeTransport{}).Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "crm.stage.list", Input: `{}`}); err == nil {
		t.Fatal("an adapter without GovernedSurface executed a surface read")
	}
}
