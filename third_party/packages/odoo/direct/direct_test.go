package direct_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

func allow(context.Context, string) error { return nil }
func target(endpoint string) direct.Target {
	return direct.Target{ProfileName: "lab", Endpoint: endpoint, Database: "test-db", Login: "operator", Password: "private-credential-42"}
}

func TestTargetValidationBeforeContact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*direct.Target)
		want   error
	}{
		{"endpoint", func(v *direct.Target) { v.Endpoint = "" }, direct.ErrTargetIncomplete},
		{"database", func(v *direct.Target) { v.Database = "" }, direct.ErrTargetIncomplete},
		{"login", func(v *direct.Target) { v.Login = "" }, direct.ErrCredentialMaterial},
		{"password", func(v *direct.Target) { v.Password = "" }, direct.ErrCredentialMaterial},
		{"URL credentials", func(v *direct.Target) { v.Endpoint = "https://operator:private-credential-42@example.com" }, direct.ErrEndpointDisallowed},
		{"URL query", func(v *direct.Target) { v.Endpoint = "https://example.com?token=private-credential-42" }, direct.ErrEndpointDisallowed},
		{"invalid URL", func(v *direct.Target) { v.Endpoint = "https://example.com/%private-credential-42" }, direct.ErrEndpointDisallowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := target("https://example.com")
			tc.change(&v)
			checks := 0
			_, err := direct.NewExecutor(context.Background(), v, func(context.Context, string) error { checks++; return nil })
			if !errors.Is(err, tc.want) || checks != 0 {
				t.Fatalf("err=%v checks=%d", err, checks)
			}
			if strings.Contains(err.Error(), "private-credential-42") {
				t.Fatal("credential leaked")
			}
		})
	}
	_, err := direct.NewExecutor(context.Background(), target("https://example.com"), nil)
	if !errors.Is(err, direct.ErrEndpointDisallowed) {
		t.Fatalf("missing checker: %v", err)
	}
	_, err = direct.NewExecutor(context.Background(), target("https://example.com"), func(context.Context, string) error { return errors.New("private-credential-42") })
	if !errors.Is(err, direct.ErrEndpointDisallowed) || strings.Contains(err.Error(), "private-credential-42") {
		t.Fatalf("checker failure: %v", err)
	}
}

