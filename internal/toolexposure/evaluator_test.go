package toolexposure

import (
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/contract"
	"github.com/asao-platform/atoc/packages/cli/internal/contractbundle"
)

// --- real ABE policy set: must expose exactly zero eligible operations ---

// TestEvaluate_RealABEPolicySet_AllNotEligible proves the fail-closed
// baseline: with the real, accepted ABE contracts (DEC-2026-0083), every
// one of the 9 operations evaluates to NotEligible, and specifically
// because aiToolEligible is false -- not because of some unrelated failure
// that would silently mask a future change to that flag. This is success,
// not a gap: a correct first MCP runtime exposes zero ABE tools.
func TestEvaluate_RealABEPolicySet_AllNotEligible(t *testing.T) {
	bundle, err := contractbundle.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	policies, err := LoadPolicies()
	if err != nil {
		t.Fatalf("LoadPolicies: %v", err)
	}

	descByID := make(map[string]contract.CommandDescriptor)
	for _, d := range bundle.Descriptors {
		descByID[d.Metadata.ID] = d
	}

	evaluator := NewPolicyEvaluator()
	abeCount := 0
	for opID, policy := range policies {
		if !strings.HasPrefix(opID, "Abe") {
			continue
		}
		abeCount++
		desc, ok := descByID[opID]
		if !ok {
			t.Fatalf("policy %q has no matching descriptor in the bundle", opID)
		}
		policyCopy := policy
		decision, reason := evaluator.Evaluate(&desc, &policyCopy, EvalContext{
			Environment: "DEV_TEST_ONLY",
			Audience:    "OPERATOR",
		})
		if decision != NotEligible {
			t.Errorf("%s: expected NotEligible, got %s (%s)", opID, decision, reason)
		}
		if !strings.Contains(reason, "aiToolEligible is false") {
			t.Errorf("%s: expected rejection reason to cite aiToolEligible=false, got %q", opID, reason)
		}
	}
	if abeCount != 9 {
		t.Fatalf("expected exactly 9 ABE policies, found %d", abeCount)
	}
}

// TestEvaluate_RealABEPolicySet_GuardianAudienceStillNotEligible specifically
// re-checks AbeResult.Explain (the one GUARDIAN-audience operation) since it
// is "the single most exposure-sensitive ABE C4 operation" per its own
// blockReason.
func TestEvaluate_RealABEPolicySet_GuardianAudienceStillNotEligible(t *testing.T) {
	bundle, err := contractbundle.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	policies, err := LoadPolicies()
	if err != nil {
		t.Fatalf("LoadPolicies: %v", err)
	}
	var desc *contract.CommandDescriptor
	for _, d := range bundle.Descriptors {
		if d.Metadata.ID == "AbeResult.Explain" {
			dc := d
			desc = &dc
		}
	}
	if desc == nil {
		t.Fatal("AbeResult.Explain descriptor not found in bundle")
	}
	policy := policies["AbeResult.Explain"]
	decision, _ := NewPolicyEvaluator().Evaluate(desc, &policy, EvalContext{
		Environment: "DEV_TEST_ONLY",
		Audience:    "GUARDIAN",
	})
	if decision != NotEligible {
		t.Fatalf("expected NotEligible, got %s", decision)
	}
}

// --- CLI availability != MCP availability ---

func TestEvaluate_CLIVisibilityDoesNotImplyMCPEligibility(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Internal", "INTERNAL")
	policy := syntheticEligiblePolicy("Synthetic.Internal")
	decision, reason := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible for INTERNAL-visibility descriptor even with aiToolEligible=true, got %s", decision)
	}
	if !strings.Contains(reason, "PUBLIC") {
		t.Fatalf("expected reason to cite the PUBLIC requirement, got %q", reason)
	}
}

// --- missing / mismatched contract inputs ---

func TestEvaluate_MissingDescriptor(t *testing.T) {
	policy := syntheticEligiblePolicy("Synthetic.Op")
	decision, _ := NewPolicyEvaluator().Evaluate(nil, &policy, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible for nil descriptor, got %s", decision)
	}
}

