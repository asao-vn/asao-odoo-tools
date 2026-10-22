package review

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/asao-platform/atoc/packages/odoo"
)

// Declared checks (DEC-2026-0181): a check is a file, odoo-cli.check/v1, run by this one engine. The file
// names the verified read operation it needs, how its records match (a matcher), how a finding reads, and
// its own cases. The engine's input is the operation's records over the period; its output is Finding.

// CheckSchema names the declared check format this build reads.
const CheckSchema = "odoo-cli.check/v1"

//go:embed checks/*.json
var builtinCheckFiles embed.FS

// CheckFile is one declared check.
type CheckFile struct {
	Schema   string             `json:"schema"`
	ID       string             `json:"id"`
	Version  int                `json:"version"`
	Name     string             `json:"name"`
	Question string             `json:"question"`
	Kind     string             `json:"kind"`
	Formula  string             `json:"formula"`
	Params   map[string]float64 `json:"params,omitempty"`
	Source   CheckSource        `json:"source"`
	Match    CheckMatch         `json:"match"`
	Severity []SeverityRule     `json:"severity"`
	Finding  FindingTemplate    `json:"finding"`
	Cases    []CheckCase        `json:"cases"`
}

// CheckSource is where the records come from: one verified read operation, the domain sent with it,
// the date field that places a record in the period, and conditions applied to what came back.
type CheckSource struct {
	Operation string          `json:"operation"`
	Domain    []interface{}   `json:"domain,omitempty"`
	Period    string          `json:"period"`
	Where     [][]interface{} `json:"where,omitempty"`
}

// CheckMatch is how records make a finding.
//   - pairs: records sharing Key, in Order, each with the next one when their Date is at most Within days
//     apart; Flag, when set, names records of the pair that must meet a condition (AtLeast of them).
//   - groups: records sharing Key within Within days of the first of them, together reaching Threshold
//     (Aggregate count, or sum(field)), each below EachBelow when set; MinCount records at least.
type CheckMatch struct {
	Kind      string     `json:"kind"`
	Key       []string   `json:"key"`
	Date      string     `json:"date"`
	Order     []string   `json:"order,omitempty"`
	Within    string     `json:"within"`
	Flag      *CheckFlag `json:"flag,omitempty"`
	Aggregate string     `json:"aggregate,omitempty"`
	Threshold string     `json:"threshold,omitempty"`
	EachBelow string     `json:"eachBelow,omitempty"`
	// EachAtLeast keeps only records at or above it, as EachBelow keeps those under it. Either names a
	// parameter or the product of two ("band*threshold"), so a band follows the threshold it is a share of.
	EachAtLeast string `json:"eachAtLeast,omitempty"`
	MinCount    string `json:"minCount,omitempty"`
}

// CheckFlag is the condition some records of a pair must meet.
type CheckFlag struct {
	Where   [][]interface{} `json:"where"`
	AtLeast int             `json:"atLeast"`
}

// SeverityRule gives a finding its severity: the first rule whose condition holds (no condition: always).
type SeverityRule struct {
	If   string `json:"if,omitempty"`
	Then string `json:"then"`
}

// FindingTemplate is how a finding reads. Text in braces is replaced: {first.field}, {second.field},
// {first.relation.name}, {first.relation.id}, {money(first.amount)}, {join(flagged)}, {gap}, {count},
// {total}, {from}, {to}, {params.name}.
type FindingTemplate struct {
	Title   string            `json:"title"`
	Subject map[string]string `json:"subject"`
	Why     string            `json:"why"`
	Amount  string            `json:"amount,omitempty"`
	Facts   map[string]string `json:"facts,omitempty"`
}

// CheckCase is a record set the check must answer as expected.
type CheckCase struct {
	Name    string                              `json:"name"`
	From    string                              `json:"from,omitempty"`
	To      string                              `json:"to,omitempty"`
	Records map[string][]map[string]interface{} `json:"records"`
	Expect  []ExpectedFinding                   `json:"expect"`
}

