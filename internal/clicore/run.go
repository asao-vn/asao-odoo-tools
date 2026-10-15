package clicore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Streams are the CLI's three streams, injected so the whole dispatcher is testable without a
// process. Diagnostics is where every non-contract line goes; nothing but the result document is
// ever written to Out in JSON mode.
type Streams struct {
	Out         io.Writer
	Diagnostics io.Writer
	// In is read only by a ClientLocalServer leaf (`asao mcp serve`). Every other command ignores
	// it: no command prompts interactively.
	In io.Reader
}

// ContextDefaulter fills in a flag the caller did not type from client-local convenience state,
// BEFORE the Guard evaluates anything. Running it here, and only here, is what keeps the
// defaulted value honest: the Guard authorizes the same string the command body will read, so a
// hand-edited context file cannot make the guard authorize one organization while the command
// operates on another.
type ContextDefaulter func(inv *Invocation, diagnostics io.Writer) *Error

// ResultPersister records client-local state from a result the runtime has ALREADY returned
// successfully. It runs on the machine the person is sitting at, never on the runtime, which is
// why `asao org use` can be authorized remotely and still remember its answer locally.
type ResultPersister func(inv *Invocation, data interface{}) *Error

// Guard is the ONE authorization evaluation seam clicore exposes. Run calls root.Guard (never a
// non-root command's — see Command.Guard's own doc comment) after the --help short-circuit and the
// group/nil-Run checks, and before the resolved leaf's own Run.
//
// A nil return (both context and error nil is never valid; a nil *Error means allowed) lets Run
// continue into the leaf's Run with the returned context — which the guard may have replaced, e.g.
// to attach the authenticated Actor and a correlation id (see packages/cli/internal/authority) for
// Run and everything Run calls to read back via ActorFromContext/CorrelationIDFromContext. A non-nil
// *Error means denied, refused, or a diagnostic fault the guard itself hit while evaluating; Run
// emits it through the exact same `emit` path a Run-returned error takes, so to a caller a denial is
// indistinguishable in shape from any other refusal — same envelope, same exit-code contract.
//
// WHY THIS RUNS EXACTLY WHERE IT RUNS, AND NOWHERE ELSE: --help and `commands list` must stay
// reachable with NO authority at all, or an agent that does not yet hold a credential could never
// discover HOW to obtain one — the discovery document is the only bootstrap path out of that trap.
// Running Guard strictly after the --help/group/nil-Run checks (never before) is what keeps that
// path open while still gating every actual Run. A group is never guarded at all — Guard runs
// immediately before invocation.Command.Run(...), by which point a group has already returned via
// its own COMMAND_IS_GROUP refusal above.
//
// The guard's implementation lives in packages/cli/internal/authority, not here: clicore must not
// import runtimeenv or any bridge package (organization-service/governancebridge,
// identity-service/identitybridge), so this is a plain function value wired onto the root Command by
// commands.Root — `root.Guard = authority.NewGuard(env).Check` — exactly the same shape Command.Run
// already has.
type Guard func(ctx context.Context, inv *Invocation) (context.Context, *Error)

// OutcomeRecorder observes the actual result of an operation that the Guard
// already allowed to run. It is used by the privileged runtime to append an
// execution outcome through the established audit path. A recorder failure is
// never allowed to rewrite an already-real provider outcome.
type OutcomeRecorder func(ctx context.Context, inv *Invocation, runErr *Error)

// ErrorAdvisor annotates a refusal that a command's Run (or a panic in it) produced, just before it
// is rendered. It runs on the client, sees the resolved error and may only add context to it.
type ErrorAdvisor func(inv *Invocation, err *Error)