func TestCatalogParityAndNoGovernedInputs(t *testing.T) {
	ops := direct.Operations()
	if len(ops) != 105 || len(ops) != len(odoo.Catalog()) {
		t.Fatalf("catalog counts direct=%d parent=%d", len(ops), len(odoo.Catalog()))
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if seen[op.ID] {
			t.Fatalf("duplicate %s", op.ID)
		}
		seen[op.ID] = true
		entry, ok := odoo.CatalogByID[op.ID]
		if !ok || op.Strategy != string(entry.Strategy) || op.TargetRequirement != string(odoo.TargetRequirementOf(entry)) {
			t.Fatalf("catalog drift: %s", op.ID)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(direct.Target{}), reflect.TypeOf(direct.Request{})} {
		for _, name := range []string{"OrganizationID", "ProviderConnectionID", "Entity", "CapabilityGrant", "ActorID"} {
			if _, ok := typ.FieldByName(name); ok {
				t.Fatalf("%s accepts governed input %s", typ, name)
			}
		}
	}
}

func TestPlanAndTargetRulesDoNotContactProvider(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		request direct.Request
		want    error
	}{
		{direct.Request{Operation: "not.an.operation"}, direct.ErrUnknownOperation},
		{direct.Request{Operation: "finance.invoice.post"}, direct.ErrTargetRequired},
		{direct.Request{Operation: "identity.contact.create", ProviderTargetID: 42}, direct.ErrTargetNotAccepted},
	} {
		if _, err := e.Plan(context.Background(), tc.request); !errors.Is(err, tc.want) {
			t.Fatalf("plan: %v", err)
		}
		if _, err := e.Execute(context.Background(), tc.request); !errors.Is(err, tc.want) {
			t.Fatalf("execute: %v", err)
		}
	}
	plan, err := e.Plan(context.Background(), direct.Request{Operation: "people.employee.list", Limit: 3, Order: "name", ProviderContext: map[string]interface{}{"active_test": false}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Provider != "odoo19" || plan.Input["limit"] != float64(3) || plan.ProviderRoute.Resolution != "LOCAL_PROFILE" || plan.ProviderRoute.ConnectionID != "lab" {
		t.Fatalf("plan=%+v", plan)
	}
	if calls != 0 {
		t.Fatalf("plan/invalid requests contacted provider %d times", calls)
	}
}

func TestCapabilityPackEvidenceIsCurrentInDirectPlan(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Params struct {
				Service string `json:"service"`
				Method  string `json:"method"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		// Answered here rather than through odoo19(): this test counts every contact, and the
		// series check is one.
		if req.Params.Service == "common" && req.Params.Method == "version" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "19.0-20260803", "server_serie": "19.0"}})
			return
		}
		if req.Params.Service == "common" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{map[string]interface{}{"id": 1, "name": "base", "state": "installed"}}})
	}))
	defer server.Close()

	executor, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	request := direct.Request{Operation: "admin.module.install", ProviderTargetID: 1, Limit: 1}
	plan, err := executor.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("plan refused an unverified operation: %v", err)
	}
	if plan.Readiness != string(odoo.ReadinessCurrentVerified) {
		t.Fatalf("plan=%+v", plan)
	}
	if calls != 0 {
		t.Fatalf("plan contacted provider %d times", calls)
	}

	outcome, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("current verified execute failed: %v", err)
	}
	if outcome.Readiness != string(odoo.ReadinessCurrentVerified) {
		t.Fatalf("outcome.Readiness = %q, want CURRENT_VERIFIED", outcome.Readiness)
	}
	if calls == 0 {
		t.Fatal("current verified execute did not contact provider")
	}
}

// Exercises the real adapter, ClientTransport and RPCClient through their public constructor.
func TestDirectRPCExecutionAndSingleAttemptSecretSafeError(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "expired-session"}[fail], func(t *testing.T) {
			calls := 0
			logins := 0
			server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/jsonrpc" {
					t.Errorf("path=%s", r.URL.Path)
				}
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
					logins++
					if !reflect.DeepEqual(req.Params.Args, []interface{}{"test-db", "operator", "private-credential-42"}) {
						t.Error("wrong login routing")
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
					return
				}
				calls++
				a := req.Params.Args
				if req.Params.Method != "execute_kw" || a[0] != "test-db" || a[1] != float64(7) || a[2] != "private-credential-42" || a[3] != "res.partner" || a[4] != "create" {
					t.Error("wrong execution routing")
				}
				if fail {
					json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": 100, "message": "Odoo Session Expired", "data": map[string]interface{}{"message": "Access denied private-credential-42"}}})
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{"result": 42})
			}))
			defer server.Close()
			e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
			if err != nil {
				t.Fatal(err)
			}
			out, err := e.Execute(context.Background(), direct.Request{Operation: "identity.contact.create", Values: map[string]interface{}{"name": "Test contact"}, Verify: false})
			if fail {
				var opErr *direct.OperationError
				if !errors.As(err, &opErr) || opErr.Class != "ACCESS_DENIED" {
					t.Fatalf("error=%v", err)
				}
				b, _ := json.Marshal(opErr)
				if strings.Contains(string(b), "private-credential-42") {
					t.Fatal("provider error leaked credential")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if out.Status != "SUCCESS" || out.ProviderReference != "42" || out.Verification != "UNVERIFIED" {
					t.Fatalf("outcome=%+v", out)
				}
			}
			if calls != 1 || logins != 1 {
				t.Fatalf("mutation retried: calls=%d logins=%d", calls, logins)
			}
		})
	}
}

func TestDirectExecutor_ReadRetriesOnHTTP503(t *testing.T) {
	logins := 0
	readCalls := 0
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Method string        `json:"method"`
				Args   []interface{} `json:"args"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Method == "login" {
			logins++
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
			return
		}
		readCalls++
		if readCalls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": []interface{}{
				map[string]interface{}{"id": float64(1), "name": "Alice"},
			},
		})
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	e.SetRetryPolicy(odoo.RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	})

	// An operation without optional fields, so every call counted is the read itself.
	out, err := e.Execute(context.Background(), direct.Request{
		Operation: "people.department.list",
	})
	if err != nil {
		t.Fatalf("expected read retry to succeed on 3rd attempt, got error: %v", err)
	}
	if out.Status != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %s", out.Status)
	}
	if readCalls != 3 {
		t.Fatalf("expected 3 read calls, got %d", readCalls)
	}
}