// ExpectedFinding is one finding a case expects: the ids of its evidence, and its severity when given.
type ExpectedFinding struct {
	Evidence []int64 `json:"evidence"`
	Severity string  `json:"severity,omitempty"`
}

var (
	checkID      = regexp.MustCompile(`^[A-Z][A-Z0-9-]{1,23}$`)
	placeholder  = regexp.MustCompile(`\{([^{}]+)\}`)
	condition    = regexp.MustCompile(`^\s*([a-z_]+)\s*(==|!=|<=|>=|<|>)\s*(-?[0-9.]+)\s*$`)
	severityName = map[string]bool{XemNgay: true, NenXuLy: true, TheoDoi: true}
)

// ParseCheck reads a declared check and checks it without running it: the format, the operation (a
// verified read), the matcher, the parameters it names, the severities and that it has cases of both kinds.
func ParseCheck(data []byte) (CheckFile, error) {
	var file CheckFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&file); err != nil {
		return CheckFile{}, fmt.Errorf("check: not an %s document: %v", CheckSchema, err)
	}
	return file, file.validate()
}

func (f CheckFile) validate() error {
	switch {
	case f.Schema != CheckSchema:
		return fmt.Errorf("check: schema is %q, this build reads %q", f.Schema, CheckSchema)
	case !checkID.MatchString(f.ID):
		return fmt.Errorf("check: id %q is not upper-case letters, digits and dashes", f.ID)
	case f.Version < 1:
		return fmt.Errorf("check %s: version is a whole number from 1", f.ID)
	case f.Name == "" || f.Question == "" || f.Formula == "":
		return fmt.Errorf("check %s: name, question and formula are required", f.ID)
	case f.Source.Period == "":
		return fmt.Errorf("check %s: source.period names the date field that places a record in the period", f.ID)
	}
	if op, ok := odoo.LookupStandaloneOperation(f.Source.Operation); !ok || op.Kind != odoo.KindRead || op.Strategy != odoo.StrategyRead {
		return fmt.Errorf("check %s: source.operation %q is not a read operation of the catalog", f.ID, f.Source.Operation)
	}
	param := func(name, role string) error {
		if name == "" {
			return nil
		}
		if _, ok := f.Params[name]; !ok {
			return fmt.Errorf("check %s: match.%s names the parameter %q, which params does not hold", f.ID, role, name)
		}
		return nil
	}
	m := f.Match
	if len(m.Key) == 0 || m.Date == "" || m.Within == "" {
		return fmt.Errorf("check %s: match needs key, date and within", f.ID)
	}
	if err := param(m.Within, "within"); err != nil {
		return err
	}
	switch m.Kind {
	case "pairs":
		if m.Aggregate != "" || m.Threshold != "" || m.EachBelow != "" || m.EachAtLeast != "" || m.MinCount != "" {
			return fmt.Errorf("check %s: a pairs match takes no aggregate, threshold, eachBelow, eachAtLeast or minCount", f.ID)
		}
		if m.Flag != nil && (len(m.Flag.Where) == 0 || m.Flag.AtLeast < 1) {
			return fmt.Errorf("check %s: match.flag needs where and atLeast from 1", f.ID)
		}
	case "groups":
		if m.Flag != nil {
			return fmt.Errorf("check %s: a groups match takes no flag", f.ID)
		}
		if m.Aggregate != "count" && !strings.HasPrefix(m.Aggregate, "sum(") {
			return fmt.Errorf("check %s: match.aggregate is count or sum(field)", f.ID)
		}
		for role, name := range map[string]string{"threshold": m.Threshold, "minCount": m.MinCount} {
			if err := param(name, role); err != nil {
				return err
			}
		}
		for role, expr := range map[string]string{"eachBelow": m.EachBelow, "eachAtLeast": m.EachAtLeast} {
			if expr == "" {
				continue
			}
			names := strings.Split(expr, "*")
			if len(names) > 2 {
				return fmt.Errorf("check %s: match.%s is a parameter or the product of two", f.ID, role)
			}
			for _, name := range names {
				if err := param(strings.TrimSpace(name), role); err != nil {
					return err
				}
			}
		}
		if m.Threshold == "" {
			return fmt.Errorf("check %s: a groups match needs a threshold", f.ID)
		}
	default:
		return fmt.Errorf("check %s: match.kind %q is not pairs or groups", f.ID, m.Kind)
	}
	if len(f.Severity) == 0 {
		return fmt.Errorf("check %s: severity needs a rule", f.ID)
	}
	for i, rule := range f.Severity {
		if !severityName[rule.Then] {
			return fmt.Errorf("check %s: severity %q is not XEM_NGAY, NEN_XU_LY or THEO_DOI", f.ID, rule.Then)
		}
		if rule.If != "" && !condition.MatchString(rule.If) {
			return fmt.Errorf("check %s: severity condition %q is not 'name op number'", f.ID, rule.If)
		}
		if rule.If == "" && i != len(f.Severity)-1 {
			return fmt.Errorf("check %s: only the last severity rule may hold no condition", f.ID)
		}
	}
	if f.Finding.Title == "" || f.Finding.Why == "" || f.Finding.Subject["type"] == "" {
		return fmt.Errorf("check %s: finding needs a title, a why and a subject type", f.ID)
	}
	flags, clean := 0, 0
	for _, c := range f.Cases {
		if len(c.Expect) > 0 {
			flags++
		} else {
			clean++
		}
	}
	if flags == 0 || clean == 0 {
		return fmt.Errorf("check %s: cases need one the check must flag and one it must leave alone", f.ID)
	}
	return nil
}

