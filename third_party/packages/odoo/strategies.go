package odoo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// ---- READ ---------------------------------------------------------------

func (a *Adapter) execRead(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, error) {
	if entry.ReadNewRecord {
		return a.execReadNewRecord(ctx, entry, in)
	}
	if in.Offset < 0 {
		return nil, &FieldValidationError{Operation: entry.SemanticID, Field: "offset"}
	}
	dom := in.Domain
	if dom == nil {
		dom = []interface{}{}
	}
	if in.TargetID != 0 {
		dom = []interface{}{[]interface{}{"id", "=", in.TargetID}}
	}
	if len(entry.ProviderDomain) > 0 {
		// In Odoo domains, consecutive top-level clauses form an implicit conjunction (AND).
		// Conjoin the fixed ProviderDomain clauses with the target/caller domain so the
		// operation is strictly constrained to its semantic model subset, even with TargetID.
		combined := make([]interface{}, 0, len(entry.ProviderDomain)+len(dom))
		combined = append(combined, entry.ProviderDomain...)
		combined = append(combined, dom...)
		dom = combined
	}
	grouped := len(in.GroupBy) > 0 || len(in.Aggregates) > 0
	if grouped {
		// Refused before the provider is contacted, metadata included.
		allowed := append(append([]string(nil), entry.ReadFields...), entry.OptionalReadFields...)
		if err := checkGroupSpecs(entry, in, allowed); err != nil {
			return nil, err
		}
	}
	fields, err := a.readFields(ctx, entry, in.ProviderContext)
	if err != nil {
		return nil, err
	}
	if grouped {
		return a.execReadGrouped(ctx, entry, in, dom, fields)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	// The caller's sort wins over the catalog default: the CLI publishes --order for READ operations,
	// and ignoring it silently returned records in an order the caller did not ask for.
	order := entry.DefaultSort
	if in.Order != "" {
		order = in.Order
	}
	var recs []map[string]interface{}
	if in.Offset > 0 {
		pager, ok := a.Transport.(PagedSearcher)
		if !ok {
			return nil, fmt.Errorf("odoo: this transport cannot page a read (offset %d)", in.Offset)
		}
		recs, err = pager.SearchReadPage(ctx, entry.ProviderModel, dom, fields, in.Offset, limit, order, in.ProviderContext)
	} else {
		recs, err = a.Transport.SearchRead(ctx, entry.ProviderModel, dom, fields, limit, order, in.ProviderContext)
	}
	if err != nil {
		return nil, err
	}
	return normalizeRecords(entry.SemanticID, recs), nil
}

// groupGranularities are the date and datetime buckets formatted_read_group accepts (Odoo 19).
var groupGranularities = map[string]bool{
	"day": true, "week": true, "month": true, "quarter": true, "year": true,
	"year_number": true, "quarter_number": true, "month_number": true, "iso_week_number": true,
	"day_of_year": true, "day_of_month": true, "day_of_week": true,
	"hour_number": true, "minute_number": true, "second_number": true,
}

// groupFunctions are the aggregate functions a grouped read may ask for.
var groupFunctions = map[string]bool{
	"sum": true, "avg": true, "min": true, "max": true, "count": true, "count_distinct": true,
	"bool_and": true, "bool_or": true,
}

// execReadGrouped answers groups instead of records: the provider groups and aggregates the records
// the read's domain matches, the way Odoo's own grouped lists and pivots do. Only the fields the read
// shows can be grouped or aggregated, so grouping never reveals more than reading does. Every group
// carries its record count. A many2one group value is {id, display_name}; a date bucket is
// {value, display_name}, its first day and Odoo's label for it.
func (a *Adapter) execReadGrouped(ctx context.Context, entry Odoo19Operation, in OperationInput, domain []interface{}, fields []string) (interface{}, error) {
	// The specifications passed the catalog's allowlist; this Odoo may still lack an optional field.
	if err := checkGroupSpecs(entry, in, fields); err != nil {
		return nil, err
	}
	aggregates := append(append(make([]string, 0, len(in.Aggregates)+1), in.Aggregates...), "__count")
	reader, ok := a.Transport.(GroupReader)
	if !ok {
		return nil, fmt.Errorf("odoo: this transport cannot group a read")
	}
	groupBy := in.GroupBy
	if groupBy == nil {
		groupBy = []string{} // aggregates over every matching record: one group (Odoo needs a list)
	}
	groups, err := reader.ReadGroup(ctx, entry.ProviderModel, domain, groupBy, aggregates, in.Offset, in.Limit, in.Order, in.ProviderContext)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		delete(group, "__extra_domain")
		delete(group, "__fold")
		group["count"] = group["__count"]
		delete(group, "__count")
		for key, value := range group {
			pair, isPair := value.([]interface{})
			if !isPair || len(pair) != 2 {
				continue
			}
			if _, isID := pair[0].(float64); isID {
				group[key] = map[string]interface{}{"id": pair[0], "display_name": pair[1]}
			} else {
				group[key] = map[string]interface{}{"value": pair[0], "display_name": pair[1]}
			}
		}
	}
	// Odoo's false for a relation or a date is no value (tasks without a project): null, as a read
	// says it. A boolean group keeps its false.
	valued := map[string]bool{}
	for _, group := range groups {
		for key, value := range group {
			if _, isObject := value.(map[string]interface{}); isObject {
				valued[key] = true
			}
		}
	}
	for _, group := range groups {
		for key := range valued {
			if group[key] == false {
				group[key] = nil
			}
		}
	}
	return groups, nil
}

// readFields are the fields a read asks for: its read fields, and those of its optional read fields
// the provider declares. Callers never widen the projection beyond the catalog's allowlist.
func (a *Adapter) readFields(ctx context.Context, entry Odoo19Operation, providerCtx map[string]interface{}) ([]string, error) {
	if len(entry.OptionalReadFields) == 0 {
		return entry.ReadFields, nil
	}
	fieldRecs, err := a.Transport.SearchRead(ctx, "ir.model.fields", [][]interface{}{{"model", "=", entry.ProviderModel}}, []string{"name"}, 0, "", providerCtx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(fieldRecs))
	for _, r := range fieldRecs {
		if name, ok := r["name"].(string); ok {
			existing[name] = true
		}
	}
	fields := append([]string(nil), entry.ReadFields...)
	for _, opt := range entry.OptionalReadFields {
		if existing[opt] {
			fields = append(fields, opt)
		}
	}
	return fields, nil
}

