package standalone

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func TestDataAccessIsLoweredByTheServerNeverRaised(t *testing.T) {
	cases := []struct {
		profile, flag string
		want          dataAccess
		refused       bool
	}{
		{"", "", accessAct, false},
		{"", "full", accessFull, false},
		{"", "operate", accessOperate, false},
		{"full", "", accessFull, false},
		{"full", "act", accessAct, false},
		{"act", "full", accessAct, true},
		{"operate", "act", accessAct, true},
		{"", "everything", accessAct, true},
		{"loose", "", accessAct, true},
	}
	for _, c := range cases {
		got, err := resolveDataAccess(c.profile, c.flag)
		if (err != nil) != c.refused || (!c.refused && got != c.want) {
			t.Errorf("profile %q flag %q: %s %v", c.profile, c.flag, got, err)
		}
	}
}

func decodeDocument(t *testing.T, document string) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(document), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

const readResult = `{"apiVersion":"cli.asao/v1","kind":"CommandResult","command":"odoo-cli explore search","status":"OK",
 "data":{"model":"res.partner","explanation":"` + "a long explanation of odoo-cli's own, which is not a record and stays whole even though it runs well beyond one hundred and sixty characters of text in all" + `",
  "records":[{"id":7,"name":"Nguyễn Văn A","email":"a@example.com","phone":"0900","comment":"<p>call me</p>",
   "x_customer_remark":"VIP","x_notes2":"line one\nline two","partner_id":[3,"Company"],"amount_total":1500000,"api_key":"k"}],
  "groups":[{"email":"a@example.com","__count":2}]}}`

func TestActWithholdsPersonalDataFreeTextAndFilesFromRecordsOnly(t *testing.T) {
	filter := dataAccessFilter(accessAct)
	got := filter(agentsurface.Tool{Command: "odoo-cli explore search"}, decodeDocument(t, readResult))
	data := got["data"].(map[string]interface{})
	record := data["records"].([]interface{})[0].(map[string]interface{})
	for field, want := range map[string]string{
		"email": "[withheld: personal]", "phone": "[withheld: personal]", "comment": "[withheld: free text]",
		"x_customer_remark": "[withheld: free text]", "x_notes2": "[withheld: free text]", "api_key": "[withheld: credential]",
		"name": "Nguyễn Văn A",
	} {
		if record[field] != want {
			t.Errorf("%s: %v, want %s", field, record[field], want)
		}
	}
	if record["amount_total"] != 1500000.0 || record["partner_id"].([]interface{})[1] != "Company" {
		t.Errorf("plain fields: %v", record)
	}
	if group := data["groups"].([]interface{})[0].(map[string]interface{}); group["email"] != "[withheld: personal]" {
		t.Errorf("a grouped row keeps its personal key: %v", group)
	}
	if !strings.HasPrefix(data["explanation"].(string), "a long explanation") || got["dataAccess"] != "act" {
		t.Errorf("odoo-cli's own text or the level: %v %v", data["explanation"], got["dataAccess"])
	}
	// Discovery is never filtered, and full changes nothing.
	describe := decodeDocument(t, `{"status":"OK","data":{"description":"Confirm a quotation.","inputs":[]}}`)
	if out := filter(agentsurface.Tool{Command: "odoo-cli catalog describe"}, describe); out["data"].(map[string]interface{})["description"] != "Confirm a quotation." {
		t.Errorf("discovery was filtered: %v", out)
	}
	if out := dataAccessFilter(accessFull)(agentsurface.Tool{Command: "odoo-cli explore search"}, decodeDocument(t, readResult)); out["data"].(map[string]interface{})["records"].([]interface{})[0].(map[string]interface{})["email"] != "a@example.com" {
		t.Errorf("full withheld a field: %v", out)
	}
}

