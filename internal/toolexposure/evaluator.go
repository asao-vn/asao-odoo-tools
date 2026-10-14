package toolexposure

import (
	"fmt"

	"github.com/asao-platform/atoc/packages/cli/internal/contract"
)

// Decision is the deterministic outcome of PolicyEvaluator.Evaluate. There
// is no implicit fallback: any path not explicitly reaching Eligible
// returns NotEligible (or one of the two informational-but-blocking
// decisions below) with a specific reason string.
type Decision string

const (
	// Eligible means every required condition in Evaluate held: the
	// operation may be projected as an MCP tool.
	Eligible Decision = "ELIGIBLE"

	// NotEligible is the fail-closed default. Every one of the 9 real ABE
	// policies evaluates to this today (aiToolEligible=false).
	NotEligible Decision = "NOT_ELIGIBLE"

	// ConsentRequired is returned instead of Eligible/NotEligible when
	// every other check passed but the policy requires consent that the
	// caller's EvalContext does not report as granted. This is never
	// silently treated as Eligible -- missing consent is never inferred as
	// granted.
	ConsentRequired Decision = "CONSENT_REQUIRED"

	// HumanReviewRequired is informational, carried alongside Eligible for
	// the planner to encode -- it does not itself block MCP *projection*
	// (a human-review-required tool can still be listed), but the agent
	// planner (packages/cli/internal/agentplanner) must never convert it
	// into "reviewed" or "approved": see Planner.Plan.
	HumanReviewRequired Decision = "HUMAN_REVIEW_REQUIRED"
)

// EvalContext carries the caller-supplied situational facts PolicyEvaluator
// checks the policy against. It grants no authority itself -- Environment,
// Audience, ConsentGranted, and RealSensitiveData must come from an
// accepted identity/policy/consent context upstream (see agentplanner),
// never inferred by this package or invented by an LLM.
type EvalContext struct {
	Environment       string
	Audience          string
	ConsentGranted    bool
	RealSensitiveData bool
}

// PolicyEvaluator is the single, deterministic authority for whether an
// operation may be projected as an MCP/agent tool. It is intentionally
// small and side-effect free: no network call, no database, no clinical
// logic. CommandDescriptor + ToolExposurePolicy are the only inputs; this
// type never trusts CommandDescriptor.Spec.Visibility as a proxy for
// aiToolEligible/mcpExposure (CLI_AVAILABLE != MCP_AVAILABLE).
type PolicyEvaluator struct{}

// NewPolicyEvaluator returns a PolicyEvaluator. It carries no state; a
// zero-value PolicyEvaluator{} is equally valid.
func NewPolicyEvaluator() *PolicyEvaluator {
	return &PolicyEvaluator{}
}

// Evaluate returns the eligibility Decision for one operation and a
// specific, non-generic reason string. descriptor and policy may be nil to
// represent "no descriptor found" / "no policy found" respectively -- both
// are checked explicitly rather than causing a nil-pointer panic, since a
// missing contract is an expected, common, fail-closed case (every
// operation without an authored policy must reject cleanly).
func (e *PolicyEvaluator) Evaluate(descriptor *contract.CommandDescriptor, policy *PolicyDocument, ctx EvalContext) (Decision, string) {
	if descriptor == nil {
		return NotEligible, "no CommandDescriptor found for this operation"
	}
	if policy == nil {
		return NotEligible, "no ToolExposurePolicy found for this operation"
	}
	if policy.OperationID != descriptor.Metadata.ID {
		return NotEligible, fmt.Sprintf(
			"policy operationId %q does not match descriptor id %q",
			policy.OperationID, descriptor.Metadata.ID,
		)
	}
	if !policy.Exposure.AIToolEligible {
		reason := "policy.exposure.aiToolEligible is false"
		if policy.BlockReason != nil && *policy.BlockReason != "" {
			reason = fmt.Sprintf("%s: %s", reason, *policy.BlockReason)
		}
		return NotEligible, reason
	}
	if !policy.Exposure.MCPExposure {
		return NotEligible, "policy.exposure.mcpExposure is false"
	}
	// CLI_AVAILABLE != MCP_AVAILABLE, enforced structurally: aiToolEligible
	// requires the descriptor be PUBLIC (a HIDDEN/INTERNAL descriptor has
	// no representable form in the AI introspection contract by that
	// schema's own design). This never treats an INTERNAL descriptor's
	// mere CLI availability as sufficient for MCP eligibility.
	if descriptor.Spec.Visibility != "PUBLIC" {
		return NotEligible, fmt.Sprintf(
			"descriptor visibility is %q, must be PUBLIC for AI/MCP eligibility (CLI availability never implies MCP availability)",
			descriptor.Spec.Visibility,
		)
	}
	if descriptor.Spec.InputSchema == "" || descriptor.Spec.OutputSchema == "" {
		return NotEligible, "descriptor is missing a resolvable input or output schema reference"
	}
	if ctx.Environment == "" {
		return NotEligible, "no environment supplied in evaluation context"
	}
	if !policy.AllowsEnvironment(ctx.Environment) {
		return NotEligible, fmt.Sprintf(
			"environment %q is not in policy.exposure.environmentRestrictions %v",
			ctx.Environment, policy.Exposure.EnvironmentRestrictions,
		)
	}
	if ctx.Audience == "" {
		return NotEligible, "no audience supplied in evaluation context"
	}
	if !policy.AllowsAudience(ctx.Audience) {
		return NotEligible, fmt.Sprintf(
			"audience %q is not in policy.allowedAudiences %v",
			ctx.Audience, policy.AllowedAudiences,
		)
	}
	if ctx.RealSensitiveData && !policy.RealSensitiveDataAllowed {
		return NotEligible, "real sensitive data requested but policy.realSensitiveDataAllowed is false"
	}
	if "GUARDIAN" == ctx.Audience && !policy.OutputSafety.ForbidsNumericGuardianProbability {
		return NotEligible, "GUARDIAN audience requires outputSafety.forbidsNumericGuardianProbability=true"
	}
	if !policy.OutputSafety.ForbidsClinicalClaims {
		return NotEligible, "policy.outputSafety.forbidsClinicalClaims is false"
	}
	if ContainsForbiddenVocabulary(descriptor.Metadata.ID) ||
		ContainsForbiddenVocabulary(policyPathString(descriptor)) ||
		ContainsForbiddenVocabulary(descriptor.Metadata.Description) {
		return NotEligible, "operation id, path, or description contains forbidden clinical-authority vocabulary"
	}
	if policy.Consent.Required && !ctx.ConsentGranted {
		return ConsentRequired, "policy.consent.required is true and consent was not granted in this context"
	}
	if policy.HumanReview.Required {
		return HumanReviewRequired, policy.HumanReview.Reason
	}
	return Eligible, "all eligibility conditions satisfied"
}

func policyPathString(descriptor *contract.CommandDescriptor) string {
	out := ""
	for i, p := range descriptor.Spec.Path {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
