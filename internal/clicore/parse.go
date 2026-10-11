package clicore

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Invocation is one parsed command line: which command, with which values. Commands read values
// through its accessors, which enforce the declared kind — a command never re-parses a raw string.
type Invocation struct {
	Command *Command
	// Raw holds every occurrence of every flag, in order.
	Raw map[string][]string
	// ActorAsaoUserID is set by the authority guard (packages/cli/internal/authority) immediately
	// after successful authentication, before Authority.CapabilityFrom is ever evaluated — Parse
	// never sets it, and it is empty for any command whose Authority.Authentication is
	// AuthenticationNone, or before the guard has run at all. It exists so a CapabilityFrom closure
	// can compare the authenticated actor against a caller-supplied subject (see
	// `capability show`/`capability authorize`'s self-inspection rule) without CapabilityFrom's own
	// signature needing a context.Context parameter.
	ActorAsaoUserID string
}

// Output reports the requested output format.
func (i *Invocation) Output() string {
	if values := i.Raw["output"]; len(values) > 0 {
		return values[len(values)-1]
	}
	return "text"
}

// JSONOutput reports whether the machine contract was requested.
func (i *Invocation) JSONOutput() bool { return i.Output() == "json" }

// Has reports whether a flag was supplied at all.
func (i *Invocation) Has(name string) bool { return len(i.Raw[name]) > 0 }

// String returns the last value of a string flag, or its declared default.
func (i *Invocation) String(name string) string {
	if values := i.Raw[name]; len(values) > 0 {
		return values[len(values)-1]
	}
	if flag, ok := i.Command.flag(name); ok {
		return flag.Default
	}
	return ""
}

// RequiredString returns a string flag's value, refusing an empty one by name.
func (i *Invocation) RequiredString(name string) (string, error) {
	value := strings.TrimSpace(i.String(name))
	if value == "" {
		return "", UsageError("MISSING_REQUIRED_FLAG", "--%s is required", name).
			WithContextValue("flag", name).
			WithRemediation(fmt.Sprintf("Run `%s --help` to see every accepted flag.", qualify(i.Command)))
	}
	return value, nil
}

// Duration returns a duration flag's value, or fallback when it was not supplied.
// If the flag was supplied, it must be a valid positive duration string (e.g. 5s, 1m, 500ms).
// An explicitly supplied empty value, invalid, zero, or negative values are rejected with a UsageError.
func (i *Invocation) Duration(name string, fallback time.Duration) (time.Duration, error) {
	if !i.Has(name) {
		return fallback, nil
	}
	raw := i.String(name)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, UsageError("INVALID_FLAG_VALUE", "--%s expects a valid duration (e.g. 5s, 1m, 500ms), got %q", name, raw).
			WithContextValue("flag", name)
	}
	if d <= 0 {
		return 0, UsageError("INVALID_FLAG_VALUE", "--%s must be positive and non-zero, got %q", name, raw).
			WithContextValue("flag", name)
	}
	return d, nil
}

// Int returns an integer flag's value, or fallback when it was not supplied.
func (i *Invocation) Int(name string, fallback int) (int, error) {
	raw := i.String(name)
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, UsageError("INVALID_FLAG_VALUE", "--%s expects an integer, got %q", name, raw).
			WithContextValue("flag", name)
	}
	return parsed, nil
}

// Int64 returns a 64-bit integer flag's value, or fallback when it was not supplied.
func (i *Invocation) Int64(name string, fallback int64) (int64, error) {
	raw := i.String(name)
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, UsageError("INVALID_FLAG_VALUE", "--%s expects an integer, got %q", name, raw).
			WithContextValue("flag", name)
	}
	return parsed, nil
}

// Bool returns a boolean flag's value.
func (i *Invocation) Bool(name string) (bool, error) {
	values := i.Raw[name]
	if len(values) == 0 {
		if flag, ok := i.Command.flag(name); ok && flag.Default != "" {
			return strconv.ParseBool(flag.Default)
		}
		return false, nil
	}
	raw := values[len(values)-1]
	if raw == "" {
		return true, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, UsageError("INVALID_FLAG_VALUE", "--%s expects true or false, got %q", name, raw).
			WithContextValue("flag", name)
	}
	return parsed, nil
}

