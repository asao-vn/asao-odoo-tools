package standalone

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/asao-platform/atoc/packages/cli/agentkit/trace"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/cli/internal/mcpserver"
)

// The trace (DEC-2026-0180, rule 8): every tool call an agent makes through mcp serve is appended to a
// local, hash-chained journal of the profile it ran against: when, which agent, the command line the person
// saw, whether they confirmed, what came of it. Never what a read returned. ODOO_CLI_TRACE=off turns it off.
// An agent that runs odoo-cli in a shell, not through mcp serve, is not traced.

// traceProfile names the profile a server runs against: the one --profile names, or the default one.
func traceProfile(explicit string) string {
	if store, err := localprofile.Load(); err == nil {
		if selection, err := store.Resolve(explicit); err == nil {
			return selection.Profile.Name
		}
	}
	if explicit != "" {
		return explicit
	}
	return "default"
}

func tracePath(profile string) (string, error) {
	path, err := localprofile.DefaultPath()
	if err != nil {
		return "", err
	}
	if profile == "" || profile == "." || profile == ".." || strings.ContainsAny(profile, `/\`) {
		return "", fmt.Errorf("profile name %q cannot name a directory", profile)
	}
	return filepath.Join(filepath.Dir(path), "trace", profile, "trace.jsonl"), nil
}

// serverTrace is the mcpserver Trace of one server, or nil when tracing is off or has no place.
func serverTrace(profile string, level dataAccess, diag io.Writer) func(mcpserver.Call) {
	if strings.EqualFold(os.Getenv("ODOO_CLI_TRACE"), "off") {
		return nil
	}
	path, err := tracePath(profile)
	if err != nil {
		fmt.Fprintf(diag, "odoo-cli mcp: no trace: %v\n", err)
		return nil
	}
	journal := trace.Open(path)
	session := make([]byte, 6)
	_, _ = rand.Read(session)
	var once sync.Once
	return func(call mcpserver.Call) {
		entry := trace.Entry{
			Session: hex.EncodeToString(session), Agent: call.Agent, Profile: profile, Tool: call.Tool.Name,
			Command: call.CommandLine, Access: string(call.Tool.AgentAccess), Confirmation: call.Confirmation,
			DataAccess: level.String(),
		}
		switch {
		case call.Refusal != nil:
			entry.Status, entry.ErrorCode = "NOT_RUN", call.Refusal.Code
		default:
			var document struct {
				Status string `json:"status"`
				Data   struct {
					ProviderReference string `json:"providerReference"`
				} `json:"data"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(call.Stdout, &document)
			entry.Status, entry.ErrorCode = document.Status, document.Error.Code
			if entry.Status == "" {
				entry.Status = "ERROR"
			}
			if document.Data.ProviderReference != "" {
				entry.References = []string{document.Data.ProviderReference}
			}
		}
		if _, err := journal.Append(entry); err != nil {
			once.Do(func() { fmt.Fprintf(diag, "odoo-cli mcp: the trace at %s cannot be written: %v\n", path, err) })
		}
	}
}

func traceGroup(config programConfig) *clicore.Command {
	list := local(&clicore.Command{
		Name:    "list",
		Summary: "What agents did through mcp serve on this profile, newest first.",
		Description: `Reads the profile's trace: one entry per tool call an agent made through mcp serve, with when, which
agent, the command line, whether you confirmed it, and what came of it. Never what a read returned.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags: []clicore.Flag{
			profileFlag,
			{Name: "limit", Kind: clicore.KindInt, Description: "At most this many entries (default 50)."},
		},
		Examples: []string{config.name + " trace list --profile my-odoo --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			path, err := tracePath(traceProfile(invocation.String("profile")))
			if err != nil {
				return nil, clicore.UsageError("TRACE_PROFILE_INVALID", "%v", err)
			}
			entries, err := trace.Read(path)
			if err != nil {
				return nil, clicore.NewError(clicore.CategoryConfig, "TRACE_UNREADABLE", err.Error())
			}
			limit, err := invocation.Int("limit", 50)
			if err != nil {
				return nil, err
			}
			if limit <= 0 {
				limit = 50
			}
			out := make([]trace.Entry, 0, limit)
			for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
				out = append(out, entries[i])
			}
			return map[string]interface{}{"path": path, "entries": out, "count": len(out), "total": len(entries)}, nil
		},
	})
	verify := local(&clicore.Command{
		Name:        "verify",
		Summary:     "Check that no entry of the profile's trace was removed or changed.",
		Description: `Recomputes the hash chain of the profile's trace. Exits with an error naming the first entry that breaks it.`,
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags:       []clicore.Flag{profileFlag},
		Examples:    []string{config.name + " trace verify --profile my-odoo --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			path, err := tracePath(traceProfile(invocation.String("profile")))
			if err != nil {
				return nil, clicore.UsageError("TRACE_PROFILE_INVALID", "%v", err)
			}
			n, err := trace.Verify(path)
			var broken *trace.Broken
			if errors.As(err, &broken) {
				return nil, clicore.NewError(clicore.CategoryConflict, "TRACE_BROKEN", err.Error()).
					WithContextValue("seq", broken.Seq).WithContextValue("verifiedEntries", n)
			}
			if err != nil {
				return nil, clicore.NewError(clicore.CategoryConfig, "TRACE_UNREADABLE", err.Error())
			}
			return map[string]interface{}{"path": path, "entries": n, "intact": true}, nil
		},
	})
	return &clicore.Command{
		Name:     "trace",
		Summary:  "What agents did through mcp serve, kept in a local hash-chained journal (DEC-2026-0180).",
		Children: []*clicore.Command{list, verify},
	}
}
