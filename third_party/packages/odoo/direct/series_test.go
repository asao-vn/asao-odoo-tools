package direct_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// odoo20Partners is a 20.0 provider holding one contact. It answers a read only when every
// requested field exists in 20.0 (company_type does not), so a success proves the dialect ran.
func odoo20Partners(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Params struct {
				Method string        `json:"method"`
				Args   []interface{} `json:"args"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if answerProviderFit(w, body, odoo.Odoo20ProviderSeries) {
			return
		}
		mu.Lock()
		methods = append(methods, req.Params.Method)
		mu.Unlock()
		switch req.Params.Method {
		case "version":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "20.0-20260926", "server_serie": "20.0"}})
			return
		case "login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 2})
			return
		}
		model, _ := req.Params.Args[3].(string)
		if model == "ir.model.fields" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{map[string]interface{}{"name": "customer_rank"}}})
			return
		}
		kwargs, _ := req.Params.Args[6].(map[string]interface{})
		fields, _ := kwargs["fields"].([]interface{})
		for _, field := range fields {
			if field == "company_type" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": 200, "message": "Odoo Server Error", "data": map[string]interface{}{"message": "Invalid field 'company_type' on 'res.partner'"}}})
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{map[string]interface{}{"id": 7, "name": "Cong ty An Phat", "is_company": true, "email": false, "phone": false, "parent_id": false, "customer_rank": 1}}})
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}

func TestDirectExecutorRunsACandidateSeriesOnlyWhenUnverifiedResultsAreAccepted(t *testing.T) {
	server, calls := odoo20Partners(t)
	executor, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	_, err = executor.Execute(context.Background(), direct.Request{Operation: "identity.contact.list", Limit: 1})
	var operationErr *direct.OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != string(odoo.ErrClassProviderVersionUnsupported) || operationErr.Detail["candidate"] != true {
		t.Fatalf("err = %v, want a candidate PROVIDER_VERSION_UNSUPPORTED", err)
	}
	for _, method := range calls() {
		if method != "version" {
			t.Fatalf("calls = %v: nothing but the version question may precede the admission", calls())
		}
	}

	outcome, err := executor.Execute(context.Background(), direct.Request{Operation: "identity.contact.list", Limit: 1, AllowUnverified: true})
	if err != nil {
		t.Fatalf("execute with unverified results accepted: %v", err)
	}
	if outcome.ProviderSeries != odoo.Odoo20ProviderSeries || outcome.EvidenceClass != string(odoo.Odoo19EvidenceOtherSeries) {
		t.Fatalf("outcome series=%q evidence=%q; a 20.0 answer must say so and claim no 19.0 evidence", outcome.ProviderSeries, outcome.EvidenceClass)
	}
	records, _ := outcome.Data.([]interface{})
	if len(records) != 1 || records[0].(map[string]interface{})["company_type"] != "company" {
		t.Fatalf("data = %v, want company_type rebuilt from is_company", outcome.Data)
	}

	// A 20.0 answer reports 20.0's readiness, never 19.0's, in both directions: an operation with a
	// 20.0 record reads CURRENT_VERIFIED, one verified on 19.0 only reads UNVERIFIED.
	assertCandidateReadiness(t, outcome, "identity.contact.list", odoo.ReadinessCurrentVerified)
	other, err := executor.Execute(context.Background(), direct.Request{Operation: "identity.contact.describe", ProviderTargetID: 1, AllowUnverified: true})
	if err != nil {
		t.Fatalf("execute identity.contact.describe: %v", err)
	}
	if baseline, _ := odoo.ReadinessOf("identity.contact.describe"); baseline != odoo.ReadinessCurrentVerified {
		t.Fatalf("test premise: identity.contact.describe must be CURRENT_VERIFIED on 19.0 so a leak would show; it is %s", baseline)
	}
	assertCandidateReadiness(t, other, "identity.contact.describe", odoo.ReadinessUnverified)
}

func assertCandidateReadiness(t *testing.T, outcome direct.Outcome, operation string, want odoo.OperationReadiness) {
	t.Helper()
	if recorded, _ := odoo.ReadinessFor(odoo.Odoo20ProviderSeries, operation); recorded != want {
		t.Fatalf("test premise: the 20.0 manifest gives %s %s, want %s; pick another operation", operation, recorded, want)
	}
	if outcome.ProviderSeries != odoo.Odoo20ProviderSeries || outcome.Readiness != string(want) {
		t.Fatalf("%s on 20.0: series=%q readiness=%q, want readiness %s from the 20.0 manifest", operation, outcome.ProviderSeries, outcome.Readiness, want)
	}
}

func TestDirectExecutorReportsTheBaselineSeries(t *testing.T) {
	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Method string `json:"method"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Method == "login" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 2})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{}})
	}))
	defer server.Close()
	executor, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := executor.Execute(context.Background(), direct.Request{Operation: "structure.company.list", Limit: 1})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	baseline, _ := odoo.ReadinessOf("structure.company.list")
	evidence, _ := odoo.Odoo19EvidenceClassOf("structure.company.list")
	if outcome.ProviderSeries != odoo.Odoo19ProviderSeries || outcome.Readiness != string(baseline) || outcome.EvidenceClass != string(evidence) {
		t.Fatalf("outcome series=%q readiness=%q evidence=%q", outcome.ProviderSeries, outcome.Readiness, outcome.EvidenceClass)
	}
}