// execReadNewRecord reads a new record's values as the web client gets them when a form opens on
// one: onchange with nothing set computes the defaults, the related values and, for
// res.config.settings, what get_values reads from the configuration. It reads and writes nothing
// else: the record only exists in that call.
func (a *Adapter) execReadNewRecord(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, error) {
	if in.TargetID != 0 {
		return nil, &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeCreate), TargetSupplied: true}
	}
	if len(in.Domain) > 0 {
		return nil, &FieldValidationError{Operation: entry.SemanticID, Field: "domain"}
	}
	fields, err := a.readFields(ctx, entry, in.ProviderContext)
	if err != nil {
		return nil, err
	}
	spec := make(map[string]interface{}, len(fields))
	for _, field := range fields {
		spec[field] = map[string]interface{}{}
	}
	res, err := a.Transport.CallMethod(ctx, entry.ProviderModel, nil, "onchange", []interface{}{map[string]interface{}{}, []interface{}{}, spec}, in.ProviderContext)
	if err != nil {
		return nil, err
	}
	answer, _ := res.(map[string]interface{})
	value, _ := answer["value"].(map[string]interface{})
	record := make(map[string]interface{}, len(fields))
	for _, field := range fields {
		if v, ok := value[field]; ok {
			record[field] = v
		}
	}
	return normalizeRecords(entry.SemanticID, []map[string]interface{}{record}), nil
}

// ---- FIELD_WRITE (covers both create and update) -------------------------

func remapFields(entry Odoo19Operation, values map[string]interface{}) (map[string]interface{}, error) {
	aliases := map[string]string{}
	if entry.FieldAliasesJSON != "" {
		if err := json.Unmarshal([]byte(entry.FieldAliasesJSON), &aliases); err != nil {
			return nil, fmt.Errorf("invalid field alias table for %s: %w", entry.SemanticID, err)
		}
	}
	allowed := map[string]bool{}
	for _, f := range entry.AllowedFields {
		allowed[f] = true
	}
	// A caller-facing alias (e.g. "manager_id") is allowed exactly when its
	// provider-field target (e.g. "parent_id") is itself allowlisted — the
	// allowlist is expressed in provider field names, aliases are a stable
	// name a caller may use INSTEAD of the provider name for that same slot.
	for stable, provider := range aliases {
		if allowed[provider] {
			allowed[stable] = true
		}
	}
	// Semantic contract names are the primary consumer vocabulary: a caller
	// writes `stock_managed`, the provider is told `is_storable`. Provider
	// names stay accepted so the contract is additive, not a breaking rename.
	contract := Odoo19Contracts[entry.SemanticID]
	for _, f := range contract.Inputs {
		if allowed[f.ProviderField] {
			allowed[f.Name] = true
			if _, taken := aliases[f.Name]; !taken && f.Name != f.ProviderField {
				aliases[f.Name] = f.ProviderField
			}
		}
	}

	out := make(map[string]interface{}, len(values))
	for k, v := range values {
		if !allowed[k] {
			// The rejection has to tell the agent what it could have said
			// instead, or it cannot correct itself without reading source.
			accepted := contract.InputNames()
			if len(accepted) == 0 {
				accepted = entry.AllowedFields
			}
			return nil, &FieldValidationError{
				Operation: entry.SemanticID,
				Field:     k,
				Accepted:  accepted,
			}
		}
		providerKey := k
		if remapped, ok := aliases[k]; ok {
			providerKey = remapped
		}
		// Closed-value fields are translated from the semantic vocabulary the
		// contract publishes into the provider's own spelling, and a value
		// outside the declared set is refused here rather than becoming an
		// opaque provider rejection (or, worse, a silently-stored bad value).
		if spec, ok := contract.InputByProviderField(providerKey); ok && spec.IsEnum() {
			supplied, isString := v.(string)
			if !isString {
				return nil, &EnumValueError{
					Operation: entry.SemanticID, Field: spec.Name,
					Value: fmt.Sprintf("%v", v), Allowed: spec.EnumValues,
				}
			}
			mapped, valid := spec.ToProviderValue(supplied)
			if !valid {
				return nil, &EnumValueError{
					Operation: entry.SemanticID, Field: spec.Name,
					Value: supplied, Allowed: spec.EnumValues,
				}
			}
			v = mapped
		} else if closed, enumerated := entry.InputEnums[providerKey]; enumerated && v != nil && v != false {
			// A provider-native operation (the surface's) declares its closed values in InputEnums: a
			// selection's values, or a scope it holds a field to (a system parameter's key). They are
			// refused here as the contract's are; a scope above all, since the provider accepts any
			// key and only this check keeps a write inside the records its family reads. Clearing a
			// field (false) chooses no value, so it is the provider's to accept or refuse.
			supplied, isString := v.(string)
			if !isString || !containsString(closed, supplied) {
				return nil, &EnumValueError{
					Operation: entry.SemanticID, Field: k,
					Value: fmt.Sprintf("%v", v), Allowed: append([]string(nil), closed...),
				}
			}
		}
		if prefix, scoped := entry.InputPrefixes[providerKey]; scoped && !withinPrefix(v, prefix) {
			return nil, &PrefixValueError{Operation: entry.SemanticID, Field: k, Value: fmt.Sprintf("%v", v), Prefix: prefix}
		}
		out[providerKey] = v
	}
	return out, nil
}

// withinPrefix reports whether a value starts with the prefix an operation holds the field to,
// and, for a path prefix, is a clean path, so that "/media/../web/login" is not "under /media/".
func withinPrefix(value interface{}, prefix string) bool {
	s, ok := value.(string)
	if !ok || !strings.HasPrefix(s, prefix) {
		return false
	}
	return !strings.HasPrefix(prefix, "/") || path.Clean(s) == s
}

func (a *Adapter) ensureTargetEligible(ctx context.Context, entry Odoo19Operation, targetID int64, providerCtx map[string]interface{}) error {
	if len(entry.ProviderDomain) == 0 {
		return nil
	}
	// The same declarative field constrains reads and gates mutations, so
	// an operation cannot be pointed at a record outside the business subset it names.
	dom := make([]interface{}, 0, len(entry.ProviderDomain)+1)
	dom = append(dom, entry.ProviderDomain...)
	dom = append(dom, []interface{}{"id", "=", targetID})
	// An archived record is still in the subset the domain names: eligibility is not about being
	// active, and an update is how an archived record comes back (an alert rule odoo-cli archived).
	// A caller's own active_test still wins.
	searchCtx := map[string]interface{}{"active_test": false}
	for key, value := range providerCtx {
		searchCtx[key] = value
	}
	recs, err := a.Transport.SearchRead(ctx, entry.ProviderModel, dom, []string{"id"}, 1, "", searchCtx)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return &TargetNotEligibleError{
			Operation:      entry.SemanticID,
			ProviderModel:  entry.ProviderModel,
			TargetID:       targetID,
			ProviderDomain: entry.ProviderDomain,
		}
	}
	return nil
}

