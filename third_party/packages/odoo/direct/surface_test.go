package direct_test

import (
	"errors"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// TestGovernedProjectionsNeverSeeTheSurfaceCatalog: Operations, OperationIDs and DescribeOperation
// are shared with the governed product (aerpbridge re-exports them), so none of them may expose a
// surface operation. Only the standalone-only projections do.
func TestGovernedProjectionsNeverSeeTheSurfaceCatalog(t *testing.T) {
	for _, operation := range direct.Operations() {
		if operation.Surface {
			t.Fatalf("Operations() exposes surface operation %s", operation.ID)
		}
	}
	surface := direct.SurfaceOperations()
	if len(surface) == 0 {
		t.Fatal("no surface operations")
	}
	probe := surface[0].ID
	if _, err := direct.DescribeOperation(probe); !errors.Is(err, direct.ErrUnknownOperation) {
		t.Fatalf("DescribeOperation(%s) err = %v; the governed projection must not know it", probe, err)
	}
	described, err := direct.DescribeAnyOperation(probe)
	readiness, _ := odoo.ReadinessOf(probe)
	if err != nil || !described.Surface || described.EvidenceClass != string(odoo.Odoo19EvidenceSurfaceGenerated) || described.Readiness != string(readiness) {
		t.Fatalf("DescribeAnyOperation(%s) = %+v, %v; want readiness %s", probe, described, err, readiness)
	}
	if got, want := len(direct.AllOperations()), len(direct.Operations())+len(surface); got != want {
		t.Fatalf("AllOperations has %d operations, want %d", got, want)
	}
	if got := len(direct.AllOperationIDs()); got != len(direct.AllOperations()) {
		t.Fatalf("AllOperationIDs has %d ids for %d operations", got, len(direct.AllOperations()))
	}
}
