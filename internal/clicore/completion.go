package clicore

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// CompletionResult contains the generated shell completion script and implements
// RawTextResult to enable pipeable unquoted output in text mode.
type CompletionResult struct {
	Shell  string `json:"shell"`
	Script string `json:"script"`
}

// RawText returns the unformatted script string for text rendering.
func (r CompletionResult) RawText() string {
	return r.Script
}

type compCommandInfo struct {
	path        string
	name        string
	summary     string
	subcommands []compSubcommandInfo
	flags       []compFlagInfo
}

type compSubcommandInfo struct {
	name    string
	summary string
}

type compFlagInfo struct {
	name        string
	description string
	enum        []string
	isBool      bool
}

func sanitizeDescription(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func isSafeShellWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func sanitizeIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	res := b.String()
	if res == "" || (res[0] >= '0' && res[0] <= '9') {
		res = "_" + res
	}
	return res
}

func escapeBash(s string) string {
	s = sanitizeDescription(s)
	if isSafeShellWord(s) {
		return s
	}
	sq := "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	return escapeDoubleQuotes(sq)
}

func quoteBashWord(s string) string {
	s = sanitizeDescription(s)
	if isSafeShellWord(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func quoteBashPattern(s string) string {
	s = sanitizeDescription(s)
	if s == "" {
		return `""`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func escapeDoubleQuotes(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "$", "\\$")
	s = strings.ReplaceAll(s, "`", "\\`")
	return s
}

func escapeZsh(s string) string {
	s = sanitizeDescription(s)
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ":", "\\:")
	s = strings.ReplaceAll(s, "'", "'\\''")
	return s
}

func escapeFish(s string) string {
	s = sanitizeDescription(s)
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

func escapeFishDouble(s string) string {
	s = sanitizeDescription(s)
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "$", "\\$")
	s = strings.ReplaceAll(s, "(", "\\(")
	s = strings.ReplaceAll(s, ")", "\\)")
	return s
}

func escapeFishA(s string) string {
	s = sanitizeDescription(s)
	if isSafeShellWord(s) {
		return s
	}
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "$", "\\$")
	return "'" + s + "'"
}

func quoteFishCondArg(s string) string {
	s = sanitizeDescription(s)
	if isSafeShellWord(s) {
		return s
	}
	s = strings.ReplaceAll(s, "\\", "\\\\\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\\\\\"")
	s = strings.ReplaceAll(s, "$", "\\\\\\$")
	s = strings.ReplaceAll(s, "(", "\\\\\\(")
	s = strings.ReplaceAll(s, ")", "\\\\\\)")
	return "\\\"" + s + "\\\""
}

func quoteFishWord(s string) string {
	s = sanitizeDescription(s)
	if isSafeShellWord(s) {
		return s
	}
	return "'" + escapeFish(s) + "'"
}

func collectCompCommands(c *Command, prefix string) []compCommandInfo {
	path := prefix
	subcommands := make([]compSubcommandInfo, 0, len(c.Children))
	for _, child := range c.Children {
		subcommands = append(subcommands, compSubcommandInfo{
			name:    child.Name,
			summary: child.Summary,
		})
	}
	sort.Slice(subcommands, func(i, j int) bool {
		return subcommands[i].name < subcommands[j].name
	})

	seenFlags := map[string]bool{}
	flags := make([]compFlagInfo, 0, len(c.Flags)+len(GlobalFlags()))
	for _, f := range append(append([]Flag{}, c.Flags...), GlobalFlags()...) {
		if seenFlags[f.Name] {
			continue
		}
		seenFlags[f.Name] = true
		flags = append(flags, compFlagInfo{
			name:        f.Name,
			description: f.Description,
			enum:        f.Enum,
			isBool:      f.Kind == KindBool,
		})
	}
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].name < flags[j].name
	})

	res := []compCommandInfo{{
		path:        path,
		name:        c.Name,
		summary:     c.Summary,
		subcommands: subcommands,
		flags:       flags,
	}}

	for _, child := range c.Children {
		childPrefix := child.Name
		if prefix != "" {
			childPrefix = prefix + "__" + child.Name
		}
		res = append(res, collectCompCommands(child, childPrefix)...)
	}
	return res
}

