// Package routines runs the checks a business makes on its Odoo every day, week or month, and hands
// what Odoo answered to the agent the owner chose to read it.
//
// A routine asks Odoo the same questions on a schedule (sales yesterday, invoices overdue, transfers
// late, a books review of last month), keeps what Odoo answered, and gives the answer to an agent:
// Claude Code, Codex, Gemini CLI, agy, any command, or none. The agent only reads: what changes Odoo is
// written in the routine as a step, never improvised by the agent.
//
// It is the model the asao-erp SDK introduced, moved into the command line so that running a
// business's routines needs no Python. Routines live in one file (TOML, YAML or JSON):
//
//	profile = "cong-ty"            # the odoo-cli profile the routines read
//	output = "~/asao-routines"     # where every run is kept
//	agent = "claude"               # claude | codex | gemini | agy | none | ["cmd", "arg", "{prompt}"]
//
//	[[routine]]
//	id = "cong-no-qua-han"
//	title = "Công nợ quá hạn"
//	every = "daily"                # daily | weekdays | weekly | monthly
//	at = "07:30"
//
//	  [[routine.step]]
//	  name = "theo-khach"
//	  group = "finance.invoice.list"
//	  by = ["partner_id"]
//	  aggregate = ["amount_residual:sum"]
//	  filter = [["payment_state", "in", ["not_paid", "partial"]], ["invoice_date_due", "<", "{today}"]]
//
// A step is one of read (records), group (Odoo's aggregates), review (the books review of a period),
// ensure_task (an Odoo task created once and never twice) and execute (any other write). A routine with
// a write step says writes = true. Dates in filters and values are {today}, {yesterday},
// {week_start}, {last_week_start}, {month_start}, {last_month_start} and {next_month_start}, moved by
// days ({today-7}) or months ({month_start-3m}), counted from the day the routine's slot falls on.
//
// Every run is kept in <output>/<routine>/<day>/: result.json (what Odoo answered), summary.md (counts
// and totals, computed, never written by a model) and, when an agent reads it, prompt.md and
// analysis.md. <output>/index.md lists each routine's latest run; AGENTS.md, CLAUDE.md and GEMINI.md
// there tell an interactive agent how to read them.
package routines

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The cadences, weekdays, step kinds and review periods a routines file may name.
var (
	Cadences  = []string{"daily", "weekdays", "weekly", "monthly"}
	Weekdays  = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	StepKinds = []string{"read", "group", "review", "ensure_task", "execute"}
	Periods   = []string{"yesterday", "last_7_days", "last_week", "this_month", "last_month"}
)

// writeKinds are the steps that change Odoo: a routine with one of them says writes = true.
var writeKinds = map[string]bool{"ensure_task": true, "execute": true}

// Grace is how late a missed slot still runs: a laptop that was off at 07:30 runs the daily routine
// when it wakes, but a monthly one is not run three weeks late.
var Grace = map[string]time.Duration{
	"daily":    20 * time.Hour,
	"weekdays": 20 * time.Hour,
	"weekly":   3 * 24 * time.Hour,
	"monthly":  7 * 24 * time.Hour,
}

// DefaultOutput is where runs are kept when the file does not say.
const DefaultOutput = "~/asao-routines"

// slotLayout is how a slot is written in result.json and latest.json: local time to the minute, the
// form the SDK's routines wrote, so a folder either one kept is read the same by both.
const slotLayout = "2006-01-02T15:04"

const placeholderPattern = `\{(today|yesterday|week_start|last_week_start|month_start|last_month_start|next_month_start)(?:([+-])(\d+)(m?))?\}`

var (
	routineIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	clockPattern     = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	bracesPattern    = regexp.MustCompile(`\{[^{}]*\}`)
	placeholder      = regexp.MustCompile(placeholderPattern)
	placeholderWhole = regexp.MustCompile(`^` + placeholderPattern + `$`)
)

// Error is a routines file that cannot run as written: the message names the routine and the field.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func invalid(format string, args ...interface{}) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

// Step is one question a routine asks Odoo, or one change it makes.
type Step struct {
	Name      string
	Kind      string
	Operation string
	Filter    []interface{}
	By        []string
	Aggregate []string
	Limit     int
	Order     string
	Period    string
	Values    map[string]interface{}
	// ID names one record (--provider-target-id), when the step reads or changes one.
	ID   *int64
	Note string
}

