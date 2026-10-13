package odoo

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

// Provider fit (DEC-2026-0157): conformance evidence proves an operation on one environment, the
// stock Community image of a series. Before acting, the standalone executor observes the Odoo it
// reaches instead of assuming it: which modules it runs, whether they are the image's own, and
// what was changed where the operation acts. An operation that changes records is refused there
// unless the caller accepts an unverified result.

//go:embed odoo_standard_modules.json
var standardModulesDocument []byte

// moduleBaseline is the module list of the image a series' evidence was recorded on
// (scripts/dev/odoo-standard-modules.py).
type moduleBaseline struct {
	image      string
	standard   map[string]bool
	enterprise map[string]bool
}

var standardModules = func() map[string]moduleBaseline {
	var document struct {
		Series map[string]struct {
			Image      string   `json:"image"`
			Standard   []string `json:"standard"`
			Enterprise []string `json:"enterprise"`
		} `json:"series"`
	}
	if err := json.Unmarshal(standardModulesDocument, &document); err != nil {
		panic("odoo: odoo_standard_modules.json is malformed: " + err.Error())
	}
	out := make(map[string]moduleBaseline, len(document.Series))
	for series, list := range document.Series {
		baseline := moduleBaseline{image: list.Image, standard: map[string]bool{}, enterprise: map[string]bool{}}
		for _, name := range list.Standard {
			baseline.standard[name] = true
		}
		for _, name := range list.Enterprise {
			baseline.enterprise[name] = true
		}
		out[series] = baseline
	}
	return out
}()

// StandardModules lists, sorted, the modules the stock image of series ships; nil for a series
// without a recorded list.
func StandardModules(series string) []string {
	baseline, ok := standardModules[series]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(baseline.standard))
	for name := range baseline.standard {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ModuleOrigin says where an installed module comes from.
type ModuleOrigin string

const (
	// ModuleStandard is shipped by the stock Community image of the series.
	ModuleStandard ModuleOrigin = "STANDARD"
	// ModuleEnterprise is Odoo's own Enterprise code (licensed OEEL-1, or an app the Community
	// image only advertises).
	ModuleEnterprise ModuleOrigin = "ENTERPRISE"
	// ModuleThirdParty is everything else: OCA, a partner's or the owner's own module, or a module
	// a newer build of the series added (which errs toward asking).
	ModuleThirdParty ModuleOrigin = "THIRD_PARTY"
)

// ClassifyModule classifies an installed module against the module list of the series' image. A
// series without a recorded list classifies every module as third-party.
func ClassifyModule(series, name, license string) ModuleOrigin {
	baseline := standardModules[series]
	switch {
	case license == "OEEL-1" || baseline.enterprise[name]:
		return ModuleEnterprise
	case baseline.standard[name]:
		return ModuleStandard
	default:
		return ModuleThirdParty
	}
}

const (
	EditionCommunity  = "COMMUNITY"
	EditionEnterprise = "ENTERPRISE"
	// EditionUnknown: this login could not read the installed modules, and the build does not say.
	EditionUnknown = "UNKNOWN"

	// HostingOdooCloud is an endpoint under odoo.com: Odoo Online or Odoo.sh. Anything else is
	// HostingElsewhere (self-hosted, or a partner's host); hosting cannot be proven from outside.
	HostingOdooCloud = "ODOO_CLOUD"
	HostingElsewhere = "ELSEWHERE"

	FitOK             = "OK"
	FitUnverifiedHere = "UNVERIFIED_HERE"
	FitRefused        = "REFUSED"
)

// CustomisedOdooNotice is ASAO's stance on an Odoo changed beyond the stock image (DEC-2026-0157,
// owner 2026-10-11). It goes with every verdict on such an Odoo, with the refusal of a write there,
// and with the doctor's report of it.
const CustomisedOdooNotice = "ASAO verifies each operation on the stock Odoo image of its series only. " +
	"On an Odoo changed beyond that image (Enterprise, third-party or in-house modules, Studio, automations, " +
	"fields added by hand) ASAO cannot verify and does not guarantee that operations work as described or " +
	"without error; an operation accepted there runs at the caller's own risk."

// ProviderFit is what the check observed about one Odoo.
type ProviderFit struct {
	ProviderVersion   string   `json:"providerVersion"`
	ProviderSeries    string   `json:"providerSeries"`
	Edition           string   `json:"edition"`
	Hosting           string   `json:"hosting"`
	StandardModules   int      `json:"standardModules"`
	EnterpriseModules []string `json:"enterpriseModules"`
	ThirdPartyModules []string `json:"thirdPartyModules"`
	Automations       bool     `json:"automationsInstalled"`
	// ModulesUnreadable: this login could not read the installed modules, so the check cannot tell
	// a stock Odoo from a customised one, nor which required modules are missing.
	ModulesUnreadable bool                `json:"modulesUnreadable,omitempty"`
	Models            map[string]ModelFit `json:"models,omitempty"`

	origins map[string]ModuleOrigin
}

// ModelFit is what was changed on one model, as far as the login could read it.
type ModelFit struct {
	// ExtendedBy lists the non-standard modules that define or extend the model.
	ExtendedBy []string `json:"extendedBy,omitempty"`
	// Automations lists the active automations that run when a record of the model changes.
	Automations []string `json:"automations,omitempty"`
	// ManualFields lists fields added by hand or by Studio; RequiredManualFields those of them a
	// create must supply.
	ManualFields         []string `json:"manualFields,omitempty"`
	RequiredManualFields []string `json:"requiredManualFields,omitempty"`
	// Unreadable says what this login could not read about the model, where it mattered.
	Unreadable []string `json:"unreadable,omitempty"`
}

// OperationFit is the check's verdict on one operation.
type OperationFit struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
	// Notes inform without deciding: a read on a database with non-standard modules still runs.
	Notes []string `json:"notes,omitempty"`
	// MissingModules names the required modules that are not installed (verdict REFUSED).
	MissingModules []string `json:"missingModules,omitempty"`
	// Notice is CustomisedOdooNotice whenever this Odoo is not the stock image, or cannot be told
	// apart from it.
	Notice string `json:"notice,omitempty"`
}