// check turns the file into a registered check.
func (f CheckFile) check() check {
	return check{
		id: f.ID, name: f.Name, question: f.Question, kind: f.Kind, formula: f.Formula,
		needs: []string{f.Source.Operation}, params: f.Params,
		run: func(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) { return f.run(ctx, b, p) },
	}
}

func (f CheckFile) run(ctx context.Context, b *books, p map[string]float64) ([]Finding, error) {
	rows, err := b.read(ctx, f.Source.Operation, f.Source.Domain)
	if err != nil {
		return nil, err
	}
	var records []map[string]interface{}
	for _, row := range rows {
		if b.inPeriod(row[f.Source.Period]) && matchesAll(row, f.Source.Where) {
			records = append(records, row)
		}
	}
	byKey := map[string][]map[string]interface{}{}
	var keys []string
	for _, row := range records {
		parts := make([]string, len(f.Match.Key))
		for i, expr := range f.Match.Key {
			parts[i] = keyPart(row, expr)
		}
		key := strings.Join(parts, "/")
		if _, seen := byKey[key]; !seen {
			keys = append(keys, key)
		}
		byKey[key] = append(byKey[key], row)
	}
	sort.Strings(keys)
	order := f.Match.Order
	if len(order) == 0 {
		order = []string{f.Match.Date}
	}
	var out []Finding
	for _, key := range keys {
		same := byKey[key]
		sort.SliceStable(same, func(i, j int) bool {
			for _, field := range order {
				a, z := sortValue(same[i], field), sortValue(same[j], field)
				if a != z {
					return a < z
				}
			}
			return false
		})
		switch f.Match.Kind {
		case "pairs":
			out = append(out, f.pairs(same, p)...)
		case "groups":
			out = append(out, f.groups(same, p)...)
		}
	}
	return out, nil
}

func (f CheckFile) pairs(same []map[string]interface{}, p map[string]float64) []Finding {
	var out []Finding
	for i := 0; i+1 < len(same); i++ {
		first, second := same[i], same[i+1]
		d1, _ := parseDay(first[f.Match.Date])
		d2, _ := parseDay(second[f.Match.Date])
		gap := d1.daysUntil(d2)
		if float64(gap) > p[f.Match.Within] {
			continue
		}
		var flagged []string
		if f.Match.Flag != nil {
			for _, row := range []map[string]interface{}{first, second} {
				if matchesAll(row, f.Match.Flag.Where) {
					flagged = append(flagged, text(row["name"]))
				}
			}
			if len(flagged) < f.Match.Flag.AtLeast {
				continue
			}
		}
		vars := map[string]interface{}{"gap": gap, "flagged": flagged, "flagged_count": len(flagged)}
		out = append(out, f.finding(map[string]map[string]interface{}{"first": first, "second": second}, vars, p,
			[]map[string]interface{}{first, second}))
	}
	return out
}

