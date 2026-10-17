package standalone

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/agentkit/trace"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

// Every tool call an agent makes through mcp serve lands in the profile's trace, run or refused, with the
// command line and the consent, and never with what a read returned.
func TestEveryAgentCallIsTracedAndTheTraceHolds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(localprofile.FileVariable, filepath.Join(dir, "profiles.json"))
	args := []string{"--mode", "tools", "--allow-writes", "--data-access", "operate"}
	converse := func(lines ...string) {
		t.Helper()
		inRead, inWrite := io.Pipe()
		outRead, outWrite := io.Pipe()
		var diag bytes.Buffer
		exit := make(chan int, 1)
		go func() {
			exit <- ServeMCP(context.Background(), "odoo-cli", args, inRead, outWrite, &diag)
			outWrite.Close()
		}()
		replies := bufio.NewScanner(outRead)
		replies.Buffer(make([]byte, 1<<20), 1<<24)
		for _, line := range lines {
			if _, err := io.WriteString(inWrite, line+"\n"); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(line, `"id"`) && !replies.Scan() { // a request: wait for its answer, as a host does
				t.Fatalf("no answer to %s: %s", line, diag.String())
			}
		}
		inWrite.Close()
		go func() {
			for replies.Scan() {
			}
		}()
		if code := <-exit; code != 0 {
			t.Fatalf("exit %d: %s", code, diag.String())
		}
	}
	conversation := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test-agent","version":"1.2"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"catalog_describe","arguments":{"operation":"sales.quotation.list"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"secret_env","arguments":{"file":".env","name":"KEY","browser":true}}}`,
	}
	converse(conversation...)
	path := filepath.Join(dir, "trace", "default", "trace.jsonl")
	entries, err := trace.Read(path)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries %+v %v ", entries, err)
	}
	read, refused := entries[0], entries[1]
	if read.Tool != "catalog_describe" || read.Status != "OK" || read.Confirmation != trace.NotAsked || read.Agent != "test-agent 1.2" ||
		read.DataAccess != "operate" || !strings.Contains(read.Command, "catalog describe") {
		t.Errorf("read: %+v", read)
	}
	if refused.Tool != "secret_env" || refused.Status != "NOT_RUN" || refused.Confirmation != trace.Refused ||
		refused.ErrorCode != "HUMAN_CONFIRMATION_REQUIRED" || refused.Access != "CONFIRM" {
		t.Errorf("refused: %+v", refused)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "outputs") || strings.Contains(string(data), "description") {
		t.Errorf("the trace holds what a read returned: %s", data)
	}
	if n, err := trace.Verify(path); n != 2 || err != nil {
		t.Fatalf("verify %d %v", n, err)
	}
	// The person reads it with trace list, and trace verify finds a changed line.
	code, listed, _ := runTrace(t, "trace", "list", "--output", "json")
	var document struct {
		Data struct {
			Count   int           `json:"count"`
			Entries []trace.Entry `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listed, &document); err != nil || code != 0 || document.Data.Count != 2 || document.Data.Entries[0].Tool != "secret_env" {
		t.Fatalf("trace list: %d %s", code, listed)
	}
	if err := os.WriteFile(path, bytes.Replace(data, []byte(`"NOT_RUN"`), []byte(`"OK"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, verified, _ := runTrace(t, "trace", "verify", "--output", "json"); code == 0 || !strings.Contains(string(verified), "TRACE_BROKEN") {
		t.Fatalf("trace verify after a change: %d %s", code, verified)
	}
	// ODOO_CLI_TRACE=off keeps nothing.
	t.Setenv("ODOO_CLI_TRACE", "off")
	before, _ := os.ReadFile(path)
	converse(conversation...)
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("ODOO_CLI_TRACE=off still traced")
	}
}

func runTrace(t *testing.T, args ...string) (int, []byte, []byte) {
	t.Helper()
	var out, diag bytes.Buffer
	code := clicore.Run(context.Background(), RootFor("odoo-cli"), args, clicore.Streams{Out: &out, Diagnostics: &diag})
	return code, out.Bytes(), diag.Bytes()
}
