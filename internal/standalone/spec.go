package standalone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo/spec"
)

// The spec group: business configuration as code (packages/odoo/spec). A spec file states the
// modules and records a business keeps; plan says what applying it would change in the selected
// Odoo; apply makes those changes through the catalog's operations.

func specGroup(config programConfig) *clicore.Command {
	return &clicore.Command{
		Name:    "spec",
		Summary: "Plan and apply a business spec: the modules and records a business keeps, as code.",
		Description: `A business spec (apiVersion asao.business/v0, kind BusinessSpec; YAML or JSON) states what a
business keeps in its Odoo:
- the modules it needs;
- the records it holds (company, departments, products, stages, ...), in the order they apply.

"spec plan" compares the spec with the selected Odoo and lists each step, with its operation and
that operation's readiness. Each step is one of:
- CREATE or UPDATE (only the fields that differ);
- NO_CHANGE;
- INSTALL (a module);
- UNSUPPORTED: the catalog has no operation for the step. This is a gap, with its reason.
- AMBIGUOUS: several records match.

"spec apply" plans again and carries out the steps through the same operations "execute" runs,
with the same readiness rule and refusals. It installs the modules first and then plans the
records again: a module brings models and records the first plan could not see (AFTER_MODULES).
Options:
- --allow-unverified lets an UNVERIFIED operation run;
- --partial applies the supported steps of a plan that has gaps;
- --plan-hash refuses to run if the plan differs from the one that was reviewed.`,
		Children: []*clicore.Command{specPlanCommand(config), specApplyCommand(config)},
	}
}

var specFileFlag = clicore.Flag{Name: "file", Kind: clicore.KindString, Required: true,
	Description: "The business spec, YAML or JSON (.json)."}

func specPlanCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "plan",
		Summary:    "Show what applying a business spec would change in the selected Odoo. Reads only.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Flags:      []clicore.Flag{profileFlag, specFileFlag},
		Examples:   []string{"aerp spec plan --profile asao-hq --file asao-hq.yaml --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			document, err := loadSpec(invocation)
			if err != nil {
				return nil, err
			}
			executor, err := standaloneTargetExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			plan, err := spec.BuildPlan(ctx, executor, document)
			if err != nil {
				return nil, clicore.NewError(clicore.CategoryProvider, "SPEC_PLAN_FAILED", err.Error()).WithCause(err)
			}
			return plan, nil
		},
	})
}

func specApplyCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "apply",
		Summary:    "Apply a business spec to the selected Odoo through the catalog's operations.",
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Flags: []clicore.Flag{profileFlag, specFileFlag,
			{Name: "allow-unverified", Kind: clicore.KindBool, Description: "Let steps run operations that are UNVERIFIED, as execute --allow-unverified does."},
			{Name: "partial", Kind: clicore.KindBool, Description: "Apply the supported steps of a plan that has UNSUPPORTED or AMBIGUOUS ones."},
			{Name: "plan-hash", Kind: clicore.KindString, Description: "The planHash of the reviewed plan: apply refuses if the plan is now another."},
		},
		Examples: []string{"aerp spec apply --profile asao-hq --file asao-hq.yaml --plan-hash sha256:... --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			document, err := loadSpec(invocation)
			if err != nil {
				return nil, err
			}
			var options spec.ApplyOptions
			if options.AllowUnverified, err = invocation.Bool("allow-unverified"); err != nil {
				return nil, err
			}
			if options.Partial, err = invocation.Bool("partial"); err != nil {
				return nil, err
			}
			options.PlanHash = strings.TrimSpace(invocation.String("plan-hash"))
			executor, err := standaloneTargetExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			plan, replanned, report, err := spec.ApplySpec(ctx, executor, document, options)
			switch {
			case errors.Is(err, spec.ErrPlanHasGaps):
				return nil, clicore.NewError(clicore.CategoryConflict, "SPEC_PLAN_HAS_GAPS", err.Error()).
					WithRemediation("Run \"spec plan\" to see each UNSUPPORTED or AMBIGUOUS step and its reason; pass --partial to apply the others.")
			case errors.Is(err, spec.ErrPlanChanged):
				return nil, clicore.NewError(clicore.CategoryConflict, "SPEC_PLAN_CHANGED", err.Error()).
					WithRemediation("Review the new plan with \"spec plan\" and pass its planHash.")
			case err != nil:
				return nil, clicore.NewError(clicore.CategoryProvider, "SPEC_APPLY_FAILED", err.Error()).WithCause(err)
			}
			out := map[string]interface{}{"plan": plan, "report": report}
			if len(replanned.Steps) > 0 {
				// The plan made once the spec's modules were installed.
				out["planAfterModules"] = replanned
			}
			for _, result := range report.Results {
				if result.Status != spec.ResultFailed {
					continue
				}
				// The steps before it were applied and the ones after it were not run: the error
				// carries the plans and the report, so a script sees both the failure and what was done.
				return nil, clicore.NewError(clicore.CategoryProvider, "SPEC_APPLY_STEP_FAILED",
					fmt.Sprintf("step %d (%s %s) failed: %s", result.Index, result.Entity, result.Key, result.Error)).
					WithContext(out).
					WithRemediation("Fix the cause, then plan and apply again: the new plan shows only what is left.")
			}
			return out, nil
		},
	})
}

// loadSpec reads and validates the --file business spec.
func loadSpec(invocation *clicore.Invocation) (spec.Spec, error) {
	path, err := invocation.RequiredString("file")
	if err != nil {
		return spec.Spec{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return spec.Spec{}, clicore.UsageError("SPEC_FILE_UNREADABLE", "cannot read %s", path).WithContextValue("file", path)
	}
	var document spec.Spec
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		err = json.Unmarshal(data, &document)
	} else {
		err = yaml.Unmarshal(data, &document)
	}
	if err != nil {
		return spec.Spec{}, clicore.UsageError("SPEC_INVALID", "%s is not a business spec: %v", path, err).WithContextValue("file", path)
	}
	if err := document.Validate(); err != nil {
		return spec.Spec{}, clicore.UsageError("SPEC_INVALID", "%v", err).WithContextValue("file", path)
	}
	// File values ({file: home.xml}, {file: logo.png, encoding: base64}) are relative to the spec.
	base := filepath.Dir(path)
	document, err = spec.ResolveFiles(document, func(relative string) ([]byte, error) {
		return os.ReadFile(filepath.Join(base, relative))
	})
	if err != nil {
		return spec.Spec{}, clicore.UsageError("SPEC_INVALID", "%v", err).WithContextValue("file", path)
	}
	return document, nil
}
