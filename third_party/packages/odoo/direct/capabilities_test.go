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
	"time"

	"github.com/asao-platform/atoc/packages/odoo/direct"
)

func TestOperationAvailabilityUnknownDoesNotContactProvider(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.OperationAvailability(context.Background(), "not.a.catalog.operation"); !errors.Is(err, direct.ErrUnknownOperation) {
		t.Fatalf("error = %v, want ErrUnknownOperation", err)
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

func TestModuleInventoryThroughDirectExecutor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		switch {
		case req.Params.Service == "common" && req.Params.Method == "version":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "19.0-test"}})
		case req.Params.Service == "common" && req.Params.Method == "login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
		case req.Params.Service == "object" && req.Params.Method == "execute_kw" && len(req.Params.Args) >= 5 && req.Params.Args[3] == "ir.module.module" && req.Params.Args[4] == "search_read":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{
				map[string]interface{}{"name": "sale_management", "state": "installed", "latest_version": "19.0"},
				map[string]interface{}{"name": "base", "state": "installed", "latest_version": "19.0"},
				map[string]interface{}{"name": "sale", "state": "installed", "latest_version": "19.0"},
			}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": 200, "message": "unexpected probe"}})
		}
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.ModuleInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderVersion != "19.0-test" || !reflect.DeepEqual(got.Installed, []string{"base", "sale", "sale_management"}) {
		t.Fatalf("inventory = %+v", got)
	}
	availability, err := e.OperationAvailability(context.Background(), "sales.order.confirm")
	if err != nil {
		t.Fatal(err)
	}
	if !availability.Available {
		t.Fatalf("sales availability = %+v", availability)
	}
}

func TestMissingModelIsStableAndCredentialSafe(t *testing.T) {
	secret := "private-credential-42"
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Service string `json:"service"`
				Method  string `json:"method"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Service == "common" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{
			"code": 200, "message": "Odoo Server Error",
			"data": map[string]interface{}{"message": "Model 'res.partner' does not exist in registry; token=" + secret},
		}})
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(context.Background(), direct.Request{Operation: "identity.contact.list"})
	if err == nil {
		t.Fatal("expected missing-model error")
	}
	var opErr *direct.OperationError
	if !errors.As(err, &opErr) || opErr.Class != "MODULE_NOT_INSTALLED" {
		t.Fatalf("error = %v, operation error = %+v", err, opErr)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(opErr.Message, "does not exist") {
		t.Fatalf("unsanitized provider detail leaked: %v", err)
	}
	if got := opErr.Detail["provider_model"]; got != "res.partner" {
		t.Fatalf("provider_model detail = %#v", got)
	}
}

func TestModuleInventoryAuthAndTimeoutDoNotLeakPassword(t *testing.T) {
	secret := "private-credential-42"
	t.Run("auth failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Params struct {
					Service string `json:"service"`
					Method  string `json:"method"`
				} `json:"params"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Params.Method == "version" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "19.0-test"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": 200, "message": "Access denied " + secret}})
		}))
		defer server.Close()
		e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.ModuleInventory(context.Background())
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()
		e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		_, err = e.ModuleInventory(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want deadline exceeded", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("timeout error leaked password: %v", err)
		}
	})
}
