package odoo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// JSON-2 is a second way to the same provider: everything above the RPC client (the transport, the
// dialect, the executor) must not be able to tell. These tests pin what goes over the wire.

const json2Key = "test-api-key-1f2e3d"

type json2Request struct {
	Path     string
	Database string
	Bearer   string
	Body     map[string]interface{}
}

// json2Server answers /web/webclient/version_info with series, and /json/2/<model>/<method> from
// answers keyed by "model.method" (default: true).
type json2Server struct {
	series   string
	answers  map[string]func(body map[string]interface{}) (int, interface{})
	mu       sync.Mutex
	requests []json2Request
	raw      []string
}

func (s *json2Server) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.raw = append(s.raw, string(payload)+r.Header.Get("Authorization"))
		s.mu.Unlock()
		if r.URL.Path == "/web/webclient/version_info" {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("the version probe carried a credential")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1,
				"result": map[string]interface{}{"server_version": s.series + "-20260803", "server_serie": s.series}})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/json/2/") {
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]interface{}
		_ = json.Unmarshal(payload, &body)
		s.mu.Lock()
		s.requests = append(s.requests, json2Request{Path: r.URL.Path, Database: r.Header.Get("X-Odoo-Database"),
			Bearer: r.Header.Get("Authorization"), Body: body})
		s.mu.Unlock()
		key := strings.Replace(strings.TrimPrefix(r.URL.Path, "/json/2/"), "/", ".", 1)
		status, answer := http.StatusOK, interface{}(true)
		if respond, ok := s.answers[key]; ok {
			status, answer = respond(body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(answer)
	})
}

func newJSON2Transport(t *testing.T, s *json2Server) (*ClientTransport, *RPCClient) {
	t.Helper()
	server := httptest.NewServer(s.handler(t))
	t.Cleanup(server.Close)
	client := NewSingleAttemptClient(server.URL, "lab19", "admin", json2Key, ProtocolJSON2)
	return NewClientTransport(client), client
}

func asJSON(v interface{}) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}

