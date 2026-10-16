package routines

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ERP is what a routine asks of Odoo. The command line answers it through the catalog's operations,
// with the same readiness rule and refusals a typed command has.
type ERP interface {
	// Read returns the records of a read operation: a filtered page, or the one record id names.
	Read(ctx context.Context, operation string, domain []interface{}, limit int, order string, id *int64) ([]map[string]interface{}, error)
	// Group returns Odoo's groups for a read operation: by fields or date buckets, with aggregates.
	Group(ctx context.Context, operation string, by, aggregate []string, domain []interface{}, limit int, order string) ([]map[string]interface{}, error)
	// Review returns the books review of a period, as the review command prints it.
	Review(ctx context.Context, from, to, asOf time.Time) (map[string]interface{}, error)
	// Execute runs an operation that changes Odoo and returns its data.
	Execute(ctx context.Context, operation string, id *int64, values map[string]interface{}) (interface{}, error)
	// Describe says what each field an operation returns means, for the agent's prompt.
	Describe(operation string) []Meaning
}

// Meaning is one field an operation returns, as the catalog describes it.
type Meaning struct {
	Name          string `json:"name"`
	ProviderField string `json:"providerField"`
	Description   string `json:"description"`
}

// Refusal is a step Odoo or the command line refused, as the run keeps it.
type Refusal struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
	// Retryable marks a refusal that may pass unchanged later: Odoo could not be reached.
	Retryable bool `json:"retryable,omitempty"`
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Message }

// Result is one run of a routine: what Odoo answered at each step. It is kept as result.json.
type Result struct {
	Routine string       `json:"routine"`
	Title   string       `json:"title"`
	Area    string       `json:"area"`
	Every   string       `json:"every"`
	Slot    string       `json:"slot"`
	RanAt   string       `json:"ranAt"`
	Source  string       `json:"source"`
	Steps   []StepResult `json:"steps"`
	Errors  int          `json:"errors"`
	Seconds float64      `json:"seconds"`
	Agent   *AgentResult `json:"agent,omitempty"`
	// Folder is where the run was kept.
	Folder string `json:"folder"`
	// Retry says no step reached Odoo: the slot is not taken as run, so the next "due" runs it again.
	Retry bool `json:"retry,omitempty"`
}

// StepResult is what one step asked and what Odoo answered.
type StepResult struct {
	Name      string                    `json:"name"`
	Kind      string                    `json:"kind"`
	Operation *string                   `json:"operation"`
	Asked     string                    `json:"asked"`
	Filter    interface{}               `json:"filter,omitempty"`
	By        []string                  `json:"by,omitempty"`
	Rows      *[]map[string]interface{} `json:"rows,omitempty"`
	Groups    *[]map[string]interface{} `json:"groups,omitempty"`
	Totals    map[string]interface{}    `json:"totals,omitempty"`
	Period    map[string]string         `json:"period,omitempty"`
	Report    map[string]interface{}    `json:"report,omitempty"`
	Task      *TaskResult               `json:"task,omitempty"`
	Data      interface{}               `json:"data,omitempty"`
	Count     *int                      `json:"count,omitempty"`
	Complete  *bool                     `json:"complete,omitempty"`
	Status    string                    `json:"status"`
	Error     *Refusal                  `json:"error,omitempty"`
	// CutForPrompt says the agent was given only the first rows of this step.
	CutForPrompt string `json:"cutForPrompt,omitempty"`
}

// TaskResult is the task an ensure_task step found or created.
type TaskResult struct {
	ID      interface{} `json:"id"`
	Created bool        `json:"created"`
}