// bound is the value of eachBelow or eachAtLeast: one parameter, or the product of two.
func bound(expr string, p map[string]float64) float64 {
	value := 1.0
	for _, name := range strings.Split(expr, "*") {
		value *= p[strings.TrimSpace(name)]
	}
	return value
}

func (f CheckFile) groups(rows []map[string]interface{}, p map[string]float64) []Finding {
	// A record outside the bounds takes no part: it neither joins a group nor starts one.
	var same []map[string]interface{}
	for _, row := range rows {
		if f.Match.EachBelow != "" && f.aggregateOf(row) >= bound(f.Match.EachBelow, p) {
			continue
		}
		if f.Match.EachAtLeast != "" && f.aggregateOf(row) < bound(f.Match.EachAtLeast, p) {
			continue
		}
		same = append(same, row)
	}
	// Each record closes a window of the records within `within` days before it. A window that qualifies is
	// a finding; one that grew from the finding before it replaces that finding. A chain of records (a with
	// b, then b with c) makes two findings, so no record that qualifies goes unreported.
	var out []Finding
	var window, previous []map[string]interface{}
	for _, row := range same {
		when, _ := parseDay(row[f.Match.Date])
		kept := window[:0:0]
		for _, earlier := range window {
			if d, _ := parseDay(earlier[f.Match.Date]); float64(d.daysUntil(when)) <= p[f.Match.Within] {
				kept = append(kept, earlier)
			}
		}
		window = append(kept, row)
		total := 0.0
		for _, r := range window {
			total += f.aggregateOf(r)
		}
		if total < p[f.Match.Threshold] || (f.Match.MinCount != "" && float64(len(window)) < p[f.Match.MinCount]) {
			continue
		}
		names := make([]string, len(window))
		for k, r := range window {
			names[k] = text(r["name"])
		}
		first, _ := parseDay(window[0][f.Match.Date])
		vars := map[string]interface{}{"count": len(window), "total": total, "records": names,
			"from": first.t.Format("2006-01-02"), "to": when.t.Format("2006-01-02")}
		finding := f.finding(map[string]map[string]interface{}{"first": window[0], "last": row}, vars, p, window)
		if len(out) > 0 && within(previous, window) {
			out = out[:len(out)-1]
		}
		out = append(out, finding)
		previous = append([]map[string]interface{}(nil), window...)
	}
	return out
}

// within says whether every record of a is in b.
func within(a, b []map[string]interface{}) bool {
	in := map[interface{}]bool{}
	for _, r := range b {
		in[r["id"]] = true
	}
	for _, r := range a {
		if !in[r["id"]] {
			return false
		}
	}
	return true
}

func (f CheckFile) aggregateOf(row map[string]interface{}) float64 {
	if f.Match.Aggregate == "count" {
		return 1
	}
	return num(row[strings.TrimSuffix(strings.TrimPrefix(f.Match.Aggregate, "sum("), ")")])
}

func (f CheckFile) finding(rows map[string]map[string]interface{}, vars map[string]interface{}, p map[string]float64, evidenceRows []map[string]interface{}) Finding {
	render := func(template string) string {
		return placeholder.ReplaceAllStringFunc(template, func(token string) string {
			return renderValue(resolve(strings.Trim(token, "{}"), rows, vars, p))
		})
	}
	severity := f.Severity[len(f.Severity)-1].Then
	for _, rule := range f.Severity {
		if rule.If == "" || holds(rule.If, vars) {
			severity = rule.Then
			break
		}
	}
	ev := make([]Evidence, len(evidenceRows))
	for i, row := range evidenceRows {
		ev[i] = evidence(f.Source.Operation, row, "")
	}
	facts := map[string]interface{}{}
	for name, expr := range f.Finding.Facts {
		if strings.Contains(expr, "{") {
			facts[name] = render(expr)
		} else {
			facts[name] = resolve(expr, rows, vars, p)
		}
	}
	amount := 0.0
	if f.Finding.Amount != "" {
		amount = num(resolve(f.Finding.Amount, rows, vars, p))
	}
	return Finding{
		Title: render(f.Finding.Title), Severity: severity,
		Subject:   Subject{Type: f.Finding.Subject["type"], ID: render(f.Finding.Subject["id"]), Name: render(f.Finding.Subject["name"])},
		Why:       render(f.Finding.Why),
		AmountVND: amount, Evidence: ev, Facts: facts,
	}
}

