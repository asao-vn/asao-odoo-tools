package odoo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeOdoo20 is an Odoo 20.0 that knows its own schema: it refuses any field it does not have the
// way Odoo does ("Invalid field"), and it computes res.partner.is_company on create the way 20.0
// does (own commercial entity with a VAT number), ignoring a supplied value. A dialect that leaks
// a 19.0 name, or that writes a value 20.0 silently replaces, fails against it.
type fakeOdoo20 struct {
	mu      sync.Mutex
	schema  map[string][]string
	records map[string][]map[string]interface{}
	calls   []fakeOdoo20Call
}

type fakeOdoo20Call struct {
	Model  string
	Method string
	Domain interface{}
	Fields []string
	Order  string
	Values map[string]interface{}
}

func newFakeOdoo20() *fakeOdoo20 {
	return &fakeOdoo20{
		schema: map[string][]string{
			"res.partner":                  {"id", "name", "is_company", "email", "phone", "parent_id", "child_ids", "commercial_partner_id", "customer_rank", "supplier_rank", "company_id", "ref", "type", "vat", "street", "city"},
			"stock.quant":                  {"id", "product_id", "location_id", "quantity", "reserved_quantity", "uom_id", "company_id"},
			"stock.move":                   {"id", "reference", "picking_id", "product_id", "product_uom_qty", "quantity", "state", "location_id", "location_dest_id", "date", "company_id", "uom_id"},
			"stock.move.line":              {"id", "move_id", "picking_id", "product_id", "location_id", "quantity", "quantity_product_uom", "uom_id", "company_id", "state", "picked"},
			"purchase.order.line":          {"id", "name", "order_id", "product_id", "product_qty", "qty_received", "qty_invoiced", "price_unit", "price_subtotal", "date_planned", "state", "uom_id"},
			"uom.uom":                      {"id", "name", "active", "factor", "relative_factor"},
			"resource.calendar":            {"id", "name", "attendance_ids"},
			"hr.leave":                     {"id", "employee_id", "date_from", "date_to", "state", "work_entry_type_id"},
			"decimal.precision":            {"id", "name", "digits"},
			"resource.calendar.attendance": {"id", "calendar_id", "dayofweek", "hour_from", "hour_to", "day_period"},
		},
		records: map[string][]map[string]interface{}{
			"res.partner": {
				{"id": 7.0, "name": "Cong ty An Phat", "is_company": true, "vat": "0101234567", "parent_id": false, "email": false, "phone": false, "child_ids": []interface{}{}, "commercial_partner_id": []interface{}{7.0, "Cong ty An Phat"}, "customer_rank": 1.0, "supplier_rank": 0.0, "company_id": false, "ref": false, "type": "contact"},
				{"id": 8.0, "name": "Nguyen Van A", "is_company": false, "vat": false, "parent_id": false, "email": false, "phone": false, "child_ids": []interface{}{}, "commercial_partner_id": []interface{}{8.0, "Nguyen Van A"}, "customer_rank": 1.0, "supplier_rank": 0.0, "company_id": false, "ref": false, "type": "contact"},
			},
			"stock.quant":         {{"id": 1.0, "product_id": []interface{}{3.0, "Ao thun"}, "location_id": []interface{}{8.0, "WH/Stock"}, "quantity": 5.0, "reserved_quantity": 0.0, "uom_id": []interface{}{1.0, "Units"}, "company_id": []interface{}{1.0, "ASAO"}}},
			"stock.move":          {{"id": 1.0, "reference": "WH/OUT/1", "picking_id": false, "product_id": []interface{}{3.0, "Ao thun"}, "product_uom_qty": 2.0, "quantity": 2.0, "state": "assigned", "location_id": false, "location_dest_id": false, "date": "2026-10-10 00:00:00", "company_id": false, "uom_id": []interface{}{1.0, "Units"}}},
			"stock.move.line":     {{"id": 1.0, "move_id": []interface{}{1.0, "WH/OUT/1"}, "picking_id": false, "product_id": []interface{}{3.0, "Ao thun"}, "location_id": false, "quantity": 2.0, "quantity_product_uom": 2.0, "uom_id": []interface{}{1.0, "Units"}, "company_id": false, "state": "assigned", "picked": false}},
			"purchase.order.line": {{"id": 1.0, "name": "Vai", "order_id": []interface{}{1.0, "P00001"}, "product_id": []interface{}{4.0, "Vai"}, "product_qty": 10.0, "qty_received": 0.0, "qty_invoiced": 0.0, "price_unit": 1000.0, "price_subtotal": 10000.0, "date_planned": false, "state": "purchase", "uom_id": []interface{}{2.0, "m"}}},
			"uom.uom":             {{"id": 1.0, "name": "Units", "active": true, "factor": 1.0, "relative_factor": 1.0}},
			"resource.calendar":   {{"id": 1.0, "name": "Standard 40 hours/week", "attendance_ids": []interface{}{1.0, 2.0}}},
			"hr.leave":            {{"id": 1.0, "employee_id": []interface{}{1.0, "Admin"}, "date_from": "2026-10-12 01:00:00", "date_to": "2026-10-12 10:00:00", "state": "confirm", "work_entry_type_id": []interface{}{4.0, "Paid Time Off"}}},
			"decimal.precision":   {{"id": 1.0, "name": "Product Unit", "digits": 3.0}},
		},
	}
}

