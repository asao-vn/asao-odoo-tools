package standalone_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
	"github.com/asao-platform/atoc/packages/odoo"
)

const salonPack = `{
  "schema": "odoo-cli.pack/v1",
  "module": "x_salon",
  "title": "Salon bookings",
  "author": "A partner",
  "series": ["19.0"],
  "operations": [
    {"id": "x_salon.booking.list", "kind": "READ", "strategy": "READ", "model": "x_salon.booking",
     "summary": "Bookings", "readFields": ["name", "partner_id", "start", "state"]},
    {"id": "x_salon.booking.create", "kind": "COMMAND", "strategy": "FIELD_WRITE", "model": "x_salon.booking",
     "writeMode": "CREATE", "fields": ["partner_id", "start", "seats", "vip"]},
    {"id": "x_salon.booking.confirm", "kind": "ACTION", "strategy": "METHOD_CALL", "model": "x_salon.booking",
     "method": "action_confirm", "risk": "LOW", "summary": "Confirm the booking"},
    {"id": "x_salon.booking.reschedule", "kind": "ACTION", "strategy": "WIZARD", "model": "x_salon.booking",
     "wizard": {"model": "x_salon.reschedule", "method": "action_apply", "fields": ["start"]}},
    {"id": "x_salon.booking.print", "kind": "READ", "strategy": "REPORT", "model": "x_salon.booking",
     "report": "x_salon.report_booking"}
  ]
}`

// packOdoo is a 19.0 with two custom modules: x_salon, which ships the pack it is given at its static
// path, and x_other, which ships none.
func packOdoo(t *testing.T, calls *[]string, mu *sync.Mutex, packs map[string]string) http.HandlerFunc {
	fields := map[string]map[string]interface{}{
		"x_salon.booking": {
			"name":       map[string]interface{}{"type": "char", "store": true},
			"partner_id": map[string]interface{}{"type": "many2one", "relation": "res.partner", "store": true},
			"start":      map[string]interface{}{"type": "datetime", "store": true},
			"seats":      map[string]interface{}{"type": "integer", "store": true},
			"vip":        map[string]interface{}{"type": "boolean", "store": true},
			"state":      map[string]interface{}{"type": "selection", "store": true, "readonly": true},
			"photo":      map[string]interface{}{"type": "binary", "store": true},
			"line_ids":   map[string]interface{}{"type": "one2many", "relation": "x_salon.line", "store": true},
		},
		"x_salon.reschedule": {"start": map[string]interface{}{"type": "datetime", "store": true}},
	}
	views := map[string]string{
		"x_salon.booking": `<form><header><button name="action_confirm" type="object" string="Confirm"/>` +
			`<button name="%(x_salon.action_reschedule)d" type="action"/></header><field name="name"/></form>`,
		"x_salon.reschedule": `<form><field name="start"/><footer><button name="action_apply" type="object"/></footer></form>`,
	}
	transient := map[string]bool{"x_salon.booking": false, "x_salon.reschedule": true, "res.partner": false}
	rpc := odoo19(func(w http.ResponseWriter, r *http.Request) {
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
		mu.Lock()
		*calls = append(*calls, fmt.Sprintf("%s.%s %v", model, method, positional))
		mu.Unlock()
		switch {
		case model == "ir.module.module":
			answer([]interface{}{
				map[string]interface{}{"name": "base", "state": "installed", "latest_version": "19.0.1.3"},
				map[string]interface{}{"name": "x_salon", "state": "installed", "latest_version": "19.0.1.0.0"},
				map[string]interface{}{"name": "x_other", "state": "installed", "latest_version": "19.0.1.0.0"},
			})
		case model == "ir.model" && method == "search_read":
			rows := []interface{}{}
			for _, wanted := range positional[0].([]interface{})[0].([]interface{})[2].([]interface{}) {
				if isTransient, ok := transient[wanted.(string)]; ok {
					rows = append(rows, map[string]interface{}{"model": wanted, "transient": isTransient})
				}
			}
			answer(rows)
		case method == "fields_get":
			answer(fields[model])
		case method == "get_views":
			answer(map[string]interface{}{"views": map[string]interface{}{"form": map[string]interface{}{"arch": views[model]},
				"list": map[string]interface{}{"arch": "<list><field name=\"name\"/></list>"}}})
		case model == "ir.actions.report" && method == "search_count":
			answer(1)
		case method == "search_count":
			answer(1)
		case method == "search":
			answer([]interface{}{5})
		case method == "read" || method == "search_read":
			answer([]interface{}{map[string]interface{}{"id": 5, "name": "Booking 5", "state": "confirmed"}})
		case model == "x_salon.booking" && method == "action_confirm":
			answer(true)
		default:
			answer(false)
		}
	})
	return func(w http.ResponseWriter, r *http.Request) {
		for _, file := range []string{"pack.json", "evidence.json"} {
			if module, ok := strings.CutSuffix(r.URL.Path, "/static/odoo-cli/"+file); ok {
				key := strings.TrimPrefix(module, "/")
				if file == "evidence.json" {
					key += "#evidence"
				}
				mu.Lock()
				content, ships := packs[key]
				mu.Unlock()
				if ships {
					_, _ = w.Write([]byte(content))
					return
				}
				http.NotFound(w, r)
				return
			}
		}
		rpc(w, r)
	}
}

