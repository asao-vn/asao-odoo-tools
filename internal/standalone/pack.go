package standalone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// The pack group (DEC-2026-0176): a custom module's own operations, declared as data in a pack
// (odoo-cli.pack/v1) and kept per profile. A person syncs or adds a pack; odoo-cli checks it against
// the live Odoo (its module, models, fields, object buttons, wizards and reports) and keeps the
// checked copy. At start, the selected profile's packs are registered (LoadPacks), so their operations
// become commands and MCP tools like the catalog's: unverified, standalone-only, and asking the person
// before every write an agent asks for.

const (
	// packStaticPath is where a module ships its pack: Odoo serves a module's static files without a
	// login, at /<module>/static/...
	packStaticPath = "static/odoo-cli/pack.json"
	packMaxBytes   = 1 << 20
)

// packReserved are this program's own top-level commands: a pack's module named like one would mount
// its operations under that command (TestPackReservedNamesCoverTheCommands keeps the list whole).
var packReserved = []string{"admin", "call", "catalog", "commands", "completion", "controls", "credential", "doctor",
	"execute", "explore", "help", "mcp", "pack", "plan", "profile", "provenance", "report", "review", "routines", "secret", "spec", "trust",
	"update", "upgrade", "version"}

// PackReservedNames are the module names a pack may not take: this program's own commands.
func PackReservedNames() []string { return append([]string(nil), packReserved...) }

// packWriteTypes say how a field type of Odoo is given on the command line; a type not here cannot be
// written by a pack (binary, one2many, many2many, json, properties, reference).
var packWriteTypes = map[string]string{
	"char": "string", "text": "string", "html": "string", "selection": "string", "date": "string", "datetime": "string",
	"integer": "integer", "float": "number", "monetary": "number", "boolean": "boolean", "many2one": "reference",
}

// storedPack is a checked pack as a profile keeps it.
type storedPack struct {
	Pack      odoo.Pack       `json:"pack"`
	Origin    odoo.PackOrigin `json:"origin"`
	Series    string          `json:"series"`
	CheckedAt string          `json:"checkedAt"`
	Warnings  []string        `json:"warnings,omitempty"`
}

