// Package controls is the catalogue of Odoo's own switches that stop what a review finds, and the
// reading and planning of them (docs/design/CONTROLS_v0_1.md). It adds nothing to Odoo: a control is
// a setting, a company's lock dates or a journal's flag, read through the catalog's operations;
// turning controls on is a business spec, planned and applied by packages/odoo/spec.
package controls

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/odoo/spec"
)

//go:embed controls.json
var catalogueJSON []byte

// Where Odoo enforces a control.
const (
	// EnforcedServer: every caller, the API included.
	EnforcedServer = "SERVER"
	// EnforcedWebClient: the screens only; a caller of the API is not stopped.
	EnforcedWebClient = "WEB_CLIENT"
	// EnforcedWarning: Odoo warns and lets the user go on.
	EnforcedWarning = "WARNING"
)

// The state of a control in one Odoo.
const (
	StateOn           = "ON"
	StateOff          = "OFF"
	StatePartial      = "PARTIAL"
	StateNotAvailable = "NOT_AVAILABLE"
	// StateUnread: the read it needs was refused or could not run; Reason says why.
	StateUnread = "UNREAD"
)

// The operations controls read with. The settings are read and written by the spec engine.
const (
	settingsRead    = "structure.settings.describe"
	companyList     = "structure.company.list"
	companyDescribe = "structure.company.describe"
	journalList     = "finance.journal.list"
	journalDescribe = "finance.journal.describe"
	hardLockField   = "hard_lock_date"
	hashField       = "restrict_mode_hash_table"
)

// Text is a sentence in Vietnamese (vi) and English (en).
type Text map[string]string

// Setting is one field of the Settings page a control turns on.
type Setting struct {
	Field string `json:"field"`
	// Set is the value turning the control on writes.
	Set interface{} `json:"set,omitempty"`
	// On are the values that count as on; by default Set alone.
	On []interface{} `json:"on,omitempty"`
	// Param names the parameter that fills the field. Such a field is read and shown, and written
	// only when the parameter is given; it never decides whether the control is on.
	Param string `json:"param,omitempty"`
}

// Param is a value the person gives when turning a control on.
type Param struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Required    bool   `json:"required,omitempty"`
	Description Text   `json:"description"`
}

// Control is one switch.
type Control struct {
	ID             string    `json:"id"`
	Title          Text      `json:"title"`
	Prevents       Text      `json:"prevents"`
	Odoo           Text      `json:"odoo"`
	Source         []string  `json:"source"`
	Enforced       string    `json:"enforced"`
	Reversible     bool      `json:"reversible"`
	ReversibleNote Text      `json:"reversibleNote,omitempty"`
	Applies        bool      `json:"applies"`
	Manual         Text      `json:"manual,omitempty"`
	Modules        []string  `json:"modules"`
	Checks         []string  `json:"checks"`
	Patterns       []string  `json:"patterns"`
	Settings       []Setting `json:"settings,omitempty"`
	CompanyFields  []string  `json:"companyFields,omitempty"`
	JournalTypes   []string  `json:"journalTypes,omitempty"`
	Params         []Param   `json:"params,omitempty"`
	Changes        Text      `json:"changes"`
}

// Catalogue is every control.
type Catalogue struct {
	SchemaVersion string    `json:"schemaVersion"`
	About         string    `json:"about"`
	Controls      []Control `json:"controls"`
}

var (
	catalogueOnce sync.Once
	catalogue     Catalogue
	catalogueErr  error
)

// Load returns the catalogue.
func Load() (Catalogue, error) {
	catalogueOnce.Do(func() { catalogueErr = json.Unmarshal(catalogueJSON, &catalogue) })
	return catalogue, catalogueErr
}

// Find returns one control.
func (c Catalogue) Find(id string) (Control, bool) {
	for _, control := range c.Controls {
		if control.ID == strings.ToUpper(strings.TrimSpace(id)) {
			return control, true
		}
	}
	return Control{}, false
}