func (a *Adapter) execFieldWrite(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, string, error) {
	// The operation's declared intent decides whether this is a create or an
	// update — NOT whether the caller happened to supply a target id. An
	// UPDATE without a target must fail here, before the provider is
	// contacted, instead of falling through to a create that invents a record
	// nobody asked for and still reports success.
	switch entry.WriteMode {
	case WriteModeUpdate:
		if in.TargetID == 0 {
			return nil, "", &TargetRequirementError{
				Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false,
			}
		}
	case WriteModeCreate:
		if in.TargetID != 0 {
			return nil, "", &TargetRequirementError{
				Operation: entry.SemanticID, Mode: string(WriteModeCreate), TargetSupplied: true,
			}
		}
	}

	if entry.WriteMode == WriteModeUpdate {
		if err := a.ensureTargetEligible(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
			return nil, "", err
		}
	}

	// The same declarative Precondition that guards METHOD_CALL and WIZARD now also guards a
	// FIELD_WRITE update, so a declared invariant is enforced rather than merely documented.
	// A create has no target record to read, so it is never precondition-checked.
	if entry.WriteMode == WriteModeUpdate && entry.Precondition.Field != "" {
		if err := a.checkPrecondition(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
			return nil, "", err
		}
	}

	values, err := remapFields(entry, in.Values)
	if err != nil {
		return nil, "", err
	}
	// Values the operation's own meaning implies are applied by the binding,
	// after remapping so they cannot be overridden by a caller passing the raw
	// provider field. This is what makes a semantic verb self-contained.
	for providerField, forced := range entry.ForcedValues {
		values[providerField] = forced
	}
	if err := a.resolveLines(ctx, entry, values, in.ProviderContext); err != nil {
		return nil, "", err
	}
	// A relation field's value is validated (and, where the catalog permits
	// it, resolved from a business-facing name) BEFORE the provider ever
	// sees it, and any failure names the actual offending semantic field —
	// never target_id, which the caller may have supplied correctly.
	if err := a.resolveRelations(ctx, entry, values, in.ProviderContext); err != nil {
		return nil, "", err
	}
	if in.TargetID == 0 {
		// Creates are checked against the contract's required set before the
		// provider sees them, so the caller is told what is missing by name
		// instead of receiving an opaque provider refusal.
		if err := missingOnCreate(entry, values); err != nil {
			return nil, "", err
		}
	}
	writeCtx := withContext(in.ProviderContext, entry.WriteContext)
	if in.TargetID == 0 {
		id, err := a.Transport.Create(ctx, entry.ProviderModel, values, writeCtx)
		if err != nil {
			return nil, "", err
		}
		return map[string]interface{}{"id": id, "created": true}, fmt.Sprintf("%d", id), nil
	}
	if err := a.Transport.Write(ctx, entry.ProviderModel, []int64{in.TargetID}, values, writeCtx); err != nil {
		return nil, "", err
	}
	return map[string]interface{}{"id": in.TargetID, "updated": true}, fmt.Sprintf("%d", in.TargetID), nil
}

// methodInputs checks the caller's values of a keyword method call against MethodInputs and
// MethodRequiredInputs, before anything is sent.
func methodInputs(entry Odoo19Operation, values map[string]interface{}) (map[string]interface{}, error) {
	accepted := make(map[string]bool, len(entry.MethodInputs))
	for _, name := range entry.MethodInputs {
		accepted[name] = true
	}
	out := make(map[string]interface{}, len(values))
	for name, value := range values {
		if !accepted[name] {
			return nil, &FieldValidationError{Operation: entry.SemanticID, Field: name, Accepted: append([]string(nil), entry.MethodInputs...)}
		}
		out[name] = value
	}
	var missing []string
	for _, name := range entry.MethodRequiredInputs {
		if value, given := out[name]; !given || value == "" || value == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, &MissingFieldError{Operation: entry.SemanticID, Missing: missing, Accepted: append([]string(nil), entry.MethodInputs...), Call: true}
	}
	return out, nil
}

// callMethodKwargs calls a method that takes keyword arguments (message_post, activity_schedule):
// the caller's accepted values, relations resolved, under the operation's fixed keywords.
func (a *Adapter) callMethodKwargs(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, error) {
	kwargs, err := methodInputs(entry, in.Values)
	if err != nil {
		return nil, err
	}
	if err := a.resolveRelations(ctx, entry, kwargs, in.ProviderContext); err != nil {
		return nil, err
	}
	for name, value := range entry.MethodKwargs {
		kwargs[name] = value
	}
	caller, ok := a.Transport.(KeywordCaller)
	if !ok {
		return nil, fmt.Errorf("odoo: this transport cannot call %s with keyword arguments", entry.Method)
	}
	return caller.CallMethodKwargs(ctx, entry.ProviderModel, []int64{in.TargetID}, entry.Method, kwargs, in.ProviderContext)
}

// withContext is the caller's provider context with fixed over it: the operation's own keys win.
func withContext(caller, fixed map[string]interface{}) map[string]interface{} {
	if len(fixed) == 0 {
		return caller
	}
	out := make(map[string]interface{}, len(caller)+len(fixed))
	for key, value := range caller {
		out[key] = value
	}
	for key, value := range fixed {
		out[key] = value
	}
	return out
}

// lineEntry is the operation a lines field's every line is checked against: its own allowlist, typing
// and relations, and no semantic contract (a line's fields are provider fields).
func lineEntry(entry Odoo19Operation, field string, spec LineSpec) Odoo19Operation {
	return Odoo19Operation{SemanticID: entry.SemanticID + "/" + field, ProviderModel: entry.ProviderModel,
		AllowedFields: spec.AllowedFields, InputTypes: spec.InputTypes, Relations: spec.Relations}
}

