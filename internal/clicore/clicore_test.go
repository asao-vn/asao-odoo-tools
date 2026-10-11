// Package clicore_test exercises the framework itself against small, synthetic command trees, so a
// regression in dispatch, parsing or rendering is caught without needing the real (database-backed)
// command tree in internal/commands.
package clicore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// buildTestTree returns a fresh synthetic command tree. Fresh per call because clicore.Parse links
// (sorts, wires parents) the tree it is given, and sharing one tree across parallel subtests would
// be a data race waiting to happen.
func buildTestTree() *clicore.Command {
	beta := &clicore.Command{
		Name: "beta", Summary: "beta leaf", SideEffect: clicore.EffectNone,
		Run: func(context.Context, *clicore.Invocation) (interface{}, error) { return "beta-ok", nil },
	}
	gamma := &clicore.Command{
		Name: "gamma", Summary: "gamma leaf", SideEffect: clicore.EffectNone,
		Run: func(context.Context, *clicore.Invocation) (interface{}, error) { return "gamma-ok", nil },
	}
	alpha := &clicore.Command{
		Name:     "alpha",
		Summary:  "alpha group",
		Children: []*clicore.Command{beta, gamma},
	}
	leaf := &clicore.Command{
		Name: "leaf", Summary: "leaf test bed", SideEffect: clicore.EffectNone,
		Flags: []clicore.Flag{
			{Name: "name", Kind: clicore.KindString, Required: true, Description: "the name"},
			{Name: "count", Kind: clicore.KindInt, Description: "a count"},
			{Name: "verbose", Kind: clicore.KindBool, Description: "verbosity"},
			{Name: "mode", Kind: clicore.KindString, Enum: []string{"fast", "slow"}, Description: "execution mode"},
			{Name: "meta", Kind: clicore.KindKeyValue, Description: "arbitrary metadata, repeated name=value"},
			{Name: "payload", Kind: clicore.KindJSON, Description: "a JSON payload"},
		},
		Run: func(_ context.Context, inv *clicore.Invocation) (interface{}, error) {
			name, err := inv.RequiredString("name")
			if err != nil {
				return nil, err
			}
			meta, err := inv.KeyValues("meta", nil)
			if err != nil {
				return nil, err
			}
			var payload map[string]interface{}
			if err := inv.JSON("payload", &payload); err != nil {
				return nil, err
			}
			verbose, err := inv.Bool("verbose")
			if err != nil {
				return nil, err
			}
			count, err := inv.Int("count", 0)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"name": name, "meta": meta, "payload": payload,
				"verbose": verbose, "count": count, "mode": inv.String("mode"),
			}, nil
		},
	}
	fails := &clicore.Command{
		Name: "fails", Summary: "always fails, for testing failure rendering", SideEffect: clicore.EffectNone,
		Run: func(context.Context, *clicore.Invocation) (interface{}, error) {
			return nil, clicore.NewError(clicore.CategoryNotFound, "SYNTHETIC_NOT_FOUND", "synthetic record not found")
		},
	}
	return &clicore.Command{
		Summary:  "synthetic root",
		Children: []*clicore.Command{alpha, leaf, fails},
	}
}

// run drives clicore.Run against freshly allocated stream buffers.
func run(t *testing.T, root *clicore.Command, args []string) (code int, out, diag *bytes.Buffer) {
	t.Helper()
	out, diag = &bytes.Buffer{}, &bytes.Buffer{}
	code = clicore.Run(context.Background(), root, args, clicore.Streams{Out: out, Diagnostics: diag})
	return code, out, diag
}

// decodeEnvelope decodes exactly one JSON document from buf and fails the test if anything follows
// it — the whole point of the JSON contract is "one document, nothing else", so a passing decode of
// the first document is not enough; the stream must also be exhausted.
func decodeEnvelope(t *testing.T, buf *bytes.Buffer) clicore.Envelope {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var envelope clicore.Envelope
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("stdout did not decode as one JSON envelope: %v; raw=%q", err, buf.String())
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		t.Fatalf("stdout carried more than one JSON document (decoder not at EOF): err=%v raw=%q", err, buf.String())
	}
	return envelope
}

