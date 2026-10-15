package standalone

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/controls"
	"github.com/asao-platform/atoc/packages/odoo/spec"
)

// The controls group: Odoo's own switches that stop what a review finds (packages/odoo/controls,
// docs/design/CONTROLS_v0_1.md). list and status read; plan shows what turning controls on would
// change; apply turns them on through the spec engine and stays with the person.

func controlsGroup(config programConfig) *clicore.Command {
	personal := func(flag clicore.Flag) clicore.Flag {
		flag.PersonOnly = true
		return flag
	}
	unverified := personal(clicore.Flag{Name: "allow-unverified", Kind: clicore.KindBool,
		Description: "Read and write through operations not verified on this Odoo (a candidate series such as 20.0, or a customised Odoo)."})
	company := clicore.Flag{Name: "company", Kind: clicore.KindInt, Description: "The company's id, when the database has several (status lists them)."}
	selection := []clicore.Flag{
		{Name: "enable", Kind: clicore.KindString, Repeatable: true, Required: true,
			Description: "A control to turn on, e.g. K01 (repeatable, or comma-separated: K01,K04)."},
		{Name: "param", Kind: clicore.KindString, Repeatable: true,
			Description: "A value a control takes, CONTROL.NAME=VALUE: K04.date=2026-09-30 (required by K04), K01.amount=20000000."},
		company, unverified,
	}

	list := local(&clicore.Command{
		Name:    "list",
		Summary: "List the controls: what each prevents, what Odoo does, where Odoo enforces it, whether it can be undone.",
		Description: `Pure local listing: no network, no credential. Each control is one of Odoo's own settings, unchanged:
- "enforced" says where Odoo stops it: SERVER (every caller, the API included), WEB_CLIENT (the
  screens only), WARNING (Odoo warns and lets the user go on);
- "applies": false marks a control odoo-cli only reads, because Odoo never lets it be undone; "manual"
  says how to turn it on in Odoo;
- "checks" and "patterns" are the review checks and risk patterns it prevents.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{{Name: "control", Kind: clicore.KindString, Repeatable: true, Description: "Only this control (repeatable), e.g. K04."}},
		Examples:    []string{config.name + " controls list --output json", config.name + " controls list --control K04 --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			all, err := controls.Load()
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			only := map[string]bool{}
			for _, id := range splitIDs(invocation.StringSlice("control")) {
				if _, ok := all.Find(id); !ok {
					return nil, clicore.UsageError("UNKNOWN_CONTROL", "no control %q", id).WithRemediation("Run controls list without --control.")
				}
				only[id] = true
			}
			out := []controls.Control{}
			for _, c := range all.Controls {
				if len(only) == 0 || only[c.ID] {
					out = append(out, c)
				}
			}
			return map[string]interface{}{"about": all.About, "controls": out}, nil
		},
	})

	status := local(&clicore.Command{
		Name:    "status",
		Summary: "Read which controls are on in the selected Odoo. Reads only.",
		Description: `Reads each control's state through the catalog's reads: the Settings page, the company's lock dates
and the journals' hash switch. A state is one of:
- ON or OFF; PARTIAL when only some of its switches are on (some lock dates, some journals);
- NOT_AVAILABLE: the module it needs is not installed;
- UNREAD: the read it needs was refused, with the reason.
The Settings page is the one of the profile's user's current company; lock dates and journals are the
company's named by --company, or the database's only company.`,
		SideEffect:  clicore.EffectRead,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{personal(profileFlag), company, unverified},
		Examples:    []string{config.name + " controls status --profile my-odoo --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			options, err := controlOptions(invocation)
			if err != nil {
				return nil, err
			}
			executor, err := standaloneTargetExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			report, err := controls.Read(ctx, executor, options)
			if err != nil {
				return nil, controlsError(err)
			}
			return report, nil
		},
	})

	plan := local(&clicore.Command{
		Name:    "plan",
		Summary: "Show what turning controls on would change in the selected Odoo. Reads only.",
		Description: `Renders the chosen controls into a business spec and plans it as "spec plan" does: each step, its
operation, its readiness and the plan's hash, which "controls apply --plan-hash" takes. A control
already on is NO_CHANGE.
- A control odoo-cli does not turn on (Odoo never lets it be undone) is listed under "manual", with
  the steps to take in Odoo.
- K04 needs --param K04.date: a past day, never before a lock date already set. odoo-cli never picks a
  lock date, never moves one back, and never sets the hard lock date.
- A value already stronger than the control's (all employees and portal users signing in in two
  steps) is kept.`,
		SideEffect:  clicore.EffectRead,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       append([]clicore.Flag{personal(profileFlag)}, selection...),
		Examples: []string{
			config.name + " controls plan --profile my-odoo --enable K01,K05 --param K01.amount=20000000 --output json",
			config.name + " controls plan --profile my-odoo --enable K04 --param K04.date=2026-09-30 --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			chosen, options, err := controlSelection(invocation)
			if err != nil {
				return nil, err
			}
			executor, err := standaloneTargetExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			planned, err := controls.PlanControls(ctx, executor, chosen, options)
			if err != nil {
				return nil, controlsError(err)
			}
			out := map[string]interface{}{"controls": planned.Controls, "plan": planned.Plan, "spec": planned.Spec}
			if len(planned.Manual) > 0 {
				out["manual"] = planned.Manual
			}
			if planned.Company != nil {
				out["company"] = planned.Company
			}
			if len(planned.Controls) > 0 {
				out["apply"] = applyLine(config.name, invocation, planned.Plan.Hash)
			}
			return out, nil
		},
	})

	apply := local(&clicore.Command{
		Name:    "apply",
		Summary: "Turn controls on in the selected Odoo, as the plan with --plan-hash showed.",
		Description: `Renders the chosen controls again and applies them through the spec engine, with the same operations,
readiness rule and refusals as "spec apply". --plan-hash refuses to run when the plan is no longer
the one reviewed. It turns controls on and never off, and never applies one Odoo does not let be
undone. An agent proposes this command; the person runs it.`,
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Flags: append([]clicore.Flag{profileFlag}, append(selection,
			clicore.Flag{Name: "plan-hash", Kind: clicore.KindString, Description: "The planHash of the reviewed plan: apply refuses if the plan is now another."})...),
		Examples: []string{config.name + " controls apply --profile my-odoo --enable K01,K04 --param K04.date=2026-09-30 --plan-hash sha256:... --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			chosen, options, err := controlSelection(invocation)
			if err != nil {
				return nil, err
			}
			executor, err := standaloneTargetExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			applied, report, err := controls.ApplyControls(ctx, executor, chosen, options, spec.ApplyOptions{
				AllowUnverified: options.AllowUnverified, PlanHash: strings.TrimSpace(invocation.String("plan-hash"))})
			switch {
			case errors.Is(err, spec.ErrPlanHasGaps):
				return nil, clicore.NewError(clicore.CategoryConflict, "CONTROLS_PLAN_HAS_GAPS", err.Error()).
					WithRemediation("Run \"controls plan\" to see the step the catalog cannot carry out and its reason.")
			case errors.Is(err, spec.ErrPlanChanged):
				return nil, clicore.NewError(clicore.CategoryConflict, "CONTROLS_PLAN_CHANGED", err.Error()).
					WithRemediation("Review the new plan with \"controls plan\" and pass its planHash.")
			case err != nil:
				return nil, controlsError(err)
			}
			out := map[string]interface{}{"controls": applied.Controls, "plan": applied.Plan, "report": report}
			if len(applied.Manual) > 0 {
				out["manual"] = applied.Manual
			}
			for _, result := range report.Results {
				if result.Status == spec.ResultFailed {
					return nil, clicore.NewError(clicore.CategoryProvider, "CONTROLS_APPLY_STEP_FAILED",
						fmt.Sprintf("step %d (%s %s) failed: %s", result.Index, result.Entity, result.Key, result.Error)).
						WithContext(out).
						WithRemediation("Fix the cause (Odoo's message says it), then plan and apply again: the new plan shows only what is left.")
				}
			}
			return out, nil
		},
	})

	return &clicore.Command{
		Name:    "controls",
		Summary: "Odoo's own switches that stop what the review finds: list them, read them, turn them on.",
		Description: `"review" finds problems after they happened; most have a switch in Odoo that would have stopped them.
"controls list" says what each switch does in Odoo, where Odoo enforces it and whether it can be
undone; "controls status" reads which are on; "controls plan" and "controls apply" turn the chosen
ones on through the catalog's verified operations, as a business spec. Nothing is added to Odoo.`,
		Children: []*clicore.Command{list, status, plan, apply},
	}
}

