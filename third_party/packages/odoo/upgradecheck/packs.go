package upgradecheck

import (
	"sort"
	"strings"

	"github.com/asao-platform/atoc/packages/odoo"
)

// PackKey is the key of the operations of the operation packs a person registered (DEC-2026-0176),
// checked as customizations: a pack names models and fields of the database the way a saved filter does,
// and an upgrade that drops one breaks the operation.
const PackKey = "pack_operations"

// PackRecords turns packs into the rows a check reads under PackKey: one per operation, with the model,
// the fields it reads, writes and sorts by, the field each relation searches by, and its wizard.
func PackRecords(packs []odoo.Pack) []map[string]interface{} {
	strs := func(values []string) []interface{} {
		out := make([]interface{}, len(values))
		for i, v := range values {
			out[i] = v
		}
		return out
	}
	var rows []map[string]interface{}
	for _, pack := range packs {
		for _, op := range pack.Operations {
			row := map[string]interface{}{
				"id": int64(len(rows) + 1), "name": op.ID, "module": pack.Module, "model": op.Model,
				"read": strs(op.ReadFields), "write": strs(op.Fields), "method": op.Method,
			}
			var sort []interface{}
			for _, part := range strings.Split(op.DefaultSort, ",") {
				if name := strings.Fields(part); len(name) > 0 {
					sort = append(sort, name[0])
				}
			}
			row["sort"] = sort
			var searches []interface{}
			for _, field := range sortedKeys(op.Relations) {
				if relation := op.Relations[field]; relation.SearchField != "" {
					searches = append(searches, field+"."+relation.SearchField)
				}
			}
			row["searches"] = searches
			if op.Wizard != nil {
				row["wizard_model"] = op.Wizard.Model
				row["wizard_fields"] = strs(op.Wizard.Fields)
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// packs checks each pack operation's model, then every field it names, on the target series. A model of
// the module itself is outside the map unless the target lab installs the module: it is UNKNOWN, never
// guessed. Methods are not in the map: pack sync, run again after the upgrade, checks them.
func (s *scanner) packs() {
	for _, record := range s.rows(PackKey) {
		model := text(record["model"])
		where := s.where(PackKey, record, model)
		if s.modelGone(where, model) {
			continue
		}
		// A relation's search field is used in a domain (name_search), so it must stay searchable.
		for _, use := range [][2]string{{"read", "read"}, {"write", "write"}, {"order", "sort"}, {"domain", "searches"}} {
			for _, field := range list(record[use[1]]) {
				s.checkPath(where, model, text(field), use[0], "CERTAIN", nil, ".")
			}
		}
		if wizard := text(record["wizard_model"]); wizard != "" && !s.modelGone(where, wizard) {
			for _, field := range list(record["wizard_fields"]) {
				s.checkPath(where, wizard, text(field), "write", "CERTAIN", nil, ".")
			}
		}
	}
}

func sortedKeys(relations map[string]odoo.PackRelation) []string {
	keys := make([]string, 0, len(relations))
	for key := range relations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