type countingWriter struct {
	w       io.Writer
	written int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.w == nil {
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.written += int64(n)
	return n, err
}

type lenChecker interface {
	Len() int
}

// Run parses args against root, executes the resolved command, renders the outcome, and returns
// the process exit code. It recovers unexpected panics, enforces execution timeouts, and never writes
// anything but the contract document to Streams.Out when JSON output is requested.
func Run(ctx context.Context, root *Command, args []string, streams Streams) (exitCode int) {
	if streams.Out == nil {
		streams.Out = io.Discard
	}
	if streams.Diagnostics == nil {
		streams.Diagnostics = io.Discard
	}
	var initialLen int
	if lc, ok := streams.Out.(lenChecker); ok {
		initialLen = lc.Len()
	}

	outCounter := &countingWriter{w: streams.Out}
	streams.Out = outCounter

	program := programOf(root)
	var commandPath string
	jsonOutput := wantsJSON(args)
	var emitted bool
	var advisedInvocation *Invocation

	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(streams.Diagnostics, "%s: unexpected panic: %v\n", program, r)
			hasPriorOutput := outCounter.written > 0 || emitted
			if !hasPriorOutput {
				if lc, ok := outCounter.w.(lenChecker); ok && lc.Len() > initialLen {
					hasPriorOutput = true
				}
			}
			if hasPriorOutput {
				exitCode = ExitInternal
				return
			}
			cmd := commandPath
			if cmd == "" && root != nil {
				cmd = root.PathString()
			}
			panicErr := NewError(CategoryInternal, "INTERNAL_ERROR", fmt.Sprintf("unexpected panic: %v", r))
			if root != nil && root.ErrorAdvisor != nil && advisedInvocation != nil {
				root.ErrorAdvisor(advisedInvocation, panicErr)
			}
			exitCode = emit(streams, program, cmd, jsonOutput, nil, panicErr)
			emitted = true
		}
	}()

	invocation, err := Parse(root, args)
	if err != nil {
		// A parse failure still needs a command name for the envelope. When parsing got far
		// enough to resolve one, use it; otherwise report the root.
		command := root
		if invocation != nil && invocation.Command != nil {
			command = invocation.Command
		}
		jsonOutput = wantsJSON(args)
		exitCode = emit(streams, programOf(root), command.PathString(), jsonOutput, nil, AsError(err))
		emitted = true
		return exitCode
	}

	jsonOutput = invocation.JSONOutput()
	commandPath = invocation.Command.PathString()

	if help, _ := invocation.Bool("help"); help {
		if jsonOutput {
			exitCode = emit(streams, programOf(root), invocation.Command.PathString(), true, Specify(invocation.Command, false), nil)
			emitted = true
			return exitCode
		}
		RenderHelp(streams.Out, invocation.Command)
		return ExitOK
	}

	if explain, _ := invocation.Bool("explain"); explain {
		spec := Specify(invocation.Command, false)
		explainData := map[string]interface{}{
			"command":            spec.Command,
			"path":               spec.Path,
			"summary":            spec.Summary,
			"sideEffect":         spec.SideEffect,
			"executionBoundary":  spec.ExecutionBoundary,
			"authentication":     spec.Authentication,
			"requiredCapability": spec.RequiredCapability,
			"scopeKind":          spec.ScopeKind,
			"destructive":        spec.Destructive,
			"flags":              spec.Flags,
			"message":            "Contract and execution plan explained cleanly with zero side-effects.",
		}
		exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, explainData, nil)
		emitted = true
		return exitCode
	}

	// Check common --timeout duration flag. Reject invalid/zero/negative values as usage errors.
	timeout, timeoutErr := invocation.Duration("timeout", 0)
	if timeoutErr != nil {
		exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, nil, AsError(timeoutErr))
		emitted = true
		return exitCode
	}

	if invocation.Command.IsGroup() {
		// A group is not runnable. Saying so with the list of what IS runnable is more useful
		// than printing help and exiting 0, which would let a scripted caller believe it ran.
		if jsonOutput {
			program := programOf(root)
			exitCode = emit(streams, program, invocation.Command.PathString(), true, nil,
				UsageError("COMMAND_IS_GROUP", "`%s` is a command group, not a runnable command", qualify(invocation.Command)).
					WithContextValue("availableCommands", invocation.Command.childNames()).
					WithRemediation(fmt.Sprintf("Run `%s --help` or `%s commands list --output json`.", qualify(invocation.Command), program)))
			emitted = true
			return exitCode
		}
		RenderHelp(streams.Out, invocation.Command)
		return ExitUsage
	}

	if invocation.Command.Run == nil {
		exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, nil,
			NewError(CategoryInternal, "COMMAND_NOT_IMPLEMENTED", "command has no implementation"))
		emitted = true
		return exitCode
	}

	if timeout > 0 {
		effectiveTimeout := timeout
		if effectiveTimeout > 110*time.Second {
			effectiveTimeout = 110 * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, effectiveTimeout)
		defer cancel()
	}

	if root.ContextDefaulter != nil {
		if defaultErr := root.ContextDefaulter(invocation, streams.Diagnostics); defaultErr != nil {
			exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, nil, defaultErr)
			emitted = true
			return exitCode
		}
	}

	if root.Guard != nil {
		guardedCtx, guardErr := root.Guard(ctx, invocation)
		if guardErr != nil {
			exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, nil, guardErr)
			emitted = true
			return exitCode
		}
		ctx = guardedCtx
	}

	advisedInvocation = invocation
	data, runErr := invocation.Command.Run(ctx, invocation)
	if runErr != nil {
		var resolved *Error
		var cliErr *Error
		if errors.As(runErr, &cliErr) && cliErr.Category == CategoryUnavailable {
			resolved = cliErr
		} else if errors.Is(runErr, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			resolved = NewError(CategoryUnavailable, "TIMEOUT", "command timed out before completion").
				WithRetryable(true).
				WithCause(runErr)
		} else if errors.Is(runErr, context.Canceled) || ctx.Err() == context.Canceled {
			resolved = NewError(CategoryUnavailable, "CANCELED", "command was canceled").
				WithRetryable(true).
				WithCause(runErr)
		} else {
			resolved = AsError(runErr)
		}
		if root.OutcomeRecorder != nil {
			root.OutcomeRecorder(ctx, invocation, resolved)
		}
		if root.ErrorAdvisor != nil {
			root.ErrorAdvisor(invocation, resolved)
		}
		exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, nil, resolved)
		emitted = true
		return exitCode
	}
	if root.OutcomeRecorder != nil {
		root.OutcomeRecorder(ctx, invocation, nil)
	}
	if invocation.Command.ClientContextWrite && root.ResultPersister != nil {
		if persistErr := root.ResultPersister(invocation, data); persistErr != nil {
			exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, nil, persistErr)
			emitted = true
			return exitCode
		}
	}
	exitCode = emit(streams, programOf(root), invocation.Command.PathString(), jsonOutput, data, nil)
	emitted = true
	return exitCode
}