func TestEvaluate_MissingPolicy(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, nil, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible for nil policy, got %s", decision)
	}
}

func TestEvaluate_OperationIDMismatch(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.OtherOp")
	decision, reason := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible for operationId mismatch, got %s", decision)
	}
	if !strings.Contains(reason, "does not match") {
		t.Fatalf("expected mismatch reason, got %q", reason)
	}
}

func TestEvaluate_AIToolEligibleFalse(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	policy.Exposure.AIToolEligible = false
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible, got %s", decision)
	}
}

func TestEvaluate_MCPExposureFalse(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	policy.Exposure.MCPExposure = false
	decision, reason := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible, got %s", decision)
	}
	if !strings.Contains(reason, "mcpExposure") {
		t.Fatalf("expected reason to cite mcpExposure, got %q", reason)
	}
}

func TestEvaluate_WrongAudienceRejected(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	ctx := syntheticEvalCtx()
	ctx.Audience = "SOMEONE_ELSE"
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, &policy, ctx)
	if decision != NotEligible {
		t.Fatalf("expected NotEligible for disallowed audience, got %s", decision)
	}
}

func TestEvaluate_EnvironmentRestrictionEnforced(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	policy.Exposure.EnvironmentRestrictions = []string{"DEV_TEST_ONLY"}
	ctx := syntheticEvalCtx()
	ctx.Environment = "PRODUCTION"
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, &policy, ctx)
	if decision != NotEligible {
		t.Fatalf("expected NotEligible for disallowed environment, got %s", decision)
	}
}

func TestEvaluate_RealSensitiveDataRestrictionEnforced(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	ctx := syntheticEvalCtx()
	ctx.RealSensitiveData = true
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, &policy, ctx)
	if decision != NotEligible {
		t.Fatalf("expected NotEligible when real sensitive data is requested but not allowed, got %s", decision)
	}
}

// --- consent ---

func TestEvaluate_ConsentMissingFailsClosed(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	policy.Consent = ConsentRequirement{Required: true, Scope: "guardian consent to X", RealDataRequiresSeparateApproval: true}
	ctx := syntheticEvalCtx()
	ctx.ConsentGranted = false
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, &policy, ctx)
	if decision != ConsentRequired {
		t.Fatalf("expected ConsentRequired, got %s", decision)
	}
}

func TestEvaluate_ConsentGrantedReachesEligible(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	policy.Consent = ConsentRequirement{Required: true, Scope: "guardian consent to X", RealDataRequiresSeparateApproval: true}
	ctx := syntheticEvalCtx()
	ctx.ConsentGranted = true
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, &policy, ctx)
	if decision != Eligible {
		t.Fatalf("expected Eligible once consent is granted, got %s", decision)
	}
}

// --- human review ---

func TestEvaluate_HumanReviewRequiredPreserved(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	policy.HumanReview = HumanReviewRequirement{Required: true, ReviewerRole: "licensed_clinician_reviewer", Reason: "high-stakes composed output"}
	decision, reason := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != HumanReviewRequired {
		t.Fatalf("expected HumanReviewRequired, got %s", decision)
	}
	for _, forbidden := range []string{"approved", "review completed", "clinician approved", "medical approval"} {
		if strings.Contains(strings.ToLower(reason), forbidden) {
			t.Fatalf("HumanReviewRequired reason must never claim review happened, got %q", reason)
		}
	}
}

// --- guardian numeric probability regression ---

func TestEvaluate_GuardianNumericProbabilityGuardEnforced(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.GuardianOp", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.GuardianOp")
	policy.AllowedAudiences = []string{"GUARDIAN"}
	policy.OutputSafety.ForbidsNumericGuardianProbability = false // the regression under test
	ctx := syntheticEvalCtx()
	ctx.Audience = "GUARDIAN"
	decision, reason := NewPolicyEvaluator().Evaluate(&desc, &policy, ctx)
	if decision != NotEligible {
		t.Fatalf("expected NotEligible: guardian-facing numeric disease probability exposure must stay blocked, got %s", decision)
	}
	if !strings.Contains(reason, "forbidsNumericGuardianProbability") {
		t.Fatalf("expected reason to cite forbidsNumericGuardianProbability, got %q", reason)
	}
}

