package spec

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// Executor runs one catalog operation: the standalone executor (direct.Executor) is one.
type Executor interface {
	Execute(ctx context.Context, request direct.Request) (direct.Outcome, error)
}

// Action is what a plan step does.
type Action string

const (
	ActionInstall  Action = "INSTALL"
	ActionCreate   Action = "CREATE"
	ActionUpdate   Action = "UPDATE"
	ActionNoChange Action = "NO_CHANGE"
	// ActionDelete removes a record the spec marks absent.
	ActionDelete Action = "DELETE"
	// ActionUnsupported marks a step the catalog cannot carry out: a gap, with its reason.
	ActionUnsupported Action = "UNSUPPORTED"
	// ActionAmbiguous marks a record more than one provider record matches.
	ActionAmbiguous Action = "AMBIGUOUS"
	// ActionAfterModules marks a record whose model the database has only once the spec's modules
	// are installed: apply installs them, plans again, then carries the record out.
	ActionAfterModules Action = "AFTER_MODULES"
)

// The operations a plan finds and installs modules with, and reads and saves the settings with.
const (
	moduleFindOperation    = "structure.module.list"
	moduleInstallOperation = "admin.module.install"
	settingsReadOperation  = "structure.settings.describe"
	settingsWriteOperation = "structure.settings.update"
)

// Change is one field a step changes.
type Change struct {
	From interface{} `json:"from"`
	To   interface{} `json:"to"`
}