func TestDirectExecutor_ReadRetriesOnECONNRESET(t *testing.T) {
	readCalls := 0
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Method string `json:"method"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Method == "login" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
			return
		}
		readCalls++
		if readCalls < 3 {
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				if tcp, ok := conn.(*net.TCPConn); ok {
					tcp.SetLinger(0)
				}
				conn.Close()
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": []interface{}{
				map[string]interface{}{"id": float64(1), "name": "Bob"},
			},
		})
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	e.SetRetryPolicy(odoo.RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	})

	// An operation without optional fields, so every call counted is the read itself.
	out, err := e.Execute(context.Background(), direct.Request{
		Operation: "people.department.list",
	})
	if err != nil {
		t.Fatalf("expected read retry to succeed on 3rd attempt, got error: %v", err)
	}
	if out.Status != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %s", out.Status)
	}
	if readCalls != 3 {
		t.Fatalf("expected 3 read calls, got %d", readCalls)
	}
}

func TestDirectExecutor_ReadDoesNotRetryOnAuthOrNotFound(t *testing.T) {
	for _, tc := range []struct {
		name      string
		errResult map[string]interface{}
		wantClass string
	}{
		{
			name: "access denied",
			errResult: map[string]interface{}{
				"code": 200, "message": "Access Denied: cannot be called remotely",
			},
			wantClass: "ACCESS_DENIED",
		},
		{
			name: "not found",
			errResult: map[string]interface{}{
				"code": 200, "message": "MissingError: record does not exist",
			},
			wantClass: "NOT_FOUND",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readCalls := 0
			server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Params struct {
						Method string `json:"method"`
					} `json:"params"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				if req.Params.Method == "login" {
					json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
					return
				}
				readCalls++
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": tc.errResult,
				})
			}))
			defer server.Close()

			e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
			if err != nil {
				t.Fatal(err)
			}
			e.SetRetryPolicy(odoo.RetryPolicy{
				MaxAttempts: 3,
				BaseDelay:   time.Millisecond,
				MaxDelay:    5 * time.Millisecond,
			})

			// An operation without optional fields: their probe is a call of its own, which an
			// access-denied answer would add to the count without being a retry.
			_, err = e.Execute(context.Background(), direct.Request{
				Operation: "people.department.list",
			})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if readCalls != 1 {
				t.Fatalf("expected exactly 1 read call (no retry), got %d", readCalls)
			}
			var opErr *direct.OperationError
			if !errors.As(err, &opErr) || opErr.Class != tc.wantClass {
				t.Fatalf("expected class %s, got: %v", tc.wantClass, err)
			}
		})
	}
}

func TestDirectExecutor_MutationDoesNotRetryOnHTTP503(t *testing.T) {
	calls := 0
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Method string `json:"method"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Method == "login" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
			return
		}
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	e.SetRetryPolicy(odoo.RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	})

	_, err = e.Execute(context.Background(), direct.Request{
		Operation: "identity.contact.create",
		Values:    map[string]interface{}{"name": "Acme Inc"},
	})
	if err == nil {
		t.Fatal("expected error from 503, got nil")
	}
	if calls != 1 {
		t.Fatalf("expected mutation to execute exactly once, but saw %d calls", calls)
	}
}

func TestDirectExecutor_RawCallSingleAttempt(t *testing.T) {
	calls := 0
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Method string `json:"method"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Method == "login" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
			return
		}
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	// Even read-only raw call (search_read) must execute exactly once
	_, err = e.Call(context.Background(), direct.RawRequest{
		Model:  "res.partner",
		Method: "search_read",
	}, false)
	if err == nil {
		t.Fatal("expected error from 503, got nil")
	}
	if calls != 1 {
		t.Fatalf("expected raw call to execute exactly once, but saw %d calls", calls)
	}
}
