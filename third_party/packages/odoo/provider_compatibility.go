package odoo

import "strconv"

// ProviderCompatibilityIdentity is the complete compatibility snapshot a caller and a
// runtime must agree on before a semantic operation may execute across a process boundary.
// The three dimensions are orthogonal and each answers a different question.
//
// Security notice:
// This type carries NO secrets: no credentials, no tokens, no environment values,
// no provider password.
type ProviderCompatibilityIdentity struct {
	CatalogCount             int    `json:"catalog_count"`
	CatalogIdentity          string `json:"catalog_identity"`
	ExecutionBuildIdentity   string `json:"execution_build_identity"`
	SemanticContractIdentity string `json:"semantic_contract_identity"`
}

const (
	ReasonCatalogStale          = "RUNTIME_CATALOG_STALE"
	ReasonImplementationStale   = "RUNTIME_IMPLEMENTATION_STALE"
	ReasonSemanticContractStale = "RUNTIME_SEMANTIC_CONTRACT_STALE"
)

// CompatibilityMismatch reports the dimension and values when compatibility verification fails.
type CompatibilityMismatch struct {
	Dimension string `json:"dimension"` // "CATALOG" | "EXECUTION" | "CONTRACT" | "" when compatible
	Expected  string `json:"expected"`
	Runtime   string `json:"runtime"`
}

// Reason returns the stable reason string for a mismatch, or "" when compatible.
func (m CompatibilityMismatch) Reason() string {
	switch m.Dimension {
	case "CATALOG":
		return ReasonCatalogStale
	case "EXECUTION":
		return ReasonImplementationStale
	case "CONTRACT":
		return ReasonSemanticContractStale
	default:
		return ""
	}
}

// ComputeProviderCompatibilityIdentity populates the complete compatibility snapshot
// from existing functions: ComputeCatalogIdentity(), ExecutionBuildIdentity(), and
// ComputeSemanticContractIdentity().
func ComputeProviderCompatibilityIdentity() ProviderCompatibilityIdentity {
	cat := ComputeCatalogIdentity()
	return ProviderCompatibilityIdentity{
		CatalogCount:             cat.OperationCount,
		CatalogIdentity:          cat.SemanticIDSetSHA256,
		ExecutionBuildIdentity:   ExecutionBuildIdentity(),
		SemanticContractIdentity: ComputeSemanticContractIdentity(),
	}
}

// Compare compares an expected snapshot against a runtime one and reports
// WHICH dimension differs, in this order — membership first, then execution, then contract,
// so a caller learns the coarsest mismatch first.
//
// An EMPTY runtime value in any dimension is reported as a mismatch, not silently accepted,
// ensuring an older runtime that does not know about a dimension fails closed.
func (expected ProviderCompatibilityIdentity) Compare(runtime ProviderCompatibilityIdentity) CompatibilityMismatch {
	// 1. Catalog dimension (membership / count / sha)
	if expected.CatalogCount != runtime.CatalogCount || expected.CatalogIdentity != runtime.CatalogIdentity || runtime.CatalogCount <= 0 || runtime.CatalogIdentity == "" {
		if expected.CatalogIdentity != runtime.CatalogIdentity || runtime.CatalogIdentity == "" {
			return CompatibilityMismatch{
				Dimension: "CATALOG",
				Expected:  expected.CatalogIdentity,
				Runtime:   runtime.CatalogIdentity,
			}
		}
		return CompatibilityMismatch{
			Dimension: "CATALOG",
			Expected:  strconv.Itoa(expected.CatalogCount),
			Runtime:   strconv.Itoa(runtime.CatalogCount),
		}
	}

	// 2. Execution dimension (core execution behaviour build identity)
	if expected.ExecutionBuildIdentity != runtime.ExecutionBuildIdentity || runtime.ExecutionBuildIdentity == "" {
		return CompatibilityMismatch{
			Dimension: "EXECUTION",
			Expected:  expected.ExecutionBuildIdentity,
			Runtime:   runtime.ExecutionBuildIdentity,
		}
	}

	// 3. Contract dimension (semantic contract declarations and schemas)
	if expected.SemanticContractIdentity != runtime.SemanticContractIdentity || runtime.SemanticContractIdentity == "" {
		return CompatibilityMismatch{
			Dimension: "CONTRACT",
			Expected:  expected.SemanticContractIdentity,
			Runtime:   runtime.SemanticContractIdentity,
		}
	}

	return CompatibilityMismatch{}
}