// JSON decodes a KindJSON flag into target.
//
// It decodes exactly ONCE, and refuses a value that decodes to a bare JSON string. That refusal is
// the fix for the historical double-encoding defect: a caller that ran `--values "$(jq -c ...)"`
// through one too many encoding steps sent `"{\"name\":\"x\"}"`, which is valid JSON — a string —
// and the old CLI forwarded it as a string, producing a confusing provider-side failure far from
// the real cause. Refusing here names the cause.
func (i *Invocation) JSON(name string, target interface{}) error {
	raw := strings.TrimSpace(i.String(name))
	if raw == "" {
		return nil
	}
	var probe interface{}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return UsageError("INVALID_JSON", "--%s is not valid JSON: %v", name, err).
			WithContextValue("flag", name).
			WithRemediation("Pass the JSON document itself, not a quoted string containing JSON.")
	}
	if _, isString := probe.(string); isString {
		return UsageError("DOUBLE_ENCODED_JSON", "--%s received a JSON string, not a JSON document", name).
			WithContextValue("flag", name).
			WithRemediation("The value was encoded twice. Pass {\"key\":\"value\"} directly, not \"{\\\"key\\\":\\\"value\\\"}\".")
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return UsageError("INVALID_JSON", "--%s could not be decoded: %v", name, err).
			WithContextValue("flag", name)
	}
	return nil
}

// KeyValues collects every occurrence of a repeatable name=value flag into a map. Values are typed
// by the caller-supplied coerce function, so a command can apply its own schema; pass nil to keep
// every value a string.
func (i *Invocation) KeyValues(name string, coerce func(key, value string) (interface{}, error)) (map[string]interface{}, error) {
	values := i.Raw[name]
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]interface{}, len(values))
	for _, pair := range values {
		key, value, found := strings.Cut(pair, "=")
		if !found || strings.TrimSpace(key) == "" {
			return nil, UsageError("INVALID_FLAG_VALUE", "--%s expects name=value, got %q", name, pair).
				WithContextValue("flag", name).
				WithRemediation(fmt.Sprintf("Repeat the flag once per field, e.g. --%s name=Example.", name))
		}
		if coerce == nil {
			out[key] = value
			continue
		}
		coerced, err := coerce(key, value)
		if err != nil {
			return nil, err
		}
		out[key] = coerced
	}
	return out, nil
}

