package direct_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo/direct"
)

func TestRawCallArgumentForwardingAndReadAllowlist(t *testing.T) {
	allowlistMethods := []string{
		"search",
		"search_read",
		"read",
		"search_count",
		"fields_get",
		"name_search",
	}

	for _, method := range allowlistMethods {
		t.Run("read_"+method, func(t *testing.T) {
			var recordedArgs []interface{}
			var recordedKwargs map[string]interface{}
			calls := 0

			server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Params struct {
						Service string        `json:"service"`
						Method  string        `json:"method"`
						Args    []interface{} `json:"args"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				if req.Params.Service == "common" {
					json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
					return
				}
				if req.Params.Method == "execute_kw" {
					// execute_kw args: [db, uid, password, model, method, args, kwargs]
					a := req.Params.Args
					if len(a) < 7 {
						t.Errorf("execute_kw args length %d < 7", len(a))
						return
					}
					if m, ok := a[3].(string); !ok || m != "res.partner" {
						t.Errorf("model = %v, want res.partner", a[3])
					}
					if meth, ok := a[4].(string); !ok || meth != method {
						t.Errorf("method = %v, want %s", a[4], method)
					}
					if passedArgs, ok := a[5].([]interface{}); ok {
						recordedArgs = passedArgs
					}
					if passedKwargs, ok := a[6].(map[string]interface{}); ok {
						recordedKwargs = passedKwargs
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{map[string]interface{}{"id": 1, "name": "Acme"}}})
				}
			}))
			defer server.Close()

			e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
			if err != nil {
				t.Fatal(err)
			}

			testArgs := []interface{}{[]interface{}{[]interface{}{"name", "=", "Acme"}}}
			testKwargs := map[string]interface{}{"fields": []interface{}{"name"}, "limit": float64(5)}

			outcome, err := e.Call(context.Background(), direct.RawRequest{
				Model:  "res.partner",
				Method: method,
				Args:   testArgs,
				Kwargs: testKwargs,
			}, false)
			if err != nil {
				t.Fatalf("call to %s failed without mutation flag: %v", method, err)
			}

			if !outcome.Raw {
				t.Errorf("outcome.Raw = false, want true")
			}
			if outcome.Model != "res.partner" || outcome.Method != method {
				t.Errorf("outcome model/method = %s/%s, want res.partner/%s", outcome.Model, outcome.Method, method)
			}
			if outcome.MutationAllowed {
				t.Errorf("outcome.MutationAllowed = true, want false")
			}
			if outcome.ProviderRoute.ConnectionID != "lab" || outcome.ProviderRoute.Resolution != "LOCAL_PROFILE" {
				t.Errorf("outcome.ProviderRoute = %+v", outcome.ProviderRoute)
			}
			if outcome.Data == nil {
				t.Errorf("outcome.Data is nil")
			}

			if !reflect.DeepEqual(recordedArgs, testArgs) {
				t.Errorf("recordedArgs = %#v, want %#v", recordedArgs, testArgs)
			}
			if !reflect.DeepEqual(recordedKwargs, testKwargs) {
				t.Errorf("recordedKwargs = %#v, want %#v", recordedKwargs, testKwargs)
			}
		})
	}
}

func TestRawCallMutationRefusalAndOverride(t *testing.T) {
	calls := 0
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Params struct {
				Service string `json:"service"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Params.Service == "common" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"result": 42})
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	for _, mutatingMethod := range []string{"write", "create", "unlink", "action_confirm", "button_validate"} {
		// 1. Refused without allowMutation
		callsBefore := calls
		req := direct.RawRequest{
			Model:  "res.partner",
			Method: mutatingMethod,
			Args:   []interface{}{[]interface{}{42}, map[string]interface{}{"name": "Updated"}},
		}
		_, err := e.Call(context.Background(), req, false)
		if !errors.Is(err, direct.ErrRawMutationRefused) {
			t.Fatalf("method %q: err = %v, want ErrRawMutationRefused", mutatingMethod, err)
		}
		if calls != callsBefore {
			t.Fatalf("method %q: refused call made %d HTTP requests, want 0", mutatingMethod, calls-callsBefore)
		}

		// 2. Allowed with allowMutation
		outcome, err := e.Call(context.Background(), req, true)
		if err != nil {
			t.Fatalf("method %q with allowMutation failed: %v", mutatingMethod, err)
		}
		if !outcome.Raw || !outcome.MutationAllowed {
			t.Errorf("method %q: outcome.Raw=%v, outcome.MutationAllowed=%v", mutatingMethod, outcome.Raw, outcome.MutationAllowed)
		}
	}
}

func TestRawCallValidationRefusalsMakeZeroHTTPRequests(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name    string
		req     direct.RawRequest
		mut     bool
		wantErr error
	}{
		{"blank model empty", direct.RawRequest{Model: "", Method: "read"}, false, direct.ErrRawModelRequired},
		{"blank model whitespace", direct.RawRequest{Model: "   ", Method: "read"}, false, direct.ErrRawModelRequired},
		{"blank method empty", direct.RawRequest{Model: "res.partner", Method: ""}, false, direct.ErrRawMethodRequired},
		{"blank method whitespace", direct.RawRequest{Model: "res.partner", Method: "   "}, false, direct.ErrRawMethodRequired},
		{"underscore method _private", direct.RawRequest{Model: "res.partner", Method: "_private"}, true, direct.ErrRawMethodDisallowed},
		{"underscore method __init__", direct.RawRequest{Model: "res.partner", Method: "__init__"}, true, direct.ErrRawMethodDisallowed},
		{"underscore method with leading spaces", direct.RawRequest{Model: "res.partner", Method: "  _search"}, true, direct.ErrRawMethodDisallowed},
		{"mutating method without flag", direct.RawRequest{Model: "res.partner", Method: "unlink"}, false, direct.ErrRawMutationRefused},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callsBefore := calls
			_, err := e.Call(context.Background(), tc.req, tc.mut)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if calls != callsBefore {
				t.Fatalf("refused call made %d HTTP requests, want 0", calls-callsBefore)
			}
		})
	}
}