func toStringSlice(t *testing.T, value interface{}) []string {
	t.Helper()
	raw, ok := value.([]interface{})
	if !ok {
		t.Fatalf("expected a JSON array, got %#v", value)
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("element %d is not a string: %#v", i, v)
		}
		out[i] = s
	}
	return out
}

func containsStr(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestUnknownCommand pins that an unknown subcommand is exit 2, code UNKNOWN_COMMAND, and — the
// part that matters for a caller trying to self-correct — names exactly the real siblings, not a
// generic "not found".
func TestUnknownCommand(t *testing.T) {
	root := buildTestTree()
	code, out, _ := run(t, root, []string{"alpha", "delta", "--output", "json"})
	if code != clicore.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
	}
	envelope := decodeEnvelope(t, out)
	if envelope.Status != "ERROR" || envelope.Error == nil {
		t.Fatalf("expected an error envelope, got %+v", envelope)
	}
	if envelope.Error.Code != "UNKNOWN_COMMAND" {
		t.Fatalf("error code = %q, want UNKNOWN_COMMAND", envelope.Error.Code)
	}
	available := toStringSlice(t, envelope.Error.Context["availableCommands"])
	if !equalStrings(available, []string{"beta", "gamma"}) {
		t.Fatalf("context.availableCommands = %v, want exactly [beta gamma]", available)
	}
}

// TestUnknownFlag pins that rejecting an unknown flag publishes EVERY accepted name — the command's
// own flags AND the global ones. Omitting the globals here is the historical defect this catches:
// an agent that only sees the local flags can never discover --output or --help from the error.
func TestUnknownFlag(t *testing.T) {
	root := buildTestTree()
	code, out, _ := run(t, root, []string{"leaf", "--nope", "x", "--output", "json"})
	if code != clicore.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
	}
	envelope := decodeEnvelope(t, out)
	if envelope.Error.Code != "UNKNOWN_FLAG" {
		t.Fatalf("error code = %q, want UNKNOWN_FLAG", envelope.Error.Code)
	}
	accepted := toStringSlice(t, envelope.Error.Context["acceptedFlags"])
	for _, want := range []string{"name", "count", "verbose", "mode", "meta", "payload", "output", "help"} {
		if !containsStr(accepted, want) {
			t.Fatalf("acceptedFlags = %v, missing %q", accepted, want)
		}
	}
}

// TestNearMissRemediation pins that a small typo — in either a flag or a command name — produces a
// "Did you mean" remediation, which is what lets a caller correct itself in one round trip instead
// of re-reading the whole discovery document.
func TestNearMissRemediation(t *testing.T) {
	t.Run("flag", func(t *testing.T) {
		root := buildTestTree()
		_, out, _ := run(t, root, []string{"leaf", "--verbos", "--output", "json"})
		envelope := decodeEnvelope(t, out)
		if envelope.Error == nil || !strings.Contains(envelope.Error.Remediation, "Did you mean") {
			t.Fatalf("remediation = %q, want a Did-you-mean suggestion", envelope.Error.Remediation)
		}
	})
	t.Run("command", func(t *testing.T) {
		root := buildTestTree()
		_, out, _ := run(t, root, []string{"alpha", "bet", "--output", "json"})
		envelope := decodeEnvelope(t, out)
		if envelope.Error == nil || !strings.Contains(envelope.Error.Remediation, "Did you mean") {
			t.Fatalf("remediation = %q, want a Did-you-mean suggestion", envelope.Error.Remediation)
		}
	})
}

// TestMissingRequiredFlag pins the exit code and machine code for the single most common refusal:
// leaving out a flag the command declared Required.
func TestMissingRequiredFlag(t *testing.T) {
	root := buildTestTree()
	code, out, _ := run(t, root, []string{"leaf", "--output", "json"})
	if code != clicore.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
	}
	envelope := decodeEnvelope(t, out)
	if envelope.Error.Code != "MISSING_REQUIRED_FLAG" {
		t.Fatalf("error code = %q, want MISSING_REQUIRED_FLAG", envelope.Error.Code)
	}
}

