// Package odoo provides the reusable, provider-semantic Odoo 19 execution
// core. It deliberately has no dependency on ASAO identities, scopes,
// approval, persistence, audit, or evidence concerns.
package odoo

import "time"

type Capability string

type RiskClassification string

const (
	RiskLow      RiskClassification = "LOW"
	RiskMedium   RiskClassification = "MEDIUM"
	RiskHigh     RiskClassification = "HIGH"
	RiskCritical RiskClassification = "CRITICAL"
)

// Invocation is the provider-semantic subset of an ASAO execution intent.
type Invocation struct {
	ID        string
	Operation string
	Input     string
}

// ExecutionIntent is retained as a provider-semantic name for callers that
// previously used the executor directly; unlike ASAO's intent it contains no
// organization, authorization, approval, or persistence state.
type ExecutionIntent = Invocation

type ExecutionPlan struct {
	InvocationID         string
	Provider             string
	Resource             string
	Operation            string
	ProposedInput        string
	ExpectedEffect       string
	RequiredCapability   Capability
	Risk                 RiskClassification
	ConfirmationRequired bool
	VerificationStrategy string
	DiffAvailable        bool
}

type ExecutionResult struct {
	ID                   string
	InvocationID         string
	Provider             string
	Operation            string
	Status               string
	ProviderReference    string
	ExecutedAt           time.Time
	VerificationStatus   string
	ErrorClass           *string
	ProviderReadbackJSON string
	Data                 interface{}
}

// SurfaceApplicability makes each catalog operation's future-surface intent
// inspectable without activating a direct CLI runtime.
type SurfaceApplicability struct {
	DirectCLI         bool   `json:"direct_cli"`
	DirectCLIReason   string `json:"direct_cli_reason,omitempty"`
	AERPCLI           bool   `json:"aerp_cli"`
	AERPCLIReason     string `json:"aerp_cli_reason,omitempty"`
	Conformance       bool   `json:"conformance"`
	ConformanceReason string `json:"conformance_reason,omitempty"`
}

func allSurfaces() SurfaceApplicability {
	return SurfaceApplicability{DirectCLI: true, AERPCLI: true, Conformance: true}
}
