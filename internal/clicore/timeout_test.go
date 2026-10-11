package clicore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func TestTimeout_InvalidZeroNegativeRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"invalid string", []string{"--timeout", "not-a-duration", "--output", "json"}},
		{"zero duration", []string{"--timeout", "0s", "--output", "json"}},
		{"zero duration unitless", []string{"--timeout", "0", "--output", "json"}},
		{"negative duration", []string{"--timeout", "-5s", "--output", "json"}},
		{"negative duration ms", []string{"--timeout", "-100ms", "--output", "json"}},
		{"explicit empty value", []string{"--timeout", "", "--output", "json"}},
		{"explicit empty inline", []string{"--timeout=", "--output", "json"}},
		{"explicit whitespace value", []string{"--timeout", "   ", "--output", "json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := &clicore.Command{
				Name:    "dummy",
				Program: "testapp",
				Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
					return "ok", nil
				},
			}

			var out bytes.Buffer
			var diag bytes.Buffer
			streams := clicore.Streams{Out: &out, Diagnostics: &diag}

			exitCode := clicore.Run(context.Background(), root, tc.args, streams)
			if exitCode != clicore.ExitUsage {
				t.Fatalf("expected ExitUsage (2), got %d (output: %s)", exitCode, out.String())
			}

			var env clicore.Envelope
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatalf("failed to decode JSON envelope: %v", err)
			}
			if env.Kind != clicore.KindError || env.Status != "ERROR" {
				t.Fatalf("expected CommandError/ERROR, got %s/%s", env.Kind, env.Status)
			}
			if env.Error == nil || env.Error.Code != "INVALID_FLAG_VALUE" {
				t.Fatalf("expected INVALID_FLAG_VALUE error code, got: %+v", env.Error)
			}
			if env.Error.Category != clicore.CategoryUsage {
				t.Fatalf("expected category USAGE, got %s", env.Error.Category)
			}
		})
	}
}

func TestTimeout_InvocationDurationEmptyRejected(t *testing.T) {
	root := &clicore.Command{
		Name:    "dummy",
		Program: "testapp",
		Flags: []clicore.Flag{
			{Name: "timeout", Kind: clicore.KindString},
		},
	}

	// Case 1: Explicitly supplied empty value via --timeout=
	inv1, err := clicore.Parse(root, []string{"--timeout="})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	_, dErr := inv1.Duration("timeout", 10*time.Second)
	if dErr == nil {
		t.Fatal("expected error for explicitly supplied empty timeout, got nil")
	}
	cliErr, ok := dErr.(*clicore.Error)
	if !ok || cliErr.Code != "INVALID_FLAG_VALUE" {
		t.Fatalf("expected INVALID_FLAG_VALUE error, got: %v", dErr)
	}

	// Case 2: Flag not supplied -> returns fallback
	inv2, err := clicore.Parse(root, []string{})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	d2, dErr2 := inv2.Duration("timeout", 10*time.Second)
	if dErr2 != nil {
		t.Fatalf("unexpected error: %v", dErr2)
	}
	if d2 != 10*time.Second {
		t.Fatalf("expected fallback 10s, got: %v", d2)
	}
}

func TestTimeout_HelpDiscoveryShortCircuit(t *testing.T) {
	cases := []struct {
		name string
		args []string
		json bool
	}{
		{"help with empty inline timeout", []string{"--help", "--timeout="}, false},
		{"empty inline timeout before help", []string{"--timeout=", "--help"}, false},
		{"help with empty space timeout", []string{"--timeout", "", "--help"}, false},
		{"help with invalid string timeout", []string{"--timeout=not-a-duration", "--help"}, false},
		{"help with zero timeout", []string{"--timeout=0s", "--help"}, false},
		{"help with negative timeout", []string{"--timeout=-5s", "--help"}, false},
		{"json help with empty timeout", []string{"--help", "--timeout=", "--output", "json"}, true},
		{"json help with garbage timeout", []string{"--timeout=garbage", "--help", "--output", "json"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := &clicore.Command{
				Name:    "mycmd",
				Program: "testapp",
				Summary: "My test command",
				Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
					return "ok", nil
				},
			}

			var out bytes.Buffer
			var diag bytes.Buffer
			streams := clicore.Streams{Out: &out, Diagnostics: &diag}

			exitCode := clicore.Run(context.Background(), root, tc.args, streams)
			if exitCode != clicore.ExitOK {
				t.Fatalf("expected ExitOK (0) for --help discovery short-circuit, got %d (diag: %s)", exitCode, diag.String())
			}

			if tc.json {
				var env clicore.Envelope
				if err := json.Unmarshal(out.Bytes(), &env); err != nil {
					t.Fatalf("failed decoding json spec: %v\nOutput: %s", err, out.String())
				}
				if env.Kind != clicore.KindResult || env.Status != "OK" {
					t.Fatalf("expected CommandResult/OK, got: %s/%s", env.Kind, env.Status)
				}
			} else {
				helpText := out.String()
				if !bytes.Contains(out.Bytes(), []byte("My test command")) {
					t.Fatalf("expected help text to contain summary, got: %s", helpText)
				}
			}
		})
	}
}

