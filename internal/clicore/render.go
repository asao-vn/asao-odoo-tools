package clicore

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Envelope is the single JSON document the CLI emits in `--output json` mode, for success and for
// failure alike. One shape, always on stdout, exactly once per invocation.
//
// Putting failures on stdout too is deliberate: an agent reads one stream and always gets a
// parseable document. stderr carries diagnostics only and is never part of the contract, so a
// diagnostic line can never corrupt the document being parsed.
type Envelope struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Command    string `json:"command"`
	// Status is OK or ERROR.
	Status string      `json:"status"`
	Data   interface{} `json:"data,omitempty"`
	Error  *Error      `json:"error,omitempty"`
}

const (
	// KindResult is the envelope kind for a successful command.
	KindResult = "CommandResult"
	// KindError is the envelope kind for a failed command.
	KindError = "CommandError"
)

// EmitJSON writes exactly one envelope to w, followed by a newline.
func EmitJSON(w io.Writer, envelope Envelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	// Every value the CLI emits is composed of plain Go types; escaping HTML would corrupt
	// provider text (a `&` in a record name) for no benefit.
	encoder.SetEscapeHTML(false)
	return encoder.Encode(envelope)
}

// RawTextResult is an optional interface data payloads can implement to render
// raw, unformatted text (such as generated shell scripts) in default text mode
// while preserving the standard Envelope in JSON mode.
type RawTextResult interface {
	RawText() string
}

// RenderText writes a human-readable rendering of a successful result.
func RenderText(w io.Writer, command string, data interface{}) error {
	if data == nil {
		_, err := fmt.Fprintf(w, "%s: OK\n", command)
		return err
	}
	if raw, ok := data.(RawTextResult); ok {
		_, err := fmt.Fprint(w, raw.RawText())
		return err
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", encoded)
	return err
}

// RenderTextError writes a human-readable rendering of a failure. It carries the same facts as the
// JSON error body — code, message, resource, remediation — so a human and an agent are never told
// different things.
func RenderTextError(w io.Writer, command string, err *Error) {
	fmt.Fprintf(w, "error: %s\n", err.Message)
	fmt.Fprintf(w, "  command:   %s\n", command)
	fmt.Fprintf(w, "  code:      %s\n", err.Code)
	fmt.Fprintf(w, "  category:  %s (exit %d)\n", err.Category, err.ExitCode)
	if err.Resource != nil {
		fmt.Fprintf(w, "  resource:  %s %s\n", err.Resource.Type, err.Resource.ID)
	}
	if len(err.Context) > 0 {
		encoded, marshalErr := json.MarshalIndent(err.Context, "  ", "  ")
		if marshalErr == nil {
			fmt.Fprintf(w, "  context:   %s\n", encoded)
		}
	}
	if err.Remediation != "" {
		fmt.Fprintf(w, "  next:      %s\n", err.Remediation)
	}
}

// RenderHelp writes the human-readable help for one command. It is the text twin of Specify: the
// same facts, same order, so `--help` and `--help --output json` never disagree — which is the
// historical "help and JSON output inconsistency" debt, closed by construction rather than by
// keeping two hand-written descriptions in sync.
func RenderHelp(w io.Writer, command *Command) {
	spec := Specify(command, false)

	fmt.Fprintf(w, "%s\n", spec.Command)
	if spec.Summary != "" {
		fmt.Fprintf(w, "  %s\n", spec.Summary)
	}
	if spec.Description != "" {
		fmt.Fprintf(w, "\n%s\n", wrap(spec.Description, 92, "  "))
	}
	if spec.SideEffect != "" {
		fmt.Fprintf(w, "\nSIDE EFFECT\n  %s — %s\n", spec.SideEffect, sideEffectMeaning(spec.SideEffect))
	}
	if len(spec.Subcommands) > 0 {
		fmt.Fprintf(w, "\nSUBCOMMANDS\n")
		width := 0
		for _, child := range command.Children {
			if len(child.Name) > width {
				width = len(child.Name)
			}
		}
		for _, child := range command.Children {
			fmt.Fprintf(w, "  %-*s  %s\n", width, child.Name, child.Summary)
		}
	}
	if len(spec.Flags) > 0 {
		fmt.Fprintf(w, "\nFLAGS\n")
		for _, flag := range spec.Flags {
			label := fmt.Sprintf("--%s <%s>", flag.Name, flag.Kind)
			if flag.Kind == KindBool {
				label = "--" + flag.Name
			}
			marks := []string{}
			if flag.Required {
				marks = append(marks, "required")
			}
			if flag.repeatable() {
				marks = append(marks, "repeatable")
			}
			if flag.Default != "" {
				marks = append(marks, "default "+flag.Default)
			}
			if len(flag.Enum) > 0 {
				marks = append(marks, "one of "+strings.Join(flag.Enum, "|"))
			}
			suffix := ""
			if len(marks) > 0 {
				suffix = "  [" + strings.Join(marks, ", ") + "]"
			}
			fmt.Fprintf(w, "  %s%s\n", label, suffix)
			if flag.Description != "" {
				fmt.Fprintf(w, "%s\n", wrap(flag.Description, 88, "      "))
			}
		}
	}
	if len(spec.Examples) > 0 {
		fmt.Fprintf(w, "\nEXAMPLES\n")
		for _, example := range spec.Examples {
			fmt.Fprintf(w, "  %s\n", example)
		}
	}
	if command.parent == nil {
		program := programOf(command)
		fmt.Fprintf(w, "\nDISCOVERY\n")
		fmt.Fprintf(w, "  %s commands list --output json      Full machine-readable command tree.\n", program)
		fmt.Fprintf(w, "  %s <command> --help --output json   Machine-readable specification of one command.\n", program)
		fmt.Fprintf(w, "  %s doctor --output json             Environment and dependency diagnosis.\n", program)
		fmt.Fprintf(w, "\nEXIT CODES\n")
		for _, code := range ExitCodes() {
			fmt.Fprintf(w, "  %d  %s\n", code.Code, code.Meaning)
		}
	}
}

func sideEffectMeaning(effect SideEffect) string {
	switch effect {
	case EffectNone:
		return "local only; contacts nothing"
	case EffectRead:
		return "reads canonical state and/or a provider; mutates nothing"
	case EffectWriteCanonical:
		return "writes ASAO canonical state"
	case EffectWriteProvider:
		return "causes a mutation in an external provider"
	case EffectWriteLocalConfig:
		return "writes local configuration on this machine; contacts nothing"
	case EffectWriteLocalHost:
		return "writes files on this machine and drives its local containers; contacts no ASAO service"
	case EffectWriteLocalFiles:
		return "writes new files under a folder you name on this machine; contacts nothing"
	default:
		return "unspecified"
	}
}

// wrap re-flows text to width, prefixing every line with indent.
//
// Paragraphs are separated by a blank line; single newlines inside a paragraph are soft, so a
// description written to fit a source file re-flows to the terminal instead of being wrapped twice
// into ragged half-lines.
func wrap(text string, width int, indent string) string {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := indent + words[0]
		for _, word := range words[1:] {
			if len(line)+1+len(word) > width {
				lines = append(lines, line)
				line = indent + word
				continue
			}
			line += " " + word
		}
		lines = append(lines, line, "")
	}
	// The trailing blank produced after the final paragraph is not a paragraph break.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