// linesOf is a lines field's value as its lines: a non-empty list of objects.
func linesOf(entry Odoo19Operation, field string, raw interface{}) ([]map[string]interface{}, error) {
	list, ok := raw.([]interface{})
	if !ok || len(list) == 0 {
		return nil, &LinesError{Operation: entry.SemanticID, Field: field, Line: -1, Cause: fmt.Errorf("got %T", raw)}
	}
	lines := make([]map[string]interface{}, 0, len(list))
	for i, item := range list {
		line, ok := item.(map[string]interface{})
		if !ok {
			return nil, &LinesError{Operation: entry.SemanticID, Field: field, Line: -1, Cause: fmt.Errorf("line %d is a %T", i, item)}
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// checkLines checks every line of the lines fields against its allowlist, before anything is sent.
func checkLines(entry Odoo19Operation, values map[string]interface{}) error {
	for field, spec := range entry.LineInputs {
		raw, present := values[field]
		if !present {
			continue
		}
		lines, err := linesOf(entry, field, raw)
		if err != nil {
			return err
		}
		for i, line := range lines {
			if _, err := remapFields(lineEntry(entry, field, spec), line); err != nil {
				return &LinesError{Operation: entry.SemanticID, Field: field, Line: i, Cause: err}
			}
		}
	}
	return nil
}

// resolveLines checks each line of the lines fields, resolves its relations (an account by its code),
// and writes the lines as Odoo's (0, 0, {...}) commands. An error names the line: line_ids[1].
func (a *Adapter) resolveLines(ctx context.Context, entry Odoo19Operation, values map[string]interface{}, providerCtx map[string]interface{}) error {
	for field, spec := range entry.LineInputs {
		raw, present := values[field]
		if !present {
			continue
		}
		lines, err := linesOf(entry, field, raw)
		if err != nil {
			return err
		}
		line := lineEntry(entry, field, spec)
		commands := make([]interface{}, 0, len(lines))
		for i, rawLine := range lines {
			lineValues, err := remapFields(line, rawLine)
			if err == nil {
				err = a.resolveRelations(ctx, line, lineValues, providerCtx)
			}
			if err != nil {
				return &LinesError{Operation: entry.SemanticID, Field: field, Line: i, Cause: err}
			}
			commands = append(commands, []interface{}{0, 0, lineValues})
		}
		values[field] = commands
	}
	return nil
}

// ---- METHOD_CALL ----------------------------------------------------------

func (a *Adapter) execMethodCall(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, error) {
	if in.TargetID == 0 {
		return nil, &TargetRequirementError{
			Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false,
		}
	}
	if err := a.ensureTargetEligible(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
		return nil, err
	}
	if err := a.checkPrecondition(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
		return nil, err
	}
	var res interface{}
	var err error
	if len(entry.MethodKwargs) > 0 || len(entry.MethodInputs) > 0 {
		res, err = a.callMethodKwargs(ctx, entry, in)
	} else {
		res, err = a.Transport.CallMethod(ctx, entry.ProviderModel, []int64{in.TargetID}, entry.Method, entry.MethodArgs, in.ProviderContext)
	}
	if err != nil {
		return nil, err
	}
	// A nil/void Python return (e.g. crm.lead.action_set_lost) is a valid
	// success — report it as such rather than treating absence of a result
	// body as failure. This is the concrete fix for the LAB CLI's known
	// "unexpected end of JSON input" defect.
	if res == nil {
		return map[string]interface{}{"id": in.TargetID, "method": entry.Method, "result": nil, "note": "provider method returned void; treated as success"}, nil
	}
	// A dialog is a question, not a result (provider_dialog.go): reporting it as success would claim
	// a cancel or a validate that did not happen.
	if dialog, ok := dialogOf(res); ok {
		if note, after := followUpDialogs[dialog.Model]; after {
			return map[string]interface{}{"id": in.TargetID, "created_id": in.TargetID, "method": entry.Method, "result": res,
				"follow_up_dialog": followUpDetail(dialog, note)}, nil
		}
		return nil, a.dialogError(entry, in.TargetID, dialog)
	}
	// Some methods (e.g. purchase.order.action_create_invoice) also return
	// an ir.actions.act_window descriptor with the new record's id in
	// "res_id" — surface it the same way execWizard does.
	createdID := in.TargetID
	if m, ok := res.(map[string]interface{}); ok {
		if ridf, ok := m["res_id"].(float64); ok && ridf > 0 {
			createdID = int64(ridf)
		}
	}
	return map[string]interface{}{"id": in.TargetID, "created_id": createdID, "method": entry.Method, "result": res}, nil
}

// ---- WIZARD -----------------------------------------------------------------

func (a *Adapter) execWizard(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, string, error) {
	if entry.WizardStandalone {
		return a.execStandaloneWizard(ctx, entry, in)
	}
	if in.TargetID == 0 {
		return nil, "", &TargetRequirementError{
			Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false,
		}
	}
	if err := a.ensureTargetEligible(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
		return nil, "", err
	}
	// A WIZARD strategy hand-builds a provider side-effect that a native
	// Odoo action would normally gate itself (e.g. account.move's own
	// action_register_payment refuses a draft document). Building the
	// wizard directly does not inherit that gate for free — it must be
	// checked here, before the provider is contacted at all.
	if err := a.checkPrecondition(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
		return nil, "", err
	}

	wizardValues, err := wizardCallerValues(entry, in.Values)
	if err != nil {
		return nil, "", err
	}
	if entry.WizardOpenerMethod != "" {
		return a.execOpenedWizard(ctx, entry, in, wizardValues)
	}
	providerCtx := in.ProviderContext
	if entry.WizardActiveContext {
		// Mirrors the context Odoo's own button opens the wizard with: the active record, then what
		// the button and its action add; caller-supplied keys win.
		providerCtx = map[string]interface{}{"active_model": entry.ProviderModel, "active_id": in.TargetID, "active_ids": []interface{}{in.TargetID}}
		for key, value := range entry.WizardContext {
			providerCtx[key] = value
		}
		for key, binding := range entry.WizardContextTarget {
			if binding == "ids" {
				providerCtx[key] = []interface{}{in.TargetID}
			} else {
				providerCtx[key] = in.TargetID
			}
		}
		for key, value := range in.ProviderContext {
			providerCtx[key] = value
		}
	}

	var linkIDs []int64
	if entry.WizardLinkSourceModel == "" {
		linkIDs = []int64{in.TargetID}
	} else {
		dom := []interface{}{[]interface{}{entry.WizardLinkSourceDomainField, "=", in.TargetID}}
		for _, clause := range entry.WizardLinkSourceExtraDomain {
			dom = append(dom, []interface{}(clause))
		}
		recs, err := a.Transport.SearchRead(ctx, entry.WizardLinkSourceModel, dom, []string{"id"}, 0, "", in.ProviderContext)
		if err != nil {
			return nil, "", err
		}
		for _, r := range recs {
			if idf, ok := r["id"].(float64); ok {
				linkIDs = append(linkIDs, int64(idf))
			}
		}
		if len(linkIDs) == 0 {
			// Empirically the safe outcome for retrying finance.payment.register
			// against a document that already has no open balance (already
			// settled, or double-submitted): fail loudly with zero provider
			// mutation, classified as a business precondition rather than an
			// opaque provider error, rather than silently finding nothing to
			// link and erroring on the resulting empty wizard.
			return nil, "", &NoEligibleWizardLinkError{
				Operation: entry.SemanticID, SourceModel: entry.WizardLinkSourceModel, TargetID: in.TargetID,
			}
		}
	}

	values := map[string]interface{}{}
	for k, v := range entry.WizardValues {
		values[k] = v
	}
	if entry.WizardActiveContext && len(entry.WizardAllowedFields) > 0 {
		// What the web client shows prefilled when it opens the wizard: onchange computes defaults
		// default_get alone does not (account.move.reversal's journal_id). The caller's values win.
		for field, value := range a.wizardOnchangeDefaults(ctx, entry, providerCtx) {
			if _, set := values[field]; !set {
				values[field] = value
			}
		}
	}
	for k, v := range wizardValues {
		values[k] = v
	}
	switch {
	case entry.WizardLinkField == "":
		// Linked through the active context only.
	case entry.WizardLinkMulti:
		idArgsAny := make([]interface{}, len(linkIDs))
		for i, id := range linkIDs {
			idArgsAny[i] = id
		}
		values[entry.WizardLinkField] = []interface{}{[]interface{}{6, 0, idArgsAny}}
	default:
		values[entry.WizardLinkField] = linkIDs[0]
	}

	wizardID, err := a.Transport.Create(ctx, entry.WizardModel, values, providerCtx)
	if err != nil {
		return nil, "", err
	}
	res, err := a.Transport.CallMethod(ctx, entry.WizardModel, []int64{wizardID}, entry.WizardMethod, nil, providerCtx)
	if err != nil {
		return nil, "", err
	}
	return a.wizardOutcome(entry, in.TargetID, wizardID, res)
}

// execStandaloneWizard runs a wizard no record opens (res.config.settings and its execute button).
// The caller's values make the wizard; every other field takes what Odoo shows on opening it, so
// applying the wizard changes only what the caller set. A dialog it answers with fails the
// operation, as for any wizard.
func (a *Adapter) execStandaloneWizard(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, string, error) {
	if in.TargetID != 0 {
		return nil, "", &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeCreate), TargetSupplied: true}
	}
	callerValues, err := wizardCallerValues(entry, in.Values)
	if err != nil {
		return nil, "", err
	}
	if len(callerValues) == 0 {
		return nil, "", &MissingFieldError{Operation: entry.SemanticID, Missing: []string{"values"}, Accepted: append([]string(nil), entry.WizardAllowedFields...), Call: true}
	}
	values := map[string]interface{}{}
	for k, v := range entry.WizardValues {
		values[k] = v
	}
	for k, v := range callerValues {
		values[k] = v
	}
	wizardID, err := a.Transport.Create(ctx, entry.WizardModel, values, in.ProviderContext)
	if err != nil {
		return nil, "", err
	}
	res, err := a.Transport.CallMethod(ctx, entry.WizardModel, []int64{wizardID}, entry.WizardMethod, nil, in.ProviderContext)
	if err != nil {
		return nil, "", err
	}
	out, _, err := a.wizardOutcome(entry, 0, wizardID, res)
	if err != nil {
		return nil, "", err
	}
	delete(out, "target_id")
	delete(out, "created_id")
	return out, fmt.Sprintf("%d", wizardID), nil
}

// wizardOutcome is what a confirmed wizard did. A dialog the wizard answers with is a question, not
// a result: the operation fails with it, unless Odoo offers that dialog after acting.
func (a *Adapter) wizardOutcome(entry Odoo19Operation, targetID, wizardID int64, res interface{}) (map[string]interface{}, string, error) {
	out := map[string]interface{}{"wizard_model": entry.WizardModel, "wizard_id": wizardID, "target_id": targetID, "created_id": targetID, "result": res}
	if dialog, ok := dialogOf(res); ok {
		note, after := followUpDialogs[dialog.Model]
		if !after {
			return nil, "", a.dialogError(entry, targetID, dialog)
		}
		out["follow_up_dialog"] = followUpDetail(dialog, note)
		return out, fmt.Sprintf("%d", targetID), nil
	}
	// Many Odoo wizard methods (e.g. sale.advance.payment.inv.create_invoices)
	// return an ir.actions.act_window descriptor pointing at the record they
	// just created, with the new id in "res_id" — surface it generically as
	// "created_id" so a caller can chain into it (e.g. finance.invoice.post)
	// without knowing this is wizard-specific Odoo convention.
	createdID := targetID
	if m, ok := res.(map[string]interface{}); ok {
		if ridf, ok := m["res_id"].(float64); ok && ridf > 0 {
			createdID = int64(ridf)
		}
	}
	out["created_id"] = createdID
	return out, fmt.Sprintf("%d", createdID), nil
}

// maxAnsweredDialogs bounds a chain of dialogs an opened wizard answers: Odoo's longest known chain
// is two (a manufacturing order's consumption warning, then its backorder question).
const maxAnsweredDialogs = 4

// execOpenedWizard calls the operation's business method the way the web client's button does and
// answers the dialogs it responds with: each opened in the context Odoo gave it and confirmed with
// the button the operation names for it, until Odoo answers with something other than a dialog.
// Only the operation's own dialog (WizardModel) takes the operation's values and the caller's. A
// method that acts without asking (a transfer with nothing left over needs no backorder decision)
// has done the operation when WizardOpenerActs says it does.
func (a *Adapter) execOpenedWizard(ctx context.Context, entry Odoo19Operation, in OperationInput, callerValues map[string]interface{}) (interface{}, string, error) {
	target := fmt.Sprintf("%d", in.TargetID)
	answer, err := a.Transport.CallMethod(ctx, entry.ProviderModel, []int64{in.TargetID}, entry.WizardOpenerMethod, nil, in.ProviderContext)
	if err != nil {
		return nil, "", err
	}
	var answered []string
	var wizardID int64
	for {
		dialog, asked := dialogOf(answer)
		if !asked {
			if len(answered) == 0 {
				if !entry.WizardOpenerActs {
					return nil, "", &DialogNotOpenedError{Operation: entry.SemanticID, ProviderModel: entry.ProviderModel,
						TargetID: in.TargetID, Opener: entry.WizardOpenerMethod, DialogModel: entry.WizardModel}
				}
				return map[string]interface{}{"target_id": in.TargetID, "created_id": in.TargetID, "method": entry.WizardOpenerMethod,
					"dialog": DialogNotAsked, "result": answer}, target, nil
			}
			out, created, err := a.wizardOutcome(entry, in.TargetID, wizardID, answer)
			if err != nil {
				return nil, "", err
			}
			// The dialog answered last, which is not the operation's own when Odoo did not ask it.
			out["wizard_model"] = answered[len(answered)-1]
			out["method"], out["dialog"], out["answered_dialogs"] = entry.WizardOpenerMethod, DialogCompleted, answered
			return out, created, nil
		}
		button := entry.WizardAlsoAnswers[dialog.Model]
		if dialog.Model == entry.WizardModel {
			button = entry.WizardMethod
		}
		if button == "" || len(answered) == maxAnsweredDialogs || containsString(answered, dialog.Model) {
			if note, after := followUpDialogs[dialog.Model]; after && (len(answered) > 0 || entry.WizardOpenerActs) {
				state := DialogNotAsked
				if len(answered) > 0 {
					state = DialogCompleted
				}
				return map[string]interface{}{"target_id": in.TargetID, "created_id": in.TargetID, "method": entry.WizardOpenerMethod,
					"dialog": state, "answered_dialogs": answered, "result": answer, "follow_up_dialog": followUpDetail(dialog, note)}, target, nil
			}
			refusal := a.dialogError(entry, in.TargetID, dialog)
			refusal.ResolveWith = a.dialogResolvers(entry.ProviderModel, append(append([]string(nil), answered...), dialog.Model)...)
			return nil, "", refusal
		}
		if wizardID, answer, err = a.answerDialog(ctx, entry, in, dialog, button, callerValues); err != nil {
			return nil, "", err
		}
		answered = append(answered, dialog.Model)
	}
}

// answerDialog opens one dialog in the context Odoo gave it and presses one of its buttons.
func (a *Adapter) answerDialog(ctx context.Context, entry Odoo19Operation, in OperationInput, dialog providerDialog, button string, callerValues map[string]interface{}) (int64, interface{}, error) {
	providerCtx, err := dialogContext(entry.ProviderModel, in.TargetID, dialog)
	if err != nil {
		refusal := a.dialogError(entry, in.TargetID, dialog)
		refusal.Reason = err.Error()
		return 0, nil, refusal
	}
	for key, value := range in.ProviderContext {
		providerCtx[key] = value
	}
	values := map[string]interface{}{}
	if dialog.Model == entry.WizardModel {
		for key, value := range entry.WizardValues {
			values[key] = value
		}
		if dialog.ResID == 0 && len(entry.WizardAllowedFields) > 0 {
			// What the web client shows prefilled in the dialog; the caller's values win.
			for field, value := range a.wizardOnchangeDefaults(ctx, entry, providerCtx) {
				if _, set := values[field]; !set {
					values[field] = value
				}
			}
		}
		for key, value := range callerValues {
			values[key] = value
		}
	}
	wizardID := dialog.ResID
	if wizardID > 0 {
		// Odoo created the dialog's record itself (validate.account.move): fill that one in.
		if len(values) > 0 {
			if err := a.Transport.Write(ctx, dialog.Model, []int64{wizardID}, values, providerCtx); err != nil {
				return 0, nil, err
			}
		}
	} else if wizardID, err = a.Transport.Create(ctx, dialog.Model, values, providerCtx); err != nil {
		return 0, nil, err
	}
	res, err := a.Transport.CallMethod(ctx, dialog.Model, []int64{wizardID}, button, nil, providerCtx)
	return wizardID, res, err
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// ---- DESTRUCTIVE_GUARDED -----------------------------------------------------

// destructiveImpactError is a typed sentinel so Execute() can distinguish a
// deliberate ASAO-side refusal from an actual provider error and attach a
// structured impact plan to the failed ExecutionResult.
//
// It implements StructuredError (ErrorClass/StructuredDetail below)
// so the SAME generic error-to-JSON path every other catalog error goes
// through (operation_usecase.go's errors.As(err, &structured)) carries
// dependent_count and a sample of the actual dependent records into the
// caller-facing payload. Previously it did not implement that interface, so
// the refusal reached the caller as a bare class name with no way to learn
// the blast radius short of authorizing it.
// wizardOnchangeDefaults asks the provider for the values the web client would prefill when opening
// the wizard (Model.onchange with no values), and returns those of the fields a caller may supply.
// Like the web client it asks for every plain field of the wizard at once: a default can depend on
// a field the caller does not set (change.production.qty's product_qty is the order's quantity,
// found through mo_id), and asked alone it comes back zero, which would then overwrite what Odoo
// itself fills in. It returns only scalar and many2one values; a provider that cannot answer yields
// none, and the wizard's own required-field check then names what is missing.
func (a *Adapter) wizardOnchangeDefaults(ctx context.Context, entry Odoo19Operation, providerCtx map[string]interface{}) map[string]interface{} {
	spec := map[string]interface{}{}
	// fields_get is a model method: the empty id list stands for allfields.
	if res, err := a.Transport.CallMethod(ctx, entry.WizardModel, nil, "fields_get", []interface{}{[]interface{}{"type"}}, providerCtx); err == nil {
		fields, _ := res.(map[string]interface{})
		for name, raw := range fields {
			attributes, _ := raw.(map[string]interface{})
			switch attributes["type"] {
			case "one2many", "many2many", "binary", "properties", "properties_definition", "json":
				continue
			}
			spec[name] = map[string]interface{}{}
		}
	}
	allowed := make(map[string]bool, len(entry.WizardAllowedFields))
	for _, field := range entry.WizardAllowedFields {
		allowed[field] = true
		spec[field] = map[string]interface{}{}
	}
	res, err := a.Transport.CallMethod(ctx, entry.WizardModel, nil, "onchange", []interface{}{map[string]interface{}{}, []interface{}{}, spec}, providerCtx)
	if err != nil {
		return nil
	}
	answer, _ := res.(map[string]interface{})
	value, _ := answer["value"].(map[string]interface{})
	out := map[string]interface{}{}
	for field, raw := range value {
		if !allowed[field] {
			continue
		}
		switch v := raw.(type) {
		case string, float64:
			out[field] = v
		case bool:
			if v {
				out[field] = v
			}
		case map[string]interface{}:
			if id, ok := v["id"].(float64); ok && id > 0 {
				out[field] = id
			}
		}
	}
	return out
}

// wizardCallerValues returns the caller-supplied wizard values an operation accepts. With no
// WizardAllowedFields the caller's values are not passed on, as before; with them, a value outside
// the list is refused before the provider is contacted.
func wizardCallerValues(entry Odoo19Operation, values map[string]interface{}) (map[string]interface{}, error) {
	if len(entry.WizardAllowedFields) == 0 {
		return nil, nil
	}
	allowed := make(map[string]bool, len(entry.WizardAllowedFields))
	for _, field := range entry.WizardAllowedFields {
		allowed[field] = true
	}
	out := make(map[string]interface{}, len(values))
	for field, value := range values {
		if !allowed[field] || field == entry.WizardLinkField {
			return nil, &FieldValidationError{Operation: entry.SemanticID, Field: field, Accepted: append([]string(nil), entry.WizardAllowedFields...)}
		}
		out[field] = value
	}
	return out, nil
}

type destructiveImpactError struct {
	ProviderModel   string                   `json:"provider_model"`
	ImpactModel     string                   `json:"impact_model"`
	ImpactField     string                   `json:"impact_field"`
	ImpactCount     int64                    `json:"impact_count"`
	TargetID        int64                    `json:"target_id"`
	Message         string                   `json:"message"`
	Dependencies    []map[string]interface{} `json:"dependencies,omitempty"`
	ImpactBreakdown map[string]int64         `json:"impact_breakdown,omitempty"`
}

var _ StructuredError = (*destructiveImpactError)(nil)

func (e *destructiveImpactError) Error() string { return e.Message }

func (e *destructiveImpactError) ErrorClass() string {
	return string(ErrClassDestructiveImpactRequiresConfirm)
}

func (e *destructiveImpactError) StructuredDetail() map[string]interface{} {
	detail := map[string]interface{}{
		"provider_model":  e.ProviderModel,
		"impact_model":    e.ImpactModel,
		"impact_field":    e.ImpactField,
		"dependent_count": e.ImpactCount,
		"target_id":       e.TargetID,
	}
	if len(e.Dependencies) > 0 {
		detail["dependencies"] = e.Dependencies
	}
	if len(e.ImpactBreakdown) > 0 {
		detail["impact_breakdown"] = e.ImpactBreakdown
	}
	return detail
}

func (a *Adapter) execDestructiveGuarded(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, error) {
	if in.TargetID == 0 {
		return nil, &TargetRequirementError{
			Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false,
		}
	}
	if err := a.ensureTargetEligible(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
		return nil, err
	}
	// The primary ImpactModel/ImpactField plus every AdditionalImpacts entry
	// are probed together: a provider that cascade-deletes more than one
	// dependent kind (e.g. project.project cascades BOTH project.task and
	// project.milestone) must have every one of those kinds checked, or a
	// target with zero of the primary kind but live records of an
	// additional kind reports dependent_count:0 and deletes them unconfirmed
	// — which is exactly the gap found independently on work.project.delete
	// before AdditionalImpacts existed: it declared only project.task.
	// An operation with no ImpactModel has nothing that points at its records (website.media: pages
	// name a file by its url); its ProviderDomain is the whole guard.
	var checks []ImpactSpec
	if entry.ImpactModel != "" {
		checks = append(checks, ImpactSpec{Model: entry.ImpactModel, Field: entry.ImpactField})
	}
	checks = append(checks, entry.AdditionalImpacts...)
	// Archived dependents count too: Odoo's searches skip them by default, but a delete strands or
	// cascades them all the same (a project takes its archived tasks with it; a lost lead, which
	// Odoo archives, keeps pointing at its lost reason).
	everyRecord := map[string]interface{}{"active_test": false}
	for key, value := range in.ProviderContext {
		if key != "active_test" {
			everyRecord[key] = value
		}
	}
	var (
		total     int64
		deps      []map[string]interface{}
		breakdown = map[string]int64{}
	)
	for _, chk := range checks {
		count, err := a.Transport.SearchCount(ctx, chk.Model, []interface{}{[]interface{}{chk.Field, "=", in.TargetID}}, everyRecord)
		if err != nil {
			return nil, err
		}
		total += count
		breakdown[chk.Model] += count
		if count == 0 || in.ConfirmImpact {
			continue
		}
		// Best-effort: a small sample of the actual dependent records, so the
		// refusal is something a caller can plan against rather than merely a
		// number. display_name is present on essentially every Odoo model, but
		// this is deliberately non-fatal — a model where it is unavailable
		// still returns the count, which is the acceptance-critical part.
		if recs, sampleErr := a.Transport.SearchRead(ctx, chk.Model,
			[]interface{}{[]interface{}{chk.Field, "=", in.TargetID}},
			[]string{"id", "display_name"}, 5, "", everyRecord); sampleErr == nil {
			for _, r := range recs {
				deps = append(deps, map[string]interface{}{
					"type": chk.Model, "id": r["id"], "display_name": r["display_name"],
				})
			}
		}
	}
	if total > 0 && !in.ConfirmImpact {
		return nil, &destructiveImpactError{
			ProviderModel: entry.ProviderModel, ImpactModel: entry.ImpactModel, ImpactField: entry.ImpactField,
			ImpactCount: total, TargetID: in.TargetID, Dependencies: deps, ImpactBreakdown: breakdown,
			Message: fmt.Sprintf("refusing to delete %s(%d): %d dependent record(s) reference it (%v); provider itself would not block this — resubmit with confirm_impact=true to proceed anyway", entry.ProviderModel, in.TargetID, total, breakdown),
		}
	}
	if err := a.Transport.Unlink(ctx, entry.ProviderModel, []int64{in.TargetID}, in.ProviderContext); err != nil {
		return nil, err
	}
	return map[string]interface{}{"id": in.TargetID, "deleted": true, "impact_count_at_time_of_delete": total, "impact_confirmed": in.ConfirmImpact}, nil
}

// ---- REPORT ---------------------------------------------------------------

// execReport renders the target's PDF report (ReportName) as the web client's Print menu does and
// returns it base64-encoded, with its size and SHA-256, named after the record.
func (a *Adapter) execReport(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, error) {
	if in.TargetID == 0 {
		return nil, &TargetRequirementError{Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false}
	}
	if err := a.ensureTargetEligible(ctx, entry, in.TargetID, in.ProviderContext); err != nil {
		return nil, err
	}
	renderer, ok := a.Transport.(ReportRenderer)
	if !ok {
		return nil, fmt.Errorf("odoo: this transport cannot render reports")
	}
	name := fmt.Sprintf("%s-%d", entry.ProviderModel, in.TargetID)
	if recs, err := a.Transport.Read(ctx, entry.ProviderModel, []int64{in.TargetID}, []string{"display_name"}, in.ProviderContext); err == nil && len(recs) == 1 {
		if display, _ := recs[0]["display_name"].(string); strings.TrimSpace(display) != "" {
			name = display
		}
	}
	pdf, err := renderer.RenderReport(ctx, entry.ReportName, []int64{in.TargetID})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pdf)
	return map[string]interface{}{
		"id": in.TargetID, "report": entry.ReportName, "mimetype": "application/pdf",
		"filename": reportFileName(name), "size": len(pdf), "sha256": hex.EncodeToString(sum[:]),
		"content": base64.StdEncoding.EncodeToString(pdf),
	}, nil
}

// reportFileName is a record's name as a file name: INV/2026/00017 -> INV_2026_00017.pdf.
func reportFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' || r == ' ':
			b.WriteRune('_')
		case r < 32:
		default:
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "._") + ".pdf"
}