// PreventedBy lists the controls that prevent what a review check finds.
func PreventedBy(check string) []string {
	all, err := Load()
	if err != nil {
		return nil
	}
	var out []string
	for _, control := range all.Controls {
		for _, id := range control.Checks {
			if id == check {
				out = append(out, control.ID)
			}
		}
	}
	return out
}

// Options are the caller's choices for reading and planning.
type Options struct {
	// AllowUnverified reads through operations without current conformance evidence (a candidate
	// series, a customised Odoo), as `execute --allow-unverified` does.
	AllowUnverified bool
	// CompanyID names the company whose lock dates and journals are read; 0 takes the database's
	// only company.
	CompanyID int64
	// Today is the day lock dates are judged against; zero means now.
	Today time.Time
}

// Status is the state of one control in one Odoo.
type Status struct {
	ID       string                 `json:"id"`
	Title    Text                   `json:"title"`
	State    string                 `json:"state"`
	Enforced string                 `json:"enforced"`
	Applies  bool                   `json:"applies"`
	Detail   map[string]interface{} `json:"detail,omitempty"`
	Reason   string                 `json:"reason,omitempty"`
	Checks   []string               `json:"checks,omitempty"`
	Patterns []string               `json:"patterns,omitempty"`
}

// Company is a company of the database.
type Company struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Report is the state of every control.
type Report struct {
	ProviderSeries string         `json:"providerSeries,omitempty"`
	Company        *Company       `json:"company,omitempty"`
	Companies      []Company      `json:"companies,omitempty"`
	Controls       []Status       `json:"controls"`
	Summary        map[string]int `json:"summary"`
}

// reader runs the reads one status or plan needs, and keeps what it read.
type reader struct {
	exec    spec.Executor
	options Options
	series  string
}

func (r *reader) read(ctx context.Context, request direct.Request) ([]map[string]interface{}, error) {
	request.AllowUnverified = r.options.AllowUnverified
	outcome, err := r.exec.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	if r.series == "" {
		r.series = outcome.ProviderSeries
	}
	return records(outcome.Data), nil
}

// company resolves the company controls act on, and lists every company when there are several.
func (r *reader) company(ctx context.Context) (*Company, []Company, error) {
	rows, err := r.read(ctx, direct.Request{Operation: companyList, Limit: 50, Order: "id"})
	if err != nil {
		return nil, nil, err
	}
	all := make([]Company, 0, len(rows))
	for _, row := range rows {
		all = append(all, Company{ID: idOf(row), Name: fmt.Sprint(row["name"])})
	}
	for _, company := range all {
		if r.options.CompanyID != 0 && company.ID == r.options.CompanyID {
			chosen := company
			return &chosen, all, nil
		}
	}
	if r.options.CompanyID != 0 {
		return nil, all, fmt.Errorf("%w %d", errNoCompany, r.options.CompanyID)
	}
	if len(all) == 1 {
		return &all[0], all, nil
	}
	return nil, all, nil
}

// errNoCompany: --company names a company the database does not have.
var errNoCompany = errors.New("controls: the database has no company")