func TestRawCallProviderErrorAndCredentialRedaction(t *testing.T) {
	secret := "private-credential-42"
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Service string `json:"service"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Params.Service == "common" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
			return
		}
		// Return an Odoo error that includes the secret in the message
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"code":    200,
				"message": "Odoo Server Error",
				"data": map[string]interface{}{
					"name":    "odoo.exceptions.AccessDenied",
					"message": "Access denied for token " + secret,
				},
			},
		})
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	_, err = e.Call(context.Background(), direct.RawRequest{
		Model:  "res.partner",
		Method: "read",
		Args:   []interface{}{[]interface{}{1}},
	}, false)
	if err == nil {
		t.Fatal("expected error from provider, got nil")
	}

	var opErr *direct.OperationError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected *direct.OperationError, got %T: %v", err, err)
	}

	if opErr.Class != "ACCESS_DENIED" {
		t.Errorf("opErr.Class = %q, want ACCESS_DENIED", opErr.Class)
	}
	if opErr.Operation != "res.partner.read" {
		t.Errorf("opErr.Operation = %q, want res.partner.read", opErr.Operation)
	}

	// Ensure secret is redacted
	errStr := err.Error()
	if strings.Contains(errStr, secret) {
		t.Fatalf("error message leaked secret: %s", errStr)
	}
	if !strings.Contains(errStr, "[REDACTED]") {
		t.Fatalf("error message does not contain [REDACTED]: %s", errStr)
	}

	// Marshal to JSON to ensure no secret leak in JSON representation
	b, err := json.Marshal(opErr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatalf("marshaled OperationError leaked secret: %s", string(b))
	}
}