func TestTimeout_HonorsSmallerTimeout(t *testing.T) {
	executed := false
	root := &clicore.Command{
		Name:    "slow",
		Program: "testapp",
		Run: func(ctx context.Context, _ *clicore.Invocation) (interface{}, error) {
			executed = true
			select {
			case <-time.After(200 * time.Millisecond):
				return "finished", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	var out bytes.Buffer
	var diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}

	exitCode := clicore.Run(context.Background(), root, []string{"--timeout", "20ms", "--output", "json"}, streams)
	if exitCode != clicore.ExitUnavailable {
		t.Fatalf("expected ExitUnavailable (8), got %d (output: %s)", exitCode, out.String())
	}
	if !executed {
		t.Fatal("expected command to have been executed")
	}

	var env clicore.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("failed decoding json: %v", err)
	}
	if env.Error == nil || env.Error.Code != "TIMEOUT" {
		t.Fatalf("expected code TIMEOUT, got %+v", env.Error)
	}
	if env.Error.Category != clicore.CategoryUnavailable {
		t.Fatalf("expected CategoryUnavailable, got %s", env.Error.Category)
	}
	if !env.Error.Retryable {
		t.Fatal("expected timeout error to be retryable")
	}
}

func TestTimeout_ContextCancellationMappedConsistently(t *testing.T) {
	root := &clicore.Command{
		Name:    "cancelme",
		Program: "testapp",
		Run: func(ctx context.Context, _ *clicore.Invocation) (interface{}, error) {
			select {
			case <-time.After(200 * time.Millisecond):
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	var out bytes.Buffer
	var diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}

	exitCode := clicore.Run(ctx, root, []string{"--output", "json"}, streams)
	if exitCode != clicore.ExitUnavailable {
		t.Fatalf("expected ExitUnavailable (8), got %d", exitCode)
	}

	var env clicore.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("failed decoding json: %v", err)
	}
	if env.Error == nil || env.Error.Code != "CANCELED" {
		t.Fatalf("expected code CANCELED, got %+v", env.Error)
	}
	if env.Error.Category != clicore.CategoryUnavailable {
		t.Fatalf("expected CategoryUnavailable, got %s", env.Error.Category)
	}
}

func TestTimeout_Cap110Seconds(t *testing.T) {
	var deadlineRemaining time.Duration
	root := &clicore.Command{
		Name:    "checkdeadline",
		Program: "testapp",
		Run: func(ctx context.Context, _ *clicore.Invocation) (interface{}, error) {
			if dl, ok := ctx.Deadline(); ok {
				deadlineRemaining = time.Until(dl)
			}
			return "ok", nil
		},
	}

	var out bytes.Buffer
	var diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}

	// Supply timeout of 200s (greater than 110s cap)
	exitCode := clicore.Run(context.Background(), root, []string{"--timeout", "200s", "--output", "json"}, streams)
	if exitCode != clicore.ExitOK {
		t.Fatalf("expected ExitOK, got %d", exitCode)
	}
	// Deadline should be capped at <= 110s
	if deadlineRemaining <= 0 || deadlineRemaining > 111*time.Second {
		t.Fatalf("expected deadline <= 110s, got remaining: %v", deadlineRemaining)
	}
}