// GenerateBashCompletion produces a complete, valid bash completion script for root.
func GenerateBashCompletion(root *Command) string {
	rawProg := programOf(root)
	progIdent := sanitizeIdent(rawProg)
	progArg := quoteBashWord(rawProg)
	commands := collectCompCommands(root, "")

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("# bash completion for %s\n", sanitizeDescription(rawProg)))
	buf.WriteString(fmt.Sprintf("_%s_completion() {\n", progIdent))
	buf.WriteString("    local cur prev words cword\n")
	buf.WriteString("    _init_completion -n : 2>/dev/null || {\n")
	buf.WriteString("        cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	buf.WriteString("        prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	buf.WriteString("        words=(\"${COMP_WORDS[@]}\")\n")
	buf.WriteString("        cword=$COMP_CWORD\n")
	buf.WriteString("    }\n\n")

	// Flag enum value completions
	enumMap := map[string][]string{}
	for _, cmd := range commands {
		for _, f := range cmd.flags {
			if len(f.enum) > 0 {
				enumMap["--"+f.name] = f.enum
			}
		}
	}
	if len(enumMap) > 0 {
		var flagNames []string
		for fn := range enumMap {
			flagNames = append(flagNames, fn)
		}
		sort.Strings(flagNames)
		buf.WriteString("    case \"$prev\" in\n")
		for _, fn := range flagNames {
			vals := make([]string, len(enumMap[fn]))
			for i, v := range enumMap[fn] {
				vals[i] = escapeBash(v)
			}
			pat := fn
			if !isSafeShellWord(pat) {
				pat = quoteBashPattern(pat)
			}
			buf.WriteString(fmt.Sprintf("        %s)\n", pat))
			buf.WriteString(fmt.Sprintf("            COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") )\n", strings.Join(vals, " ")))
			buf.WriteString("            return 0\n")
			buf.WriteString("            ;;\n")
		}
		buf.WriteString("    esac\n\n")
	}

	buf.WriteString("    local cmd=\"\"\n")
	buf.WriteString("    local i=1\n")
	buf.WriteString("    while [[ $i -lt $cword ]]; do\n")
	buf.WriteString("        local w=\"${words[i]}\"\n")
	buf.WriteString("        if [[ \"$w\" != -* ]]; then\n")
	buf.WriteString("            if [[ -z \"$cmd\" ]]; then\n")
	buf.WriteString("                cmd=\"$w\"\n")
	buf.WriteString("            else\n")
	buf.WriteString("                cmd=\"${cmd}__${w}\"\n")
	buf.WriteString("            fi\n")
	buf.WriteString("        fi\n")
	buf.WriteString("        ((i++))\n")
	buf.WriteString("    done\n\n")

	buf.WriteString("    case \"$cmd\" in\n")
	for _, cmd := range commands {
		var words []string
		for _, sub := range cmd.subcommands {
			words = append(words, escapeBash(sub.name))
		}
		for _, f := range cmd.flags {
			words = append(words, escapeBash("--"+f.name))
		}
		sort.Strings(words)
		pattern := quoteBashPattern(cmd.path)
		buf.WriteString(fmt.Sprintf("        %s)\n", pattern))
		buf.WriteString(fmt.Sprintf("            COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") )\n", strings.Join(words, " ")))
		buf.WriteString("            return 0\n")
		buf.WriteString("            ;;\n")
	}
	buf.WriteString("        *)\n")
	buf.WriteString("            ;;\n")
	buf.WriteString("    esac\n")
	buf.WriteString("}\n")
	buf.WriteString(fmt.Sprintf("complete -F _%s_completion %s\n", progIdent, progArg))

	return buf.String()
}

