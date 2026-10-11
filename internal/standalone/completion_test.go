package standalone_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
)

func TestStandaloneCompletion_RawTextPipeable(t *testing.T) {
	root := standalone.RootFor("odoo")

	shells := []string{"bash", "zsh", "fish"}
	for _, sh := range shells {
		t.Run(sh+" text mode", func(t *testing.T) {
			var out bytes.Buffer
			var diag bytes.Buffer
			streams := clicore.Streams{Out: &out, Diagnostics: &diag}

			exitCode := clicore.Run(context.Background(), root, []string{"completion", sh}, streams)
			if exitCode != clicore.ExitOK {
				t.Fatalf("expected ExitOK, got %d, diag: %s", exitCode, diag.String())
			}

			output := out.String()
			// Should NOT start with quote or JSON envelope
			if strings.HasPrefix(output, "\"") || strings.HasPrefix(output, "{") {
				t.Fatalf("expected raw text output, got JSON-like start: %s", output[:min(50, len(output))])
			}

			// Validate with shell -n if available
			if shPath, err := exec.LookPath(sh); err == nil {
				cmd := exec.Command(shPath, "-n")
				cmd.Stdin = strings.NewReader(output)
				outBytes, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s -n failed: %v\nOutput: %s", sh, err, string(outBytes))
				}
			}
		})
	}
}

func TestStandaloneCompletion_JSONEnvelope(t *testing.T) {
	root := standalone.RootFor("odoo")

	var out bytes.Buffer
	var diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}

	exitCode := clicore.Run(context.Background(), root, []string{"completion", "bash", "--output", "json"}, streams)
	if exitCode != clicore.ExitOK {
		t.Fatalf("expected ExitOK, got %d", exitCode)
	}

	var env clicore.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode JSON envelope: %v", err)
	}
	if env.Kind != clicore.KindResult || env.Status != "OK" {
		t.Fatalf("expected CommandResult / OK, got %s / %s", env.Kind, env.Status)
	}

	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map in Data, got: %T", env.Data)
	}
	if dataMap["shell"] != "bash" {
		t.Fatalf("expected shell bash, got %v", dataMap["shell"])
	}
	script, ok := dataMap["script"].(string)
	if !ok || len(script) == 0 {
		t.Fatalf("expected non-empty script in Data, got: %v", dataMap["script"])
	}
}

func TestStandaloneCompletion_CatalogParity(t *testing.T) {
	root := standalone.RootFor("odoo")

	var out bytes.Buffer
	var diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}

	exitCode := clicore.Run(context.Background(), root, []string{"completion", "bash"}, streams)
	if exitCode != clicore.ExitOK {
		t.Fatalf("expected ExitOK, got %d", exitCode)
	}

	output := out.String()
	// Check that generated catalog commands and standard commands are present in completion
	for _, expected := range []string{"people", "catalog", "doctor", "profile", "version", "call"} {
		if !strings.Contains(output, expected) {
			t.Errorf("expected completion script to contain %q", expected)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