// Read reads the state of every control.
func Read(ctx context.Context, exec spec.Executor, options Options) (Report, error) {
	all, err := Load()
	if err != nil {
		return Report{}, err
	}
	r := &reader{exec: exec, options: options}
	report := Report{Summary: map[string]int{}}

	settings, settingsErr := r.read(ctx, direct.Request{Operation: settingsRead})
	if settingsErr != nil && !refusal(settingsErr) {
		return Report{}, settingsErr
	}
	company, companies, companyErr := r.company(ctx)
	if companyErr != nil && !refusal(companyErr) && !errors.Is(companyErr, errNoCompany) {
		return Report{}, companyErr
	}
	report.Company = company
	if company == nil || len(companies) > 1 {
		report.Companies = companies
	}
	var companyRow map[string]interface{}
	var companyReadErr error
	if company != nil {
		rows, err := r.read(ctx, direct.Request{Operation: companyDescribe, ProviderTargetID: company.ID})
		switch {
		case err != nil:
			companyReadErr = err
		case len(rows) == 1:
			companyRow = rows[0]
		}
	}

	for _, control := range all.Controls {
		status := Status{ID: control.ID, Title: control.Title, Enforced: control.Enforced, Applies: control.Applies,
			Checks: control.Checks, Patterns: control.Patterns}
		switch {
		case len(control.Settings) > 0:
			if settingsErr != nil {
				status.State, status.Reason = StateUnread, settingsErr.Error()
				break
			}
			values := map[string]interface{}{}
			if len(settings) == 1 {
				values = settings[0]
			}
			status.State, status.Detail, status.Reason = settingsState(control, values)
		case len(control.CompanyFields) > 0:
			switch {
			case companyErr != nil:
				status.State, status.Reason = StateUnread, companyErr.Error()
			case company == nil:
				status.State, status.Reason = StateUnread, fmt.Sprintf("the database has %d companies: name one with --company", len(companies))
			case companyReadErr != nil:
				status.State, status.Reason = StateUnread, companyReadErr.Error()
			default:
				status.State, status.Detail, status.Reason = lockState(control, companyRow, options.today())
			}
		case len(control.JournalTypes) > 0:
			status.State, status.Detail, status.Reason = r.hashState(ctx, control, company)
		}
		report.Summary[status.State]++
		report.Controls = append(report.Controls, status)
	}
	report.ProviderSeries = r.series
	return report, nil
}

func (o Options) today() time.Time {
	if o.Today.IsZero() {
		return time.Now()
	}
	return o.Today
}

// settingsState: ON when every switch holds an on value, OFF when none does.
func settingsState(control Control, values map[string]interface{}) (string, map[string]interface{}, string) {
	detail := map[string]interface{}{}
	on, switches := 0, 0
	for _, setting := range control.Settings {
		value, present := values[setting.Field]
		if !present {
			return StateNotAvailable, nil, fmt.Sprintf("this Odoo has no setting %s: module %s is not installed",
				setting.Field, strings.Join(control.Modules, ", "))
		}
		detail[setting.Field] = value
		if setting.Param != "" {
			continue
		}
		switches++
		if setting.isOn(value) {
			on++
		}
	}
	switch {
	case on == switches:
		return StateOn, detail, ""
	case on == 0:
		return StateOff, detail, ""
	}
	return StatePartial, detail, ""
}

func (s Setting) isOn(value interface{}) bool {
	on := s.On
	if len(on) == 0 {
		on = []interface{}{s.Set}
	}
	for _, want := range on {
		if fmt.Sprint(want) == fmt.Sprint(value) && (want == nil) == (value == nil) {
			return true
		}
	}
	return false
}

// lockState: ON when every lock date is set. Detail says up to which day the books are locked for
// everything (the earliest of the dates) and the hard lock date, which nobody can move back.
func lockState(control Control, company map[string]interface{}, today time.Time) (string, map[string]interface{}, string) {
	detail := map[string]interface{}{}
	set := 0
	earliest := ""
	for _, field := range control.CompanyFields {
		day := dateOf(company[field])
		detail[field] = day
		if day == "" {
			continue
		}
		set++
		if earliest == "" || day < earliest {
			earliest = day
		}
	}
	if hard := dateOf(company[hardLockField]); hard != "" {
		detail[hardLockField] = hard
	}
	if set == 0 {
		return StateOff, detail, ""
	}
	detail["lockedThrough"] = earliest
	reason := ""
	if locked, err := time.Parse("2006-01-02", earliest); err == nil {
		// By the middle of a month the one before last is reported: a lock before its end leaves it open.
		monthBeforeLastEnds := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, -1)
		if locked.Before(monthBeforeLastEnds) {
			reason = fmt.Sprintf("locked through %s only: every month since is open", earliest)
		}
	}
	if set < len(control.CompanyFields) {
		return StatePartial, detail, reason
	}
	return StateOn, detail, reason
}

