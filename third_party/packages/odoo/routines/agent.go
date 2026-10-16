package routines

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Agents are the agents a routine can name. The prompt goes on standard input unless the command
// line holds {prompt}.
var Agents = map[string][]string{
	"claude": {"claude", "-p", "--output-format", "text"},
	"codex":  {"codex", "exec", "--skip-git-repo-check", "--sandbox", "read-only", "--output-last-message", "{answer_file}", "-"},
	"gemini": {"gemini", "--skip-trust", "-p", "Follow the instructions given on standard input."},
	"agy":    {"agy", "-p", "{prompt}"},
}

// An argument holds at most 128 KiB on Linux: a prompt given as one is cut well below that.
const (
	argumentPromptLimit = 100_000
	stdinPromptLimit    = 400_000
)

// AgentResult is how the agent's reading of a run went.
type AgentResult struct {
	Status   string  `json:"status"`
	Command  string  `json:"command,omitempty"`
	ExitCode *int    `json:"exitCode,omitempty"`
	Seconds  float64 `json:"seconds,omitempty"`
}

// AgentRunner starts an agent: the command line, what goes on its standard input ("" for none), the
// folder it runs in and how long it may take. It returns standard output, standard error and the
// exit code; an error means it could not run or ran out of time (context.DeadlineExceeded).
type AgentRunner func(ctx context.Context, command []string, stdin, folder string, timeout time.Duration) (string, string, int, error)

// runProcess is the AgentRunner that starts the agent as a process.
func runProcess(ctx context.Context, command []string, stdin, folder string, timeout time.Duration) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	process := exec.CommandContext(ctx, command[0], command[1:]...)
	process.Dir = folder
	if stdin != "" {
		process.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	// An agent's own children may hold its pipes open after it is stopped.
	process.WaitDelay = 5 * time.Second
	err := process.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), -1, context.DeadlineExceeded
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return stdout.String(), stderr.String(), exited.ExitCode(), nil
	}
	if err != nil {
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

// agentCommand is the command line an agent value names; nil for none.
func agentCommand(agent interface{}) []string {
	switch typed := agent.(type) {
	case nil:
		return nil
	case string:
		if typed == "" || typed == "none" {
			return nil
		}
		if known, ok := Agents[typed]; ok {
			return append([]string(nil), known...)
		}
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	}
	return nil
}

// analyse asks the chosen agent to read the run. Its answer is kept in analysis.md; it is given no
// tools beyond what its own command line gives it, and it runs in the run's folder.
func (f *File) analyse(ctx context.Context, agent interface{}, prompt string, result *Result, folder string, runner AgentRunner) *AgentResult {
	command := agentCommand(agent)
	if command == nil {
		return &AgentResult{Status: "SKIPPED"}
	}
	if runner == nil {
		if _, err := exec.LookPath(command[0]); err != nil {
			return &AgentResult{Status: "AGENT_NOT_FOUND", Command: command[0]}
		}
		runner = runProcess
	}
	asArgument := false
	for _, part := range command {
		if strings.Contains(part, "{prompt}") {
			asArgument = true
		}
	}
	limit := stdinPromptLimit
	if asArgument {
		limit = argumentPromptLimit
	}
	full := prompt + fit(result, limit-len(prompt), f.Language != "en")
	if err := writeText(filepath.Join(folder, "prompt.md"), full); err != nil {
		return &AgentResult{Status: "AGENT_FAILED", Command: command[0]}
	}
	answerFile := filepath.Join(folder, "agent-answer.txt")
	_ = os.Remove(answerFile)
	args := make([]string, len(command))
	for index, part := range command {
		args[index] = strings.NewReplacer("{prompt}", full, "{answer_file}", answerFile, "{folder}", folder).Replace(part)
	}
	stdin := full
	if asArgument {
		stdin = ""
	}
	started := time.Now()
	stdout, stderr, exitCode, err := runner(ctx, args, stdin, folder, f.AgentTimeout)
	seconds := math.Round(time.Since(started).Seconds()*10) / 10
	if errors.Is(err, context.DeadlineExceeded) {
		return &AgentResult{Status: "AGENT_TIMEOUT", Command: command[0], Seconds: f.AgentTimeout.Seconds()}
	}
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return &AgentResult{Status: "AGENT_NOT_FOUND", Command: command[0]}
		}
		return &AgentResult{Status: "AGENT_FAILED", Command: command[0], Seconds: seconds}
	}
	answer := stdout
	if saved, readErr := os.ReadFile(answerFile); readErr == nil {
		answer = string(saved)
	}
	_ = os.Remove(answerFile)
	if len(stderr) > 20000 {
		stderr = stderr[len(stderr)-20000:]
	}
	_ = writeText(filepath.Join(folder, "agent.log"), stderr)
	if exitCode != 0 || strings.TrimSpace(answer) == "" {
		code := exitCode
		return &AgentResult{Status: "AGENT_FAILED", Command: command[0], ExitCode: &code, Seconds: seconds}
	}
	if err := writeText(filepath.Join(folder, "analysis.md"), strings.TrimSpace(answer)+"\n"); err != nil {
		return &AgentResult{Status: "AGENT_FAILED", Command: command[0], Seconds: seconds}
	}
	return &AgentResult{Status: "OK", Command: command[0], Seconds: seconds}
}
