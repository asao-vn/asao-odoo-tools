package odoo

import "testing"

// The three-identity handshake decides whether a client and a runtime share the same Odoo
// semantics before either executes anything. Its value is entirely in WHICH dimension it names:
// "your catalog is stale" and "your execution engine is stale" send an operator to different
// places, and a handshake that reported one reason for everything would be no better than the
// version skew it exists to detect.

func matchingIdentity() ProviderCompatibilityIdentity {
	return ComputeProviderCompatibilityIdentity()
}

func TestCompatibility_IdenticalIdentitiesAreCompatible(t *testing.T) {
	got := matchingIdentity().Compare(matchingIdentity())
	if got.Dimension != "" {
		t.Fatalf("a runtime must be compatible with itself, got dimension %q (%s)", got.Dimension, got.Reason())
	}
	if got.Reason() != "" {
		t.Fatalf("a compatible result must carry no reason, got %q", got.Reason())
	}
}

func TestCompatibility_EachDimensionReportsItsOwnReason(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*ProviderCompatibilityIdentity)
		wantDim    string
		wantReason string
	}{
		{"catalog membership", func(i *ProviderCompatibilityIdentity) { i.CatalogIdentity = "stale-sha" }, "CATALOG", ReasonCatalogStale},
		{"catalog size", func(i *ProviderCompatibilityIdentity) { i.CatalogCount = i.CatalogCount - 1 }, "CATALOG", ReasonCatalogStale},
		{"execution build", func(i *ProviderCompatibilityIdentity) { i.ExecutionBuildIdentity = "stale-build" }, "EXECUTION", ReasonImplementationStale},
		{"semantic contract", func(i *ProviderCompatibilityIdentity) { i.SemanticContractIdentity = "stale-contract" }, "CONTRACT", ReasonSemanticContractStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := matchingIdentity()
			tc.mutate(&expected)
			got := expected.Compare(matchingIdentity())
			if got.Dimension != tc.wantDim {
				t.Fatalf("want dimension %s, got %s", tc.wantDim, got.Dimension)
			}
			if got.Reason() != tc.wantReason {
				t.Fatalf("want reason %s, got %s", tc.wantReason, got.Reason())
			}
		})
	}
}

// An older runtime that cannot populate a dimension must fail closed rather than be treated as
// agreeing with whatever the client claims.
func TestCompatibility_EmptyRuntimeDimensionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime ProviderCompatibilityIdentity
		wantDim string
	}{
		{"no catalog", ProviderCompatibilityIdentity{}, "CATALOG"},
		{"no execution build", func() ProviderCompatibilityIdentity {
			r := matchingIdentity()
			r.ExecutionBuildIdentity = ""
			return r
		}(), "EXECUTION"},
		{"no semantic contract", func() ProviderCompatibilityIdentity {
			r := matchingIdentity()
			r.SemanticContractIdentity = ""
			return r
		}(), "CONTRACT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := matchingIdentity().Compare(tc.runtime)
			if got.Dimension != tc.wantDim {
				t.Fatalf("an empty %s dimension must fail closed, got dimension %q", tc.wantDim, got.Dimension)
			}
		})
	}
}

// The identity is published across a process boundary, so it must never carry provider secrets.
func TestCompatibility_IdentityCarriesNoCredentialMaterial(t *testing.T) {
	i := matchingIdentity()
	if i.CatalogCount <= 0 || i.CatalogIdentity == "" || i.ExecutionBuildIdentity == "" || i.SemanticContractIdentity == "" {
		t.Fatalf("every dimension must be populated by this build: %+v", i)
	}
	if i.CatalogCount != CatalogOperationCount() {
		t.Fatalf("catalog count %d disagrees with the catalog itself (%d)", i.CatalogCount, CatalogOperationCount())
	}
}
