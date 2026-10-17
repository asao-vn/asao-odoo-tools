// Package review is the Odoo pack of ASAO's review checks (DEC-2026-0165,
// docs/design/REVIEW_CHECKS_v0_1.md §3): fixed formulas over a period of an Odoo's books that find
// mistakes, duplicates and signs of fraud, and say what the period looked like. A check reads and
// never writes; every finding names the formula, its thresholds and the records that are its
// evidence, and the same books give the same report.
//
// The formulas live here once. odoo-cli review reads the books through the catalog on the person's
// own Odoo; asao aerp review reads them through the governed catalog, each read authorized and
// audited. An agent writes the words of a report from the findings; it never computes the numbers.
//
// Proven on a simulated business before it was written here: scripts/dev/odoo-review-sim (16 of 16
// planted cases, no decoy flagged, every number of an agent's memo traced to a finding).
package review

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The severities of DEC-2026-0165.
const (
	XemNgay  = "XEM_NGAY"  // money, or a sign a record was made to look right
	NenXuLy  = "NEN_XU_LY" // work left undone, money due
	TheoDoi  = "THEO_DOI"  // a trend, a data-quality hint
	kindErr  = "sai sót"
	kindSign = "dấu hiệu gian lận"
	kindStat = "tình hình"
)

var severityOrder = map[string]int{XemNgay: 0, NenXuLy: 1, TheoDoi: 2}

// SeverityLabel is how a severity reads to a person.
var SeverityLabel = map[string]string{XemNgay: "Xem ngay", NenXuLy: "Nên xử lý", TheoDoi: "Theo dõi"}

//go:embed knowledge/*.md
var knowledge embed.FS

// Reader answers every record a catalog read operation matches with domain, page after page. The
// records carry the operation's projection: relations as [id, name] or {id, display_name}.
type Reader interface {
	ReadAll(ctx context.Context, operation string, domain []interface{}) ([]map[string]interface{}, error)
}

// Subject is what a finding is about.
type Subject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Evidence is one record a finding rests on: the read operation that shows it, its id and its name.
type Evidence struct {
	Operation string `json:"operation"`
	ID        int64  `json:"id"`
	Name      string `json:"name"`
}

// Finding is one thing to look at again.
type Finding struct {
	// Key names the finding the same way from one run to the next: its check and its evidence. A person's
	// verdict on it (real or false) is kept under this key.
	Key string `json:"key"`
	// Verdict is the person's verdict on this key, "real" or "false", when one was given (review verdict).
	Verdict   string                 `json:"verdict,omitempty"`
	Check     string                 `json:"check"`
	Title     string                 `json:"title"`
	Severity  string                 `json:"severity"`
	Subject   Subject                `json:"subject"`
	Why       string                 `json:"why"`
	Formula   string                 `json:"formula"`
	Params    map[string]float64     `json:"params,omitempty"`
	AmountVND float64                `json:"amountVnd"`
	Evidence  []Evidence             `json:"evidence"`
	Facts     map[string]interface{} `json:"facts,omitempty"`
}

// CheckInfo is one check as the report lists it: what it asks and whether it ran.
type CheckInfo struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Question string             `json:"question"`
	Kind     string             `json:"kind"`
	Needs    []string           `json:"needs"`
	Formula  string             `json:"formula"`
	Params   map[string]float64 `json:"params,omitempty"`
	Ran      bool               `json:"ran"`
	NotRun   string             `json:"notRun,omitempty"`
}

// Report is the answer of a review.
type Report struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	AsOf     string         `json:"asOf"`
	Checks   []CheckInfo    `json:"checks"`
	Findings []Finding      `json:"findings"`
	Counts   map[string]int `json:"counts"`
	// ChecksNotLoaded are a team's own checks that did not load, each with why (DEC-2026-0181).
	ChecksNotLoaded []string       `json:"checksNotLoaded,omitempty"`
	ByCheck         map[string]int `json:"byCheck"`
	Figures         *Figures       `json:"figures,omitempty"`
	// FiguresNotRun says why the period's figures could not be computed, when they could not.
	FiguresNotRun string `json:"figuresNotRun,omitempty"`
}

// Options is one review.
type Options struct {
	From, To time.Time
	AsOf     time.Time // the day overdue is counted at; zero means To
	Checks   []string  // only these (all when empty)
	Params   map[string]map[string]float64
}

type check struct {
	id, name, question, kind, formula string
	needs                             []string
	params                            map[string]float64
	run                               func(ctx context.Context, b *books, p map[string]float64) ([]Finding, error)
}

var checks = map[string]check{}

