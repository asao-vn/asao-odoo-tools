package odoo

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
)

// A provider dialect makes one Odoo series answer in the vocabulary the catalog is written in: the
// 19.0 baseline. The catalog, the semantic contracts, normalization and verification all name 19.0
// fields; a dialect translates at the transport boundary (ClientTransport), so a caller of any
// operation sees the same fields and the same meaning whichever series answered, and nothing above
// the transport knows a second series exists.
//
// A dialect only covers what was measured. Each entry below traces to a field the 20.0 schema
// removed, checked against the 20.0 source and a live 20.0-20260926 lab on 2026-10-10; the renames
// are generated from the two series' inventories (odoo20_dialect_renames.gen.go) and merged at init.
// A field a dialect does not mention is assumed unchanged; the conformance run for the series is
// what proves that assumption per operation, and until it does every operation on the series reads
// UNVERIFIED (ReadinessFor).
type providerDialect struct {
	series string
	models map[string]modelDialect
	// absentModels are baseline models the series no longer has, with what became of them. An
	// operation on one is refused before the provider is contacted.
	absentModels map[string]string
}

// modelDialect is what one provider model changed in a series.
type modelDialect struct {
	// renamed maps a baseline field to the series field that carries the same value.
	renamed map[string]string
	// derived are baseline fields the series removed, rebuilt from what it kept.
	derived map[string]derivedField
	// writeRules are baseline fields that still exist but whose writes the series treats
	// differently. A rule may rewrite the values or refuse them.
	writeRules map[string]writeRule
}

// writeMode distinguishes create from write: a series may honour a value on one and recompute it
// on the other.
type writeMode string

const (
	writeModeCreate writeMode = "create"
	writeModeWrite  writeMode = "write"
)

// writeRule translates one baseline field of a create or write. values holds every other value of
// the same call, in series vocabulary, so a rule can depend on them (a VAT number, a parent).
// It returns the series values to merge in; an empty result drops the field.
type writeRule func(mode writeMode, value interface{}, values map[string]interface{}) (map[string]interface{}, error)

type derivedField struct {
	// needs are the series fields the baseline value is computed from; they are read in its place.
	needs []string
	// read sets the baseline field on every record. The records carry needs.
	read func(ctx context.Context, t *ClientTransport, records []map[string]interface{}, field string) error
	// write translates the baseline field of a create or write; nil refuses the write.
	write writeRule
	// filter translates one domain leaf on the baseline field; nil refuses the filter.
	filter func(operator string, value interface{}) ([]interface{}, error)
	// why is the reason given when a write, filter or sort on the field is refused.
	why string
}

// DialectRefusalError is a VALIDATION_FAILED raised before the provider is contacted, when a
// caller asks a series for something its schema can no longer express with the baseline meaning.
type DialectRefusalError struct {
	Series string
	Model  string
	Field  string
	Reason string
}

func (e *DialectRefusalError) Error() string {
	return fmt.Sprintf("odoo %s cannot express %s.%s with its 19.0 meaning: %s", e.Series, e.Model, e.Field, e.Reason)
}

func (e *DialectRefusalError) ErrorClass() string { return string(ErrClassValidationFailed) }

func (e *DialectRefusalError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"provider_series": e.Series,
		"provider_model":  e.Model,
		"field":           e.Field,
		"reason":          e.Reason,
	}
}

var _ StructuredError = (*DialectRefusalError)(nil)

// ModelUnavailableError refuses an operation on a model the provider's series removed. It is
// raised before the provider is contacted.
type ModelUnavailableError struct {
	Series string
	Model  string
	Reason string
}

func (e *ModelUnavailableError) Error() string {
	return fmt.Sprintf("odoo %s has no %s: %s", e.Series, e.Model, e.Reason)
}

func (e *ModelUnavailableError) ErrorClass() string {
	return string(ErrClassOperationUnavailableOnSeries)
}

func (e *ModelUnavailableError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{
		"provider_series": e.Series,
		"provider_model":  e.Model,
		"reason":          e.Reason,
	}
}

var _ StructuredError = (*ModelUnavailableError)(nil)

// checkModel refuses a model the series removed.
// SeriesFeatureError refuses a feature this build does not translate for a candidate series yet
// (grouped reads on 20.0). Nothing was sent to the provider.
type SeriesFeatureError struct {
	Series  string
	Feature string
}

func (e *SeriesFeatureError) Error() string {
	return fmt.Sprintf("%s are not translated for odoo %s yet", e.Feature, e.Series)
}