func packDir(profile string) (string, error) {
	path, err := localprofile.DefaultPath()
	if err != nil {
		return "", err
	}
	if profile == "" || profile == "." || profile == ".." || strings.ContainsAny(profile, `/\`) {
		return "", fmt.Errorf("profile name %q cannot name a directory", profile)
	}
	return filepath.Join(filepath.Dir(path), "packs", profile), nil
}

func readStoredPacks(profile string) ([]storedPack, []error) {
	dir, err := packDir(profile)
	if err != nil {
		return nil, []error{err}
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	var packs []storedPack
	var problems []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		var stored storedPack
		if err := json.Unmarshal(data, &stored); err != nil {
			problems = append(problems, fmt.Errorf("%s: %v", entry.Name(), err))
			continue
		}
		packs = append(packs, stored)
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Pack.Module < packs[j].Pack.Module })
	return packs, problems
}

func writeStoredPack(profile string, stored storedPack) error {
	dir, err := packDir(profile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(stored, "", " ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+stored.Pack.Module+".json.tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, stored.Pack.Module+".json"))
}

func removeStoredPack(profile, module string) (bool, error) {
	dir, err := packDir(profile)
	if err != nil {
		return false, err
	}
	err = os.Remove(filepath.Join(dir, module+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// LoadPacks registers the selected profile's packs before the command tree is built, so their
// operations become commands and MCP tools. args are the program's arguments: the profile is the one
// --profile names, or the default one. A stored pack this build no longer accepts is skipped, with a
// line on diagnostics saying to sync it again; nothing else is printed.
func LoadPacks(args []string, diagnostics io.Writer) {
	store, err := localprofile.Load()
	if err != nil {
		return
	}
	selection, err := store.Resolve(profileArgument(args))
	if err != nil {
		return
	}
	packs, problems := readStoredPacks(selection.Profile.Name)
	for _, problem := range problems {
		fmt.Fprintf(diagnostics, "odoo-cli: a pack of profile %s is skipped: %v\n", selection.Profile.Name, problem)
	}
	for _, stored := range packs {
		if err := odoo.RegisterPack(stored.Pack, stored.Origin, packReserved); err != nil {
			fmt.Fprintf(diagnostics, "odoo-cli: pack %s of profile %s is skipped (run `odoo-cli pack sync --profile %s`): %v\n",
				stored.Pack.Module, selection.Profile.Name, selection.Profile.Name, err)
		}
	}
}

// profileArgument is the value of --profile in a program's arguments, or "" for the default profile.
func profileArgument(args []string) string {
	for index, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--profile" && index+1 < len(args) {
			return args[index+1]
		}
		if value, ok := strings.CutPrefix(arg, "--profile="); ok {
			return value
		}
	}
	return ""
}

func packGroup(config programConfig) *clicore.Command {
	personal := func(flag clicore.Flag) clicore.Flag {
		flag.PersonOnly = true
		return flag
	}
	moduleFlag := clicore.Flag{Name: "module", Kind: clicore.KindString, Required: true, Description: "The pack's Odoo module, e.g. x_salon."}
	sync := local(&clicore.Command{
		Name:    "sync",
		Summary: "Find the packs the installed custom modules ship, check them against this Odoo, and keep them for the profile.",
		Description: `For every installed module that is neither standard nor Enterprise, reads <module>/static/odoo-cli/pack.json
from the profile's own Odoo (static files need no login). Each pack found is checked against this Odoo: its
module is installed, its models and fields exist, every method it calls is an object button of the model's
views, its wizards are wizards and its reports exist. A pack that passes is kept for the profile; its
operations are commands and MCP tools from the next run of this program. sync prints every pack added,
changed or removed, and --dry-run prints them without keeping anything. Packs added from a file stay.`,
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags: []clicore.Flag{
			profileFlag,
			{Name: "dry-run", Kind: clicore.KindBool, Description: "Show what would change, keep nothing."},
		},
		Examples: []string{config.name + " pack sync --profile my-odoo --dry-run --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runPackSync(ctx, invocation, config)
		},
	})
	add := local(&clicore.Command{
		Name:    "add",
		Summary: "Check a pack from a file against this Odoo and keep it for the profile.",
		Description: `For a module that ships no pack, a Studio customization or an Odoo Online database. The pack is checked
as sync checks one; a pack of module "custom" covers what was made in the database itself and requires only
base.`,
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags: []clicore.Flag{
			profileFlag,
			{Name: "file", Kind: clicore.KindString, Required: true, Description: "The pack (odoo-cli.pack/v1)."},
		},
		Examples: []string{config.name + " pack add --profile my-odoo --file pack.json --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runPackAdd(ctx, invocation, config)
		},
	})
	list := local(&clicore.Command{
		Name:        "list",
		Summary:     "List the packs kept for the profile.",
		Description: `Local: no network, no credential.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{personal(profileFlag)},
		Examples:    []string{config.name + " pack list --profile my-odoo --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runPackList(invocation, config)
		},
	})
	show := local(&clicore.Command{
		Name:        "show",
		Summary:     "Show a kept pack's operations: what each runs, its risk, and what an agent may do with it.",
		Description: `Local: no network, no credential. Risks are shown after the floors odoo-cli applies.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{personal(profileFlag), moduleFlag},
		Examples:    []string{config.name + " pack show --profile my-odoo --module x_salon --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runPackShow(invocation, config)
		},
	})
	remove := local(&clicore.Command{
		Name:        "remove",
		Summary:     "Forget a kept pack: its operations are gone from the next run.",
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags:       []clicore.Flag{profileFlag, moduleFlag},
		Examples:    []string{config.name + " pack remove --profile my-odoo --module x_salon"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runPackRemove(invocation, config)
		},
	})
	generate := local(&clicore.Command{
		Name:    "generate",
		Summary: "Write a pack for an installed module from what the module declares: its models, fields and buttons.",
		Description: `For the module's author, on a lab with the module installed. For each model the module defines: a read of
its stored plain fields, a create and an update of the fields a person can edit, and one call per object
button of its views. Wizards are not generated: declare them by hand. Edit the names, summaries and risks,
then ship the file as <module>/static/odoo-cli/pack.json.`,
		SideEffect:  clicore.EffectRead,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags: []clicore.Flag{
			profileFlag, moduleFlag,
			{Name: "file", Kind: clicore.KindString, Description: "Write the pack to this file as well."},
		},
		Examples: []string{config.name + " pack generate --profile lab --module x_salon --file pack.json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runPackGenerate(ctx, invocation, config)
		},
	})
	return &clicore.Command{
		Name:     "pack",
		Summary:  "A custom module's own operations, declared as data in a pack, checked against this Odoo (DEC-2026-0176).",
		Children: []*clicore.Command{sync, add, list, show, remove, generate},
	}
}

// packTarget is what the checks and the generator read: the executor, and the Odoo's modules.
type packTarget struct {
	ctx       context.Context
	executor  *direct.Executor
	profile   localprofile.Selection
	inventory odoo.ModuleInventory
	series    string
	fields    map[string]map[string]map[string]interface{}
}

func newPackTarget(ctx context.Context, invocation *clicore.Invocation, config programConfig) (*packTarget, error) {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return nil, err
	}
	executor, err := executorFor(ctx, selection, config)
	if err != nil {
		return nil, err
	}
	inventory, err := executor.ModuleInventory(ctx)
	if err != nil {
		return nil, classifyRawError(err, "ir.module.module", "search_read", config.surface)
	}
	series := odoo.ProviderVersionInfo{ServerVersion: inventory.ProviderVersion}.Series()
	return &packTarget{ctx: ctx, executor: executor, profile: selection, inventory: inventory, series: series,
		fields: map[string]map[string]map[string]interface{}{}}, nil
}

func (t *packTarget) installed(module string) bool {
	for _, name := range t.inventory.Installed {
		if name == module {
			return true
		}
	}
	return false
}

func (t *packTarget) read(model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error) {
	outcome, err := t.executor.Call(t.ctx, direct.RawRequest{Model: model, Method: method, Args: args, Kwargs: kwargs, AdmitCandidateSeries: true}, false)
	if err != nil {
		return nil, err
	}
	return outcome.Data, nil
}

func (t *packTarget) fieldsOf(model string) (map[string]map[string]interface{}, error) {
	if known, ok := t.fields[model]; ok {
		return known, nil
	}
	result, err := t.executor.FieldsGet(t.ctx, model, []string{"type", "relation", "store", "readonly", "required", "string"})
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]interface{}, len(result.Fields))
	for _, field := range result.Fields {
		out[field.Name] = field.Attributes
	}
	t.fields[model] = out
	return out, nil
}

// models answers, for each name, whether it is a model of this Odoo and whether it is transient.
func (t *packTarget) models(names []string) (map[string]bool, error) {
	values := make([]interface{}, len(names))
	for index, name := range names {
		values[index] = name
	}
	data, err := t.read("ir.model", "search_read", []interface{}{[]interface{}{[]interface{}{"model", "in", values}}},
		map[string]interface{}{"fields": []interface{}{"model", "transient"}})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	rows, _ := data.([]interface{})
	for _, row := range rows {
		record, _ := row.(map[string]interface{})
		if model, ok := record["model"].(string); ok {
			transient, _ := record["transient"].(bool)
			out[model] = transient
		}
	}
	return out, nil
}

// check is the live half of a pack's checks: it returns the pack with the types and relations of its
// written fields filled from Odoo, and warnings that do not refuse it.
func (t *packTarget) check(pack odoo.Pack) (odoo.Pack, []string, error) {
	var warnings []string
	if pack.Module != odoo.PackCustomModule && !t.installed(pack.Module) {
		return pack, nil, fmt.Errorf("module %s is not installed in this Odoo", pack.Module)
	}
	if len(pack.Series) > 0 && !packContainsString(pack.Series, t.series) {
		warnings = append(warnings, fmt.Sprintf("the pack was written for %s; this Odoo is %s", strings.Join(pack.Series, ", "), t.series))
	}
	transient, err := t.models(pack.PackModels())
	if err != nil {
		return pack, nil, err
	}
	for _, model := range pack.PackModels() {
		if _, ok := transient[model]; !ok {
			return pack, nil, fmt.Errorf("model %s does not exist in this Odoo", model)
		}
	}
	checked := pack
	checked.Operations = make([]odoo.PackOperation, len(pack.Operations))
	for index, op := range pack.Operations {
		completed, err := t.checkOperation(op, transient)
		if err != nil {
			return pack, nil, fmt.Errorf("%s: %w", op.ID, err)
		}
		checked.Operations[index] = completed
	}
	return checked, warnings, nil
}

func (t *packTarget) checkOperation(op odoo.PackOperation, transient map[string]bool) (odoo.PackOperation, error) {
	if transient[op.Model] {
		return op, fmt.Errorf("model %s is a wizard; an operation runs on a record of a model", op.Model)
	}
	fields, err := t.fieldsOf(op.Model)
	if err != nil {
		return op, err
	}
	switch op.Strategy {
	case "READ":
		for _, name := range op.ReadFields {
			meta, ok := fields[name]
			if !ok {
				return op, fmt.Errorf("field %s does not exist on %s", name, op.Model)
			}
			if meta["type"] == "binary" {
				return op, fmt.Errorf("field %s is a file; a pack reads no binary field", name)
			}
		}
	case "FIELD_WRITE":
		return t.completeWrites(op, op.Model, fields, op.Fields)
	case "METHOD_CALL":
		buttons, err := t.executor.ObjectButtons(t.ctx, op.Model, []string{"form", "list"})
		if err != nil {
			return op, err
		}
		if !packContainsString(buttons, op.Method) {
			return op, fmt.Errorf("%s is not an object button of the views of %s; a pack calls only what a person can click", op.Method, op.Model)
		}
	case "WIZARD":
		if !transient[op.Wizard.Model] {
			return op, fmt.Errorf("%s is not a wizard (a transient model)", op.Wizard.Model)
		}
		buttons, err := t.executor.ObjectButtons(t.ctx, op.Wizard.Model, []string{"form"})
		if err != nil {
			return op, err
		}
		if !packContainsString(buttons, op.Wizard.Method) {
			return op, fmt.Errorf("%s is not an object button of the wizard %s", op.Wizard.Method, op.Wizard.Model)
		}
		wizardFields, err := t.fieldsOf(op.Wizard.Model)
		if err != nil {
			return op, err
		}
		return t.completeWrites(op, op.Wizard.Model, wizardFields, op.Wizard.Fields)
	case "REPORT":
		count, err := t.read("ir.actions.report", "search_count",
			[]interface{}{[]interface{}{[]interface{}{"report_name", "=", op.Report}, []interface{}{"model", "=", op.Model}}}, nil)
		if err != nil {
			return op, err
		}
		if number, _ := count.(float64); number < 1 {
			return op, fmt.Errorf("report %s of %s does not exist in this Odoo", op.Report, op.Model)
		}
	}
	return op, nil
}

// completeWrites checks the written fields and fills their types and relations from Odoo.
func (t *packTarget) completeWrites(op odoo.PackOperation, model string, fields map[string]map[string]interface{}, names []string) (odoo.PackOperation, error) {
	types := map[string]string{}
	for field, kind := range op.Types {
		types[field] = kind
	}
	relations := map[string]odoo.PackRelation{}
	for field, relation := range op.Relations {
		relations[field] = relation
	}
	for _, name := range names {
		meta, ok := fields[name]
		if !ok {
			return op, fmt.Errorf("field %s does not exist on %s", name, model)
		}
		odooType, _ := meta["type"].(string)
		kind, writable := packWriteTypes[odooType]
		if !writable {
			return op, fmt.Errorf("field %s is a %s field, which a pack does not write", name, odooType)
		}
		if declared := types[name]; declared != "" && declared != kind {
			return op, fmt.Errorf("field %s is declared %s, and Odoo's %s field takes %s", name, declared, odooType, kind)
		}
		types[name] = kind
		if kind == "reference" {
			target, _ := meta["relation"].(string)
			declared, ok := relations[name]
			if ok && declared.Model != target {
				return op, fmt.Errorf("field %s relates to %s, not %s", name, target, declared.Model)
			}
			if !ok {
				relations[name] = odoo.PackRelation{Model: target}
			}
		}
	}
	op.Types = types
	if len(relations) > 0 {
		op.Relations = relations
	}
	return op, nil
}

func packSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fetchModulePack(ctx context.Context, endpoint, module string) ([]byte, error) {
	address := strings.TrimRight(endpoint, "/") + "/" + module + "/" + packStaticPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	// The profile's own Odoo, checked when the executor was built; a redirect elsewhere is no pack.
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, packMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > packMaxBytes {
		return nil, fmt.Errorf("the pack of %s is over %d bytes", module, packMaxBytes)
	}
	return data, nil
}

type packChange struct {
	Module     string   `json:"module"`
	Status     string   `json:"status"`
	Operations int      `json:"operations,omitempty"`
	Error      string   `json:"error,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

func runPackSync(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	dryRun, err := invocation.Bool("dry-run")
	if err != nil {
		return nil, err
	}
	target, err := newPackTarget(ctx, invocation, config)
	if err != nil {
		return nil, err
	}
	stored, _ := readStoredPacks(target.profile.Profile.Name)
	kept := map[string]storedPack{}
	for _, pack := range stored {
		kept[pack.Pack.Module] = pack
	}
	var changes []packChange
	looked, found := 0, map[string]bool{}
	for _, module := range target.inventory.Installed {
		if odoo.ClassifyModule(target.series, module, "") != odoo.ModuleThirdParty {
			continue
		}
		looked++
		data, err := fetchModulePack(ctx, target.profile.Profile.Endpoint, module)
		if err != nil {
			changes = append(changes, packChange{Module: module, Status: "REFUSED", Error: err.Error()})
			continue
		}
		if data == nil {
			continue
		}
		found[module] = true
		change, next := target.accept(data, module, "module static file "+module+"/"+packStaticPath)
		if next != nil {
			previous, had := kept[module]
			switch {
			case !had:
				change.Status = "ADDED"
			case previous.Origin.SHA256 == next.Origin.SHA256:
				change.Status = "UNCHANGED"
			default:
				change.Status = "CHANGED"
			}
			if !dryRun && change.Status != "UNCHANGED" {
				if err := writeStoredPack(target.profile.Profile.Name, *next); err != nil {
					return nil, clicore.InternalError(err)
				}
			}
		}
		changes = append(changes, change)
	}
	for module, pack := range kept {
		if found[module] || !strings.HasPrefix(pack.Origin.Source, "module static file") {
			continue
		}
		changes = append(changes, packChange{Module: module, Status: "REMOVED", Operations: len(pack.Pack.Operations)})
		if !dryRun {
			if _, err := removeStoredPack(target.profile.Profile.Name, module); err != nil {
				return nil, clicore.InternalError(err)
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Module < changes[j].Module })
	return map[string]interface{}{
		"profile": target.profile.Profile.Name, "series": target.series, "customModules": looked,
		"packs": changes, "dryRun": dryRun,
		"next": "A pack's operations are commands and MCP tools from the next run: " + config.name + " pack show --module <module>.",
	}, nil
}

// accept parses and checks one pack. It returns the change to report and, when the pack passed, the
// pack to keep.
func (t *packTarget) accept(data []byte, module, source string) (packChange, *storedPack) {
	change := packChange{Module: module}
	pack, err := odoo.ParsePack(data)
	if err == nil {
		err = pack.Validate(packReserved)
	}
	if err == nil && module != "" && pack.Module != module {
		err = fmt.Errorf("module %s serves the pack of %s; a module serves only its own", module, pack.Module)
	}
	if err != nil {
		change.Status, change.Error = "REFUSED", err.Error()
		return change, nil
	}
	change.Module = pack.Module
	checked, warnings, err := t.check(pack)
	if err != nil {
		change.Status, change.Error = "REFUSED", err.Error()
		return change, nil
	}
	change.Operations, change.Warnings = len(checked.Operations), warnings
	return change, &storedPack{
		Pack:      checked,
		Origin:    odoo.PackOrigin{Module: checked.Module, Title: checked.Title, Author: checked.Author, SHA256: packSHA(data), Source: source},
		Series:    t.series,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Warnings:  warnings,
	}
}

func runPackAdd(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	path := strings.TrimSpace(invocation.String("file"))
	data, err := readPackFile(path)
	if err != nil {
		return nil, err
	}
	target, err := newPackTarget(ctx, invocation, config)
	if err != nil {
		return nil, err
	}
	change, next := target.accept(data, "", "file "+filepath.Base(path))
	if next == nil {
		return nil, clicore.UsageError("PACK_REFUSED", "the pack is refused: %s", change.Error).WithContextValue("module", change.Module)
	}
	stored, _ := readStoredPacks(target.profile.Profile.Name)
	change.Status = "ADDED"
	for _, pack := range stored {
		if pack.Pack.Module == next.Pack.Module {
			change.Status = "CHANGED"
		}
	}
	if err := writeStoredPack(target.profile.Profile.Name, *next); err != nil {
		return nil, clicore.InternalError(err)
	}
	return map[string]interface{}{"profile": target.profile.Profile.Name, "series": target.series, "pack": change,
		"next": "The pack's operations are commands and MCP tools from the next run: " + config.name + " pack show --module " + next.Pack.Module + "."}, nil
}

func readPackFile(path string) ([]byte, error) {
	if path == "" {
		return nil, clicore.UsageError("MISSING_REQUIRED_FLAG", "--file is required").WithContextValue("flag", "file")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "PACK_FILE_UNREADABLE", fmt.Sprintf("cannot read %s: %v", path, err))
	}
	if info.Size() > packMaxBytes {
		return nil, clicore.UsageError("PACK_REFUSED", "%s is over %d bytes", path, packMaxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "PACK_FILE_UNREADABLE", fmt.Sprintf("cannot read %s: %v", path, err))
	}
	return data, nil
}

func runPackList(invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return nil, err
	}
	stored, problems := readStoredPacks(selection.Profile.Name)
	packs := make([]map[string]interface{}, 0, len(stored))
	for _, pack := range stored {
		packs = append(packs, map[string]interface{}{
			"module": pack.Pack.Module, "title": pack.Pack.Title, "author": pack.Pack.Author, "operations": len(pack.Pack.Operations),
			"source": pack.Origin.Source, "sha256": pack.Origin.SHA256, "series": pack.Series, "checkedAt": pack.CheckedAt,
			"warnings": pack.Warnings,
		})
	}
	out := map[string]interface{}{"profile": selection.Profile.Name, "packs": packs, "count": len(packs)}
	if len(problems) > 0 {
		unreadable := make([]string, len(problems))
		for index, problem := range problems {
			unreadable[index] = problem.Error()
		}
		out["unreadable"] = unreadable
	}
	return out, nil
}

func runPackShow(invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return nil, err
	}
	module := strings.TrimSpace(invocation.String("module"))
	stored, _ := readStoredPacks(selection.Profile.Name)
	for _, pack := range stored {
		if pack.Pack.Module != module {
			continue
		}
		operations := make([]map[string]interface{}, 0, len(pack.Pack.Operations))
		for _, op := range pack.Pack.Operations {
			access := clicore.AgentAccessRead
			if op.Strategy != "READ" && op.Strategy != "REPORT" {
				access = clicore.AgentAccessConfirm
			}
			entry := map[string]interface{}{
				"id": op.ID, "strategy": op.Strategy, "model": op.Model, "risk": odoo.PackRisk(op), "agentAccess": access,
				"command": config.name + " " + strings.Join(strings.Split(op.ID, "."), " "),
			}
			if op.Summary != "" {
				entry["summary"] = op.Summary
			}
			if op.Risk != "" && odoo.RiskClassification(op.Risk) != odoo.PackRisk(op) {
				entry["declaredRisk"] = op.Risk
			}
			operations = append(operations, entry)
		}
		return map[string]interface{}{
			"profile": selection.Profile.Name, "module": module, "title": pack.Pack.Title, "author": pack.Pack.Author,
			"source": pack.Origin.Source, "sha256": pack.Origin.SHA256, "series": pack.Series, "checkedAt": pack.CheckedAt,
			"readiness": string(odoo.ReadinessUnverified), "operations": operations,
			"notice": "A pack is its author's declaration. ASAO has not verified these operations; each runs only with --allow-unverified, and an agent's write always asks you first.",
		}, nil
	}
	return nil, clicore.NewError(clicore.CategoryNotFound, "PACK_NOT_FOUND", fmt.Sprintf("profile %s keeps no pack of %s", selection.Profile.Name, module)).
		WithRemediation("Run `" + config.name + " pack list --profile " + selection.Profile.Name + "`.")
}

func runPackRemove(invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	selection, err := resolveProfile(invocation, config.name)
	if err != nil {
		return nil, err
	}
	module := strings.TrimSpace(invocation.String("module"))
	removed, err := removeStoredPack(selection.Profile.Name, module)
	if err != nil {
		return nil, clicore.InternalError(err)
	}
	if !removed {
		return nil, clicore.NewError(clicore.CategoryNotFound, "PACK_NOT_FOUND", fmt.Sprintf("profile %s keeps no pack of %s", selection.Profile.Name, module))
	}
	return map[string]interface{}{"profile": selection.Profile.Name, "module": module, "removed": true}, nil
}

func packContainsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
