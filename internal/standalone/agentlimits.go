package standalone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

// Bounds on an agent (DEC-2026-0182). A person's permission in Odoo says what they may do; it does not say
// how much an agent working for them may do, how fast, or that it can be stopped at once. Every tool call
// of mcp serve passes admit first:
//   - the stop switch: agents stop --profile P makes every server on that profile refuse every call,
//     routines run refuses too, until agents resume;
//   - a call budget and a write budget for the server's life (--max-calls, --max-writes);
//   - a rate (--rate, calls a minute, 60 by default): an agent calls far more often than a person, and each
//     call takes one of Odoo's workers while it runs.

const defaultAgentRate = 60

func stopPath(profile string) (string, error) {
	path, err := localprofile.DefaultPath()
	if err != nil {
		return "", err
	}
	if profile == "" || profile == "." || profile == ".." || strings.ContainsAny(profile, `/\`) {
		return "", fmt.Errorf("profile name %q cannot name a file", profile)
	}
	return filepath.Join(filepath.Dir(path), "stop", profile+".json"), nil
}

type stopRecord struct {
	Reason string `json:"reason,omitempty"`
	At     string `json:"at"`
}

// stopped says whether the agents of a profile were stopped, and why.
func stopped(profile string) (*stopRecord, error) {
	path, err := stopPath(profile)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record stopRecord
	_ = json.Unmarshal(data, &record)
	return &record, nil
}

func stoppedError(profile string, record *stopRecord) *clicore.Error {
	message := fmt.Sprintf("the agents of profile %s were stopped at %s", profile, record.At)
	if record.Reason != "" {
		message += ": " + record.Reason
	}
	return clicore.NewError(clicore.CategoryDenied, "AGENTS_STOPPED", message).
		WithRemediation("Tell the person. Only they can run: odoo-cli agents resume --profile " + profile)
}

type agentBounds struct {
	profile             string
	maxCalls, maxWrites int
	rate                int
	now                 func() time.Time

	mu     sync.Mutex
	calls  int
	writes int
	recent []time.Time
}

func (b *agentBounds) admit(tool agentsurface.Tool) *clicore.Error {
	// The switch is read on every call, so a stop takes effect on servers already running.
	if record, err := stopped(b.profile); err != nil {
		return clicore.NewError(clicore.CategoryConfig, "AGENTS_STOP_UNREADABLE", err.Error())
	} else if record != nil {
		return stoppedError(b.profile, record)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if b.rate > 0 {
		kept := b.recent[:0]
		for _, at := range b.recent {
			if now.Sub(at) < time.Minute {
				kept = append(kept, at)
			}
		}
		b.recent = kept
		if len(b.recent) >= b.rate {
			wait := time.Minute - now.Sub(b.recent[0])
			return clicore.NewError(clicore.CategoryUnavailable, "AGENT_RATE_LIMITED",
				fmt.Sprintf("more than %d calls in a minute: each call takes one of Odoo's workers", b.rate)).
				WithRetryable(true).WithContextValue("retryAfterSeconds", int(wait.Seconds())+1).
				WithRemediation("Wait, then go on; read in fewer, larger pages where you can.")
		}
	}
	if b.maxCalls > 0 && b.calls >= b.maxCalls {
		return clicore.NewError(clicore.CategoryDenied, "AGENT_CALL_BUDGET_SPENT",
			fmt.Sprintf("this server allows %d calls, and they are spent", b.maxCalls)).
			WithRemediation("Tell the person what is left to do; they set the budget when they started the server.")
	}
	write := tool.AgentAccess != clicore.AgentAccessRead
	if write && b.maxWrites > 0 && b.writes >= b.maxWrites {
		return clicore.NewError(clicore.CategoryDenied, "AGENT_WRITE_BUDGET_SPENT",
			fmt.Sprintf("this server allows %d writes, and they are spent", b.maxWrites)).
			WithRemediation("Tell the person what is left to change; they set the budget when they started the server.")
	}
	b.calls++
	if write {
		b.writes++
	}
	b.recent = append(b.recent, now)
	return nil
}

func agentsGroup(config programConfig) *clicore.Command {
	// The switch must work when it is needed most, so a profile file that cannot be read does not stop it:
	// the name given, or the default profile's, is enough.
	resolve := func(invocation *clicore.Invocation) (string, error) {
		return traceProfile(invocation.String("profile")), nil
	}
	stop := local(&clicore.Command{
		Name:    "stop",
		Summary: "Stop every agent on a profile at once: each call of each running server is refused, and routines do not run.",
		Description: `The switch to pull when an agent does what it should not. It takes effect on servers already running, on
their next call. Nothing is undone; agents resume brings them back.`,
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags:       []clicore.Flag{profileFlag, {Name: "reason", Kind: clicore.KindString, Description: "Why, kept with the switch and shown to the agent."}},
		Examples:    []string{config.name + " agents stop --profile my-odoo --reason 'it changed prices'"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := resolve(invocation)
			if err != nil {
				return nil, err
			}
			path, err := stopPath(name)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			record := stopRecord{Reason: strings.TrimSpace(invocation.String("reason")), At: time.Now().UTC().Format(time.RFC3339)}
			data, _ := json.Marshal(record)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, clicore.InternalError(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				return nil, clicore.InternalError(err)
			}
			return map[string]interface{}{"profile": name, "stopped": true, "at": record.At, "reason": record.Reason}, nil
		},
	})
	resume := local(&clicore.Command{
		Name:        "resume",
		Summary:     "Let agents on a profile work again after agents stop.",
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessNone,
		Flags:       []clicore.Flag{profileFlag},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := resolve(invocation)
			if err != nil {
				return nil, err
			}
			path, err := stopPath(name)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, clicore.InternalError(err)
			}
			return map[string]interface{}{"profile": name, "stopped": false}, nil
		},
	})
	status := local(&clicore.Command{
		Name:        "status",
		Summary:     "Whether agents on a profile are stopped.",
		SideEffect:  clicore.EffectNone,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessRead,
		Flags:       []clicore.Flag{personalProfileFlag()},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := resolve(invocation)
			if err != nil {
				return nil, err
			}
			record, err := stopped(name)
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			out := map[string]interface{}{"profile": name, "stopped": record != nil}
			if record != nil {
				out["at"], out["reason"] = record.At, record.Reason
			}
			return out, nil
		},
	})
	return &clicore.Command{
		Name:     "agents",
		Summary:  "Stop or resume every agent on a profile at once (DEC-2026-0182).",
		Children: []*clicore.Command{stop, resume, status},
	}
}
