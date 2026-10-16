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
)

// pack generate: a first pack for a module, from what the module declares in a lab (DEC-2026-0176).
// For each model the module defines, a read of its stored plain fields, a create and an update of the
// fields a person can edit, and one call per object button. The author edits it before shipping it.

const (
	packGeneratedReadFields  = 25
	packGeneratedWriteFields = 40
)

func runPackGenerate(ctx context.Context, invocation *clicore.Invocation, config programConfig) (interface{}, error) {
	module := strings.TrimSpace(invocation.String("module"))
	target, err := newPackTarget(ctx, invocation, config)
	if err != nil {
		return nil, err
	}
	if !target.installed(module) {
		return nil, clicore.UsageError("PACK_MODULE_NOT_INSTALLED", "module %s is not installed in this Odoo", module).WithContextValue("module", module)
	}
	pack, skipped, err := target.generate(module)
	if err != nil {
		return nil, clicore.UsageError("PACK_REFUSED", "%v", err).WithContextValue("module", module)
	}
	if path := strings.TrimSpace(invocation.String("file")); path != "" {
		data, err := json.MarshalIndent(pack, "", "  ")
		if err != nil {
			return nil, clicore.InternalError(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			return nil, clicore.NewError(clicore.CategoryConfig, "PACK_FILE_NOT_WRITTEN", fmt.Sprintf("could not write %s: %v", path, err))
		}
	}
	return map[string]interface{}{"pack": pack, "operations": len(pack.Operations), "skipped": skipped,
		"next": "Edit the names, summaries and risks, declare the wizards by hand, then ship the file as " + module + "/" + packStaticPath + "."}, nil
}

// generate writes the pack; skipped says what it left out and why.
func (t *packTarget) generate(module string) (odoo.Pack, []string, error) {
	data, err := t.read("ir.model.data", "search_read",
		[]interface{}{[]interface{}{[]interface{}{"module", "=", module}, []interface{}{"model", "=", "ir.model"}}},
		map[string]interface{}{"fields": []interface{}{"res_id"}})
	if err != nil {
		return odoo.Pack{}, nil, err
	}
	var ids []interface{}
	rows, _ := data.([]interface{})
	for _, row := range rows {
		record, _ := row.(map[string]interface{})
		if id, ok := record["res_id"].(float64); ok {
			ids = append(ids, int64(id))
		}
	}
	if len(ids) == 0 {
		return odoo.Pack{}, nil, fmt.Errorf("module %s defines no model", module)
	}
	data, err = t.read("ir.model", "read", []interface{}{ids}, map[string]interface{}{"fields": []interface{}{"model", "transient"}})
	if err != nil {
		return odoo.Pack{}, nil, err
	}
	var models []string
	rows, _ = data.([]interface{})
	for _, row := range rows {
		record, _ := row.(map[string]interface{})
		if transient, _ := record["transient"].(bool); transient {
			continue
		}
		if model, ok := record["model"].(string); ok {
			models = append(models, model)
		}
	}
	sort.Strings(models)
	pack := odoo.Pack{Schema: odoo.PackSchema, Module: module, Title: module, Series: []string{t.series}}
	var skipped []string
	usedIDs := map[string]bool{}
	for _, model := range models {
		operations, notes, err := t.generateModel(module, model, usedIDs)
		if err != nil {
			return odoo.Pack{}, nil, err
		}
		pack.Operations = append(pack.Operations, operations...)
		skipped = append(skipped, notes...)
	}
	if len(pack.Operations) == 0 {
		return odoo.Pack{}, skipped, fmt.Errorf("module %s defines no model a pack may name", module)
	}
	if err := pack.Validate(packReserved); err != nil {
		return odoo.Pack{}, skipped, err
	}
	return pack, skipped, nil
}

func (t *packTarget) generateModel(module, model string, usedIDs map[string]bool) ([]odoo.PackOperation, []string, error) {
	probe := odoo.Pack{Schema: odoo.PackSchema, Module: module, Operations: []odoo.PackOperation{
		{ID: module + ".probe.list", Kind: "READ", Strategy: "READ", Model: model, ReadFields: []string{"id"}}}}
	if err := probe.Validate(packReserved); err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", model, err)}, nil
	}
	fields, err := t.fieldsOf(model)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	// The name first, then the required fields, then the rest, each part alphabetical.
	rank := func(name string) int {
		switch {
		case name == "name":
			return 0
		case fields[name]["required"] == true:
			return 1
		default:
			return 2
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if rank(names[i]) != rank(names[j]) {
			return rank(names[i]) < rank(names[j])
		}
		return names[i] < names[j]
	})
	plain := func(name string) bool {
		return !strings.HasPrefix(name, "__") && !odoo.IsSecretField(name) && strings.ToLower(name) == name &&
			!packContainsString([]string{"id", "create_uid", "create_date", "write_uid", "write_date", "display_name"}, name)
	}
	var read, write []string
	types := map[string]string{}
	relations := map[string]odoo.PackRelation{}
	for _, name := range names {
		meta := fields[name]
		odooType, _ := meta["type"].(string)
		if !plain(name) || meta["store"] != true || odooType == "binary" {
			continue
		}
		if len(read) < packGeneratedReadFields && odooType != "one2many" && odooType != "many2many" {
			read = append(read, name)
		}
		kind, writable := packWriteTypes[odooType]
		if !writable || meta["readonly"] == true || len(write) >= packGeneratedWriteFields {
			continue
		}
		if kind == "reference" {
			target, _ := meta["relation"].(string)
			probe.Operations[0].Model = target
			if probe.Validate(packReserved) != nil {
				continue // a relation to a model a pack may not name
			}
			relations[name] = odoo.PackRelation{Model: target}
		}
		write = append(write, name)
		types[name] = kind
	}
	entity := packEntity(module, model)
	id := func(verb string) string {
		candidate := module + "." + entity + "." + verb
		if usedIDs[candidate] {
			candidate = module + "." + entity + "." + verb + "_" + fmt.Sprint(len(usedIDs))
		}
		usedIDs[candidate] = true
		return candidate
	}
	var operations []odoo.PackOperation
	if len(read) > 0 {
		operations = append(operations, odoo.PackOperation{ID: id("list"), Kind: "READ", Strategy: "READ", Model: model, ReadFields: read})
	}
	if len(write) > 0 {
		for _, mode := range []string{"CREATE", "UPDATE"} {
			operations = append(operations, odoo.PackOperation{ID: id(strings.ToLower(mode)), Kind: "COMMAND", Strategy: "FIELD_WRITE", Model: model,
				WriteMode: mode, Fields: write, Types: types, Relations: relations})
		}
	}
	buttons, err := t.executor.ObjectButtons(t.ctx, model, []string{"form", "list"})
	if err != nil {
		return nil, nil, err
	}
	var skipped []string
	for _, method := range buttons {
		op := odoo.PackOperation{Kind: "ACTION", Strategy: "METHOD_CALL", Model: model, Method: method}
		probe.Operations[0] = odoo.PackOperation{ID: module + ".probe.call", Kind: "ACTION", Strategy: "METHOD_CALL", Model: model, Method: method}
		if err := probe.Validate(packReserved); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s.%s: %v", model, method, err))
			continue
		}
		op.ID = id(packVerb(method))
		operations = append(operations, op)
	}
	return operations, skipped, nil
}

// packEntity names a model inside its module's pack: x_salon.booking -> booking, salon_booking (of
// module salon) -> booking.
func packEntity(module, model string) string {
	name := strings.TrimPrefix(model, module+".")
	name = strings.TrimPrefix(name, module+"_")
	name = strings.TrimPrefix(name, "x_")
	name = strings.ReplaceAll(name, ".", "_")
	if name == "" {
		name = "record"
	}
	return name
}

// packVerb names a button: action_confirm -> confirm, button_done -> done.
func packVerb(method string) string {
	for _, prefix := range []string{"action_", "button_"} {
		if rest := strings.TrimPrefix(method, prefix); rest != method && rest != "" {
			return rest
		}
	}
	return method
}