func (e *SeriesFeatureError) ErrorClass() string {
	return string(ErrClassOperationUnavailableOnSeries)
}

func (e *SeriesFeatureError) StructuredDetail() map[string]interface{} {
	return map[string]interface{}{"provider_series": e.Series, "feature": e.Feature}
}

func (d *providerDialect) checkModel(model string) error {
	if d == nil {
		return nil
	}
	if reason, absent := d.absentModels[model]; absent {
		return &ModelUnavailableError{Series: d.series, Model: model, Reason: reason}
	}
	return nil
}

// dialectFor returns the dialect of series, or nil for the baseline series (no translation).
func dialectFor(series string) *providerDialect {
	if series == Odoo20ProviderSeries {
		return odoo20Dialect
	}
	return nil
}

func (d *providerDialect) model(model string) (modelDialect, bool) {
	if d == nil {
		return modelDialect{}, false
	}
	m, ok := d.models[model]
	return m, ok
}

func (d *providerDialect) refuse(model, field, reason string) error {
	return &DialectRefusalError{Series: d.series, Model: model, Field: field, Reason: reason}
}

// fieldsOut translates a read projection. requested is nil when fields is empty (every field).
func (d *providerDialect) fieldsOut(model string, fields []string) (out []string, requested map[string]bool) {
	m, ok := d.model(model)
	if !ok || len(fields) == 0 {
		return fields, nil
	}
	requested = make(map[string]bool, len(fields))
	seen := map[string]bool{}
	add := func(field string) {
		if !seen[field] {
			seen[field] = true
			out = append(out, field)
		}
	}
	for _, field := range fields {
		requested[field] = true
		if series, renamed := m.renamed[field]; renamed {
			add(series)
			continue
		}
		if derived, isDerived := m.derived[field]; isDerived {
			for _, need := range derived.needs {
				add(need)
			}
			continue
		}
		add(field)
	}
	return out, requested
}

// recordsIn translates read results back into baseline vocabulary. A series field read only to
// rebuild a baseline one is removed again, so the record carries exactly what was asked for.
func (d *providerDialect) recordsIn(ctx context.Context, t *ClientTransport, model string, records []map[string]interface{}, requested map[string]bool) error {
	m, ok := d.model(model)
	if !ok || len(records) == 0 {
		return nil
	}
	wanted := func(field string) bool { return requested == nil || requested[field] }
	for baseline, series := range m.renamed {
		if !wanted(baseline) {
			continue
		}
		for _, record := range records {
			if value, present := record[series]; present {
				record[baseline] = value
				if requested != nil && !requested[series] {
					delete(record, series)
				}
			}
		}
	}
	for baseline, derived := range m.derived {
		if !wanted(baseline) {
			continue
		}
		if err := derived.read(ctx, t, records, baseline); err != nil {
			return err
		}
	}
	if requested == nil {
		return nil
	}
	for _, derived := range m.derived {
		for _, need := range derived.needs {
			if requested[need] {
				continue
			}
			for _, record := range records {
				delete(record, need)
			}
		}
	}
	return nil
}

