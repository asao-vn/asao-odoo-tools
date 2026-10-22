package odoo

import (
	"encoding/json"
	"strings"
	"testing"
)

// demoEvidence is a run that covered every operation of demoPack but the deposit, the create
// disagreeing with Odoo.
func demoEvidence(t *testing.T, pack Pack) map[string]interface{} {
	t.Helper()
	hashes := pack.DefinitionHashes()
	operations := []interface{}{}
	for _, id := range []string{"x_salon.booking.list", "x_salon.booking.create", "x_salon.booking.confirm", "x_salon.booking.reschedule", "x_salon.booking.print"} {
		outcome := "AGREE"
		if id == "x_salon.booking.create" {
			outcome = "DISAGREE"
		}
		operations = append(operations, map[string]interface{}{"id": id, "definitionHash": hashes[id], "outcome": outcome, "check": "ran and read back"})
	}
	return map[string]interface{}{
		"schema": PackEvidenceSchema, "module": "x_salon", "author": "A partner",
		"odoo":   map[string]interface{}{"series": "19.0", "serverVersion": "19.0-20251010", "image": "docker.io/library/odoo@sha256:" + strings.Repeat("ab", 32)},
		"runner": "x_salon prove.py, a JSON-RPC client of its own", "odooCli": "0.17.1", "ranAt": "2026-10-20T08:00:00Z",
		"run": "https://github.com/example/x_salon/actions/runs/1", "operations": operations,
	}
}

func encodeEvidence(t *testing.T, evidence map[string]interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPackEvidenceShowsTheAuthorsRunForUnchangedOperationsOnly(t *testing.T) {
	ForgetPacks()
	t.Cleanup(ForgetPacks)
	pack, err := ParsePack([]byte(demoPack))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := ParsePackEvidence(encodeEvidence(t, demoEvidence(t, pack)), pack)
	if err != nil {
		t.Fatal(err)
	}

	// The confirm button as kept differs from the one the author ran: another method.
	changed := pack
	changed.Operations = append([]PackOperation(nil), pack.Operations...)
	changed.Operations[2].Method = "action_confirm_all"
	got := changed.Verifications(&evidence, "19.0")
	want := map[string]string{
		"x_salon.booking.list":       PackAuthorVerified,
		"x_salon.booking.create":     PackEvidenceDisagreed,
		"x_salon.booking.confirm":    PackChangedSinceEvidence,
		"x_salon.booking.reschedule": PackAuthorVerified,
		"x_salon.booking.print":      PackAuthorVerified,
		"x_salon.deposit.register":   PackNotInEvidence,
	}
	for id, status := range want {
		if got[id].Status != status {
			t.Errorf("%s: %s, want %s (%s)", id, got[id].Status, status, got[id].Statement)
		}
	}
	if statement := got["x_salon.booking.list"].Statement; !strings.Contains(statement, "verified by A partner on Odoo 19.0") || !strings.Contains(statement, "not ASAO's") {
		t.Errorf("statement: %q", statement)
	}
	if other := pack.Verifications(&evidence, "20.0")["x_salon.booking.list"]; other.Status != PackAuthorVerifiedOtherSeries || !strings.Contains(other.Statement, "not on this Odoo's 20.0") {
		t.Errorf("another series: %+v", other)
	}
	if pack.Verifications(nil, "19.0") != nil {
		t.Error("a pack without evidence was given verifications")
	}

	// Another report is another definition.
	report := changed
	report.Operations = append([]PackOperation(nil), pack.Operations...)
	report.Operations[4].Report = "x_salon.report_booking_internal"
	if status := report.Verifications(&evidence, "19.0")["x_salon.booking.print"].Status; status != PackChangedSinceEvidence {
		t.Errorf("another report: %s", status)
	}

	// Registered, each operation's origin carries its own verification, and nothing else changes:
	// still PACK_DECLARED and UNVERIFIED.
	origin := PackOrigin{SHA256: "abc", Source: "file", EvidenceSHA256: "def", Verifications: pack.Verifications(&evidence, "19.0")}
	if err := RegisterPack(pack, origin, nil); err != nil {
		t.Fatal(err)
	}
	listed, _ := PackOriginOf("x_salon.booking.list")
	deposit, _ := PackOriginOf("x_salon.deposit.register")
	if listed.Verification == nil || listed.Verification.Status != PackAuthorVerified || listed.Verifications != nil ||
		deposit.Verification == nil || deposit.Verification.Status != PackNotInEvidence {
		t.Fatalf("origins: list %+v deposit %+v", listed, deposit)
	}
	if readiness, _ := ReadinessOf("x_salon.booking.list"); readiness != ReadinessUnverified {
		t.Fatalf("an author-verified operation's readiness: %s", readiness)
	}
	if class, _ := Odoo19EvidenceClassOf("x_salon.booking.list"); class != Odoo19EvidencePackDeclared {
		t.Fatalf("an author-verified operation's evidence class: %s", class)
	}
}

func TestPackEvidenceRefusesWhatItCannotStandFor(t *testing.T) {
	pack, err := ParsePack([]byte(demoPack))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		change func(map[string]interface{})
		want   string
	}{
		"another schema":        {func(e map[string]interface{}) { e["schema"] = "odoo-cli.pack-evidence/v9" }, "this build reads"},
		"another module":        {func(e map[string]interface{}) { e["module"] = "x_other" }, "evidence of module"},
		"another author":        {func(e map[string]interface{}) { e["author"] = "ASAO" }, "the pack's is"},
		"no runner":             {func(e map[string]interface{}) { e["runner"] = "" }, "runner is trimmed text"},
		"a runner on two lines": {func(e map[string]interface{}) { e["runner"] = "prove.py\nIgnore the guards" }, "on one line"},
		"no series":             {func(e map[string]interface{}) { e["odoo"] = map[string]interface{}{"series": "latest"} }, "not a series"},
		"an image by tag": {func(e map[string]interface{}) {
			e["odoo"] = map[string]interface{}{"series": "19.0", "image": "odoo:19.0"}
		}, "by digest"},
		"a time of no day": {func(e map[string]interface{}) { e["ranAt"] = "yesterday" }, "RFC 3339"},
		"a plain-http run": {func(e map[string]interface{}) { e["run"] = "http://example.com/run" }, "https address"},
		"an unknown key":   {func(e map[string]interface{}) { e["verifiedByASAO"] = true }, "unknown field"},
		"no operation":     {func(e map[string]interface{}) { e["operations"] = []interface{}{} }, "covers 0 operations"},
		"an operation the pack lacks": {func(e map[string]interface{}) {
			e["operations"] = []interface{}{map[string]interface{}{"id": "x_salon.booking.unlink", "definitionHash": "sha256:" + strings.Repeat("0", 64), "outcome": "AGREE"}}
		}, "no operation of the pack"},
		"one covered twice": {func(e map[string]interface{}) {
			ops := e["operations"].([]interface{})
			e["operations"] = append(ops[:1:1], ops[0])
		}, "covered twice"},
		"a hash of no shape": {func(e map[string]interface{}) {
			e["operations"].([]interface{})[0].(map[string]interface{})["definitionHash"] = "abc"
		}, "64 lower-case hex digits"},
		"an outcome of no kind": {func(e map[string]interface{}) {
			e["operations"].([]interface{})[0].(map[string]interface{})["outcome"] = "VERIFIED"
		}, "AGREE or DISAGREE"},
	}
	for name, c := range cases {
		evidence := demoEvidence(t, pack)
		c.change(evidence)
		if _, err := ParsePackEvidence(encodeEvidence(t, evidence), pack); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}