func TestOperateGivesStatusIdsAndVerdictsNeverValues(t *testing.T) {
	filter := dataAccessFilter(accessOperate)
	outcome := decodeDocument(t, `{"apiVersion":"cli.asao/v1","kind":"CommandResult","command":"odoo-cli execute","status":"OK",
	 "data":{"operation":"sales.order.confirm","status":"SUCCEEDED","verificationStatus":"VERIFIED","providerReference":"sale.order,42",
	  "data":{"id":42,"name":"S00042","partner_id":[7,"Nguyễn Văn A"],"amount_total":1500000},"providerRoute":{"profile":"shop","endpoint":"https://erp.example.com"}}}`)
	got := filter(agentsurface.Tool{Command: "odoo-cli execute"}, outcome)
	text, _ := json.Marshal(got)
	for _, leaked := range []string{"S00042", "Nguyễn", "1500000", "erp.example.com"} {
		if strings.Contains(string(text), leaked) {
			t.Errorf("operate let %q through: %s", leaked, text)
		}
	}
	data := got["data"].(map[string]interface{})
	if data["verificationStatus"] != "VERIFIED" || data["data"].(map[string]interface{})["id"] != 42.0 || got["dataAccess"] != "operate" {
		t.Errorf("operate lost what the agent needs: %s", text)
	}
	refused := decodeDocument(t, `{"status":"ERROR","error":{"code":"AERP_PROVIDER_REFUSED","category":"PROVIDER","message":"The partner Nguyễn Văn A has no address","context":{"field":"partner_id","record":{"name":"Nguyễn Văn A"}}}}`)
	got = filter(agentsurface.Tool{Command: "odoo-cli execute"}, refused)
	text, _ = json.Marshal(got)
	if strings.Contains(string(text), "Nguyễn") || !strings.Contains(string(text), "AERP_PROVIDER_REFUSED") || !strings.Contains(string(text), "partner_id") {
		t.Errorf("an Odoo refusal at operate: %s", text)
	}
	usage := decodeDocument(t, `{"status":"ERROR","error":{"code":"AERP_UNKNOWN_FIELD","category":"USAGE","message":"sales.order.confirm does not accept a field named \"x\""}}`)
	if got := filter(agentsurface.Tool{Command: "odoo-cli execute"}, usage); !strings.Contains(got["error"].(map[string]interface{})["message"].(string), "does not accept") {
		t.Errorf("odoo-cli's own message was withheld: %v", got)
	}
}

func TestOperateLeavesNoReadToolButDiscovery(t *testing.T) {
	tools := []agentsurface.Tool{
		{Name: "explore_search", Command: "odoo-cli explore search", AgentAccess: clicore.AgentAccessRead},
		{Name: "sales_order_list", Command: "odoo-cli sales order list", AgentAccess: clicore.AgentAccessRead},
		{Name: "catalog_describe", Command: "odoo-cli catalog describe", AgentAccess: clicore.AgentAccessRead},
		{Name: "sales_order_confirm", Command: "odoo-cli sales order confirm", AgentAccess: clicore.AgentAccessConfirm},
	}
	var names []string
	for _, tool := range toolsAtLevel(tools, accessOperate) {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "catalog_describe,sales_order_confirm" {
		t.Fatalf("operate tools: %v", names)
	}
	if len(toolsAtLevel(tools, accessAct)) != 4 {
		t.Fatal("act took a tool away")
	}
}

func TestServeAtOperateOffersNoReadAndSaysSo(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var out, diag bytes.Buffer
	args := []string{"--mode", "tools", "--allow-writes", "--data-access", "operate"}
	if code := ServeMCP(context.Background(), "odoo-cli", args, strings.NewReader(input), &out, &diag); code != 0 {
		t.Fatalf("exit %d: %s", code, diag.String())
	}
	replies := map[float64]map[string]interface{}{}
	scanner := bufio.NewScanner(&out)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		var reply map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			t.Fatalf("not a protocol message: %q", scanner.Text())
		}
		if id, ok := reply["id"].(float64); ok {
			replies[id] = reply["result"].(map[string]interface{})
		}
	}
	if !strings.Contains(replies[1]["instructions"].(string), "Data level operate") {
		t.Fatalf("the instructions do not give the level: %v", replies[1]["instructions"])
	}
	names := map[string]bool{}
	for _, tool := range replies[2]["tools"].([]interface{}) {
		names[tool.(map[string]interface{})["name"].(string)] = true
	}
	if names["explore_search"] || names["sales_order_list"] || !names["catalog_describe"] || !names["sales_order_confirm"] {
		t.Fatalf("operate tools: explore_search %v, sales_order_list %v, catalog_describe %v, sales_order_confirm %v",
			names["explore_search"], names["sales_order_list"], names["catalog_describe"], names["sales_order_confirm"])
	}
}