// resolve reads one expression: a variable, params.name, row.field[.id|.name], money(expr), join(expr).
func resolve(expr string, rows map[string]map[string]interface{}, vars map[string]interface{}, p map[string]float64) interface{} {
	expr = strings.TrimSpace(expr)
	if inner, ok := strings.CutPrefix(expr, "money("); ok {
		return money(num(resolve(strings.TrimSuffix(inner, ")"), rows, vars, p)))
	}
	if inner, ok := strings.CutPrefix(expr, "join("); ok {
		list, _ := resolve(strings.TrimSuffix(inner, ")"), rows, vars, p).([]string)
		return strings.Join(list, ", ")
	}
	if value, ok := vars[expr]; ok {
		return value
	}
	if name, ok := strings.CutPrefix(expr, "params."); ok {
		return p[name]
	}
	head, rest, _ := strings.Cut(expr, ".")
	row, ok := rows[head]
	if !ok || rest == "" {
		return ""
	}
	field, part, _ := strings.Cut(rest, ".")
	value := row[field]
	switch part {
	case "id":
		return relID(value)
	case "name":
		return relName(value)
	}
	return value
}

func renderValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case []string:
		return strings.Join(v, ", ")
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func holds(rule string, vars map[string]interface{}) bool {
	m := condition.FindStringSubmatch(rule)
	if m == nil {
		return false
	}
	left, _ := number(vars[m[1]])
	if n, ok := vars[m[1]].(int); ok {
		left = float64(n)
	}
	right, _ := strconv.ParseFloat(m[3], 64)
	switch m[2] {
	case "==":
		return left == right
	case "!=":
		return left != right
	case "<":
		return left < right
	case "<=":
		return left <= right
	case ">":
		return left > right
	default:
		return left >= right
	}
}

// keyPart is one part of a record's key: field, field.id, field.name or round(field).
func keyPart(row map[string]interface{}, expr string) string {
	if inner, ok := strings.CutPrefix(expr, "round("); ok {
		return strconv.FormatInt(int64(math.Round(num(row[strings.TrimSuffix(inner, ")")]))), 10)
	}
	field, part, _ := strings.Cut(expr, ".")
	switch part {
	case "id":
		return strconv.FormatInt(relID(row[field]), 10)
	case "name":
		return relName(row[field])
	}
	return renderValue(row[field])
}

func sortValue(row map[string]interface{}, field string) string {
	if n, ok := number(row[field]); ok {
		return fmt.Sprintf("%020.6f", n+1e12)
	}
	return text(row[field])
}