func (f *fakeOdoo20) hasField(model, field string) bool {
	for _, known := range f.schema[model] {
		if known == field {
			return true
		}
	}
	return false
}

func (f *fakeOdoo20) checkDomain(model string, domain interface{}) error {
	items, _ := domainItems(domain)
	for _, item := range items {
		if leaf, ok := item.([]interface{}); ok && len(leaf) == 3 {
			path, _ := leaf[0].(string)
			head, _, _ := strings.Cut(path, ".")
			if !f.hasField(model, head) {
				return fmt.Errorf("Invalid field %s.%s in leaf %v", model, head, leaf)
			}
		}
	}
	return nil
}

func (f *fakeOdoo20) matches(record map[string]interface{}, domain interface{}) bool {
	items, _ := domainItems(domain)
	for _, item := range items {
		leaf, ok := item.([]interface{})
		if !ok || len(leaf) != 3 {
			continue
		}
		field, _ := leaf[0].(string)
		got := record[field]
		switch leaf[1] {
		case "=":
			if !reflect.DeepEqual(got, leaf[2]) {
				return false
			}
		case "!=":
			if reflect.DeepEqual(got, leaf[2]) {
				return false
			}
		}
	}
	return true
}

func (f *fakeOdoo20) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Service string        `json:"service"`
				Method  string        `json:"method"`
				Args    []interface{} `json:"args"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(result interface{}, err error) {
			if err != nil {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": 200, "message": "Odoo Server Error", "data": map[string]interface{}{"message": err.Error()}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": result})
		}
		switch req.Params.Method {
		case "version":
			reply(map[string]interface{}{"server_version": "20.0-20260926", "server_serie": "20.0"}, nil)
			return
		case "login":
			reply(2, nil)
			return
		}
		args := req.Params.Args
		model, _ := args[3].(string)
		method, _ := args[4].(string)
		positional, _ := args[5].([]interface{})
		kwargs, _ := args[6].(map[string]interface{})
		call := fakeOdoo20Call{Model: model, Method: method}
		f.mu.Lock()
		defer f.mu.Unlock()
		defer func() { f.calls = append(f.calls, call) }()

		switch method {
		case "search_read", "search_count":
			if len(positional) > 0 {
				call.Domain = positional[0]
			}
			if model == "ir.model.fields" {
				items, _ := domainItems(call.Domain)
				target := items[0].([]interface{})[2].(string)
				names := []interface{}{}
				for _, field := range f.schema[target] {
					names = append(names, map[string]interface{}{"name": field})
				}
				reply(names, nil)
				return
			}
			if err := f.checkDomain(model, call.Domain); err != nil {
				reply(nil, err)
				return
			}
			if method == "search_count" {
				count := 0
				for _, record := range f.records[model] {
					if f.matches(record, call.Domain) {
						count++
					}
				}
				reply(count, nil)
				return
			}
			call.Order, _ = kwargs["order"].(string)
			for _, term := range strings.Split(call.Order, ",") {
				if words := strings.Fields(term); len(words) > 0 && !f.hasField(model, words[0]) {
					reply(nil, fmt.Errorf("Invalid field %q in order", words[0]))
					return
				}
			}
			out := []interface{}{}
			fields, _ := kwargs["fields"].([]interface{})
			for _, raw := range fields {
				name, _ := raw.(string)
				call.Fields = append(call.Fields, name)
				if !f.hasField(model, name) {
					reply(nil, fmt.Errorf("Invalid field '%s' on '%s'", name, model))
					return
				}
			}
			for _, record := range f.records[model] {
				if !f.matches(record, call.Domain) {
					continue
				}
				projected := map[string]interface{}{}
				for _, name := range call.Fields {
					projected[name] = record[name]
				}
				if len(call.Fields) == 0 {
					for name, value := range record {
						projected[name] = value
					}
				}
				out = append(out, projected)
			}
			reply(out, nil)
		case "create", "write":
			index := 0
			if method == "write" {
				index = 1
			}
			values, _ := positional[index].(map[string]interface{})
			call.Values = values
			for name := range values {
				if !f.hasField(model, name) {
					reply(nil, fmt.Errorf("Invalid field '%s' on '%s'", name, model))
					return
				}
			}
			if method == "write" {
				reply(true, nil)
				return
			}
			record := map[string]interface{}{"id": float64(100 + len(f.calls))}
			for name, value := range values {
				record[name] = value
			}
			if model == "res.partner" {
				// Odoo 20 computes is_company on create and ignores a supplied value.
				parent, hasParent := values["parent_id"]
				record["is_company"] = (!hasParent || parent == false) && providerHasVAT(values["vat"])
			}
			f.records[model] = append(f.records[model], record)
			reply(record["id"], nil)
		default:
			reply(nil, fmt.Errorf("fake odoo 20 does not implement %s", method))
		}
	}
}

func (f *fakeOdoo20) callsTo(model, method string) []fakeOdoo20Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeOdoo20Call
	for _, call := range f.calls {
		if call.Model == model && call.Method == method {
			out = append(out, call)
		}
	}
	return out
}

func newOdoo20Adapter(t *testing.T) (*Adapter, *fakeOdoo20) {
	t.Helper()
	fake := newFakeOdoo20()
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	client := NewRPCClient(server.URL, "db", "admin", "secret")
	client.AdmitCandidateSeries(true)
	return NewAdapter(NewClientTransport(client)), fake
}

func executeOn(t *testing.T, adapter *Adapter, operation, input string) (*ExecutionResult, error) {
	t.Helper()
	return adapter.Execute(context.Background(), &ExecutionIntent{ID: "t", Operation: operation, Input: input})
}

func firstRecord(t *testing.T, result *ExecutionResult) map[string]interface{} {
	t.Helper()
	// An operation with no output contract returns the transport's records unnormalized.
	if raw, isRaw := result.Data.([]map[string]interface{}); isRaw && len(raw) > 0 {
		return raw[0]
	}
	records, ok := result.Data.([]interface{})
	if !ok || len(records) == 0 {
		t.Fatalf("data = %#v, want at least one record", result.Data)
	}
	record, ok := records[0].(map[string]interface{})
	if !ok {
		t.Fatalf("record = %#v", records[0])
	}
	return record
}

// TestOdoo20DialectReadsEveryBrokenOperationInBaselineVocabulary covers the ten operations that
// fail against a 20.0 provider without a dialect. Each must succeed against a 20.0 schema and
// answer with the 19.0 field names and values.
func TestOdoo20DialectReadsEveryBrokenOperationInBaselineVocabulary(t *testing.T) {
	for _, tc := range []struct {
		operation string
		input     string
		want      map[string]interface{}
		absent    []string
	}{
		{"identity.contact.list", `{"limit":5}`, map[string]interface{}{"company_type": "company", "is_organization": true}, []string{"is_company"}},
		{"identity.contact.describe", `{"target_id":7}`, map[string]interface{}{"company_type": "company"}, nil},
		{"inventory.quant.list", `{"limit":5}`, map[string]interface{}{"product_uom_id": []interface{}{1.0, "Units"}}, []string{"uom_id"}},
		{"inventory.move.describe", `{"target_id":1}`, map[string]interface{}{"product_uom": []interface{}{1.0, "Units"}}, []string{"uom_id"}},
		{"inventory.move_line.list", `{"limit":5}`, map[string]interface{}{"product_uom_id": []interface{}{1.0, "Units"}}, []string{"uom_id"}},
		{"procurement.order.line.describe", `{"target_id":1}`, map[string]interface{}{"product_uom_id": []interface{}{2.0, "m"}}, []string{"uom_id"}},
		{"catalog.uom.describe", `{"target_id":1}`, map[string]interface{}{"rounding": 0.001}, nil},
		{"workforce.calendar.list", `{"limit":5}`, map[string]interface{}{"tz": false}, nil},
		{"workforce.leave.list", `{"limit":5}`, map[string]interface{}{"holiday_status_id": []interface{}{4.0, "Paid Time Off"}}, []string{"work_entry_type_id"}},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			adapter, _ := newOdoo20Adapter(t)
			result, err := executeOn(t, adapter, tc.operation, tc.input)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			record := firstRecord(t, result)
			for field, want := range tc.want {
				if got := record[field]; !reflect.DeepEqual(got, want) {
					t.Fatalf("%s = %#v, want %#v (record %v)", field, record[field], want, record)
				}
			}
			for _, field := range tc.absent {
				if _, present := record[field]; present {
					t.Fatalf("%s leaked into the baseline answer: %v", field, record)
				}
			}
		})
	}
}

func TestOdoo20DialectTranslatesFiltersAndSorts(t *testing.T) {
	adapter, fake := newOdoo20Adapter(t)
	result, err := executeOn(t, adapter, "identity.contact.list", `{"domain":[["company_type","=","person"]]}`)
	if err != nil {
		t.Fatalf("filter on company_type: %v", err)
	}
	if records := result.Data.([]interface{}); len(records) != 1 || records[0].(map[string]interface{})["name"] != "Nguyen Van A" {
		t.Fatalf("company_type=person matched %v", result.Data)
	}
	calls := fake.callsTo("res.partner", "search_read")
	if got := fmt.Sprint(calls[len(calls)-1].Domain); got != "[[is_company = false]]" {
		t.Fatalf("provider domain = %s, want is_company = false", got)
	}

	if _, err := executeOn(t, adapter, "workforce.leave.list", `{"domain":[["holiday_status_id","=",4]]}`); err != nil {
		t.Fatalf("filter on holiday_status_id: %v", err)
	}
	leaves := fake.callsTo("hr.leave", "search_read")
	if got := fmt.Sprint(leaves[len(leaves)-1].Domain); got != "[[work_entry_type_id = 4]]" {
		t.Fatalf("provider domain = %s", got)
	}

	before := len(fake.callsTo("res.partner", "search_read"))
	_, err = executeOn(t, adapter, "identity.contact.list", `{"domain":[["company_type","ilike","comp"]]}`)
	var refusal *DialectRefusalError
	if !errors.As(err, &refusal) || refusal.Field != "company_type" || refusal.ErrorClass() != string(ErrClassValidationFailed) {
		t.Fatalf("err = %v, want a DialectRefusalError on company_type", err)
	}
	if after := len(fake.callsTo("res.partner", "search_read")); after != before {
		t.Fatal("a refused filter still reached the provider")
	}

	d := dialectFor(Odoo20ProviderSeries)
	if order, err := d.orderOut("stock.quant", "product_uom_id desc, id"); err != nil || order != "uom_id desc, id" {
		t.Fatalf("orderOut = %q, %v", order, err)
	}
	if _, err := d.orderOut("res.partner", "company_type"); !errors.As(err, &refusal) {
		t.Fatalf("sorting on a derived field was not refused: %v", err)
	}
}

// TestOdoo20DialectNeverLetsTheProviderSilentlyChangeAContactsType pins the one semantic change in
// this dialect: 20.0 decides on create whether a contact is an organization from its VAT number
// and ignores the flag, so asking for an organization without one must be refused rather than
// silently creating a person.
func TestOdoo20DialectNeverLetsTheProviderSilentlyChangeAContactsType(t *testing.T) {
	for _, input := range []string{
		`{"values":{"name":"Cong ty B","company_type":"company"}}`,
		`{"values":{"name":"Cong ty B","is_company":true}}`,
		`{"values":{"name":"Chi nhanh","company_type":"company","parent_id":7}}`,
	} {
		adapter, fake := newOdoo20Adapter(t)
		_, err := executeOn(t, adapter, "identity.contact.create", input)
		var refusal *DialectRefusalError
		if !errors.As(err, &refusal) {
			t.Fatalf("%s: err = %v, want a DialectRefusalError", input, err)
		}
		if creates := fake.callsTo("res.partner", "create"); len(creates) != 0 {
			t.Fatalf("%s: the provider received a create it would have changed: %v", input, creates)
		}
	}

	adapter, fake := newOdoo20Adapter(t)
	if _, err := executeOn(t, adapter, "identity.contact.create", `{"values":{"name":"Tran Thi C","company_type":"person"}}`); err != nil {
		t.Fatalf("create a person: %v", err)
	}
	creates := fake.callsTo("res.partner", "create")
	if len(creates) != 1 {
		t.Fatalf("creates = %v", creates)
	}
	if _, sent := creates[0].Values["company_type"]; sent {
		t.Fatalf("company_type reached a 20.0 provider: %v", creates[0].Values)
	}
	if _, sent := creates[0].Values["is_company"]; sent {
		t.Fatalf("is_company was sent on create although 20.0 ignores it: %v", creates[0].Values)
	}

	adapter, fake = newOdoo20Adapter(t)
	if _, err := executeOn(t, adapter, "identity.contact.update", `{"target_id":8,"values":{"is_company":true}}`); err != nil {
		t.Fatalf("update is_company: %v", err)
	}
	writes := fake.callsTo("res.partner", "write")
	if len(writes) != 1 || writes[0].Values["is_company"] != true {
		t.Fatalf("writes = %v: 20.0 honours is_company on write, so it must be sent", writes)
	}
}

func TestOdoo20DialectAnswersTheOptionalFieldProbeInBaselineNames(t *testing.T) {
	d := dialectFor(Odoo20ProviderSeries)
	names := func(records []map[string]interface{}) []string {
		var out []string
		for _, record := range records {
			out = append(out, record["name"].(string))
		}
		sort.Strings(out)
		return out
	}
	quant := d.fieldNamesIn("stock.quant", []map[string]interface{}{{"name": "id"}, {"name": "uom_id"}})
	if got := strings.Join(names(quant), ","); got != "id,product_uom_id,uom_id" {
		t.Fatalf("stock.quant probe = %s", got)
	}
	partner := d.fieldNamesIn("res.partner", []map[string]interface{}{{"name": "is_company"}})
	if got := strings.Join(names(partner), ","); got != "company_type,is_company" {
		t.Fatalf("res.partner probe = %s", got)
	}
	if model, ok := fieldProbeModel("ir.model.fields", [][]interface{}{{"model", "=", "stock.quant"}}); !ok || model != "stock.quant" {
		t.Fatalf("fieldProbeModel = %q, %v", model, ok)
	}
	if _, ok := fieldProbeModel("ir.model.fields", [][]interface{}{{"model", "=", "stock.quant"}, {"name", "=", "x"}}); ok {
		t.Fatal("a narrower ir.model.fields read was mistaken for the probe")
	}
}

func TestBaselineSeriesIsNotTranslated(t *testing.T) {
	if d := dialectFor(Odoo19ProviderSeries); d != nil {
		t.Fatalf("19.0 has a dialect: %+v", d)
	}
	var none *providerDialect
	fields, requested := none.fieldsOut("res.partner", []string{"company_type"})
	if !reflect.DeepEqual(fields, []string{"company_type"}) || requested != nil {
		t.Fatalf("nil dialect changed a projection: %v %v", fields, requested)
	}
	values, err := none.valuesOut(writeModeCreate, "res.partner", map[string]interface{}{"company_type": "company"})
	if err != nil || values["company_type"] != "company" {
		t.Fatalf("nil dialect changed values: %v %v", values, err)
	}
}

func TestReadinessDoesNotCrossSeries(t *testing.T) {
	odoo20Records, _, err := seriesConformanceRecords(Odoo20ProviderSeries)
	if err != nil {
		t.Fatal(err)
	}
	verifiedOn20 := map[string]bool{}
	for _, record := range odoo20Records {
		verifiedOn20[record.OperationID] = true
	}
	for _, operation := range Catalog() {
		baseline, _ := ReadinessOf(operation.SemanticID)
		if got, ok := ReadinessFor(Odoo19ProviderSeries, operation.SemanticID); !ok || got != baseline {
			t.Fatalf("ReadinessFor(19.0, %s) = %s, %v; want %s", operation.SemanticID, got, ok, baseline)
		}
		// 20.0 readiness comes from the 20.0 manifest alone: 19.0 evidence never counts there.
		want := ReadinessUnverified
		if verifiedOn20[operation.SemanticID] {
			want = ReadinessCurrentVerified
		}
		if got, ok := ReadinessFor(Odoo20ProviderSeries, operation.SemanticID); !ok || got != want {
			t.Fatalf("ReadinessFor(20.0, %s) = %s, %v; want %s", operation.SemanticID, got, ok, want)
		}
	}
	if verifiedOn20["admin.module.install"] {
		t.Fatal("a 19.0-only record leaked into the 20.0 manifest")
	}
	if got, _ := ReadinessFor(Odoo20ProviderSeries, "admin.module.install"); got != ReadinessUnverified {
		t.Fatalf("admin.module.install is CURRENT_VERIFIED on 19.0 only, but reads %s on 20.0", got)
	}
	if _, ok := ReadinessFor("21.0", "identity.contact.list"); ok {
		t.Fatal("a series with no manifest reported readiness")
	}
	if _, ok := ReadinessFor(Odoo20ProviderSeries, "not.an.operation"); ok {
		t.Fatal("an unknown operation reported readiness")
	}
	record := ConformanceRecord{OperationID: "identity.contact.list", ProviderVersion: "20.0-20260926", ImageDigest: "sha256:cdd83e8359b3e8c357895d476396c05021fed9975bf420f353bab25fcaed1533", Modules: []string{"base"}, Status: string(ReadinessCurrentVerified), EvidenceRef: "runs/x.json", VerifiedAt: "2026-10-10T00:00:00Z"}
	if err := validateSeriesConformanceRecord(record, Odoo20ProviderSeries); err != nil {
		t.Fatalf("a 20.0 record was refused by the 20.0 manifest rules: %v", err)
	}
	if err := ValidateConformanceRecord(record); err == nil {
		t.Fatal("a 20.0 record was accepted as 19.0 evidence")
	}
}

func TestRPCClientAdmitsACandidateSeriesOnlyWhenAsked(t *testing.T) {
	fake := &fakeVersionedOdoo{version: map[string]interface{}{"server_version": "20.0-20260926", "server_serie": "20.0"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	client := NewRPCClient(server.URL, "db", "admin", "secret")
	_, err := client.ProviderSeries(context.Background())
	var refusal *ProviderVersionUnsupportedError
	if !errors.As(err, &refusal) || !refusal.Candidate || refusal.StructuredDetail()["candidate"] != true {
		t.Fatalf("err = %v, want a candidate refusal", err)
	}
	client.AdmitCandidateSeries(true)
	if series, err := client.ProviderSeries(context.Background()); err != nil || series != Odoo20ProviderSeries {
		t.Fatalf("admitted series = %q, %v", series, err)
	}
	if version, err := client.ProviderVersion(context.Background()); err != nil || version != "20.0-20260926" {
		t.Fatalf("ProviderVersion = %q, %v", version, err)
	}
	if _, err := client.ExecuteKw(context.Background(), "res.partner", "search_read", []interface{}{[]interface{}{}}, nil); err != nil {
		t.Fatalf("execute after admission: %v", err)
	}
	if calls := strings.Join(fake.calls(), ","); calls != "version,login,execute_kw" {
		t.Fatalf("calls = %s, want the version asked exactly once", calls)
	}
}

func TestOdoo20DialectRefusesARemovedModelBeforeContactingTheProvider(t *testing.T) {
	fake := newFakeOdoo20()
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()
	client := NewRPCClient(server.URL, "db", "admin", "secret")
	client.AdmitCandidateSeries(true)
	transport := NewClientTransport(client)

	for _, model := range []string{"stock.scrap", "hr.leave.type", "res.bank", "account.group"} {
		_, err := transport.SearchRead(context.Background(), model, []interface{}{}, []string{"id"}, 1, "", nil)
		var unavailable *ModelUnavailableError
		if !errors.As(err, &unavailable) || unavailable.Model != model || unavailable.Reason == "" {
			t.Fatalf("%s: err = %v, want a ModelUnavailableError naming what replaced it", model, err)
		}
		if unavailable.ErrorClass() != string(ErrClassOperationUnavailableOnSeries) {
			t.Fatalf("%s: class = %s", model, unavailable.ErrorClass())
		}
		if calls := fake.callsTo(model, "search_read"); len(calls) != 0 {
			t.Fatalf("%s: the provider was asked for a model 20.0 does not have", model)
		}
	}
	if err := dialectFor(Odoo19ProviderSeries).checkModel("stock.scrap"); err != nil {
		t.Fatalf("19.0 refused a model it has: %v", err)
	}
}

// TestOdoo20RenamesAreMergedIntoTheDialect: the generated renames reach the dialect next to its
// hand-written entries, including the ones that used to be written by hand.
func TestOdoo20RenamesAreMergedIntoTheDialect(t *testing.T) {
	for model, pairs := range odoo20Renames {
		m, ok := odoo20Dialect.model(model)
		if !ok {
			t.Fatalf("%s has generated renames but no dialect entry", model)
		}
		for baseline, series := range pairs {
			if m.renamed[baseline] != series {
				t.Fatalf("%s.%s renames to %q in the dialect, want %q", model, baseline, m.renamed[baseline], series)
			}
		}
	}
	for _, check := range []struct{ model, baseline, series string }{
		{"stock.move", "product_uom", "uom_id"},
		{"purchase.order.line", "product_uom_id", "uom_id"},
		{"hr.leave", "holiday_status_id", "work_entry_type_id"},
		{"mrp.production", "product_uom_id", "uom_id"},
		{"res.partner.bank", "acc_number", "account_number"},
	} {
		if m, _ := odoo20Dialect.model(check.model); m.renamed[check.baseline] != check.series {
			t.Fatalf("%s.%s -> %q, want %q", check.model, check.baseline, m.renamed[check.baseline], check.series)
		}
	}
	if m, _ := odoo20Dialect.model("res.partner"); m.derived["company_type"].read == nil || m.writeRules["is_company"] == nil {
		t.Fatal("merging renames lost res.partner's hand-written derived field or write rule")
	}
}

// TestAddRenamesRefusesConflicts: a generated rename that contradicts the dialect stops the program
// instead of silently winning or losing.
func TestAddRenamesRefusesConflicts(t *testing.T) {
	base := func() *providerDialect {
		return &providerDialect{
			series:       "test",
			absentModels: map[string]string{"gone.model": "removed"},
			models: map[string]modelDialect{
				"x.model": {
					renamed:    map[string]string{"a": "b"},
					derived:    map[string]derivedField{"c": {}},
					writeRules: map[string]writeRule{"d": nil},
				},
			},
		}
	}
	for name, renames := range map[string]map[string]map[string]string{
		"absent model":        {"gone.model": {"f": "g"}},
		"renamed differently": {"x.model": {"a": "z"}},
		"derived field":       {"x.model": {"c": "z"}},
		"write rule":          {"x.model": {"d": "z"}},
		"shared series field": {"x.model": {"e": "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("addRenames accepted %v", renames)
				}
			}()
			base().addRenames(renames)
		})
	}
	d := base()
	d.addRenames(map[string]map[string]string{"x.model": {"a": "b", "e": "f"}, "y.model": {"g": "h"}})
	if d.models["x.model"].renamed["e"] != "f" || d.models["x.model"].renamed["a"] != "b" || d.models["y.model"].renamed["g"] != "h" {
		t.Fatalf("merge lost a rename: %+v", d.models)
	}
}

// odoo20RenamesVerified fingerprints odoo20Renames as it was when the 20.0 conformance manifest was
// last rebuilt (2026-10-11: every 20.0 record re-run under these renames, the parked apps installed). A regenerated table
// changes what 20.0 operations send and return, so the records no longer cover it: re-run the 20.0
// conformance (docs/architecture/provider-research/odoo-surface/README.md), rebuild the 20.0
// manifest, then update this value.
const odoo20RenamesVerified = "sha256:08e866aa8ed79149c1d5204aeac714dddcd31eb4604372dd147a238fcd8d90fb"

func TestOdoo20ManifestWasVerifiedUnderTheCurrentRenames(t *testing.T) {
	encoded, err := json.Marshal(odoo20Renames) // encoding/json sorts map keys
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != odoo20RenamesVerified {
		t.Fatalf("odoo20Renames is %s, the 20.0 manifest was verified under %s: re-run the 20.0 conformance, rebuild the manifest, then update odoo20RenamesVerified", got, odoo20RenamesVerified)
	}
}
