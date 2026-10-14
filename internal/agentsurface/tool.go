// Package agentsurface projects the governed `asao` command tree onto agent tools.
//
// A tool IS a CLI leaf. Its name, description and input schema are derived from the leaf's own
// registration (path, summary, flags, side effect, authority), and calling it means running exactly
// that leaf with exactly those flags through the same executable path a person's terminal uses. There
// is no second catalogue of operations here, and no second executor: Argv turns a tool call back into
// the command line, and the caller runs it.
//
// Which leaves become tools is decided by the leaf's AgentAccess, assigned once at tree assembly by
// internal/commands/agent_surface.go. This package never widens that decision; Filter can only
// narrow it further.
package agentsurface

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/toolexposure"
)

// OperatorAuthorityFlag is the deliberate-action acknowledgement 139 governed writes require. It is
// never part of a tool's input: the person's deliberate action on an agent surface is the host's
// approval of the call (WRITE) or the server's own confirmation request (CONFIRM), so the server
// supplies the acknowledgement itself, and only for a leaf the policy opened.
const OperatorAuthorityFlag = "operator-authority"

// ConfirmImpactFlag is the destructive-impact acknowledgement. It is offered only on CONFIRM tools;
// a person who confirms such a call is told that the impact is accepted with it.
const ConfirmImpactFlag = "confirm-impact"

// descriptionLimit and flagDescriptionLimit keep a full tools/list affordable for an agent's
// context. The whole text of every command and flag stays one call away: `asao <command> --help`,
// or describe in the catalog mode of `asao mcp serve`.
const (
	descriptionLimit     = 480
	flagDescriptionLimit = 240
)

// sharedFlagDescriptions replace the long, identical text a few flags carry on a hundred commands.
// The meaning is unchanged; the full text is in `asao <command> --help`.
var sharedFlagDescriptions = map[string]string{
	"organization": "Organization ID: the isolation boundary every resource belongs to.",
	"reason":       "Why this change is being made. Echoed in the result; the audit trail records the action and a stable reason code.",
}

// clinicalToolsets are the top-level commands whose tools speak about a person's health.
var clinicalToolsets = map[string]bool{"care": true}

// MaxToolNameLength keeps every tool name usable by hosts that prefix it (Claude Code calls a tool
// mcp__<server>__<name>) and cap the whole at 64 characters.
const MaxToolNameLength = 64 - len("mcp__asao__")

