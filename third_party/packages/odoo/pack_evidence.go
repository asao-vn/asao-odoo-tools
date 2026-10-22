package odoo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Pack evidence (DEC-2026-0176 v0.2).
//
// A pack's author runs the pack on a lab with the module installed, checks every operation against
// Odoo with a client of their own (the conformance oracle's way), and ships the result next to the
// pack as <module>/static/odoo-cli/evidence.json. Each operation the run covered carries the
// definition hash odoo-cli gave it on that lab, and the run's outcome.
//
// odoo-cli shows a covered operation whose definition is unchanged as "verified by <author> on
// <series>", never as verified by ASAO: the evidence is the author's claim, and nothing here can prove
// who ran the lab. It changes no guard. The operation's readiness stays UNVERIFIED: execute still
// needs --allow-unverified, and an agent's write still asks the person.
//
// The evidence is a file of its own, not a key of the pack: a pack is read with unknown keys refused,
// so a pack carrying evidence would be refused by every earlier odoo-cli.

// PackEvidenceSchema names the evidence format this build reads.
const PackEvidenceSchema = "odoo-cli.pack-evidence/v1"

// What odoo-cli says about one operation of a pack that comes with evidence.
const (
	// PackAuthorVerified: the author's run agreed with Odoo, on this Odoo's series, for this definition.
	PackAuthorVerified = "AUTHOR_VERIFIED"
	// PackAuthorVerifiedOtherSeries: as above, on another series than this Odoo's.
	PackAuthorVerifiedOtherSeries = "AUTHOR_VERIFIED_OTHER_SERIES"
	// PackChangedSinceEvidence: the definition differs from the one the author's run covered.
	PackChangedSinceEvidence = "CHANGED_SINCE_EVIDENCE"
	// PackEvidenceDisagreed: the author's own run found the operation disagreeing with Odoo.
	PackEvidenceDisagreed = "AUTHOR_RUN_DISAGREED"
	// PackNotInEvidence: the evidence does not cover the operation.
	PackNotInEvidence = "NOT_IN_EVIDENCE"
)

// PackEvidence is an author's verification run of a pack.
type PackEvidence struct {
	Schema string           `json:"schema"`
	Module string           `json:"module"`
	Author string           `json:"author"`
	Odoo   PackEvidenceOdoo `json:"odoo"`
	// Runner names what ran the operations and what checked them against Odoo.
	Runner  string `json:"runner"`
	OdooCLI string `json:"odooCli,omitempty"`
	RanAt   string `json:"ranAt"`
	// Run is where the run can be read, e.g. a public CI run.
	Run        string                  `json:"run,omitempty"`
	Operations []PackEvidenceOperation `json:"operations"`
}

// PackEvidenceOdoo is the Odoo the run was made on.
type PackEvidenceOdoo struct {
	Series        string `json:"series"`
	ServerVersion string `json:"serverVersion,omitempty"`
	// Image is the container image the lab ran, by digest.
	Image string `json:"image,omitempty"`
}

// PackEvidenceOperation is one operation's result in the run.
type PackEvidenceOperation struct {
	ID             string `json:"id"`
	DefinitionHash string `json:"definitionHash"`
	Outcome        string `json:"outcome"`
	Check          string `json:"check,omitempty"`
}