// Options are what a run takes from the command line rather than from the file.
type Options struct {
	// Agent overrides the agent of the routine and of the file for this run; nil keeps theirs.
	Agent interface{}
	// Now is the clock; nil is the wall clock.
	Now func() time.Time
	// RunAgent starts an agent; nil starts it as a process.
	RunAgent AgentRunner
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Run runs one routine for slot and keeps what it found in <output>/<routine>/<day>/. A step Odoo
// refuses is kept as an error and the next step still runs; an error returned here is about the
// folder the run is kept in.
func (f *File) Run(ctx context.Context, erp ERP, routine *Routine, slot time.Time, options Options) (*Result, error) {
	if f.Organization != "" && f.Profile == "" {
		return nil, invalid("this file runs on the governed asao CLI (organization = %q); odoo-cli runs routines files that name a profile", f.Organization)
	}
	started := time.Now()
	day := dayOf(slot)
	result := &Result{
		Routine: routine.ID, Title: routine.Title, Area: routine.Area, Every: routine.Every,
		Slot: slot.Format(slotLayout), RanAt: options.now().Format("2006-01-02T15:04:05"),
		Source: "odoo-cli profile " + f.Profile,
		Steps:  []StepResult{},
	}
	meanings := map[string][]Meaning{}
	for index := range routine.Steps {
		result.Steps = append(result.Steps, runStep(ctx, erp, routine, &routine.Steps[index], day, meanings))
	}
	for _, step := range result.Steps {
		if step.Status == "ERROR" {
			result.Errors++
		}
	}
	result.Seconds = math.Round(time.Since(started).Seconds()*10) / 10
	result.Retry = unreached(result)

	folder := filepath.Join(f.Output, routine.ID, day.Format(dayLayout))
	result.Folder = folder
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nil, fmt.Errorf("routines: keeping the run in %s: %w", folder, err)
	}
	if err := writeJSON(filepath.Join(folder, "result.json"), result); err != nil {
		return nil, err
	}
	if err := writeText(filepath.Join(folder, "summary.md"), Summary(result, f.Language)); err != nil {
		return nil, err
	}
	agent := options.Agent
	if agent == nil {
		agent = routine.Agent
	}
	if agent == nil {
		agent = f.Agent
	}
	if result.Retry {
		// Nothing reached Odoo: there is nothing for an agent to read.
		agent = "none"
	}
	result.Agent = f.analyse(ctx, agent, Prompt(routine, result, meanings, f.Language), result, folder, options.RunAgent)
	if err := writeJSON(filepath.Join(folder, "result.json"), result); err != nil {
		return nil, err
	}
	if !result.Retry {
		if err := f.remember(routine, result); err != nil {
			return nil, err
		}
	}
	return result, f.WriteGuides()
}

// unreached says every step failed because Odoo could not be reached: a laptop that wakes before its
// network does should not lose the morning's run.
func unreached(result *Result) bool {
	for _, step := range result.Steps {
		if step.Status != "ERROR" || step.Error == nil || !step.Error.Retryable {
			return false
		}
	}
	return len(result.Steps) > 0
}

func runStep(ctx context.Context, erp ERP, routine *Routine, step *Step, day time.Time, meanings map[string][]Meaning) StepResult {
	done := StepResult{Name: step.Name, Kind: step.Kind, Asked: step.Note}
	if step.Operation != "" {
		operation := step.Operation
		done.Operation = &operation
	}
	var domain []interface{}
	if len(step.Filter) > 0 {
		domain, _ = Render(step.Filter, day).([]interface{})
		done.Filter = domain
	}
	if (step.Kind == "read" || step.Kind == "group") && meanings[step.Operation] == nil {
		meanings[step.Operation] = erp.Describe(step.Operation)
	}
	var err error
	switch step.Kind {
	case "read":
		var rows []map[string]interface{}
		if rows, err = erp.Read(ctx, step.Operation, domain, step.Limit, step.Order, step.ID); err == nil {
			count, complete := len(rows), len(rows) < step.Limit
			done.Rows, done.Count, done.Complete = &rows, &count, &complete
		}
	case "group":
		var groups []map[string]interface{}
		if groups, err = erp.Group(ctx, step.Operation, step.By, step.Aggregate, domain, step.Limit, step.Order); err == nil {
			count := len(groups)
			done.Groups, done.Count, done.Totals, done.By = &groups, &count, Totals(groups, step.Aggregate), step.By
		}
	case "review":
		from, to := Period(step.Period, day)
		var report map[string]interface{}
		if report, err = erp.Review(ctx, from, to, day); err == nil {
			findings, _ := asList(report["findings"])
			count := len(findings)
			done.Period = map[string]string{"from": from.Format(dayLayout), "to": to.Format(dayLayout)}
			done.Report, done.Count = report, &count
		}
	case "ensure_task", "execute":
		if !routine.Writes {
			// FromMap refuses such a file; this keeps a hand-built Routine to the same rule.
			err = &Refusal{Code: "ROUTINE_READS_ONLY", Message: routine.ID + " does not say writes = true"}
			break
		}
		values, _ := Render(step.Values, day).(map[string]interface{})
		if step.Kind == "ensure_task" {
			done.Task, err = ensureTask(ctx, erp, values)
		} else {
			done.Data, err = erp.Execute(ctx, step.Operation, step.ID, values)
		}
	}
	done.Status = "OK"
	if err != nil {
		done.Status = "ERROR"
		var refused *Refusal
		if !errors.As(err, &refused) {
			refused = &Refusal{Code: "STEP_FAILED", Message: err.Error()}
		}
		done.Error = refused
	}
	return done
}