// FitReader is the authenticated client the check reads through. It reads provider metadata and
// raw field names, so it bypasses the series dialect.
type FitReader interface {
	ExecuteKw(ctx context.Context, model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error)
}

// RetryReads makes every read of the check retry as a READ operation does (Retry): the check only
// reads, so a transient failure is retried rather than refusing the operation it guards.
func RetryReads(reader FitReader, policy RetryPolicy) FitReader {
	return retryingReader{reader: reader, policy: policy}
}

type retryingReader struct {
	reader FitReader
	policy RetryPolicy
}

func (r retryingReader) ExecuteKw(ctx context.Context, model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error) {
	var res interface{}
	err := Retry(ctx, r.policy, StrategyRead, func(ctx context.Context) error {
		var err error
		res, err = r.reader.ExecuteKw(ctx, model, method, args, kwargs)
		return err
	})
	return res, err
}

// FetchProviderFit observes the Odoo client reaches: its modules, and what was changed on models.
// info is the version the series gate admitted; endpoint gives the hosting. It returns an error only
// when the provider could not be reached. What this login may not read is recorded instead, never
// with the provider's own words: a refusal can echo what it refused.
func FetchProviderFit(ctx context.Context, client FitReader, endpoint string, info ProviderVersionInfo, models []string) (ProviderFit, error) {
	series := info.Series()
	fit := ProviderFit{ProviderVersion: info.ServerVersion, ProviderSeries: series, Edition: EditionCommunity,
		Hosting: hostingOf(endpoint), EnterpriseModules: []string{}, ThirdPartyModules: []string{}, origins: map[string]ModuleOrigin{}}
	if strings.Contains(info.ServerVersion, "+e") {
		fit.Edition = EditionEnterprise
	}
	rows, err := searchRead(ctx, client, "ir.module.module", []interface{}{[]interface{}{"state", "=", "installed"}}, []string{"name", "license"})
	if err != nil && unreachable(err) {
		return ProviderFit{}, fmt.Errorf("the installed modules could not be read: %w", err)
	}
	if err != nil || !listsBase(rows) {
		// Refused, or an answer without base, which every Odoo installs: not a module list to trust.
		fit.ModulesUnreadable = true
		if fit.Edition == EditionCommunity {
			fit.Edition = EditionUnknown
		}
		if len(models) > 0 {
			fit.Models = map[string]ModelFit{}
			for _, model := range models {
				fit.Models[model] = ModelFit{Unreadable: []string{"which modules are installed and what they change on it (this login cannot read ir.module.module)"}}
			}
		}
		return fit, nil
	}
	for _, row := range rows {
		name, _ := row["name"].(string)
		if name == "" {
			continue
		}
		license, _ := row["license"].(string)
		origin := ClassifyModule(series, name, license)
		fit.origins[name] = origin
		switch origin {
		case ModuleStandard:
			fit.StandardModules++
		case ModuleEnterprise:
			fit.EnterpriseModules = append(fit.EnterpriseModules, name)
		default:
			fit.ThirdPartyModules = append(fit.ThirdPartyModules, name)
		}
	}
	sort.Strings(fit.EnterpriseModules)
	sort.Strings(fit.ThirdPartyModules)
	if len(fit.EnterpriseModules) > 0 {
		fit.Edition = EditionEnterprise
	}
	_, fit.Automations = fit.origins["base_automation"]
	if len(models) > 0 {
		fit.Models = fetchModelFits(ctx, client, fit, models)
	}
	return fit, nil
}