// Routine is one check on a schedule.
type Routine struct {
	ID    string
	Title string
	Area  string
	Every string
	At    string
	// On is the weekday of a weekly routine ("mon"), or the day of a monthly one: an int 1-31 or
	// "last". It is nil for daily and weekdays routines.
	On     interface{}
	Prompt string
	// Agent overrides the file's agent for this routine; nil keeps the file's.
	Agent   interface{}
	Writes  bool
	Enabled bool
	Steps   []Step
}

// File is a routines file, checked.
type File struct {
	Routines     []Routine
	Profile      string
	Organization string
	Connection   string
	// Output is the folder runs are kept in, with ~ expanded.
	Output string
	// Agent is "none", an agent's name (claude, codex, gemini, agy), a command, or a command line as
	// a list of arguments where {prompt}, {answer_file} and {folder} are filled in.
	Agent           interface{}
	Language        string
	AgentTimeout    time.Duration
	AllowUnverified bool
	// Source is the file the routines were read from.
	Source string
}

// Get finds a routine by id.
func (f *File) Get(id string) (*Routine, error) {
	for index := range f.Routines {
		if f.Routines[index].ID == id {
			return &f.Routines[index], nil
		}
	}
	ids := make([]string, 0, len(f.Routines))
	for _, routine := range f.Routines {
		ids = append(ids, routine.ID)
	}
	return nil, invalid("no routine %q; the file has: %s", id, strings.Join(ids, ", "))
}

// FromMap reads a decoded routines file (TOML, YAML or JSON) and checks all of it before anything runs.
func FromMap(data map[string]interface{}) (*File, error) {
	profile := text(data["profile"])
	organization := text(data["organization"])
	if strings.TrimSpace(profile) == "" && strings.TrimSpace(organization) == "" {
		return nil, invalid(`say which Odoo: profile = "..." (odoo-cli) or organization = "..." (asao CLI)`)
	}
	file := &File{
		Profile:      profile,
		Organization: organization,
		Connection:   text(data["connection"]),
		Output:       ExpandHome(DefaultOutput),
		Agent:        "none",
		Language:     "vi",
		AgentTimeout: 900 * time.Second,
	}
	if output := strings.TrimSpace(text(data["output"])); output != "" {
		file.Output = ExpandHome(output)
	}
	if raw, ok := data["agent"]; ok && raw != nil {
		agent, err := agentValue(raw, "agent")
		if err != nil {
			return nil, err
		}
		file.Agent = agent
	}
	if raw, ok := data["language"]; ok {
		language := text(raw)
		if language != "vi" && language != "en" {
			return nil, invalid("language = %q; vi or en", language)
		}
		file.Language = language
	}
	if raw, ok := data["agent_timeout"]; ok {
		seconds, isNumber := number(raw)
		if !isNumber || seconds <= 0 {
			return nil, invalid("agent_timeout = %v; seconds, a number above 0", raw)
		}
		file.AgentTimeout = time.Duration(seconds * float64(time.Second))
	}
	if raw, ok := data["allow_unverified"]; ok {
		allow, isBool := raw.(bool)
		if !isBool {
			return nil, invalid("allow_unverified = %v; true or false", raw)
		}
		file.AllowUnverified = allow
	}
	items := tables(data["routine"])
	if items == nil {
		items = tables(data["routines"])
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item == nil {
			return nil, invalid("a routine is a table of fields")
		}
		routine, err := parseRoutine(item)
		if err != nil {
			return nil, err
		}
		if seen[routine.ID] {
			return nil, invalid("two routines are called %q", routine.ID)
		}
		seen[routine.ID] = true
		file.Routines = append(file.Routines, routine)
	}
	return file, nil
}