// ---- MODULE_INSTALL ----------------------------------------------------------

func (a *Adapter) execModuleInstall(ctx context.Context, entry Odoo19Operation, in OperationInput) (interface{}, error) {
	if in.TargetID == 0 {
		return nil, &TargetRequirementError{
			Operation: entry.SemanticID, Mode: string(WriteModeUpdate), TargetSupplied: false,
		}
	}
	model := entry.ProviderModel
	if model == "" {
		model = "ir.module.module"
	}
	method := entry.Method
	if method == "" {
		method = "button_immediate_install"
	}

	recs, err := a.Transport.Read(ctx, model, []int64{in.TargetID}, []string{"name", "state"}, in.ProviderContext)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, &SingleRecordNotFoundError{
			Operation: entry.SemanticID, ProviderModel: model, TargetID: in.TargetID,
		}
	}

	name, _ := recs[0]["name"].(string)
	state, _ := recs[0]["state"].(string)

	if state == "installed" {
		return map[string]interface{}{
			"module":  name,
			"state":   "installed",
			"changed": false,
		}, nil
	}

	if err := a.callModuleInstall(ctx, model, method, in); err != nil {
		return nil, err
	}

	recs, err = a.Transport.Read(ctx, model, []int64{in.TargetID}, []string{"name", "state"}, in.ProviderContext)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, &SingleRecordNotFoundError{
			Operation: entry.SemanticID, ProviderModel: model, TargetID: in.TargetID,
		}
	}

	name, _ = recs[0]["name"].(string)
	state, _ = recs[0]["state"].(string)

	if state == "installed" {
		return map[string]interface{}{
			"module":  name,
			"state":   "installed",
			"changed": true,
		}, nil
	}

	return nil, &ModuleInstallStateError{
		Operation: entry.SemanticID,
		Module:    name,
		State:     state,
	}
}