// StringSlice returns every occurrence of a repeatable string flag, in order. A single occurrence
// carrying a comma-separated list is also accepted, because that is what callers reach for.
func (i *Invocation) StringSlice(name string) []string {
	values := i.Raw[name]
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// Parse walks the command tree and parses flags against the resolved command's declared set.
//
// Every refusal names what was accepted instead, because an agent that cannot see the accepted set
// can only correct itself by guessing.
func Parse(root *Command, args []string) (*Invocation, error) {
	Prepare(root)
	command := root
	raw := map[string][]string{}

	index := 0
	for index < len(args) {
		arg := args[index]

		if arg == "--" {
			index++
			break
		}
		if strings.HasPrefix(arg, "--") {
			name, inline, hasInline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			flag, known := command.flag(name)
			if !known {
				return nil, unknownFlagError(command, name)
			}
			switch {
			case hasInline:
				raw[name] = append(raw[name], inline)
			case flag.Kind == KindBool:
				raw[name] = append(raw[name], "true")
			default:
				if index+1 >= len(args) {
					return nil, UsageError("MISSING_FLAG_VALUE", "--%s expects a %s value", name, flag.Kind).
						WithContextValue("flag", name)
				}
				index++
				raw[name] = append(raw[name], args[index])
			}
			if !flag.repeatable() && len(raw[name]) > 1 {
				// Keeping the last value silently is how a typo becomes an invisible behaviour
				// change; a repeated non-repeatable flag is a caller mistake worth naming.
				return nil, UsageError("REPEATED_FLAG", "--%s was given more than once but is not repeatable", name).
					WithContextValue("flag", name).
					WithContextValue("values", raw[name])
			}
			index++
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			return nil, UsageError("UNKNOWN_FLAG", "unknown flag %q; this CLI accepts only long flags (--name)", arg).
				WithContextValue("acceptedFlags", command.flagNames())
		}

		child := command.child(arg)
		if child == nil {
			return nil, unknownCommandError(command, arg)
		}
		command = child
		index++
	}
	if index < len(args) {
		return nil, UsageError("UNEXPECTED_ARGUMENTS", "unexpected positional arguments: %s", strings.Join(args[index:], " ")).
			WithContextValue("arguments", args[index:]).
			WithRemediation("This CLI takes no positional arguments; every input is a named flag.")
	}

	invocation := &Invocation{Command: command, Raw: raw}
	if err := validate(invocation); err != nil {
		return invocation, err
	}
	return invocation, nil
}

// validate applies the declared enum and required rules. It is skipped for --help, so a caller
// that does not yet know the required flags can always ask.
func validate(invocation *Invocation) error {
	if len(invocation.Raw["help"]) > 0 {
		return nil
	}
	for _, flag := range append(append([]Flag{}, invocation.Command.Flags...), GlobalFlags()...) {
		values := invocation.Raw[flag.Name]
		if len(values) == 0 {
			if flag.Required {
				return UsageError("MISSING_REQUIRED_FLAG", "--%s is required", flag.Name).
					WithContextValue("flag", flag.Name).
					WithRemediation(fmt.Sprintf("Run `%s --help` to see every accepted flag.", qualify(invocation.Command)))
			}
			continue
		}
		if len(flag.Enum) == 0 {
			continue
		}
		for _, value := range values {
			if !contains(flag.Enum, value) {
				return UsageError("INVALID_FLAG_VALUE", "--%s does not accept %q", flag.Name, value).
					WithContextValue("flag", flag.Name).
					WithContextValue("acceptedValues", flag.Enum)
			}
		}
	}
	return nil
}

func unknownFlagError(command *Command, name string) *Error {
	accepted := command.flagNames()
	err := UsageError("UNKNOWN_FLAG", "unknown flag --%s for `%s`", name, qualify(command)).
		WithContextValue("acceptedFlags", accepted)
	if suggestion, ok := closest(name, accepted); ok {
		return err.WithRemediation(fmt.Sprintf("Did you mean --%s?", suggestion))
	}
	return err.WithRemediation(fmt.Sprintf("Run `%s --help` to see every accepted flag.", qualify(command)))
}

func unknownCommandError(command *Command, name string) *Error {
	if !command.IsGroup() {
		return UsageError("UNEXPECTED_ARGUMENTS", "`%s` takes no subcommand, but got %q", qualify(command), name).
			WithContextValue("argument", name)
	}
	available := command.childNames()
	err := UsageError("UNKNOWN_COMMAND", "unknown command %q under `%s`", name, qualify(command)).
		WithResource("cli.command", strings.TrimSpace(command.PathString()+" "+name)).
		WithContextValue("availableCommands", available)
	if suggestion, ok := closest(name, available); ok {
		return err.WithRemediation(fmt.Sprintf("Did you mean `%s`?", joinProgram(programOf(command), strings.TrimSpace(command.PathString()+" "+suggestion))))
	}
	return err.WithRemediation(fmt.Sprintf("Run `%s commands list --output json` for the full command tree.", programOf(command)))
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// closest finds the nearest accepted name within a small edit distance, so a typo gets corrected
// rather than merely rejected. It is deliberately conservative: a far-off guess is worse than none.
func closest(input string, candidates []string) (string, bool) {
	best, bestDistance := "", 1<<30
	sorted := append([]string{}, candidates...)
	sort.Strings(sorted)
	for _, candidate := range sorted {
		distance := editDistance(input, candidate)
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	limit := len(input) / 3
	if limit < 1 {
		limit = 1
	}
	if best != "" && bestDistance <= limit+1 {
		return best, true
	}
	return "", false
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min3(current[j-1]+1, previous[j]+1, previous[j-1]+cost)
		}
		copy(previous, current)
	}
	return previous[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