// Annotations are the MCP tool annotations (protocol 2025-03-26 and later). They are hints for the
// host, never a security boundary; the runtime decides.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// Tool is one leaf as an agent sees it. The JSON shape is the MCP tool definition plus the fields
// `asao mcp tools` publishes for review.
type Tool struct {
	Name        string                 `json:"name"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Annotations Annotations            `json:"annotations"`

	// Review fields — never sent to an MCP host in its tool list.
	Command                string              `json:"command"`
	Help                   string              `json:"help,omitempty"`
	Toolset                string              `json:"toolset"`
	AgentAccess            clicore.AgentAccess `json:"agentAccess"`
	ConfirmWhen            map[string][]string `json:"confirmWhen,omitempty"`
	SideEffect             clicore.SideEffect  `json:"sideEffect"`
	RequiredCapability     string              `json:"requiredCapability,omitempty"`
	CapabilityResolvedFrom string              `json:"capabilityResolvedFrom,omitempty"`
	HumanStep              clicore.HumanStep   `json:"humanStep,omitempty"`

	path       []string
	params     map[string]clicore.Flag
	supplyAck  bool
	contextOrg bool
}

// Filter narrows the projection. The zero value keeps every tool the policy opened.
type Filter struct {
	// Toolsets keeps only tools whose top-level command is listed (e.g. "alop", "social").
	Toolsets []string
	// ReadOnly keeps only READ tools.
	ReadOnly bool
}

// Project returns the tools for every leaf the policy opened, sorted by name. root must be the
// governed tree (commands.ClientRoot()).
func Project(root *clicore.Command, filter Filter) ([]Tool, error) {
	return ProjectAs(root, "asao", filter)
}

// ProjectAs projects another tree the same way, naming program in every tool's command line: the
// standalone odoo-cli offers its own leaves through the same server under its own name.
func ProjectAs(root *clicore.Command, program string, filter Filter) ([]Tool, error) {
	clicore.Prepare(root)
	wanted := map[string]bool{}
	for _, toolset := range filter.Toolsets {
		if trimmed := strings.TrimSpace(toolset); trimmed != "" {
			wanted[trimmed] = true
		}
	}
	var tools []Tool
	seen := map[string]string{}
	var walk func(command *clicore.Command) error
	walk = func(command *clicore.Command) error {
		if command.IsGroup() {
			for _, child := range command.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}
		access := command.AgentAccess
		if access == "" || access == clicore.AgentAccessNone {
			return nil
		}
		if filter.ReadOnly && access != clicore.AgentAccessRead {
			return nil
		}
		path := command.Path()
		if len(wanted) > 0 && !wanted[path[0]] {
			return nil
		}
		tool, err := toolFor(command, path, program)
		if err != nil {
			return err
		}
		if previous, clash := seen[tool.Name]; clash {
			return fmt.Errorf("agentsurface: tool name %q is derived from both %q and %q", tool.Name, previous, tool.Command)
		}
		seen[tool.Name] = tool.Command
		tools = append(tools, tool)
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

// Toolsets lists the top-level commands that contribute at least one tool.
func Toolsets(tools []Tool) []string {
	set := map[string]bool{}
	for _, tool := range tools {
		set[tool.Toolset] = true
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// NameFor derives a tool name from a command path: "alop students add-student" → "alop_students_add_student".
func NameFor(path []string) string {
	return strings.ReplaceAll(strings.Join(path, "_"), "-", "_")
}

func toolFor(command *clicore.Command, path []string, program string) (Tool, error) {
	name := NameFor(path)
	if len(name) > MaxToolNameLength {
		// The governed tree names its commands to fit, and a longer one is a defect to fix there. A
		// generated tree (odoo-cli's catalog) cannot choose its names: a long one keeps its start and
		// gains a short hash of the whole, so it stays unique and the same on every run.
		if program == "asao" {
			return Tool{}, fmt.Errorf("agentsurface: tool name %q is longer than %d characters", name, MaxToolNameLength)
		}
		sum := sha256.Sum256([]byte(name))
		name = strings.TrimRight(name[:MaxToolNameLength-9], "_") + "_" + hex.EncodeToString(sum[:4])
	}
	commandLine := program + " " + strings.Join(path, " ")
	description := describe(command, commandLine)
	// The clinical-authority vocabulary rule (DEC-2026-0084) binds every tool name, and the whole
	// description of a clinical tool. It is not applied to other descriptions: `asao doctor`
	// "diagnoses" a network connection, which is not a clinical claim.
	if toolexposure.ContainsForbiddenVocabulary(name) ||
		(clinicalToolsets[path[0]] && toolexposure.ContainsForbiddenVocabulary(description)) {
		return Tool{}, fmt.Errorf("agentsurface: %q carries forbidden clinical-authority vocabulary", commandLine)
	}
	tool := Tool{
		Name:        name,
		Title:       commandLine,
		Description: description,
		Command:     commandLine,
		Help:        strings.TrimSpace(command.Description),
		Toolset:     path[0],
		AgentAccess: command.AgentAccess,
		ConfirmWhen: command.AgentConfirmWhen,
		SideEffect:  command.SideEffect,
		HumanStep:   command.HumanStep,
		path:        append([]string(nil), path...),
		params:      map[string]clicore.Flag{},
		contextOrg:  command.ContextualOrganization,
	}
	if authority := command.Authority; authority != nil {
		tool.RequiredCapability = authority.Capability
		if authority.CapabilityFrom != nil {
			tool.CapabilityResolvedFrom = authority.CapabilityFromFlag
		}
	}
	properties := map[string]interface{}{}
	var required []string
	for _, flag := range command.Flags {
		if flag.Name == OperatorAuthorityFlag {
			tool.supplyAck = true
			continue
		}
		if flag.Name == ConfirmImpactFlag && command.AgentAccess != clicore.AgentAccessConfirm {
			continue
		}
		if flag.PersonOnly {
			continue
		}
		property := schemaFor(flag)
		if short, ok := sharedFlagDescriptions[flag.Name]; ok {
			property["description"] = short
			if flag.Name == "organization" && command.ContextualOrganization {
				property["description"] = short + " Defaults to the person's active organization (asao org use)."
			}
		}
		properties[flag.Name] = property
		tool.params[flag.Name] = flag
		if flag.Required && !(flag.Name == "organization" && command.ContextualOrganization) {
			required = append(required, flag.Name)
		}
	}
	sort.Strings(required)
	schema := map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	tool.InputSchema = schema
	tool.Annotations = Annotations{
		ReadOnlyHint:    command.AgentAccess == clicore.AgentAccessRead,
		DestructiveHint: command.Authority != nil && command.Authority.Destructive,
		IdempotentHint:  command.AgentAccess == clicore.AgentAccessRead,
		OpenWorldHint:   command.SideEffect == clicore.EffectWriteProvider,
	}
	return tool, nil
}

// describe is the tool description: the leaf's own words, then the facts an agent needs before it
// calls — the equivalent command, the effect, the capability, and what confirmation it needs.
func describe(command *clicore.Command, commandLine string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(command.Summary))
	if paragraph := firstParagraph(command.Description, descriptionLimit); paragraph != "" {
		b.WriteString("\n\n")
		b.WriteString(paragraph)
	}
	b.WriteString("\n\nCLI: ")
	b.WriteString(commandLine)
	b.WriteString(". Effect: ")
	b.WriteString(string(command.SideEffect))
	b.WriteString(".")
	if authority := command.Authority; authority != nil {
		switch {
		case authority.Capability != "":
			b.WriteString(" Requires capability ")
			b.WriteString(authority.Capability)
			if authority.ScopeKind == "organization" {
				b.WriteString(" in the organization")
			}
			b.WriteString(".")
		case authority.CapabilityFrom != nil:
			b.WriteString(" The required capability depends on --")
			b.WriteString(authority.CapabilityFromFlag)
			b.WriteString(".")
		}
	}
	if command.ContextualOrganization {
		b.WriteString(" organization defaults to the person's active organization (asao org use).")
	}
	switch command.AgentAccess {
	case clicore.AgentAccessConfirm:
		b.WriteString(" The person is asked to confirm this call before it runs.")
	case clicore.AgentAccessWrite:
		if len(command.AgentConfirmWhen) > 0 {
			b.WriteString(" The person is asked to confirm when ")
			b.WriteString(describeConfirmWhen(command.AgentConfirmWhen))
			b.WriteString(".")
		}
	}
	if hasFlag(command, OperatorAuthorityFlag) && hasFlag(command, "reason") {
		b.WriteString(" State why in reason; it is echoed in the result.")
	}
	if command.HumanStep == clicore.HumanStepBrowser {
		b.WriteString(" The result is a link the person must open in a browser to finish.")
	}
	return b.String()
}

func describeConfirmWhen(when map[string][]string) string {
	names := make([]string, 0, len(when))
	for name := range when {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+" is "+strings.Join(when[name], " or "))
	}
	return strings.Join(parts, ", or ")
}

func firstParagraph(text string, limit int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if index := strings.Index(text, "\n\n"); index >= 0 {
		text = text[:index]
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > limit {
		cut := strings.LastIndex(text[:limit], " ")
		if cut < limit/2 {
			cut = limit
		}
		text = text[:cut] + " …"
	}
	return text
}

func hasFlag(command *clicore.Command, name string) bool {
	for _, flag := range command.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}

func schemaFor(flag clicore.Flag) map[string]interface{} {
	item := map[string]interface{}{}
	switch flag.Kind {
	case clicore.KindInt:
		item["type"] = "integer"
	case clicore.KindBool:
		item["type"] = "boolean"
	case clicore.KindNumber:
		item["type"] = "number"
	case clicore.KindJSON:
		// Any JSON value, sent as itself — never as a string containing JSON.
	case clicore.KindKeyValue:
		item["type"] = "object"
		item["additionalProperties"] = map[string]interface{}{"type": []string{"string", "number", "boolean"}}
	default:
		item["type"] = "string"
	}
	if len(flag.Enum) > 0 {
		item["enum"] = append([]string(nil), flag.Enum...)
	}
	if flag.Default != "" && flag.Kind != clicore.KindKeyValue {
		item["default"] = typedDefault(flag)
	}
	description := firstParagraph(flag.Description, flagDescriptionLimit)
	if flag.Kind == clicore.KindJSON {
		description = "A JSON value (object, array or scalar), sent as JSON. " + description
	}
	if flag.Repeatable && flag.Kind != clicore.KindKeyValue {
		return map[string]interface{}{
			"type":        "array",
			"items":       item,
			"description": description,
		}
	}
	item["description"] = description
	return item
}

func typedDefault(flag clicore.Flag) interface{} {
	switch flag.Kind {
	case clicore.KindInt:
		if value, err := strconv.ParseInt(flag.Default, 10, 64); err == nil {
			return value
		}
	case clicore.KindBool:
		if value, err := strconv.ParseBool(flag.Default); err == nil {
			return value
		}
	case clicore.KindNumber:
		if value, err := strconv.ParseFloat(flag.Default, 64); err == nil {
			return value
		}
	}
	return flag.Default
}

// NeedsConfirmation reports whether this call needs the person's confirmation before it runs.
func (t Tool) NeedsConfirmation(arguments map[string]interface{}) bool {
	if t.AgentAccess == clicore.AgentAccessConfirm {
		return true
	}
	if t.AgentAccess != clicore.AgentAccessWrite {
		return false
	}
	for name, values := range t.ConfirmWhen {
		given, ok := arguments[name]
		if !ok {
			if flag, known := t.params[name]; known && flag.Default != "" {
				given = flag.Default
			} else {
				continue
			}
		}
		text := fmt.Sprint(given)
		for _, value := range values {
			if text == value {
				return true
			}
		}
	}
	return false
}

// Argv turns a tool call back into the exact command line it stands for, always ending in
// --output=json. The CLI's own parser then validates it exactly as it validates a typed command, so
// an agent can never reach an input a person could not have typed.
func (t Tool) Argv(arguments map[string]interface{}) ([]string, *clicore.Error) {
	argv := append([]string(nil), t.path...)
	names := make([]string, 0, len(arguments))
	for name := range arguments {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		flag, known := t.params[name]
		if !known {
			return nil, clicore.UsageError("UNKNOWN_TOOL_ARGUMENT", "%s takes no argument %q", t.Name, name).
				WithContextValue("argument", name).
				WithContextValue("acceptedArguments", t.ArgumentNames())
		}
		values, err := flagValues(flag, arguments[name])
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			argv = append(argv, "--"+name+"="+value)
		}
	}
	if t.supplyAck && t.AgentAccess != clicore.AgentAccessRead {
		argv = append(argv, "--"+OperatorAuthorityFlag+"=true")
	}
	return append(argv, "--output=json"), nil
}

// ArgumentNames lists the tool's accepted arguments.
func (t Tool) ArgumentNames() []string {
	names := make([]string, 0, len(t.params))
	for name := range t.params {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CommandLine renders a tool call as the line a person would type, for transcripts and for the
// confirmation fallback. It quotes values for a POSIX shell.
func (t Tool) CommandLine(arguments map[string]interface{}) string {
	argv, err := t.Argv(arguments)
	if err != nil {
		return t.Command
	}
	parts := []string{"asao"}
	for _, arg := range argv {
		if arg == "--output=json" {
			continue
		}
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '=' || r == '/' || r == ':' || r == ',' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) < 0 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func flagValues(flag clicore.Flag, value interface{}) ([]string, *clicore.Error) {
	if value == nil {
		return nil, nil
	}
	if flag.Kind == clicore.KindKeyValue {
		fields, ok := value.(map[string]interface{})
		if !ok {
			return nil, argumentError(flag, "an object of name: value fields")
		}
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]string, 0, len(keys))
		for _, key := range keys {
			scalar, err := scalarText(flag, fields[key])
			if err != nil {
				return nil, err
			}
			out = append(out, key+"="+scalar)
		}
		return out, nil
	}
	if flag.Repeatable {
		items, ok := value.([]interface{})
		if !ok {
			items = []interface{}{value}
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			text, err := oneValue(flag, item)
			if err != nil {
				return nil, err
			}
			out = append(out, text)
		}
		return out, nil
	}
	text, err := oneValue(flag, value)
	if err != nil {
		return nil, err
	}
	return []string{text}, nil
}

func oneValue(flag clicore.Flag, value interface{}) (string, *clicore.Error) {
	switch flag.Kind {
	case clicore.KindJSON:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", argumentError(flag, "a JSON value")
		}
		return string(encoded), nil
	case clicore.KindBool:
		switch typed := value.(type) {
		case bool:
			return strconv.FormatBool(typed), nil
		case string:
			if parsed, err := strconv.ParseBool(typed); err == nil {
				return strconv.FormatBool(parsed), nil
			}
		}
		return "", argumentError(flag, "true or false")
	case clicore.KindInt:
		switch typed := value.(type) {
		case float64:
			if typed == math.Trunc(typed) && math.Abs(typed) < 1<<53 {
				return strconv.FormatInt(int64(typed), 10), nil
			}
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return strconv.FormatInt(parsed, 10), nil
			}
		case string:
			if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
				return strconv.FormatInt(parsed, 10), nil
			}
		}
		return "", argumentError(flag, "an integer")
	case clicore.KindNumber:
		switch typed := value.(type) {
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64), nil
		case json.Number:
			return typed.String(), nil
		case string:
			if _, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
				return strings.TrimSpace(typed), nil
			}
		}
		return "", argumentError(flag, "a number")
	default:
		return scalarText(flag, value)
	}
}

func scalarText(flag clicore.Flag, value interface{}) (string, *clicore.Error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		return strconv.FormatBool(typed), nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), nil
	case json.Number:
		return typed.String(), nil
	}
	return "", argumentError(flag, "a string")
}

func argumentError(flag clicore.Flag, want string) *clicore.Error {
	return clicore.UsageError("INVALID_TOOL_ARGUMENT", "argument %q expects %s", flag.Name, want).
		WithContextValue("argument", flag.Name).
		WithContextValue("kind", string(flag.Kind))
}
