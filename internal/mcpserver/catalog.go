package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
)

// The catalog mode offers four tools instead of one per command, for hosts that cannot hold a
// few hundred tool definitions (several cap the list at 40 to 128). It is the same offer, reached
// in two steps: <name>_find and _describe read the projection; <name>_read and _write run a
// tool from it with exactly the checks a direct call gets — the same Argv, the same confirmation,
// the same executor. Reads and writes are separate tools so a host can approve reads once and still
// ask for every write.

const (
	ModeTools   = "tools"
	ModeCatalog = "catalog"
)

// catalogNames are one server's four catalog tools: <name>_find, _describe, _read and _write.
type catalogNames struct{ find, describe, read, write string }

func (s *server) catalog() catalogNames {
	name := s.name()
	return catalogNames{find: name + "_find", describe: name + "_describe", read: name + "_read", write: name + "_write"}
}

func (s *server) catalogTools() []map[string]interface{} {
	names, product, program := s.catalog(), s.product(), s.program()
	argumentsSchema := map[string]interface{}{
		"type":        "object",
		"description": "The tool's arguments, exactly as " + names.describe + " lists them in inputSchema.",
	}
	return []map[string]interface{}{
		{
			"name":  names.find,
			"title": "Find " + product + " commands",
			"description": "Search the " + product + " commands offered to you. Returns each match's tool name, the " + program + " command it runs, " +
				"its access (READ, WRITE, CONFIRM) and summary. With no query it lists the toolsets. Then call " + names.describe + " for the inputs.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":   map[string]interface{}{"type": "string", "description": "Words to look for in the command, its summary or description, e.g. \"student attendance\"."},
					"toolset": map[string]interface{}{"type": "string", "description": "Only this command family, e.g. alop, social, scheduling."},
					"access":  map[string]interface{}{"type": "string", "enum": []string{"READ", "WRITE", "CONFIRM"}, "description": "Only this access level."},
					"limit":   map[string]interface{}{"type": "integer", "description": "At most this many matches (default 25)."},
				},
				"additionalProperties": false,
			},
			"annotations": agentsurface.Annotations{ReadOnlyHint: true, IdempotentHint: true},
		},
		{
			"name":  names.describe,
			"title": "Describe " + article(product) + " command",
			"description": "The full definition of one tool from " + names.find + ": what it does, its JSON input schema, its access and " +
				"the capability that governs it. Call it before " + names.read + " or " + names.write + ".",
			"inputSchema": map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"name": map[string]interface{}{"type": "string", "description": "A tool name from " + names.find + "."}},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
			"annotations": agentsurface.Annotations{ReadOnlyHint: true, IdempotentHint: true},
		},
		{
			"name":  names.read,
			"title": "Run " + article(product) + " read",
			"description": "Run a READ tool (from " + names.find + ") with its arguments. Returns the " + program + " command's JSON result document. " +
				"Refuses anything that writes: use " + names.write + " for those.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":      map[string]interface{}{"type": "string", "description": "A READ tool name."},
					"arguments": argumentsSchema,
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
			"annotations": agentsurface.Annotations{ReadOnlyHint: true, IdempotentHint: true},
		},
		{
			"name":  names.write,
			"title": "Run " + article(product) + " change",
			"description": "Run a WRITE or CONFIRM tool (from " + names.find + ") with its arguments, as the person. CONFIRM tools ask the person " +
				"first. Returns the " + program + " command's JSON result document.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":      map[string]interface{}{"type": "string", "description": "A WRITE or CONFIRM tool name."},
					"arguments": argumentsSchema,
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
			"annotations": agentsurface.Annotations{DestructiveHint: true, OpenWorldHint: true},
		},
	}
}

// article is "an ASAO" / "an odoo-cli": both names start with a vowel sound; a consonant gets "a".
func article(product string) string {
	if product != "" && strings.ContainsRune("AEIOUaeiou", rune(product[0])) {
		return "an " + product
	}
	return "a " + product
}

func (s *server) isCatalogTool(name string) bool {
	names := s.catalog()
	switch name {
	case names.find, names.describe, names.read, names.write:
		return true
	}
	return false
}

