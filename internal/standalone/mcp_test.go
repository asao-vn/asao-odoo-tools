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

func TestMCPOffersReadsUnlessWritesAreAllowed(t *testing.T) {
	readOnly, err := agentsurface.ProjectAs(RootFor("odoo-cli"), "odoo-cli", agentsurface.Filter{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	everything, err := agentsurface.ProjectAs(RootFor("odoo-cli"), "odoo-cli", agentsurface.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]agentsurface.Tool{}
	for _, tool := range everything {
		byName[tool.Name] = tool
	}
	for _, tool := range readOnly {
		if tool.AgentAccess != clicore.AgentAccessRead {
			t.Fatalf("%s (%s) is offered on a read-only server", tool.Name, tool.AgentAccess)
		}
	}
	for name, want := range map[string]clicore.AgentAccess{
		"sales_quotation_list": clicore.AgentAccessRead,
		"catalog_list":         clicore.AgentAccessRead,
		"sales_order_confirm":  clicore.AgentAccessWrite,
	} {
		if byName[name].AgentAccess != want {
			t.Errorf("%s: access %q, want %q", name, byName[name].AgentAccess, want)
		}
	}
	confirmed := 0
	for _, tool := range everything {
		if !strings.HasPrefix(tool.Command, "odoo-cli ") {
			t.Fatalf("%s runs %q, not an odoo-cli command", tool.Name, tool.Command)
		}
		if len(tool.Name) > agentsurface.MaxToolNameLength {
			t.Fatalf("%s is longer than a host accepts", tool.Name)
		}
		if tool.Annotations.DestructiveHint && tool.AgentAccess != clicore.AgentAccessConfirm {
			t.Errorf("%s is destructive but runs without the person's confirmation", tool.Name)
		}
		if tool.AgentAccess == clicore.AgentAccessConfirm {
			confirmed++
		}
		// The person's choices never reach an agent's schema.
		for _, personal := range []string{"profile", "allow-unverified", "save-file", "provider-context"} {
			if _, offered := tool.InputSchema["properties"].(map[string]interface{})[personal]; offered {
				t.Fatalf("%s lets the agent set %s", tool.Name, personal)
			}
		}
	}
	if confirmed == 0 {
		t.Fatal("no operation asks the person to confirm")
	}
	for _, internal := range []string{"profile_set", "credential_set", "spec_apply", "call", "admin_module_install", "execute"} {
		if _, offered := byName[internal]; offered {
			t.Errorf("%s is offered to an agent", internal)
		}
	}
}

func TestServeMCPSpeaksTheCatalogUnderItsOwnNames(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var out, diag bytes.Buffer
	if code := ServeMCP(context.Background(), "odoo-cli", nil, strings.NewReader(input), &out, &diag); code != 0 {
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
	info := replies[1]["serverInfo"].(map[string]interface{})
	if info["name"] != "odoo" || info["title"] != "odoo-cli" {
		t.Fatalf("serverInfo = %v", info)
	}
	if !strings.Contains(replies[1]["instructions"].(string), "without --allow-writes") {
		t.Fatalf("the instructions do not say writes are off: %v", replies[1]["instructions"])
	}
	var names []string
	for _, tool := range replies[2]["tools"].([]interface{}) {
		names = append(names, tool.(map[string]interface{})["name"].(string))
	}
	if strings.Join(names, ",") != "odoo_find,odoo_describe,odoo_read,odoo_write" {
		t.Fatalf("tools = %v, want the four catalog tools", names)
	}
}

func TestIsMCPServeLeavesHelpToTheCommandTree(t *testing.T) {
	for args, want := range map[string]bool{
		"mcp serve":                   true,
		"mcp serve --profile a":       true,
		"mcp serve --help":            false,
		"mcp tools":                   false,
		"sales quotation list --plan": false,
	} {
		if got := IsMCPServe(strings.Fields(args)); got != want {
			t.Errorf("IsMCPServe(%q) = %v, want %v", args, got, want)
		}
	}
}
