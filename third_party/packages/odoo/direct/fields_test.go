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

func TestFieldsGetPathAndArgs(t *testing.T) {
	var recordedService string
	var recordedMethod string
	var recordedRPCMethod string
	var recordedModel string
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
		recordedService = req.Params.Service
		recordedMethod = req.Params.Method
		if req.Params.Method == "execute_kw" {
			a := req.Params.Args
			if len(a) >= 7 {
				recordedModel, _ = a[3].(string)
				recordedRPCMethod, _ = a[4].(string)
				recordedArgs, _ = a[5].([]interface{})
				recordedKwargs, _ = a[6].(map[string]interface{})
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"name": map[string]interface{}{
						"string":   "Name",
						"type":     "char",
						"required": true,
						"readonly": false,
					},
					"company_id": map[string]interface{}{
						"string":   "Company",
						"type":     "many2one",
						"relation": "res.company",
						"required": false,
						"readonly": false,
					},
				},
			})
		}
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.FieldsGet(context.Background(), "res.partner", nil)
	if err != nil {
		t.Fatalf("FieldsGet failed: %v", err)
	}

	if recordedService != "object" {
		t.Errorf("recordedService = %q, want object", recordedService)
	}
	if recordedMethod != "execute_kw" {
		t.Errorf("recordedMethod = %q, want execute_kw", recordedMethod)
	}
	if recordedModel != "res.partner" {
		t.Errorf("recordedModel = %q, want res.partner", recordedModel)
	}
	if recordedRPCMethod != "fields_get" {
		t.Errorf("recordedRPCMethod = %q, want fields_get", recordedRPCMethod)
	}
	if len(recordedArgs) != 0 {
		t.Errorf("recordedArgs = %#v, want empty []", recordedArgs)
	}
	expectedAttrs := []interface{}{"readonly", "relation", "required", "selection", "string", "type"}
	if passedAttrs, ok := recordedKwargs["attributes"].([]interface{}); !ok || !reflect.DeepEqual(passedAttrs, expectedAttrs) {
		t.Errorf("recordedKwargs[attributes] = %#v, want %#v", recordedKwargs["attributes"], expectedAttrs)
	}

	if !res.Raw {
		t.Errorf("res.Raw = false, want true")
	}
	if res.Discovery != "provider_schema" {
		t.Errorf("res.Discovery = %q, want provider_schema", res.Discovery)
	}
	if res.Model != "res.partner" {
		t.Errorf("res.Model = %q, want res.partner", res.Model)
	}
	if res.ReadinessClaim != "NONE" {
		t.Errorf("res.ReadinessClaim = %q, want NONE", res.ReadinessClaim)
	}
	if res.EvidenceClaim != "NONE" {
		t.Errorf("res.EvidenceClaim = %q, want NONE", res.EvidenceClaim)
	}
	if res.ProviderRoute.ConnectionID != "lab" || res.ProviderRoute.Resolution != "LOCAL_PROFILE" {
		t.Errorf("res.ProviderRoute = %+v, want lab / LOCAL_PROFILE", res.ProviderRoute)
	}
	if len(res.Fields) != 2 {
		t.Fatalf("len(res.Fields) = %d, want 2", len(res.Fields))
	}
	// Sorted field order check: company_id must precede name
	if res.Fields[0].Name != "company_id" {
		t.Errorf("res.Fields[0].Name = %q, want company_id", res.Fields[0].Name)
	}
	if res.Fields[1].Name != "name" {
		t.Errorf("res.Fields[1].Name = %q, want name", res.Fields[1].Name)
	}

	expectedHash, err := direct.ComputeSchemaHash(res.Fields)
	if err != nil {
		t.Fatal(err)
	}
	if res.SchemaHash != expectedHash {
		t.Errorf("res.SchemaHash = %q, want %q", res.SchemaHash, expectedHash)
	}
}

