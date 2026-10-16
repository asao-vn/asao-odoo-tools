package standalone

import (
	"fmt"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo"
)

// How much of the data an agent sees (DEC-2026-0178). The person chooses a level when they start
// mcp serve, or once for a profile; odoo-cli enforces it where its answers leave for the agent:
//   - operate: the agent acts, and gets status, ids, odoo-cli's own verdict and error codes, never a
//     value read from Odoo; it has no read tool but discovery;
//   - act (the default): personal data, free text and files are withheld from every record it reads;
//   - full: everything a read returns.
// A record is a map that Odoo answered: it carries an id, or it is a grouped row. Its keys are Odoo
// field names, classed by packages/odoo (ClassifyField, LooksLikeFreeText). odoo-cli's own text
// (explanations, remediation) is not a record and is kept.

type dataAccess int

const (
	accessOperate dataAccess = iota
	accessAct
	accessFull
)

func (level dataAccess) String() string { return localprofile.DataAccessLevels[level] }

func parseDataAccess(value string) (dataAccess, bool) {
	for index, name := range localprofile.DataAccessLevels {
		if value == name {
			return dataAccess(index), true
		}
	}
	return accessAct, false
}

// resolveDataAccess is a server's level: the profile's (act when it records none), and the flag may
// lower it, never raise it. With no level in the profile, the flag chooses any.
func resolveDataAccess(profileLevel, flagLevel string) (dataAccess, error) {
	ceiling, level := accessFull, accessAct
	if profileLevel != "" {
		recorded, ok := parseDataAccess(profileLevel)
		if !ok {
			return accessAct, fmt.Errorf("the profile records agentDataAccess %q (want operate, act or full)", profileLevel)
		}
		ceiling, level = recorded, recorded
	}
	if flagLevel != "" {
		asked, ok := parseDataAccess(flagLevel)
		if !ok {
			return accessAct, fmt.Errorf("--data-access must be operate, act or full, not %q", flagLevel)
		}
		if asked > ceiling {
			return accessAct, fmt.Errorf("--data-access %s is more than the profile allows (%s): a person raises it with profile set --agent-data-access", asked, ceiling)
		}
		level = asked
	}
	return level, nil
}

// discoveryCommands describe odoo-cli and the Odoo, never a record: open at every level, never filtered.
var discoveryCommands = map[string]bool{
	"catalog list": true, "catalog describe": true, "pack list": true, "pack show": true, "doctor": true, "trust": true,
	"upgrade maps": true, "review checks": true, "controls list": true, "routines templates": true,
}

func isDiscovery(tool agentsurface.Tool) bool {
	command := tool.Command
	if _, rest, ok := strings.Cut(command, " "); ok {
		command = rest
	}
	return discoveryCommands[command]
}

// toolsAtLevel takes away the read tools at operate: discovery stays.
func toolsAtLevel(tools []agentsurface.Tool, level dataAccess) []agentsurface.Tool {
	if level != accessOperate {
		return tools
	}
	kept := make([]agentsurface.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.AgentAccess != clicore.AgentAccessRead || isDiscovery(tool) {
			kept = append(kept, tool)
		}
	}
	return kept
}

// dataAccessFilter is the mcpserver Filter of a level.
func dataAccessFilter(level dataAccess) func(agentsurface.Tool, map[string]interface{}) map[string]interface{} {
	return func(tool agentsurface.Tool, envelope map[string]interface{}) map[string]interface{} {
		if level == accessFull || isDiscovery(tool) {
			return envelope
		}
		if level == accessOperate {
			return operateEnvelope(envelope)
		}
		out := make(map[string]interface{}, len(envelope)+1)
		for key, value := range envelope {
			out[key] = value
		}
		if data, ok := envelope["data"]; ok {
			out["data"] = withholdIn(data, false)
		}
		if problem, ok := envelope["error"].(map[string]interface{}); ok {
			out["error"] = actError(problem)
		}
		out["dataAccess"] = accessAct.String()
		return out
	}
}

const withheldMark = "[withheld: %s]"

// withholdIn walks a result: inside a record it withholds personal data, free text, files and
// credentials; outside one it only looks for records.
func withholdIn(value interface{}, inRecord bool) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		record := isRecord(typed)
		out := make(map[string]interface{}, len(typed))
		for key, nested := range typed {
			if record {
				switch odoo.ClassifyField(key, "") {
				case odoo.FieldCredential:
					out[key] = fmt.Sprintf(withheldMark, "credential")
					continue
				case odoo.FieldPersonal:
					out[key] = fmt.Sprintf(withheldMark, "personal")
					continue
				case odoo.FieldFreeText:
					out[key] = fmt.Sprintf(withheldMark, "free text")
					continue
				}
			}
			out[key] = withholdIn(nested, record)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, nested := range typed {
			out[index] = withholdIn(nested, inRecord)
		}
		return out
	case string:
		if inRecord && odoo.LooksLikeFreeText(typed) {
			return fmt.Sprintf(withheldMark, "free text")
		}
		return typed
	default:
		return value
	}
}