// TestEnumViolation pins that a value outside a flag's declared enum is refused with the CLOSED set
// of accepted values attached — so a caller never has to guess a legal value by trial and error.
func TestEnumViolation(t *testing.T) {
	root := buildTestTree()
	code, out, _ := run(t, root, []string{"leaf", "--name", "x", "--mode", "warp", "--output", "json"})
	if code != clicore.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
	}
	envelope := decodeEnvelope(t, out)
	if envelope.Error.Code != "INVALID_FLAG_VALUE" {
		t.Fatalf("error code = %q, want INVALID_FLAG_VALUE", envelope.Error.Code)
	}
	accepted := toStringSlice(t, envelope.Error.Context["acceptedValues"])
	if !equalStrings(accepted, []string{"fast", "slow"}) {
		t.Fatalf("acceptedValues = %v, want exactly the declared enum [fast slow]", accepted)
	}
}

// TestFlagFormEquivalence pins that `--flag=value` and `--flag value` are two spellings of the same
// input, not two different behaviours — a caller should never have to learn which form a particular
// flag requires.
func TestFlagFormEquivalence(t *testing.T) {
	_, out1, _ := run(t, buildTestTree(), []string{"leaf", "--name=Foo", "--output", "json"})
	_, out2, _ := run(t, buildTestTree(), []string{"leaf", "--name", "Foo", "--output", "json"})
	e1, e2 := decodeEnvelope(t, out1), decodeEnvelope(t, out2)
	d1, err := json.Marshal(e1.Data)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := json.Marshal(e2.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(d1) != string(d2) {
		t.Fatalf("--name=Foo and --name Foo produced different results:\n  %s\n  %s", d1, d2)
	}
}

// TestBoolFlagForms pins the two accepted spellings of a boolean flag: bare (true) and explicit
// --flag=false. A regression here would silently flip a caller's intent.
func TestBoolFlagForms(t *testing.T) {
	_, out, _ := run(t, buildTestTree(), []string{"leaf", "--name", "x", "--verbose", "--output", "json"})
	data := decodeEnvelope(t, out).Data.(map[string]interface{})
	if data["verbose"] != true {
		t.Fatalf("bare --verbose = %v, want true", data["verbose"])
	}

	_, out2, _ := run(t, buildTestTree(), []string{"leaf", "--name", "x", "--verbose=false", "--output", "json"})
	data2 := decodeEnvelope(t, out2).Data.(map[string]interface{})
	if data2["verbose"] != false {
		t.Fatalf("--verbose=false = %v, want false", data2["verbose"])
	}
}

// TestRepeatedNonRepeatableFlag pins that giving a non-repeatable flag twice is refused rather than
// silently keeping the last value — a repeated flag is almost always a caller mistake, and silently
// "winning" on the last one turns that mistake into an invisible behaviour change.
func TestRepeatedNonRepeatableFlag(t *testing.T) {
	root := buildTestTree()
	code, out, _ := run(t, root, []string{"leaf", "--name", "x", "--mode", "fast", "--mode", "slow", "--output", "json"})
	if code != clicore.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
	}
	envelope := decodeEnvelope(t, out)
	if envelope.Error.Code != "REPEATED_FLAG" {
		t.Fatalf("error code = %q, want REPEATED_FLAG", envelope.Error.Code)
	}
}

// TestKeyValueFlag pins that a repeatable keyvalue flag collects every occurrence into one map, and
// that an occurrence with no "=" is refused rather than silently dropped or mis-split.
func TestKeyValueFlag(t *testing.T) {
	t.Run("collects every occurrence", func(t *testing.T) {
		root := buildTestTree()
		_, out, _ := run(t, root, []string{"leaf", "--name", "x", "--meta", "a=1", "--meta", "b=2", "--output", "json"})
		data := decodeEnvelope(t, out).Data.(map[string]interface{})
		meta, ok := data["meta"].(map[string]interface{})
		if !ok {
			t.Fatalf("meta = %#v, want a map", data["meta"])
		}
		if meta["a"] != "1" || meta["b"] != "2" || len(meta) != 2 {
			t.Fatalf("meta = %v, want {a:1 b:2}", meta)
		}
	})
	t.Run("missing equals is refused", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"leaf", "--name", "x", "--meta", "noequals", "--output", "json"})
		if code != clicore.ExitUsage {
			t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
		}
		envelope := decodeEnvelope(t, out)
		if envelope.Error.Code != "INVALID_FLAG_VALUE" {
			t.Fatalf("error code = %q, want INVALID_FLAG_VALUE", envelope.Error.Code)
		}
	})
}