func register(c check) { checks[c.id] = c }

// Knowledge is a check's note for an agent: why it matters, how to read it, when it alarms
// wrongly, what to do next and what it cannot prove.
func Knowledge(id string) string {
	text, err := knowledge.ReadFile("knowledge/" + id + ".md")
	if err != nil {
		return ""
	}
	return string(text)
}

// Catalog lists every check, sorted by id.
func Catalog() []CheckInfo {
	ids := make([]string, 0, len(checks))
	for id := range checks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]CheckInfo, 0, len(ids))
	for _, id := range ids {
		c := checks[id]
		out = append(out, CheckInfo{ID: c.id, Name: c.name, Question: c.question, Kind: c.kind, Needs: c.needs, Formula: c.formula, Params: c.params})
	}
	return out
}

// RefusalError is a read the Odoo or the caller's rights refused: the check that needed it is not
// run, and the report says so rather than reporting nothing found.
type RefusalError interface {
	error
	Refusal() string
}

// Run reviews the period. A check whose reads are refused is listed as not run, with the reason; any
// other error ends the review.
func Run(ctx context.Context, reader Reader, options Options) (*Report, error) {
	if options.To.Before(options.From) {
		return nil, fmt.Errorf("review: the period ends before it starts")
	}
	asOf := options.AsOf
	if asOf.IsZero() {
		asOf = options.To
	}
	b := newBooks(reader, day(options.From), day(options.To), day(asOf))
	wanted := options.Checks
	if len(wanted) == 0 {
		for id := range checks {
			wanted = append(wanted, id)
		}
	}
	sort.Strings(wanted)
	report := &Report{From: b.from.format(), To: b.to.format(), AsOf: b.asOf.format(), Counts: map[string]int{}, ByCheck: map[string]int{}}
	for _, id := range wanted {
		c, known := checks[id]
		if !known {
			return nil, fmt.Errorf("review: no check %q", id)
		}
		params := map[string]float64{}
		for key, value := range c.params {
			params[key] = value
		}
		for key, value := range options.Params[id] {
			if _, ok := params[key]; !ok {
				return nil, fmt.Errorf("review: check %s has no parameter %q", id, key)
			}
			params[key] = value
		}
		info := CheckInfo{ID: c.id, Name: c.name, Question: c.question, Kind: c.kind, Needs: c.needs, Formula: c.formula, Params: params}
		found, err := c.run(ctx, b, params)
		if err != nil {
			if refusal, ok := err.(RefusalError); ok {
				info.NotRun = refusal.Refusal()
				report.Checks = append(report.Checks, info)
				continue
			}
			return nil, fmt.Errorf("review %s: %w", id, err)
		}
		info.Ran = true
		report.Checks = append(report.Checks, info)
		for index := range found {
			found[index].Check, found[index].Formula, found[index].Params = c.id, c.formula, params
			if found[index].Evidence == nil {
				found[index].Evidence = []Evidence{}
			}
			found[index].Key = FindingKey(c.id, found[index].Evidence)
			report.Counts[found[index].Severity]++
			report.ByCheck[c.id]++
		}
		report.Findings = append(report.Findings, found...)
	}
	sort.SliceStable(report.Findings, func(i, j int) bool {
		a, z := report.Findings[i], report.Findings[j]
		if severityOrder[a.Severity] != severityOrder[z.Severity] {
			return severityOrder[a.Severity] < severityOrder[z.Severity]
		}
		if a.AmountVND != z.AmountVND {
			return a.AmountVND > z.AmountVND
		}
		if a.Check != z.Check {
			return a.Check < z.Check
		}
		return a.Why < z.Why
	})
	if report.Findings == nil {
		report.Findings = []Finding{}
	}
	figures, err := periodFigures(ctx, b)
	if err != nil {
		if refusal, ok := err.(RefusalError); ok {
			report.FiguresNotRun = refusal.Refusal()
		} else {
			return nil, err
		}
	} else {
		report.Figures = figures
	}
	return report, nil
}

func formatParams(params map[string]float64) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, trimFloat(params[key])))
	}
	return strings.Join(parts, ", ")
}

func trimFloat(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", value), "0"), ".")
}

// FindingKey is a finding's key: its check and its evidence, in a short stable form.
func FindingKey(check string, evidence []Evidence) string {
	parts := make([]string, len(evidence))
	for i, e := range evidence {
		parts[i] = fmt.Sprintf("%s#%d", e.Operation, e.ID)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(check + "|" + strings.Join(parts, ",")))
	return check + "-" + hex.EncodeToString(sum[:])[:12]
}
