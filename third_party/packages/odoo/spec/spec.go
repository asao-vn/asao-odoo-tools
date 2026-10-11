// Package spec is business configuration as code.
//
// A Spec states what a business keeps in its ERP: the modules it needs and the records it holds
// (its company, departments, products, stages, ...), in the order they are applied. Plan compares
// that desired state with what the provider holds and says, step by step, what would change. Apply
// makes those changes through the same catalog operations the CLI runs, so every change carries the
// operation's readiness, its refusals and its safety rules: nothing here talks to Odoo directly.
//
// A step the catalog cannot carry out is UNSUPPORTED, with the reason, rather than worked around:
// such a step is a gap in the catalog, and the plan is how a gap is found.
package spec

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// APIVersion and Kind identify a business spec document.
const (
	APIVersion = "asao.business/v0"
	Kind       = "BusinessSpec"
)

// Spec is a business's desired configuration in its provider.
type Spec struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	// Modules are the provider modules the business needs, installed in this order.
	Modules []string `json:"modules,omitempty" yaml:"modules,omitempty"`
	// Settings are the business's choices on Odoo's Settings page, by field (group_use_lead: true):
	// applied after the modules, whose settings they are, and before the records.
	Settings map[string]interface{} `json:"settings,omitempty" yaml:"settings,omitempty"`
	// Resources are the records the business keeps, applied in this order: a record another one
	// refers to comes first.
	Resources []Resource `json:"resources" yaml:"resources"`
}

// Metadata names a spec. Placeholders lists the values that are not facts yet (a rehearsal's
// invented tax code), so a reviewer never mistakes one for the business's own.
type Metadata struct {
	Name         string   `json:"name" yaml:"name"`
	Description  string   `json:"description,omitempty" yaml:"description,omitempty"`
	Placeholders []string `json:"placeholders,omitempty" yaml:"placeholders,omitempty"`
}

// Resource is one record the business keeps.
type Resource struct {
	// Entity is the catalog family of the record: Entity.list finds it, Entity.describe reads it,
	// Entity.create makes it and Entity.update changes it (people.department).
	Entity string `json:"entity" yaml:"entity"`
	// Find, Describe, Create, Update and Delete name those operations where the family is irregular
	// (sales.quotation.create, sales.order.describe).
	Find     string `json:"find,omitempty" yaml:"find,omitempty"`
	Describe string `json:"describe,omitempty" yaml:"describe,omitempty"`
	Create   string `json:"create,omitempty" yaml:"create,omitempty"`
	Update   string `json:"update,omitempty" yaml:"update,omitempty"`
	Delete   string `json:"delete,omitempty" yaml:"delete,omitempty"`
	// Absent says the business does not keep the record: when it exists, apply deletes it through
	// Entity.delete, a guarded delete that is refused while other records point at it. The record is
	// named by match or by values.name, and has no other values.
	Absent bool `json:"absent,omitempty" yaml:"absent,omitempty"`
	// Match are the provider field values that identify the record; by default its name. Select
	// "first" takes the first record found instead, for a record the business has exactly one of
	// (the company of a single-company database).
	Match  map[string]interface{} `json:"match,omitempty" yaml:"match,omitempty"`
	Select string                 `json:"select,omitempty" yaml:"select,omitempty"`
	// Values are the record's desired values, by the operation's input names.
	Values map[string]interface{} `json:"values" yaml:"values"`
	// Note says why, for the reviewer.
	Note string `json:"note,omitempty" yaml:"note,omitempty"`
}

// SelectFirst takes the first record found.
const SelectFirst = "first"