// openTaskStates are the states of a task that is not closed.
var openTaskStates = []interface{}{"01_in_progress", "02_changes_requested", "03_approved", "04_waiting_normal"}

// ensureTask finds an open task with this name in this project and creates one only when there is
// none: a recurring one stays open, and Odoo opens its next occurrence when one is closed.
func ensureTask(ctx context.Context, erp ERP, values map[string]interface{}) (*TaskResult, error) {
	project := values["project"]
	projectTerm := []interface{}{"project_id.name", "=", project}
	if id, isInt := integer(project); isInt {
		projectTerm = []interface{}{"project_id", "=", id}
	}
	domain := []interface{}{[]interface{}{"name", "=", values["name"]}, []interface{}{"state", "in", openTaskStates}, projectTerm}
	existing, err := erp.Read(ctx, "work.task.list", domain, 1, "", nil)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return &TaskResult{ID: existing[0]["id"], Created: false}, nil
	}
	created, err := erp.Execute(ctx, "work.task.create", nil, values)
	if err != nil {
		return nil, err
	}
	record, _ := created.(map[string]interface{})
	return &TaskResult{ID: record["id"], Created: true}, nil
}

// Totals are a group step's count and each summed aggregate. A sum over several currencies is given
// per currency, never added up into one figure that means nothing.
func Totals(groups []map[string]interface{}, aggregate []string) map[string]interface{} {
	count := int64(0)
	currencies := map[string]bool{}
	for _, group := range groups {
		if n, ok := integer(group["count"]); ok {
			count += n
		}
		if currency, has := group["currency_id"]; has {
			currencies[Label(currency)] = true
		}
	}
	totals := map[string]interface{}{"count": count}
	for _, spec := range aggregate {
		if !strings.HasSuffix(spec, ":sum") {
			continue
		}
		if len(currencies) > 1 {
			per := map[string]*sum{}
			for _, group := range groups {
				if value, ok := group[spec]; ok && isNumber(value) {
					key := Label(group["currency_id"])
					if per[key] == nil {
						per[key] = &sum{}
					}
					per[key].add(value)
				}
			}
			out := map[string]interface{}{}
			for key, total := range per {
				out[key] = total.value()
			}
			totals[spec] = out
			continue
		}
		total, seen := &sum{}, false
		for _, group := range groups {
			if value, ok := group[spec]; ok && isNumber(value) {
				total.add(value)
				seen = true
			}
		}
		if seen {
			totals[spec] = total.value()
		}
	}
	return totals
}

// sum adds numbers the way the run shows them: whole while every term is whole, else to the cent.
type sum struct {
	whole    int64
	fraction float64
	floating bool
}

func (s *sum) add(value interface{}) {
	if isWhole(value) {
		n, _ := integer(value)
		s.whole += n
		return
	}
	f, _ := number(value)
	s.fraction += f
	s.floating = true
}

func (s *sum) value() interface{} {
	if !s.floating {
		return s.whole
	}
	return math.Round((float64(s.whole)+s.fraction)*100) / 100
}

func isNumber(value interface{}) bool {
	if _, isBool := value.(bool); isBool {
		return false
	}
	_, ok := number(value)
	return ok
}

// ---- what a run leaves ------------------------------------------------------------------------

// Latest is a routine's latest run, kept as <output>/<routine>/latest.json.
type Latest struct {
	Slot   string `json:"slot"`
	RanAt  string `json:"ranAt"`
	Folder string `json:"folder"`
	Errors int    `json:"errors"`
	Agent  string `json:"agent,omitempty"`
}

func (f *File) latestFile(routine *Routine) string {
	return filepath.Join(f.Output, routine.ID, "latest.json")
}

// LatestRun reads a routine's latest run; ok is false when it has not run yet.
func (f *File) LatestRun(routine *Routine) (Latest, bool) {
	data, err := os.ReadFile(f.latestFile(routine))
	if err != nil {
		return Latest{}, false
	}
	var latest Latest
	if json.Unmarshal(data, &latest) != nil || latest.Slot == "" {
		return Latest{}, false
	}
	return latest, true
}

