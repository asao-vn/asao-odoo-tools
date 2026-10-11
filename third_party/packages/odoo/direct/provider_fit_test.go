package direct_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// customisedOdoo is a 19.0 Odoo whose installed modules are given, with partner_firstname (when
// installed) extending contacts. It answers the provider-fit check, and records every other call:
// the operations themselves.
func customisedOdoo(t *testing.T, modules map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var operations []string
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
			answer(2)
			return
		}
		model, _ := req.Params.Args[3].(string)
		method, _ := req.Params.Args[4].(string)
		switch {
		case model == "ir.module.module" && method == "search_read":
			rows := []interface{}{}
			for name, license := range modules {
				rows = append(rows, map[string]interface{}{"name": name, "license": license})
			}
			answer(rows)
		case model == "ir.model" && method == "search_read":
			answer([]interface{}{map[string]interface{}{"model": "res.partner", "modules": "base, contacts, partner_firstname"}})
		case method == "fields_get":
			answer(map[string]interface{}{})
		default:
			mu.Lock()
			operations = append(operations, model+"."+method)
			mu.Unlock()
			if method == "create" {
				answer(42)
				return
			}
			answer([]interface{}{})
		}
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), operations...)
	}
}

// Where a third-party module extends contacts, a contact write is held back before it is sent; the
// caller may accept the unverified result, and the outcome then says why it is unverified. A read
// still runs, noting the module (DEC-2026-0157).
func TestDirectExecutorHoldsBackAWriteWhereTheOdooDiffers(t *testing.T) {
	server, operations := customisedOdoo(t, map[string]string{"base": "LGPL-3", "contacts": "LGPL-3", "partner_firstname": "AGPL-3"})
	executor, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	create := direct.Request{Operation: "identity.contact.create", Values: map[string]interface{}{"name": "An Phat"}}

	_, err = executor.Execute(context.Background(), create)
	var operationErr *direct.OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != string(odoo.ErrClassEnvironmentUnverified) {
		t.Fatalf("err = %v, want ENVIRONMENT_UNVERIFIED", err)
	}
	reasons, _ := operationErr.Detail["reasons"].([]string)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "partner_firstname") {
		t.Fatalf("reasons = %v", operationErr.Detail["reasons"])
	}
	if operationErr.Detail["notice"] != odoo.CustomisedOdooNotice {
		t.Fatalf("the refusal must carry ASAO's notice: %v", operationErr.Detail["notice"])
	}
	if sent := operations(); len(sent) != 0 {
		t.Fatalf("a held-back write reached the provider: %v", sent)
	}

	create.AllowUnverified = true
	outcome, err := executor.Execute(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ProviderFit == nil || outcome.ProviderFit.Verdict != odoo.FitUnverifiedHere || outcome.ProviderFit.Notice != odoo.CustomisedOdooNotice {
		t.Fatalf("an accepted write must say it is unverified here, with ASAO's notice: %+v", outcome.ProviderFit)
	}
	if sent := operations(); !reflect.DeepEqual(sent, []string{"res.partner.create"}) {
		t.Fatalf("sent = %v, want the one create", sent)
	}

	read, err := executor.Execute(context.Background(), direct.Request{Operation: "identity.contact.list", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if read.ProviderFit == nil || read.ProviderFit.Verdict != odoo.FitOK || len(read.ProviderFit.Notes) != 1 || !strings.Contains(read.ProviderFit.Notes[0], "partner_firstname") {
		t.Fatalf("read fit = %+v", read.ProviderFit)
	}
}

// An operation whose modules this Odoo lacks is refused before it is sent, naming them.
func TestDirectExecutorRefusesAnOperationWhoseModulesAreMissing(t *testing.T) {
	server, operations := customisedOdoo(t, map[string]string{"base": "LGPL-3"})
	executor, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), direct.Request{Operation: "sales.quotation.list", Limit: 1, AllowUnverified: true})
	var operationErr *direct.OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != string(odoo.ErrClassModuleNotInstalled) {
		t.Fatalf("err = %v, want MODULE_NOT_INSTALLED", err)
	}
	if missing, _ := operationErr.Detail["missing_modules"].([]string); !reflect.DeepEqual(missing, []string{"sale"}) {
		t.Fatalf("missing = %v", operationErr.Detail["missing_modules"])
	}
	if sent := operations(); len(sent) != 0 {
		t.Fatalf("an operation without its modules reached the provider: %v", sent)
	}
}
