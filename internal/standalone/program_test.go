package standalone_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
)

func runWithRoot(t *testing.T, root *clicore.Command, args ...string) (int, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), root, args, clicore.Streams{Out: out, Diagnostics: diag})
	return code, out, diag
}

// TestRootForPublishesTheActualBinaryName catches a direct binary that runs the right provider
// path but tells users and agents to invoke a different executable in its envelope.
func TestRootForPublishesTheActualBinaryName(t *testing.T) {
	code, out, _ := runWithRoot(t, standalone.RootFor("odoo"), "version", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	data := decode(t, out)
	if data["program"] != "odoo" {
		t.Fatalf("program = %v, want odoo", data["program"])
	}

	code, out, _ = runWithRoot(t, standalone.RootFor("odoo"), "commands", "list", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("commands list exit %d: %s", code, out.String())
	}
	commands := decode(t, out)
	if commands["program"] != "odoo" {
		t.Fatalf("commands list program = %v, want odoo", commands["program"])
	}
	if commands["cliVersion"] == nil || commands["cliVersion"] == "" {
		t.Fatal("commands list did not publish cliVersion")
	}
}

// TestVersionPublishesCompatibilityIdentities catches a version command that reports only the CLI
// binary version while hiding the catalog and semantic implementation actually shipped inside it.
func TestVersionPublishesCompatibilityIdentities(t *testing.T) {
	code, out, _ := runWithRoot(t, standalone.Root(), "version", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("version exit %d: %s", code, out.String())
	}
	version := decode(t, out)
	for _, field := range []string{"catalogIdentity", "executionBuildIdentity", "semanticContractIdentity", "configSchema"} {
		value, ok := version[field].(string)
		if !ok || value == "" {
			t.Fatalf("version.%s = %v, want a non-empty string", field, version[field])
		}
	}
	if version["configSchema"] != "aerp.profiles/v1" {
		t.Fatalf("version.configSchema = %v, want aerp.profiles/v1", version["configSchema"])
	}
}

// TestRenamedBinaryDoesNotPublishHistoricalCommandTokens catches stale help, examples or
// remediation text that makes the new `odoo` binary teach callers to invoke `aerp` instead.
func TestRenamedBinaryDoesNotPublishHistoricalCommandTokens(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"commands", "list", "--output", "json"}, {"catalog", "list", "--output", "json"}} {
		code, out, _ := runWithRoot(t, standalone.RootFor("odoo"), args...)
		if code != clicore.ExitOK {
			t.Fatalf("%v exit %d: %s", args, code, out.String())
		}
		if strings.Contains(out.String(), "aerp ") {
			t.Fatalf("%v published the historical command token: %s", args, out.String())
		}
	}
}
