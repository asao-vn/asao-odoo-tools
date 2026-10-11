package standalone_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func TestConformanceGate_AdminModuleInstallRemainsPackOnly(t *testing.T) {
	writeTestProfile(t, "http://127.0.0.1:1")
	code, out, _ := runOdoo(t, "execute", "--profile", "lab-a", "--operation", "admin.module.install", "--provider-target-id", "1", "--output", "json")
	if code != clicore.ExitUsage {
		t.Fatalf("admin module install exit=%d, want ExitUsage (2)", code)
	}

	var envelope struct {
		Status string `json:"status"`
		Error  struct {
			Code     string                 `json:"code"`
			Category string                 `json:"category"`
			Message  string                 `json:"message"`
			Context  map[string]interface{} `json:"context"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if envelope.Status != "ERROR" || envelope.Error.Code != "AERP_OPERATION_REFUSED" || envelope.Error.Category != "USAGE" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if !strings.Contains(envelope.Error.Message, "capability pack") {
		t.Fatalf("pack-only refusal message = %q", envelope.Error.Message)
	}
}

func TestConformanceGate_AdminModuleInstallCannotBypassPackBoundary(t *testing.T) {
	writeTestProfile(t, "http://127.0.0.1:1")
	code, out, _ := runOdoo(t, "execute", "--profile", "lab-a", "--operation", "admin.module.install", "--provider-target-id", "1", "--allow-unverified", "--output", "json")
	if code != clicore.ExitUsage {
		t.Fatalf("admin module install with override exit=%d, want ExitUsage (2): %s", code, out.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if envelope.Error.Code != "AERP_OPERATION_REFUSED" || !strings.Contains(envelope.Error.Message, "capability pack") {
		t.Fatalf("unexpected pack-only refusal: %+v", envelope.Error)
	}
}

func TestConformanceGate_NeverLeaksCredentialsInOutput(t *testing.T) {
	secret := "private-secret-pass"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{"code": 200, "message": "AccessDenied"},
		})
	}))
	defer server.Close()

	writeTestProfile(t, server.URL)
	_, out, diag := runOdoo(t, "doctor", "--profile", "lab-a", "--probe", "--output", "json")
	if strings.Contains(out.String(), secret) || strings.Contains(diag.String(), secret) {
		t.Fatal("secret leaked in doctor output or diagnostics")
	}

	_, out2, diag2 := runOdoo(t, "execute", "--profile", "lab-a", "--operation", "identity.contact.list", "--allow-unverified", "--output", "json")
	if strings.Contains(out2.String(), secret) || strings.Contains(diag2.String(), secret) {
		t.Fatal("secret leaked in execute output or diagnostics")
	}
}

func TestConformanceGate_DoctorReportsAvailableCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Service string        `json:"service"`
				Method  string        `json:"method"`
				Args    []interface{} `json:"args"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		switch {
		case request.Params.Service == "common" && request.Params.Method == "version":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"server_version": "19.0-test"},
			})
		case request.Params.Service == "common" && request.Params.Method == "login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
		case request.Params.Service == "object" && request.Params.Method == "execute_kw":
			model, _ := request.Params.Args[3].(string)
			if model == "ir.module.module" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{
					map[string]interface{}{"name": "base", "state": "installed", "latest_version": "19.0"},
				}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{}})
			}
		}
	}))
	defer server.Close()

	writeTestProfile(t, server.URL)
	code, out, _ := runOdoo(t, "doctor", "--profile", "lab-a", "--probe", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("doctor probe exit=%d: %s", code, out.String())
	}

	var envelope struct {
		Data struct {
			Selection struct {
				Probe struct {
					CapabilityProbe struct {
						Status                    string `json:"status"`
						CatalogOperationCount     int    `json:"catalogOperationCount"`
						AvailableOperationCount   int    `json:"availableOperationCount"`
						UnavailableOperationCount int    `json:"unavailableOperationCount"`
					} `json:"capabilityProbe"`
				} `json:"probe"`
			} `json:"selection"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	cp := envelope.Data.Selection.Probe.CapabilityProbe
	if cp.Status != "OK" {
		t.Fatalf("capability probe status = %q, want OK", cp.Status)
	}
	if cp.CatalogOperationCount != AcceptedCatalogSize {
		t.Fatalf("catalog count = %d, want %d", cp.CatalogOperationCount, AcceptedCatalogSize)
	}
	if cp.AvailableOperationCount+cp.UnavailableOperationCount != AcceptedCatalogSize {
		t.Fatalf("available (%d) + unavailable (%d) != %d", cp.AvailableOperationCount, cp.UnavailableOperationCount, AcceptedCatalogSize)
	}
}

func TestConformanceGate_ExpandedSafeMutationsPlanValidation(t *testing.T) {
	writeTestProfile(t, "http://127.0.0.1:1")

	testCases := []struct {
		op   string
		args []string
	}{
		{"crm.lead.create", []string{"--set", "name=TestLead"}},
		{"crm.lead.update", []string{"--provider-target-id", "1", "--set", "name=TestLeadUpdated"}},
		{"sales.quotation.create", []string{"--set", "partner=1"}},
		{"sales.quotation.line.add", []string{"--set", "order=1", "--set", "product=1"}},
		{"sales.quotation.update", []string{"--provider-target-id", "1", "--set", "client_order_ref=REF001"}},
		{"sales.order.line.update", []string{"--provider-target-id", "1", "--set", "quantity=5"}},
		{"procurement.rfq.create", []string{"--set", "partner=1"}},
		{"procurement.rfq.line.add", []string{"--set", "order=1", "--set", "product=1"}},
		{"procurement.rfq.update", []string{"--provider-target-id", "1", "--set", "partner_ref=REF001"}},
		{"procurement.order.line.update", []string{"--provider-target-id", "1", "--set", "quantity=5"}},
		{"work.project.create", []string{"--set", "name=TestProj"}},
		{"work.project.update", []string{"--provider-target-id", "1", "--set", "name=TestProjUpdated"}},
		{"work.task.create", []string{"--set", "name=TestTask", "--set", "project=1"}},
		{"work.task.update", []string{"--provider-target-id", "1", "--set", "name=TestTaskUpdated"}},
		{"work.milestone.create", []string{"--set", "name=TestMS", "--set", "project=1"}},
		{"work.milestone.reach", []string{"--provider-target-id", "1", "--set", "reached=true"}},
		{"schedule.event.create", []string{"--set", "name=TestEvent", "--set", "starts_at=2026-09-20 09:00:00", "--set", "ends_at=2026-09-20 10:00:00"}},
		{"schedule.event.update", []string{"--provider-target-id", "1", "--set", "name=TestEventUpdated"}},
	}

	if len(testCases) != 18 {
		t.Fatalf("expected 18 expanded mutation test cases, got %d", len(testCases))
	}

	for _, tc := range testCases {
		t.Run(tc.op, func(t *testing.T) {
			cmdArgs := append([]string{"plan", "--profile", "lab-a", "--operation", tc.op}, tc.args...)
			cmdArgs = append(cmdArgs, "--output", "json")
			code, out, diag := runOdoo(t, cmdArgs...)
			if code != clicore.ExitOK {
				t.Fatalf("plan %s exited %d: %s\n%s", tc.op, code, out.String(), diag.String())
			}
			var envelope struct {
				Status string `json:"status"`
				Data   struct {
					Operation string `json:"operation"`
					Effect    string `json:"expectedEffect"`
				} `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if envelope.Status != "OK" || envelope.Data.Operation != tc.op {
				t.Fatalf("plan %s envelope mismatch: %+v", tc.op, envelope)
			}
		})
	}
}
