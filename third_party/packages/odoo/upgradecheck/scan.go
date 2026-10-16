package upgradecheck

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Field is what a check knows of a field, on either side of an upgrade.
type Field struct {
	Type       string   `json:"type"`
	Relation   string   `json:"relation,omitempty"`
	Store      bool     `json:"store,omitempty"`
	Searchable bool     `json:"searchable,omitempty"`
	Selection  []string `json:"selection,omitempty"`
}

// Source is the database to be upgraded, read as the check goes.
type Source interface {
	// Fields are the model's fields, and false when the source has no such model.
	Fields(model string) (map[string]Field, bool)
	// Modules are the modules that define the model (field "") or one of its fields.
	Modules(model, field string) []string
}

// Customizations are what people made in the source database: the rows search_read returns, by
// kind (see Kinds), with the names behind the ir.model and ir.model.fields ids those rows hold.
type Customizations struct {
	Records map[string][]map[string]interface{} `json:"records"`
	// ModelIDs names the model of each ir.model id; FieldIDs the model and name of each field id.
	ModelIDs map[string]string    `json:"modelIds"`
	FieldIDs map[string][2]string `json:"fieldIds"`
	// CustomModels are the models people made (ir.model state manual), which an upgrade keeps.
	CustomModels []string `json:"customModels,omitempty"`
}

// Place is the model, and the field when there is one, where a reference stops resolving.
type Place struct {
	Model string `json:"model"`
	Field string `json:"field,omitempty"`
}