// matchesAll says whether a record meets every condition [field, op, value] (ops =, !=, in, not in).
// A field the record does not carry meets no condition but !=.
func matchesAll(row map[string]interface{}, where [][]interface{}) bool {
	for _, cond := range where {
		if len(cond) != 3 {
			return false
		}
		field, _ := cond[0].(string)
		op, _ := cond[1].(string)
		value, present := row[field]
		switch op {
		case "=":
			if !present || !same(value, cond[2]) {
				return false
			}
		case "!=":
			if present && same(value, cond[2]) {
				return false
			}
		case "in", "not in":
			list, _ := cond[2].([]interface{})
			found := false
			for _, item := range list {
				found = found || (present && same(value, item))
			}
			if found != (op == "in") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func same(a, b interface{}) bool {
	if x, ok := number(a); ok {
		y, ok := number(b)
		return ok && x == y
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// TestCase runs one of a file's cases: the findings it gave, and why it did not answer as expected.
func (f CheckFile) TestCase(c CheckCase) ([]Finding, error) {
	from, to := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2100, 12, 31, 0, 0, 0, 0, time.UTC)
	if c.From != "" {
		from, _ = time.Parse("2006-01-02", c.From)
	}
	if c.To != "" {
		to, _ = time.Parse("2006-01-02", c.To)
	}
	b := newBooks(caseReader(c.Records), day(from), day(to), day(to))
	found, err := f.run(context.Background(), b, f.Params)
	if err != nil {
		return nil, err
	}
	if len(found) != len(c.Expect) {
		return found, fmt.Errorf("case %q: %d findings, expected %d", c.Name, len(found), len(c.Expect))
	}
	// Each expected finding is one found with the same evidence, in any order.
	used := map[int]bool{}
	for _, want := range c.Expect {
		match := -1
		for i, finding := range found {
			got := make([]int64, len(finding.Evidence))
			for k, e := range finding.Evidence {
				got[k] = e.ID
			}
			if !used[i] && fmt.Sprint(got) == fmt.Sprint(want.Evidence) {
				match = i
				break
			}
		}
		if match < 0 {
			return found, fmt.Errorf("case %q: no finding names %v", c.Name, want.Evidence)
		}
		used[match] = true
		if want.Severity != "" && found[match].Severity != want.Severity {
			return found, fmt.Errorf("case %q: the finding naming %v is %s, expected %s", c.Name, want.Evidence, found[match].Severity, want.Severity)
		}
	}
	return found, nil
}

// TestCases runs every case of the file; the first one that does not answer as expected is the error.
func (f CheckFile) TestCases() error {
	for _, c := range f.Cases {
		if _, err := f.TestCase(c); err != nil {
			return fmt.Errorf("check %s v%d: %w", f.ID, f.Version, err)
		}
	}
	return nil
}

type caseReader map[string][]map[string]interface{}

func (r caseReader) ReadAll(_ context.Context, operation string, _ []interface{}) ([]map[string]interface{}, error) {
	return normalized(r[operation]), nil
}

// normalized turns a case's JSON numbers into the float64 a reader gives.
func normalized(rows []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		data, _ := json.Marshal(row)
		var plain map[string]interface{}
		_ = json.Unmarshal(data, &plain)
		out[i] = plain
	}
	return out
}

// RegisterCheck adds a declared check after its own cases pass. A built-in check's id cannot be taken.
func RegisterCheck(f CheckFile) error {
	if err := f.validate(); err != nil {
		return err
	}
	if existing, taken := checks[f.ID]; taken && !declaredIDs[existing.id] {
		return fmt.Errorf("check %s: the id is a built-in check's", f.ID)
	}
	if err := f.TestCases(); err != nil {
		return err
	}
	checks[f.ID] = f.check()
	return nil
}

// declaredIDs are the checks that come from files, built in or added.
var declaredIDs = map[string]bool{}

func init() {
	entries, err := builtinCheckFiles.ReadDir("checks")
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		data, err := builtinCheckFiles.ReadFile(path.Join("checks", entry.Name()))
		if err != nil {
			panic(err)
		}
		file, err := ParseCheck(data)
		if err != nil {
			panic("review: built-in " + entry.Name() + ": " + err.Error())
		}
		if strings.TrimSuffix(entry.Name(), ".json") != file.ID {
			panic("review: built-in " + entry.Name() + " holds check " + file.ID)
		}
		declaredIDs[file.ID] = true
		register(file.check())
	}
}

// BuiltinCheckFiles lists the declared built-in checks, for listing and for their cases' test.
func BuiltinCheckFiles() []CheckFile {
	entries, _ := builtinCheckFiles.ReadDir("checks")
	out := make([]CheckFile, 0, len(entries))
	for _, entry := range entries {
		data, _ := builtinCheckFiles.ReadFile(path.Join("checks", entry.Name()))
		file, _ := ParseCheck(data)
		out = append(out, file)
	}
	return out
}