// TestDoubleEncodedJSONRefusal is the headline fix under test: a caller that ran its JSON payload
// through one encoding step too many (producing a JSON STRING that itself contains a JSON document)
// must be refused by name, not silently forwarded as a string. Sibling cases pin that a properly
// formed document still works and that genuinely malformed JSON gets a different, distinct code.
func TestDoubleEncodedJSONRefusal(t *testing.T) {
	// doubleEncoded is the literal bytes `"{\"a\":1}"` — a JSON string whose value is itself the
	// JSON text `{"a":1}`. This is exactly what `--values "$(jq -c ...)"` produces when the shell
	// quoting is wrong, which is the historical defect this refusal exists to name.
	doubleEncoded := `"{\"a\":1}"`

	t.Run("double-encoded string is refused", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"leaf", "--name", "x", "--payload", doubleEncoded, "--output", "json"})
		if code != clicore.ExitUsage {
			t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
		}
		envelope := decodeEnvelope(t, out)
		if envelope.Error.Code != "DOUBLE_ENCODED_JSON" {
			t.Fatalf("error code = %q, want DOUBLE_ENCODED_JSON (raw arg was %q)", envelope.Error.Code, doubleEncoded)
		}
	})

	t.Run("well-formed JSON succeeds", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"leaf", "--name", "x", "--payload", `{"a":1}`, "--output", "json"})
		if code != clicore.ExitOK {
			t.Fatalf("exit code = %d, want 0; out=%s", code, out.String())
		}
		data := decodeEnvelope(t, out).Data.(map[string]interface{})
		payload, ok := data["payload"].(map[string]interface{})
		if !ok || payload["a"] != float64(1) {
			t.Fatalf("payload = %#v, want {a:1}", data["payload"])
		}
	})

	t.Run("invalid JSON reports INVALID_JSON, not DOUBLE_ENCODED_JSON", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"leaf", "--name", "x", "--payload", `{not json`, "--output", "json"})
		if code != clicore.ExitUsage {
			t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
		}
		envelope := decodeEnvelope(t, out)
		if envelope.Error.Code != "INVALID_JSON" {
			t.Fatalf("error code = %q, want INVALID_JSON", envelope.Error.Code)
		}
	})
}

// TestUnexpectedPositionalArguments pins that this CLI, which takes no positional arguments at all,
// refuses a stray token after a leaf command rather than silently ignoring it.
func TestUnexpectedPositionalArguments(t *testing.T) {
	root := buildTestTree()
	code, out, _ := run(t, root, []string{"leaf", "--name", "x", "extra", "--output", "json"})
	if code != clicore.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
	}
	envelope := decodeEnvelope(t, out)
	if envelope.Error.Code != "UNEXPECTED_ARGUMENTS" {
		t.Fatalf("error code = %q, want UNEXPECTED_ARGUMENTS", envelope.Error.Code)
	}
}

// TestJSONContractOnSuccess pins the full machine contract on a successful call: exactly one JSON
// document on stdout, the right envelope fields, and — the part most likely to regress silently — an
// EMPTY diagnostics stream, so a caller never has to wonder whether stderr carried something it
// needed.
func TestJSONContractOnSuccess(t *testing.T) {
	root := buildTestTree()
	code, out, diag := run(t, root, []string{"leaf", "--name", "Foo", "--output", "json"})
	if code != clicore.ExitOK {
		t.Fatalf("exit code = %d, want 0; out=%s diag=%s", code, out, diag)
	}
	if diag.Len() != 0 {
		t.Fatalf("diagnostics not empty on success: %q", diag.String())
	}
	envelope := decodeEnvelope(t, out)
	if envelope.APIVersion != clicore.APIVersion {
		t.Fatalf("apiVersion = %q, want %q", envelope.APIVersion, clicore.APIVersion)
	}
	if envelope.Kind != clicore.KindResult {
		t.Fatalf("kind = %q, want %q", envelope.Kind, clicore.KindResult)
	}
	if envelope.Status != "OK" {
		t.Fatalf("status = %q, want OK", envelope.Status)
	}
}