func parseRoutine(item map[string]interface{}) (Routine, error) {
	id := text(item["id"])
	if !routineIDPattern.MatchString(id) {
		return Routine{}, invalid("routine id %q: lower-case letters, digits and dashes", id)
	}
	routine := Routine{ID: id, Title: id, Every: "daily", At: "07:30", Enabled: true}
	if raw, ok := item["every"]; ok {
		routine.Every = text(raw)
	}
	if !contains(Cadences, routine.Every) {
		return Routine{}, invalid("%s: every = %q; one of %s", id, routine.Every, strings.Join(Cadences, ", "))
	}
	if raw, ok := item["at"]; ok {
		routine.At = text(raw)
	}
	if !clockPattern.MatchString(routine.At) {
		return Routine{}, invalid("%s: at = %q; a time as HH:MM", id, routine.At)
	}
	on := item["on"]
	switch routine.Every {
	case "weekly":
		day := "mon"
		if on != nil {
			day = text(on)
		}
		if !contains(Weekdays, day) {
			return Routine{}, invalid("%s: on = %v; a weekday, one of %s", id, on, strings.Join(Weekdays, ", "))
		}
		routine.On = day
	case "monthly":
		if on != nil {
			if day, isInt := integer(on); isInt && day >= 1 && day <= 31 {
				routine.On = int(day)
			} else if text(on) == "last" {
				routine.On = "last"
			} else {
				return Routine{}, invalid(`%s: on = %v; a day of the month (1-31) or "last"`, id, on)
			}
		}
	}
	if title := text(item["title"]); title != "" {
		routine.Title = title
	}
	routine.Area = text(item["area"])
	routine.Prompt = text(item["prompt"])
	if raw, ok := item["agent"]; ok && raw != nil {
		agent, err := agentValue(raw, id+": agent")
		if err != nil {
			return Routine{}, err
		}
		routine.Agent = agent
	}
	for _, flag := range []struct {
		name   string
		target *bool
	}{{"writes", &routine.Writes}, {"enabled", &routine.Enabled}} {
		if raw, ok := item[flag.name]; ok {
			value, isBool := raw.(bool)
			if !isBool {
				return Routine{}, invalid("%s: %s = %v; true or false", id, flag.name, raw)
			}
			*flag.target = value
		}
	}
	steps := tables(item["step"])
	if steps == nil {
		steps = tables(item["steps"])
	}
	if len(steps) == 0 {
		return Routine{}, invalid("%s: a routine needs at least one step", id)
	}
	var writing []string
	for _, raw := range steps {
		if raw == nil {
			return Routine{}, invalid("%s: a step is a table of fields", id)
		}
		step, err := parseStep(id, raw)
		if err != nil {
			return Routine{}, err
		}
		if writeKinds[step.Kind] {
			writing = append(writing, step.Name)
		}
		routine.Steps = append(routine.Steps, step)
	}
	if len(writing) > 0 && !routine.Writes {
		return Routine{}, invalid("%s: steps %s change Odoo; say writes = true to let this routine write", id, strings.Join(writing, ", "))
	}
	return routine, nil
}

