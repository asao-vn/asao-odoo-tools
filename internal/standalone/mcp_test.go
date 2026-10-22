package standalone

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
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
		// Who the company pays is the person's to change, whatever the operation's risk.
		"identity_bank_account_list":         clicore.AgentAccessRead,
		"identity_bank_account_create":       clicore.AgentAccessConfirm,
		"identity_bank_account_update":       clicore.AgentAccessConfirm,
		"identity_bank_account_archive_bank": clicore.AgentAccessConfirm,
		// Code is the person's to write, whatever the record's risk: QWeb runs on the server, custom code
		// in every visitor's browser.
		"website_page_new":      clicore.AgentAccessConfirm,
		"website_page_create":   clicore.AgentAccessConfirm,
		"website_page_update":   clicore.AgentAccessConfirm,
		"website_layout_update": clicore.AgentAccessConfirm,
		"website_site_update":   clicore.AgentAccessConfirm,
		"website_page_list":     clicore.AgentAccessRead,
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
	// A secret is typed on the person's own one-time page (DEC-2026-0179): asking for one is a CONFIRM
	// tool, and its value never passes through the agent (TestAgentsAskForSecretsOnlyThroughThePage).
	for _, asks := range []string{"credential_set", "secret_env"} {
		if byName[asks].AgentAccess != clicore.AgentAccessConfirm {
			t.Errorf("%s: access %q, want CONFIRM", asks, byName[asks].AgentAccess)
		}
	}
	for _, internal := range []string{"profile_set", "credential_remove", "spec_apply", "call", "admin_module_install", "execute"} {
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

// Every write asks the person by default; --confirm-writes=false lets the low- and medium-risk ones
// run, and a change to a bank account asks even then. A host that cannot ask gets a refusal naming the
// odoo-cli command line; a person who declines stops the call; and what they are asked says what the
// call runs with.
func TestEveryWriteAsksThePersonUnlessToldOtherwise(t *testing.T) {
	t.Setenv("AERP_PROFILE_FILE", t.TempDir()+"/none.json")
	type outcome struct{ code, commandLine, asked string }
	exchange := func(args []string, tool string, canAsk bool) outcome {
		t.Helper()
		// The input stays open until the call is answered, as a host's does: input that ends means the
		// host has gone, and the server then cancels what is still running.
		inRead, inWrite := io.Pipe()
		outRead, outWrite := io.Pipe()
		var diag bytes.Buffer
		exit := make(chan int, 1)
		go func() {
			exit <- ServeMCP(context.Background(), "odoo-cli", args, inRead, outWrite, &diag)
			_ = outWrite.Close()
		}()
		capabilities := `{}`
		if canAsk {
			capabilities = `{"elicitation":{}}`
		}
		lines := make(chan string, 4)
		lines <- `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":` + capabilities + `,"clientInfo":{"name":"test","version":"0"}}}`
		lines <- `{"jsonrpc":"2.0","method":"notifications/initialized"}`
		lines <- `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
		go func() {
			for line := range lines {
				_, _ = io.WriteString(inWrite, line+"\n")
			}
		}()
		var got outcome
		answered := false
		scanner := bufio.NewScanner(outRead)
		scanner.Buffer(make([]byte, 1<<20), 1<<24)
		for !answered && scanner.Scan() {
			var message struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params struct {
					Message string `json:"message"`
				} `json:"params"`
				Result struct {
					Structured struct {
						Error struct {
							Code    string `json:"code"`
							Context struct {
								CommandLine string `json:"commandLine"`
							} `json:"context"`
						} `json:"error"`
					} `json:"structuredContent"`
				} `json:"result"`
			}
			if json.Unmarshal(scanner.Bytes(), &message) != nil {
				continue
			}
			switch {
			case message.Method == "elicitation/create":
				got.asked = message.Params.Message
				lines <- `{"jsonrpc":"2.0","id":` + string(message.ID) + `,"result":{"action":"decline"}}`
			case string(message.ID) == "2":
				got.code, got.commandLine = message.Result.Structured.Error.Code, message.Result.Structured.Error.Context.CommandLine
				answered = true
			}
		}
		close(lines)
		_ = inWrite.Close()
		go func() { _, _ = io.Copy(io.Discard, outRead) }()
		if status := <-exit; status != 0 || !answered {
			t.Fatalf("exit %d, answered %v: %s", status, answered, diag.String())
		}
		return got
	}
	writes := []string{"--allow-writes", "--mode", "tools", "--toolsets", "identity"}
	refused := exchange(writes, "identity_bank_account_update", false)
	if refused.code != "HUMAN_CONFIRMATION_REQUIRED" || !strings.HasPrefix(refused.commandLine, "odoo-cli identity bank_account update") {
		t.Errorf("a bank account change, on a host that cannot ask: %+v", refused)
	}
	declined := exchange(writes, "identity_bank_account_update", true)
	if declined.code != "HUMAN_CONFIRMATION_DECLINED" || !strings.Contains(declined.asked, "full authority of the Odoo login") ||
		strings.Contains(declined.asked, "ASAO") {
		t.Errorf("a bank account change the person declined: %+v", declined)
	}
	if got := exchange(writes, "identity_contact_create", false); got.code != "HUMAN_CONFIRMATION_REQUIRED" {
		t.Errorf("a low-risk write ran without the person by default: %+v", got)
	}
	unattended := append(append([]string(nil), writes...), "--confirm-writes=false")
	if got := exchange(unattended, "identity_contact_create", false); got.code == "HUMAN_CONFIRMATION_REQUIRED" {
		t.Errorf("a low-risk write asked with --confirm-writes=false")
	}
	if got := exchange(unattended, "identity_bank_account_update", false); got.code != "HUMAN_CONFIRMATION_REQUIRED" {
		t.Errorf("a bank account change ran without the person under --confirm-writes=false: %+v", got)
	}
	// A command line written for 0.13.1 still starts.
	if got := exchange(append(append([]string(nil), writes...), "--confirm-writes"), "identity_contact_create", false); got.code != "HUMAN_CONFIRMATION_REQUIRED" {
		t.Errorf("--confirm-writes: %+v", got)
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