// callCatalog runs one of the four catalog tools.
func (s *server) callCatalog(ctx context.Context, name string, arguments map[string]interface{}) map[string]interface{} {
	names := s.catalog()
	switch name {
	case names.find:
		return s.dataResult(s.find(arguments))
	case names.describe:
		tool, problem := s.lookup(arguments, "")
		if problem != nil {
			return s.errorResult(agentsurface.Tool{Command: s.program() + " mcp " + name}, problem, "")
		}
		return s.dataResult(map[string]interface{}{
			"name":                   tool.Name,
			"command":                tool.Command,
			"agentAccess":            tool.AgentAccess,
			"confirmWhen":            tool.ConfirmWhen,
			"sideEffect":             tool.SideEffect,
			"requiredCapability":     tool.RequiredCapability,
			"capabilityResolvedFrom": tool.CapabilityResolvedFrom,
			"humanStep":              tool.HumanStep,
			"description":            tool.Description,
			"help":                   tool.Help,
			"inputSchema":            tool.InputSchema,
			"runWith":                map[bool]string{true: names.read, false: names.write}[tool.AgentAccess == clicore.AgentAccessRead],
		})
	case names.read, names.write:
		tool, problem := s.lookup(arguments, name)
		if problem != nil {
			return s.errorResult(agentsurface.Tool{Command: s.program() + " mcp " + name}, problem, "")
		}
		inner := map[string]interface{}{}
		if raw, ok := arguments["arguments"]; ok && raw != nil {
			typed, isObject := raw.(map[string]interface{})
			if !isObject {
				return s.errorResult(tool, clicore.UsageError("INVALID_TOOL_ARGUMENT", "arguments must be an object of the tool's inputs"), "")
			}
			inner = typed
		}
		return s.callTool(ctx, tool, inner)
	}
	return s.errorResult(agentsurface.Tool{Command: s.program() + " mcp"}, clicore.UsageError("UNKNOWN_TOOL", "unknown catalog tool %q", name), "")
}

func (s *server) lookup(arguments map[string]interface{}, runner string) (agentsurface.Tool, *clicore.Error) {
	names := s.catalog()
	name, _ := arguments["name"].(string)
	tool, ok := s.tools[strings.TrimSpace(name)]
	if !ok {
		return agentsurface.Tool{}, clicore.NewError(clicore.CategoryNotFound, "UNKNOWN_TOOL", fmt.Sprintf("no tool named %q is offered", name)).
			WithRemediation("Call " + names.find + " to see the tools offered to you.")
	}
	switch runner {
	case names.read:
		if tool.AgentAccess != clicore.AgentAccessRead {
			return agentsurface.Tool{}, clicore.UsageError("TOOL_WRITES", "%s changes state; run it with %s", tool.Name, names.write)
		}
	case names.write:
		if tool.AgentAccess == clicore.AgentAccessRead {
			return agentsurface.Tool{}, clicore.UsageError("TOOL_READS", "%s only reads; run it with %s", tool.Name, names.read)
		}
	}
	return tool, nil
}

func (s *server) find(arguments map[string]interface{}) map[string]interface{} {
	query, _ := arguments["query"].(string)
	toolset, _ := arguments["toolset"].(string)
	access, _ := arguments["access"].(string)
	limit := 25
	if raw, ok := arguments["limit"]; ok {
		if number, isNumber := raw.(json.Number); isNumber {
			if parsed, err := number.Int64(); err == nil && parsed > 0 {
				limit = int(parsed)
			}
		}
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 && toolset == "" && access == "" {
		counts := map[string]int{}
		for _, name := range s.order {
			counts[s.tools[name].Toolset]++
		}
		names := make([]string, 0, len(counts))
		for name := range counts {
			names = append(names, name)
		}
		sort.Strings(names)
		toolsets := make([]map[string]interface{}, 0, len(names))
		for _, name := range names {
			toolsets = append(toolsets, map[string]interface{}{"toolset": name, "tools": counts[name]})
		}
		return map[string]interface{}{"toolsets": toolsets, "toolCount": len(s.order),
			"next": "Call " + s.catalog().find + " with a query or a toolset."}
	}
	type scored struct {
		tool  agentsurface.Tool
		score int
	}
	var matches []scored
	for _, name := range s.order {
		tool := s.tools[name]
		if toolset != "" && tool.Toolset != toolset {
			continue
		}
		if access != "" && string(tool.AgentAccess) != access {
			continue
		}
		haystackName := strings.ToLower(tool.Command)
		haystack := strings.ToLower(tool.Description)
		score := 0
		for _, word := range words {
			switch {
			case strings.Contains(haystackName, word):
				score += 3
			case strings.Contains(haystack, word):
				score++
			default:
				score = -1
			}
			if score < 0 {
				break
			}
		}
		if score >= 0 {
			matches = append(matches, scored{tool: tool, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	total := len(matches)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	found := make([]map[string]interface{}, 0, len(matches))
	for _, match := range matches {
		found = append(found, map[string]interface{}{
			"name":        match.tool.Name,
			"command":     match.tool.Command,
			"agentAccess": match.tool.AgentAccess,
			"summary":     firstLine(match.tool.Description),
		})
	}
	return map[string]interface{}{"matches": found, "total": total}
}

// dataResult wraps a catalog answer, which is the server's own data rather than a CLI document.
func (s *server) dataResult(data map[string]interface{}) map[string]interface{} {
	compact, _ := json.Marshal(data)
	result := map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": string(compact)}},
		"isError": false,
	}
	s.stateMu.Lock()
	structured := s.protocolVersion >= "2025-06-18"
	s.stateMu.Unlock()
	if structured {
		result["structuredContent"] = data
	}
	return result
}
