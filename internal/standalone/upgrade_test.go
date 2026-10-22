package standalone_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

// upgradeOdoo is a 19.0 database with a few customizations: one a person made and archived, one a
// module shipped, one exported, a server action whose code holds a credential, and a record rule.
func upgradeOdoo(t *testing.T, calls *[]string, mu *sync.Mutex) http.HandlerFunc {
	fieldsGet := map[string]map[string]interface{}{
		"res.partner": {"name": map[string]interface{}{"type": "char", "store": true, "searchable": true},
			"is_company": map[string]interface{}{"type": "boolean", "store": true, "searchable": true}},
		"crm.stage": {"name": map[string]interface{}{"type": "char", "store": true, "searchable": true},
			"color": map[string]interface{}{"type": "integer", "store": true, "searchable": true}},
		"ir.filters": {"name": map[string]interface{}{"type": "char"}, "model_id": map[string]interface{}{"type": "selection"},
			"domain": map[string]interface{}{"type": "text"}, "context": map[string]interface{}{"type": "text"},
			"sort": map[string]interface{}{"type": "char"}, "active": map[string]interface{}{"type": "boolean"}},
		"ir.actions.server": {"name": map[string]interface{}{"type": "char"}, "model_id": map[string]interface{}{"type": "many2one", "relation": "ir.model"},
			"state": map[string]interface{}{"type": "selection"}, "code": map[string]interface{}{"type": "text"}},
		"ir.rule": {"name": map[string]interface{}{"type": "char"}, "model_id": map[string]interface{}{"type": "many2one", "relation": "ir.model"},
			"domain_force": map[string]interface{}{"type": "text"}, "active": map[string]interface{}{"type": "boolean"}},
		"ir.model.fields": {"model": map[string]interface{}{"type": "char"}, "name": map[string]interface{}{"type": "char"},
			"ttype": map[string]interface{}{"type": "selection"}, "related": map[string]interface{}{"type": "char"}},
	}
	records := map[string][]map[string]interface{}{
		"ir.filters": {
			{"id": 1, "name": "made by a person", "model_id": "crm.stage", "domain": "[('color', '=', 1)]", "context": "{}", "sort": "[]", "active": false},
			{"id": 2, "name": "shipped by crm", "model_id": "crm.stage", "domain": "[('color', '=', 2)]", "context": "{}", "sort": "[]", "active": true},
			{"id": 3, "name": "exported", "model_id": "res.partner", "domain": "[('is_company', '=', True)]", "context": "{}", "sort": "[]", "active": true},
		},
		"ir.actions.server": {
			{"id": 7, "name": "secret code", "model_id": []interface{}{1, "Contact"}, "state": "code", "code": "token = 'sk-live-0123'\nrecords.mapped('is_company')"},
		},
		"ir.rule": {
			{"id": 9, "name": "my rule", "model_id": []interface{}{1, "Contact"}, "domain_force": "[('is_company', '=', True)]", "active": true},
		},
		"ir.model.fields": {},
	}
	owners := map[string][]map[string]interface{}{
		"ir.filters": {{"module": "crm", "res_id": 2}, {"module": "__export__", "res_id": 3}},
	}
	models := []interface{}{
		map[string]interface{}{"id": 1, "model": "res.partner", "state": "base"},
		map[string]interface{}{"id": 2, "model": "crm.stage", "state": "base"},
		map[string]interface{}{"id": 3, "model": "ir.filters", "state": "base"},
		map[string]interface{}{"id": 4, "model": "ir.actions.server", "state": "base"},
		map[string]interface{}{"id": 5, "model": "ir.rule", "state": "base"},
		map[string]interface{}{"id": 6, "model": "ir.model.fields", "state": "base"},
	}
	return odoo19(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Params struct {
				Service string        `json:"service"`
				Method  string        `json:"method"`
				Args    []interface{} `json:"args"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Error(err)
			return
		}
		answer := func(result interface{}) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": result})
		}
		if call.Params.Service == "common" {
			answer(1)
			return
		}
		args := call.Params.Args
		model, method := args[3].(string), args[4].(string)
		positional, _ := args[5].([]interface{})
		kwargs, _ := args[6].(map[string]interface{})
		mu.Lock()
		*calls = append(*calls, model+"."+method)
		mu.Unlock()
		switch method {
		case "search_read", "search", "read", "fields_get":
		default:
			t.Errorf("the check called %s.%s", model, method)
		}
		if model != "ir.module.module" {
			context, _ := kwargs["context"].(map[string]interface{})
			if context["active_test"] != false {
				t.Errorf("%s.%s without active_test false: %v", model, method, kwargs)
			}
		}
		switch {
		case model == "ir.module.module":
			answer([]interface{}{map[string]interface{}{"name": "base", "state": "installed", "latest_version": "19.0.1.3"}})
		case model == "ir.model" && method == "search_read":
			answer(models)
		case method == "fields_get":
			answer(fieldsGet[model])
		case model == "ir.model.data":
			domain := positional[0].([]interface{})
			container := domain[0].([]interface{})[2].(string)
			rows := []interface{}{}
			for _, row := range owners[container] {
				rows = append(rows, row)
			}
			answer(rows)
		case method == "search":
			ids := []interface{}{}
			for _, row := range records[model] {
				ids = append(ids, row["id"])
			}
			answer(ids)
		case method == "read":
			wanted := map[float64]bool{}
			for _, id := range positional[0].([]interface{}) {
				wanted[id.(float64)] = true
			}
			rows := []interface{}{}
			for _, row := range records[model] {
				if wanted[float64(row["id"].(int))] {
					rows = append(rows, row)
				}
			}
			answer(rows)
		default:
			t.Errorf("unexpected %s.%s", model, method)
			answer([]interface{}{})
		}
	})
}

func TestUpgradeCheckReadsWhatPeopleMadeAndNeverPrintsTheirCode(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	server := httptest.NewServer(upgradeOdoo(t, &calls, &mu))
	defer server.Close()
	writeTestProfile(t, server.URL)
	report := filepath.Join(t.TempDir(), "report.md")

	code, out, diag := runOdoo(t, "upgrade", "check", "--profile", "lab-a", "--to", "20.0", "--markdown-file", report, "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s\n%s", code, out.String(), diag.String())
	}
	if strings.Contains(out.String(), "sk-live") {
		t.Fatalf("the report prints the server action's code: %s", out.String())
	}
	data := decode(t, out)
	if data["from"] != "19.0" || data["to"] != "20.0" {
		t.Fatalf("from %v to %v", data["from"], data["to"])
	}
	scanned := data["scanned"].(map[string]interface{})
	if scanned["filters"] != 2.0 || scanned["server_actions"] != 1.0 || scanned["rules"] != 1.0 {
		t.Fatalf("scanned %v: the filter crm ships is no customization, the archived and the exported ones are", scanned)
	}
	var got []string
	for _, item := range data["findings"].([]interface{}) {
		f := item.(map[string]interface{})
		got = append(got, f["name"].(string)+" "+f["problem"].(string)+" "+f["severity"].(string))
	}
	want := "made by a person FIELD_MISSING BREAKS|my rule CONTAINER_REPLACED BREAKS"
	if strings.Join(got, "|") != want {
		t.Fatalf("findings %v, want %s", got, want)
	}
	markdown, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), "Quy tắc bản ghi \"my rule\"") || !strings.Contains(string(markdown), "ir.access") ||
		strings.Contains(string(markdown), "sk-live") {
		t.Fatalf("the person's report:\n%s", markdown)
	}
}

func TestUpgradeCheckRefusesATargetWithoutAMapBeforeContactingOdoo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("contacted Odoo: %s", r.URL.Path)
	}))
	defer server.Close()
	writeTestProfile(t, server.URL)
	code, out, _ := runOdoo(t, "upgrade", "check", "--profile", "lab-a", "--to", "21.0", "--output", "json")
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if code != clicore.ExitUsage || envelope.Error.Code != "UPGRADE_MAP_UNKNOWN" {
		t.Fatalf("exit %d: %s", code, out.String())
	}
}

func TestUpgradeMapsListsThePairs(t *testing.T) {
	code, out, _ := runOdoo(t, "upgrade", "maps", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	data := decode(t, out)
	maps := data["maps"].([]interface{})
	first := maps[0].(map[string]interface{})
	if len(maps) != 1 || first["from"] != "19.0" || first["to"] != "20.0" || first["targetVersion"] != "20.0-20260926" {
		t.Fatalf("maps %v", maps)
	}
}

// A pack registered for the profile is checked with the customizations: its operation reading a field
// 20.0 drops breaks, and is named as a pack operation.
func TestUpgradeCheckReadsThePacksOfTheProfile(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	server := httptest.NewServer(upgradeOdoo(t, &calls, &mu))
	defer server.Close()
	writeTestProfile(t, server.URL)
	dir := filepath.Join(filepath.Dir(os.Getenv(localprofile.FileVariable)), "packs", "lab-a")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stored := `{"pack": {"schema": "odoo-cli.pack/v1", "module": "stage_tools", "operations": [
		{"id": "stage_tools.stage.list", "kind": "READ", "strategy": "READ", "model": "crm.stage", "readFields": ["name", "color"], "defaultSort": "name"}]},
		"origin": {"module": "stage_tools"}, "series": "19.0"}`
	if err := os.WriteFile(filepath.Join(dir, "stage_tools.json"), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, diag := runOdoo(t, "upgrade", "check", "--profile", "lab-a", "--to", "20.0", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s\n%s", code, out.String(), diag.String())
	}
	data := decode(t, out)
	if data["scanned"].(map[string]interface{})["pack_operations"] != 1.0 {
		t.Fatalf("scanned %v", data["scanned"])
	}
	found := false
	for _, item := range data["findings"].([]interface{}) {
		f := item.(map[string]interface{})
		if f["kind"] == "pack operation" {
			found = f["name"] == "stage_tools.stage.list" && f["path"] == "color" && f["severity"] == "BREAKS"
		}
	}
	if !found {
		t.Fatalf("no BREAKS finding on the pack's color: %v", data["findings"])
	}
}