func splitIDs(values []string) []string {
	var out []string
	for _, value := range values {
		for _, id := range strings.Split(value, ",") {
			if id = strings.ToUpper(strings.TrimSpace(id)); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func controlOptions(invocation *clicore.Invocation) (controls.Options, error) {
	var options controls.Options
	var err error
	if options.AllowUnverified, err = invocation.Bool("allow-unverified"); err != nil {
		return options, err
	}
	if options.CompanyID, err = invocation.Int64("company", 0); err != nil {
		return options, err
	}
	return options, nil
}

func controlSelection(invocation *clicore.Invocation) (controls.Selection, controls.Options, error) {
	options, err := controlOptions(invocation)
	if err != nil {
		return controls.Selection{}, options, err
	}
	chosen := controls.Selection{IDs: splitIDs(invocation.StringSlice("enable")), Params: map[string]string{}}
	for _, raw := range invocation.StringSlice("param") {
		name, value, ok := strings.Cut(raw, "=")
		if !ok || !strings.Contains(name, ".") || strings.TrimSpace(value) == "" {
			return chosen, options, clicore.UsageError("INVALID_FLAG_VALUE", "--param must be CONTROL.NAME=VALUE, got %q", raw).
				WithContextValue("flag", "param")
		}
		chosen.Params[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return chosen, options, nil
}

// applyLine is the command a person runs to apply the plan just shown.
func applyLine(program string, invocation *clicore.Invocation, hash string) string {
	parts := []string{program, "controls", "apply", "--profile", invocation.String("profile"),
		"--enable", strings.Join(splitIDs(invocation.StringSlice("enable")), ",")}
	for _, raw := range invocation.StringSlice("param") {
		parts = append(parts, "--param", strings.TrimSpace(raw))
	}
	if id, _ := invocation.Int64("company", 0); id != 0 {
		parts = append(parts, "--company", fmt.Sprint(id))
	}
	if unverified, _ := invocation.Bool("allow-unverified"); unverified {
		parts = append(parts, "--allow-unverified")
	}
	return strings.Join(append(parts, "--plan-hash", hash), " ")
}

func controlsError(err error) error {
	var cliErr *clicore.Error
	switch {
	case errors.As(err, &cliErr):
		return cliErr
	case errors.Is(err, controls.ErrSelection):
		return clicore.UsageError("CONTROLS_SELECTION_INVALID", "%v", err).WithRemediation("Run controls list for each control and its parameters.")
	}
	failure := clicore.NewError(clicore.CategoryProvider, "CONTROLS_READ_FAILED", err.Error()).WithCause(err)
	if strings.Contains(err.Error(), "UNVERIFIED") {
		failure = failure.WithRemediation("The operation is not verified on this Odoo (a candidate series such as 20.0): pass --allow-unverified to read through it anyway.")
	}
	return failure
}
