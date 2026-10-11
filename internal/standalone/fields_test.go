package standalone_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
	"github.com/asao-platform/atoc/packages/platform/providerendpoint"
)

func TestCatalogFieldsDiscovery(t *testing.T) {
	code, out, _ := runWithRoot(t, standalone.RootFor("odoo"), "commands", "list", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("commands list exit %d: %s", code, out.String())
	}
	raw, err := json.Marshal(decode(t, out)["tree"])
	if err != nil {
		t.Fatal(err)
	}
	var tree clicore.CommandSpec
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("tree unmarshal failed: %v", err)
	}

	var fieldsCmd *clicore.CommandSpec
	walk(tree, func(spec clicore.CommandSpec) {
		if strings.Join(spec.Path, " ") == "catalog fields" {
			cmd := spec
			fieldsCmd = &cmd
		}
	})

	if fieldsCmd == nil {
		t.Fatal("command 'catalog fields' not found in discovery tree for odoo binary")
	}

	if fieldsCmd.SideEffect != clicore.EffectRead {
		t.Errorf("fieldsCmd.SideEffect = %q, want READ", fieldsCmd.SideEffect)
	}
	if fieldsCmd.Destructive == nil || *fieldsCmd.Destructive {
		t.Errorf("fieldsCmd.Destructive = %v, want false", fieldsCmd.Destructive)
	}
	if fieldsCmd.Authentication != string(clicore.AuthenticationNone) {
		t.Errorf("fieldsCmd.Authentication = %q, want NONE", fieldsCmd.Authentication)
	}
	if fieldsCmd.ExecutionBoundary != clicore.ExecutionBoundaryLocalOperator {
		t.Errorf("fieldsCmd.ExecutionBoundary = %q, want LOCAL_OPERATOR", fieldsCmd.ExecutionBoundary)
	}

	hasModel := false
	hasAttr := false
	hasAttrJSON := false
	for _, flag := range fieldsCmd.Flags {
		if flag.Name == "allow-mutation" {
			t.Errorf("catalog fields command must not declare --allow-mutation")
		}
		if flag.Name == "model" {
			hasModel = true
			if !flag.Required {
				t.Errorf("--model must be required")
			}
		}
		if flag.Name == "attribute" {
			hasAttr = true
			if !flag.Repeatable {
				t.Errorf("--attribute must be repeatable")
			}
		}
		if flag.Name == "attributes-json" {
			hasAttrJSON = true
		}
	}
	if !hasModel {
		t.Errorf("missing --model flag")
	}
	if !hasAttr {
		t.Errorf("missing --attribute flag")
	}
	if !hasAttrJSON {
		t.Errorf("missing --attributes-json flag")
	}
}

