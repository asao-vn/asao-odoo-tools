package standalone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/odoo/review"
)

// reviewPage is how many records one catalog read of a review asks for.
const reviewPage = 500

func reviewGroup(config programConfig) *clicore.Command {
	personal := func(flag clicore.Flag) clicore.Flag {
		flag.PersonOnly = true
		return flag
	}
	run := local(&clicore.Command{
		Name:    "run",
		Summary: "Review the books of a period: mistakes, duplicates and signs of fraud, with the records behind each.",
		Description: `Runs the review checks (DEC-2026-0165) over a period of the selected Odoo, through the catalog's own
reads: bills entered twice, payments recorded twice, duplicate partners, a supplier paid into an
employee's account, a new supplier paid fast, orders split under the approval threshold, sales below
cost, a tax rate unlike the product's, refunds after payment, expenses claimed twice, on leave or in
round amounts, valuable scrap, overdue receivables; and the period's figures month by month.

It reads and never writes. Every finding names its formula, its thresholds and the records that are
its evidence, and says "cần xem lại": a sign to look at again, never a verdict. A check whose reads
the Odoo refuses (a module not installed, a model the series removed) is listed as not run, with the
reason. "review checks" lists the checks with the note an agent reads before it explains a finding.`,
		SideEffect:  clicore.EffectRead,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags: []clicore.Flag{
			personal(profileFlag),
			{Name: "from", Kind: clicore.KindString, Required: true, Description: "First day of the period, YYYY-MM-DD."},
			{Name: "to", Kind: clicore.KindString, Required: true, Description: "Last day of the period, YYYY-MM-DD."},
			{Name: "as-of", Kind: clicore.KindString, Description: "The day overdue is counted at, YYYY-MM-DD (default: --to)."},
			{Name: "check", Kind: clicore.KindString, Repeatable: true, Description: "Run only this check (repeatable), e.g. O01."},
			{Name: "param", Kind: clicore.KindString, Repeatable: true, Description: "A threshold, CHECK.NAME=VALUE, e.g. O01.window_days=10 (see review checks)."},
			personal(clicore.Flag{Name: "allow-unverified", Kind: clicore.KindBool, Description: "Read a series this build has as a candidate (20.0) or a customised Odoo."}),
			personal(clicore.Flag{Name: "markdown-file", Kind: clicore.KindString, Description: "Also write the report a director reads, in Vietnamese, to this file."}),
			personal(clicore.Flag{Name: "company", Kind: clicore.KindString, Description: "The company's name, for the report's title."}),
		},
		Examples: []string{
			config.name + " review run --profile my-odoo --from 2026-07-01 --to 2026-09-30 --output json",
			config.name + " review run --profile my-odoo --from 2026-09-01 --to 2026-09-30 --check O01 --check O22 --markdown-file september.md",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runReview(ctx, invocation, config)
		},
	})
	checks := local(&clicore.Command{
		Name:        "checks",
		Summary:     "List the review checks: what each asks, its formula, its thresholds and the note to read it by.",
		Description: `Pure local listing: no network, no credential.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{{Name: "check", Kind: clicore.KindString, Repeatable: true, Description: "Only this check (repeatable)."}},
		Examples:    []string{config.name + " review checks --output json", config.name + " review checks --check O17 --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			only := map[string]bool{}
			for _, id := range invocation.StringSlice("check") {
				only[strings.ToUpper(strings.TrimSpace(id))] = true
			}
			var out []map[string]interface{}
			for _, c := range review.Catalog() {
				if len(only) > 0 && !only[c.ID] {
					continue
				}
				out = append(out, map[string]interface{}{"id": c.ID, "name": c.Name, "question": c.Question, "kind": c.Kind,
					"needs": c.Needs, "formula": c.Formula, "params": c.Params, "knowledge": review.Knowledge(c.ID)})
			}
			return map[string]interface{}{"checks": out, "count": len(out)}, nil
		},
	})
	return &clicore.Command{
		Name:     "review",
		Summary:  "Review an Odoo's books over a period: what to look at again, and what the period looked like.",
		Children: []*clicore.Command{run, checks},
	}
}

func reviewDay(invocation *clicore.Invocation, flag string) (time.Time, error) {
	value := strings.TrimSpace(invocation.String(flag))
	if value == "" {
		return time.Time{}, nil
	}
	day, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, clicore.UsageError("INVALID_FLAG_VALUE", "--%s must be a day, YYYY-MM-DD", flag).WithContextValue("flag", flag)
	}
	return day, nil
}

func runReview(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	from, err := reviewDay(invocation, "from")
	if err != nil {
		return nil, err
	}
	to, err := reviewDay(invocation, "to")
	if err != nil {
		return nil, err
	}
	asOf, err := reviewDay(invocation, "as-of")
	if err != nil {
		return nil, err
	}
	if to.Before(from) {
		return nil, clicore.UsageError("INVALID_PERIOD", "--to is before --from")
	}
	known := map[string]bool{}
	for _, c := range review.Catalog() {
		known[c.ID] = true
	}
	var only []string
	for _, id := range invocation.StringSlice("check") {
		id = strings.ToUpper(strings.TrimSpace(id))
		if !known[id] {
			return nil, clicore.UsageError("UNKNOWN_CHECK", "no review check %q", id).WithRemediation("Run review checks for the list.")
		}
		only = append(only, id)
	}
	params := map[string]map[string]float64{}
	for _, spec := range invocation.StringSlice("param") {
		name, value, hasValue := strings.Cut(spec, "=")
		check, key, hasKey := strings.Cut(name, ".")
		number, parseErr := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if !hasValue || !hasKey || parseErr != nil {
			return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--param must be CHECK.NAME=NUMBER, got %q", spec).WithContextValue("flag", "param")
		}
		check = strings.ToUpper(strings.TrimSpace(check))
		if params[check] == nil {
			params[check] = map[string]float64{}
		}
		params[check][strings.TrimSpace(key)] = number
	}
	allowUnverified, err := invocation.Bool("allow-unverified")
	if err != nil {
		return nil, err
	}
	executor, err := standaloneTargetExecutor(ctx, invocation, config)
	if err != nil {
		return nil, err
	}
	reader := &catalogReader{executor: executor, allowUnverified: allowUnverified, config: config}
	report, err := review.Run(ctx, reader, review.Options{From: from, To: to, AsOf: asOf, Checks: only, Params: params})
	if err != nil {
		var cliErr *clicore.Error
		if errors.As(err, &cliErr) {
			return nil, cliErr
		}
		if strings.Contains(err.Error(), "has no parameter") {
			return nil, clicore.UsageError("UNKNOWN_PARAMETER", "%v", err).WithRemediation("Run review checks for each check's parameters.")
		}
		return nil, clicore.InternalError(err)
	}
	if path := strings.TrimSpace(invocation.String("markdown-file")); path != "" {
		if err := os.WriteFile(path, []byte(review.Markdown(report, invocation.String("company"))), 0o600); err != nil {
			return nil, clicore.NewError(clicore.CategoryConfig, "REVIEW_FILE_NOT_WRITTEN", fmt.Sprintf("could not write %s: %v", path, err))
		}
	}
	return report, nil
}

// catalogReader reads a review's books through the catalog on the profile's Odoo, page by page.
type catalogReader struct {
	executor        *direct.Executor
	allowUnverified bool
	config          programConfig
}

// readRefusal is a read the Odoo or this build refused: the check that needed it is not run.
type readRefusal struct{ err *clicore.Error }

func (r readRefusal) Error() string   { return r.err.Error() }
func (r readRefusal) Refusal() string { return r.err.Code + ": " + r.err.Message }

func (r *catalogReader) ReadAll(ctx context.Context, operation string, domain []interface{}) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	for offset := 0; ; offset += reviewPage {
		outcome, err := r.executor.Execute(ctx, direct.Request{Operation: operation, Domain: domain, Limit: reviewPage, Offset: offset,
			Order: "id", AllowUnverified: r.allowUnverified})
		if err != nil {
			classified := classifyExecution(err, operation, r.config.surface)
			var cliErr *clicore.Error
			if errors.As(classified, &cliErr) && cliErr.Category != clicore.CategoryUnavailable && cliErr.Category != clicore.CategoryInternal {
				return nil, readRefusal{cliErr}
			}
			return nil, classified
		}
		// The records as the command line prints them: the executor answers some operations as
		// []interface{} and others as []map[string]interface{}, with Go-typed values; JSON makes them
		// the same shapes a script or the SDK reads.
		encoded, err := json.Marshal(outcome.Data)
		if err != nil {
			return nil, clicore.InternalError(err)
		}
		var page []map[string]interface{}
		if err := json.Unmarshal(encoded, &page); err != nil {
			return nil, clicore.InternalError(fmt.Errorf("%s answered %T, not a list of records", operation, outcome.Data))
		}
		out = append(out, page...)
		if len(page) < reviewPage {
			return out, nil
		}
	}
}
