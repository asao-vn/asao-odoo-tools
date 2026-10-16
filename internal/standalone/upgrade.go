package standalone

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
	"github.com/asao-platform/atoc/packages/odoo/upgradecheck"
)

// The upgrade group: what an upgrade to the next series does to the customizations people made in
// the database (packages/odoo/upgradecheck). It reads only, through Odoo's read methods, and never
// prints the code or the templates it reads: only the references that will stop resolving.

func upgradeGroup(config programConfig) *clicore.Command {
	personal := func(flag clicore.Flag) clicore.Flag {
		flag.PersonOnly = true
		return flag
	}
	check := local(&clicore.Command{
		Name:    "check",
		Summary: "Say which customizations of this database an upgrade to the next series breaks, before the upgrade.",
		Description: `Reads what people made in the selected Odoo (saved filters, automation rules, server actions, custom
fields, email templates, views, record rules, access rights, export templates, default values and window
actions: the records no standard module owns) and checks every model and field they reference against
the target series, with the map this build ships for the pair (upgrade maps). It reads and never writes.

Each finding names the customization, the path that stops resolving, why, and a severity:
- BREAKS: stops working on the target; RENAME: the same, and names the field's probable new name;
- CHECK: still runs but may mean something else (a filter on a selection value the target dropped
  matches nothing);
- REVIEW: text it could not read; UNKNOWN: outside what the map covers (an Enterprise or third-party
  module, or one the target lab did not install); INFO: already broken before the upgrade.
Code and templates are read to find references and never printed.

The map comes from the conformance labs of both series (Community, demo data). Measured on customizations
planted on 19.0 and replayed on 20.0, it flagged 8 of 10 breaks it had not been tuned on; read every
finding, and test the upgrade on a copy of the database. Run it as an administrator: the customizations
are only readable to one.`,
		SideEffect:  clicore.EffectRead,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags: []clicore.Flag{
			personal(profileFlag),
			{Name: "to", Kind: clicore.KindString, Required: true, Description: "The series to upgrade to, e.g. 20.0 (upgrade maps lists the pairs)."},
			personal(clicore.Flag{Name: "markdown-file", Kind: clicore.KindString, Description: "Also write the report a person reads, in Vietnamese, to this file."}),
		},
		Examples: []string{
			config.name + " upgrade check --profile my-odoo --to 20.0 --output json",
			config.name + " upgrade check --profile my-odoo --to 20.0 --markdown-file nang-cap-20.md",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runUpgradeCheck(ctx, invocation, config)
		},
	})
	maps := local(&clicore.Command{
		Name:        "maps",
		Summary:     "List the upgrade maps this build ships: which series to which, and the labs each was made from.",
		Description: `Pure local listing: no network, no credential.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Examples:    []string{config.name + " upgrade maps --output json"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			infos, err := upgradecheck.Pairs()
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			return map[string]interface{}{"maps": infos, "count": len(infos)}, nil
		},
	})
	return &clicore.Command{
		Name:     "upgrade",
		Summary:  "Before an upgrade to the next Odoo series: what it breaks among the customizations people made.",
		Children: []*clicore.Command{check, maps},
	}
}

func runUpgradeCheck(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	to := strings.TrimSpace(invocation.String("to"))
	remedy := "Run " + config.name + " upgrade maps for the pairs this build can check: " + strings.Join(upgradecheck.Available(), ", ") + "."
	reachable := false
	for _, pair := range upgradecheck.Available() {
		reachable = reachable || strings.HasSuffix(pair, " -> "+to)
	}
	if !reachable { // refused before the Odoo is contacted
		return nil, clicore.UsageError("UPGRADE_MAP_UNKNOWN", "this build has no map to %s", to).
			WithContextValue("to", to).WithRemediation(remedy)
	}
	executor, err := standaloneTargetExecutor(ctx, invocation, config)
	if err != nil {
		return nil, err
	}
	inventory, err := executor.ModuleInventory(ctx)
	if err != nil {
		return nil, classifyRawError(err, "ir.module.module", "search_read", config.surface)
	}
	from := odoo.ProviderVersionInfo{ServerVersion: inventory.ProviderVersion}.Series()
	pair, err := upgradecheck.Load(from, to)
	if err != nil {
		return nil, clicore.UsageError("UPGRADE_MAP_UNKNOWN", "this build has no map from %s (the selected Odoo, %s) to %s", from, inventory.ProviderVersion, to).
			WithContextValue("from", from).WithContextValue("to", to).WithRemediation(remedy)
	}
	reader := &upgradeReader{ctx: ctx, executor: executor, config: config, fields: map[string]map[string]upgradecheck.Field{}}
	custom, err := reader.customizations()
	if err != nil {
		return nil, err
	}
	report := upgradecheck.Scan(reader, custom, pair)
	if reader.err != nil {
		return nil, reader.err
	}
	total := 0
	for key, count := range report.Scanned {
		if key != "export_lines" {
			total += count
		}
	}
	if path := strings.TrimSpace(invocation.String("markdown-file")); path != "" {
		if err := os.WriteFile(path, []byte(upgradecheck.Markdown(report, pair, inventory.ProviderVersion)), 0o600); err != nil {
			return nil, clicore.NewError(clicore.CategoryConfig, "UPGRADE_FILE_NOT_WRITTEN", fmt.Sprintf("could not write %s: %v", path, err))
		}
	}
	return map[string]interface{}{
		"from":           pair.From,
		"to":             pair.To,
		"source":         map[string]interface{}{"serverVersion": inventory.ProviderVersion, "installedModules": len(inventory.Installed)},
		"map":            pair.Info(),
		"customizations": total,
		"scanned":        report.Scanned,
		"bySeverity":     report.BySeverity,
		"findings":       report.Findings,
		"severities":     upgradecheck.SeverityMeanings,
		"notChecked":     upgradecheck.NotChecked,
	}, nil
}

// upgradeReader reads the source database for the check, through Odoo's read methods only.
type upgradeReader struct {
	ctx      context.Context
	executor *direct.Executor
	config   programConfig
	err      error
	models   map[string]bool
	fields   map[string]map[string]upgradecheck.Field
}

// upgradePage is how many ids one read asks for.
const upgradePage = 200

func (r *upgradeReader) call(model, method string, args []interface{}, kwargs map[string]interface{}) (interface{}, error) {
	if kwargs == nil {
		kwargs = map[string]interface{}{}
	}
	// Archived customizations still upgrade.
	kwargs["context"] = map[string]interface{}{"active_test": false}
	outcome, err := r.executor.Call(r.ctx, direct.RawRequest{Model: model, Method: method, Args: args, Kwargs: kwargs}, false)
	if err != nil {
		return nil, classifyRawError(err, model, method, r.config.surface)
	}
	// The values as a script reads them: JSON numbers, lists and maps.
	encoded, err := json.Marshal(outcome.Data)
	if err != nil {
		return nil, clicore.InternalError(err)
	}
	var data interface{}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return nil, clicore.InternalError(err)
	}
	return data, nil
}

func (r *upgradeReader) rows(model string, domain []interface{}, fields []string) ([]map[string]interface{}, error) {
	answer, err := r.call(model, "search_read", []interface{}{domain}, map[string]interface{}{"fields": fields, "order": "id"})
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, item := range list(answer) {
		if row, ok := item.(map[string]interface{}); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

// Fields is the model's fields as fields_get describes them, read once per model.
func (r *upgradeReader) Fields(model string) (map[string]upgradecheck.Field, bool) {
	if r.err != nil || !r.models[model] {
		return nil, false
	}
	if fields, ok := r.fields[model]; ok {
		return fields, true
	}
	answer, err := r.call(model, "fields_get", []interface{}{}, map[string]interface{}{"attributes": []string{"type", "relation", "store", "searchable", "selection"}})
	if err != nil {
		r.err = err
		return nil, false
	}
	described, _ := answer.(map[string]interface{})
	fields := make(map[string]upgradecheck.Field, len(described))
	for name, value := range described {
		spec, _ := value.(map[string]interface{})
		field := upgradecheck.Field{Type: stringOf(spec["type"]), Relation: stringOf(spec["relation"]), Store: spec["store"] == true,
			Searchable: spec["searchable"] == true}
		for _, option := range list(spec["selection"]) {
			if pair := list(option); len(pair) > 0 {
				field.Selection = append(field.Selection, fmt.Sprint(pair[0]))
			}
		}
		fields[name] = field
	}
	r.fields[model] = fields
	return fields, true
}

// Modules names the modules that define the model, or one of its fields.
func (r *upgradeReader) Modules(model, field string) []string {
	if r.err != nil {
		return nil
	}
	var rows []map[string]interface{}
	var err error
	if field == "" {
		rows, err = r.rows("ir.model", []interface{}{[]interface{}{"model", "=", model}}, []string{"modules"})
	} else {
		rows, err = r.rows("ir.model.fields", []interface{}{[]interface{}{"model", "=", model}, []interface{}{"name", "=", field}}, []string{"modules"})
	}
	if err != nil {
		r.err = err
		return nil
	}
	var modules []string
	for _, row := range rows {
		for _, module := range strings.Split(stringOf(row["modules"]), ",") {
			if module = strings.TrimSpace(module); module != "" {
				modules = append(modules, module)
			}
		}
	}
	return modules
}

// upgradeKinds are the fields read of each kind of customization, among those the model has.
var upgradeKinds = []struct {
	key, model string
	fields     []string
	domain     []interface{}
}{
	{"filters", "ir.filters", []string{"name", "model_id", "domain", "context", "sort", "active"}, nil},
	{"automations", "base.automation", []string{"name", "model_id", "trigger", "trigger_field_ids", "filter_domain", "filter_pre_domain",
		"on_change_field_ids", "trg_date_id", "trg_selection_field_id", "active"}, nil},
	{"server_actions", "ir.actions.server", []string{"name", "model_id", "state", "code", "update_path", "update_field_id", "crud_model_id",
		"link_field_id", "webhook_field_ids"}, nil},
	{"custom_fields", "ir.model.fields", []string{"model", "name", "ttype", "relation", "related", "depends", "compute"},
		[]interface{}{[]interface{}{"state", "=", "manual"}}},
	{"templates", "mail.template", []string{"name", "model", "subject", "body_html", "email_from", "email_to", "email_cc", "partner_to",
		"reply_to", "lang", "scheduled_date"}, nil},
	{"views", "ir.ui.view", []string{"name", "model", "type", "arch_db", "inherit_id", "mode", "active"}, nil},
	{"rules", "ir.rule", []string{"name", "model_id", "domain_force", "active"}, nil},
	{"access_rights", "ir.model.access", []string{"name", "model_id", "group_id"}, nil},
	{"exports", "ir.exports", []string{"name", "resource"}, nil},
	{"export_lines", "ir.exports.line", []string{"name", "export_id"}, nil},
	{"defaults", "ir.default", []string{"field_id", "json_value"}, nil},
	{"window_actions", "ir.actions.act_window", []string{"name", "res_model", "domain", "context"}, nil},
}

// Modules whose external ids mark a record a person made: exported, made by hand, or by Studio.
var personModules = map[string]bool{"__export__": true, "__custom__": true, "studio_customization": true}

func (r *upgradeReader) customizations() (upgradecheck.Customizations, error) {
	custom := upgradecheck.Customizations{Records: map[string][]map[string]interface{}{}, ModelIDs: map[string]string{},
		FieldIDs: map[string][2]string{}}
	models, err := r.rows("ir.model", []interface{}{}, []string{"model", "state"})
	if err != nil {
		return custom, err
	}
	r.models = map[string]bool{}
	for _, row := range models {
		name := stringOf(row["model"])
		r.models[name] = true
		custom.ModelIDs[fmt.Sprint(integerOf(row["id"]))] = name
		if stringOf(row["state"]) == "manual" {
			custom.CustomModels = append(custom.CustomModels, name)
		}
	}
	referenced := map[int64]bool{}
	for _, kind := range upgradeKinds {
		available, exists := r.Fields(kind.model)
		if r.err != nil {
			return custom, r.err
		}
		if !exists {
			continue
		}
		var fields []string
		for _, name := range kind.fields {
			if _, ok := available[name]; ok {
				fields = append(fields, name)
			}
		}
		rows, err := r.personMade(kind.model, kind.domain, fields)
		if err != nil {
			return custom, err
		}
		custom.Records[kind.key] = rows
		for _, row := range rows {
			for _, key := range []string{"trigger_field_ids", "on_change_field_ids", "webhook_field_ids"} {
				for _, id := range list(row[key]) {
					referenced[integerOf(id)] = true
				}
			}
			for _, key := range []string{"trg_date_id", "trg_selection_field_id", "update_field_id", "link_field_id", "field_id"} {
				if pair := list(row[key]); len(pair) > 0 {
					referenced[integerOf(pair[0])] = true
				}
			}
		}
	}
	if len(referenced) > 0 {
		ids := make([]interface{}, 0, len(referenced))
		for id := range referenced {
			ids = append(ids, id)
		}
		answer, err := r.call("ir.model.fields", "read", []interface{}{ids}, map[string]interface{}{"fields": []string{"model", "name"}})
		if err != nil {
			return custom, err
		}
		for _, item := range list(answer) {
			row, _ := item.(map[string]interface{})
			custom.FieldIDs[fmt.Sprint(integerOf(row["id"]))] = [2]string{stringOf(row["model"]), stringOf(row["name"])}
		}
	}
	return custom, nil
}

// personMade reads the records of model that no standard module owns.
func (r *upgradeReader) personMade(model string, domain []interface{}, fields []string) ([]map[string]interface{}, error) {
	if domain == nil {
		domain = []interface{}{}
	}
	answer, err := r.call(model, "search", []interface{}{domain}, map[string]interface{}{"order": "id"})
	if err != nil {
		return nil, err
	}
	owners, err := r.rows("ir.model.data", []interface{}{[]interface{}{"model", "=", model}}, []string{"module", "res_id"})
	if err != nil {
		return nil, err
	}
	owned := map[int64]bool{}
	for _, row := range owners {
		if !personModules[stringOf(row["module"])] {
			owned[integerOf(row["res_id"])] = true
		}
	}
	var ids []interface{}
	for _, id := range list(answer) {
		if !owned[integerOf(id)] {
			ids = append(ids, integerOf(id))
		}
	}
	var out []map[string]interface{}
	for start := 0; start < len(ids); start += upgradePage {
		end := start + upgradePage
		if end > len(ids) {
			end = len(ids)
		}
		page, err := r.call(model, "read", []interface{}{ids[start:end]}, map[string]interface{}{"fields": fields})
		if err != nil {
			return nil, err
		}
		for _, item := range list(page) {
			if row, ok := item.(map[string]interface{}); ok {
				out = append(out, row)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return integerOf(out[i]["id"]) < integerOf(out[j]["id"]) })
	return out, nil
}

func list(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

func stringOf(value interface{}) string {
	if value, ok := value.(string); ok {
		return value
	}
	return ""
}

func integerOf(value interface{}) int64 {
	switch value := value.(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	}
	return 0
}
