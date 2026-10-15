package standalone

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/agentsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/mcpserver"
	"github.com/asao-platform/atoc/packages/odoo"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// mcpCatalogThreshold is how many tools the auto mode offers one by one before it offers the four
// catalog tools instead: the catalog has more than a thousand operations, and several agent hosts
// cap their tool list at 40 to 128.
const mcpCatalogThreshold = 60

// agentAccessFor is an operation's exposure to an agent on `mcp serve`: reads are offered; writes
// are offered only on a server started with --allow-writes; a high-risk, critical or destructive
// operation, or one whose impact must be accepted, runs only after the person confirms it in the
// agent host; installing a module is never offered.
func agentAccessFor(op direct.Operation) clicore.AgentAccess {
	switch {
	case op.Strategy == string(odoo.StrategyModuleInstall):
		// Installing a module changes what the Odoo is, not a record in it: the person does that.
		return clicore.AgentAccessNone
	case op.Kind == string(odoo.KindRead) || op.Strategy == string(odoo.StrategyRead):
		return clicore.AgentAccessRead
	case op.RequiresImpactConfirmation, op.Strategy == string(odoo.StrategyDestructiveGuarded),
		op.Risk == string(odoo.RiskHigh), op.Risk == string(odoo.RiskCritical):
		return clicore.AgentAccessConfirm
	default:
		return clicore.AgentAccessWrite
	}
}

// openAgentSurface offers the discovery commands that read nothing from Odoo, or only describe it,
// to an agent. Everything else that is not an operation (profiles, credentials, specs, raw calls,
// module installs, updates) stays with the person.
func openAgentSurface(root *clicore.Command) {
	for _, path := range [][]string{{"catalog", "list"}, {"catalog", "describe"}, {"doctor"}, {"trust"}} {
		if leaf := findPath(root, path); leaf != nil && !leaf.IsGroup() {
			leaf.AgentAccess = clicore.AgentAccessRead
			// The server runs every call against the profile the person started it with.
			for index := range leaf.Flags {
				if leaf.Flags[index].Name == "profile" {
					leaf.Flags[index].PersonOnly = true
				}
			}
		}
	}
}

