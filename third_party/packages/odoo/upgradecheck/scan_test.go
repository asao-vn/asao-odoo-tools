package upgradecheck

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"
)

// registrySource is a source read from a registry dump (scripts/dev/odoo-upgrade-check/dump_registry.py).
type registrySource struct {
	models map[string]registryModel
	cache  map[string]map[string]Field
}

type registryModel struct {
	Fields  map[string]registryField `json:"fields"`
	Modules []string                 `json:"modules"`
}

type registryField struct {
	Field
	Modules []string `json:"modules"`
}

func (r *registrySource) Fields(model string) (map[string]Field, bool) {
	entry, ok := r.models[model]
	if !ok || entry.Fields == nil {
		return nil, false
	}
	if fields, cached := r.cache[model]; cached {
		return fields, true
	}
	fields := make(map[string]Field, len(entry.Fields))
	for name, field := range entry.Fields {
		fields[name] = field.Field
	}
	r.cache[model] = fields
	return fields, true
}

func (r *registrySource) Modules(model, field string) []string {
	entry := r.models[model]
	if field == "" {
		return entry.Modules
	}
	return entry.Fields[field].Modules
}

func readJSON(t *testing.T, path string, into interface{}) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		reader = gz
	}
	if err := json.NewDecoder(reader).Decode(into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func researchSource(t *testing.T) *registrySource {
	var registry struct {
		Models map[string]registryModel `json:"models"`
	}
	readJSON(t, "testdata/registry-19.0.json.gz", &registry)
	return &registrySource{models: registry.Models, cache: map[string]map[string]Field{}}
}

// researchCustomizations reads a dump_custom.py document.
func researchCustomizations(t *testing.T) Customizations {
	var dump map[string]json.RawMessage
	readJSON(t, "testdata/customizations-19.0.json", &dump)
	custom := Customizations{Records: map[string][]map[string]interface{}{}}
	for key, raw := range dump {
		switch key {
		case "model_ids":
			if err := json.Unmarshal(raw, &custom.ModelIDs); err != nil {
				t.Fatal(err)
			}
		case "field_ids":
			if err := json.Unmarshal(raw, &custom.FieldIDs); err != nil {
				t.Fatal(err)
			}
		default:
			var rows []map[string]interface{}
			if err := json.Unmarshal(raw, &rows); err != nil {
				t.Fatalf("%s: %v", key, err)
			}
			custom.Records[key] = rows
		}
	}
	return custom
}

func loadPair(t *testing.T) *Pair {
	t.Helper()
	pair, err := Load("19.0", "20.0")
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

// The Go check finds, on the customizations planted for the research, exactly what the research's
// scanner (scripts/dev/odoo-upgrade-check/scan.py, run with this map) found: 54 findings over 62
// planted customizations and 55 made by the lab itself.
func TestScanAgreesWithTheResearchScanner(t *testing.T) {
	report := Scan(researchSource(t), researchCustomizations(t), loadPair(t))
	var reference []map[string]interface{}
	readJSON(t, "testdata/findings-19.0-20.0.json", &reference)
	// scan.py names a container's customizations by their key; the check by their kind.
	labels := map[string]string{"rules": "record rule", "access rights": "access right"}
	canonical := func(kind string, id int64, path, problem, severityName, confidence string, at []string, to, values, was, now, replaced string) string {
		return fmt.Sprintf("%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s", kind, id, path, problem, severityName, confidence, strings.Join(at, "."), to, values, was, now, replaced)
	}
	want := map[string]int{}
	for _, f := range reference {
		kind := text(f["kind"])
		if label, ok := labels[kind]; ok {
			kind = label
		}
		var at []string
		for _, part := range list(f["at"]) {
			if part != nil {
				at = append(at, text(part))
			}
		}
		var values []string
		for _, value := range list(f["values"]) {
			values = append(values, text(value))
		}
		want[canonical(kind, integer(f["id"]), text(f["path"]), text(f["problem"]), text(f["severity"]), text(f["confidence"]), at,
			text(f["to"]), strings.Join(values, ","), text(f["was"]), text(f["now"]), text(f["replacedBy"]))]++
	}
	got := map[string]int{}
	for _, f := range report.Findings {
		var at []string
		if f.At != nil {
			at = append(at, f.At.Model)
			if f.At.Field != "" {
				at = append(at, f.At.Field)
			}
		}
		got[canonical(f.Kind, f.ID, f.Path, f.Problem, f.Severity, f.Confidence, at, f.To, strings.Join(f.Values, ","), f.Was, f.Now, f.ReplacedBy)]++
	}
	var missing, extra []string
	for key, count := range want {
		if got[key] < count {
			missing = append(missing, key)
		}
	}
	for key, count := range got {
		if want[key] < count {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("the Go check and scan.py disagree:\nonly scan.py:\n  %s\nonly Go:\n  %s", strings.Join(missing, "\n  "), strings.Join(extra, "\n  "))
	}
	if len(report.Findings) != len(reference) {
		t.Fatalf("%d findings, scan.py %d", len(report.Findings), len(reference))
	}
}

// fixtureSource is a hand-made source for the behaviour the research data never exercised.
type fixtureSource struct {
	fields  map[string]map[string]Field
	modules map[string][]string // "model" or "model.field"
}

func (f fixtureSource) Fields(model string) (map[string]Field, bool) {
	fields, ok := f.fields[model]
	return fields, ok
}

func (f fixtureSource) Modules(model, field string) []string {
	if field == "" {
		return f.modules[model]
	}
	return f.modules[model+"."+field]
}

func findingsOf(report Report, name string) []Finding {
	var out []Finding
	for _, f := range report.Findings {
		if f.Name == name {
			out = append(out, f)
		}
	}
	return out
}

func problems(findings []Finding) string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Problem+" "+f.Path)
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

func TestScanKeepsCustomFieldsAndClassifiesWhatTheMapDoesNotCover(t *testing.T) {
	pair := loadPair(t)
	partner := map[string]Field{"name": {Type: "char", Store: true, Searchable: true},
		"x_tier": {Type: "selection", Store: true, Searchable: true, Selection: []string{"gold"}},
		"x_vip":  {Type: "boolean", Store: true, Searchable: true}, "planned_x": {Type: "char", Store: true, Searchable: true},
		"gone_x": {Type: "char", Store: true, Searchable: true}, "lab_x": {Type: "char", Store: true, Searchable: true}}
	source := fixtureSource{
		fields: map[string]map[string]Field{
			"res.partner":     partner,
			"x_studio_box":    {"x_name": {Type: "char", Store: true, Searchable: true}},
			"helpdesk.ticket": {"name": {Type: "char", Store: true, Searchable: true}},
			"ir.filters":      {}, "ir.model.fields": {},
		},
		modules: map[string][]string{
			"res.partner.planned_x": {"helpdesk"},      // an Enterprise module
			"res.partner.gone_x":    {"base_vat"},      // a 19.0 module 20.0 does not have
			"res.partner.lab_x":     {"website_forum"}, // on 20.0, not installed in its lab
			"helpdesk.ticket":       {"helpdesk"},
		},
	}
	custom := Customizations{
		Records: map[string][]map[string]interface{}{
			"custom_fields": {
				{"id": 1.0, "name": "x_vip", "model": "res.partner", "ttype": "boolean"},
				{"id": 2.0, "name": "x_tier", "model": "res.partner", "ttype": "selection"},
				{"id": 3.0, "name": "x_name", "model": "x_studio_box", "ttype": "char"},
			},
			"filters": {
				{"id": 10.0, "name": "custom fields", "model_id": "res.partner", "domain": "[('x_vip', '=', True), ('x_tier', '=', 'gold')]"},
				{"id": 11.0, "name": "custom model", "model_id": "x_studio_box", "domain": "[('x_name', '!=', False)]"},
				{"id": 12.0, "name": "enterprise field", "model_id": "res.partner", "domain": "[('planned_x', '=', 'a')]"},
				{"id": 13.0, "name": "removed module", "model_id": "res.partner", "domain": "[('gone_x', '=', 'a')]"},
				{"id": 14.0, "name": "lab gap", "model_id": "res.partner", "domain": "[('lab_x', '=', 'a')]"},
				{"id": 15.0, "name": "enterprise model", "model_id": "helpdesk.ticket", "domain": "[]"},
			},
		},
		CustomModels: []string{"x_studio_box"},
	}
	report := Scan(source, custom, pair)
	for name, want := range map[string]string{
		"custom fields":    "",
		"custom model":     "",
		"enterprise field": "NOT_COVERED planned_x",
		"removed module":   "MODULE_REMOVED gone_x",
		"lab gap":          "NOT_IN_TARGET_LAB lab_x",
		"enterprise model": "NOT_COVERED helpdesk.ticket",
	} {
		if got := problems(findingsOf(report, name)); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	for _, f := range findingsOf(report, "removed module") {
		if strings.Join(f.Modules, ",") != "base_vat" {
			t.Errorf("removed module names %v", f.Modules)
		}
	}
}

func TestScanNeverQuotesWhatItCannotRead(t *testing.T) {
	source := fixtureSource{fields: map[string]map[string]Field{"res.partner": {"name": {Type: "char", Store: true, Searchable: true}}}}
	secret := "api_key = 'sk-live-0123' +"
	custom := Customizations{Records: map[string][]map[string]interface{}{
		"server_actions": {{"id": 1.0, "name": "broken", "model_id": "res.partner", "state": "code", "code": secret}},
		"filters":        {{"id": 2.0, "name": "broken domain", "model_id": "res.partner", "domain": "[('name', '=', 'sk-live-0123'"}},
	}}
	report := Scan(source, custom, loadPair(t))
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "sk-live") {
		t.Fatalf("the report quotes what it could not read: %s", encoded)
	}
	if got := problems(report.Findings); got != "UNPARSABLE (code); UNPARSABLE (domain)" {
		t.Fatalf("got %q", got)
	}
}

func TestScanReadsTheLocatorsOfAnInheritingView(t *testing.T) {
	pair := loadPair(t)
	journal := map[string]Field{"bank_id": {Type: "many2one", Relation: "res.bank"}, "code": {Type: "char", Store: true, Searchable: true},
		"name": {Type: "char", Store: true, Searchable: true}}
	source := fixtureSource{fields: map[string]map[string]Field{"account.journal": journal, "res.bank": {"name": {Type: "char"}}}}
	custom := Customizations{Records: map[string][]map[string]interface{}{"views": {
		{"id": 1.0, "name": "root locator", "model": "account.journal", "arch_db": `<field name="bank_id" position="after"><field name="code"/></field>`},
		{"id": 2.0, "name": "attribute modifier", "model": "account.journal",
			"arch_db": `<xpath expr="//field[@name='name']" position="attributes"><attribute name="invisible">not bank_id</attribute></xpath>`},
	}}}
	report := Scan(source, custom, pair)
	if got := problems(findingsOf(report, "root locator")); got != "FIELD_MISSING bank_id" {
		t.Errorf("root locator: %q", got)
	}
	if got := problems(findingsOf(report, "attribute modifier")); got != "FIELD_MISSING bank_id" {
		t.Errorf("attribute modifier: %q", got)
	}
}

func TestPairDescribesItsLabs(t *testing.T) {
	infos, err := Pairs()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].From != "19.0" || infos[0].To != "20.0" {
		t.Fatalf("pairs: %+v", infos)
	}
	info := infos[0]
	if info.SourceVersion != "19.0-20260803" || info.TargetVersion != "20.0-20260926" || info.Changes["MODEL_REMOVED"] != 44 {
		t.Fatalf("info: %+v", info)
	}
	pair := loadPair(t)
	if !pair.Enterprise("helpdesk") || pair.Enterprise("sale") {
		t.Fatal("the 19.0 Enterprise modules are not the map's")
	}
	if _, err := Load("18.0", "19.0"); err == nil || !strings.Contains(err.Error(), "19.0 -> 20.0") {
		t.Fatalf("an unknown pair: %v", err)
	}
}