// TestJSONContractOnFailure pins that a failing command STILL writes exactly one JSON document to
// stdout (never partial output, never plain text mixed in), and that the process exit code always
// equals the envelope's own error.exitCode — the two must never be able to disagree.
func TestJSONContractOnFailure(t *testing.T) {
	root := buildTestTree()
	code, out, _ := run(t, root, []string{"fails", "--output", "json"})
	envelope := decodeEnvelope(t, out)
	if envelope.Kind != clicore.KindError {
		t.Fatalf("kind = %q, want %q", envelope.Kind, clicore.KindError)
	}
	if envelope.Status != "ERROR" {
		t.Fatalf("status = %q, want ERROR", envelope.Status)
	}
	if envelope.Error == nil {
		t.Fatalf("error field is nil on a CommandError envelope")
	}
	if code != envelope.Error.ExitCode {
		t.Fatalf("process exit code %d != envelope error.exitCode %d", code, envelope.Error.ExitCode)
	}
}

// TestDiagnosticsNeverCorruptStdout builds a command whose Run writes a diagnostic line before
// returning its result, and pins that stdout still parses as exactly one JSON document. A command
// that accidentally wrote its diagnostic to Out (instead of Diagnostics) would corrupt the stream
// every JSON caller depends on being a single document.
func TestDiagnosticsNeverCorruptStdout(t *testing.T) {
	var out, diag bytes.Buffer
	streams := clicore.Streams{Out: &out, Diagnostics: &diag}
	leaf := &clicore.Command{
		Name: "diag", Summary: "writes a diagnostic before returning", SideEffect: clicore.EffectNone,
		Run: func(context.Context, *clicore.Invocation) (interface{}, error) {
			clicore.Diagnose(streams, "progress: halfway there")
			return map[string]interface{}{"ok": true}, nil
		},
	}
	root := &clicore.Command{Summary: "root", Children: []*clicore.Command{leaf}}

	code := clicore.Run(context.Background(), root, []string{"diag", "--output", "json"}, streams)
	if code != clicore.ExitOK {
		t.Fatalf("exit code = %d, want 0; out=%s diag=%s", code, out.String(), diag.String())
	}
	if diag.Len() == 0 {
		t.Fatalf("expected the command's own diagnostic line to have been written to Diagnostics")
	}
	envelope := decodeEnvelope(t, &out)
	if envelope.Status != "OK" {
		t.Fatalf("status = %q, want OK", envelope.Status)
	}
}

// TestTextModeFailureWritesToDiagnostics pins that, in text (non-JSON) mode, a failure is rendered
// to Diagnostics and Out is left untouched — the text-mode twin of the JSON stdout/stderr split.
func TestTextModeFailureWritesToDiagnostics(t *testing.T) {
	root := buildTestTree()
	code, out, diag := run(t, root, []string{"fails"})
	if code == clicore.ExitOK {
		t.Fatalf("exit code = 0, want a failure code")
	}
	if out.Len() != 0 {
		t.Fatalf("text-mode failure wrote to stdout: %q", out.String())
	}
	if diag.Len() == 0 {
		t.Fatalf("text-mode failure produced no diagnostics")
	}
	if !strings.Contains(diag.String(), "SYNTHETIC_NOT_FOUND") {
		t.Fatalf("diagnostics missing the error code: %q", diag.String())
	}
}

// TestHelpOnLeaf pins both halves of `--help`: text mode renders human help and exits 0, and
// `--help --output json` emits the command's own CommandSpec as data — the machine-readable twin the
// discovery contract promises never to disagree with the text.
func TestHelpOnLeaf(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"leaf", "--help"})
		if code != clicore.ExitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if !strings.Contains(out.String(), "leaf") {
			t.Fatalf("help text missing the command name: %q", out.String())
		}
	})
	t.Run("json", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"leaf", "--help", "--output", "json"})
		if code != clicore.ExitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		envelope := decodeEnvelope(t, out)
		if envelope.Kind != clicore.KindResult {
			t.Fatalf("kind = %q, want %q", envelope.Kind, clicore.KindResult)
		}
		raw, err := json.Marshal(envelope.Data)
		if err != nil {
			t.Fatal(err)
		}
		var spec clicore.CommandSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatalf("data is not a CommandSpec: %v (%s)", err, raw)
		}
		if spec.Command != "asao leaf" {
			t.Fatalf("spec.Command = %q, want %q", spec.Command, "asao leaf")
		}
		if len(spec.Path) == 0 {
			t.Fatalf("spec.Path is empty")
		}
		if len(spec.Flags) == 0 {
			t.Fatalf("spec.Flags is empty, want the leaf's own flags plus globals")
		}
	})
}

