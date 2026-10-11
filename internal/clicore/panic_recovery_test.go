package clicore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

func TestPanicRecovery_JSONMode(t *testing.T) {
	root := &clicore.Command{
		Name:    "boom",
		Program: "testapp",
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			panic("something completely unexpected broke")
		},
	}

	var out bytes.Buffer
	var diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}

	exitCode := clicore.Run(context.Background(), root, []string{"--output", "json"}, streams)
	if exitCode != clicore.ExitInternal {
		t.Fatalf("expected ExitInternal (1), got %d", exitCode)
	}

	// Stderr has diagnostics
	diagStr := diag.String()
	if !strings.Contains(diagStr, "unexpected panic: something completely unexpected broke") {
		t.Fatalf("expected diagnostic on stderr, got: %q", diagStr)
	}
	// No normal stack trace
	if strings.Contains(diagStr, "goroutine ") || strings.Contains(diagStr, ".go:") {
		t.Fatalf("normal stack trace found in stderr: %q", diagStr)
	}

	// Stdout has exactly one valid JSON error envelope
	var envelope clicore.Envelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout did not contain valid JSON envelope: %v\nOutput: %s", err, out.String())
	}
	if envelope.Kind != clicore.KindError {
		t.Fatalf("expected kind CommandError, got: %s", envelope.Kind)
	}
	if envelope.Status != "ERROR" {
		t.Fatalf("expected status ERROR, got: %s", envelope.Status)
	}
	if envelope.Error == nil {
		t.Fatal("expected error field in envelope")
	}
	if envelope.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("expected code INTERNAL_ERROR, got: %s", envelope.Error.Code)
	}
	if envelope.Error.Category != clicore.CategoryInternal {
		t.Fatalf("expected category INTERNAL, got: %s", envelope.Error.Category)
	}
	if envelope.Error.ExitCode != clicore.ExitInternal {
		t.Fatalf("expected exitCode 1, got: %d", envelope.Error.ExitCode)
	}
	if !strings.Contains(envelope.Error.Message, "something completely unexpected broke") {
		t.Fatalf("expected panic message in error message, got: %s", envelope.Error.Message)
	}

	// Exactly one JSON document (decoder EOF check)
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var first, second interface{}
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("failed decoding first doc: %v", err)
	}
	if err := dec.Decode(&second); err == nil {
		t.Fatalf("expected exactly one JSON document, but found a second document in output: %v", second)
	}
}

func TestPanicRecovery_TextMode(t *testing.T) {
	root := &clicore.Command{
		Name:    "boom",
		Program: "testapp",
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			panic("text panic fault")
		},
	}

	var out bytes.Buffer
	var diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}

	exitCode := clicore.Run(context.Background(), root, []string{}, streams)
	if exitCode != clicore.ExitInternal {
		t.Fatalf("expected ExitInternal (1), got %d", exitCode)
	}

	if out.Len() != 0 {
		t.Fatalf("expected nothing on stdout in text error mode, got: %s", out.String())
	}

	diagStr := diag.String()
	if !strings.Contains(diagStr, "unexpected panic: text panic fault") {
		t.Fatalf("expected diagnostic on stderr, got: %q", diagStr)
	}
	if !strings.Contains(diagStr, "error: unexpected panic: text panic fault") {
		t.Fatalf("expected rendered text error on stderr, got: %q", diagStr)
	}
	if strings.Contains(diagStr, "goroutine ") {
		t.Fatalf("stack trace leaked to stderr: %q", diagStr)
	}
}

func TestPanicRecovery_NoSecondEnvelopeAfterPriorOutput(t *testing.T) {
	var streams clicore.Streams
	root := &clicore.Command{
		Name:    "partial",
		Program: "testapp",
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			// Write some prior output directly before panicking
			streams.Out.Write([]byte(`{"prior":"output"}` + "\n"))
			panic("panic after prior output")
		},
	}

	var out bytes.Buffer
	var diag bytes.Buffer
	streams = clicore.Streams{Out: &out, Diagnostics: &diag}

	exitCode := clicore.Run(context.Background(), root, []string{"--output", "json"}, streams)
	if exitCode != clicore.ExitInternal {
		t.Fatalf("expected ExitInternal (1), got %d", exitCode)
	}

	// Should not have emitted a second JSON error envelope
	if strings.Contains(out.String(), "CommandError") {
		t.Fatalf("expected no second envelope after prior output, got: %s", out.String())
	}
	if !strings.Contains(diag.String(), "unexpected panic: panic after prior output") {
		t.Fatalf("expected panic in diagnostics, got: %s", diag.String())
	}
}