// PackVerification is what odoo-cli says about one pack operation, given the evidence.
type PackVerification struct {
	Status        string `json:"status"`
	By            string `json:"by,omitempty"`
	Series        string `json:"series,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
	Image         string `json:"image,omitempty"`
	RanAt         string `json:"ranAt,omitempty"`
	Run           string `json:"run,omitempty"`
	Statement     string `json:"statement"`
}

var (
	packEvidenceHash   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	packEvidenceSeries = regexp.MustCompile(`^\d{2}\.\d$`)
	packEvidenceImage  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)
)

// ParsePackEvidence reads evidence for pack and checks it. Unknown keys are refused, like a pack's.
func ParsePackEvidence(data []byte, pack Pack) (PackEvidence, error) {
	var evidence PackEvidence
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return PackEvidence{}, fmt.Errorf("evidence: not an %s document: %v", PackEvidenceSchema, err)
	}
	if err := evidence.validate(pack); err != nil {
		return PackEvidence{}, fmt.Errorf("evidence of %s: %w", pack.Module, err)
	}
	return evidence, nil
}

func (e PackEvidence) validate(pack Pack) error {
	if e.Schema != PackEvidenceSchema {
		return fmt.Errorf("schema is %q, this build reads %q", e.Schema, PackEvidenceSchema)
	}
	if e.Module != pack.Module {
		return fmt.Errorf("it is the evidence of module %q", e.Module)
	}
	for name, value := range map[string]string{"author": e.Author, "runner": e.Runner} {
		if value == "" || strings.TrimSpace(value) != value || len(value) > 200 || hasControl(value) {
			return fmt.Errorf("%s is trimmed text of 1 to 200 characters on one line", name)
		}
	}
	if hasControl(e.Odoo.ServerVersion + e.OdooCLI) {
		return fmt.Errorf("a version is text on one line")
	}
	// The pack and its evidence name one author: the statement shown is "verified by" the pack's author.
	if pack.Author != "" && e.Author != pack.Author {
		return fmt.Errorf("the author is %q, the pack's is %q", e.Author, pack.Author)
	}
	if !packEvidenceSeries.MatchString(e.Odoo.Series) {
		return fmt.Errorf("odoo.series %q is not a series like 19.0", e.Odoo.Series)
	}
	if len(e.Odoo.ServerVersion) > 64 || len(e.OdooCLI) > 64 {
		return fmt.Errorf("a version is at most 64 characters")
	}
	if e.Odoo.Image != "" && !packEvidenceImage.MatchString(e.Odoo.Image) {
		return fmt.Errorf("odoo.image %q is not an image by digest (<name>@sha256:<digest>)", e.Odoo.Image)
	}
	if _, err := time.Parse(time.RFC3339, e.RanAt); err != nil {
		return fmt.Errorf("ranAt %q is not an RFC 3339 time", e.RanAt)
	}
	if e.Run != "" && (!strings.HasPrefix(e.Run, "https://") || len(e.Run) > 300 || strings.ContainsAny(e.Run, " \n\t")) {
		return fmt.Errorf("run is an https address")
	}
	declared := map[string]bool{}
	for _, op := range pack.Operations {
		declared[op.ID] = true
	}
	if len(e.Operations) == 0 || len(e.Operations) > len(pack.Operations) {
		return fmt.Errorf("it covers %d operations; the pack declares %d", len(e.Operations), len(pack.Operations))
	}
	seen := map[string]bool{}
	for _, op := range e.Operations {
		if !declared[op.ID] {
			return fmt.Errorf("%s is no operation of the pack", op.ID)
		}
		if seen[op.ID] {
			return fmt.Errorf("%s is covered twice", op.ID)
		}
		seen[op.ID] = true
		if !packEvidenceHash.MatchString(op.DefinitionHash) {
			return fmt.Errorf("%s: definitionHash is sha256:<64 lower-case hex digits>", op.ID)
		}
		if op.Outcome != "AGREE" && op.Outcome != "DISAGREE" {
			return fmt.Errorf("%s: outcome is AGREE or DISAGREE", op.ID)
		}
		if strings.TrimSpace(op.Check) != op.Check || len(op.Check) > 200 || hasControl(op.Check) {
			return fmt.Errorf("%s: check is trimmed text of at most 200 characters on one line", op.ID)
		}
	}
	return nil
}

// hasControl reports a control character: the evidence's text reaches the results an agent reads, on
// one line.
func hasControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029 }) >= 0
}

// DefinitionHashes are the pack's operations' fingerprints, by id: the catalog's definition hash of each
// compiled operation (catalog describe's definitionHash), which a sync completes with the field types and
// relations of the Odoo it checked.
func (p Pack) DefinitionHashes() map[string]string {
	out := make(map[string]string, len(p.Operations))
	for _, op := range p.Operations {
		out[op.ID] = OperationDefinitionHash(p.operation(op))
	}
	return out
}

// Verifications says, for each operation of the pack, what the evidence shows; series is this Odoo's.
// Without evidence it returns nil: the pack is its author's declaration only.
func (p Pack) Verifications(evidence *PackEvidence, series string) map[string]PackVerification {
	if evidence == nil {
		return nil
	}
	covered := map[string]PackEvidenceOperation{}
	for _, op := range evidence.Operations {
		covered[op.ID] = op
	}
	hashes := p.DefinitionHashes()
	out := make(map[string]PackVerification, len(p.Operations))
	for _, op := range p.Operations {
		verification := PackVerification{By: evidence.Author, Series: evidence.Odoo.Series, ServerVersion: evidence.Odoo.ServerVersion,
			Image: evidence.Odoo.Image, RanAt: evidence.RanAt, Run: evidence.Run}
		run, ok := covered[op.ID]
		switch {
		case !ok:
			verification = PackVerification{Status: PackNotInEvidence,
				Statement: "unverified: the author's evidence does not cover this operation"}
		case run.DefinitionHash != hashes[op.ID]:
			verification.Status = PackChangedSinceEvidence
			verification.Statement = fmt.Sprintf("unverified: the operation differs from the one %s verified on Odoo %s", evidence.Author, evidence.Odoo.Series)
		case run.Outcome != "AGREE":
			verification.Status = PackEvidenceDisagreed
			verification.Statement = fmt.Sprintf("unverified: %s's own run on Odoo %s found it disagreeing with Odoo", evidence.Author, evidence.Odoo.Series)
		case series != "" && series != evidence.Odoo.Series:
			verification.Status = PackAuthorVerifiedOtherSeries
			verification.Statement = fmt.Sprintf("verified by %s on Odoo %s, not on this Odoo's %s; the author's claim, not ASAO's", evidence.Author, evidence.Odoo.Series, series)
		default:
			verification.Status = PackAuthorVerified
			verification.Statement = fmt.Sprintf("verified by %s on Odoo %s; the author's claim, not ASAO's", evidence.Author, evidence.Odoo.Series)
		}
		out[op.ID] = verification
	}
	return out
}