// fetchModelFits reads, for each model: its hand-added fields (fields_get, any login), the
// non-standard modules extending it (ir.model, an administrator's login) when such modules are
// installed, and its automations (base.automation, an administrator's login) when installed.
func fetchModelFits(ctx context.Context, client FitReader, fit ProviderFit, models []string) map[string]ModelFit {
	out := make(map[string]ModelFit, len(models))
	for _, model := range models {
		var mf ModelFit
		fields, err := client.ExecuteKw(ctx, model, "fields_get", []interface{}{}, map[string]interface{}{"attributes": []string{"manual", "required"}})
		if described, ok := fields.(map[string]interface{}); err == nil && ok {
			for name, raw := range described {
				attrs, _ := raw.(map[string]interface{})
				if manual, _ := attrs["manual"].(bool); !manual {
					continue
				}
				mf.ManualFields = append(mf.ManualFields, name)
				if required, _ := attrs["required"].(bool); required {
					mf.RequiredManualFields = append(mf.RequiredManualFields, name)
				}
			}
			sort.Strings(mf.ManualFields)
			sort.Strings(mf.RequiredManualFields)
		}
		out[model] = mf
	}

	if len(fit.EnterpriseModules)+len(fit.ThirdPartyModules) > 0 {
		rows, err := searchRead(ctx, client, "ir.model", []interface{}{[]interface{}{"model", "in", models}}, []string{"model", "modules"})
		if err != nil {
			markUnreadable(out, "which modules extend it (this login cannot read ir.model; an administrator's can)")
		}
		for _, row := range rows {
			model, _ := row["model"].(string)
			listed, _ := row["modules"].(string)
			mf := out[model]
			for _, module := range strings.Split(listed, ",") {
				module = strings.TrimSpace(module)
				if module != "" && fit.originOf(module) != ModuleStandard {
					mf.ExtendedBy = append(mf.ExtendedBy, module)
				}
			}
			sort.Strings(mf.ExtendedBy)
			out[model] = mf
		}
	}

	if fit.Automations {
		rows, err := searchRead(ctx, client, "base.automation",
			[]interface{}{[]interface{}{"model_name", "in", models}, []interface{}{"active", "=", true}},
			[]string{"name", "model_name", "trigger"})
		if err != nil {
			markUnreadable(out, "which automations run on it (this login cannot read base.automation; an administrator's can)")
		}
		for _, row := range rows {
			model, _ := row["model_name"].(string)
			trigger, _ := row["trigger"].(string)
			if !changeTrigger(trigger) {
				continue
			}
			name, _ := row["name"].(string)
			mf := out[model]
			mf.Automations = append(mf.Automations, fmt.Sprintf("%s (%s)", name, trigger))
			sort.Strings(mf.Automations)
			out[model] = mf
		}
	}
	return out
}

