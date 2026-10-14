package toolexposure

import "testing"

// TestContainsForbiddenVocabulary_SnakeCaseDoseCompounds is a regression
// test for a real bypass an independent review found: \bdose\b alone does
// not match underscore-joined compounds because Go's RE2 engine treats '_'
// as a word character (no boundary exists between "dose" and "_"). This
// covers dose_recommendation -- the literal forbidden example named in the
// gate spec -- and its common variants.
func TestContainsForbiddenVocabulary_SnakeCaseDoseCompounds(t *testing.T) {
	mustBlock := []string{
		"dose_recommendation",
		"recommend_dose",
		"calculate_dose",
		"dose-recommendation",
		"recommend-dose",
		"dose recommendation",
		"dose",
		"Dose_Recommendation",
	}
	for _, s := range mustBlock {
		if !ContainsForbiddenVocabulary(s) {
			t.Errorf("expected %q to be blocked as forbidden vocabulary, but it was not", s)
		}
	}
}

// TestContainsForbiddenVocabulary_CamelCaseAndConcatenatedDoseCompounds is
// a second regression test: an independent re-review of the fix for
// TestContainsForbiddenVocabulary_SnakeCaseDoseCompounds found that the
// underscore/hyphen-only alternatives ([_-]dose, dose[_-]) still missed
// the no-separator/camelCase transition. AbeDoseRecommendation.Compute
// specifically mirrors this codebase's own real operationId naming
// convention (compare AbeSessionCompleteness.Describe,
// AbeSessionInput.Record), so it is a realistic collision, not a
// contrived string.
func TestContainsForbiddenVocabulary_CamelCaseAndConcatenatedDoseCompounds(t *testing.T) {
	mustBlock := []string{
		"DoseRecommendation",
		"doseRecommendation",
		"recommendDose",
		"recommenddose",
		"AbeDoseRecommendation.Compute",
	}
	for _, s := range mustBlock {
		if !ContainsForbiddenVocabulary(s) {
			t.Errorf("expected %q to be blocked as forbidden vocabulary, but it was not", s)
		}
	}
}

func TestContainsForbiddenVocabulary_FullRequiredWordlist(t *testing.T) {
	mustBlock := []string{
		"diagnose",
		"diagnosis",
		"predict_disease",
		"prescribe",
		"prescription",
		"treatment_recommendation",
		"dose_recommendation",
		"emergency_clear",
		"safe_to_stay_home",
	}
	for _, s := range mustBlock {
		if !ContainsForbiddenVocabulary(s) {
			t.Errorf("expected forbidden term %q to be blocked, but it was not", s)
		}
	}
}

func TestContainsForbiddenVocabulary_AllowsBenignNames(t *testing.T) {
	mustAllow := []string{
		"describe",
		"list",
		"session-start",
		"results.explain",
		"completeness-describe",
	}
	for _, s := range mustAllow {
		if ContainsForbiddenVocabulary(s) {
			t.Errorf("expected benign name %q to be allowed, but it was blocked", s)
		}
	}
}