// defaultModuleBusyWaits are the waits between module install attempts that Odoo refused while a
// scheduled action ran: about a minute in all, the time a fresh database's first scheduled actions take.
var defaultModuleBusyWaits = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second}

// callModuleInstall calls the install method, and calls it again after a wait when Odoo refused it
// without changing anything. Odoo refuses every module operation while a scheduled action holds the
// cron table ("Odoo is currently processing a scheduled action", in the user's language), and a
// fresh database runs its first scheduled actions right after its first installs. That refusal comes
// before the module is marked: a module still uninstalled after a plain provider refusal is that
// case, or another refusal made before any change, so calling again is safe. A module the call left
// in any other state, or a refusal of another class (access, validation), is reported at once.
func (a *Adapter) callModuleInstall(ctx context.Context, model, method string, in OperationInput) error {
	waits := a.ModuleBusyWaits
	if waits == nil {
		waits = defaultModuleBusyWaits
	}
	for attempt := 0; ; attempt++ {
		_, err := a.Transport.CallMethod(WithLongCall(ctx, 110*time.Second), model, []int64{in.TargetID}, method, nil, in.ProviderContext)
		if err == nil {
			return nil
		}
		var refused *OdooError
		if attempt >= len(waits) || !errors.As(err, &refused) || ClassifyOdooError(err.Error()) != ErrClassProviderError {
			return err
		}
		recs, readErr := a.Transport.Read(ctx, model, []int64{in.TargetID}, []string{"state"}, in.ProviderContext)
		if readErr != nil || len(recs) == 0 || recs[0]["state"] != "uninstalled" {
			return err
		}
		timer := time.NewTimer(waits[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}

// checkGroupSpecs refuses a grouped read that names a field outside shown, an unknown date
// granularity or aggregate function, or a target.
func checkGroupSpecs(entry Odoo19Operation, in OperationInput, shown []string) error {
	if in.TargetID != 0 {
		return &GroupSpecError{Operation: entry.SemanticID, Spec: "target_id", Reason: "a grouped read groups every record its domain matches, not one target"}
	}
	known := make(map[string]bool, len(shown))
	for _, field := range shown {
		known[field] = true
	}
	for _, spec := range in.GroupBy {
		field, granularity, bucketed := strings.Cut(spec, ":")
		switch {
		case !known[field]:
			return &GroupSpecError{Operation: entry.SemanticID, Spec: spec, Reason: "the read does not show this field", Accepted: shown}
		case bucketed && !groupGranularities[granularity]:
			return &GroupSpecError{Operation: entry.SemanticID, Spec: spec, Reason: "unknown date granularity " + strconv.Quote(granularity)}
		}
	}
	for _, spec := range in.Aggregates {
		field, function, ok := strings.Cut(spec, ":")
		switch {
		case !ok || !groupFunctions[function]:
			return &GroupSpecError{Operation: entry.SemanticID, Spec: spec, Reason: "an aggregate is field:function, the function one of sum, avg, min, max, count, count_distinct, bool_and, bool_or"}
		case !known[field]:
			return &GroupSpecError{Operation: entry.SemanticID, Spec: spec, Reason: "the read does not show this field", Accepted: shown}
		}
	}
	return nil
}