// domainOut translates a domain. Operators ("&", "|", "!") pass through; a leaf on a renamed field
// is renamed, a leaf on a derived field is rebuilt by its filter or refused.
func (d *providerDialect) domainOut(model string, domain interface{}) (interface{}, error) {
	m, ok := d.model(model)
	if !ok {
		return domain, nil
	}
	items, isList := domainItems(domain)
	if !isList {
		return domain, nil
	}
	out := make([]interface{}, 0, len(items))
	for _, item := range items {
		leaf, isLeaf := item.([]interface{})
		if !isLeaf || len(leaf) != 3 {
			out = append(out, item)
			continue
		}
		path, isPath := leaf[0].(string)
		if !isPath {
			out = append(out, item)
			continue
		}
		head, rest, dotted := strings.Cut(path, ".")
		if series, renamed := m.renamed[head]; renamed {
			translated := series
			if dotted {
				translated += "." + rest
			}
			out = append(out, []interface{}{translated, leaf[1], leaf[2]})
			continue
		}
		if derived, isDerived := m.derived[head]; isDerived {
			operator, _ := leaf[1].(string)
			if dotted || derived.filter == nil {
				return nil, d.refuse(model, head, derived.why)
			}
			rebuilt, err := derived.filter(operator, leaf[2])
			if err != nil {
				return nil, d.refuse(model, head, err.Error())
			}
			out = append(out, rebuilt)
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// domainItems flattens the two shapes a domain arrives in ([]interface{} and [][]interface{}).
func domainItems(domain interface{}) ([]interface{}, bool) {
	switch v := domain.(type) {
	case []interface{}:
		return v, true
	case [][]interface{}:
		items := make([]interface{}, len(v))
		for i, leaf := range v {
			items[i] = leaf
		}
		return items, true
	default:
		return nil, false
	}
}

// orderOut translates a sort specification ("field [asc|desc], ...").
func (d *providerDialect) orderOut(model, order string) (string, error) {
	m, ok := d.model(model)
	if !ok || strings.TrimSpace(order) == "" {
		return order, nil
	}
	terms := strings.Split(order, ",")
	for i, term := range terms {
		words := strings.Fields(term)
		if len(words) == 0 {
			continue
		}
		if series, renamed := m.renamed[words[0]]; renamed {
			words[0] = series
		} else if derived, isDerived := m.derived[words[0]]; isDerived {
			return "", d.refuse(model, words[0], derived.why)
		}
		terms[i] = strings.Join(words, " ")
	}
	return strings.Join(terms, ", "), nil
}

// valuesOut translates the values of a create or write.
func (d *providerDialect) valuesOut(mode writeMode, model string, values map[string]interface{}) (map[string]interface{}, error) {
	m, ok := d.model(model)
	if !ok {
		return values, nil
	}
	out := make(map[string]interface{}, len(values))
	type pending struct {
		field string
		value interface{}
		rule  writeRule
		why   string
	}
	var ruled []pending
	for field, value := range values {
		if series, renamed := m.renamed[field]; renamed {
			out[series] = value
			continue
		}
		if derived, isDerived := m.derived[field]; isDerived {
			ruled = append(ruled, pending{field: field, value: value, rule: derived.write, why: derived.why})
			continue
		}
		if rule, hasRule := m.writeRules[field]; hasRule {
			ruled = append(ruled, pending{field: field, value: value, rule: rule})
			continue
		}
		out[field] = value
	}
	for _, p := range ruled {
		if p.rule == nil {
			return nil, d.refuse(model, p.field, p.why)
		}
		translated, err := p.rule(mode, p.value, out)
		if err != nil {
			return nil, d.refuse(model, p.field, err.Error())
		}
		for field, value := range translated {
			if existing, taken := out[field]; taken && !reflect.DeepEqual(existing, value) {
				return nil, d.refuse(model, p.field, fmt.Sprintf("it translates to %s=%v, which contradicts %s=%v in the same call", field, value, field, existing))
			}
			out[field] = value
		}
	}
	return out, nil
}

// fieldNamesIn completes the answer to the field-existence probe execRead makes for
// OptionalReadFields (ir.model.fields filtered on one model): a baseline field the series renamed
// or can rebuild exists, under its baseline name, whenever what it is read from exists.
func (d *providerDialect) fieldNamesIn(model string, records []map[string]interface{}) []map[string]interface{} {
	m, ok := d.model(model)
	if !ok {
		return records
	}
	present := make(map[string]bool, len(records))
	for _, record := range records {
		if name, isName := record["name"].(string); isName {
			present[name] = true
		}
	}
	for baseline, series := range m.renamed {
		if present[series] && !present[baseline] {
			records = append(records, map[string]interface{}{"name": baseline})
		}
	}
	for baseline, derived := range m.derived {
		available := !present[baseline]
		for _, need := range derived.needs {
			available = available && present[need]
		}
		if available {
			records = append(records, map[string]interface{}{"name": baseline})
		}
	}
	return records
}

// fieldProbeModel reports the model of an ir.model.fields read restricted to one model, the shape
// execRead uses to discover which OptionalReadFields a provider has.
func fieldProbeModel(model string, domain interface{}) (string, bool) {
	if model != "ir.model.fields" {
		return "", false
	}
	items, isList := domainItems(domain)
	if !isList || len(items) != 1 {
		return "", false
	}
	leaf, isLeaf := items[0].([]interface{})
	if !isLeaf || len(leaf) != 3 || leaf[0] != "model" || leaf[1] != "=" {
		return "", false
	}
	target, isString := leaf[2].(string)
	return target, isString
}

// ---- Odoo 20.0 --------------------------------------------------------------

// providerHasVAT mirrors Odoo 20's res.partner.has_vat: set, and not one of the placeholders its
// heuristic ignores ('/', 'na', 'NA').
func providerHasVAT(value interface{}) bool {
	vat, isString := value.(string)
	if !isString {
		return false
	}
	switch strings.TrimSpace(vat) {
	case "", "/", "na", "NA":
		return false
	}
	return true
}

// partnerIsCompanyRule is the 20.0 meaning of "this contact is an organization". Odoo 20 computes
// res.partner.is_company: a partner is a company when it is its own commercial entity (no parent)
// and has a VAT number. A create IGNORES a supplied is_company (measured: is_company=true without
// a VAT is stored as false), so a create may only ask for what the provider will compute. A write
// is honoured (measured), so it passes through.
func partnerIsCompanyRule(mode writeMode, value interface{}, values map[string]interface{}) (map[string]interface{}, error) {
	organization, isBool := value.(bool)
	if !isBool {
		return nil, fmt.Errorf("expected true or false, got %v", value)
	}
	if mode == writeModeWrite {
		return map[string]interface{}{"is_company": organization}, nil
	}
	hasParent := false
	if parent, present := values["parent_id"]; present && parent != nil && parent != false {
		hasParent = true
	}
	computed := !hasParent && providerHasVAT(values["vat"])
	switch {
	case organization && !computed && hasParent:
		return nil, fmt.Errorf("Odoo 20 makes a contact with a parent a person; create it without parent_id or as a person")
	case organization && !computed:
		return nil, fmt.Errorf("Odoo 20 decides that a contact is an organization from its VAT number and ignores the flag on create, so this would silently create a person; create it as a person, then set is_company with an update")
	case !organization && computed:
		return nil, fmt.Errorf("Odoo 20 makes a contact with its own VAT number an organization; drop the VAT or create it as an organization")
	}
	// The provider computes exactly what was asked; sending the flag would only be ignored.
	return map[string]interface{}{}, nil
}

func partnerCompanyTypeRule(mode writeMode, value interface{}, values map[string]interface{}) (map[string]interface{}, error) {
	switch value {
	case "company":
		return partnerIsCompanyRule(mode, true, values)
	case "person":
		return partnerIsCompanyRule(mode, false, values)
	default:
		return nil, fmt.Errorf("expected company or person, got %v", value)
	}
}

var odoo20Dialect = &providerDialect{
	series: Odoo20ProviderSeries,
	// Measured against the 20.0-20260926 lab and its surface inventory (scripts/dev/odoo-surface-inventory.py):
	// these 19.0 models do not exist in 20.0.
	absentModels: map[string]string{
		"account.group":                         "account groups were replaced by parent accounts on account.account",
		"res.bank":                              "bank details are fields of res.partner.bank (bank_name, bank_bic)",
		"stock.scrap":                           "a scrap is a stock.move with is_scrap set",
		"hr.leave.type":                         "leave types became work entry types (hr.work.entry.type)",
		"hr.contract.type":                      "contract types were removed; contract terms live on hr.version",
		"hr.attendance.overtime.line":           "the overtime rule models were removed",
		"hr.attendance.overtime.rule":           "the overtime rule models were removed",
		"hr.attendance.overtime.ruleset":        "the overtime rule models were removed",
		"l10n_vn_edi_viettel.sinvoice.template": "SInvoice templates became a code on the symbol (l10n_vn_edi_viettel.sinvoice.symbol.invoice_template_code)",
		"im_livechat.conversation.tag":          "live chat conversation tags were removed",
	},
	models: map[string]modelDialect{
		"res.partner": {
			derived: map[string]derivedField{
				// company_type was a computed interface field over is_company in 19.0
				// (person|company); 20.0 removed it.
				"company_type": {
					needs: []string{"is_company"},
					read: func(_ context.Context, _ *ClientTransport, records []map[string]interface{}, field string) error {
						for _, record := range records {
							if isCompany, present := record["is_company"].(bool); present {
								if isCompany {
									record[field] = "company"
								} else {
									record[field] = "person"
								}
							}
						}
						return nil
					},
					write: partnerCompanyTypeRule,
					filter: func(operator string, value interface{}) ([]interface{}, error) {
						asFlag := func(v interface{}) (bool, error) {
							switch v {
							case "company":
								return true, nil
							case "person":
								return false, nil
							}
							return false, fmt.Errorf("company_type has no value %v", v)
						}
						switch operator {
						case "=", "!=":
							flag, err := asFlag(value)
							if err != nil {
								return nil, err
							}
							return []interface{}{"is_company", operator, flag}, nil
						case "in", "not in":
							list, isList := value.([]interface{})
							if !isList {
								return nil, fmt.Errorf("operator %s needs a list", operator)
							}
							flags := make([]interface{}, 0, len(list))
							for _, v := range list {
								flag, err := asFlag(v)
								if err != nil {
									return nil, err
								}
								flags = append(flags, flag)
							}
							return []interface{}{"is_company", operator, flags}, nil
						}
						return nil, fmt.Errorf("operator %q has no equivalent on is_company", operator)
					},
					why: "company_type does not exist in 20.0; it is read from is_company",
				},
			},
			writeRules: map[string]writeRule{
				"is_company": partnerIsCompanyRule,
			},
		},
		// Renames (product_uom(_id) -> uom_id, holiday_status_id -> work_entry_type_id, ...) are not
		// written here: odoo20_dialect_renames.gen.go holds them, generated from the two series'
		// inventories by scripts/dev/odoo-dialect-renames.py, and init merges them below.
		"uom.uom": {
			derived: map[string]derivedField{
				// In 19.0 uom.uom.rounding was already computed, identically for every unit, as
				// 10^-digits of the 'Product Unit' decimal precision; 20.0 removed the field and
				// rounds with that precision directly. Rebuilding it the same way gives the same value.
				"rounding": {
					read: func(ctx context.Context, t *ClientTransport, records []map[string]interface{}, field string) error {
						precision, err := t.rawSearchRead(ctx, "decimal.precision", []interface{}{[]interface{}{"name", "=", "Product Unit"}}, []string{"digits"}, 0, 1, "", nil)
						if err != nil {
							return err
						}
						digits := 2.0 // decimal.precision.precision_get's default when the record is missing
						if len(precision) == 1 {
							if value, isNumber := precision[0]["digits"].(float64); isNumber {
								digits = value
							}
						}
						rounding := math.Pow(10, -digits)
						for _, record := range records {
							record[field] = rounding
						}
						return nil
					},
					why: "rounding does not exist in 20.0; every unit rounds with the 'Product Unit' precision",
				},
			},
		},
		"resource.calendar": {
			derived: map[string]derivedField{
				// 20.0 removed the calendar's own timezone: a calendar's hours are interpreted in
				// the timezone of the resource working them. There is no value to rebuild, so the
				// field is reported unset (Odoo's false), which is what it is.
				"tz": {
					read: func(_ context.Context, _ *ClientTransport, records []map[string]interface{}, field string) error {
						for _, record := range records {
							record[field] = false
						}
						return nil
					},
					why: "tz does not exist in 20.0; a calendar's hours are interpreted in each resource's timezone",
				},
			},
		},
	},
}

func init() {
	odoo20Dialect.addRenames(odoo20Renames)
}

// addRenames merges generated renames into the dialect, keeping its hand-written entries. Each
// conflict is a defect in the generator's input, so it stops the program: a model the series
// removed, a baseline field the dialect already rebuilds (derived) or rewrites (writeRules), a
// field renamed twice differently, or two baseline fields sharing one series field.
func (d *providerDialect) addRenames(renames map[string]map[string]string) {
	for model, pairs := range renames {
		if _, absent := d.absentModels[model]; absent {
			panic(fmt.Sprintf("odoo: the %s dialect renames fields of %s, a model it removed", d.series, model))
		}
		m := d.models[model]
		merged := make(map[string]string, len(m.renamed)+len(pairs))
		for baseline, series := range m.renamed {
			merged[baseline] = series
		}
		for baseline, series := range pairs {
			if existing, present := merged[baseline]; present && existing != series {
				panic(fmt.Sprintf("odoo: the %s dialect renames %s.%s to both %s and %s", d.series, model, baseline, existing, series))
			}
			if _, isDerived := m.derived[baseline]; isDerived {
				panic(fmt.Sprintf("odoo: the %s dialect both rebuilds and renames %s.%s", d.series, model, baseline))
			}
			if _, hasRule := m.writeRules[baseline]; hasRule {
				panic(fmt.Sprintf("odoo: the %s dialect both rewrites and renames %s.%s", d.series, model, baseline))
			}
			merged[baseline] = series
		}
		owners := make(map[string]string, len(merged))
		for baseline, series := range merged {
			if other, taken := owners[series]; taken {
				panic(fmt.Sprintf("odoo: the %s dialect maps both %s.%s and %s.%s to %s", d.series, model, other, model, baseline, series))
			}
			owners[series] = baseline
		}
		m.renamed = merged
		if d.models == nil {
			d.models = map[string]modelDialect{}
		}
		d.models[model] = m
	}
}
