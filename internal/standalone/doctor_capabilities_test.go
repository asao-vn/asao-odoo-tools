package standalone_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func TestDoctorProbeReportsModuleCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Service string        `json:"service"`
				Method  string        `json:"method"`
				Args    []interface{} `json:"args"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}

		switch {
		case request.Params.Service == "common" && request.Params.Method == "version":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"server_version": "19.0-test"},
			})
		case request.Params.Service == "common" && request.Params.Method == "login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
		case request.Params.Service == "object" && request.Params.Method == "execute_kw" && len(request.Params.Args) >= 5:
			model, _ := request.Params.Args[3].(string)
			method, _ := request.Params.Args[4].(string)
			switch {
			case model == "ir.module.module" && method == "search_read":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{
					map[string]interface{}{"name": "base", "state": "installed", "latest_version": "19.0"},
					map[string]interface{}{"name": "hr", "state": "installed", "latest_version": "19.0"},
				}})
			case model == "res.partner" && method == "search_read":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{
					"code": 200, "message": "unexpected probe operation",
				}})
			}
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{
				"code": 200, "message": "unexpected probe request",
			}})
		}
	}))
	defer server.Close()

	writeTestProfile(t, server.URL)
	code, out, diag := runOdoo(t, "doctor", "--profile", "lab-a", "--probe", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("doctor failed: %s\n%s", out, diag)
	}
	if strings.Contains(out.String(), "private-secret-pass") || strings.Contains(diag.String(), "private-secret-pass") {
		t.Fatal("doctor leaked credential material")
	}

	data := decode(t, out)
	selection, ok := data["selection"].(map[string]interface{})
	if !ok {
		t.Fatalf("selection = %v", data["selection"])
	}
	probe, ok := selection["probe"].(map[string]interface{})
	if !ok {
		t.Fatalf("probe = %v", selection["probe"])
	}
	if status, _ := probe["status"].(string); !strings.HasPrefix(status, "OK") {
		t.Fatalf("credential probe status = %v", probe["status"])
	}

	capability, ok := probe["capabilityProbe"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilityProbe = %v", probe["capabilityProbe"])
	}
	if capability["status"] != "OK" || capability["providerVersion"] != "19.0-test" {
		t.Fatalf("capability probe = %v", capability)
	}
	installed, ok := capability["installedModules"].([]interface{})
	if !ok {
		t.Fatalf("installedModules = %v", capability["installedModules"])
	}
	installedText, _ := json.Marshal(installed)
	if !strings.Contains(string(installedText), "base") || !strings.Contains(string(installedText), "hr") || strings.Contains(string(installedText), "hr_holidays") {
		t.Fatalf("installedModules = %v", installed)
	}
	available, availableOK := capability["availableOperationCount"].(float64)
	total, totalOK := capability["catalogOperationCount"].(float64)
	if !availableOK || !totalOK || available >= total {
		t.Fatalf("capability counts available=%v total=%v", available, total)
	}
}

func TestDoctorProbeSeparatesCapabilityProbeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Service string        `json:"service"`
				Method  string        `json:"method"`
				Args    []interface{} `json:"args"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}

		switch {
		case request.Params.Service == "common" && request.Params.Method == "version":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"server_version": "19.0-test"}})
		case request.Params.Service == "common" && request.Params.Method == "login":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": 7})
		case request.Params.Service == "object" && request.Params.Method == "execute_kw" && len(request.Params.Args) >= 5:
			model, _ := request.Params.Args[3].(string)
			method, _ := request.Params.Args[4].(string)
			switch {
			case model == "ir.module.module" && method == "search_read":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{
					"code": 200, "message": "Access denied private-secret-pass",
				}})
			case model == "res.partner" && method == "search_read":
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": []interface{}{}})
			default:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{
					"code": 200, "message": "unexpected probe operation",
				}})
			}
		default:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{
				"code": 200, "message": "unexpected probe request",
			}})
		}
	}))
	defer server.Close()

	writeTestProfile(t, server.URL)
	code, out, diag := runOdoo(t, "doctor", "--profile", "lab-a", "--probe", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("doctor failed: %s\n%s", out, diag)
	}
	if strings.Contains(out.String(), "private-secret-pass") || strings.Contains(diag.String(), "private-secret-pass") || strings.Contains(out.String(), "Access denied") {
		t.Fatalf("doctor leaked capability provider detail: %s", out)
	}

	data := decode(t, out)
	selection := data["selection"].(map[string]interface{})
	probe := selection["probe"].(map[string]interface{})
	if status, _ := probe["status"].(string); !strings.HasPrefix(status, "OK") {
		t.Fatalf("credential probe status = %v", probe["status"])
	}
	capability := probe["capabilityProbe"].(map[string]interface{})
	if capability["status"] != "FAILED" || capability["class"] != "CAPABILITY_PROBE_FAILED" {
		t.Fatalf("capability probe = %v", capability)
	}
}

// The doctor states ASAO's notice for an Odoo that is not the stock image, and only for one.
func TestDoctorProbeStatesTheCustomisedOdooNotice(t *testing.T) {
	for name, modules := range map[string][]string{"stock": {"base", "hr"}, "customised": {"base", "hr", "x_inhouse_payroll"}} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Params struct {
						Service string        `json:"service"`
						Method  string        `json:"method"`
						Args    []interface{} `json:"args"`
					} `json:"params"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				answer := func(result interface{}) { _ = json.NewEncoder(w).Encode(map[string]interface{}{"result": result}) }
				switch {
				case request.Params.Method == "version":
					answer(map[string]interface{}{"server_version": "19.0-test"})
				case request.Params.Method == "login":
					answer(7)
				case len(request.Params.Args) >= 5 && request.Params.Args[3] == "ir.module.module":
					rows := []interface{}{}
					for _, module := range modules {
						rows = append(rows, map[string]interface{}{"name": module, "state": "installed", "latest_version": "19.0", "license": "LGPL-3"})
					}
					answer(rows)
				case len(request.Params.Args) >= 5 && request.Params.Args[4] == "search_read":
					answer([]interface{}{})
				default:
					answer(map[string]interface{}{})
				}
			}))
			defer server.Close()

			writeTestProfile(t, server.URL)
			code, out, diag := runOdoo(t, "doctor", "--profile", "lab-a", "--probe", "--output", "json")
			if code != clicore.ExitOK {
				t.Fatalf("doctor failed: %s\n%s", out, diag)
			}
			data := decode(t, out)
			capability := data["selection"].(map[string]interface{})["probe"].(map[string]interface{})["capabilityProbe"].(map[string]interface{})
			fit := capability["providerFit"].(map[string]interface{})
			notice, stated := fit["notice"].(string)
			if name == "stock" {
				if stated {
					t.Fatalf("a stock Odoo got the notice: %v", fit)
				}
				return
			}
			if !stated || !strings.Contains(notice, "does not guarantee") {
				t.Fatalf("a customised Odoo must carry the notice: %v", fit)
			}
			if third, _ := fit["thirdPartyModules"].([]interface{}); len(third) != 1 || third[0] != "x_inhouse_payroll" {
				t.Fatalf("thirdPartyModules = %v", fit["thirdPartyModules"])
			}
		})
	}
}