func (f *File) remember(routine *Routine, result *Result) error {
	latest := Latest{Slot: result.Slot, RanAt: result.RanAt, Folder: result.Folder, Errors: result.Errors}
	if result.Agent != nil {
		latest.Agent = result.Agent.Status
	}
	return writeJSON(f.latestFile(routine), latest)
}

// Due are the routines whose latest slot has come, has not run yet, and is not too late to run.
func (f *File) Due(now time.Time) []Scheduled {
	var due []Scheduled
	for index := range f.Routines {
		routine := &f.Routines[index]
		if !routine.Enabled {
			continue
		}
		slot := routine.Slot(now)
		if now.Sub(slot) > Grace[routine.Every] {
			continue
		}
		if latest, ok := f.LatestRun(routine); ok && latest.Slot == slot.Format(slotLayout) {
			continue
		}
		due = append(due, Scheduled{Routine: routine, Slot: slot})
	}
	return due
}

// Scheduled is a routine and the slot it runs for.
type Scheduled struct {
	Routine *Routine
	Slot    time.Time
}

// DueRun is what running one due routine came to.
type DueRun struct {
	Routine string  `json:"routine"`
	Slot    string  `json:"slot"`
	Result  *Result `json:"result,omitempty"`
	Failed  string  `json:"failed,omitempty"`
}

// ErrDueRunning refuses a second "due" while one is still running over the same output folder.
var ErrDueRunning = errors.New("routines: another run of the due routines is still going in this output folder")

// dueLockAge is how old a lock may get before it is taken as left by a run that died.
const dueLockAge = 6 * time.Hour

// RunDue runs every routine that is due, in file order. A routine that fails does not stop the others.
// The routines of one output folder run one "due" at a time: a scheduler that fires again while an
// agent is still reading the last run finds them taken.
func (f *File) RunDue(ctx context.Context, erpFor func(*Routine) ERP, options Options) ([]DueRun, error) {
	if err := os.MkdirAll(f.Output, 0o700); err != nil {
		return nil, fmt.Errorf("routines: %w", err)
	}
	lock := filepath.Join(f.Output, ".due.lock")
	release, err := takeLock(lock, options.now())
	if err != nil {
		return nil, err
	}
	defer release()
	var runs []DueRun
	for _, scheduled := range f.Due(options.now()) {
		run := DueRun{Routine: scheduled.Routine.ID, Slot: scheduled.Slot.Format(slotLayout)}
		result, err := f.Run(ctx, erpFor(scheduled.Routine), scheduled.Routine, scheduled.Slot, options)
		if err != nil {
			run.Failed = err.Error()
		} else {
			run.Result = result
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func takeLock(path string, now time.Time) (func(), error) {
	for attempt := 0; attempt < 2; attempt++ {
		handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(handle, "%d %s\n", os.Getpid(), now.Format(time.RFC3339))
			_ = handle.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("routines: taking %s: %w", path, err)
		}
		info, statErr := os.Stat(path)
		if statErr != nil || now.Sub(info.ModTime()) < dueLockAge {
			return nil, ErrDueRunning
		}
		_ = os.Remove(path)
	}
	return nil, ErrDueRunning
}

// Index is each routine's latest run, as the index.md table shows it.
type Index struct {
	Routine   *Routine
	Latest    Latest
	HasLatest bool
}

// Indexes reads each routine's latest run.
func (f *File) Indexes() []Index {
	out := make([]Index, 0, len(f.Routines))
	for index := range f.Routines {
		latest, ok := f.LatestRun(&f.Routines[index])
		out = append(out, Index{Routine: &f.Routines[index], Latest: latest, HasLatest: ok})
	}
	return out
}

// IndexMarkdown is index.md: every routine, its schedule and its latest run.
func (f *File) IndexMarkdown() string {
	vi := f.Language != "en"
	lines := []string{"# " + pick(vi, "Việc định kỳ", "Routines"), "",
		pick(vi, "| Việc | Lĩnh vực | Lịch | Lần chạy gần nhất | Lỗi | Phân tích |", "| Routine | Area | Schedule | Latest run | Errors | Analysis |"),
		"|---|---|---|---|---|---|"}
	for _, entry := range f.Indexes() {
		run, errorsText, agent := "-", "-", "-"
		if entry.HasLatest {
			errorsText = fmt.Sprint(entry.Latest.Errors)
			if entry.Latest.Agent != "" {
				agent = entry.Latest.Agent
			}
			if relative, err := filepath.Rel(f.Output, entry.Latest.Folder); err == nil && entry.Latest.Folder != "" {
				run = fmt.Sprintf("[%s](%s/summary.md)", entry.Latest.Slot, filepath.ToSlash(relative))
			}
		}
		lines = append(lines, fmt.Sprintf("| %s (`%s`) | %s | %s | %s | %s | %s |", entry.Routine.Title, entry.Routine.ID,
			entry.Routine.Area, entry.Routine.Schedule(f.Language), run, errorsText, agent))
	}
	return strings.Join(lines, "\n") + "\n"
}

// WriteGuides writes index.md and the guide an interactive agent reads first (AGENTS.md, CLAUDE.md,
// GEMINI.md) into the output folder.
func (f *File) WriteGuides() error {
	if err := os.MkdirAll(f.Output, 0o700); err != nil {
		return fmt.Errorf("routines: %w", err)
	}
	if err := writeText(filepath.Join(f.Output, "index.md"), f.IndexMarkdown()); err != nil {
		return err
	}
	source := f.Source
	if source == "" {
		source = "routines.toml"
	}
	guide := strings.NewReplacer("{source}", source, "{output}", f.Output).Replace(pick(f.Language != "en", guideVI, guideEN))
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if err := writeText(filepath.Join(f.Output, name), guide); err != nil {
			return err
		}
	}
	return nil
}

// ---- writing files ----------------------------------------------------------------------------

// encodeJSON writes a value as the run keeps it: indented by one space, non-ASCII as is.
func encodeJSON(value interface{}, indent bool) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", " ")
	}
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeJSON(path string, value interface{}) error {
	data, err := encodeJSON(value, true)
	if err != nil {
		return fmt.Errorf("routines: writing %s: %w", path, err)
	}
	return writeFile(path, data)
}

