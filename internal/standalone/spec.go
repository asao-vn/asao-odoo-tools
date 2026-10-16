package standalone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/specbackend"
	"github.com/asao-platform/atoc/packages/odoo/spec"
)

// The spec group: business configuration as code (packages/odoo/spec). A spec file states the
// modules and records a business keeps; plan says what applying it would change in the selected
// Odoo; apply makes those changes through the catalog's operations. A backend (specbackend) is where
// a team keeps the lock, the plans saved for review, the record of every apply and the spec last
// applied.

// specBackendVariable names the backend when neither --backend nor the spec says.
const specBackendVariable = "AERP_SPEC_BACKEND"

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
- --plan-hash refuses to run if the plan differs from the one that was reviewed.

A TEAM'S BACKEND. With --backend, the spec's "backend:" or $` + specBackendVariable + ` (s3://bucket/prefix
on S3, R2, MinIO or B2, with AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY and, for a store other than
AWS, AWS_ENDPOINT_URL; or a folder), the team shares:
- the lock: one apply at a time, and "spec unlock" when a holder died;
- the plans: "spec plan" saves each one under its hash, for a colleague to review ("spec pull --plan");
- the record of every apply, refused, failed or applied ("spec runs");
- the spec last applied in full, which anyone can pull back ("spec pull").
Apply then needs --plan-hash (or --auto-approve). There is no state file: every plan reads Odoo itself.
Who is recorded as the user and host name, a label: the backend authenticates nobody, and the bucket's
own permissions decide who can write. A colleague's approval with a checked identity belongs to the
governed asao CLI, or to the reviews of the git host the spec lives in.`,
		Children: []*clicore.Command{specPlanCommand(config), specApplyCommand(config), specStateCommand(config),
			specRunsCommand(config), specPullCommand(config), specUnlockCommand(config)},
	}
}

var specFileFlag = clicore.Flag{Name: "file", Kind: clicore.KindString, Required: true,
	Description: "The business spec, YAML or JSON (.json)."}

var specBackendFlag = clicore.Flag{Name: "backend", Kind: clicore.KindString,
	Description: "Where the team keeps the lock, plans, runs and the spec last applied: s3://bucket/prefix or a folder; none for no backend. Default: the spec's backend, then $" + specBackendVariable + "."}

func specPlanCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "plan",
		Summary:    "Show what applying a business spec would change in the selected Odoo. Reads only.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Description: `Reads the selected Odoo and plans the spec. With a backend, the plan is also saved there under its