// hashState: ON when every journal of the control's types hashes its posted entries.
func (r *reader) hashState(ctx context.Context, control Control, company *Company) (string, map[string]interface{}, string) {
	domain := []interface{}{[]interface{}{"type", "in", control.JournalTypes}}
	if company != nil {
		domain = append(domain, []interface{}{"company_id", "=", company.ID})
	}
	journals, err := r.read(ctx, direct.Request{Operation: journalList, Domain: domain, Limit: 200, Order: "id"})
	if err != nil {
		return StateUnread, nil, err.Error()
	}
	if len(journals) == 0 {
		return StateNotAvailable, nil, "no sales, purchase or general journal"
	}
	var rows []map[string]interface{}
	hashed := 0
	for _, journal := range journals {
		described, err := r.read(ctx, direct.Request{Operation: journalDescribe, ProviderTargetID: idOf(journal)})
		if err != nil {
			return StateUnread, nil, err.Error()
		}
		on := len(described) == 1 && described[0][hashField] == true
		if on {
			hashed++
		}
		rows = append(rows, map[string]interface{}{"id": idOf(journal), "code": journal["code"], "name": journal["name"],
			"type": journal["type"], "company": journal["company_id"], "hashed": on})
	}
	detail := map[string]interface{}{"journals": rows}
	switch hashed {
	case len(journals):
		return StateOn, detail, ""
	case 0:
		return StateOff, detail, ""
	}
	return StatePartial, detail, ""
}

// Selection is what the person asks to turn on.
type Selection struct {
	IDs []string
	// Params are CONTROL.NAME=VALUE (K04.date=2026-09-30).
	Params map[string]string
}

// Manual is a control odoo-cli does not turn on, with the steps to take in Odoo.
type Manual struct {
	ID    string `json:"id"`
	Title Text   `json:"title"`
	Steps Text   `json:"steps"`
	Why   Text   `json:"why,omitempty"`
}

// Chosen is a control the plan turns on.
type Chosen struct {
	ID       string `json:"id"`
	Title    Text   `json:"title"`
	Enforced string `json:"enforced"`
	Changes  Text   `json:"changes"`
}

// Plan is what turning controls on would change: the spec the controls render to, and its plan.
type Plan struct {
	Controls []Chosen  `json:"controls"`
	Manual   []Manual  `json:"manual,omitempty"`
	Company  *Company  `json:"company,omitempty"`
	Spec     spec.Spec `json:"spec"`
	Plan     spec.Plan `json:"plan"`
}

// ErrSelection is a selection the controls cannot be planned from: an unknown control or
// parameter, a missing or malformed value, a lock date that would reopen a closed period.
var ErrSelection = errors.New("controls")

func selectionError(format string, args ...interface{}) error {
	return fmt.Errorf("%w: %s", ErrSelection, fmt.Sprintf(format, args...))
}