func TestFieldsGetCustomAttributes(t *testing.T) {
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

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.FieldsGet(context.Background(), "res.partner", []string{"type", "relation"})
	if err != nil {
		t.Fatalf("FieldsGet failed: %v", err)
	}

	expectedAttrs := []interface{}{"relation", "type"}
	if passedAttrs, ok := recordedKwargs["attributes"].([]interface{}); !ok || !reflect.DeepEqual(passedAttrs, expectedAttrs) {
		t.Errorf("recordedKwargs[attributes] = %#v, want %#v", recordedKwargs["attributes"], expectedAttrs)
	}
	if !reflect.DeepEqual(res.Attributes, []string{"relation", "type"}) {
		t.Errorf("res.Attributes = %#v, want %#v", res.Attributes, []string{"relation", "type"})
	}
}

func TestFieldsGetDeterministicOrdering(t *testing.T) {
	server1 := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
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
		if req.Params.Service == "common" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
			return
		}
		// Jumbled order 1
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"z_field": map[string]interface{}{"type": "char", "string": "Z Field"},
				"a_field": map[string]interface{}{"string": "A Field", "type": "integer"},
				"m_field": map[string]interface{}{"type": "boolean", "string": "M Field"},
			},
		})
	}))
	defer server1.Close()

	server2 := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
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
		if req.Params.Service == "common" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
			return
		}
		// Jumbled order 2
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"m_field": map[string]interface{}{"string": "M Field", "type": "boolean"},
				"z_field": map[string]interface{}{"string": "Z Field", "type": "char"},
				"a_field": map[string]interface{}{"type": "integer", "string": "A Field"},
			},
		})
	}))
	defer server2.Close()

	// Server 3 with modified attribute (drift)
	server3 := httptest.NewServer(odoo19(func(w http.ResponseWriter, r *http.Request) {
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
		if req.Params.Service == "common" {
			json.NewEncoder(w).Encode(map[string]interface{}{"result": 1})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"a_field": map[string]interface{}{"type": "integer", "string": "A Field"},
				"m_field": map[string]interface{}{"string": "M Field", "type": "boolean"},
				"z_field": map[string]interface{}{"string": "Z Field", "type": "text"}, // drift: char -> text
			},
		})
	}))
	defer server3.Close()

	e1, err := direct.NewExecutor(context.Background(), target(server1.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := direct.NewExecutor(context.Background(), target(server2.URL), allow)
	if err != nil {
		t.Fatal(err)
	}
	e3, err := direct.NewExecutor(context.Background(), target(server3.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	res1, err := e1.FieldsGet(context.Background(), "res.partner", nil)
	if err != nil {
		t.Fatalf("e1.FieldsGet failed: %v", err)
	}
	res2, err := e2.FieldsGet(context.Background(), "res.partner", nil)
	if err != nil {
		t.Fatalf("e2.FieldsGet failed: %v", err)
	}
	res3, err := e3.FieldsGet(context.Background(), "res.partner", nil)
	if err != nil {
		t.Fatalf("e3.FieldsGet failed: %v", err)
	}

	// Verify both e1 and e2 sorted order: a_field, m_field, z_field
	expectedFieldNames := []string{"a_field", "m_field", "z_field"}
	for i, want := range expectedFieldNames {
		if res1.Fields[i].Name != want {
			t.Errorf("res1.Fields[%d].Name = %q, want %q", i, res1.Fields[i].Name, want)
		}
		if res2.Fields[i].Name != want {
			t.Errorf("res2.Fields[%d].Name = %q, want %q", i, res2.Fields[i].Name, want)
		}
	}

	// Verify deterministic hash match between server1 and server2
	if res1.SchemaHash != res2.SchemaHash {
		t.Errorf("res1.SchemaHash (%s) != res2.SchemaHash (%s)", res1.SchemaHash, res2.SchemaHash)
	}

	json1, _ := json.Marshal(res1.Fields)
	json2, _ := json.Marshal(res2.Fields)
	if string(json1) != string(json2) {
		t.Errorf("canonical JSON mismatch:\njson1: %s\njson2: %s", string(json1), string(json2))
	}

	// Verify drift signal: server3 with modified type produces a different hash
	if res3.SchemaHash == res1.SchemaHash {
		t.Errorf("res3.SchemaHash should have differed from res1 due to schema drift, got %q", res3.SchemaHash)
	}
}

func TestFieldsGetValidationRefusalMakesZeroHTTPRequests(t *testing.T) {
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
		name       string
		model      string
		attributes []string
		wantErr    error
	}{
		{"blank model empty", "", nil, direct.ErrFieldsModelRequired},
		{"blank model whitespace", "   ", nil, direct.ErrFieldsModelRequired},
		{"blank attribute empty", "res.partner", []string{""}, direct.ErrFieldsAttributeBlank},
		{"blank attribute whitespace", "res.partner", []string{"   "}, direct.ErrFieldsAttributeBlank},
		{"blank attribute in list", "res.partner", []string{"type", ""}, direct.ErrFieldsAttributeBlank},
		{"duplicate attribute exact", "res.partner", []string{"type", "type"}, direct.ErrFieldsAttributeDuplicate},
		{"duplicate attribute with spaces", "res.partner", []string{"type", "  type  "}, direct.ErrFieldsAttributeDuplicate},
		{"duplicate attribute case insensitive", "res.partner", []string{"Type", "type"}, direct.ErrFieldsAttributeDuplicate},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			callsBefore := calls
			_, err := e.FieldsGet(context.Background(), tc.model, tc.attributes)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if calls != callsBefore {
				t.Fatalf("refused call made %d HTTP requests, want 0", calls-callsBefore)
			}
		})
	}
}

