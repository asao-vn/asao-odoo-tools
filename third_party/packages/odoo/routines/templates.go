package routines

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The starter routines, by business area and cadence: what Odoo is asked and what it answers. Every
// operation they name is one the catalog verifies on Odoo 19. "routines init" writes the chosen ones
// into a routines file, where they are the owner's to change.
//
//go:embed templates.json
var templatesJSON []byte

// Template is a starter routine, in both languages.
type Template struct {
	ID      string                   `json:"id"`
	Area    string                   `json:"area"`
	Every   string                   `json:"every"`
	On      interface{}              `json:"on,omitempty"`
	At      string                   `json:"at"`
	Writes  bool                     `json:"writes,omitempty"`
	Agent   interface{}              `json:"agent,omitempty"`
	Title   map[string]string        `json:"title"`
	Does    map[string]string        `json:"does"`
	Returns map[string]string        `json:"returns"`
	Prompt  map[string]string        `json:"prompt"`
	Steps   []map[string]interface{} `json:"steps"`
}

// Areas names each business area in both languages.
var Areas = map[string]map[string]string{
	"sales":     {"vi": "Bán hàng", "en": "Sales"},
	"finance":   {"vi": "Tài chính, kế toán", "en": "Finance"},
	"inventory": {"vi": "Kho", "en": "Inventory"},
	"work":      {"vi": "Công việc", "en": "Work"},
	"crm":       {"vi": "Khách hàng tiềm năng", "en": "CRM"},
}

// Templates are the starter routines, read fresh so a caller may change its copy.
func Templates() []Template {
	decoder := json.NewDecoder(bytes.NewReader(templatesJSON))
	decoder.UseNumber()
	var out []Template
	if err := decoder.Decode(&out); err != nil {
		panic("routines: templates.json: " + err.Error())
	}
	return out
}

// TemplateByID finds a starter routine.
func TemplateByID(id string) (Template, error) {
	templates := Templates()
	ids := make([]string, 0, len(templates))
	for _, template := range templates {
		if template.ID == id {
			return template, nil
		}
		ids = append(ids, template.ID)
	}
	return Template{}, invalid("no template %q; there are: %s", id, strings.Join(ids, ", "))
}

// AsRoutine is a template as a routine of a routines file, in the chosen language.
func (t Template) AsRoutine(language string) map[string]interface{} {
	routine := map[string]interface{}{"id": t.ID, "title": t.Title[language], "area": t.Area, "every": t.Every, "at": t.At}
	if t.On != nil {
		routine["on"] = t.On
	}
	if t.Writes {
		routine["writes"] = true
	}
	if t.Agent != nil {
		routine["agent"] = t.Agent
	}
	if prompt := t.Prompt[language]; prompt != "" {
		routine["prompt"] = prompt
	}
	steps := make([]interface{}, 0, len(t.Steps))
	for _, step := range t.Steps {
		steps = append(steps, step)
	}
	routine["step"] = steps
	return routine
}

// ---- writing a routines file as TOML ----------------------------------------------------------
//
// The keys are written in the order a person reads them, not alphabetically.

var (
	fileKeys    = []string{"profile", "organization", "connection", "output", "agent", "language", "agent_timeout", "allow_unverified"}
	routineKeys = []string{"id", "title", "area", "every", "at", "on", "writes", "enabled", "agent", "prompt"}
	stepKeys    = []string{"name", "read", "group", "review", "ensure_task", "execute", "period", "by", "aggregate", "order", "limit", "id", "filter", "values", "note"}
	tableKeys   = []string{"name", "project", "recurring", "repeat_every", "repeat_unit", "due_on"}
)

// ordered lists a table's keys: the preferred ones first, the rest alphabetically, skipping skip.
func ordered(table map[string]interface{}, preferred []string, skip string) []string {
	var keys []string
	for _, key := range preferred {
		if _, ok := table[key]; ok {
			keys = append(keys, key)
		}
	}
	var rest []string
	for key := range table {
		if key != skip && indexOf(preferred, key) < 0 {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// TOML writes a routines file: its settings, then [[routine]] with each routine's [[routine.step]].
func TOML(data map[string]interface{}, header string) (string, error) {
	lines := []string{"# " + header}
	write := func(indent string, table map[string]interface{}, keys []string) error {
		for _, key := range keys {
			if table[key] == nil {
				continue
			}
			value, err := tomlValue(table[key])
			if err != nil {
				return fmt.Errorf("routines: %s: %w", key, err)
			}
			lines = append(lines, indent+tomlKey(key)+" = "+value)
		}
		return nil
	}
	if err := write("", data, ordered(data, fileKeys, "routine")); err != nil {
		return "", err
	}
	for _, routine := range tables(data["routine"]) {
		lines = append(lines, "", "[[routine]]")
		if err := write("", routine, ordered(routine, routineKeys, "step")); err != nil {
			return "", err
		}
		for _, step := range tables(routine["step"]) {
			lines = append(lines, "", "  [[routine.step]]")
			if err := write("  ", step, ordered(step, stepKeys, "")); err != nil {
				return "", err
			}
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func tomlKey(key string) string {
	for _, r := range key {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			quoted, _ := encodeJSON(key, false)
			return strings.TrimSpace(string(quoted))
		}
	}
	return key
}

func tomlValue(value interface{}) (string, error) {
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed), nil
	case string:
		quoted, err := encodeJSON(typed, false)
		return strings.TrimSpace(string(quoted)), err
	case json.Number:
		return typed.String(), nil
	case int, int64, int32:
		whole, _ := integer(typed)
		return strconv.FormatInt(whole, 10), nil
	case float64:
		written := strconv.FormatFloat(typed, 'g', -1, 64)
		if !strings.ContainsAny(written, ".eE") {
			written += ".0"
		}
		return written, nil
	case map[string]interface{}:
		parts := make([]string, 0, len(typed))
		for _, key := range ordered(typed, tableKeys, "") {
			if typed[key] == nil {
				continue
			}
			item, err := tomlValue(typed[key])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKey(key)+" = "+item)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}
	items, isList := asList(value)
	if !isList {
		return "", fmt.Errorf("cannot write %T as TOML", value)
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		written, err := tomlValue(item)
		if err != nil {
			return "", err
		}
		parts = append(parts, written)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}