// Render turns a selection into a business spec, reading what it must not undo: the settings the
// database has, and the lock dates already set.
func Render(ctx context.Context, exec spec.Executor, selection Selection, options Options) (spec.Spec, []Chosen, []Manual, *Company, error) {
	all, err := Load()
	if err != nil {
		return spec.Spec{}, nil, nil, nil, err
	}
	if len(selection.IDs) == 0 {
		return spec.Spec{}, nil, nil, nil, selectionError("name the controls to turn on, e.g. --enable K01,K04")
	}
	chosen := map[string]Control{}
	var order []string
	for _, id := range selection.IDs {
		control, ok := all.Find(id)
		if !ok {
			return spec.Spec{}, nil, nil, nil, selectionError("no control %s (see controls list)", strings.TrimSpace(id))
		}
		if _, seen := chosen[control.ID]; !seen {
			order = append(order, control.ID)
		}
		chosen[control.ID] = control
	}
	sort.Strings(order)
	params := map[string]string{}
	for key, value := range selection.Params {
		id, name, _ := strings.Cut(strings.TrimSpace(key), ".")
		params[strings.ToUpper(id)+"."+name] = value
	}
	selection.Params = params
	for key := range selection.Params {
		id, name, _ := strings.Cut(key, ".")
		control, ok := chosen[id]
		if !ok {
			return spec.Spec{}, nil, nil, nil, selectionError("parameter %s is for a control not chosen", key)
		}
		if !control.hasParam(name) {
			return spec.Spec{}, nil, nil, nil, selectionError("control %s has no parameter %s", control.ID, name)
		}
	}

	r := &reader{exec: exec, options: options}
	document := spec.Spec{APIVersion: spec.APIVersion, Kind: spec.Kind,
		Metadata: spec.Metadata{Name: "controls " + strings.Join(order, ","),
			Description: "Odoo's own controls turned on by odoo-cli controls (docs/design/CONTROLS_v0_1.md)."},
		Settings: map[string]interface{}{}, Resources: []spec.Resource{}}
	var picked []Chosen
	var manual []Manual
	var company *Company
	var settings map[string]interface{}
	for _, id := range order {
		control := chosen[id]
		if !control.Applies {
			manual = append(manual, Manual{ID: control.ID, Title: control.Title, Steps: control.Manual, Why: control.ReversibleNote})
			continue
		}
		picked = append(picked, Chosen{ID: control.ID, Title: control.Title, Enforced: control.Enforced, Changes: control.Changes})
		if len(control.Settings) > 0 && settings == nil {
			rows, err := r.read(ctx, direct.Request{Operation: settingsRead})
			if err != nil {
				return spec.Spec{}, nil, nil, nil, err
			}
			settings = map[string]interface{}{}
			if len(rows) == 1 {
				settings = rows[0]
			}
		}
		for _, setting := range control.Settings {
			if _, present := settings[setting.Field]; !present {
				return spec.Spec{}, nil, nil, nil, selectionError("%s needs module %s, which this Odoo does not have (no setting %s)",
					control.ID, strings.Join(control.Modules, ", "), setting.Field)
			}
			switch {
			case setting.Param != "":
				value, given := selection.Params[control.ID+"."+setting.Param]
				if !given {
					continue
				}
				amount, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(value), "_", ""), 64)
				if err != nil || amount < 0 {
					return spec.Spec{}, nil, nil, nil, selectionError("%s.%s must be a positive amount, got %q", control.ID, setting.Param, value)
				}
				document.Settings[setting.Field] = amount
			case setting.isOn(settings[setting.Field]):
				// Already on, perhaps with a stronger value than Set (all_required for employee_required):
				// kept as it is, so the plan says NO_CHANGE and never weakens it.
				document.Settings[setting.Field] = settings[setting.Field]
			default:
				document.Settings[setting.Field] = setting.Set
			}
		}
		if len(control.CompanyFields) > 0 {
			resource, at, err := r.lockResource(ctx, control, selection.Params, options.today())
			if err != nil {
				return spec.Spec{}, nil, nil, nil, err
			}
			company = at
			document.Resources = append(document.Resources, resource)
		}
	}
	if len(document.Settings) == 0 {
		document.Settings = nil
	}
	return document, picked, manual, company, nil
}

func (c Control) hasParam(name string) bool {
	for _, param := range c.Params {
		if param.Name == name {
			return true
		}
	}
	return false
}