func TestCatalogFieldsExecutionWithFakeServer(t *testing.T) {
	secret := "private-secret-credentials-xyz"
	var recordedArgs []interface{}
	var recordedKwargs map[string]interface{}

	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
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
		if req.Params.Method == "execute_kw" && len(req.Params.Args) >= 7 {
			recordedArgs, _ = req.Params.Args[5].([]interface{})
			recordedKwargs, _ = req.Params.Args[6].(map[string]interface{})
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"name": map[string]interface{}{
						"string":   "Name",
						"type":     "char",
						"required": true,
						"readonly": false,
					},
					"country_id": map[string]interface{}{
						"string":   "Country",
						"type":     "many2one",
						"relation": "res.country",
						"required": false,
						"readonly": false,
					},
				},
			})
		}
	}))
	defer server.Close()

	writeProfiles(t, fmt.Sprintf(`{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":%q,"database":"lab_db","login":"admin","credentialRef":"lab"}}}`, server.URL))
	t.Setenv("AERP_CREDENTIAL_LAB", secret)
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")

	code, out, diag := runWithRoot(t, standalone.RootFor("odoo"), "catalog", "fields", "--profile", "lab", "--model", "res.partner", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("catalog fields exit %d: %s\ndiag: %s", code, out.String(), diag.String())
	}

	data := decode(t, out)
	if raw, ok := data["raw"].(bool); !ok || !raw {
		t.Errorf("data.raw = %v, want true", data["raw"])
	}
	if disc, ok := data["discovery"].(string); !ok || disc != "provider_schema" {
		t.Errorf("data.discovery = %v, want provider_schema", data["discovery"])
	}
	if model, ok := data["model"].(string); !ok || model != "res.partner" {
		t.Errorf("data.model = %v, want res.partner", data["model"])
	}
	if rc, ok := data["readinessClaim"].(string); !ok || rc != "NONE" {
		t.Errorf("data.readinessClaim = %v, want NONE", data["readinessClaim"])
	}
	if ec, ok := data["evidenceClaim"].(string); !ok || ec != "NONE" {
		t.Errorf("data.evidenceClaim = %v, want NONE", data["evidenceClaim"])
	}

	route, ok := data["providerRoute"].(map[string]interface{})
	if !ok || route["connectionId"] != "lab" || route["resolution"] != "LOCAL_PROFILE" {
		t.Errorf("data.providerRoute = %+v, want connectionId lab and resolution LOCAL_PROFILE", data["providerRoute"])
	}

	hash, ok := data["schemaHash"].(string)
	if !ok || len(hash) != 64 {
		t.Errorf("data.schemaHash = %v, want 64 hex characters", data["schemaHash"])
	}

	fields, ok := data["fields"].([]interface{})
	if !ok || len(fields) != 2 {
		t.Fatalf("data.fields = %v, want 2 items", data["fields"])
	}
	f0 := fields[0].(map[string]interface{})
	f1 := fields[1].(map[string]interface{})
	if f0["name"] != "country_id" || f1["name"] != "name" {
		t.Errorf("fields sorted order incorrect: f0=%v, f1=%v", f0["name"], f1["name"])
	}

	if len(recordedArgs) != 0 {
		t.Errorf("recordedArgs = %v, want empty", recordedArgs)
	}
	defaultAttrs := []interface{}{"readonly", "relation", "required", "selection", "string", "type"}
	if !reflect.DeepEqual(recordedKwargs["attributes"], defaultAttrs) {
		t.Errorf("recordedKwargs[attributes] = %v, want %v", recordedKwargs["attributes"], defaultAttrs)
	}

	if strings.Contains(out.String(), secret) || strings.Contains(diag.String(), secret) {
		t.Fatal("secret leaked in output or diagnostics")
	}
}

func TestCatalogFieldsAttributeInputs(t *testing.T) {
	var recordedKwargs map[string]interface{}

	server := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
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
		if req.Params.Method == "execute_kw" && len(req.Params.Args) >= 7 {
			recordedKwargs, _ = req.Params.Args[6].(map[string]interface{})
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"name": map[string]interface{}{
						"type":     "char",
						"relation": false,
					},
				},
			})
		}
	}))
	defer server.Close()

	writeProfiles(t, fmt.Sprintf(`{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":%q,"database":"lab_db","login":"admin","credentialRef":"lab"}}}`, server.URL))
	t.Setenv("AERP_CREDENTIAL_LAB", "x")
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")

	// Repeatable --attribute
	code, out, _ := runWithRoot(t, standalone.RootFor("odoo"), "catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attribute", "type", "--attribute", "relation", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("repeatable --attribute exit %d: %s", code, out.String())
	}
	expectedAttrs := []interface{}{"relation", "type"}
	if !reflect.DeepEqual(recordedKwargs["attributes"], expectedAttrs) {
		t.Errorf("repeatable --attribute kwargs[attributes] = %v, want %v", recordedKwargs["attributes"], expectedAttrs)
	}

	// JSON --attributes-json
	code, out, _ = runWithRoot(t, standalone.RootFor("odoo"), "catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attributes-json", `["type","selection"]`, "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("--attributes-json exit %d: %s", code, out.String())
	}
	expectedJSONAttrs := []interface{}{"selection", "type"}
	if !reflect.DeepEqual(recordedKwargs["attributes"], expectedJSONAttrs) {
		t.Errorf("--attributes-json kwargs[attributes] = %v, want %v", recordedKwargs["attributes"], expectedJSONAttrs)
	}
}