func parseStep(routineID string, raw map[string]interface{}) (Step, error) {
	var kinds []string
	for _, kind := range StepKinds {
		if _, ok := raw[kind]; ok {
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) != 1 {
		got := "none"
		if len(kinds) > 0 {
			got = "[" + strings.Join(kinds, ", ") + "]"
		}
		return Step{}, invalid("%s: a step names exactly one of %s; got %s", routineID, strings.Join(StepKinds, ", "), got)
	}
	kind := kinds[0]
	name := text(raw["name"])
	if name == "" {
		name = kind
	}
	step := Step{Name: name, Kind: kind, Limit: 50, Order: text(raw["order"]), Note: text(raw["note"])}
	where := routineID + "." + name
	switch kind {
	case "read", "group", "execute":
		operation, isText := raw[kind].(string)
		if !isText || strings.TrimSpace(operation) == "" {
			return Step{}, invalid(`%s: %s = "<operation id>"`, where, kind)
		}
		step.Operation = strings.TrimSpace(operation)
	case "ensure_task":
		step.Operation = "work.task.create"
	case "review":
		step.Period = "last_month"
		if period, ok := raw["period"]; ok {
			step.Period = text(period)
		}
		if !contains(Periods, step.Period) {
			return Step{}, invalid("%s: period = %q; one of %s", where, step.Period, strings.Join(Periods, ", "))
		}
	}
	values := raw["values"]
	if kind == "ensure_task" {
		values = raw[kind]
		table, isTable := asTable(values)
		if !isTable || text(table["name"]) == "" || table["project"] == nil {
			return Step{}, invalid(`%s: ensure_task = { name = "...", project = "...", ... }`, where)
		}
	}
	if values != nil {
		table, isTable := asTable(values)
		if !isTable {
			return Step{}, invalid("%s: values = { field = value, ... }", where)
		}
		step.Values = table
	}
	if filter, ok := raw["filter"]; ok && filter != nil {
		domain, isList := asList(filter)
		if !isList {
			return Step{}, invalid(`%s: filter = [["field", "=", value], ...]`, where)
		}
		step.Filter = domain
	}
	for _, field := range []struct {
		name   string
		target *[]string
	}{{"by", &step.By}, {"aggregate", &step.Aggregate}} {
		if raw, ok := raw[field.name]; ok && raw != nil {
			items, isList := asList(raw)
			if !isList {
				return Step{}, invalid(`%s: %s = ["...", ...]`, where, field.name)
			}
			for _, item := range items {
				name, isText := item.(string)
				if !isText || strings.TrimSpace(name) == "" {
					return Step{}, invalid(`%s: %s = ["...", ...]`, where, field.name)
				}
				*field.target = append(*field.target, name)
			}
		}
	}
	if raw, ok := raw["limit"]; ok {
		limit, isInt := integer(raw)
		if !isInt || limit < 1 {
			return Step{}, invalid("%s: limit = %v; a whole number above 0", where, raw)
		}
		step.Limit = int(limit)
	}
	if raw, ok := raw["id"]; ok && raw != nil {
		id, isInt := integer(raw)
		if !isInt || id < 1 {
			return Step{}, invalid("%s: id = %v; a record id", where, raw)
		}
		step.ID = &id
	}
	for _, value := range stringsIn([]interface{}{raw["filter"], values}) {
		for _, mark := range bracesPattern.FindAllString(value, -1) {
			if !placeholderWhole.MatchString(mark) {
				return Step{}, invalid("%s: unknown date %s", where, mark)
			}
		}
	}
	return step, nil
}

// agentValue checks an agent: a name or a command as text, or a command line as a list of texts.
func agentValue(raw interface{}, where string) (interface{}, error) {
	if name, isText := raw.(string); isText {
		return strings.TrimSpace(name), nil
	}
	items, isList := asList(raw)
	if !isList || len(items) == 0 {
		return nil, invalid(`%s = %v; an agent's name (claude, codex, gemini, agy, none) or a command line ["cmd", "arg", "{prompt}"]`, where, raw)
	}
	command := make([]string, 0, len(items))
	for _, item := range items {
		part, isText := item.(string)
		if !isText {
			return nil, invalid(`%s: every part of the command line is text`, where)
		}
		command = append(command, part)
	}
	if strings.TrimSpace(command[0]) == "" {
		return nil, invalid(`%s: the command line starts with the program`, where)
	}
	return command, nil
}

// ExpandHome writes a leading ~ out as the user's home folder.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// ---- the shapes a decoder hands over -----------------------------------------------------------
//
// TOML, YAML and JSON decoders give the same file in different Go types: arrays of tables are
// []map[string]interface{} from TOML and []interface{} from the others, and a whole number is int64,
// int, float64 or json.Number. These helpers read all of them alike.

func text(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func asTable(value interface{}) (map[string]interface{}, bool) {
	table, ok := value.(map[string]interface{})
	return table, ok
}

func asList(value interface{}) ([]interface{}, bool) {
	switch typed := value.(type) {
	case []interface{}:
		return typed, true
	case []map[string]interface{}:
		out := make([]interface{}, len(typed))
		for index := range typed {
			out[index] = typed[index]
		}
		return out, true
	case []string:
		out := make([]interface{}, len(typed))
		for index := range typed {
			out[index] = typed[index]
		}
		return out, true
	}
	return nil, false
}

// tables reads an array of tables; a member that is not a table comes back nil, for the caller to
// refuse with its own words.
func tables(value interface{}) []map[string]interface{} {
	items, ok := asList(value)
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, len(items))
	for index, item := range items {
		out[index], _ = item.(map[string]interface{})
	}
	return out
}

func integer(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	case uint64:
		return int64(typed), true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func number(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case int, int64, int32, uint64:
		whole, _ := integer(typed)
		return float64(whole), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	}
	return 0, false
}

// isWhole says whether a number is written without a fraction, as Python tells an int from a float.
func isWhole(value interface{}) bool {
	switch typed := value.(type) {
	case int, int64, int32, uint64:
		return true
	case json.Number:
		return !strings.ContainsAny(string(typed), ".eE")
	}
	return false
}

func stringsIn(value interface{}) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case map[string]interface{}:
		var out []string
		for _, item := range typed {
			out = append(out, stringsIn(item)...)
		}
		return out
	default:
		items, ok := asList(value)
		if !ok {
			return nil
		}
		var out []string
		for _, item := range items {
			out = append(out, stringsIn(item)...)
		}
		return out
	}
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// formatInt writes a whole number with thousands separators, as Python's "{:,}" does.
func formatInt(value int64) string {
	digits := strconv.FormatInt(value, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var grouped strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	return sign + grouped.String()
}