// GenerateZshCompletion produces a complete, valid zsh completion script for root.
func GenerateZshCompletion(root *Command) string {
	rawProg := programOf(root)
	progIdent := sanitizeIdent(rawProg)
	progArg := quoteBashWord(rawProg)
	commands := collectCompCommands(root, "")

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("#compdef %s\n\n", sanitizeDescription(rawProg)))
	buf.WriteString(fmt.Sprintf("_%s() {\n", progIdent))
	buf.WriteString("    local -a words_no_flags\n")
	buf.WriteString("    local i=2\n")
	buf.WriteString("    while (( i <= CURRENT )); do\n")
	buf.WriteString("        local w=\"${words[i]}\"\n")
	buf.WriteString("        if [[ \"$w\" != -* && $i -lt $CURRENT ]]; then\n")
	buf.WriteString("            words_no_flags+=(\"$w\")\n")
	buf.WriteString("        fi\n")
	buf.WriteString("        ((i++))\n")
	buf.WriteString("    done\n\n")

	buf.WriteString("    local cmd=\"${(j:__:)words_no_flags}\"\n\n")
	buf.WriteString("    case \"$cmd\" in\n")
	for _, cmd := range commands {
		pattern := quoteBashPattern(cmd.path)
		buf.WriteString(fmt.Sprintf("        %s)\n", pattern))

		buf.WriteString("            local -a subcmds=(\n")
		for _, sub := range cmd.subcommands {
			name := escapeZsh(sub.name)
			desc := escapeZsh(sub.summary)
			if desc == "" {
				desc = name
			}
			buf.WriteString(fmt.Sprintf("                '%s:%s'\n", name, desc))
		}
		buf.WriteString("            )\n")

		buf.WriteString("            local -a flags=(\n")
		for _, f := range cmd.flags {
			name := escapeZsh(f.name)
			desc := escapeZsh(f.description)
			if desc == "" {
				desc = name
			}
			buf.WriteString(fmt.Sprintf("                '--%s:%s'\n", name, desc))
		}
		buf.WriteString("            )\n")

		descTag := escapeZsh(rawProg)
		buf.WriteString("            if [[ \"$words[CURRENT]\" == -* ]]; then\n")
		buf.WriteString(fmt.Sprintf("                _describe -t flags '%s flags' flags\n", descTag))
		buf.WriteString("            else\n")
		buf.WriteString(fmt.Sprintf("                _describe -t subcommands '%s commands' subcmds\n", descTag))
		buf.WriteString(fmt.Sprintf("                _describe -t flags '%s flags' flags\n", descTag))
		buf.WriteString("            fi\n")
		buf.WriteString("            return 0\n")
		buf.WriteString("            ;;\n")
	}
	buf.WriteString("        *)\n")
	buf.WriteString("            ;;\n")
	buf.WriteString("    esac\n")
	buf.WriteString("}\n\n")
	buf.WriteString(fmt.Sprintf("compdef _%s %s\n", progIdent, progArg))

	return buf.String()
}

// GenerateFishCompletion produces a complete, valid fish completion script for root.
func GenerateFishCompletion(root *Command) string {
	rawProg := programOf(root)
	progArg := quoteFishWord(rawProg)
	commands := collectCompCommands(root, "")

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("# fish completion for %s\n", sanitizeDescription(rawProg)))
	buf.WriteString(fmt.Sprintf("complete -c %s -f\n\n", progArg))

	for _, cmd := range commands {
		if cmd.path == "" {
			// Root commands and flags
			for _, sub := range cmd.subcommands {
				desc := escapeFish(sub.summary)
				subName := escapeFishA(sub.name)
				buf.WriteString(fmt.Sprintf("complete -c %s -n \"__fish_use_subcommand\" -a \"%s\" -d '%s'\n", progArg, subName, desc))
			}
			for _, f := range cmd.flags {
				desc := escapeFish(f.description)
				flagName := escapeFishDouble(f.name)
				if len(f.enum) > 0 {
					enumVals := make([]string, len(f.enum))
					for i, v := range f.enum {
						enumVals[i] = escapeFishA(v)
					}
					buf.WriteString(fmt.Sprintf("complete -c %s -l \"%s\" -d '%s' -r -a \"%s\"\n", progArg, flagName, desc, strings.Join(enumVals, " ")))
				} else {
					buf.WriteString(fmt.Sprintf("complete -c %s -l \"%s\" -d '%s'\n", progArg, flagName, desc))
				}
			}
		} else {
			parts := strings.Split(cmd.path, "__")
			var seenConditions []string
			for _, p := range parts {
				seenConditions = append(seenConditions, fmt.Sprintf("__fish_seen_subcommand_from %s", quoteFishCondArg(p)))
			}
			cond := strings.Join(seenConditions, "; and ")

			if len(cmd.subcommands) > 0 {
				var notSeen []string
				for _, sub := range cmd.subcommands {
					notSeen = append(notSeen, quoteFishCondArg(sub.name))
				}
				subCond := fmt.Sprintf("%s; and not __fish_seen_subcommand_from %s", cond, strings.Join(notSeen, " "))
				for _, sub := range cmd.subcommands {
					desc := escapeFish(sub.summary)
					subName := escapeFishA(sub.name)
					buf.WriteString(fmt.Sprintf("complete -c %s -n \"%s\" -a \"%s\" -d '%s'\n", progArg, subCond, subName, desc))
				}
			}

			for _, f := range cmd.flags {
				desc := escapeFish(f.description)
				flagName := escapeFishDouble(f.name)
				if len(f.enum) > 0 {
					enumVals := make([]string, len(f.enum))
					for i, v := range f.enum {
						enumVals[i] = escapeFishA(v)
					}
					buf.WriteString(fmt.Sprintf("complete -c %s -n \"%s\" -l \"%s\" -d '%s' -r -a \"%s\"\n", progArg, cond, flagName, desc, strings.Join(enumVals, " ")))
				} else {
					buf.WriteString(fmt.Sprintf("complete -c %s -n \"%s\" -l \"%s\" -d '%s'\n", progArg, cond, flagName, desc))
				}
			}
		}
	}

	return buf.String()
}