func writeText(path, content string) error {
	return writeFile(path, []byte(content))
}

// writeFile replaces a file whole: written beside it, then renamed over it, so a full disk or a
// crash leaves the old file rather than half of the new one.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("routines: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("routines: writing %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("routines: writing %s: %w", path, err)
	}
	return nil
}

// sortedKeys returns a map's keys in order.
func sortedKeys(table map[string]interface{}) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

const guideVI = `# Việc định kỳ của doanh nghiệp (đọc trước khi trả lời)

Thư mục này chứa kết quả các việc định kỳ chạy trên Odoo qua odoo-cli.
Danh sách việc và lịch nằm trong ` + "`{source}`" + `.

- ` + "`index.md`" + `: mỗi việc và lần chạy gần nhất.
- ` + "`<việc>/<ngày>/result.json`" + `: đúng những gì Odoo trả về; ` + "`summary.md`" + `: số đếm và tổng (máy tính, không phải AI viết);
  ` + "`analysis.md`" + `: phân tích của agent ở lần chạy đó.

Khi được hỏi về tình hình hôm nay, tuần này hay tháng này:
1. Đọc ` + "`index.md`" + `, rồi ` + "`summary.md`" + ` và ` + "`result.json`" + ` của các lần chạy liên quan. Chỉ dựa trên các file đó.
2. Cần số mới hơn thì chạy lại: ` + "`odoo-cli routines run --routine <việc> --file {source}`" + `.
3. Không tự sửa dữ liệu Odoo. Một việc cần ghi vào Odoo phải được viết thành bước trong ` + "`{source}`" + ` (writes = true) và người duyệt.

Thư mục: {output}
`

const guideEN = `# The business's routines (read before answering)

This folder holds what the business's routines found in Odoo, through odoo-cli.
The routines and their schedules are in ` + "`{source}`" + `.

- ` + "`index.md`" + `: every routine and its latest run.
- ` + "`<routine>/<day>/result.json`" + `: exactly what Odoo answered; ` + "`summary.md`" + `: counts and totals (computed, not written
  by a model); ` + "`analysis.md`" + `: the agent's reading of that run.

When asked how things stand today, this week or this month:
1. Read ` + "`index.md`" + `, then the ` + "`summary.md`" + ` and ` + "`result.json`" + ` of the runs that matter. Rely on those files only.
2. For newer figures, run again: ` + "`odoo-cli routines run --routine <routine> --file {source}`" + `.
3. Never change Odoo yourself. A change belongs in ` + "`{source}`" + ` as a step (writes = true), for a person to approve.

Folder: {output}
`