// TestGroupWithoutSubcommand pins that running a group with no further arguments never behaves like
// success: text mode exits non-zero and shows help, JSON mode reports COMMAND_IS_GROUP with the list
// of what IS runnable — a scripted caller must never be able to read a group's help dump as an OK
// result.
func TestGroupWithoutSubcommand(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"alpha"})
		if code != clicore.ExitUsage {
			t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
		}
		if !strings.Contains(out.String(), "alpha") {
			t.Fatalf("expected help output naming the group: %q", out.String())
		}
	})
	t.Run("json", func(t *testing.T) {
		root := buildTestTree()
		code, out, _ := run(t, root, []string{"alpha", "--output", "json"})
		if code != clicore.ExitUsage {
			t.Fatalf("exit code = %d, want %d", code, clicore.ExitUsage)
		}
		envelope := decodeEnvelope(t, out)
		if envelope.Error.Code != "COMMAND_IS_GROUP" {
			t.Fatalf("error code = %q, want COMMAND_IS_GROUP", envelope.Error.Code)
		}
		available := toStringSlice(t, envelope.Error.Context["availableCommands"])
		if !equalStrings(available, []string{"beta", "gamma"}) {
			t.Fatalf("context.availableCommands = %v, want exactly [beta gamma]", available)
		}
	})
}

// TestExitCodeCategoryMapping pins that every Category the package defines maps to a distinct exit
// code, and that clicore.ExitCodes() — the published contract table — covers every one of them
// exactly once. Deriving the wanted code from clicore.NewError (rather than hard-coding both sides)
// is what makes this fail if a category's wiring and its published table entry ever drift apart.
func TestExitCodeCategoryMapping(t *testing.T) {
	table := clicore.ExitCodes()

	seenCodes := map[int]bool{}
	for _, entry := range table {
		if seenCodes[entry.Code] {
			t.Fatalf("exit code %d appears more than once in ExitCodes()", entry.Code)
		}
		seenCodes[entry.Code] = true
	}

	// Every Category constant the package exports, enumerated by hand from errors.go. A category
	// added there without being added here — and to the switch clicore.NewError relies on — is
	// exactly the drift this test exists to catch.
	categories := []clicore.Category{
		clicore.CategoryUsage, clicore.CategoryInternal, clicore.CategoryNotFound,
		clicore.CategoryConflict, clicore.CategoryDenied, clicore.CategoryProvider,
		clicore.CategoryConfig, clicore.CategoryUnavailable,
	}
	if len(table) != len(categories)+1 { // +1 for the success (empty-category) entry
		t.Fatalf("ExitCodes() has %d entries, want %d (one per category, plus one for success)", len(table), len(categories)+1)
	}

	byCategory := map[clicore.Category]clicore.ExitCodeSpec{}
	for _, entry := range table {
		byCategory[entry.Category] = entry
	}
	for _, category := range categories {
		entry, ok := byCategory[category]
		if !ok {
			t.Fatalf("ExitCodes() has no entry for category %q", category)
		}
		wired := clicore.NewError(category, "X", "x").ExitCode
		if entry.Code != wired {
			t.Fatalf("category %q: ExitCodes() publishes exit %d, but NewError wires exit %d", category, entry.Code, wired)
		}
	}
}