func findPath(root *clicore.Command, path []string) *clicore.Command {
	current := root
	for _, name := range path {
		var next *clicore.Command
		for _, child := range current.Children {
			if child.Name == name {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

var mcpServeFlags = []clicore.Flag{
	{Name: "profile", Kind: clicore.KindString, Description: "The profile every tool call runs against. Empty uses the profile file's default, as a typed command does."},
	{Name: "allow-writes", Kind: clicore.KindBool, Description: "Offer the operations that change Odoo. Without it the server offers reads only, and a write cannot be reached through it."},
	{Name: "allow-unverified", Kind: clicore.KindBool, Description: "Run unverified operations and act on a customised Odoo, as --allow-unverified does on a typed command. The agent cannot set it."},
	{Name: "mode", Kind: clicore.KindString, Default: "auto", Enum: []string{"auto", "tools", "catalog"},
		Description: fmt.Sprintf("tools: one MCP tool per operation. catalog: four tools (odoo_find, odoo_describe, odoo_read, odoo_write) that reach the same operations. auto: catalog above %d tools.", mcpCatalogThreshold)},
	{Name: "toolsets", Kind: clicore.KindString, Description: "Comma-separated domains to offer (e.g. sales,finance,inventory). Empty offers every domain."},
}

func mcpGroup(config programConfig) *clicore.Command {
	serve := local(&clicore.Command{
		Name:    "serve",
		Summary: "Serve the operation catalog to an AI agent over the Model Context Protocol (stdio).",
		Description: `Speaks MCP (JSON-RPC 2.0, one message per line) on standard input and output until input
closes. Every tool is one aerp command, run exactly as if you had typed it, against the profile you
started the server with: same checks before acting, same result document.

What an agent is offered:
- reads, always;
- writes, only when you start the server with --allow-writes;
- high-risk and destructive operations only after you confirm each call in the agent host (MCP
  elicitation). A host that cannot ask gets a refusal with the exact command line instead.

The agent cannot choose the profile, accept unverified results, write files on this machine or set
the provider context: those are yours. Remember the trust boundary ("aerp trust"): the server acts
with the full authority of the profile's Odoo credential, and an agent that can run commands can run
aerp directly. The server bounds what is OFFERED; it is not a sandbox.

Register it with an agent host, for example: claude mcp add odoo -- aerp mcp serve --profile my-odoo`,
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Flags:      mcpServeFlags,
		Examples: []string{
			"aerp mcp serve --profile my-odoo",
			"aerp mcp serve --profile my-odoo --allow-writes",
			"aerp mcp serve --profile my-odoo --toolsets sales,finance --mode tools",
		},
		Run: func(context.Context, *clicore.Invocation) (interface{}, error) {
			// The executable runs this leaf itself (ServeMCP): clicore.Run would write a result
			// envelope onto the protocol stream.
			return nil, clicore.NewError(clicore.CategoryInternal, "MCP_SERVE_NEEDS_EXECUTABLE",
				"aerp mcp serve is run by the executable itself and cannot be composed in-process")
		},
	})
	tools := local(&clicore.Command{
		Name:    "tools",
		Summary: "List the tools `mcp serve` would offer, with their access level.",
		Description: `Projects the command tree exactly as the server does: each tool's name, the command it runs and
its access (READ, WRITE, CONFIRM). Pure local computation: no network, no credential.`,
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Flags: []clicore.Flag{
			{Name: "allow-writes", Kind: clicore.KindBool, Description: "Include the tools --allow-writes would offer."},
			{Name: "toolsets", Kind: clicore.KindString, Description: "Comma-separated domains, as for serve."},
		},
		Examples: []string{"aerp mcp tools --output json", "aerp mcp tools --allow-writes --toolsets sales --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			allowWrites, _ := invocation.Bool("allow-writes")
			projected, err := agentsurface.ProjectAs(RootFor(config.name), config.name,
				agentsurface.Filter{ReadOnly: !allowWrites, Toolsets: splitComma(invocation.String("toolsets"))})
			if err != nil {
				return nil, clicore.InternalError(err)
			}
			counts := map[string]int{}
			listed := make([]map[string]interface{}, 0, len(projected))
			for _, tool := range projected {
				counts[string(tool.AgentAccess)]++
				listed = append(listed, map[string]interface{}{"name": tool.Name, "command": tool.Command, "agentAccess": tool.AgentAccess})
			}
			return map[string]interface{}{"toolCount": len(projected), "byAgentAccess": counts,
				"toolsets": agentsurface.Toolsets(projected), "tools": listed}, nil
		},
	})
	return &clicore.Command{
		Name:     "mcp",
		Summary:  "Offer the operation catalog to AI agents over the Model Context Protocol.",
		Children: []*clicore.Command{serve, tools},
	}
}

func splitComma(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// IsMCPServe reports whether args ask for the MCP server rather than a command (help excepted).
func IsMCPServe(args []string) bool {
	if len(args) < 2 || args[0] != "mcp" || args[1] != "serve" {
		return false
	}
	for _, arg := range args[2:] {
		if arg == "--help" || arg == "-h" {
			return false
		}
	}
	return true
}

// ServeMCP runs `<program> mcp serve <args>`: it speaks MCP on in and out until in closes, and
// answers the process exit code. Diagnostics go to diag, never to out.
func ServeMCP(ctx context.Context, program string, args []string, in io.Reader, out, diag io.Writer) int {
	flags := flag.NewFlagSet(program+" mcp serve", flag.ContinueOnError)
	flags.SetOutput(diag)
	profile := flags.String("profile", "", "")
	allowWrites := flags.Bool("allow-writes", false, "")
	allowUnverified := flags.Bool("allow-unverified", false, "")
	mode := flags.String("mode", "auto", "")
	toolsets := flags.String("toolsets", "", "")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *mode != "auto" && *mode != "tools" && *mode != "catalog" {
		fmt.Fprintf(diag, "%s mcp serve: --mode must be auto, tools or catalog\n", program)
		return 2
	}
	tools, err := agentsurface.ProjectAs(RootFor(program), program,
		agentsurface.Filter{ReadOnly: !*allowWrites, Toolsets: splitComma(*toolsets)})
	if err != nil {
		fmt.Fprintf(diag, "%s mcp serve: %v\n", program, err)
		return 1
	}
	chosen := mcpserver.ModeTools
	if *mode == "catalog" || (*mode == "auto" && len(tools) > mcpCatalogThreshold) {
		chosen = mcpserver.ModeCatalog
	}
	execute := func(ctx context.Context, argv []string) ([]byte, []byte, int) {
		root := RootFor(program)
		if leaf := leafOf(root, argv); leaf != nil {
			if *profile != "" && hasFlag(leaf, "profile") {
				argv = append(argv, "--profile="+*profile)
			}
			if *allowUnverified && hasFlag(leaf, "allow-unverified") {
				argv = append(argv, "--allow-unverified=true")
			}
		}
		var stdout, stderr bytes.Buffer
		code := clicore.Run(ctx, root, argv, clicore.Streams{Out: &stdout, Diagnostics: &stderr})
		return stdout.Bytes(), stderr.Bytes(), code
	}
	writes := "Writes are not offered: the person started this server without --allow-writes."
	if *allowWrites {
		writes = "Writes are offered; high-risk and destructive operations ask the person before they run."
	}
	instructions := fmt.Sprintf(`Each tool is one %[1]s command run against the person's Odoo (profile %[2]q), with the full
authority of that profile's Odoo credential. Operations are named by domain (sales, finance, inventory,
people, ...); every result is the command's JSON document: branch on status and error.code, and follow
error.remediation. Reads are offered. %[3]s Before acting, %[1]s checks the Odoo it reaches and refuses
with a reason (a module not installed, a series it has no catalog for, a customised Odoo for a write).`,
		program, orDefault(*profile, "the default"), writes)
	if odooFlavour(program) {
		instructions += fmt.Sprintf(`

If %[1]s itself lets you down — an INTERNAL error, a result that contradicts the tool's description,
help that misled you, or a gap you had to work around — tell the person and offer to report it: the
report draft command (its result says how) builds a scrubbed report on this machine. %[2]s`,
			program, reportConsentRule)
	}
	if err := mcpserver.Serve(ctx, in, out, mcpserver.Options{
		Tools: tools, Execute: execute, Version: releaseVersion, Instructions: instructions, Mode: chosen, Log: diag,
		Name: "odoo", Program: program, Product: program,
	}); err != nil {
		fmt.Fprintf(diag, "%s mcp serve: %v\n", program, err)
		return 1
	}
	return 0
}

// leafOf is the command argv names: the leading words, before the first flag.
func leafOf(root *clicore.Command, argv []string) *clicore.Command {
	var path []string
	for _, word := range argv {
		if strings.HasPrefix(word, "-") {
			break
		}
		path = append(path, word)
	}
	return findPath(root, path)
}

func hasFlag(command *clicore.Command, name string) bool {
	for _, f := range command.Flags {
		if f.Name == name {
			return true
		}
	}
	return false
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
