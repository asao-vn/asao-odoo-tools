package standalone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/odoo/review"
	"github.com/asao-platform/atoc/packages/odoo/routines"
)

// The routines group: the checks a business makes on its Odoo every day, week or month
// (packages/odoo/routines), run by the command line itself so they need no Python. A routines file
// says what each routine asks; run and due ask it through the catalog's operations, keep what Odoo
// answered in an output folder and hand it to the agent the owner chose to read it.

// routinesFileVariable names the routines file when --file is not given.
const routinesFileVariable = "ASAO_ROUTINES"

func routinesGroup(config programConfig) *clicore.Command {
	fileFlag := clicore.Flag{Name: "file", Kind: clicore.KindString, Default: "routines.toml",
		Description: "The routines file: TOML, YAML (.yaml) or JSON (.json). Default: $" + routinesFileVariable + ", else ./routines.toml."}
	personal := func(flag clicore.Flag) clicore.Flag {
		flag.PersonOnly = true
		return flag
	}
	agentFlag := clicore.Flag{Name: "agent", Kind: clicore.KindString,
		Description: `The agent that reads this run instead of the file's: claude, codex, gemini, agy, none, a command, or a command line as a JSON list with {prompt}.`}
	templates := local(&clicore.Command{
		Name:        "templates",
		Summary:     "List the starter routines: what each asks Odoo, what Odoo returns, and when it runs.",
		Description: `Pure local listing: no network, no credential. "routines init" writes the chosen ones into a routines file.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags: []clicore.Flag{
			{Name: "area", Kind: clicore.KindString, Enum: sortedAreas(), Description: "Only the routines of this area."},
			{Name: "every", Kind: clicore.KindString, Enum: routines.Cadences, Description: "Only the routines of this cadence."},
			{Name: "language", Kind: clicore.KindString, Default: "vi", Enum: []string{"vi", "en"}, Description: "The language of titles and descriptions."},
		},
		Examples: []string{config.name + " routines templates --output json", config.name + " routines templates --area finance --language en"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return listTemplates(invocation), nil
		},
	})
	initCommand := local(&clicore.Command{
		Name:    "init",
		Summary: "Write a routines file from the starter routines, for a profile.",
		Description: `Writes the chosen starter routines (all the ones that only read, by default) into a routines file,
where they are yours to change. It refuses to replace a file that exists unless --force.`,
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Flags: []clicore.Flag{fileFlag,
			{Name: "profile", Kind: clicore.KindString, Required: true, Description: "The profile the routines read."},
			{Name: "template", Kind: clicore.KindString, Repeatable: true, Description: "A starter routine to include (repeatable). Default: every one that only reads."},
			{Name: "agent", Kind: clicore.KindString, Default: "none", Description: `The agent that reads each run: claude, codex, gemini, agy, none, a command, or a command line as a JSON list with {prompt}.`},
			{Name: "output-folder", Kind: clicore.KindString, Default: routines.DefaultOutput, Description: "The folder every run is kept in (the file's output)."},
			{Name: "language", Kind: clicore.KindString, Default: "vi", Enum: []string{"vi", "en"}, Description: "The language of titles, prompts and summaries."},
			{Name: "force", Kind: clicore.KindBool, Description: "Replace the file if it exists."},
		},
		Examples: []string{config.name + " routines init --profile cong-ty --agent claude",
			config.name + " routines init --profile cong-ty --template cong-no-qua-han --template ra-soat-tuan --file ~/routines.toml"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return initRoutines(invocation, config)
		},
	})
	list := local(&clicore.Command{
		Name:        "list",
		Summary:     "List the routines of a file: what each asks, when it runs next, and whether it writes.",
		Description: `Reads the routines file and checks it, as run does, without contacting Odoo.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{personal(fileFlag)},
		Examples:    []string{config.name + " routines list --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			file, err := loadRoutinesFile(invocation)
			if err != nil {
				return nil, err
			}
			return describeRoutines(file, time.Now()), nil
		},
	})
	run := local(&clicore.Command{
		Name:    "run",
		Summary: "Run one routine now, keep what Odoo answered, and have the agent read it.",
		Description: `Asks each step of the routine through the catalog's operations, against the profile the file
names, with the same readiness rule and refusals a typed command has. A read or group step runs read
operations only; a routine changes Odoo only through its ensure_task and execute steps, and only when it
says writes = true. The run is kept in <output>/<routine>/<day>/ (result.json, summary.md, and the agent's
prompt.md and analysis.md); a step Odoo refuses is kept as an error and the next step still runs.`,
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Flags: []clicore.Flag{fileFlag,
			{Name: "routine", Kind: clicore.KindString, Required: true, Description: "The routine's id."},
			agentFlag,
			{Name: "day", Kind: clicore.KindString, Description: "Run for an earlier day, YYYY-MM-DD, as if its slot were now."},
		},
		Examples: []string{config.name + " routines run --routine cong-no-qua-han", config.name + " routines run --routine ra-soat-thang --agent none --day 2026-10-01"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runRoutine(ctx, invocation, config)
		},
	})
	due := local(&clicore.Command{
		Name:    "due",
		Summary: "Run every routine that is due: what the timer calls.",
		Description: `Runs, in file order, each routine whose latest slot has come and has not run yet, unless it is
too late (20 hours for a daily routine, 3 days for a weekly one, 7 days for a monthly one). A routine that
fails does not stop the others. When no step reached Odoo the slot stays open and the next call runs it
again. One "due" runs at a time per output folder.`,
		SideEffect: clicore.EffectWriteProvider,
		Authority:  destructiveLocalAuthority,
		Flags:      []clicore.Flag{fileFlag, agentFlag},
		Examples:   []string{config.name + " routines due --file ~/routines.toml"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runDueRoutines(ctx, invocation, config)
		},
	})
	status := local(&clicore.Command{
		Name:        "status",
		Summary:     "Show each routine's latest run: when, how many errors, and whether the agent read it.",
		Description: `Reads the output folder's latest.json files; contacts nothing and writes nothing.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{personal(fileFlag)},
		Examples:    []string{config.name + " routines status --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			file, err := loadRoutinesFile(invocation)
			if err != nil {
				return nil, err
			}
			return routinesStatus(file), nil
		},
	})
	return &clicore.Command{
		Name:    "routines",
		Summary: "The checks a business makes on its Odoo every day, week or month, and the agent that reads them.",
		Description: `A routines file (TOML, YAML or JSON) lists what the business asks its Odoo on a schedule: sales
yesterday, invoices overdue, transfers late, a books review of last month. "run" asks one now and "due"
asks every one whose time has come; each run is kept in an output folder and handed to the agent the
owner chose (claude, codex, gemini, agy, any command, or none), which only reads. "install" puts "due"
on the computer's timer (systemd, launchd, Task Scheduler or cron).

Start with "routines templates", then "routines init --profile <profile>".`,
		Children: []*clicore.Command{templates, initCommand, list, run, due, status,
			routinesInstallCommand(config, fileFlag), routinesUninstallCommand(config)},
	}
}

func sortedAreas() []string {
	areas := make([]string, 0, len(routines.Areas))
	for area := range routines.Areas {
		areas = append(areas, area)
	}
	sort.Strings(areas)
	return areas
}

// ---- reading a routines file ------------------------------------------------------------------

func routinesPath(invocation *clicore.Invocation) string {
	if invocation.Has("file") {
		return routines.ExpandHome(strings.TrimSpace(invocation.String("file")))
	}
	if path := strings.TrimSpace(os.Getenv(routinesFileVariable)); path != "" {
		return routines.ExpandHome(path)
	}
	return "routines.toml"
}

func loadRoutinesFile(invocation *clicore.Invocation) (*routines.File, error) {
	path := routinesPath(invocation)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "ROUTINES_FILE_UNREADABLE", fmt.Sprintf("cannot read %s", path)).
			WithContextValue("file", path).
			WithRemediation("Write one with \"routines init --profile <profile>\", or name it with --file or $" + routinesFileVariable + ".")
	}
	decoded, err := decodeRoutines(path, data)
	if err != nil {
		return nil, clicore.UsageError("ROUTINES_FILE_INVALID", "%s is not a routines file: %v", path, err).WithContextValue("file", path)
	}
	file, err := routines.FromMap(decoded)
	if err != nil {
		return nil, clicore.UsageError("ROUTINES_FILE_INVALID", "%s: %v", path, err).WithContextValue("file", path)
	}
	if absolute, err := filepath.Abs(path); err == nil {
		file.Source = absolute
	}
	return file, nil
}

// decodeRoutines reads a routines file by its extension: JSON, YAML, or TOML otherwise.
func decodeRoutines(path string, data []byte) (map[string]interface{}, error) {
	decoded := map[string]interface{}{}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &decoded); err != nil {
			return nil, err
		}
	default:
		if _, err := toml.Decode(string(data), &decoded); err != nil {
			return nil, err
		}
	}
	return decoded, nil
}

// parseAgent reads an --agent value: a name or command, or a command line as a JSON list.
func parseAgent(value string) (interface{}, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") {
		var command []string
		if err := json.Unmarshal([]byte(value), &command); err != nil || len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return nil, clicore.UsageError("INVALID_FLAG_VALUE", `--agent as a command line is a JSON list of texts, ["cmd", "arg", "{prompt}"]`).WithContextValue("flag", "agent")
		}
		return command, nil
	}
	if strings.ContainsAny(value, " \t") {
		return nil, clicore.UsageError("INVALID_FLAG_VALUE", `--agent %q holds spaces: give a command line as a JSON list, ["cmd", "arg", "{prompt}"]`, value).WithContextValue("flag", "agent")
	}
	return value, nil
}

// ---- the commands -----------------------------------------------------------------------------

func listTemplates(invocation *clicore.Invocation) map[string]interface{} {
	language := invocation.String("language")
	if language == "" {
		language = "vi"
	}
	var out []map[string]interface{}
	for _, template := range routines.Templates() {
		if area := invocation.String("area"); area != "" && template.Area != area {
			continue
		}
		if every := invocation.String("every"); every != "" && template.Every != every {
			continue
		}
		routine := routines.Routine{Every: template.Every, At: template.At, On: template.On}
		if day, ok := template.On.(json.Number); ok {
			if n, err := day.Int64(); err == nil {
				routine.On = int(n)
			}
		}
		operations := map[string]bool{}
		for _, step := range template.Steps {
			for _, kind := range []string{"read", "group", "execute"} {
				if operation, ok := step[kind].(string); ok {
					operations[operation] = true
				}
			}
			if _, ok := step["ensure_task"]; ok {
				operations["work.task.list"], operations["work.task.create"] = true, true
			}
			if _, ok := step["review"]; ok {
				operations["review run"] = true
			}
		}
		names := make([]string, 0, len(operations))
		for operation := range operations {
			names = append(names, operation)
		}
		sort.Strings(names)
		out = append(out, map[string]interface{}{
			"id": template.ID, "area": template.Area, "areaName": routines.Areas[template.Area][language],
			"every": template.Every, "schedule": routine.Schedule(language), "writes": template.Writes,
			"title": template.Title[language], "does": template.Does[language], "returns": template.Returns[language],
			"prompt": template.Prompt[language], "operations": names,
		})
	}
	return map[string]interface{}{"templates": out, "count": len(out)}
}

func initRoutines(invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	path := routinesPath(invocation)
	force, err := invocation.Bool("force")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil && !force {
		return nil, clicore.NewError(clicore.CategoryConflict, "ROUTINES_FILE_EXISTS", path+" exists").
			WithContextValue("file", path).WithRemediation("Pass --force to replace it, or name another file with --file.")
	}
	language := invocation.String("language")
	if language == "" {
		language = "vi"
	}
	ids := invocation.StringSlice("template")
	if len(ids) == 0 {
		for _, template := range routines.Templates() {
			if !template.Writes {
				ids = append(ids, template.ID)
			}
		}
	}
	var chosen []interface{}
	for _, id := range ids {
		template, err := routines.TemplateByID(strings.TrimSpace(id))
		if err != nil {
			return nil, clicore.UsageError("UNKNOWN_TEMPLATE", "%v", err).WithRemediation("Run \"routines templates\" for the list.")
		}
		chosen = append(chosen, template.AsRoutine(language))
	}
	agent, err := parseAgent(invocation.String("agent"))
	if err != nil {
		return nil, err
	}
	output := invocation.String("output-folder")
	if output == "" {
		output = routines.DefaultOutput
	}
	data := map[string]interface{}{"profile": strings.TrimSpace(invocation.String("profile")), "output": output,
		"agent": agent, "language": language, "routine": chosen}
	// A file that would not load is never written.
	file, err := routines.FromMap(data)
	if err != nil {
		return nil, clicore.UsageError("ROUTINES_FILE_INVALID", "%v", err)
	}
	var content []byte
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", " ")
		err = encoder.Encode(data)
		content = buffer.Bytes()
	case ".yaml", ".yml":
		return nil, clicore.UsageError("INVALID_FLAG_VALUE", "init writes a .toml or a .json file; a YAML file can be written by hand").WithContextValue("flag", "file")
	default:
		var written string
		written, err = routines.TOML(data, map[string]string{"vi": "Việc định kỳ trên Odoo: " + config.name + " routines --help",
			"en": "Routines on Odoo: " + config.name + " routines --help"}[language])
		content = []byte(written)
	}
	if err != nil {
		return nil, clicore.InternalError(err)
	}
	if err := writeLocalFile(path, content); err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "ROUTINES_FILE_NOT_WRITTEN", err.Error()).WithContextValue("file", path)
	}
	return map[string]interface{}{"file": path, "routines": len(file.Routines), "output": file.Output, "profile": file.Profile,
		"next": "Check it with \"" + config.name + " routines list\", run one with \"" + config.name + " routines run --routine <id>\", then put it on the timer with \"" + config.name + " routines install\"."}, nil
}

// writeLocalFile replaces a file whole: written beside it, then renamed over it.
func writeLocalFile(path string, content []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func describeRoutines(file *routines.File, now time.Time) map[string]interface{} {
	var out []map[string]interface{}
	for index := range file.Routines {
		routine := &file.Routines[index]
		var steps []map[string]interface{}
		for _, step := range routine.Steps {
			steps = append(steps, map[string]interface{}{"name": step.Name, "kind": step.Kind, "operation": step.Operation, "note": step.Note})
		}
		entry := map[string]interface{}{"id": routine.ID, "title": routine.Title, "area": routine.Area, "every": routine.Every,
			"at": routine.At, "schedule": routine.Schedule(file.Language), "enabled": routine.Enabled, "writes": routine.Writes, "steps": steps}
		if routine.On != nil {
			entry["on"] = routine.On
		}
		if routine.Enabled {
			entry["next"] = routine.Next(now).Format("2006-01-02T15:04")
		}
		if routine.Agent != nil {
			entry["agent"] = routine.Agent
		}
		out = append(out, entry)
	}
	return map[string]interface{}{"file": file.Source, "profile": file.Profile, "output": file.Output, "agent": file.Agent,
		"language": file.Language, "routines": out, "count": len(out)}
}

func routinesStatus(file *routines.File) map[string]interface{} {
	var out []map[string]interface{}
	for _, entry := range file.Indexes() {
		item := map[string]interface{}{"id": entry.Routine.ID, "title": entry.Routine.Title, "schedule": entry.Routine.Schedule(file.Language)}
		if entry.HasLatest {
			item["latest"] = entry.Latest
		}
		out = append(out, item)
	}
	return map[string]interface{}{"output": file.Output, "routines": out, "index": file.IndexMarkdown()}
}

// routinesOdoo builds the executor of the profile a routines file names.
func routinesOdoo(ctx context.Context, file *routines.File, config programConfig) (*direct.Executor, error) {
	if strings.TrimSpace(file.Profile) == "" {
		return nil, clicore.UsageError("ROUTINES_FILE_GOVERNED", "this file names organization %q: it runs on the governed asao CLI, and odoo-cli runs routines files that name a profile", file.Organization).
			WithRemediation("Run it with the asao-erp SDK (python -m asao_erp routines), or write profile = \"...\" in the file.")
	}
	store, err := localprofile.Load()
	if err != nil {
		return nil, classifyProfile(err, file.Profile, config.name)
	}
	selection, err := store.Resolve(file.Profile)
	if err != nil {
		return nil, classifyProfile(err, file.Profile, config.name)
	}
	return executorFor(ctx, selection, config)
}

func runRoutine(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	file, err := loadRoutinesFile(invocation)
	if err != nil {
		return nil, err
	}
	routine, err := file.Get(strings.TrimSpace(invocation.String("routine")))
	if err != nil {
		return nil, clicore.NewError(clicore.CategoryNotFound, "ROUTINE_NOT_FOUND", err.Error()).WithRemediation("Run \"routines list\" for the routines of the file.")
	}
	slot := routine.Slot(time.Now())
	if value := strings.TrimSpace(invocation.String("day")); value != "" {
		day, err := time.ParseInLocation("2006-01-02", value, time.Local)
		if err != nil {
			return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--day must be a day, YYYY-MM-DD").WithContextValue("flag", "day")
		}
		var hour, minute int
		_, _ = fmt.Sscanf(routine.At, "%d:%d", &hour, &minute)
		slot = time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, time.Local)
	}
	var options routines.Options
	if invocation.Has("agent") {
		if options.Agent, err = parseAgent(invocation.String("agent")); err != nil {
			return nil, err
		}
	}
	executor, err := routinesOdoo(ctx, file, config)
	if err != nil {
		return nil, err
	}
	result, err := file.Run(ctx, &routineOdoo{executor: executor, allowUnverified: file.AllowUnverified, writes: routine.Writes, config: config},
		routine, slot, options)
	if err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "ROUTINE_NOT_KEPT", err.Error())
	}
	if result.Errors > 0 {
		return nil, clicore.NewError(clicore.CategoryProvider, "ROUTINE_STEPS_FAILED",
			fmt.Sprintf("%s: %d of %d steps failed; the run is kept in %s", routine.ID, result.Errors, len(result.Steps), result.Folder)).
			WithContext(map[string]interface{}{"result": result}).
			WithRemediation("Read each failed step's error in " + filepath.Join(result.Folder, "summary.md") + ".")
	}
	return result, nil
}

func runDueRoutines(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	file, err := loadRoutinesFile(invocation)
	if err != nil {
		return nil, err
	}
	var options routines.Options
	if invocation.Has("agent") {
		if options.Agent, err = parseAgent(invocation.String("agent")); err != nil {
			return nil, err
		}
	}
	if len(file.Due(time.Now())) == 0 {
		return map[string]interface{}{"ran": []routines.DueRun{}, "count": 0}, nil
	}
	// A profile or credential that cannot be used now leaves every slot open for the next call.
	executor, err := routinesOdoo(ctx, file, config)
	if err != nil {
		return nil, err
	}
	runs, err := file.RunDue(ctx, func(routine *routines.Routine) routines.ERP {
		return &routineOdoo{executor: executor, allowUnverified: file.AllowUnverified, writes: routine.Writes, config: config}
	}, options)
	if errors.Is(err, routines.ErrDueRunning) {
		return nil, clicore.NewError(clicore.CategoryConflict, "ROUTINES_ALREADY_RUNNING", err.Error()).WithRetryable(true).
			WithRemediation("Wait for it to finish; a lock older than 6 hours is taken as left by a run that died.")
	}
	if err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "ROUTINES_NOT_RUN", err.Error())
	}
	failed := 0
	for _, run := range runs {
		if run.Failed != "" || (run.Result != nil && run.Result.Errors > 0) {
			failed++
		}
	}
	out := map[string]interface{}{"ran": runs, "count": len(runs), "failed": failed}
	if failed > 0 {
		return nil, clicore.NewError(clicore.CategoryProvider, "ROUTINES_FAILED", fmt.Sprintf("%d of %d routines had failures; each run is kept in its folder", failed, len(runs))).
			WithContext(out)
	}
	return out, nil
}

// ---- asking Odoo ------------------------------------------------------------------------------

// routineOdoo answers a routine through the catalog's operations on the profile's Odoo.
type routineOdoo struct {
	executor        *direct.Executor
	allowUnverified bool
	writes          bool
	config          programConfig
}

func refusalOf(err error) error {
	var cliErr *clicore.Error
	if errors.As(err, &cliErr) {
		return &routines.Refusal{Code: cliErr.Code, Message: cliErr.Message, Remediation: cliErr.Remediation,
			Retryable: cliErr.Retryable || cliErr.Category == clicore.CategoryUnavailable}
	}
	return &routines.Refusal{Code: "STEP_FAILED", Message: err.Error()}
}

func (o *routineOdoo) operation(id string) (direct.Operation, error) {
	operation, err := direct.DescribeAnyOperation(id)
	if err != nil {
		return direct.Operation{}, refusalOf(o.config.surface.UnknownOperationError(id, err))
	}
	return operation, nil
}

// readOnly refuses a read or group step that names an operation which changes Odoo.
func (o *routineOdoo) readOnly(id string) error {
	operation, err := o.operation(id)
	if err != nil {
		return err
	}
	if operation.Kind != string(odoo.KindRead) && operation.Strategy != string(odoo.StrategyRead) {
		return &routines.Refusal{Code: "ROUTINE_STEP_WRITES", Message: id + " changes Odoo, and a read or group step only reads",
			Remediation: "Name a read operation, or make it an execute step of a routine that says writes = true."}
	}
	return nil
}

func (o *routineOdoo) records(ctx context.Context, request direct.Request) ([]map[string]interface{}, error) {
	request.AllowUnverified = o.allowUnverified
	outcome, err := o.executor.Execute(ctx, request)
	if err != nil {
		return nil, refusalOf(classifyExecution(err, request.Operation, o.config.surface))
	}
	data, err := routines.Normalize(outcome.Data)
	if err != nil {
		return nil, err
	}
	return routines.Records(data)
}

func (o *routineOdoo) Read(ctx context.Context, operation string, domain []interface{}, limit int, order string, id *int64) ([]map[string]interface{}, error) {
	if err := o.readOnly(operation); err != nil {
		return nil, err
	}
	request := direct.Request{Operation: operation, Domain: domain, Limit: limit, Order: order}
	if id != nil {
		request.ProviderTargetID = *id
	}
	return o.records(ctx, request)
}

func (o *routineOdoo) Group(ctx context.Context, operation string, by, aggregate []string, domain []interface{}, limit int, order string) ([]map[string]interface{}, error) {
	if err := o.readOnly(operation); err != nil {
		return nil, err
	}
	return o.records(ctx, direct.Request{Operation: operation, Domain: domain, Limit: limit, Order: order, GroupBy: by, Aggregates: aggregate})
}

func (o *routineOdoo) Review(ctx context.Context, from, to, asOf time.Time) (map[string]interface{}, error) {
	reader := &catalogReader{executor: o.executor, allowUnverified: o.allowUnverified, config: o.config}
	report, err := review.Run(ctx, reader, review.Options{From: from, To: to, AsOf: asOf})
	if err != nil {
		return nil, refusalOf(err)
	}
	data, err := routines.Normalize(report)
	if err != nil {
		return nil, err
	}
	out, _ := data.(map[string]interface{})
	return out, nil
}

func (o *routineOdoo) Execute(ctx context.Context, id string, target *int64, values map[string]interface{}) (interface{}, error) {
	if !o.writes {
		return nil, &routines.Refusal{Code: "ROUTINE_READS_ONLY", Message: "this routine does not say writes = true"}
	}
	operation, err := o.operation(id)
	if err != nil {
		return nil, err
	}
	if operation.Strategy == string(odoo.StrategyModuleInstall) {
		return nil, &routines.Refusal{Code: "AERP_OPERATION_REFUSED", Message: id + " installs a module, which changes what the Odoo is",
			Remediation: "Install modules with \"spec apply\"."}
	}
	request := direct.Request{Operation: id, Values: values, AllowUnverified: o.allowUnverified}
	if target != nil {
		request.ProviderTargetID = *target
	}
	outcome, err := o.executor.Execute(ctx, request)
	if err != nil {
		return nil, refusalOf(classifyExecution(err, id, o.config.surface))
	}
	return routines.Normalize(outcome.Data)
}

func (o *routineOdoo) Describe(id string) []routines.Meaning {
	operation, err := direct.DescribeAnyOperation(id)
	if err != nil {
		return nil
	}
	out := make([]routines.Meaning, 0, len(operation.Outputs))
	for _, field := range operation.Outputs {
		out = append(out, routines.Meaning{Name: field.Name, ProviderField: field.ProviderField, Description: field.Description})
	}
	return out
}
