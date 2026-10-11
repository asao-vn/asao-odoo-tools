package spec

import (
	"context"
	"errors"
	"fmt"

	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// ApplyOptions are the caller's consents.
type ApplyOptions struct {
	// AllowUnverified lets a step run an operation without current conformance evidence, as
	// `execute --allow-unverified` does.
	AllowUnverified bool
	// Partial applies the steps the catalog supports even when the plan has gaps; otherwise a plan
	// with an unsupported or ambiguous step is refused whole.
	Partial bool
	// PlanHash, when set, must be the hash of the plan the caller reviewed: a plan that changed
	// since (the provider moved on) is refused before anything runs.
	PlanHash string
}

// Step result statuses.
const (
	ResultApplied   = "APPLIED"
	ResultUnchanged = "UNCHANGED"
	ResultSkipped   = "SKIPPED"
	ResultFailed    = "FAILED"
	ResultNotRun    = "NOT_RUN"
)

// StepResult is what one step did.
type StepResult struct {
	Index      int    `json:"index"`
	Entity     string `json:"entity"`
	Key        string `json:"key"`
	Action     Action `json:"action"`
	Operation  string `json:"operation,omitempty"`
	Status     string `json:"status"`
	TargetID   int64  `json:"targetId,omitempty"`
	Readiness  string `json:"readiness,omitempty"`
	ErrorClass string `json:"errorClass,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Report is what an apply did.
type Report struct {
	Spec     string         `json:"spec"`
	PlanHash string         `json:"planHash"`
	Complete bool           `json:"complete"`
	Results  []StepResult   `json:"results"`
	Summary  map[string]int `json:"summary"`
}

// ErrPlanHasGaps refuses an apply whose plan has an unsupported or ambiguous step.
var ErrPlanHasGaps = errors.New("spec: the plan has steps the catalog cannot carry out")

// ErrPlanChanged refuses an apply whose plan is not the one the caller reviewed.
var ErrPlanChanged = errors.New("spec: the plan changed since it was reviewed")

// Apply carries out a plan, step by step, through the catalog's operations. It stops at the first
// failure: the steps after it are NOT_RUN, and planning again shows what is left.
func Apply(ctx context.Context, exec Executor, plan Plan, options ApplyOptions) (Report, error) {
	if options.PlanHash != "" && options.PlanHash != plan.Hash {
		return Report{}, fmt.Errorf("%w: reviewed %s, now %s", ErrPlanChanged, options.PlanHash, plan.Hash)
	}
	if err := refuseGaps(plan, options); err != nil {
		return Report{}, err
	}
	report := Report{Spec: plan.Spec, PlanHash: plan.Hash, Complete: true, Summary: map[string]int{}}
	failed := false
	for _, step := range plan.Steps {
		result := StepResult{Index: step.Index, Entity: step.Entity, Key: step.Key, Action: step.Action,
			Operation: step.Operation, TargetID: step.TargetID, Readiness: step.Readiness}
		switch {
		case failed:
			result.Status = ResultNotRun
		case step.Action == ActionNoChange:
			result.Status = ResultUnchanged
		case step.Action == ActionAfterModules:
			result.Status, result.Error = ResultNotRun, step.Reason
		case step.Action == ActionUnsupported || step.Action == ActionAmbiguous:
			result.Status, result.Error = ResultSkipped, step.Reason
			report.Complete = false
		default:
			request := direct.Request{Operation: step.Operation, ProviderTargetID: step.TargetID,
				Values: step.Values, AllowUnverified: options.AllowUnverified}
			if step.Action == ActionCreate {
				request.ProviderTargetID = 0
			}
			outcome, err := exec.Execute(ctx, request)
			if err != nil {
				result.Status, result.Error = ResultFailed, err.Error()
				var refused *direct.OperationError
				if errors.As(err, &refused) {
					result.ErrorClass, result.Error = refused.Class, refused.Message
				}
				failed, report.Complete = true, false
				break
			}
			result.Status = ResultApplied
			if outcome.Readiness != "" {
				result.Readiness = outcome.Readiness
			}
			if step.Action == ActionCreate {
				var id int64
				if _, scanErr := fmt.Sscan(outcome.ProviderReference, &id); scanErr == nil {
					result.TargetID = id
				}
			}
		}
		report.Summary[result.Status]++
		report.Results = append(report.Results, result)
	}
	return report, nil
}

// refuseGaps refuses a plan with an unsupported or ambiguous step, unless a partial apply was asked for.
func refuseGaps(plan Plan, options ApplyOptions) error {
	if gaps := plan.Gaps(); len(gaps) > 0 && !options.Partial {
		return fmt.Errorf("%w: %d, the first at step %d (%s %s): %s", ErrPlanHasGaps, len(gaps), gaps[0].Index, gaps[0].Entity, gaps[0].Key, gaps[0].Reason)
	}
	return nil
}

// ApplySpec plans a spec and carries the plan out, the way an operator runs apply: the modules first,
// then, because a module brings models and records the first plan could not see, a fresh plan of
// the records, carried out with the caller's options. options.PlanHash binds the first plan, the
// one a reviewer read with its AFTER_MODULES steps. It returns both plans and one report.
func ApplySpec(ctx context.Context, exec Executor, s Spec, options ApplyOptions) (Plan, Plan, Report, error) {
	first, err := BuildPlan(ctx, exec, s)
	if err != nil {
		return Plan{}, Plan{}, Report{}, err
	}
	if options.PlanHash != "" && options.PlanHash != first.Hash {
		return first, Plan{}, Report{}, fmt.Errorf("%w: reviewed %s, now %s", ErrPlanChanged, options.PlanHash, first.Hash)
	}
	// The reviewed plan's gaps are refused before anything runs: installing its modules first would
	// change the database for an apply that is then refused.
	if err := refuseGaps(first, options); err != nil {
		return first, Plan{}, Report{}, err
	}
	if first.Summary[ActionInstall] == 0 {
		options.PlanHash = ""
		report, err := Apply(ctx, exec, first, options)
		return first, Plan{}, report, err
	}
	installs := Plan{Spec: first.Spec, Hash: first.Hash}
	for _, step := range first.Steps {
		if step.Action == ActionInstall {
			installs.Steps = append(installs.Steps, step)
		}
	}
	report, err := Apply(ctx, exec, installs, ApplyOptions{AllowUnverified: options.AllowUnverified})
	if err != nil || !report.Complete {
		return first, Plan{}, report, err
	}
	second, err := BuildPlan(ctx, exec, s)
	if err != nil {
		return first, Plan{}, report, err
	}
	options.PlanHash = ""
	rest, err := Apply(ctx, exec, second, options)
	if err != nil {
		return first, second, report, err
	}
	report.Results = append(report.Results, rest.Results...)
	for status, n := range rest.Summary {
		report.Summary[status] += n
	}
	report.Complete = rest.Complete
	return first, second, report, nil
}