// TestGuardSeam pins the four load-bearing properties of the one authorization seam clicore
// exposes: a nil Guard changes nothing, a denying Guard produces the exact same envelope/exit-code
// shape a Run-returned error would, the Guard runs strictly AFTER --help (which must stay reachable
// with no authority at all), and the Guard is never invoked for a group (which never reaches a Run
// call in the first place).
func TestGuardSeam(t *testing.T) {
	guardedTree := func(guard clicore.Guard) *clicore.Command {
		root := buildTestTree()
		root.Guard = guard
		return root
	}

	t.Run("nil guard allows", func(t *testing.T) {
		root := guardedTree(nil)
		code, out, _ := run(t, root, []string{"leaf", "--name", "x", "--output", "json"})
		if code != clicore.ExitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		envelope := decodeEnvelope(t, out)
		if envelope.Status != "OK" {
			t.Fatalf("status = %q, want OK", envelope.Status)
		}
	})

	t.Run("guard denies", func(t *testing.T) {
		denial := clicore.NewError(clicore.CategoryDenied, "SYNTHETIC_DENIED", "synthetic denial")
		root := guardedTree(func(ctx context.Context, inv *clicore.Invocation) (context.Context, *clicore.Error) {
			return ctx, denial
		})

		t.Run("json mode: one document, DENIED category, exit 5", func(t *testing.T) {
			code, out, diag := run(t, root, []string{"leaf", "--name", "x", "--output", "json"})
			if code != clicore.ExitDenied {
				t.Fatalf("exit code = %d, want %d (ExitDenied)", code, clicore.ExitDenied)
			}
			envelope := decodeEnvelope(t, out) // fails the test if this is not exactly one document
			if envelope.Error == nil || envelope.Error.Code != "SYNTHETIC_DENIED" {
				t.Fatalf("error = %+v, want code SYNTHETIC_DENIED", envelope.Error)
			}
			if envelope.Error.Category != clicore.CategoryDenied {
				t.Fatalf("category = %q, want DENIED", envelope.Error.Category)
			}
			if diag.Len() != 0 {
				t.Fatalf("diagnostics not empty on a JSON-mode denial: %q", diag.String())
			}
		})

		t.Run("text mode: nothing on stdout", func(t *testing.T) {
			code, out, diag := run(t, root, []string{"leaf", "--name", "x"})
			if code != clicore.ExitDenied {
				t.Fatalf("exit code = %d, want %d (ExitDenied)", code, clicore.ExitDenied)
			}
			if out.Len() != 0 {
				t.Fatalf("text-mode denial wrote to stdout: %q", out.String())
			}
			if diag.Len() == 0 {
				t.Fatalf("text-mode denial produced no diagnostics")
			}
		})
	})

	t.Run("guard runs after help, never for --help itself", func(t *testing.T) {
		called := false
		root := guardedTree(func(ctx context.Context, inv *clicore.Invocation) (context.Context, *clicore.Error) {
			called = true
			return ctx, clicore.NewError(clicore.CategoryDenied, "SHOULD_NOT_RUN", "guard ran for --help")
		})
		code, _, _ := run(t, root, []string{"leaf", "--help", "--output", "json"})
		if code != clicore.ExitOK {
			t.Fatalf("--help exit code = %d, want 0 (guard must never block help)", code)
		}
		if called {
			t.Fatalf("guard was invoked for --help, which must stay reachable without authority")
		}
	})

	t.Run("guard not run for a group", func(t *testing.T) {
		called := false
		root := guardedTree(func(ctx context.Context, inv *clicore.Invocation) (context.Context, *clicore.Error) {
			called = true
			return ctx, nil
		})
		code, _, _ := run(t, root, []string{"alpha", "--output", "json"})
		if code != clicore.ExitUsage {
			t.Fatalf("group exit code = %d, want %d (COMMAND_IS_GROUP)", code, clicore.ExitUsage)
		}
		if called {
			t.Fatalf("guard was invoked for a group, which never reaches a Run call")
		}
	})
}

// TestParseFailureRespectsRequestedJSON pins wantsJSON's job: even when parsing fails before an
// Invocation exists (so the normal --output lookup has nothing to read), a caller that asked for
// JSON via either `--output json` or `--output=json` must still get JSON, never text — otherwise the
// one case a scripted caller most needs JSON for (its own mistake) is the one case it doesn't get it.
func TestParseFailureRespectsRequestedJSON(t *testing.T) {
	cases := [][]string{
		{"leaf", "--nope", "--output", "json"},
		{"leaf", "--nope", "--output=json"},
	}
	for _, args := range cases {
		root := buildTestTree()
		_, out, diag := run(t, root, args)
		if out.Len() == 0 {
			t.Fatalf("args=%v: expected JSON on stdout despite the parse failure, got nothing (diag=%q)", args, diag.String())
		}
		var envelope clicore.Envelope
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatalf("args=%v: stdout is not valid JSON: %v (%q)", args, err, out.String())
		}
		if envelope.Kind != clicore.KindError {
			t.Fatalf("args=%v: kind = %q, want %q", args, envelope.Kind, clicore.KindError)
		}
	}
}