// Step is one thing a plan would do.
type Step struct {
	Index     int                    `json:"index"`
	Entity    string                 `json:"entity"`
	Key       string                 `json:"key"`
	Action    Action                 `json:"action"`
	Operation string                 `json:"operation,omitempty"`
	Readiness string                 `json:"readiness,omitempty"`
	TargetID  int64                  `json:"targetId,omitempty"`
	Values    map[string]interface{} `json:"values,omitempty"`
	Changes   map[string]Change      `json:"changes,omitempty"`
	// Blind are values written without having been read: the operation's read projection does not
	// carry them, so the plan cannot tell whether they already hold. Writing them again is harmless.
	Blind  []string `json:"blind,omitempty"`
	Reason string   `json:"reason,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// Plan is what applying a spec to a provider would do, step by step.
type Plan struct {
	Spec           string         `json:"spec"`
	Placeholders   []string       `json:"placeholders,omitempty"`
	ProviderSeries string         `json:"providerSeries,omitempty"`
	Steps          []Step         `json:"steps"`
	Summary        map[Action]int `json:"summary"`
	// Hash fingerprints the steps, so an apply can be bound to the plan a reviewer read.
	Hash string `json:"planHash"`
}

// Gaps are the steps the catalog cannot carry out.
func (p Plan) Gaps() []Step {
	var out []Step
	for _, step := range p.Steps {
		if step.Action == ActionUnsupported || step.Action == ActionAmbiguous {
			out = append(out, step)
		}
	}
	return out
}

// BuildPlan compares a spec with what the provider holds. It only reads: reads are never refused for
// being unverified here, because a plan is a proposal; each step reports its operation's readiness,
// and Apply is where an unverified write needs the caller's consent.
func BuildPlan(ctx context.Context, exec Executor, s Spec) (Plan, error) {
	if err := s.Validate(); err != nil {
		return Plan{}, err
	}
	planner := &planner{exec: exec, plan: Plan{Spec: s.Metadata.Name, Placeholders: s.Metadata.Placeholders, Summary: map[Action]int{}}}
	for _, module := range s.Modules {
		if err := planner.module(ctx, module); err != nil {
			return Plan{}, err
		}
	}
	if len(s.Settings) > 0 {
		if err := planner.settings(ctx, s.Settings); err != nil {
			return Plan{}, err
		}
	}
	for _, resource := range s.Resources {
		if err := planner.resource(ctx, resource); err != nil {
			return Plan{}, err
		}
	}
	plan := planner.plan
	for i := range plan.Steps {
		plan.Steps[i].Index = i + 1
		plan.Summary[plan.Steps[i].Action]++
	}
	plan.Hash = hashSteps(plan.Steps)
	return plan, nil
}

type planner struct {
	exec Executor
	plan Plan
	// installing says the plan installs modules, so a model it cannot find yet may come with them.
	installing bool
}

func (p *planner) add(step Step) {
	if step.Operation != "" && step.Readiness == "" {
		step.Readiness = p.readiness(step.Operation)
	}
	p.plan.Steps = append(p.plan.Steps, step)
}

// readiness is the operation's readiness on the provider's series.
func (p *planner) readiness(operation string) string {
	series := p.plan.ProviderSeries
	if series == "" {
		series = odoo.Odoo19ProviderSeries
	}
	if readiness, ok := odoo.ReadinessFor(series, operation); ok {
		return string(readiness)
	}
	return string(odoo.ReadinessUnverified)
}

// read runs a read operation and returns its records. A refusal that is about the data, not the
// call, is returned as such; a provider that cannot be reached ends the plan.
func (p *planner) read(ctx context.Context, request direct.Request) ([]map[string]interface{}, error) {
	request.AllowUnverified = true
	outcome, err := p.exec.Execute(ctx, request)
	if err != nil {
		return nil, err
	}
	if p.plan.ProviderSeries == "" {
		p.plan.ProviderSeries = outcome.ProviderSeries
	}
	return records(outcome.Data), nil
}

func (p *planner) module(ctx context.Context, name string) error {
	step := Step{Entity: "module", Key: "name=" + name}
	found, err := p.read(ctx, direct.Request{Operation: moduleFindOperation, Domain: []interface{}{[]interface{}{"name", "=", name}}, Limit: 1})
	if err != nil {
		return fmt.Errorf("spec: finding module %s: %w", name, err)
	}
	switch {
	case len(found) == 0:
		step.Action, step.Reason = ActionUnsupported, "the provider has no module "+name
	case found[0]["state"] == "installed":
		step.Action = ActionNoChange
	default:
		step.Action, step.Operation, step.TargetID = ActionInstall, moduleInstallOperation, idOf(found[0])
		p.installing = true
	}
	p.add(step)
	return nil
}

func (p *planner) resource(ctx context.Context, r Resource) error {
	find, describe, create, update := r.operations()
	step := Step{Entity: r.Entity, Key: r.key(), Note: r.Note}
	if !known(find) {
		step.Action, step.Reason = ActionUnsupported, "the catalog has no operation "+find+" to find the record with"
		p.add(step)
		return nil
	}
	limit := 2
	if r.Select == SelectFirst {
		limit = 1
	}
	// A resource that says whether its record is active, or that it must not exist, sees archived
	// records too: Odoo's searches skip them, so a record the spec archived would look missing and be
	// created again, and an absent one would survive archived.
	var seeArchived map[string]interface{}
	if _, states := r.Values["active"]; states || r.Absent {
		seeArchived = map[string]interface{}{"active_test": false}
	}
	found, err := p.read(ctx, direct.Request{Operation: find, Domain: r.domain(), Limit: limit, Order: "id", ProviderContext: seeArchived})
	if err != nil {
		var refused *direct.OperationError
		if errors.As(err, &refused) {
			// The catalog refuses an operation whose modules are missing before it reaches Odoo
			// (MODULE_NOT_INSTALLED); a model the catalog does not gate reaches Odoo and is not found.
			if p.installing && (refused.Class == string(odoo.ErrClassNotFound) || refused.Class == string(odoo.ErrClassModuleNotInstalled)) {
				step.Action, step.Reason = ActionAfterModules, "the model comes with a module this plan installs: "+refused.Message
			} else {
				step.Action, step.Reason = ActionUnsupported, fmt.Sprintf("%s refused the search: %s", find, refused.Message)
			}
			p.add(step)
			return nil
		}
		return fmt.Errorf("spec: finding %s %s: %w", r.Entity, step.Key, err)
	}
	if r.Absent {
		p.absent(r, step, found)
		return nil
	}
	switch {
	case len(found) == 0:
		switch {
		case known(create):
			step.Action, step.Operation, step.Values = ActionCreate, create, copyValues(r.Values)
		case p.installing:
			// Only a module brings a record the catalog cannot create (a website layout view, whose
			// model is in base but whose records come with website): the plan after the installs
			// decides, and reports the record as a gap then if it is still missing.
			step.Action, step.Reason = ActionAfterModules, "the record does not exist yet and the catalog cannot create it: it may come with a module this plan installs"
		default:
			step.Action, step.Reason = ActionUnsupported, "the record does not exist and the catalog has no operation "+create+" to create it"
		}
	case len(found) > 1:
		step.Action, step.Reason = ActionAmbiguous, fmt.Sprintf("%d records match %s", len(found), step.Key)
	default:
		step.TargetID = idOf(found[0])
		current := found[0]
		if known(describe) {
			described, err := p.read(ctx, direct.Request{Operation: describe, ProviderTargetID: step.TargetID, ProviderContext: seeArchived})
			if err == nil && len(described) == 1 {
				current = described[0]
			}
		}
		changes, blind := compare(r.Values, current)
		switch {
		case len(changes) == 0 && len(blind) == 0:
			step.Action = ActionNoChange
		case !known(update):
			step.Action, step.Reason = ActionUnsupported, "the record exists, differs, and the catalog has no operation "+update+" to change it"
			step.Changes, step.Blind = changes, blind
		default:
			step.Action, step.Operation, step.Changes, step.Blind = ActionUpdate, update, changes, blind
			step.Values = map[string]interface{}{}
			for field := range changes {
				step.Values[field] = r.Values[field]
			}
			for _, field := range blind {
				step.Values[field] = r.Values[field]
			}
		}
	}
	p.add(step)
	return nil
}

// settings plans the Settings page as one step: the fields that differ are saved together, as the
// page saves them. A field the catalog does not set is a gap; one this database does not have yet
// comes with a module (its settings come with it).
func (p *planner) settings(ctx context.Context, desired map[string]interface{}) error {
	step := Step{Entity: "settings", Key: "res.config.settings"}
	settable := map[string]bool{}
	for _, op := range odoo.SurfaceCatalog() {
		if op.SemanticID == settingsWriteOperation {
			for _, field := range op.WizardAllowedFields {
				settable[field] = true
			}
		}
	}
	var unknown, missing []string
	for field := range desired {
		if !settable[field] {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		step.Action, step.Reason = ActionUnsupported, fmt.Sprintf("%s does not set %s", settingsWriteOperation, strings.Join(unknown, ", "))
		p.add(step)
		return nil
	}
	read, err := p.read(ctx, direct.Request{Operation: settingsReadOperation})
	if err != nil {
		var refused *direct.OperationError
		if errors.As(err, &refused) {
			step.Action, step.Reason = ActionUnsupported, fmt.Sprintf("%s refused the read: %s", settingsReadOperation, refused.Message)
			p.add(step)
			return nil
		}
		return fmt.Errorf("spec: reading the settings: %w", err)
	}
	current := map[string]interface{}{}
	if len(read) == 1 {
		current = read[0]
	}
	for field := range desired {
		if _, has := current[field]; !has {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	changes, _ := compare(desired, current)
	switch {
	case len(missing) > 0 && p.installing:
		step.Action, step.Reason = ActionAfterModules, "the settings come with a module this plan installs: "+strings.Join(missing, ", ")
	case len(missing) > 0:
		step.Action, step.Reason = ActionUnsupported, "this database has no setting "+strings.Join(missing, ", ")+": its module is not installed"
	case len(changes) == 0:
		step.Action = ActionNoChange
	default:
		step.Action, step.Operation, step.Changes = ActionUpdate, settingsWriteOperation, changes
		step.Values = map[string]interface{}{}
		for field := range changes {
			step.Values[field] = desired[field]
		}
	}
	p.add(step)
	return nil
}

// absent plans a record the business does not keep: deleted when it exists.
func (p *planner) absent(r Resource, step Step, found []map[string]interface{}) {
	remove := r.operation(r.Delete, "delete")
	switch {
	case len(found) == 0 && p.installing:
		// A module this plan installs may bring it: Odoo's own CRM stages come with crm.
		step.Action, step.Reason = ActionAfterModules, "the record does not exist yet and may come with a module this plan installs"
	case len(found) == 0:
		step.Action = ActionNoChange
	case len(found) > 1:
		step.Action, step.Reason = ActionAmbiguous, fmt.Sprintf("%d records match %s", len(found), step.Key)
	case !known(remove):
		step.Action, step.TargetID = ActionUnsupported, idOf(found[0])
		step.Reason = "the record exists and the catalog has no operation " + remove + " to delete it"
	default:
		step.Action, step.Operation, step.TargetID = ActionDelete, remove, idOf(found[0])
	}
	p.add(step)
}

func known(operation string) bool {
	if operation == "" {
		return false
	}
	_, err := direct.DescribeAnyOperation(operation)
	return err == nil
}

// compare returns the desired values that differ from the current record, and those the record
// does not carry.
func compare(desired, current map[string]interface{}) (map[string]Change, []string) {
	changes := map[string]Change{}
	var blind []string
	for field, want := range desired {
		have, readable := current[field]
		if !readable {
			if sameContent(field, want, current) {
				continue
			}
			blind = append(blind, field)
			continue
		}
		if !sameValue(want, have) {
			changes[field] = Change{From: have, To: want}
		}
	}
	sort.Strings(blind)
	return changes, blind
}

// sameValue compares a desired value, as a spec writes it, with a value as the provider answers it:
// a relation answers [id, name] or {id, display_name} and is the same as its id or its name; an
// empty value answers false.
func sameValue(want, have interface{}) bool {
	if isEmpty(want) && isEmpty(have) {
		return true
	}
	// A many2many named by ids holds the same records in any order; a two-id list is not a many2one.
	if w, ok := want.([]interface{}); ok {
		if h, ok := have.([]interface{}); ok {
			if same, numeric := sameIDs(w, h); numeric {
				return same
			}
		}
	}
	switch h := have.(type) {
	case []interface{}:
		if len(h) == 2 {
			if id, ok := number(h[0]); ok {
				if w, ok := number(want); ok {
					return w == id
				}
				return sameName(want, h[1])
			}
		}
	case map[string]interface{}:
		if id, ok := number(h["id"]); ok {
			if w, ok := number(want); ok {
				return w == id
			}
			for _, label := range []string{"display_name", "name"} {
				if name, ok := h[label]; ok && sameName(want, name) {
					return true
				}
			}
			return false
		}
	}
	if w, ok := number(want); ok {
		if v, ok := number(have); ok {
			return math.Abs(w-v) < 1e-9
		}
	}
	if w, ok := want.(bool); ok {
		v, isBool := have.(bool)
		return isBool && v == w
	}
	// Odoo keeps text without its trailing newline (a page's arch read from a file has one).
	return strings.TrimRight(fmt.Sprint(want), " \t\r\n") == strings.TrimRight(fmt.Sprint(have), " \t\r\n")
}

func isEmpty(v interface{}) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == ""
	case []interface{}:
		return len(x) == 0
	}
	return false
}

func number(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func records(data interface{}) []map[string]interface{} {
	switch rows := data.(type) {
	case []map[string]interface{}:
		return rows
	case []interface{}:
		out := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			if record, ok := row.(map[string]interface{}); ok {
				out = append(out, record)
			}
		}
		return out
	case map[string]interface{}:
		return records(rows["data"])
	}
	return nil
}

func idOf(record map[string]interface{}) int64 {
	if id, ok := number(record["id"]); ok {
		return int64(id)
	}
	if s, ok := record["id"].(string); ok {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			return id
		}
	}
	return 0
}

func copyValues(values map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func hashSteps(steps []Step) string {
	type bound struct {
		Action    Action
		Operation string
		TargetID  int64
		Values    map[string]interface{}
	}
	material := make([]bound, len(steps))
	for i, step := range steps {
		material[i] = bound{step.Action, step.Operation, step.TargetID, step.Values}
	}
	encoded, _ := json.Marshal(material)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// sameName compares a record's name, as a spec writes it, with its display name. A record in a
// hierarchy displays its path ("Ban Giám đốc / Kỹ thuật", "WH/Stock"): its own name is the last
// segment, which is also what the operation's name resolution matched when it was written.
func sameName(want, display interface{}) bool {
	w, d := strings.TrimSpace(fmt.Sprint(want)), strings.TrimSpace(fmt.Sprint(display))
	if w == d {
		return true
	}
	if i := strings.LastIndex(d, "/"); i >= 0 && !strings.Contains(w, "/") {
		return strings.TrimSpace(d[i+1:]) == w
	}
	return false
}

// contentChecksums are the fields whose content a record does not return but keeps a checksum of:
// an attachment's datas, as the SHA-1 Odoo stores in checksum.
var contentChecksums = map[string]string{"datas": "checksum"}

// sameContent reports whether a base64 value the record cannot return is the content the record
// holds, by its checksum: a published file is not sent again when it has not changed.
func sameContent(field string, want interface{}, current map[string]interface{}) bool {
	checksumField, ok := contentChecksums[field]
	if !ok {
		return false
	}
	have, _ := current[checksumField].(string)
	encoded, _ := want.(string)
	if have == "" || encoded == "" {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:]) == have
}

// sameIDs compares two lists of record ids as sets; numeric is false when either holds anything else.
func sameIDs(want, have []interface{}) (same, numeric bool) {
	ids := func(list []interface{}) ([]float64, bool) {
		out := make([]float64, 0, len(list))
		for _, item := range list {
			n, ok := number(item)
			if !ok {
				return nil, false
			}
			out = append(out, n)
		}
		sort.Float64s(out)
		return out, true
	}
	w, wOK := ids(want)
	h, hOK := ids(have)
	if !wOK || !hOK {
		return false, false
	}
	if len(w) != len(h) {
		return false, true
	}
	for i := range w {
		if w[i] != h[i] {
			return false, true
		}
	}
	return true, true
}
