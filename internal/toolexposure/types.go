// Package toolexposure loads and evaluates ToolExposurePolicy documents
// (docs/architecture/asao-package-platform/schemas/tool-exposure-policy-v1.schema.json)
// -- the governance contract DEC-2026-0083 accepted alongside CommandDescriptor
// (packages/cli/internal/contract) to record an explicit, auditable AI/MCP
// exposure decision per operation, additive to and never duplicating
// CommandDescriptor's own fields (operation identity, path, schemas, risk,
// requiredCapability all stay exclusively CommandDescriptor's).
//
// This package contains no clinical/domain logic and no transport. It never
// imports services/abe-service/internal/**, never opens a database
// connection, and mirrors -- rather than trusts blindly -- the same
// invariants packages/cli/tools/validate_tool_exposure_policies.py already
// enforces offline in CI. Both must agree; if a future edit changes one
// without the other, PolicyEvaluator's own tests (see evaluator_test.go)
// against the real, checked-in ABE policy set will fail.
package toolexposure

// ConsentRequirement mirrors the schema's `consent` object.
type ConsentRequirement struct {
	Required                         bool   `json:"required"`
	Scope                            string `json:"scope"`
	RealDataRequiresSeparateApproval bool   `json:"realDataRequiresSeparateApproval"`
}

// HumanReviewRequirement mirrors the schema's `humanReview` object.
type HumanReviewRequirement struct {
	Required     bool   `json:"required"`
	ReviewerRole string `json:"reviewerRole"`
	Reason       string `json:"reason"`
}

// ProvenanceRequirement mirrors the schema's `provenance` object.
type ProvenanceRequirement struct {
	Required         bool `json:"required"`
	CitationRequired bool `json:"citationRequired"`
}

// ExposureChannels mirrors the schema's `exposure` object -- the explicit,
// authored, per-channel exposure decision. aiToolEligible/mcpExposure are
// never implied by CommandDescriptor.visibility=PUBLIC alone; this is the
// concrete data shape behind the CLI_AVAILABLE != MCP_AVAILABLE invariant.
type ExposureChannels struct {
	CLIEligible             bool     `json:"cliEligible"`
	AIToolEligible          bool     `json:"aiToolEligible"`
	MCPExposure             bool     `json:"mcpExposure"`
	EnvironmentRestrictions []string `json:"environmentRestrictions"`
}

// OutputSafety mirrors the schema's `outputSafety` object. These are
// assertions ABOUT the operation's already-defined behavior, checked by the
// evaluator/validator against reality -- never a place to encode clinical
// scoring or thresholding logic.
type OutputSafety struct {
	ForbidsNumericGuardianProbability      bool `json:"forbidsNumericGuardianProbability"`
	ForbidsClinicalClaims                  bool `json:"forbidsClinicalClaims"`
	RiskAuthorityIsDeterministicRuleEngine bool `json:"riskAuthorityIsDeterministicRuleEngine"`
}

// PolicyDocument mirrors tool-exposure-policy-v1.schema.json's top-level
// object exactly (same field set, same required-ness). It is the Go-side
// twin of what packages/cli/tools/validate_tool_exposure_policies.py already
// validates offline; this struct does not add or drop a single field.
type PolicyDocument struct {
	PolicyVersion            int                    `json:"policyVersion"`
	OperationID              string                 `json:"operationId"`
	AllowedAudiences         []string               `json:"allowedAudiences"`
	Consent                  ConsentRequirement     `json:"consent"`
	HumanReview              HumanReviewRequirement `json:"humanReview"`
	Provenance               ProvenanceRequirement  `json:"provenance"`
	Exposure                 ExposureChannels       `json:"exposure"`
	RealSensitiveDataAllowed bool                   `json:"realSensitiveDataAllowed"`
	OutputSafety             OutputSafety           `json:"outputSafety"`
	StateChanging            bool                   `json:"stateChanging"`
	BlockReason              *string                `json:"blockReason"`
}

// AllowsAudience reports whether audience appears in AllowedAudiences.
func (p *PolicyDocument) AllowsAudience(audience string) bool {
	if p == nil {
		return false
	}
	for _, a := range p.AllowedAudiences {
		if a == audience {
			return true
		}
	}
	return false
}

// AllowsEnvironment reports whether environment is permitted. An empty
// EnvironmentRestrictions list means no additional restriction (matches the
// schema's own documented semantics).
func (p *PolicyDocument) AllowsEnvironment(environment string) bool {
	if p == nil {
		return false
	}
	if len(p.Exposure.EnvironmentRestrictions) == 0 {
		return true
	}
	for _, e := range p.Exposure.EnvironmentRestrictions {
		if e == environment {
			return true
		}
	}
	return false
}