// lockResource is the company record with its lock dates set to the date given, refused when that
// date is not a past day or when a lock date already set is later: odoo-cli never reopens a period.
func (r *reader) lockResource(ctx context.Context, control Control, params map[string]string, today time.Time) (spec.Resource, *Company, error) {
	raw, given := params[control.ID+".date"]
	if !given {
		return spec.Resource{}, nil, selectionError("%s needs the lock date: --param %s.date=YYYY-MM-DD (the last day of the closed period)", control.ID, control.ID)
	}
	day, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return spec.Resource{}, nil, selectionError("%s.date must be a day, YYYY-MM-DD, got %q", control.ID, raw)
	}
	todayDay := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	if !day.Before(todayDay) {
		return spec.Resource{}, nil, selectionError("%s.date %s is not a past day: locking today or later would stop today's work", control.ID, raw)
	}
	company, companies, err := r.company(ctx)
	if err != nil {
		return spec.Resource{}, nil, err
	}
	if company == nil {
		return spec.Resource{}, nil, selectionError("the database has %d companies: name the one to lock with --company", len(companies))
	}
	rows, err := r.read(ctx, direct.Request{Operation: companyDescribe, ProviderTargetID: company.ID})
	if err != nil {
		return spec.Resource{}, nil, err
	}
	current := map[string]interface{}{}
	if len(rows) == 1 {
		current = rows[0]
	}
	date := day.Format("2006-01-02")
	values := map[string]interface{}{}
	for _, field := range control.CompanyFields {
		if have := dateOf(current[field]); have > date {
			return spec.Resource{}, nil, selectionError("%s is already locked through %s, after %s: odoo-cli never moves a lock date back",
				field, have, date)
		}
		values[field] = date
	}
	return spec.Resource{Entity: "structure.company", Match: map[string]interface{}{"id": company.ID}, Values: values,
		Note: fmt.Sprintf("%s: lock the books of %s through %s", control.ID, company.Name, date)}, company, nil
}

// PlanControls renders a selection and plans it with the spec engine.
func PlanControls(ctx context.Context, exec spec.Executor, selection Selection, options Options) (Plan, error) {
	document, picked, manual, company, err := Render(ctx, exec, selection, options)
	if err != nil {
		return Plan{}, err
	}
	out := Plan{Controls: picked, Manual: manual, Company: company, Spec: document}
	if len(picked) == 0 {
		out.Plan = spec.Plan{Spec: document.Metadata.Name, Steps: []spec.Step{}, Summary: map[spec.Action]int{}}
		return out, nil
	}
	plan, err := spec.BuildPlan(ctx, exec, document)
	if err != nil {
		return Plan{}, err
	}
	out.Plan = plan
	return out, nil
}

// ApplyControls renders a selection again and applies it through the spec engine; options.PlanHash
// binds it to the plan the person reviewed.
func ApplyControls(ctx context.Context, exec spec.Executor, selection Selection, options Options, apply spec.ApplyOptions) (Plan, spec.Report, error) {
	document, picked, manual, company, err := Render(ctx, exec, selection, options)
	if err != nil {
		return Plan{}, spec.Report{}, err
	}
	out := Plan{Controls: picked, Manual: manual, Company: company, Spec: document}
	if len(picked) == 0 {
		return out, spec.Report{Spec: document.Metadata.Name, Complete: true, Results: []spec.StepResult{}, Summary: map[string]int{}}, nil
	}
	plan, _, report, err := spec.ApplySpec(ctx, exec, document, apply)
	out.Plan = plan
	return out, report, err
}

// refusal is an error about the call (a module missing, a model the series removed, an operation
// not verified), not the provider being unreachable.
func refusal(err error) bool {
	var refused *direct.OperationError
	return errors.As(err, &refused)
}

func dateOf(value interface{}) string {
	if s, ok := value.(string); ok && len(s) >= 10 {
		return s[:10]
	}
	return ""
}

func records(data interface{}) []map[string]interface{} {
	switch rows := data.(type) {
	case []map[string]interface{}:
		return rows
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			if record, ok := row.(map[string]interface{}); ok {
				out = append(out, record)
			}
		}
		return out
	case map[string]interface{}:
		return records(rows["data"])
	}
	return nil
}

func idOf(record map[string]interface{}) int64 {
	switch id := record["id"].(type) {
	case float64:
		return int64(id)
	case int:
		return int64(id)
	case int64:
		return id
	case json.Number:
		n, _ := id.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(id, 10, 64)
		return n
	}
	return 0
}