hash, with the spec as written, for a colleague to review before apply.`,
		Flags:    []clicore.Flag{profileFlag, specFileFlag, specBackendFlag},
		Examples: []string{"aerp spec plan --profile asao-hq --file asao-hq.yaml --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			document, source, err := loadSpec(invocation)
			if err != nil {
				return nil, err
			}
			backend, err := specBackend(invocation, document.Backend)
			if err != nil {
				return nil, err
			}
			executor, err := standaloneTargetExecutor(ctx, invocation, config)
			if err != nil {
				return nil, err
			}
			// A backend that belongs to another Odoo is refused before that Odoo is read.
			var target specbackend.Target
			if backend != nil {
				if target, err = specTarget(ctx, invocation, config, backend); err != nil {
					return nil, err
				}
			}
			plan, err := spec.BuildPlan(ctx, executor, document)
			if err != nil {
				return nil, clicore.NewError(clicore.CategoryProvider, "SPEC_PLAN_FAILED", err.Error()).WithCause(err)
			}
			if backend == nil {
				return plan, nil
			}
			saved, err := backend.SavePlan(ctx, specbackend.SavedPlan{PlanHash: plan.Hash, Spec: source, Target: target,
				Who: specbackend.CurrentActor(), Tool: toolVersion(config), Plan: rawJSON(plan)})
			if err != nil {
				return nil, backendError(err, backend)
			}
			return savedPlanView{Plan: plan, Backend: backend.String(), Saved: saved}, nil
		},
	})
}

// savedPlanView is a plan as plan prints it when a backend kept it: the plan's own fields, and where.
type savedPlanView struct {
	spec.Plan
	Backend string `json:"backend"`
	Saved   string `json:"saved"`
}

func specApplyCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "apply",
		Summary:    "Apply a business spec to the selected Odoo through the catalog's operations.",
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Description: `Plans again and carries the plan out. With a backend it first takes the team's lock (one apply at a
time), needs --plan-hash or --auto-approve, records the apply as a run whatever its outcome, keeps the
spec as the one last applied when every step was applied, and gives the lock back.`,
		Flags: []clicore.Flag{profileFlag, specFileFlag, specBackendFlag,
			{Name: "allow-unverified", Kind: clicore.KindBool, Description: "Let steps run operations that are UNVERIFIED, as execute --allow-unverified does."},
			{Name: "partial", Kind: clicore.KindBool, Description: "Apply the supported steps of a plan that has UNSUPPORTED or AMBIGUOUS ones."},
			{Name: "plan-hash", Kind: clicore.KindString, Description: "The planHash of the reviewed plan: apply refuses if the plan is now another."},
			{Name: "auto-approve", Kind: clicore.KindBool, Description: "With a backend, apply without a reviewed plan's hash."},
		},
		Examples: []string{"aerp spec apply --profile asao-hq --file asao-hq.yaml --plan-hash sha256:... --output json",
			"aerp spec apply --profile asao-hq --file asao-hq.yaml --backend s3://my-bucket/asao-hq --plan-hash sha256:..."},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return applySpec(ctx, invocation, config)
		},
	})
}

func applySpec(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	document, source, err := loadSpec(invocation)
	if err != nil {
		return nil, err
	}
	backend, err := specBackend(invocation, document.Backend)
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
	autoApprove, err := invocation.Bool("auto-approve")
	if err != nil {
		return nil, err
	}
	options.PlanHash = strings.TrimSpace(invocation.String("plan-hash"))
	if backend != nil && options.PlanHash == "" && !autoApprove {
		return nil, clicore.UsageError("SPEC_PLAN_HASH_REQUIRED", "with a backend, apply carries out the plan that was reviewed").
			WithRemediation("Run \"spec plan\" (it saves the plan to the backend for review) and pass its planHash, or pass --auto-approve.")
	}
	executor, err := standaloneTargetExecutor(ctx, invocation, config)
	if err != nil {
		return nil, err
	}
	var target specbackend.Target
	var release func(context.Context) error
	who := specbackend.CurrentActor()
	if backend != nil {
		if target, err = specTarget(ctx, invocation, config, backend); err != nil {
			return nil, err
		}
		release, err = backend.Acquire(ctx, specbackend.Lock{Operation: "apply", Spec: document.Metadata.Name, PlanHash: options.PlanHash,
			Who: who, Tool: toolVersion(config)})
		var locked *specbackend.LockedError
		if errors.As(err, &locked) {
			return nil, clicore.NewError(clicore.CategoryConflict, "SPEC_LOCKED", err.Error()).WithRetryable(true).
				WithContext(map[string]interface{}{"lock": locked.Holder}).
				WithRemediation("Wait for that apply to end. If its holder died, run \"spec unlock --lock-id " + locked.Holder.ID + "\".")
		}
		if err != nil {
			return nil, backendError(err, backend)
		}
	}
	started := time.Now().UTC()
	plan, replanned, report, applyErr := spec.ApplySpec(ctx, executor, document, options)
	out := map[string]interface{}{"plan": plan, "report": report}
	if len(replanned.Steps) > 0 {
		// The plan made once the spec's modules were installed.
		out["planAfterModules"] = replanned
	}
	var failed *spec.StepResult
	for index := range report.Results {
		if report.Results[index].Status == spec.ResultFailed {
			failed = &report.Results[index]
			break
		}
	}
	if backend != nil {
		run := specbackend.Run{ID: specbackend.NewID(), Status: specbackend.RunApplied, PlanHash: plan.Hash, Spec: source, Target: target,
			Who: who, Started: started, Finished: time.Now().UTC(), Tool: toolVersion(config), Summary: report.Summary, Plan: rawJSON(plan)}
		if len(replanned.Steps) > 0 {
			run.PlanAfterModules = rawJSON(replanned)
		}
		if len(report.Results) > 0 {
			run.Report = rawJSON(report)
		}
		switch {
		case errors.Is(applyErr, spec.ErrPlanHasGaps), errors.Is(applyErr, spec.ErrPlanChanged):
			run.Status, run.Reason = specbackend.RunRefused, applyErr.Error()
		case applyErr != nil:
			run.Status, run.Reason = specbackend.RunFailed, applyErr.Error()
		case failed != nil:
			run.Status, run.Reason = specbackend.RunFailed, fmt.Sprintf("step %d (%s %s): %s", failed.Index, failed.Entity, failed.Key, failed.Error)
		}
		recorded := map[string]interface{}{"backend": backend.String(), "run": run.ID, "status": run.Status}
		if where, err := backend.SaveRun(ctx, run); err != nil {
			recorded["runNotRecorded"] = err.Error()
		} else {
			recorded["runObject"] = where
		}
		if run.Status == specbackend.RunApplied {
			if err := backend.SaveApplied(ctx, specbackend.Applied{Run: run.ID, PlanHash: plan.Hash, Spec: source, Target: target, Who: who,
				Tool: toolVersion(config)}); err != nil {
				recorded["appliedNotRecorded"] = err.Error()
			}
		}
		if err := release(context.WithoutCancel(ctx)); err != nil {
			recorded["lockNotReleased"] = err.Error()
		}
		out["backend"] = recorded
	}
	switch {
	case errors.Is(applyErr, spec.ErrPlanHasGaps):
		return nil, clicore.NewError(clicore.CategoryConflict, "SPEC_PLAN_HAS_GAPS", applyErr.Error()).WithContext(withoutPlan(out)).
			WithRemediation("Run \"spec plan\" to see each UNSUPPORTED or AMBIGUOUS step and its reason; pass --partial to apply the others.")
	case errors.Is(applyErr, spec.ErrPlanChanged):
		return nil, clicore.NewError(clicore.CategoryConflict, "SPEC_PLAN_CHANGED", applyErr.Error()).WithContext(withoutPlan(out)).
			WithRemediation("Review the new plan with \"spec plan\" and pass its planHash.")
	case applyErr != nil:
		return nil, clicore.NewError(clicore.CategoryProvider, "SPEC_APPLY_FAILED", applyErr.Error()).WithCause(applyErr).WithContext(withoutPlan(out))
	case failed != nil:
		// The steps before it were applied and the ones after it were not run: the error carries the
		// plans and the report, so a script sees both the failure and what was done.
		return nil, clicore.NewError(clicore.CategoryProvider, "SPEC_APPLY_STEP_FAILED",
			fmt.Sprintf("step %d (%s %s) failed: %s", failed.Index, failed.Entity, failed.Key, failed.Error)).
			WithContext(out).
			WithRemediation("Fix the cause, then plan and apply again: the new plan shows only what is left.")
	}
	return out, nil
}

// withoutPlan keeps a refusal's context to what was recorded: a refused apply ran no step.
func withoutPlan(out map[string]interface{}) map[string]interface{} {
	if recorded, ok := out["backend"]; ok {
		return map[string]interface{}{"backend": recorded}
	}
	return nil
}

func specStateCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "state",
		Summary:    "Show a backend: the Odoo it belongs to, who holds the lock, the spec last applied and the latest runs.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Flags: []clicore.Flag{specBackendFlag,
			{Name: "file", Kind: clicore.KindString, Description: "A spec whose backend: to read, when --backend is not given."},
			{Name: "limit", Kind: clicore.KindInt, Default: "10", Description: "How many of the latest runs to list."},
		},
		Examples: []string{"aerp spec state --backend s3://my-bucket/asao-hq --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			backend, err := backendFor(invocation)
			if err != nil {
				return nil, err
			}
			limit, err := invocation.Int("limit", 10)
			if err != nil {
				return nil, err
			}
			out := map[string]interface{}{"backend": backend.String()}
			target, err := backend.Target(ctx)
			switch {
			case errors.Is(err, specbackend.ErrNotFound):
				out["target"] = nil
			case err != nil:
				return nil, backendError(err, backend)
			default:
				out["target"] = target
			}
			lock, err := backend.Held(ctx)
			if err != nil {
				return nil, backendError(err, backend)
			}
			out["lock"] = lock
			applied, err := backend.LastApplied(ctx)
			if err != nil {
				return nil, backendError(err, backend)
			}
			if applied != nil {
				out["applied"] = map[string]interface{}{"run": applied.Run, "planHash": applied.PlanHash, "spec": applied.Spec.Name,
					"file": applied.Spec.File, "files": applied.Spec.Files, "who": applied.Who, "at": applied.At, "tool": applied.Tool}
			} else {
				out["applied"] = nil
			}
			runs, err := backend.Runs(ctx, limit)
			if err != nil {
				return nil, backendError(err, backend)
			}
			out["runs"] = runs
			return out, nil
		},
	})
}

func specRunsCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "runs",
		Summary:    "List the applies a backend recorded, the latest first, or show one with its plan and report.",
		SideEffect: clicore.EffectRead,
		Authority:  localAuthority,
		Flags: []clicore.Flag{specBackendFlag,
			{Name: "file", Kind: clicore.KindString, Description: "A spec whose backend: to read, when --backend is not given."},
			{Name: "run", Kind: clicore.KindString, Description: "Show this run whole."},
			{Name: "limit", Kind: clicore.KindInt, Default: "20", Description: "How many runs to list."},
		},
		Examples: []string{"aerp spec runs --backend s3://my-bucket/asao-hq", "aerp spec runs --backend s3://my-bucket/asao-hq --run 3f2a9c0d1b2e"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			backend, err := backendFor(invocation)
			if err != nil {
				return nil, err
			}
			if id := strings.TrimSpace(invocation.String("run")); id != "" {
				run, err := backend.Run(ctx, id)
				if err != nil {
					return nil, backendError(err, backend)
				}
				return run, nil
			}
			limit, err := invocation.Int("limit", 20)
			if err != nil {
				return nil, err
			}
			runs, err := backend.Runs(ctx, limit)
			if err != nil {
				return nil, backendError(err, backend)
			}
			return map[string]interface{}{"backend": backend.String(), "runs": runs, "count": len(runs)}, nil
		},
	})
}

func specPullCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:    "pull",
		Summary: "Pull back the spec last applied in full, or the spec of a saved plan, from a backend.",
		Description: `Returns the spec as its author wrote it, and the files it names with their hashes (a spec's
{file: ...} values are kept by hash, not copied). --to writes the spec to a file; an existing file is
replaced only with --force.`,
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Flags: []clicore.Flag{specBackendFlag,
			{Name: "plan", Kind: clicore.KindString, Description: "The planHash of a saved plan whose spec to pull, instead of the one last applied."},
			{Name: "to", Kind: clicore.KindString, Description: "Write the spec to this file."},
			{Name: "force", Kind: clicore.KindBool, Description: "Replace the --to file if it exists."},
		},
		Examples: []string{"aerp spec pull --backend s3://my-bucket/asao-hq --to asao-hq.yaml", "aerp spec pull --backend s3://my-bucket/asao-hq --plan sha256:... --to review.yaml"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			backend, err := backendFor(invocation)
			if err != nil {
				return nil, err
			}
			var source specbackend.SpecSource
			out := map[string]interface{}{"backend": backend.String()}
			if hash := strings.TrimSpace(invocation.String("plan")); hash != "" {
				saved, err := backend.Plan(ctx, hash)
				if err != nil {
					return nil, backendError(err, backend)
				}
				source = saved.Spec
				out["planHash"], out["who"], out["at"] = saved.PlanHash, saved.Who, saved.At
			} else {
				applied, err := backend.LastApplied(ctx)
				if err != nil {
					return nil, backendError(err, backend)
				}
				if applied == nil {
					return nil, clicore.NewError(clicore.CategoryNotFound, "SPEC_NOTHING_APPLIED", "no spec was applied in full through "+backend.String()).
						WithRemediation("Pull a saved plan's spec with --plan, or list the runs with \"spec runs\".")
				}
				source = applied.Spec
				out["run"], out["planHash"], out["who"], out["at"] = applied.Run, applied.PlanHash, applied.Who, applied.At
			}
			out["spec"], out["file"], out["files"] = source.Name, source.File, source.Files
			if path := strings.TrimSpace(invocation.String("to")); path != "" {
				force, err := invocation.Bool("force")
				if err != nil {
					return nil, err
				}
				if _, err := os.Stat(path); err == nil && !force {
					return nil, clicore.NewError(clicore.CategoryConflict, "SPEC_FILE_EXISTS", path+" exists").WithRemediation("Pass --force to replace it.")
				}
				if err := writeLocalFile(path, []byte(source.Text)); err != nil {
					return nil, clicore.NewError(clicore.CategoryConfig, "SPEC_FILE_NOT_WRITTEN", err.Error())
				}
				out["writtenTo"] = path
				return out, nil
			}
			out["text"] = source.Text
			return out, nil
		},
	})
}

func specUnlockCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:        "unlock",
		Summary:     "Remove a backend's lock that an apply which died left behind.",
		Description: `Removes the lock only when it is the one named, so a lock somebody took since is left alone. Make sure its holder is not still applying: two applies at once is what the lock prevents.`,
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		Flags: []clicore.Flag{specBackendFlag,
			{Name: "file", Kind: clicore.KindString, Description: "A spec whose backend: to use, when --backend is not given."},
			{Name: "lock-id", Kind: clicore.KindString, Required: true, Description: "The id of the lock, as \"spec state\" shows it."},
		},
		Examples: []string{"aerp spec unlock --backend s3://my-bucket/asao-hq --lock-id 3f2a9c0d1b2e"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			backend, err := backendFor(invocation)
			if err != nil {
				return nil, err
			}
			id := strings.TrimSpace(invocation.String("lock-id"))
			held, err := backend.Held(ctx)
			if err != nil {
				return nil, backendError(err, backend)
			}
			if held == nil {
				return map[string]interface{}{"backend": backend.String(), "unlocked": false, "reason": "nobody holds the lock"}, nil
			}
			if err := backend.Release(ctx, id); err != nil {
				if errors.Is(err, specbackend.ErrLockNotHeld) {
					return nil, clicore.NewError(clicore.CategoryConflict, "SPEC_LOCK_NOT_HELD", err.Error()).WithContext(map[string]interface{}{"lock": held})
				}
				return nil, backendError(err, backend)
			}
			return map[string]interface{}{"backend": backend.String(), "unlocked": true, "lock": held}, nil
		},
	})
}

// ---- reading a spec and its backend -------------------------------------------------------------

// loadSpec reads and validates the --file business spec, and keeps it as written, with the files it
// names by hash.
func loadSpec(invocation *clicore.Invocation) (spec.Spec, specbackend.SpecSource, error) {
	path, err := invocation.RequiredString("file")
	if err != nil {
		return spec.Spec{}, specbackend.SpecSource{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return spec.Spec{}, specbackend.SpecSource{}, clicore.UsageError("SPEC_FILE_UNREADABLE", "cannot read %s", path).WithContextValue("file", path)
	}
	document, err := decodeSpec(path, data)
	if err != nil {
		return spec.Spec{}, specbackend.SpecSource{}, err
	}
	// File values ({file: home.xml}, {file: logo.png, encoding: base64}) are relative to the spec.
	base := filepath.Dir(path)
	files := map[string]specbackend.FileRef{}
	document, err = spec.ResolveFiles(document, func(relative string) ([]byte, error) {
		content, err := os.ReadFile(filepath.Join(base, relative))
		if err == nil {
			sum := sha256.Sum256(content)
			files[relative] = specbackend.FileRef{Name: relative, SHA256: hex.EncodeToString(sum[:]), Size: len(content)}
		}
		return content, err
	})
	if err != nil {
		return spec.Spec{}, specbackend.SpecSource{}, clicore.UsageError("SPEC_INVALID", "%v", err).WithContextValue("file", path)
	}
	source := specbackend.SpecSource{Name: document.Metadata.Name, File: filepath.Base(path), Text: string(data)}
	for _, ref := range files {
		source.Files = append(source.Files, ref)
	}
	sort.Slice(source.Files, func(i, j int) bool { return source.Files[i].Name < source.Files[j].Name })
	return document, source, nil
}

func decodeSpec(path string, data []byte) (spec.Spec, error) {
	var document spec.Spec
	var err error
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
	return document, nil
}

// specBackend opens the backend --backend names, else the spec's, else $AERP_SPEC_BACKEND; nil when
// none is named or it is "none".
func specBackend(invocation *clicore.Invocation, fromSpec string) (*specbackend.Backend, error) {
	location := firstNonEmpty(invocation.String("backend"), fromSpec, os.Getenv(specBackendVariable))
	if location == "" || location == "none" {
		return nil, nil
	}
	backend, err := specbackend.Open(location)
	if err != nil {
		return nil, backendError(err, nil)
	}
	return backend, nil
}

// backendFor opens the backend of a command that needs one: --backend, the --file spec's, or the
// environment's.
func backendFor(invocation *clicore.Invocation) (*specbackend.Backend, error) {
	fromSpec := ""
	if path := strings.TrimSpace(invocation.String("file")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, clicore.UsageError("SPEC_FILE_UNREADABLE", "cannot read %s", path).WithContextValue("file", path)
		}
		document, err := decodeSpec(path, data)
		if err != nil {
			return nil, err
		}
		fromSpec = document.Backend
	}
	backend, err := specBackend(invocation, fromSpec)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, clicore.UsageError("SPEC_BACKEND_REQUIRED", "name a backend").
			WithRemediation("Pass --backend s3://bucket/prefix (or a folder), a --file whose spec says backend:, or set $" + specBackendVariable + ".")
	}
	return backend, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// specTarget is the Odoo the selected profile names, checked against the one the backend belongs to.
func specTarget(ctx context.Context, invocation *clicore.Invocation, config programConfig, backend *specbackend.Backend) (specbackend.Target, error) {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return specbackend.Target{}, err
	}
	target := specbackend.Target{Endpoint: selection.Profile.Endpoint, Database: selection.Profile.Database}
	if err := backend.CheckTarget(ctx, target); err != nil {
		return specbackend.Target{}, backendError(err, backend)
	}
	return target, nil
}

// backendError maps a backend's failure onto the CLI error contract. No message carries a key.
func backendError(err error, backend *specbackend.Backend) error {
	location := ""
	if backend != nil {
		location = backend.String()
	}
	with := func(e *clicore.Error) *clicore.Error {
		if location != "" {
			return e.WithContextValue("backend", location)
		}
		return e
	}
	switch {
	case errors.Is(err, specbackend.ErrBadLocation):
		return with(clicore.UsageError("SPEC_BACKEND_INVALID", "%v", err).
			WithRemediation("A backend is s3://bucket/prefix (?region=, ?endpoint= for R2, MinIO or B2) or a folder."))
	case errors.Is(err, specbackend.ErrNoCredentials):
		return with(clicore.NewError(clicore.CategoryConfig, "SPEC_BACKEND_NO_CREDENTIALS", err.Error()).
			WithRemediation("Set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (and AWS_ENDPOINT_URL for a store other than AWS), or a profile in ~/.aws/credentials."))
	case errors.Is(err, specbackend.ErrDenied):
		return with(clicore.NewError(clicore.CategoryDenied, "SPEC_BACKEND_DENIED", err.Error()).
			WithRemediation("Check the keys and the bucket's permissions: reading, writing and listing under the prefix."))
	case errors.Is(err, specbackend.ErrUnreachable):
		return with(clicore.NewError(clicore.CategoryUnavailable, "SPEC_BACKEND_UNREACHABLE", err.Error()).WithRetryable(true))
	case errors.Is(err, specbackend.ErrOtherTarget):
		return with(clicore.NewError(clicore.CategoryConflict, "SPEC_BACKEND_OTHER_ODOO", err.Error()).
			WithRemediation("Each Odoo has its own backend: use another prefix for this one, or select the profile of the Odoo the backend belongs to."))
	case errors.Is(err, specbackend.ErrNoConditionalWrites):
		return with(clicore.NewError(clicore.CategoryConfig, "SPEC_BACKEND_CANNOT_LOCK", err.Error()).
			WithRemediation("Use a store that refuses a create when the object exists (S3, R2, MinIO), or a folder."))
	case errors.Is(err, specbackend.ErrNotFound):
		return with(clicore.NewError(clicore.CategoryNotFound, "SPEC_BACKEND_NOT_FOUND", err.Error()))
	}
	return with(clicore.NewError(clicore.CategoryInternal, "SPEC_BACKEND_FAILED", err.Error()))
}

func rawJSON(value interface{}) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}

func toolVersion(config programConfig) string {
	return config.name + " " + releaseVersion
}