func TestJSON2NamesEveryArgumentAsOdoosSignaturesDo(t *testing.T) {
	s := &json2Server{series: "19.0", answers: map[string]func(map[string]interface{}) (int, interface{}){
		"res.partner.search_read": func(map[string]interface{}) (int, interface{}) {
			return 200, []interface{}{map[string]interface{}{"id": 7.0}}
		},
		"res.partner.read": func(map[string]interface{}) (int, interface{}) {
			return 200, []interface{}{map[string]interface{}{"id": 7.0}}
		},
		"res.partner.create":       func(map[string]interface{}) (int, interface{}) { return 200, []interface{}{42.0} },
		"res.partner.search_count": func(map[string]interface{}) (int, interface{}) { return 200, 3.0 },
	}}
	transport, _ := newJSON2Transport(t, s)
	ctx := context.Background()
	providerCtx := map[string]interface{}{"lang": "vi_VN"}

	if _, err := transport.SearchRead(ctx, "res.partner", []interface{}{[]interface{}{"is_company", "=", true}}, []string{"name"}, 5, "id", providerCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Read(ctx, "res.partner", []int64{7}, []string{"name"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := transport.Write(ctx, "res.partner", []int64{7}, map[string]interface{}{"name": "A"}, nil); err != nil {
		t.Fatal(err)
	}
	id, err := transport.Create(ctx, "res.partner", map[string]interface{}{"name": "B"}, nil)
	if err != nil || id != 42 {
		t.Fatalf("create = %d, %v", id, err)
	}
	if n, err := transport.SearchCount(ctx, "res.partner", []interface{}{}, nil); err != nil || n != 3 {
		t.Fatalf("search_count = %d, %v", n, err)
	}
	if err := transport.Unlink(ctx, "res.partner", []int64{7}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.CallMethod(ctx, "crm.lead.lost", nil, "onchange", []interface{}{map[string]interface{}{}, []interface{}{}, map[string]interface{}{"x": map[string]interface{}{}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.CallMethod(ctx, "crm.lead.lost", nil, "fields_get", []interface{}{[]interface{}{"type"}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.CallMethod(ctx, "purchase.order", []int64{3}, "action_create_invoice", []interface{}{[]interface{}{}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.CallMethod(ctx, "sale.order", []int64{9}, "action_confirm", nil, nil); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`/json/2/res.partner/search_read {"context":{"lang":"vi_VN"},"domain":[["is_company","=",true]],"fields":["name"],"limit":5,"order":"id"}`,
		`/json/2/res.partner/read {"fields":["name"],"ids":[7]}`,
		`/json/2/res.partner/write {"ids":[7],"vals":{"name":"A"}}`,
		`/json/2/res.partner/create {"vals_list":{"name":"B"}}`,
		`/json/2/res.partner/search_count {"domain":[]}`,
		`/json/2/res.partner/unlink {"ids":[7]}`,
		`/json/2/crm.lead.lost/onchange {"field_names":[],"fields_spec":{"x":{}},"values":{}}`,
		`/json/2/crm.lead.lost/fields_get {"allfields":[],"attributes":["type"]}`,
		`/json/2/purchase.order/action_create_invoice {"attachment_ids":[],"ids":[3]}`,
		`/json/2/sale.order/action_confirm {"ids":[9]}`,
	}
	if len(s.requests) != len(want) {
		t.Fatalf("%d requests, want %d", len(s.requests), len(want))
	}
	for i, request := range s.requests {
		if got := request.Path + " " + asJSON(request.Body); got != want[i] {
			t.Errorf("request %d =\n  %s\nwant\n  %s", i, got, want[i])
		}
		if request.Bearer != "bearer "+json2Key || request.Database != "lab19" {
			t.Errorf("request %d authorization %q database %q", i, request.Bearer, request.Database)
		}
	}
}

func TestJSON2RefusesPositionalArgumentsItCannotName(t *testing.T) {
	s := &json2Server{series: "19.0"}
	transport, _ := newJSON2Transport(t, s)
	_, err := transport.CallMethod(context.Background(), "sale.order", []int64{9}, "action_mystery", []interface{}{"x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "pass them by name") {
		t.Fatalf("err = %v, want a refusal saying how to pass the arguments", err)
	}
	if len(s.requests) != 0 {
		t.Fatalf("a call with unnamed arguments was sent: %v", s.requests)
	}
}

func TestJSON2SendsTheKeyOnlyAfterTheSeriesGate(t *testing.T) {
	s := &json2Server{series: "18.0"}
	transport, _ := newJSON2Transport(t, s)
	_, err := transport.SearchRead(context.Background(), "res.partner", []interface{}{}, []string{"name"}, 1, "", nil)
	var refusal *ProviderVersionUnsupportedError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want the series gate's refusal", err)
	}
	for _, raw := range s.raw {
		if strings.Contains(raw, json2Key) {
			t.Fatal("the API key reached a provider of a series this build does not execute against")
		}
	}
}

func TestJSON2ErrorsClassifyAsTheirJSONRPCTwins(t *testing.T) {
	odooError := func(status int, name, message string) func(map[string]interface{}) (int, interface{}) {
		return func(map[string]interface{}) (int, interface{}) {
			return status, map[string]interface{}{"name": name, "message": message, "arguments": []interface{}{message}, "context": map[string]interface{}{}}
		}
	}
	s := &json2Server{series: "19.0", answers: map[string]func(map[string]interface{}) (int, interface{}){
		"stock.picking.button_validate": odooError(422, "odoo.exceptions.UserError", "You cannot validate a transfer if no quantities are reserved nor done. Validating a zero quantity transfer?"),
		"res.partner.unlink":            odooError(422, "odoo.exceptions.UserError", "How about archiving the record instead?"),
		"res.partner.read":              odooError(401, "werkzeug.exceptions.Unauthorized", "Invalid apikey"),
		"res.partner.write":             odooError(404, "werkzeug.exceptions.NotFound", "the model 'res.partner' does not exist"),
	}}
	transport, _ := newJSON2Transport(t, s)
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
		want ProviderErrorClass
	}{
		{"user error", func() error {
			_, err := transport.CallMethod(ctx, "stock.picking", []int64{1}, "button_validate", nil, nil)
			return err
		}, ErrClassInsufficientStockOrReservation},
		{"referential", func() error { return transport.Unlink(ctx, "res.partner", []int64{1}, nil) }, ErrClassReferentialConstraint},
		{"bad key", func() error {
			_, err := transport.Read(ctx, "res.partner", []int64{1}, []string{"name"}, nil)
			return err
		}, ErrClassAccessDenied},
		{"unknown model", func() error {
			return transport.Write(ctx, "res.partner", []int64{1}, map[string]interface{}{"name": "x"}, nil)
		}, ErrClassNotFound},
	}
	for _, c := range cases {
		err := c.call()
		var provider *OdooError
		if !errors.As(err, &provider) {
			t.Fatalf("%s: err = %v, want an OdooError", c.name, err)
		}
		if got := ClassifyOdooError(err.Error()); got != c.want {
			t.Errorf("%s: class = %s, want %s (%v)", c.name, got, c.want, err)
		}
	}
}

func TestJSON2RedactsTheKeyFromWhatComesBack(t *testing.T) {
	s := &json2Server{series: "19.0", answers: map[string]func(map[string]interface{}) (int, interface{}){
		"res.partner.read": func(map[string]interface{}) (int, interface{}) {
			return 422, map[string]interface{}{"name": "odoo.exceptions.UserError", "message": "bad key " + json2Key}
		},
		"res.partner.search_read": func(map[string]interface{}) (int, interface{}) {
			return 200, []interface{}{map[string]interface{}{"id": 1.0, "comment": "echo " + json2Key}}
		},
	}}
	transport, _ := newJSON2Transport(t, s)
	_, err := transport.Read(context.Background(), "res.partner", []int64{1}, []string{"name"}, nil)
	if err == nil || strings.Contains(err.Error(), json2Key) {
		t.Fatalf("err = %v: the key leaked", err)
	}
	records, err := transport.SearchRead(context.Background(), "res.partner", []interface{}{}, []string{"comment"}, 1, "", nil)
	if err != nil || strings.Contains(fmt.Sprint(records), json2Key) {
		t.Fatalf("records = %v, %v: the key leaked", records, err)
	}
}

func TestJSON2CarriesTheSeriesDialect(t *testing.T) {
	s := &json2Server{series: "20.0", answers: map[string]func(map[string]interface{}) (int, interface{}){
		"purchase.order.line.search_read": func(map[string]interface{}) (int, interface{}) {
			return 200, []interface{}{map[string]interface{}{"id": 1.0, "uom_id": []interface{}{1.0, "Units"}}}
		},
	}}
	transport, client := newJSON2Transport(t, s)
	client.AdmitCandidateSeries(true)
	records, err := transport.SearchRead(context.Background(), "purchase.order.line",
		[]interface{}{[]interface{}{"product_uom_id", "=", 1}}, []string{"product_uom_id"}, 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := asJSON(s.requests[0].Body); got != `{"domain":[["uom_id","=",1]],"fields":["uom_id"],"limit":1}` {
		t.Fatalf("20.0 request = %s, want the 19.0 names translated", got)
	}
	if _, renamedBack := records[0]["product_uom_id"]; !renamedBack {
		t.Fatalf("records = %v, want the answer in the 19.0 vocabulary", records)
	}
}

func TestParseProtocol(t *testing.T) {
	for name, want := range map[string]Protocol{"": ProtocolJSONRPC, "jsonrpc": ProtocolJSONRPC, "json2": ProtocolJSON2} {
		if got, err := ParseProtocol(name); err != nil || got != want {
			t.Fatalf("ParseProtocol(%q) = %q, %v", name, got, err)
		}
	}
	if _, err := ParseProtocol("xmlrpc"); err == nil {
		t.Fatal("an unknown protocol was accepted")
	}
}

// get_views is a model method the signature table does not list: called by name only, it reaches the
// model, and its views are never taken for record ids (the first live pack sync was refused with
// "cannot call salon_demo.booking.get_views with ids").
func TestJSON2SendsAModelMethodsNamedArgumentsWithoutIDs(t *testing.T) {
	views := []interface{}{[]interface{}{false, "form"}, []interface{}{false, "list"}}
	body, err := json2Arguments("get_views", []interface{}{}, map[string]interface{}{"views": views, "options": map[string]interface{}{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ids := body["ids"]; ids || body["views"] == nil || body["options"] == nil {
		t.Fatalf("get_views body: %v", body)
	}
}