// Validate refuses a spec this package cannot plan.
func (s Spec) Validate() error {
	if s.APIVersion != APIVersion || s.Kind != Kind {
		return fmt.Errorf("spec: want apiVersion %q and kind %q, got %q and %q", APIVersion, Kind, s.APIVersion, s.Kind)
	}
	if strings.TrimSpace(s.Metadata.Name) == "" {
		return fmt.Errorf("spec: metadata.name is required")
	}
	for field, value := range s.Settings {
		if strings.TrimSpace(field) == "" || value == nil {
			return fmt.Errorf("spec: settings.%s: a setting is a field name and a value", field)
		}
	}
	for i, r := range s.Resources {
		where := fmt.Sprintf("spec: resources[%d] (%s)", i, r.Entity)
		switch {
		case r.Absent && strings.TrimSpace(r.Entity) == "" && (r.Find == "" || r.Delete == ""):
			return fmt.Errorf("%s: an entity, or find and delete operations, are required", where)
		case !r.Absent && strings.TrimSpace(r.Entity) == "" && (r.Find == "" || (r.Create == "" && r.Update == "")):
			return fmt.Errorf("%s: an entity, or find and create or update operations, are required", where)
		case r.Absent && r.Select != "":
			return fmt.Errorf("%s: an absent record is named by match or name, not by select", where)
		case r.Absent && len(r.Values) > 0 && (len(r.Match) > 0 || len(r.Values) > 1 || r.Values["name"] == nil):
			return fmt.Errorf("%s: an absent record has no values to set: name it with match or values.name alone", where)
		case len(r.Values) == 0 && !r.Absent:
			return fmt.Errorf("%s: values are required", where)
		case r.Select != "" && r.Select != SelectFirst:
			return fmt.Errorf("%s: select %q (want %q or nothing)", where, r.Select, SelectFirst)
		case r.Select == "" && len(r.matchFields()) == 0:
			return fmt.Errorf("%s: no match and no name to identify the record by", where)
		}
	}
	return nil
}

// operations are the resource's find, describe, create and update operation ids.
func (r Resource) operations() (find, describe, create, update string) {
	return r.operation(r.Find, "list"), r.operation(r.Describe, "describe"), r.operation(r.Create, "create"), r.operation(r.Update, "update")
}

// operation is the explicit operation id, or the entity's own for verb.
func (r Resource) operation(explicit, verb string) string {
	if explicit != "" {
		return explicit
	}
	if r.Entity == "" {
		return ""
	}
	return r.Entity + "." + verb
}

// matchFields are the provider field values that identify the record.
func (r Resource) matchFields() map[string]interface{} {
	if len(r.Match) > 0 {
		return r.Match
	}
	if name, ok := r.Values["name"]; ok {
		return map[string]interface{}{"name": name}
	}
	return nil
}

// key describes the record for a human: "name=Kỹ thuật", or "first".
func (r Resource) key() string {
	if r.Select == SelectFirst {
		return SelectFirst
	}
	fields := r.matchFields()
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%v", name, fields[name]))
	}
	return strings.Join(parts, ",")
}

// domain is the provider domain that finds the record.
func (r Resource) domain() []interface{} {
	fields := r.Match
	if r.Select != SelectFirst {
		fields = r.matchFields()
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	domain := make([]interface{}, 0, len(names))
	for _, name := range names {
		domain = append(domain, []interface{}{name, "=", fields[name]})
	}
	return domain
}

// A value read from a file next to the spec instead of written in it: {file: path} is the file's
// text (a page's QWeb), {file: path, encoding: base64} its bytes in base64 (a logo). Paths are
// relative to the spec, so a spec and the files it uses move together.
const (
	fileKey        = "file"
	encodingKey    = "encoding"
	encodingBase64 = "base64"
)

// ResolveFiles returns the spec with every file value replaced by its content, read through read
// (relative path in, bytes out). It refuses an absolute path and an unknown encoding.
func ResolveFiles(s Spec, read func(path string) ([]byte, error)) (Spec, error) {
	resolved := s
	resolved.Resources = make([]Resource, len(s.Resources))
	for i, r := range s.Resources {
		values := make(map[string]interface{}, len(r.Values))
		for field, value := range r.Values {
			ref, ok := value.(map[string]interface{})
			if !ok || ref[fileKey] == nil {
				values[field] = value
				continue
			}
			path, _ := ref[fileKey].(string)
			if path == "" || strings.HasPrefix(path, "/") {
				return Spec{}, fmt.Errorf("spec: resources[%d].values.%s: a file is a path relative to the spec, got %q", i, field, path)
			}
			for key := range ref {
				if key != fileKey && key != encodingKey {
					return Spec{}, fmt.Errorf("spec: resources[%d].values.%s: unknown file option %q", i, field, key)
				}
			}
			data, err := read(path)
			if err != nil {
				return Spec{}, fmt.Errorf("spec: resources[%d].values.%s: %w", i, field, err)
			}
			switch encoding, _ := ref[encodingKey].(string); encoding {
			case "":
				values[field] = string(data)
			case encodingBase64:
				values[field] = base64.StdEncoding.EncodeToString(data)
			default:
				return Spec{}, fmt.Errorf("spec: resources[%d].values.%s: encoding %q (want base64 or none)", i, field, encoding)
			}
		}
		r.Values = values
		resolved.Resources[i] = r
	}
	return resolved, nil
}
