package odoo

import (
	"context"
	"testing"
)

// A guarded delete counts archived dependents too. Odoo's searches skip archived records unless told
// otherwise, yet deleting a project removes its archived tasks with the live ones: a guard that
// counted only live tasks would let that delete through unconfirmed.
func TestGuardedDeleteCountsArchivedDependents(t *testing.T) {
	tr := &fakeTransport{searchCount: func(string) (int64, error) { return 0, nil }}
	if _, err := NewAdapter(tr).Execute(context.Background(), &ExecutionIntent{ID: "test", Operation: "work.project.delete", Input: `{"target_id":42}`}); err != nil {
		t.Fatal(err)
	}
	if len(tr.countContexts) != 2 {
		t.Fatalf("counted %d dependent kinds: want tasks and milestones", len(tr.countContexts))
	}
	for _, counted := range tr.countContexts {
		if counted["active_test"] != false {
			t.Fatalf("dependents counted with context %v: archived ones would be missed", counted)
		}
	}
}
