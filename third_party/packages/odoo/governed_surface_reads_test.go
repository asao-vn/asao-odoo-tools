package odoo

import (
	"context"
	"testing"
)

// The governed product reads what the surface reaches, under each family's read capability, and
// changes nothing through it: a surface write, action or print stays standalone-only.
func TestTheGovernedProductReadsTheSurfaceAndWritesNothingThroughIt(t *testing.T) {
	read, ok := LookupGovernedOperation("crm.stage.list")
	if !ok || read.RequiredCapability != "crm.records.read" {
		t.Fatalf("crm.stage.list governed as %v (%v)", read.RequiredCapability, ok)
	}
	if curated, ok := LookupGovernedOperation("crm.lead.list"); !ok || curated.RequiredCapability != "crm.lead.read" {
		t.Fatalf("a curated read keeps its own capability, got %v", curated.RequiredCapability)
	}
	for _, id := range []string{"crm.stage.create", "crm.stage.delete", "finance.journal_entry.post", "finance.invoice.print", "structure.settings.update"} {
		if _, ok := LookupGovernedOperation(id); ok {
			t.Fatalf("%s is governed: only surface reads are", id)
		}
	}

	governed := NewAdapter(&fakeTransport{})
	governed.SurfaceReads = true
	if _, err := governed.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "crm.stage.list", Input: `{}`}); err != nil {
		t.Fatalf("a governed adapter refused a surface read: %v", err)
	}
	refused := &fakeTransport{}
	governed = NewAdapter(refused)
	governed.SurfaceReads = true
	if _, err := governed.Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "crm.stage.create", Input: `{"values":{"name":"x"}}`}); err == nil {
		t.Fatal("a governed adapter executed a surface write")
	}
	assertNoMutation(t, refused, "governed surface write")
	if _, err := NewAdapter(&fakeTransport{}).Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "crm.stage.list", Input: `{}`}); err == nil {
		t.Fatal("an adapter without SurfaceReads executed a surface read")
	}
}
