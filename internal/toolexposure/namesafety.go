package toolexposure

import "regexp"

// ForbiddenVocabulary mirrors packages/cli/internal/runner/abe_test.go's
// forbiddenCommandVocabulary regexp (that var is unexported test-only state
// in package runner, so it cannot be imported directly -- this is a
// deliberate, documented mirror, not an independent redefinition: any
// change to the wordlist enforced by the C4 CLI naming gate should be
// applied to both). This is the single, canonical copy for the MCP/agent
// tool layer: PolicyEvaluator uses it as a hard eligibility check (see
// evaluator.go), and packages/cli/internal/mcpprojection re-uses this same
// function for its belt-and-suspenders check on the derived tool Name,
// rather than redefining the pattern a third time.
//
// The "dose" term went through two rounds of independent adversarial
// review before landing on five alternatives instead of a single
// \bdose\b:
//
//  1. Go's RE2 engine treats '_' as a word character, so \bdose\b alone
//     has no boundary between "dose" and an adjacent underscore and does
//     NOT match "dose_recommendation" (the literal forbidden example
//     named in this gate's spec) or "recommend_dose". [_-]dose and
//     dose[_-] close that gap for underscore/hyphen-joined compounds.
//  2. Those two additions still missed the no-separator/camelCase
//     transition -- "DoseRecommendation", "recommendDose", and
//     specifically "AbeDoseRecommendation.Compute" (a realistic
//     operationId following this codebase's own PascalCase-concatenated
//     naming convention, e.g. AbeSessionCompleteness.Describe) all still
//     passed. dose.?recommend and recommend.?dose close that gap the same
//     way treatment.?recommend/predict.?disease already do elsewhere in
//     this pattern: '.?' matches any single separator character OR a
//     zero-width (no-separator/camelCase) transition.
//
// \bdose\b remains for "dose" standing alone or space-separated (e.g.
// "dose recommendation"); [_-]dose/dose[_-] additionally catch a bare
// dose compound with no "recommend" present (e.g. "calculate_dose").
var forbiddenToolVocabulary = regexp.MustCompile(`(?i)diagnos|prescri|treatment.?recommend|\bdose\b|dose.?recommend|recommend.?dose|[_-]dose|dose[_-]|dosage|emergency.?clear|safe.?to.?stay.?home|predict.?disease`)

// ContainsForbiddenVocabulary reports whether s contains any forbidden
// clinical-authority vocabulary (diagnose, prescribe, treatment
// recommendation, dose/dosage recommendation, emergency clearance claims,
// disease prediction). No MCP tool name, description, or alias may weaken
// the CLI naming restriction this mirrors.
func ContainsForbiddenVocabulary(s string) bool {
	return forbiddenToolVocabulary.MatchString(s)
}