// unreachable is true for a failure to reach the provider, as opposed to the provider refusing a
// read: the former stays a reachability failure, the latter is recorded as what this login cannot
// see.
func unreachable(err error) bool {
	return isProviderUnreachableError(err) || IsRetryableError(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// listsBase is true when a module list names base, which every Odoo installs.
func listsBase(rows []map[string]interface{}) bool {
	for _, row := range rows {
		if name, _ := row["name"].(string); name == "base" {
			return true
		}
	}
	return false
}

// changeTrigger is true for an automation trigger that a create, write, delete, archive or posted
// message sets off. Time-based, onchange (web form) and webhook triggers are not set off by an
// operation.
func changeTrigger(trigger string) bool {
	switch trigger {
	case "on_time", "on_time_created", "on_time_updated", "on_change", "on_webhook":
		return false
	}
	return true
}

// originOf classifies a module the check found installed, by name alone when it was not.
func (f ProviderFit) originOf(module string) ModuleOrigin {
	if origin, installed := f.origins[module]; installed {
		return origin
	}
	return ClassifyModule(f.ProviderSeries, module, "")
}

func markUnreadable(models map[string]ModelFit, what string) {
	for model, mf := range models {
		mf.Unreadable = append(mf.Unreadable, what)
		models[model] = mf
	}
}

func searchRead(ctx context.Context, client FitReader, model string, domain []interface{}, fields []string) ([]map[string]interface{}, error) {
	res, err := client.ExecuteKw(ctx, model, "search_read", []interface{}{domain}, map[string]interface{}{"fields": fields})
	if err != nil {
		return nil, err
	}
	list, ok := res.([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected %s.search_read response %T", model, res)
	}
	rows := make([]map[string]interface{}, 0, len(list))
	for _, item := range list {
		if row, ok := item.(map[string]interface{}); ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func hostingOf(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return HostingElsewhere
	}
	host := strings.ToLower(u.Hostname())
	if host == "odoo.com" || strings.HasSuffix(host, ".odoo.com") {
		return HostingOdooCloud
	}
	return HostingElsewhere
}

// FitModels names the models an operation acts on, the ones whose changes the check reads.
func FitModels(op Odoo19Operation) []string {
	var models []string
	for _, model := range []string{op.ProviderModel, op.WizardModel} {
		if model != "" && !containsString(models, model) {
			models = append(models, model)
		}
	}
	return models
}

// ChangesRecords is true for a strategy that creates, writes, calls a method, runs a wizard or
// deletes. Reads and prints are not refused for what was changed around them.
func ChangesRecords(op Odoo19Operation) bool {
	return op.Strategy != StrategyRead && op.Strategy != StrategyReport
}

// Assess decides one operation against what the check observed: REFUSED when a required module is
// missing; UNVERIFIED_HERE when it changes records where this Odoo differs from the verified
// environment; OK otherwise.
func (f ProviderFit) Assess(op Odoo19Operation) OperationFit {
	verdict := OperationFit{Verdict: FitOK}
	if f.ModulesUnreadable {
		verdict.Notes = append(verdict.Notes, "this login cannot read the installed modules, so the check cannot tell a stock Odoo from a customised one")
	}
	for _, module := range op.RequiredModules {
		// A module the series merged into another is installed when that one is (SeriesModule).
		if _, installed := f.origins[SeriesModule(f.ProviderSeries, module)]; !installed && !f.ModulesUnreadable {
			verdict.MissingModules = append(verdict.MissingModules, module)
		}
	}
	if len(verdict.MissingModules) > 0 {
		verdict.Verdict = FitRefused
		verdict.Reasons = append(verdict.Reasons, "required modules are not installed: "+strings.Join(verdict.MissingModules, ", "))
		return verdict
	}
	if f.Edition == EditionEnterprise {
		verdict.Notes = append(verdict.Notes, "this Odoo is Enterprise; the evidence was recorded on Community")
	}
	if len(f.ThirdPartyModules) > 0 {
		verdict.Notes = append(verdict.Notes, "this Odoo runs modules that are not in the stock image: "+strings.Join(f.ThirdPartyModules, ", "))
	}
	if !ChangesRecords(op) {
		return verdict.withNotice()
	}
	creates := op.Strategy == StrategyFieldWrite && op.WriteMode == WriteModeCreate
	for _, model := range FitModels(op) {
		mf := f.Models[model]
		if len(mf.ExtendedBy) > 0 {
			verdict.Reasons = append(verdict.Reasons, fmt.Sprintf("%s is extended by modules outside the stock image: %s", model, strings.Join(mf.ExtendedBy, ", ")))
		}
		if len(mf.Automations) > 0 {
			verdict.Reasons = append(verdict.Reasons, fmt.Sprintf("automations run when a %s record changes: %s", model, strings.Join(mf.Automations, ", ")))
		}
		if creates && len(mf.RequiredManualFields) > 0 {
			verdict.Reasons = append(verdict.Reasons, fmt.Sprintf("%s has required fields added by hand or by Studio: %s", model, strings.Join(mf.RequiredManualFields, ", ")))
		}
		for _, what := range mf.Unreadable {
			verdict.Reasons = append(verdict.Reasons, fmt.Sprintf("this login cannot tell %s for %s", what, model))
		}
	}
	if len(verdict.Reasons) > 0 {
		verdict.Verdict = FitUnverifiedHere
	}
	return verdict.withNotice()
}

// withNotice adds CustomisedOdooNotice to a verdict on an Odoo that is not the stock image: one with
// reasons to hold a write back, or notes about the database (Enterprise, third-party modules, an
// unreadable module list).
func (v OperationFit) withNotice() OperationFit {
	if v.Verdict == FitUnverifiedHere || len(v.Notes) > 0 {
		v.Notice = CustomisedOdooNotice
	}
	return v
}

// EnvironmentUnverifiedError refuses an operation that changes records where this Odoo differs from
// the environment its evidence was recorded on. Nothing was executed.
type EnvironmentUnverifiedError struct {
	Operation string
	Reasons   []string
}

func (e *EnvironmentUnverifiedError) Error() string {
	return fmt.Sprintf("%s changes records where this Odoo differs from the environment it was verified on (%s); nothing was executed",
		e.Operation, strings.Join(e.Reasons, "; "))
}

func (e *EnvironmentUnverifiedError) ErrorClass() string {
	return string(ErrClassEnvironmentUnverified)
}

func (e *EnvironmentUnverifiedError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{"reasons": append([]string(nil), e.Reasons...), "notice": CustomisedOdooNotice}
}

// RequiredModulesMissingError refuses an operation whose required modules are not installed,
// before anything is sent for it.
type RequiredModulesMissingError struct {
	Operation string
	Required  []string
	Missing   []string
}

func (e *RequiredModulesMissingError) Error() string {
	return fmt.Sprintf("%s needs Odoo modules that are not installed: %s; nothing was executed", e.Operation, strings.Join(e.Missing, ", "))
}

func (e *RequiredModulesMissingError) ErrorClass() string { return string(ErrClassModuleNotInstalled) }

func (e *RequiredModulesMissingError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"required_modules": append([]string(nil), e.Required...),
		"missing_modules":  append([]string(nil), e.Missing...),
	}
}

// ProviderNotOdooError: the endpoint answered, but not as Odoo answers its version handshake. It is
// raised before login, so nothing (not the credential) was sent.
type ProviderNotOdooError struct {
	Detail string
}

func (e *ProviderNotOdooError) Error() string {
	return "the endpoint did not answer Odoo's version handshake (" + e.Detail + "): it is not Odoo, or a proxy hides Odoo's API; nothing was sent"
}

func (e *ProviderNotOdooError) ErrorClass() string { return string(ErrClassProviderNotOdoo) }

func (e *ProviderNotOdooError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{"detail": e.Detail}
}

// notOdoo says whether a failed version handshake means the endpoint is not Odoo: it answered with
// an HTTP status, a body that is not JSON, or closed the connection without an answer. A refused or
// timed-out connection is not an answer, and stays a reachability failure.
func notOdoo(err error) (*ProviderNotOdooError, bool) {
	var status *HTTPStatusError
	if errors.As(err, &status) {
		return &ProviderNotOdooError{Detail: fmt.Sprintf("HTTP %d", status.StatusCode)}, true
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return &ProviderNotOdooError{Detail: "the answer is not JSON"}, true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &ProviderNotOdooError{Detail: "the server closed the connection without an answer; an https endpoint addressed as http answers like this"}, true
	}
	return nil, false
}
