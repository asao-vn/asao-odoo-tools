package standalone

import (
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo/direct"
)

func TestAValueAskedForIsComparedAsOdooHoldsIt(t *testing.T) {
	for _, c := range []struct {
		asked, recorded interface{}
		same            bool
	}{
		{float64(13), []interface{}{float64(13), "Nhà thầu Lê Thành"}, true},
		{"13", []interface{}{float64(13), "Nhà thầu Lê Thành"}, true},
		{"Lê Thành", []interface{}{float64(13), "Nhà thầu Lê Thành"}, true},
		{float64(14), []interface{}{float64(13), "Nhà thầu Lê Thành"}, false},
		{"10", float64(10), true},
		{float64(375000), float64(370000), false},
		{"Đà Nẵng", "Đà Nẵng", true},
		{true, true, true},
		{true, false, false},
	} {
		if got := sameValue(c.asked, c.recorded); got != c.same {
			t.Errorf("%v vs %v: %v", c.asked, c.recorded, got)
		}
	}
}

// The first level stands on Odoo's answer, the operation's own postcondition and its readiness.
func TestAnOutcomeStopsAtTheFirstLevelThatDoesNotHold(t *testing.T) {
	op := direct.Operation{ID: "identity.contact.update", ProviderModel: "res.partner"}
	for _, c := range []struct {
		outcome direct.Outcome
		why     string
	}{
		{direct.Outcome{Status: "FAILED"}, "did not answer success"},
		{direct.Outcome{Status: "SUCCESS", Verification: "FAILED", Readiness: "CURRENT_VERIFIED"}, "postcondition failed"},
		{direct.Outcome{Status: "SUCCESS", Readiness: "UNVERIFIED"}, "UNVERIFIED"},
	} {
		got := judgeOutcome(nil, nil, op, direct.Request{}, c.outcome, programConfig{})
		if got.Level != LevelNotValid || !strings.Contains(got.Why, c.why) {
			t.Errorf("%+v: %+v", c.outcome, got)
		}
	}
	got := judgeOutcome(nil, nil, op, direct.Request{}, direct.Outcome{Status: "SUCCESS", Readiness: "CURRENT_VERIFIED"}, programConfig{})
	if got.Level != LevelOdooValid || !strings.Contains(got.Why, "names no record") {
		t.Errorf("no record: %+v", got)
	}
}