func TestPackSyncKeepsACheckedPackWhoseOperationsRunLikeTheCatalogs(t *testing.T) {
	odoo.ForgetPacks()
	t.Cleanup(odoo.ForgetPacks)
	var calls []string
	var mu sync.Mutex
	server := httptest.NewServer(packOdoo(t, &calls, &mu, map[string]string{"x_salon": salonPack}))
	defer server.Close()
	writeTestProfile(t, server.URL)

	code, out, diag := runOdoo(t, "pack", "sync", "--profile", "lab-a", "--dry-run", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("dry run: exit %d: %s\n%s", code, out.String(), diag.String())
	}
	data := decode(t, out)
	packs := data["packs"].([]interface{})
	if len(packs) != 1 || packs[0].(map[string]interface{})["status"] != "ADDED" || data["customModules"] != 2.0 || data["dryRun"] != true {
		t.Fatalf("dry run: %v", data)
	}
	if _, out, _ := runOdoo(t, "pack", "list", "--profile", "lab-a", "--output", "json"); decode(t, out)["count"] != 0.0 {
		t.Fatal("a dry run kept a pack")
	}

	code, out, diag = runOdoo(t, "pack", "sync", "--profile", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("sync: exit %d: %s\n%s", code, out.String(), diag.String())
	}
	added := decode(t, out)["packs"].([]interface{})[0].(map[string]interface{})
	if added["module"] != "x_salon" || added["status"] != "ADDED" || added["operations"] != 5.0 {
		t.Fatalf("sync: %v", added)
	}
	_, out, _ = runOdoo(t, "pack", "show", "--profile", "lab-a", "--module", "x_salon", "--output", "json")
	shown := map[string]map[string]interface{}{}
	for _, item := range decode(t, out)["operations"].([]interface{}) {
		entry := item.(map[string]interface{})
		shown[entry["id"].(string)] = entry
	}
	if shown["x_salon.booking.confirm"]["risk"] != "MEDIUM" || shown["x_salon.booking.confirm"]["declaredRisk"] != "LOW" ||
		shown["x_salon.booking.confirm"]["agentAccess"] != "CONFIRM" || shown["x_salon.booking.list"]["agentAccess"] != "READ" {
		t.Fatalf("show: %v", shown)
	}

	// The kept copy has the written fields' types and relations from Odoo.
	stored := readStoredPack(t, "lab-a", "x_salon")
	create := stored.Pack.Operations[1]
	if create.Types["seats"] != "integer" || create.Types["vip"] != "boolean" || create.Types["start"] != "string" ||
		create.Types["partner_id"] != "reference" || create.Relations["partner_id"].Model != "res.partner" {
		t.Fatalf("kept types %v relations %v", create.Types, create.Relations)
	}

	diagnostics := &bytes.Buffer{}
	standalone.LoadPacks([]string{"execute", "--profile", "lab-a"}, diagnostics)
	if !odoo.IsPackOperation("x_salon.booking.confirm") || diagnostics.Len() != 0 {
		t.Fatalf("loaded: %v %s", odoo.IsPackOperation("x_salon.booking.confirm"), diagnostics.String())
	}
	code, out, _ = runOdoo(t, "execute", "--profile", "lab-a", "--operation", "x_salon.booking.confirm", "--provider-target-id", "5", "--output", "json")
	if code == clicore.ExitOK || !strings.Contains(out.String(), "UNVERIFIED") {
		t.Fatalf("an unverified pack operation ran without --allow-unverified: exit %d %s", code, out.String())
	}
	mu.Lock()
	calls = nil
	mu.Unlock()
	code, out, diag = runOdoo(t, "x_salon", "booking", "confirm", "--profile", "lab-a", "--provider-target-id", "5", "--allow-unverified", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("confirm: exit %d: %s\n%s", code, out.String(), diag.String())
	}
	mu.Lock()
	ran := strings.Join(calls, "\n")
	mu.Unlock()
	if !strings.Contains(ran, "x_salon.booking.action_confirm [[5]]") {
		t.Fatalf("the button was not called on record 5:\n%s", ran)
	}

	code, out, _ = runOdoo(t, "mcp", "tools", "--profile", "lab-a", "--allow-writes", "--confirm-writes=false", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("mcp tools: exit %d", code)
	}
	access := map[string]string{}
	for _, item := range decode(t, out)["tools"].([]interface{}) {
		tool := item.(map[string]interface{})
		access[tool["name"].(string)] = tool["agentAccess"].(string)
	}
	if access["x_salon_booking_list"] != "READ" || access["x_salon_booking_create"] != "CONFIRM" || access["x_salon_booking_confirm"] != "CONFIRM" ||
		access["pack_list"] != "READ" || access["pack_sync"] != "" {
		t.Fatalf("an agent's access: list %q create %q confirm %q pack list %q pack sync %q", access["x_salon_booking_list"],
			access["x_salon_booking_create"], access["x_salon_booking_confirm"], access["pack_list"], access["pack_sync"])
	}

	_, out, _ = runOdoo(t, "pack", "sync", "--profile", "lab-a", "--output", "json")
	if status := decode(t, out)["packs"].([]interface{})[0].(map[string]interface{})["status"]; status != "UNCHANGED" {
		t.Fatalf("a second sync: %v", status)
	}
	if code, _, _ := runOdoo(t, "pack", "remove", "--profile", "lab-a", "--module", "x_salon"); code != clicore.ExitOK {
		t.Fatalf("remove: exit %d", code)
	}
	if _, out, _ := runOdoo(t, "pack", "list", "--profile", "lab-a", "--output", "json"); decode(t, out)["count"] != 0.0 {
		t.Fatal("remove kept the pack")
	}
}