func TestFieldsGetSecretRedaction(t *testing.T) {
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

	_, err = e.FieldsGet(context.Background(), "res.partner", nil)
	if err == nil {
		t.Fatal("expected error from provider, got nil")
	}

	errStr := err.Error()
	if strings.Contains(errStr, secret) {
		t.Fatalf("error message leaked secret: %s", errStr)
	}
	if !strings.Contains(errStr, "[REDACTED]") {
		t.Fatalf("error message does not contain [REDACTED]: %s", errStr)
	}
}

func TestFieldsGetNestedMetadataRedaction(t *testing.T) {
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
			"result": map[string]interface{}{
				"name": map[string]interface{}{
					"type":     "char",
					"safe_top": "keep-top-level",
					"api_key":  "do-not-leak-api-key",
					"nested": map[string]interface{}{
						"Authorization": "do-not-leak-authorization",
						"safe":          "keep-nested",
						"deep": []interface{}{
							map[string]interface{}{
								"secret": "do-not-leak-secret",
								"safe":   true,
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	e, err := direct.NewExecutor(context.Background(), target(server.URL), allow)
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.FieldsGet(context.Background(), "res.partner", nil)
	if err != nil {
		t.Fatalf("FieldsGet failed: %v", err)
	}

	encoded, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	output := string(encoded)
	for _, leaked := range []string{
		"api_key",
		"do-not-leak-api-key",
		"Authorization",
		"do-not-leak-authorization",
		"secret",
		"do-not-leak-secret",
	} {
		if strings.Contains(output, leaked) {
			t.Fatalf("fields_get output leaked %q: %s", leaked, output)
		}
	}
	for _, preserved := range []string{"safe_top", "keep-top-level", "safe", "keep-nested", "true"} {
		if !strings.Contains(output, preserved) {
			t.Fatalf("fields_get output lost safe value %q: %s", preserved, output)
		}
	}

	if res.SchemaHash == "" {
		t.Fatal("schema hash is empty")
	}
	expectedHash, err := direct.ComputeSchemaHash(res.Fields)
	if err != nil {
		t.Fatal(err)
	}
	if res.SchemaHash != expectedHash {
		t.Fatalf("schema hash = %q, want deterministic hash %q", res.SchemaHash, expectedHash)
	}
}
