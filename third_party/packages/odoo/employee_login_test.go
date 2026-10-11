package odoo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

// Odoo's own AccessError, from an access right or a record rule, is an access refusal; a business
// refusal that also says "not allowed", without the quoted model, is not.
func TestOdooAccessErrorsAreAccessDenied(t *testing.T) {
	for _, message := range []string{
		"You are not allowed to access 'Fields' (ir.model.fields) records.\n\nThis operation is allowed for the following groups:\n\t- Access Rights",
		"You are not allowed to modify 'Journal Entry' (account.move) records.",
		"Due to security restrictions, you are not allowed to access 'Sales Order' (sale.order) records.",
	} {
		if got := ClassifyOdooError(message); got != ErrClassAccessDenied {
			t.Errorf("%q = %s, want ACCESS_DENIED", message, got)
		}
	}
	if got := ClassifyOdooError("You are not allowed to cancel a validated payslip."); got == ErrClassAccessDenied {
		t.Errorf("a business refusal was read as an access refusal")
	}
}

// A read with optional fields discovers which of them the provider has. ir.model.fields is the
// Access Rights group's only, so an employee's own login falls back to fields_get on the model,
// and reads as an administrator's does.
func TestAnEmployeeLoginReadsOptionalFieldsThroughFieldsGet(t *testing.T) {
	var mu sync.Mutex
	var requested []interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Service string        `json:"service"`
				Method  string        `json:"method"`
				Args    []interface{} `json:"args"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		answer := func(result interface{}) { _ = json.NewEncoder(w).Encode(map[string]interface{}{"result": result}) }
		switch req.Params.Method {
		case "version":
			answer(map[string]interface{}{"server_version": "19.0-20260803", "server_serie": "19.0"})
			return
		case "login":
			answer(6)
			return
		}
		model, method := req.Params.Args[3], req.Params.Args[4]
		switch {
		case model == "ir.model.fields":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": 200, "message": "Odoo Server Error",
				"data": map[string]interface{}{"name": "odoo.exceptions.AccessError", "message": "You are not allowed to access 'Fields' (ir.model.fields) records."}}})
		case model == "res.partner" && method == "fields_get":
			answer(map[string]interface{}{"name": map[string]interface{}{"type": "char"}, "customer_rank": map[string]interface{}{"type": "integer"}})
		case model == "res.partner" && method == "search_read":
			kwargs, _ := req.Params.Args[6].(map[string]interface{})
			mu.Lock()
			requested = kwargs["fields"].([]interface{})
			mu.Unlock()
			answer([]interface{}{})
		default:
			t.Errorf("unexpected %v.%v", model, method)
			answer(nil)
		}
	}))
	defer server.Close()

	adapter := NewAdapter(NewClientTransport(NewRPCClient(server.URL, "db", "employee", "secret")))
	if _, err := adapter.Execute(context.Background(), &ExecutionIntent{ID: "t", Operation: "identity.contact.list", Input: `{"limit":1}`}); err != nil {
		t.Fatal(err)
	}
	op, _ := LookupStandaloneOperation("identity.contact.list")
	want := []interface{}{}
	for _, field := range op.ReadFields {
		want = append(want, field)
	}
	want = append(want, "customer_rank") // the one optional field this provider has
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(requested, want) {
		t.Fatalf("read fields = %v, want %v", requested, want)
	}
}