// The author's evidence, shipped next to the pack, shows the operations it covers unchanged as verified
// by the author; it changes no guard, and evidence that does not stand is left out, the pack kept.
func TestPackEvidenceIsTheAuthorsClaimAndChangesNoGuard(t *testing.T) {
	odoo.ForgetPacks()
	t.Cleanup(odoo.ForgetPacks)
	var calls []string
	var mu sync.Mutex
	served := map[string]string{"x_salon": salonPack}
	server := httptest.NewServer(packOdoo(t, &calls, &mu, served))
	defer server.Close()
	writeTestProfile(t, server.URL)

	// The author's side: sync on the lab, read each operation's definition hash, run, write the evidence.
	if code, out, diag := runOdoo(t, "pack", "sync", "--profile", "lab-a", "--output", "json"); code != clicore.ExitOK {
		t.Fatalf("sync: exit %d: %s\n%s", code, out.String(), diag.String())
	}
	_, out, _ := runOdoo(t, "pack", "show", "--profile", "lab-a", "--module", "x_salon", "--output", "json")
	hashes := map[string]string{}
	for _, item := range decode(t, out)["operations"].([]interface{}) {
		entry := item.(map[string]interface{})
		if _, has := entry["verification"]; has {
			t.Fatalf("a pack without evidence shows a verification: %v", entry)
		}
		hashes[entry["id"].(string)] = entry["definitionHash"].(string)
	}
	evidence := func(author string, confirmHash string) string {
		operations := []interface{}{}
		for _, id := range []string{"x_salon.booking.list", "x_salon.booking.create", "x_salon.booking.confirm"} {
			hash := hashes[id]
			if id == "x_salon.booking.confirm" {
				hash = confirmHash
			}
			operations = append(operations, map[string]interface{}{"id": id, "definitionHash": hash, "outcome": "AGREE"})
		}
		data, _ := json.Marshal(map[string]interface{}{"schema": odoo.PackEvidenceSchema, "module": "x_salon", "author": author,
			"odoo": map[string]interface{}{"series": "19.0"}, "runner": "a client of its own", "ranAt": "2026-10-20T08:00:00Z",
			"operations": operations})
		return string(data)
	}
	other := "sha256:" + strings.Repeat("0", 64)
	mu.Lock()
	served["x_salon#evidence"] = evidence("A partner", other)
	mu.Unlock()

	code, out, diag := runOdoo(t, "pack", "sync", "--profile", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("sync with evidence: exit %d: %s\n%s", code, out.String(), diag.String())
	}
	change := decode(t, out)["packs"].([]interface{})[0].(map[string]interface{})
	if change["status"] != "CHANGED" || change["authorVerified"] != 2.0 {
		t.Fatalf("new evidence: %v", change)
	}
	_, out, _ = runOdoo(t, "pack", "show", "--profile", "lab-a", "--module", "x_salon", "--output", "json")
	shown := decode(t, out)
	status := map[string]string{}
	for _, item := range shown["operations"].([]interface{}) {
		entry := item.(map[string]interface{})
		verification, _ := entry["verification"].(map[string]interface{})
		status[entry["id"].(string)] = fmt.Sprint(verification["status"])
		if entry["readiness"] != "UNVERIFIED" {
			t.Errorf("%s: readiness %v", entry["id"], entry["readiness"])
		}
	}
	want := map[string]string{"x_salon.booking.list": "AUTHOR_VERIFIED", "x_salon.booking.create": "AUTHOR_VERIFIED",
		"x_salon.booking.confirm": "CHANGED_SINCE_EVIDENCE", "x_salon.booking.reschedule": "NOT_IN_EVIDENCE", "x_salon.booking.print": "NOT_IN_EVIDENCE"}
	for id, expected := range want {
		if status[id] != expected {
			t.Errorf("%s: %s, want %s", id, status[id], expected)
		}
	}
	if summary, _ := shown["evidence"].(map[string]interface{}); summary == nil || summary["authorVerified"] != 2.0 || summary["author"] != "A partner" {
		t.Errorf("evidence summary: %v", shown["evidence"])
	}
	if _, out, _ := runOdoo(t, "pack", "list", "--profile", "lab-a", "--output", "json"); !strings.Contains(out.String(), `"authorVerified": 2`) {
		t.Errorf("list: %s", out.String())
	}

	// Registered: the result names the verification, and the guards stand.
	standalone.LoadPacks([]string{"execute", "--profile", "lab-a"}, &bytes.Buffer{})
	if origin, _ := odoo.PackOriginOf("x_salon.booking.list"); origin.Verification == nil || origin.Verification.Status != odoo.PackAuthorVerified {
		t.Fatalf("registered origin: %+v", origin)
	}
	code, out, _ = runOdoo(t, "execute", "--profile", "lab-a", "--operation", "x_salon.booking.list", "--output", "json")
	if code == clicore.ExitOK || !strings.Contains(out.String(), "UNVERIFIED") {
		t.Fatalf("an author-verified operation ran without --allow-unverified: exit %d %s", code, out.String())
	}
	code, out, _ = runOdoo(t, "mcp", "tools", "--profile", "lab-a", "--allow-writes", "--confirm-writes=false", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("mcp tools: exit %d", code)
	}
	for _, item := range decode(t, out)["tools"].([]interface{}) {
		tool := item.(map[string]interface{})
		if tool["name"] == "x_salon_booking_create" && tool["agentAccess"] != "CONFIRM" {
			t.Fatalf("an author-verified write is %v to an agent, not CONFIRM", tool["agentAccess"])
		}
	}

	// The same pack and evidence again: unchanged. Evidence of another author: left out, the pack kept.
	if _, out, _ := runOdoo(t, "pack", "sync", "--profile", "lab-a", "--output", "json"); !strings.Contains(out.String(), `"UNCHANGED"`) {
		t.Fatalf("a second sync: %s", out.String())
	}
	mu.Lock()
	served["x_salon#evidence"] = evidence("ASAO", hashes["x_salon.booking.confirm"])
	mu.Unlock()
	_, out, _ = runOdoo(t, "pack", "sync", "--profile", "lab-a", "--output", "json")
	change = decode(t, out)["packs"].([]interface{})[0].(map[string]interface{})
	if change["status"] != "CHANGED" || change["authorVerified"] != nil || !strings.Contains(fmt.Sprint(change["warnings"]), "evidence is ignored") {
		t.Fatalf("evidence of another author: %v", change)
	}
	_, out, _ = runOdoo(t, "pack", "show", "--profile", "lab-a", "--module", "x_salon", "--output", "json")
	if shown := decode(t, out); shown["evidence"] != nil || len(shown["operations"].([]interface{})) != 5 {
		t.Fatalf("the pack after refused evidence: %v", shown)
	}

	// A pack added from a file takes its evidence from a file.
	dir := t.TempDir()
	packFile, evidenceFile := filepath.Join(dir, "pack.json"), filepath.Join(dir, "evidence.json")
	if err := os.WriteFile(packFile, []byte(salonPack), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidenceFile, []byte(evidence("A partner", hashes["x_salon.booking.confirm"])), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, diag = runOdoo(t, "pack", "add", "--profile", "lab-a", "--file", packFile, "--evidence", evidenceFile, "--output", "json")
	if code != clicore.ExitOK || !strings.Contains(out.String(), `"authorVerified": 3`) {
		t.Fatalf("add with evidence: exit %d %s\n%s", code, out.String(), diag.String())
	}
}

func TestPackSyncRefusesWhatTheOdooDoesNotOffer(t *testing.T) {
	odoo.ForgetPacks()
	t.Cleanup(odoo.ForgetPacks)
	cases := map[string]struct{ pack, want string }{
		"a method that is no button": {strings.Replace(salonPack, `"method": "action_confirm"`, `"method": "action_purge"`, 1),
			"not an object button"},
		"a field Odoo does not have": {strings.Replace(salonPack, `"seats", "vip"]`, `"seats", "x_discount"]`, 1),
			"x_discount does not exist"},
		"a file field written": {strings.Replace(salonPack, `"seats", "vip"]`, `"seats", "photo"]`, 1),
			"binary field"},
		"a wizard that is a model": {strings.Replace(salonPack, `"model": "x_salon.reschedule"`, `"model": "res.partner"`, 1),
			"not a wizard"},
		"another module's pack": {strings.ReplaceAll(strings.Replace(salonPack, `"module": "x_salon"`, `"module": "x_other"`, 1),
			`"id": "x_salon.`, `"id": "x_other.`), "serves the pack of x_other"},
	}
	for name, c := range cases {
		var calls []string
		var mu sync.Mutex
		server := httptest.NewServer(packOdoo(t, &calls, &mu, map[string]string{"x_salon": c.pack}))
		writeTestProfile(t, server.URL)
		code, out, diag := runOdoo(t, "pack", "sync", "--profile", "lab-a", "--output", "json")
		server.Close()
		if code != clicore.ExitOK {
			t.Fatalf("%s: exit %d: %s\n%s", name, code, out.String(), diag.String())
		}
		change := decode(t, out)["packs"].([]interface{})[0].(map[string]interface{})
		if change["status"] != "REFUSED" || !strings.Contains(fmt.Sprint(change["error"]), c.want) {
			t.Errorf("%s: %v, want REFUSED with %q", name, change, c.want)
		}
		if _, out, _ := runOdoo(t, "pack", "list", "--profile", "lab-a", "--output", "json"); decode(t, out)["count"] != 0.0 {
			t.Errorf("%s: a refused pack was kept", name)
		}
	}
}

func TestPackReservedNamesCoverTheCommands(t *testing.T) {
	root := standalone.RootFor("odoo-cli")
	pack := odoo.Pack{Schema: odoo.PackSchema, Operations: []odoo.PackOperation{{Kind: "READ", Strategy: "READ", Model: "x_a", ReadFields: []string{"name"}}}}
	for _, command := range root.Children {
		if command.Name == "x_salon" {
			continue
		}
		pack.Module = command.Name
		pack.Operations[0].ID = command.Name + ".a.list"
		// A family name is refused for its own reason; any other command must be a reserved name.
		if err := odoo.RegisterPack(pack, odoo.PackOrigin{}, standalone.PackReservedNames()); err == nil {
			odoo.ForgetPacks()
			t.Errorf("a pack named %q was accepted; add it to packReserved", command.Name)
		}
	}
}

type storedPackDocument struct {
	Pack odoo.Pack `json:"pack"`
}

func readStoredPack(t *testing.T, profile, module string) storedPackDocument {
	t.Helper()
	path := filepath.Join(filepath.Dir(os.Getenv(localprofile.FileVariable)), "packs", profile, module+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored storedPackDocument
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	return stored
}