func TestEvaluate_ClinicalClaimsMustBeForbidden(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.Op", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.Op")
	policy.OutputSafety.ForbidsClinicalClaims = false
	decision, _ := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible, got %s", decision)
	}
}

// --- forbidden medical tool vocabulary ---

func TestEvaluate_ForbiddenVocabularyRejected(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.DiagnosePatient", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.DiagnosePatient")
	decision, reason := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != NotEligible {
		t.Fatalf("expected NotEligible for forbidden vocabulary in operation id, got %s", decision)
	}
	if !strings.Contains(reason, "forbidden") {
		t.Fatalf("expected reason to cite forbidden vocabulary, got %q", reason)
	}
}

// --- synthetic positive path ---

func TestEvaluate_SyntheticEligibleFixtureReachesEligible(t *testing.T) {
	desc := syntheticDescriptor("Synthetic.ReadOnlyOp", "PUBLIC")
	policy := syntheticEligiblePolicy("Synthetic.ReadOnlyOp")
	decision, reason := NewPolicyEvaluator().Evaluate(&desc, &policy, syntheticEvalCtx())
	if decision != Eligible {
		t.Fatalf("expected Eligible for the synthetic fixture, got %s (%s)", decision, reason)
	}
}

// --- fixtures shared by this file and other packages' tests are NOT
// exported here on purpose: registry_test.go and planner_test.go build
// their own, so no test package accidentally becomes a second source of
// truth for what "eligible" contract data looks like. ---

func syntheticDescriptor(id, visibility string) contract.CommandDescriptor {
	return contract.CommandDescriptor{
		APIVersion: "asao.io/v1",
		Kind:       "Command",
		Metadata: contract.CommandMetadata{
			ID:          id,
			Version:     1,
			Package:     "asao.test.synthetic",
			Description: "Synthetic read-only fixture operation for MCP gate positive-path testing only; never a real product operation.",
		},
		Spec: contract.CommandSpec{
			Path:               []string{"synthetic", "read-only-op"},
			Operation:          "describe",
			InputSchema:        "schema://synthetic/read-only-op/input/v1",
			OutputSchema:       "schema://operation/result/v1",
			RequiredCapability: "synthetic.read-only-op.read",
			ScopeTypes:         []string{"organization"},
			Risk:               "LOW",
			Visibility:         visibility,
			DataClassification: "INTERNAL_ONLY",
			Execution:          contract.ExecutionSpec{PlanRequired: false},
			AuditRequired:      true,
		},
	}
}

func syntheticEligiblePolicy(operationID string) PolicyDocument {
	return PolicyDocument{
		PolicyVersion:    1,
		OperationID:      operationID,
		AllowedAudiences: []string{"OPERATOR"},
		Consent:          ConsentRequirement{Required: false, Scope: "", RealDataRequiresSeparateApproval: true},
		HumanReview:      HumanReviewRequirement{Required: false, ReviewerRole: "", Reason: "read-only, no new clinical claim"},
		Provenance:       ProvenanceRequirement{Required: false, CitationRequired: false},
		Exposure: ExposureChannels{
			CLIEligible:             true,
			AIToolEligible:          true,
			MCPExposure:             true,
			EnvironmentRestrictions: []string{"DEV_TEST_ONLY"},
		},
		RealSensitiveDataAllowed: false,
		OutputSafety: OutputSafety{
			ForbidsNumericGuardianProbability:      true,
			ForbidsClinicalClaims:                  true,
			RiskAuthorityIsDeterministicRuleEngine: false,
		},
		StateChanging: false,
		BlockReason:   nil,
	}
}

func syntheticEvalCtx() EvalContext {
	return EvalContext{
		Environment: "DEV_TEST_ONLY",
		Audience:    "OPERATOR",
	}
}
