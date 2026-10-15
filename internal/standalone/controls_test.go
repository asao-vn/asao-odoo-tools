package standalone

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// controls list, status and plan are offered to an agent as reads, on the profile the person started
// the server with; apply turns Odoo's controls on and stays with the person, as spec apply does.
func TestControlsAreReadByAgentsAndAppliedByThePerson(t *testing.T) {
	var out, diag bytes.Buffer
	code := clicore.Run(context.Background(), RootFor("odoo-cli"), []string{"controls", "list", "--control", "k04", "--output", "json"},
		clicore.Streams{Out: &out, Diagnostics: &diag})
	var listed struct {
		Data struct {
			Controls []struct {
				ID      string `json:"id"`
				Applies bool   `json:"applies"`
			} `json:"controls"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &listed); code != clicore.ExitOK || err != nil || len(listed.Data.Controls) != 1 || listed.Data.Controls[0].ID != "K04" {
		t.Fatalf("controls list --control k04: code %d, %.300s", code, out.String())
	}
	out.Reset()
	code = clicore.Run(context.Background(), RootFor("odoo-cli"), []string{"controls", "list", "--control", "K99", "--output", "json"},
		clicore.Streams{Out: &out, Diagnostics: &diag})
	if code == clicore.ExitOK || !strings.Contains(out.String(), "UNKNOWN_CONTROL") {
		t.Fatalf("an unknown control: code %d, %.300s", code, out.String())
	}

	tools, err := agentsurface.ProjectAs(RootFor("odoo-cli"), "odoo-cli", agentsurface.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	offered := map[string]agentsurface.Tool{}
	for _, tool := range tools {
		offered[tool.Name] = tool
	}
	for _, name := range []string{"controls_list", "controls_status", "controls_plan"} {
		tool, ok := offered[name]
		if !ok || tool.AgentAccess != clicore.AgentAccessRead {
			t.Fatalf("%s is not offered as a read (%v)", name, tool.AgentAccess)
		}
		for _, personal := range []string{"profile", "allow-unverified"} {
			if _, given := tool.InputSchema["properties"].(map[string]interface{})[personal]; given {
				t.Errorf("%s lets an agent set --%s", name, personal)
			}
		}
	}
	if _, ok := offered["controls_apply"]; ok {
		t.Fatal("controls apply is offered to an agent: turning controls on stays with the person")
	}
}

// A plan needs the controls to turn on, and parameters written CONTROL.NAME=VALUE, before any Odoo
// is contacted.
func TestControlsPlanRefusesABadSelectionBeforeOdoo(t *testing.T) {
	for _, args := range [][]string{
		{"controls", "plan", "--profile", "nowhere", "--output", "json"},
		{"controls", "plan", "--profile", "nowhere", "--enable", "K04", "--param", "K04.date", "--output", "json"},
	} {
		var out, diag bytes.Buffer
		code := clicore.Run(context.Background(), RootFor("odoo-cli"), args, clicore.Streams{Out: &out, Diagnostics: &diag})
		if code != clicore.ExitUsage || !strings.Contains(out.String(), `"category": "USAGE"`) {
			t.Fatalf("%v: code %d, %.300s", args, code, out.String())
		}
	}
}