// isRecord says whether a map is what Odoo answered for a record or a grouped row.
func isRecord(value map[string]interface{}) bool {
	if _, ok := value["id"].(float64); ok {
		return true
	}
	_, counted := value["__count"]
	_, grouped := value["__domain"]
	return counted || grouped
}

// actError keeps an error's first line: Odoo's message may run on with the data it quotes.
func actError(problem map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(problem))
	for key, value := range problem {
		out[key] = value
	}
	if message, ok := problem["message"].(string); ok {
		line, _, _ := strings.Cut(message, "\n")
		if len([]rune(line)) > 200 {
			line = string([]rune(line)[:200]) + "…"
		}
		out["message"] = line
	}
	return out
}

// operateKeys are what an operation's outcome keeps at operate: what happened, never what was read.
var operateKeys = []string{"operation", "status", "readiness", "verificationStatus", "providerReference", "evidenceClass", "providerSeries", "targetMediation"}

// The categories whose message is odoo-cli's own (a refused flag, a missing profile) rather than Odoo's.
var ownMessageCategories = map[string]bool{"USAGE": true, "CONFIG": true, "DENIED": true}

func operateEnvelope(envelope map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{"dataAccess": accessOperate.String()}
	for _, key := range []string{"apiVersion", "kind", "command", "status"} {
		if value, ok := envelope[key]; ok {
			out[key] = value
		}
	}
	if data, ok := envelope["data"].(map[string]interface{}); ok {
		kept := map[string]interface{}{}
		for _, key := range operateKeys {
			if value, ok := data[key]; ok {
				kept[key] = value
			}
		}
		if inner, ok := data["data"]; ok {
			if ids := idsOnly(inner); ids != nil {
				kept["data"] = ids
			}
		}
		out["data"] = kept
	}
	if problem, ok := envelope["error"].(map[string]interface{}); ok {
		kept := map[string]interface{}{}
		for _, key := range []string{"code", "category", "exitCode", "retryable", "remediation"} {
			if value, ok := problem[key]; ok {
				kept[key] = value
			}
		}
		category, _ := problem["category"].(string)
		if message, ok := problem["message"].(string); ok && ownMessageCategories[category] {
			kept["message"] = message
		} else {
			kept["message"] = "withheld at data level operate: what Odoo answered may quote its data. The person sees it by running the command line."
		}
		if context, ok := problem["context"].(map[string]interface{}); ok {
			safe := map[string]interface{}{}
			for _, key := range []string{"field", "fields", "operation", "model", "flag", "missingFields", "acceptedFields"} {
				if value, ok := context[key]; ok {
					safe[key] = value
				}
			}
			if len(safe) > 0 {
				kept["context"] = safe
			}
		}
		out["error"] = kept
	}
	return out
}

// idsOnly keeps the ids of what an operation answered: a record's, a list's, or the number itself.
func idsOnly(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		if id, ok := typed["id"]; ok {
			return map[string]interface{}{"id": id}
		}
		if ids, ok := typed["ids"]; ok {
			return map[string]interface{}{"ids": ids}
		}
		return nil
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, nested := range typed {
			if id := idsOnly(nested); id != nil {
				out = append(out, id)
			}
		}
		return out
	case float64, int, int64, bool:
		return typed
	default:
		return nil
	}
}

// dataAccessInstructions tells the agent its level, so it never asks for what it cannot be given.
func dataAccessInstructions(level dataAccess) string {
	switch level {
	case accessOperate:
		return `

Data level operate: you act on the person's Odoo without seeing its data. A result gives the status,
the record id and whether the command's own check of the result passed; reads are not offered. When a
task needs to see data, say so to the person: only they can raise the level.`
	case accessAct:
		return `

Data level act: what you read keeps names, references, states, dates and amounts; personal data
(phones, emails, addresses, tax and identity numbers, bank accounts), free text (notes, descriptions,
messages) and files are withheld and marked [withheld: ...]. Do not try to read them another way. When
a task needs them, say so to the person: only they can raise the level.`
	default:
		return ""
	}
}

// serverDataAccess resolves a server's level from the profile it serves (its agentDataAccess) and
// the --data-access flag. A store or profile that cannot be read records no level.
func serverDataAccess(profileName, flagLevel string) (dataAccess, error) {
	recorded := ""
	if store, err := localprofile.Load(); err == nil {
		if selection, err := store.Resolve(profileName); err == nil {
			recorded = selection.Profile.AgentDataAccess
		}
	}
	return resolveDataAccess(recorded, flagLevel)
}
