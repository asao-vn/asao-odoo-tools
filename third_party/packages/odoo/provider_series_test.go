package odoo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestProviderVersionInfoSeries(t *testing.T) {
	for _, tc := range []struct {
		name string
		info ProviderVersionInfo
		want string
	}{
		{"server_serie wins", ProviderVersionInfo{ServerVersion: "19.0-20260803", ServerSerie: "19.0"}, "19.0"},
		{"dated community build", ProviderVersionInfo{ServerVersion: "19.0-20260803"}, "19.0"},
		{"enterprise build", ProviderVersionInfo{ServerVersion: "19.0+e"}, "19.0"},
		{"odoo 20", ProviderVersionInfo{ServerVersion: "20.0-20260926", ServerSerie: "20.0"}, "20.0"},
		{"odoo online serie", ProviderVersionInfo{ServerSerie: "saas~19.1"}, "saas~19.1"},
		{"odoo online build", ProviderVersionInfo{ServerVersion: "saas~19.1+e"}, "saas~19.1"},
		{"nothing reported", ProviderVersionInfo{}, ""},
		{"unparseable build", ProviderVersionInfo{ServerVersion: "nightly"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.Series(); got != tc.want {
				t.Fatalf("Series() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckProviderSeriesAcceptsOnlyTheCatalogSeries(t *testing.T) {
	if err := CheckProviderSeries(ProviderVersionInfo{ServerVersion: "19.0-20260803", ServerSerie: "19.0"}); err != nil {
		t.Fatalf("19.0 refused: %v", err)
	}
	if err := CheckProviderSeries(ProviderVersionInfo{ServerVersion: "19.0+e"}); err != nil {
		t.Fatalf("19.0 enterprise refused: %v", err)
	}
	for _, info := range []ProviderVersionInfo{
		{ServerVersion: "20.0-20260926", ServerSerie: "20.0"},
		{ServerVersion: "18.0-20260926", ServerSerie: "18.0"},
		{ServerVersion: "saas~19.1+e", ServerSerie: "saas~19.1"},
		{},
	} {
		err := CheckProviderSeries(info)
		var refusal *ProviderVersionUnsupportedError
		if !errors.As(err, &refusal) {
			t.Fatalf("%+v: err = %v, want *ProviderVersionUnsupportedError", info, err)
		}
		if refusal.ErrorClass() != string(ErrClassProviderVersionUnsupported) {
			t.Fatalf("class = %q", refusal.ErrorClass())
		}
		detail := refusal.StructuredDetail()
		if detail["provider_series"] != info.Series() || detail["provider_version"] != info.ServerVersion {
			t.Fatalf("detail = %v for %+v", detail, info)
		}
		if supported, _ := detail["supported_series"].([]string); len(supported) != 1 || supported[0] != Odoo19ProviderSeries {
			t.Fatalf("supported_series = %v", detail["supported_series"])
		}
	}
}

// fakeVersionedOdoo answers common.version with the given result and records every call, so a
// test can prove which calls a client made and what it sent.
type fakeVersionedOdoo struct {
	mu      sync.Mutex
	version interface{}
	methods []string
	bodies  []string
}

func (f *fakeVersionedOdoo) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Params struct {
			Service string `json:"service"`
			Method  string `json:"method"`
		} `json:"params"`
	}
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	f.methods = append(f.methods, req.Params.Method)
	f.bodies = append(f.bodies, string(raw))
	f.mu.Unlock()
	switch req.Params.Method {
	case "version":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": f.version})
	case "login":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": 2})
	default:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": []interface{}{}})
	}
}

func (f *fakeVersionedOdoo) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func TestRPCClientRefusesAnUnsupportedSeriesBeforeSendingTheCredential(t *testing.T) {
	const password = "private-credential-42"
	fake := &fakeVersionedOdoo{version: map[string]interface{}{"server_version": "20.0-20260926", "server_serie": "20.0"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	client := NewSingleAttemptRPCClient(server.URL, "db", "admin", password)
	_, err := client.ExecuteKw(context.Background(), "res.partner", "search_read", []interface{}{[]interface{}{}}, nil)

	var refusal *ProviderVersionUnsupportedError
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want *ProviderVersionUnsupportedError", err)
	}
	if refusal.ProviderSeries != "20.0" || refusal.ProviderVersion != "20.0-20260926" {
		t.Fatalf("refusal = %+v", refusal)
	}
	if calls := fake.calls(); len(calls) != 1 || calls[0] != "version" {
		t.Fatalf("calls = %v, want exactly [version]: no login and no execute_kw may follow a refused series", calls)
	}
	for _, body := range fake.bodies {
		if strings.Contains(body, password) {
			t.Fatal("the credential reached a provider whose series was refused")
		}
	}
}

func TestRPCClientChecksTheSeriesOnceThenLogsIn(t *testing.T) {
	fake := &fakeVersionedOdoo{version: map[string]interface{}{"server_version": "19.0-20260803", "server_serie": "19.0"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	client := NewRPCClient(server.URL, "db", "admin", "secret")
	for i := 0; i < 3; i++ {
		if _, err := client.ExecuteKw(context.Background(), "res.partner", "search_read", []interface{}{[]interface{}{}}, nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	want := []string{"version", "login", "execute_kw", "execute_kw", "execute_kw"}
	if calls := fake.calls(); strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRPCClientRefusesAVersionAnswerItCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version interface{}
		class   ProviderErrorClass
	}{
		// An Odoo always names its version (DEC-2026-0157): an answer naming none, or one that is not
		// a version record, is not Odoo's.
		{"no version reported", map[string]interface{}{}, ErrClassProviderNotOdoo},
		{"unparseable build", map[string]interface{}{"server_version": "nightly"}, ErrClassProviderVersionUnsupported},
		{"not an object", true, ErrClassProviderNotOdoo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeVersionedOdoo{version: tc.version}
			server := httptest.NewServer(http.HandlerFunc(fake.handler))
			defer server.Close()

			_, err := NewRPCClient(server.URL, "db", "admin", "secret").ExecuteKw(context.Background(), "res.partner", "search_read", []interface{}{[]interface{}{}}, nil)
			if err == nil {
				t.Fatal("an unreadable version answer was accepted")
			}
			var refusal StructuredError
			if !errors.As(err, &refusal) || refusal.ErrorClass() != string(tc.class) {
				t.Fatalf("err = %v (%T), want class %s", err, err, tc.class)
			}
			if calls := fake.calls(); len(calls) != 1 || calls[0] != "version" {
				t.Fatalf("calls = %v, want exactly [version]", calls)
			}
		})
	}
}

func TestAdapterReportsTheSeriesRefusalAsItsOwnClass(t *testing.T) {
	fake := &fakeVersionedOdoo{version: map[string]interface{}{"server_version": "20.0-20260926", "server_serie": "20.0"}}
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()

	adapter := NewAdapter(NewClientTransport(NewRPCClient(server.URL, "db", "admin", "secret")))
	result, err := adapter.Execute(context.Background(), &ExecutionIntent{ID: "t", Operation: "identity.contact.list", Input: `{"limit":1}`})
	if err == nil {
		t.Fatal("execution against Odoo 20 succeeded")
	}
	if result == nil || result.ErrorClass == nil || *result.ErrorClass != string(ErrClassProviderVersionUnsupported) {
		t.Fatalf("result = %+v, want error class %s", result, ErrClassProviderVersionUnsupported)
	}
	if calls := fake.calls(); len(calls) != 1 {
		t.Fatalf("calls = %v: a series refusal must not be retried", calls)
	}
}