// Finding is one reference of one customization that the target series does not resolve as the
// source does.
type Finding struct {
	Kind       string   `json:"kind"`
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Model      string   `json:"model,omitempty"`
	Path       string   `json:"path"`
	Problem    string   `json:"problem"`
	Severity   string   `json:"severity"`
	Confidence string   `json:"confidence"`
	At         *Place   `json:"at,omitempty"`
	To         string   `json:"to,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Values     []string `json:"values,omitempty"`
	Was        string   `json:"was,omitempty"`
	Now        string   `json:"now,omitempty"`
	ReplacedBy string   `json:"replacedBy,omitempty"`
	Modules    []string `json:"modules,omitempty"`
}

// Report is what one check found.
type Report struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Findings   []Finding      `json:"findings"`
	BySeverity map[string]int `json:"bySeverity"`
	Scanned    map[string]int `json:"scanned"`
}

// Severities, most urgent first. BREAKS and RENAME stop working on the target (RENAME names the
// field's probable new name); CHECK still runs but may mean something else; REVIEW could not be
// read; UNKNOWN is outside what the map covers; INFO was already broken on the source.
var Severities = []string{"BREAKS", "RENAME", "CHECK", "REVIEW", "UNKNOWN", "INFO"}

var severity = map[string]string{
	"FIELD_MISSING": "BREAKS", "MODEL_MISSING": "BREAKS", "NOT_RELATIONAL": "BREAKS", "RELATION_CHANGED": "BREAKS",
	"NO_LONGER_SEARCHABLE": "BREAKS", "NO_LONGER_STORED": "BREAKS", "DEFAULT_VALUE_REMOVED": "BREAKS",
	"RELATED_TYPE_CHANGED": "BREAKS", "CONTAINER_REPLACED": "BREAKS", "MODULE_REMOVED": "BREAKS",
	"FIELD_RENAMED":           "RENAME",
	"SELECTION_VALUE_REMOVED": "CHECK", "TYPE_CHANGED": "CHECK", "ORDER_NOT_STORED": "CHECK",
	"UNPARSABLE":        "REVIEW",
	"NOT_IN_TARGET_LAB": "UNKNOWN", "NOT_COVERED": "UNKNOWN",
	"ALREADY_BROKEN_ON_SOURCE": "INFO",
}

// Kinds are the customizations a check reads, each with the model that holds it.
var Kinds = []struct{ Key, Model, Label string }{
	{"filters", "ir.filters", "saved filter"},
	{"automations", "base.automation", "automation rule"},
	{"server_actions", "ir.actions.server", "server action"},
	{"custom_fields", "ir.model.fields", "custom field"},
	{"templates", "mail.template", "email template"},
	{"views", "ir.ui.view", "view"},
	{"rules", "ir.rule", "record rule"},
	{"access_rights", "ir.model.access", "access right"},
	{"exports", "ir.exports", "export template"},
	{"defaults", "ir.default", "default value"},
	{"window_actions", "ir.actions.act_window", "window action"},
}

// replacedBy names the model a target series merged a container into: Odoo 20 replaces
// ir.model.access and ir.rule with ir.access.
var replacedBy = map[string]string{"ir.rule": "ir.access", "ir.model.access": "ir.access"}

type place struct {
	kind  string
	id    int64
	name  string
	model string
}

type scanner struct {
	source   Source
	target   *targetView
	pair     *Pair
	custom   Customizations
	findings []Finding
	seen     map[string]bool
}

// Scan checks every customization's references against the pair's target series.
func Scan(source Source, custom Customizations, pair *Pair) Report {
	s := &scanner{source: source, target: newTargetView(pair, custom), pair: pair, custom: custom, seen: map[string]bool{}}
	s.run()
	bySeverity := map[string]int{}
	for _, finding := range s.findings {
		bySeverity[finding.Severity]++
	}
	scanned := map[string]int{}
	for key, rows := range custom.Records {
		scanned[key] = len(rows)
	}
	findings := s.findings
	if findings == nil {
		findings = []Finding{}
	}
	return Report{From: pair.From, To: pair.To, Findings: findings, BySeverity: bySeverity, Scanned: scanned}
}

type detail struct {
	at         *Place
	to         string
	candidates []string
	values     []string
	was, now   string
	replacedBy string
	modules    []string
}

func (s *scanner) report(where place, path, problem, confidence string, d detail) {
	key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", where.kind, where.id, path, problem)
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	s.findings = append(s.findings, Finding{Kind: where.kind, ID: where.id, Name: where.name, Model: where.model, Path: path,
		Problem: problem, Severity: severity[problem], Confidence: confidence, At: d.at, To: d.to, Candidates: d.candidates,
		Values: d.values, Was: d.was, Now: d.now, ReplacedBy: d.replacedBy, Modules: d.modules})
}

func (s *scanner) rows(key string) []map[string]interface{} { return s.custom.Records[key] }

func label(key string) string {
	for _, kind := range Kinds {
		if kind.Key == key {
			return kind.Label
		}
	}
	return strings.ReplaceAll(key, "_", " ")
}

func (s *scanner) where(key string, record map[string]interface{}, model string) place {
	name := text(record["name"])
	if name == "" {
		name = text(record["display_name"])
	}
	return place{kind: label(key), id: integer(record["id"]), name: name, model: model}
}

// modelOf is a model reference as read: a model name, or an ir.model many2one [id, label].
func (s *scanner) modelOf(value interface{}) string {
	if pair, ok := value.([]interface{}); ok && len(pair) > 0 {
		return s.custom.ModelIDs[idString(pair[0])]
	}
	return text(value)
}

func (s *scanner) fieldOf(value interface{}) (string, string, bool) {
	id := value
	if pair, ok := value.([]interface{}); ok && len(pair) > 0 {
		id = pair[0]
	}
	named, ok := s.custom.FieldIDs[idString(id)]
	return named[0], named[1], ok
}

// --- what the target has ------------------------------------------------------------------------

// targetView is the pair's target registry with what an upgrade carries over from the source: the
// models and fields people made.
type targetView struct {
	pair    *Pair
	overlay map[string]map[string]Field
	custom  map[string]bool
}

func newTargetView(pair *Pair, custom Customizations) *targetView {
	view := &targetView{pair: pair, overlay: map[string]map[string]Field{}, custom: map[string]bool{}}
	for _, model := range custom.CustomModels {
		view.custom[model] = true
	}
	for _, record := range custom.Records["custom_fields"] {
		model, name := text(record["model"]), text(record["name"])
		if model == "" || name == "" {
			continue
		}
		if _, exists := pair.Target[model]; !exists && !view.custom[model] {
			continue
		}
		if view.overlay[model] == nil {
			view.overlay[model] = map[string]Field{}
		}
		view.overlay[model][name] = Field{Type: text(record["ttype"]), Relation: text(record["relation"]), Store: true, Searchable: true}
	}
	return view
}

func (t *targetView) fields(model string) (map[string]Field, bool) {
	entry, exists := t.pair.Target[model]
	if !exists && !t.custom[model] {
		return nil, false
	}
	extra := t.overlay[model]
	if len(extra) == 0 {
		return entry.Fields, true
	}
	merged := make(map[string]Field, len(entry.Fields)+len(extra))
	for name, field := range entry.Fields {
		merged[name] = field
	}
	for name, field := range extra {
		merged[name] = field
	}
	return merged, true
}

type resolution struct {
	ok      bool
	problem string
	model   string
	field   string
	info    Field
}

func resolve(fields func(string) (map[string]Field, bool), model string, parts []string) resolution {
	current := model
	var info Field
	for index, part := range parts {
		entry, exists := fields(current)
		if !exists {
			return resolution{problem: "MODEL_MISSING", model: current}
		}
		found, has := entry[part]
		if !has {
			return resolution{problem: "FIELD_MISSING", model: current, field: part}
		}
		info = found
		if index < len(parts)-1 {
			if found.Relation == "" {
				return resolution{problem: "NOT_RELATIONAL", model: current, field: part}
			}
			current = found.Relation
		}
	}
	last := ""
	if len(parts) > 0 {
		last = parts[len(parts)-1]
	}
	return resolution{ok: true, model: current, field: last, info: info}
}

func (s *scanner) onSource(model string) bool {
	_, exists := s.source.Fields(model)
	return exists
}

// missing says why a model, or a model's field, that the source has is not on the target: what the
// map knows of it, or else what the modules that define it say.
func (s *scanner) missing(problem, model, field string) (string, detail) {
	at := &Place{Model: model, Field: field}
	if field == "" {
		if s.pair.labGaps[[2]string{model, ""}] {
			return "NOT_IN_TARGET_LAB", detail{at: at}
		}
		if s.pair.removedModels[model] {
			return problem, detail{at: at}
		}
	} else {
		key := [2]string{model, field}
		if s.pair.labGaps[key] || s.pair.labGaps[[2]string{model, ""}] {
			return "NOT_IN_TARGET_LAB", detail{at: at}
		}
		if to := s.pair.renamed[key]; to != "" && problem == "FIELD_MISSING" {
			return "FIELD_RENAMED", detail{at: at, to: to}
		}
		if change, known := s.pair.removedFields[key]; known {
			return problem, detail{at: at, candidates: change.Candidates}
		}
	}
	if problem == "NOT_RELATIONAL" {
		return problem, detail{at: at}
	}
	// Not in the map: the field or model is not in the source lab, so its modules decide.
	var installed, available, gone, uncovered []string
	for _, module := range s.source.Modules(model, field) {
		state, onTarget := s.pair.TargetLab.Modules[module]
		_, inSourceLab := s.pair.SourceLab.Modules[module]
		switch {
		case onTarget && state == "installed":
			installed = append(installed, module)
		// The Community image lists some Enterprise modules as uninstallable stubs: no lab covers them.
		case s.pair.Enterprise(module) || state == "uninstallable":
			uncovered = append(uncovered, module)
		case onTarget:
			available = append(available, module)
		case inSourceLab:
			gone = append(gone, module)
		default:
			uncovered = append(uncovered, module)
		}
	}
	switch {
	case len(installed) > 0:
		return problem, detail{at: at}
	case len(available) > 0:
		return "NOT_IN_TARGET_LAB", detail{at: at, modules: available}
	case len(uncovered) > 0:
		return "NOT_COVERED", detail{at: at, modules: uncovered}
	case len(gone) > 0:
		return "MODULE_REMOVED", detail{at: at, modules: gone}
	}
	return problem, detail{at: at}
}

// modelGone reports a customization whose model the source has and the target has not.
func (s *scanner) modelGone(where place, model string) bool {
	if model == "" || !s.onSource(model) {
		return false
	}
	if _, exists := s.target.fields(model); exists {
		return false
	}
	problem, d := s.missing("MODEL_MISSING", model, "")
	s.report(where, model, problem, "CERTAIN", d)
	return true
}

// checkPath resolves a dotted path from model on the source, then on the target. usage is domain,
// groupby, order, read, write, related, default, view, template, code or export.
func (s *scanner) checkPath(where place, model, path, usage, confidence string, values []interface{}, separator string) {
	var parts []string
	for _, part := range strings.Split(path, separator) {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) > 0 && usage == "export" && (parts[len(parts)-1] == "id" || parts[len(parts)-1] == ".id") {
		parts = parts[:len(parts)-1]
	}
	if model == "" || len(parts) == 0 {
		return
	}
	before := resolve(s.source.Fields, model, parts)
	if !before.ok {
		if confidence == "CERTAIN" {
			s.report(where, path, "ALREADY_BROKEN_ON_SOURCE", confidence, detail{at: &Place{Model: before.model, Field: before.field}})
		}
		return // in code or a template, a name that is no field on the source is a method or a variable
	}
	after := resolve(s.target.fields, model, parts)
	if !after.ok {
		problem, d := s.missing(after.problem, after.model, after.field)
		s.report(where, path, problem, confidence, d)
		return
	}
	at := &Place{Model: after.model, Field: after.field}
	old, now := before.info, after.info
	key := [2]string{after.model, after.field}
	if old.Relation != now.Relation {
		s.report(where, path, "RELATION_CHANGED", confidence, detail{at: at, was: old.Relation, now: now.Relation})
	} else if old.Type != now.Type && usage != "related" {
		s.report(where, path, "TYPE_CHANGED", confidence, detail{at: at, was: old.Type, now: now.Type})
	}
	if usage == "domain" && s.pair.unsearchable[key] {
		s.report(where, path, "NO_LONGER_SEARCHABLE", confidence, detail{at: at})
	}
	if usage == "groupby" && s.pair.unstored[key] {
		s.report(where, path, "NO_LONGER_STORED", confidence, detail{at: at})
	}
	if usage == "order" && s.pair.unstored[key] {
		s.report(where, path, "ORDER_NOT_STORED", confidence, detail{at: at})
	}
	if dropped := s.pair.dropped[key]; len(dropped) > 0 {
		var gone []string
		for _, value := range values {
			if value, isString := value.(string); isString && dropped[value] && !contains(gone, value) {
				gone = append(gone, value)
			}
		}
		if len(gone) > 0 {
			sort.Strings(gone)
			problem := "SELECTION_VALUE_REMOVED"
			if usage == "default" {
				problem = "DEFAULT_VALUE_REMOVED"
			}
			s.report(where, path, problem, confidence, detail{at: at, values: gone})
		}
	}
	if usage == "related" && now.Type != old.Type {
		s.report(where, path, "RELATED_TYPE_CHANGED", confidence, detail{at: at, was: old.Type, now: now.Type})
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

// --- domains and contexts ------------------------------------------------------------------------

// unparsable reports text that could not be read. It names what was unreadable and never quotes
// it: code and templates can hold credentials.
func (s *scanner) unparsable(where place, what string) {
	s.report(where, "("+what+")", "UNPARSABLE", "REVIEW", detail{})
}

func (s *scanner) checkDomain(where place, model string, value interface{}, confidence string) {
	domain := strings.TrimSpace(text(value))
	if domain == "" {
		return
	}
	node, err := parseExpression(domain)
	if err != nil {
		s.unparsable(where, "domain")
		return
	}
	s.checkDomainNode(where, model, node, confidence)
}

func literalValues(n *node) []interface{} {
	if n == nil {
		return nil
	}
	if n.kind == nConst {
		return []interface{}{n.value}
	}
	if n.kind == nList || n.kind == nTuple {
		values := make([]interface{}, 0, len(n.items))
		for _, item := range n.items {
			if item.kind != nConst {
				return nil
			}
			values = append(values, item.value)
		}
		return values
	}
	return nil
}

func constString(n *node) (string, bool) {
	if n == nil || n.kind != nConst {
		return "", false
	}
	value, ok := n.value.(string)
	return value, ok
}

func (s *scanner) checkDomainNode(where place, model string, n *node, confidence string) {
	if n == nil || (n.kind != nList && n.kind != nTuple) {
		return
	}
	if len(n.items) == 3 {
		path, isPath := constString(n.items[0])
		operator, isOperator := constString(n.items[1])
		if isPath && isOperator {
			value := n.items[2]
			s.checkPath(where, model, path, "domain", confidence, literalValues(value), ".")
			if (operator == "any" || operator == "not any") && (value.kind == nList || value.kind == nTuple) {
				resolved := resolve(s.source.Fields, model, strings.Split(path, "."))
				if resolved.ok && resolved.info.Relation != "" {
					s.checkDomainNode(where, resolved.info.Relation, value, confidence)
				}
			}
			return
		}
	}
	for _, item := range n.items {
		s.checkDomainNode(where, model, item, confidence)
	}
}

var groupingKeys = map[string]bool{"group_by": true, "pivot_row_groupby": true, "pivot_column_groupby": true, "graph_groupbys": true,
	"pivot_measures": true, "graph_measure": true}

func (s *scanner) checkContext(where place, model string, value interface{}) {
	context := strings.TrimSpace(text(value))
	if context == "" {
		return
	}
	n, err := parseExpression(context)
	if err != nil {
		s.unparsable(where, "context")
		return
	}
	if n.kind != nDict {
		return
	}
	for index, key := range n.keys {
		name, ok := constString(key)
		if !ok {
			continue
		}
		switch {
		case groupingKeys[name]:
			for _, item := range literalValues(n.values[index]) {
				if field, isString := item.(string); isString && !strings.HasPrefix(field, "__") {
					s.checkPath(where, model, strings.SplitN(field, ":", 2)[0], "groupby", "CERTAIN", nil, ".")
				}
			}
		case strings.HasPrefix(name, "search_default_"):
			s.checkPath(where, model, strings.TrimPrefix(name, "search_default_"), "domain", "LIKELY", nil, ".")
		case strings.HasPrefix(name, "default_"):
			s.checkPath(where, model, strings.TrimPrefix(name, "default_"), "write", "LIKELY", nil, ".")
		}
	}
}

// --- python code ----------------------------------------------------------------------------------

// chainOf is record.partner_id.country_id as [record partner_id country_id]; nil when n is not a
// plain chain of attributes from a name.
func chainOf(n *node) []string {
	var parts []string
	for n != nil && n.kind == nAttr {
		parts = append(parts, n.ident)
		n = n.left
	}
	if n == nil || n.kind != nName {
		return nil
	}
	parts = append(parts, n.ident)
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

func isEnv(chain []string) bool {
	return len(chain) == 1 && chain[0] == "env" || len(chain) == 2 && chain[1] == "env" && (chain[0] == "self" || chain[0] == "record")
}

// codeChain walks attributes from a record: it stops at the first name that is no field on the
// source (a method, id, env...) and checks the fields before it on the target. values are what
// the chain is compared with, which count only when the chain is all fields.
func (s *scanner) codeChain(where place, model string, names []string, values []interface{}) {
	current := model
	var walked []string
	for _, name := range names {
		fields, _ := s.source.Fields(current)
		field, isField := fields[name]
		if !isField {
			break
		}
		walked = append(walked, name)
		if field.Relation == "" {
			break
		}
		current = field.Relation
	}
	if len(walked) == 0 {
		return
	}
	if len(walked) != len(names) {
		values = nil
	}
	s.checkPath(where, model, strings.Join(walked, "."), "code", "LIKELY", values, ".")
}

func (s *scanner) modelFrom(n *node, records map[string]string) string {
	if chain := chainOf(n); chain != nil {
		current, bound := records[chain[0]]
		if !bound {
			return ""
		}
		for _, name := range chain[1:] {
			fields, _ := s.source.Fields(current)
			field, isField := fields[name]
			if !isField {
				return current
			}
			if field.Relation == "" {
				return ""
			}
			current = field.Relation
		}
		return current
	}
	if n.kind == nSubscript && isEnv(chainOf(n.left)) {
		if model, ok := constString(n.index); ok {
			return model
		}
	}
	if n.kind == nCall && n.left != nil && n.left.kind == nAttr { // sudo(), with_context(), browse()...
		return s.modelFrom(n.left.left, records)
	}
	return ""
}

func (s *scanner) checkCode(where place, model string, value interface{}, roots ...string) {
	code := text(value)
	if strings.TrimSpace(code) == "" {
		return
	}
	prog, err := parseCode(code)
	if err != nil {
		s.unparsable(where, "code")
		return
	}
	records := map[string]string{}
	for _, root := range roots {
		records[root] = model
	}
	for _, loop := range prog.loops { // loop variables over records or their x2many fields
		if found := s.modelFrom(loop.iter, records); found != "" {
			records[loop.target] = found
		}
	}
	visitAll := func(visit func(*node)) {
		for _, expr := range prog.exprs {
			walk(expr, visit)
		}
	}
	visitAll(func(n *node) { // lambda p: ... given to filtered, mapped or sorted takes the records' model
		if n.kind != nCall || n.left == nil || n.left.kind != nAttr {
			return
		}
		switch n.left.ident {
		case "filtered", "mapped", "sorted":
		default:
			return
		}
		on := s.modelFrom(n.left.left, records)
		if on == "" {
			return
		}
		arguments := append([]*node{}, n.items...)
		for _, kw := range n.keywords {
			arguments = append(arguments, kw.value)
		}
		for _, argument := range arguments {
			if argument.kind == nLambda && len(argument.params) > 0 {
				records[argument.params[0]] = on
			}
		}
	})
	visitAll(func(n *node) {
		switch n.kind {
		case nCompare: // p.state == 'in_process', record.state in ('a', 'b')
			operands := append([]*node{n.left}, n.items...)
			for index, operand := range operands {
				chain := chainOf(operand)
				if chain == nil || len(chain) < 2 {
					continue
				}
				if _, bound := records[chain[0]]; !bound {
					continue
				}
				var values []interface{}
				for other, peer := range operands {
					if other != index {
						values = append(values, literalValues(peer)...)
					}
				}
				if len(values) > 0 {
					s.codeChain(where, records[chain[0]], chain[1:], values)
				}
			}
		case nSubscript:
			if isEnv(chainOf(n.left)) {
				if name, ok := constString(n.index); ok && s.onSource(name) {
					if _, exists := s.target.fields(name); !exists {
						problem, d := s.missing("MODEL_MISSING", name, "")
						s.report(where, name, problem, "LIKELY", d)
					}
				}
			}
			if chain := chainOf(n.left); len(chain) == 1 { // record['x_field']
				if bound, ok := records[chain[0]]; ok {
					if field, isString := constString(n.index); isString {
						s.checkPath(where, bound, field, "code", "LIKELY", nil, ".")
					}
				}
			}
		case nAttr:
			if chain := chainOf(n); len(chain) > 1 {
				if bound, ok := records[chain[0]]; ok {
					s.codeChain(where, bound, chain[1:], nil)
				}
			}
		case nCall:
			if n.left == nil || n.left.kind != nAttr {
				return
			}
			on := s.modelFrom(n.left.left, records)
			if on == "" {
				return
			}
			switch method := n.left.ident; method {
			case "write", "create", "update", "new":
				if len(n.items) > 0 && n.items[0].kind == nDict {
					for _, key := range n.items[0].keys {
						if field, ok := constString(key); ok {
							s.checkPath(where, on, field, "write", "LIKELY", nil, ".")
						}
					}
				}
			case "search", "search_count", "search_read", "filtered_domain", "search_fetch":
				if len(n.items) > 0 {
					s.checkDomainNode(where, on, n.items[0], "LIKELY")
				}
			case "mapped", "filtered", "sorted":
				if len(n.items) > 0 {
					if field, ok := constString(n.items[0]); ok {
						s.checkPath(where, on, strings.SplitN(field, " ", 2)[0], "code", "LIKELY", nil, ".")
					}
				}
			}
		}
	})
}

// --- templates ------------------------------------------------------------------------------------

var qwebExpressions = map[string]bool{"t-out": true, "t-esc": true, "t-raw": true, "t-field": true, "t-if": true, "t-elif": true,
	"t-foreach": true, "t-value": true, "t-options": true}

var inline = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)

func (s *scanner) checkExpression(where place, expression string, aliases map[string]string) {
	n, err := parseExpression(expression)
	if err != nil {
		return
	}
	walk(n, func(sub *node) {
		if sub.kind != nAttr {
			return
		}
		if chain := chainOf(sub); len(chain) > 1 {
			if bound, ok := aliases[chain[0]]; ok {
				s.codeChain(where, bound, chain[1:], nil)
			}
		}
	})
}

func (s *scanner) checkTemplate(where place, model string, value interface{}) {
	body := text(value)
	if body == "" {
		return
	}
	aliases := map[string]string{"object": model}
	tags := startTags(body)
	for _, attributes := range tags { // t-foreach and t-as first, so loop names resolve to their model
		if attributes["t-foreach"] != "" && attributes["t-as"] != "" {
			if n, err := parseExpression(attributes["t-foreach"]); err == nil {
				if found := s.modelFrom(n, aliases); found != "" {
					aliases[attributes["t-as"]] = found
				}
			}
		}
	}
	for _, attributes := range tags {
		names := make([]string, 0, len(attributes))
		for name := range attributes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			attribute := attributes[name]
			switch {
			case attribute == "":
			case qwebExpressions[name] || strings.HasPrefix(name, "t-att-"):
				s.checkExpression(where, attribute, aliases)
			case strings.HasPrefix(name, "t-attf-"):
				for _, match := range inline.FindAllStringSubmatch(attribute, -1) {
					s.checkExpression(where, match[1], aliases)
				}
			}
		}
	}
	for _, match := range inline.FindAllStringSubmatch(body, -1) {
		s.checkExpression(where, match[1], aliases)
	}
}

// --- views ----------------------------------------------------------------------------------------

var subviews = map[string]bool{"list": true, "tree": true, "form": true, "kanban": true, "graph": true, "pivot": true, "calendar": true}

var xpathField = regexp.MustCompile(`field\[@name=['"](\w+)['"]\]`)

var modifierAttributes = []string{"invisible", "readonly", "required", "column_invisible"}

func (s *scanner) checkModifier(where place, model, expression string) {
	switch strings.TrimSpace(expression) {
	case "", "1", "0", "True", "False":
		return
	}
	n, err := parseExpression(expression)
	if err != nil {
		return
	}
	fields, _ := s.source.Fields(model)
	walk(n, func(sub *node) {
		if sub.kind == nName {
			if _, isField := fields[sub.ident]; isField {
				s.checkPath(where, model, sub.ident, "view", "LIKELY", nil, ".")
			}
		}
	})
}

func (s *scanner) checkXPath(where place, model, expression string) {
	for _, match := range xpathField.FindAllStringSubmatch(expression, -1) {
		s.checkPath(where, model, match[1], "view", "LIKELY", nil, ".")
	}
}

func (s *scanner) checkViewElement(where place, model string, element *xmlElement) {
	for _, child := range element.children {
		childModel := model
		if name := child.attr("name"); child.tag == "field" && name != "" {
			s.checkPath(where, model, name, "view", "CERTAIN", nil, ".")
			resolved := resolve(s.source.Fields, model, []string{name})
			if resolved.ok && resolved.info.Relation != "" {
				for _, grandchild := range child.children {
					if subviews[grandchild.tag] {
						childModel = resolved.info.Relation
						break
					}
				}
			}
		}
		if child.tag == "xpath" {
			s.checkXPath(where, model, child.attr("expr"))
		}
		if child.tag == "filter" {
			s.checkDomain(where, model, child.attr("domain"), "LIKELY")
			s.checkContext(where, model, child.attr("context"))
		}
		// <attribute name="invisible">expression</attribute> sets a modifier inside an inheriting view.
		if child.tag == "attribute" && contains(modifierAttributes, child.attr("name")) {
			s.checkModifier(where, model, child.text)
		}
		for _, attribute := range modifierAttributes {
			s.checkModifier(where, model, child.attr(attribute))
		}
		s.checkViewElement(where, childModel, child)
	}
}

func (s *scanner) checkView(where place, model, arch string) {
	root, err := parseXML(arch)
	if err != nil {
		s.unparsable(where, "arch")
		return
	}
	switch root.tag {
	case "xpath":
		s.checkXPath(where, model, root.attr("expr"))
	case "field": // an inheriting view whose root locates a field of the view it extends
		if name := root.attr("name"); name != "" && root.attr("position") != "" {
			s.checkPath(where, model, name, "view", "LIKELY", nil, ".")
		}
	}
	s.checkViewElement(where, model, root)
}

// --- the customizations ---------------------------------------------------------------------------

func (s *scanner) run() {
	for _, record := range s.rows("filters") {
		model := s.modelOf(record["model_id"])
		where := s.where("filters", record, model)
		if s.modelGone(where, model) {
			continue
		}
		s.checkDomain(where, model, record["domain"], "CERTAIN")
		s.checkContext(where, model, record["context"])
		if sort := text(record["sort"]); strings.HasPrefix(sort, "[") {
			var order []interface{}
			if json.Unmarshal([]byte(sort), &order) == nil {
				for _, part := range order {
					s.checkPath(where, model, strings.SplitN(fmt.Sprint(part), " ", 2)[0], "order", "CERTAIN", nil, ".")
				}
			}
		}
	}

	for _, record := range s.rows("automations") {
		model := s.modelOf(record["model_id"])
		where := s.where("automations", record, model)
		if s.modelGone(where, model) {
			continue
		}
		s.checkDomain(where, model, record["filter_domain"], "CERTAIN")
		s.checkDomain(where, model, record["filter_pre_domain"], "CERTAIN")
		for _, key := range []string{"trigger_field_ids", "on_change_field_ids"} {
			for _, id := range list(record[key]) {
				if fieldModel, name, ok := s.fieldOf(id); ok {
					s.checkPath(where, fieldModel, name, "read", "CERTAIN", nil, ".")
				}
			}
		}
		for _, key := range []string{"trg_date_id", "trg_selection_field_id"} {
			if fieldModel, name, ok := s.fieldOf(record[key]); ok && truthy(record[key]) {
				s.checkPath(where, fieldModel, name, "read", "CERTAIN", nil, ".")
			}
		}
	}

	for _, record := range s.rows("server_actions") {
		model := s.modelOf(record["model_id"])
		where := s.where("server_actions", record, model)
		if s.modelGone(where, model) {
			continue
		}
		if text(record["state"]) == "code" {
			s.checkCode(where, model, record["code"], "record", "records")
		}
		if path := text(record["update_path"]); path != "" {
			s.checkPath(where, model, path, "write", "CERTAIN", nil, ".")
		}
		for _, key := range []string{"update_field_id", "link_field_id"} {
			if fieldModel, name, ok := s.fieldOf(record[key]); ok && truthy(record[key]) {
				s.checkPath(where, fieldModel, name, "write", "CERTAIN", nil, ".")
			}
		}
		if crud := s.modelOf(record["crud_model_id"]); crud != "" && s.onSource(crud) {
			if _, exists := s.target.fields(crud); !exists {
				problem, d := s.missing("MODEL_MISSING", crud, "")
				s.report(where, crud, problem, "CERTAIN", d)
			}
		}
		for _, id := range list(record["webhook_field_ids"]) {
			if fieldModel, name, ok := s.fieldOf(id); ok {
				s.checkPath(where, fieldModel, name, "read", "CERTAIN", nil, ".")
			}
		}
	}

	for _, record := range s.rows("custom_fields") {
		model := text(record["model"])
		where := s.where("custom_fields", record, model)
		if s.modelGone(where, model) {
			continue
		}
		if relation := text(record["relation"]); relation != "" && s.onSource(relation) {
			if _, exists := s.target.fields(relation); !exists {
				problem, d := s.missing("MODEL_MISSING", relation, "")
				s.report(where, relation, problem, "CERTAIN", d)
			}
		}
		if related := text(record["related"]); related != "" {
			s.checkPath(where, model, related, "related", "CERTAIN", nil, ".")
		}
		for _, path := range strings.Split(text(record["depends"]), ",") {
			if path = strings.TrimSpace(path); path != "" {
				s.checkPath(where, model, path, "read", "CERTAIN", nil, ".")
			}
		}
		if text(record["compute"]) != "" {
			s.checkCode(where, model, record["compute"], "self", "record")
		}
	}

	for _, record := range s.rows("templates") {
		model := text(record["model"])
		where := s.where("templates", record, model)
		if s.modelGone(where, model) {
			continue
		}
		for _, key := range []string{"subject", "email_from", "email_to", "email_cc", "partner_to", "reply_to", "lang", "scheduled_date"} {
			for _, match := range inline.FindAllStringSubmatch(text(record[key]), -1) {
				s.checkExpression(where, match[1], map[string]string{"object": model})
			}
		}
		s.checkTemplate(where, model, record["body_html"])
	}

	for _, record := range s.rows("views") {
		model := text(record["model"])
		if model == "" {
			continue
		}
		where := s.where("views", record, model)
		if s.modelGone(where, model) {
			continue
		}
		s.checkView(where, model, text(record["arch_db"]))
	}

	for _, record := range s.rows("rules") {
		model := s.modelOf(record["model_id"])
		s.checkDomain(s.where("rules", record, model), model, record["domain_force"], "CERTAIN")
	}

	exports := map[int64]map[string]interface{}{}
	for _, record := range s.rows("exports") {
		resource := text(record["resource"])
		if !s.modelGone(s.where("exports", record, resource), resource) {
			exports[integer(record["id"])] = record
		}
	}
	for _, record := range s.rows("export_lines") {
		ids := list(record["export_id"])
		if len(ids) == 0 {
			continue
		}
		if parent, ok := exports[integer(ids[0])]; ok {
			resource := text(parent["resource"])
			s.checkPath(s.where("exports", parent, resource), resource, text(record["name"]), "export", "CERTAIN", nil, "/")
		}
	}

	for _, record := range s.rows("defaults") {
		if fieldModel, name, ok := s.fieldOf(record["field_id"]); ok && truthy(record["field_id"]) {
			var value interface{}
			if json.Unmarshal([]byte(text(record["json_value"])), &value) != nil {
				value = nil
			}
			s.checkPath(s.where("defaults", record, fieldModel), fieldModel, name, "default", "CERTAIN", []interface{}{value}, ".")
		}
	}

	for _, record := range s.rows("window_actions") {
		model := text(record["res_model"])
		where := s.where("window_actions", record, model)
		if s.modelGone(where, model) {
			continue
		}
		s.checkDomain(where, model, record["domain"], "CERTAIN")
		s.checkContext(where, model, record["context"])
	}

	// A container the target removed takes every customization it holds with it, whatever they reference.
	for _, kind := range Kinds {
		if !s.onSource(kind.Model) {
			continue
		}
		if _, exists := s.target.fields(kind.Model); exists {
			continue
		}
		for _, record := range s.rows(kind.Key) {
			where := s.where(kind.Key, record, s.modelOf(record["model_id"]))
			problem := "MODEL_MISSING"
			if replacedBy[kind.Model] != "" {
				problem = "CONTAINER_REPLACED"
			}
			s.report(where, kind.Model, problem, "CERTAIN", detail{replacedBy: replacedBy[kind.Model]})
		}
	}
	for _, record := range s.rows("access_rights") {
		model := s.modelOf(record["model_id"])
		s.modelGone(s.where("access_rights", record, model), model)
	}
}

// --- values as search_read gives them --------------------------------------------------------------

func text(value interface{}) string {
	if value, ok := value.(string); ok {
		return value
	}
	return ""
}

func truthy(value interface{}) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case []interface{}:
		return len(value) > 0
	case string:
		return value != ""
	case float64:
		return value != 0
	}
	return true
}

func list(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

func integer(value interface{}) int64 {
	switch value := value.(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	case json.Number:
		number, _ := value.Int64()
		return number
	}
	return 0
}

func idString(value interface{}) string {
	return fmt.Sprintf("%d", integer(value))
}