func TestCatalogFieldsValidationBeforeProviderContact(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()

	writeProfiles(t, fmt.Sprintf(`{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":%q,"database":"lab_db","login":"admin","credentialRef":"lab"}}}`, server.URL))
	t.Setenv("AERP_CREDENTIAL_LAB", "x")
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")

	testCases := []struct {
		name     string
		args     []string
		wantCode string
	}{
		{"missing model", []string{"catalog", "fields", "--profile", "lab", "--output", "json"}, "MISSING_REQUIRED_FLAG"},
		{"empty model", []string{"catalog", "fields", "--profile", "lab", "--model", "", "--output", "json"}, "MISSING_REQUIRED_FLAG"},
		{"whitespace model", []string{"catalog", "fields", "--profile", "lab", "--model", "   ", "--output", "json"}, "MISSING_REQUIRED_FLAG"},
		{"blank attribute", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attribute", "", "--output", "json"}, "INVALID_FLAG_VALUE"},
		{"whitespace attribute", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attribute", "   ", "--output", "json"}, "INVALID_FLAG_VALUE"},
		{"duplicate attribute", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attribute", "type", "--attribute", "type", "--output", "json"}, "INVALID_FLAG_VALUE"},
		{"duplicate attribute case insensitive", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attribute", "Type", "--attribute", "type", "--output", "json"}, "INVALID_FLAG_VALUE"},
		{"empty attributes-json", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attributes-json", "", "--output", "json"}, "INVALID_JSON"},
		{"invalid attributes-json", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attributes-json", "not-json", "--output", "json"}, "INVALID_JSON"},
		{"blank element in attributes-json", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attributes-json", `["type",""]`, "--output", "json"}, "INVALID_FLAG_VALUE"},
		{"duplicate in attributes-json", []string{"catalog", "fields", "--profile", "lab", "--model", "res.partner", "--attributes-json", `["type","type"]`, "--output", "json"}, "INVALID_FLAG_VALUE"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callsBefore := calls
			code, out, _ := runWithRoot(t, standalone.RootFor("odoo"), tc.args...)
			if code == clicore.ExitOK {
				t.Fatalf("expected error, got ExitOK: %s", out.String())
			}
			errObj := decodeError(t, out)
			if errObj["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s: %s", errObj["code"], tc.wantCode, out.String())
			}
			if calls != callsBefore {
				t.Fatalf("refused validation made %d HTTP requests, want 0", calls-callsBefore)
			}
		})
	}
}

func TestCatalogFieldsSecretRedactionOnProviderError(t *testing.T) {
	secret := "private-secret-42"
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

	writeProfiles(t, fmt.Sprintf(`{"version":"aerp.profiles/v1","defaultProfile":"lab","profiles":{
	  "lab":{"endpoint":%q,"database":"lab_db","login":"admin","credentialRef":"lab"}}}`, server.URL))
	t.Setenv("AERP_CREDENTIAL_LAB", secret)
	t.Setenv(providerendpoint.AllowPrivateEnvVar, "true")

	code, out, _ := runWithRoot(t, standalone.RootFor("odoo"), "catalog", "fields", "--profile", "lab", "--model", "res.partner", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("expected error, got ExitOK")
	}
	outStr := out.String()
	if strings.Contains(outStr, secret) {
		t.Fatalf("output leaked secret: %s", outStr)
	}
	if !strings.Contains(outStr, "[REDACTED]") {
		t.Fatalf("output does not contain [REDACTED]: %s", outStr)
	}
}

func TestCatalogFieldsPreservesExistingCatalogCommands(t *testing.T) {
	// 1. catalog list
	code, out, _ := runWithRoot(t, standalone.RootFor("odoo"), "catalog", "list", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("catalog list exit %d: %s", code, out.String())
	}
	data := decode(t, out)
	items, ok := data["items"].([]interface{})
	if !ok || len(items) != AcceptedCatalogSize+AcceptedSurfaceSize {
		t.Fatalf("len(items) = %d, want %d", len(items), AcceptedCatalogSize+AcceptedSurfaceSize)
	}

	// 2. catalog describe
	code, out, _ = runWithRoot(t, standalone.RootFor("odoo"), "catalog", "describe", "--operation", "people.employee.list", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("catalog describe exit %d: %s", code, out.String())
	}
	desc := decode(t, out)
	if desc["id"] != "people.employee.list" {
		t.Errorf("desc.id = %v, want people.employee.list", desc["id"])
	}
}
