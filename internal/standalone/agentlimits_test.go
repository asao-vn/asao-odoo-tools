package standalone

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asao-platform/atoc/packages/cli/agentkit/trace"
	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

func TestBoundsRefuseOverBudgetTooFastAndWhenStopped(t *testing.T) {
	t.Setenv(localprofile.FileVariable, filepath.Join(t.TempDir(), "profiles.json"))
	clock := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	bounds := &agentBounds{profile: "shop", maxCalls: 6, maxWrites: 2, rate: 3, now: func() time.Time { return clock }}
	read := agentsurface.Tool{Name: "sales_quotation_list", AgentAccess: clicore.AgentAccessRead}
	write := agentsurface.Tool{Name: "sales_order_confirm", AgentAccess: clicore.AgentAccessConfirm}
	code := func(err *clicore.Error) string {
		if err == nil {
			return ""
		}
		return err.Code
	}
	// Three calls in a minute pass; the fourth is too fast, and retryable.
	for i := 0; i < 3; i++ {
		if err := bounds.admit(read); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if err := bounds.admit(read); code(err) != "AGENT_RATE_LIMITED" || !err.Retryable {
		t.Fatalf("a fourth call in a minute: %v", err)
	}
	clock = clock.Add(61 * time.Second)
	// Two writes pass; a third is over the write budget.
	if bounds.admit(write) != nil || bounds.admit(write) != nil {
		t.Fatal("writes within the budget were refused")
	}
	if err := bounds.admit(write); code(err) != "AGENT_WRITE_BUDGET_SPENT" {
		t.Fatalf("a third write: %v", err)
	}
	clock = clock.Add(61 * time.Second)
	// Six calls in all (the refused ones do not count): the seventh is over the call budget.
	if err := bounds.admit(read); err != nil {
		t.Fatalf("a sixth call: %v", err)
	}
	if err := bounds.admit(read); code(err) != "AGENT_CALL_BUDGET_SPENT" {
		t.Fatalf("a sixth call: %v", err)
	}

	// The stop switch takes effect on a server already running, on its next call, and lifts on resume.
	fresh := &agentBounds{profile: "shop", rate: 0, now: time.Now}
	if code, out, _ := runTrace(t, "agents", "stop", "--profile", "shop", "--reason", "it changed prices", "--output", "json"); code != clicore.ExitOK {
		t.Fatalf("agents stop: %d %s", code, out)
	}
	err := fresh.admit(read)
	if code(err) != "AGENTS_STOPPED" || !strings.Contains(err.Message, "it changed prices") {
		t.Fatalf("a call after stop: %v", err)
	}
	_, out, _ := runTrace(t, "agents", "status", "--profile", "shop", "--output", "json")
	var status struct {
		Data struct {
			Stopped bool `json:"stopped"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &status) != nil || !status.Data.Stopped {
		t.Fatalf("agents status: %s", out)
	}
	if code, out, _ := runTrace(t, "agents", "resume", "--profile", "shop", "--output", "json"); code != clicore.ExitOK {
		t.Fatalf("agents resume: %d %s", code, out)
	}
	if err := fresh.admit(read); err != nil {
		t.Fatalf("a call after resume: %v", err)
	}
}

// A refused call never reaches the command, and the trace records it as refused.
func TestARefusedCallIsTracedAndNotRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(localprofile.FileVariable, filepath.Join(dir, "profiles.json"))
	if code, _, _ := runTrace(t, "agents", "stop", "--profile", "default", "--output", "json"); code != clicore.ExitOK {
		t.Fatal("agents stop")
	}
	defer runTrace(t, "agents", "resume", "--profile", "default")
	entries := serveAndTrace(t, dir, nil, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"catalog_describe","arguments":{"operation":"sales.quotation.list"}}}`)
	if len(entries) != 1 || entries[0].Status != "NOT_RUN" || entries[0].ErrorCode != "AGENTS_STOPPED" || entries[0].Confirmation != "refused" {
		t.Fatalf("entries %+v", entries)
	}
}

// serveAndTrace runs mcp serve as a host would, one request at a time, and returns the trace it kept.
func serveAndTrace(t *testing.T, dir string, args []string, calls ...string) []trace.Entry {
	t.Helper()
	inRead, inWrite := io.Pipe()
	outRead, outWrite := io.Pipe()
	var diag bytes.Buffer
	exit := make(chan int, 1)
	go func() {
		exit <- ServeMCP(context.Background(), "odoo-cli", append([]string{"--mode", "tools"}, args...), inRead, outWrite, &diag)
		outWrite.Close()
	}()
	replies := bufio.NewScanner(outRead)
	replies.Buffer(make([]byte, 1<<20), 1<<24)
	lines := append([]string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test-agent","version":"1"}}}`}, calls...)
	for _, line := range lines {
		if _, err := io.WriteString(inWrite, line+"\n"); err != nil {
			t.Fatal(err)
		}
		if !replies.Scan() {
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
	entries, err := trace.Read(filepath.Join(dir, "trace", "default", "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// A routine is an agent no one watches: agents stop stops it too.
func TestStoppedAgentsStopRoutines(t *testing.T) {
	folder := t.TempDir()
	t.Setenv(localprofile.FileVariable, filepath.Join(folder, "profiles.json"))
	path := filepath.Join(folder, "routines.toml")
	if code, envelope := runRoutinesCommand(t, "routines", "init", "--file", path, "--profile", "lab", "--output-folder", filepath.Join(folder, "out")); code != clicore.ExitOK {
		t.Fatalf("init: %v", envelope)
	}
	if code, _, _ := runTrace(t, "agents", "stop", "--profile", "lab", "--output", "json"); code != clicore.ExitOK {
		t.Fatal("agents stop")
	}
	for _, args := range [][]string{{"routines", "run", "--file", path, "--routine", "cong-no-qua-han"}, {"routines", "due", "--file", path}} {
		if _, envelope := runRoutinesCommand(t, args...); errorCode(envelope) != "AGENTS_STOPPED" {
			t.Errorf("%v while stopped: %v", args, envelope["error"])
		}
	}
}

// At data level operate the server runs resolve strictly, whatever the agent asks: a range on a date is
// refused (it is how an agent counted 54 invoices on 2026-10-18), and 30 calls a session at most.
func TestResolveAtOperateFindsWhatIsNamedAndCannotCount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(localprofile.FileVariable, filepath.Join(dir, "profiles.json"))
	byDate := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resolve","arguments":{"operation":"finance.invoice.list","filter":[["invoice_date",">=","2026-06-01"]]}}}`
	entries := serveAndTrace(t, dir, []string{"--data-access", "operate"}, byDate)
	if len(entries) != 1 || entries[0].ErrorCode != "RESOLVE_FILTER_REFUSED" {
		t.Fatalf("a date range at operate: %+v", entries)
	}
	if err := checkResolveOperate([]interface{}{[]interface{}{"partner_id.name", "ilike", "Lê Thành"}, []interface{}{"state", "=", "draft"}}, "id desc"); err != nil {
		t.Fatalf("a named draft: %v", err)
	}
	for _, c := range []struct {
		domain []interface{}
		order  string
	}{
		{[]interface{}{[]interface{}{"id", ">", 229}}, ""},
		{[]interface{}{[]interface{}{"amount_total", ">", 1000000}}, ""},
		{[]interface{}{[]interface{}{"state", "=", "posted"}}, "amount_total desc"},
	} {
		if err := checkResolveOperate(c.domain, c.order); err == nil {
			t.Errorf("%v %q was accepted at operate", c.domain, c.order)
		}
	}
	bounds := &agentBounds{profile: "shop", maxResolves: 2, now: time.Now}
	resolve := agentsurface.Tool{Name: "resolve", AgentAccess: clicore.AgentAccessRead}
	if bounds.admit(resolve) != nil || bounds.admit(resolve) != nil {
		t.Fatal("resolves within the budget were refused")
	}
	if err := bounds.admit(resolve); err == nil || err.Code != "AGENT_RESOLVE_BUDGET_SPENT" {
		t.Fatalf("a third resolve: %v", err)
	}
}