// emit writes exactly one outcome and returns the exit code.
func emit(streams Streams, program, command string, jsonOutput bool, data interface{}, err *Error) int {
	if jsonOutput {
		envelope := Envelope{APIVersion: APIVersion, Command: joinProgram(program, command)}
		if err != nil {
			envelope.Kind, envelope.Status, envelope.Error = KindError, "ERROR", err
		} else {
			envelope.Kind, envelope.Status, envelope.Data = KindResult, "OK", data
		}
		if emitErr := EmitJSON(streams.Out, envelope); emitErr != nil {
			// The contract document could not be written. Report on diagnostics only — writing a
			// partial second document to Out would be worse than writing nothing.
			fmt.Fprintf(streams.Diagnostics, "%s: could not encode result document: %v\n", program, emitErr)
			return ExitInternal
		}
		if err != nil {
			return err.ExitCode
		}
		return ExitOK
	}

	if err != nil {
		RenderTextError(streams.Diagnostics, joinProgram(program, command), err)
		return err.ExitCode
	}
	if renderErr := RenderText(streams.Out, joinProgram(program, command), data); renderErr != nil {
		fmt.Fprintf(streams.Diagnostics, "%s: could not render result: %v\n", program, renderErr)
		return ExitInternal
	}
	return ExitOK
}

// EmitResult renders a result produced by a trusted remote authority runtime
// through the same canonical CLI envelope and text/error contract as direct
// composition. Raw JSON data is kept as json.RawMessage by the caller so it is
// never round-tripped through float64 before reaching stdout.
func EmitResult(streams Streams, command string, jsonOutput bool, data interface{}, err *Error) int {
	return EmitResultAs(DefaultProgram, streams, command, jsonOutput, data, err)
}

// EmitResultAs is EmitResult for a tree whose executable is not the default `asao`. The governed
// remote client never needs it (it is asao by construction); it exists so no future caller has to
// reach for a copy of emit to render under a different program name.
func EmitResultAs(program string, streams Streams, command string, jsonOutput bool, data interface{}, err *Error) int {
	return emit(streams, program, command, jsonOutput, data, err)
}

// wantsJSON detects the requested output format from raw arguments, for the case where parsing
// failed before an Invocation existed. Without it, a malformed invocation would answer a caller
// that asked for JSON with human text — the exact inconsistency this CLI is meant to end.
func wantsJSON(args []string) bool {
	for index, arg := range args {
		if arg == "--output=json" {
			return true
		}
		if arg == "--output" && index+1 < len(args) && args[index+1] == "json" {
			return true
		}
	}
	return false
}

// Diagnose writes a diagnostic line to stderr. Commands use it for progress and warnings; it can
// never corrupt the stdout contract because it never writes to Out.
func Diagnose(streams Streams, format string, args ...interface{}) {
	fmt.Fprintf(streams.Diagnostics, DefaultProgram+": "+format+"\n", args...)
}
