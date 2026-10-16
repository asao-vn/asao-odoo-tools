// Package standalone assembles the `aerp` command tree: the standalone AERP product.
//
// READ THIS FIRST, BECAUSE THE WHOLE POINT OF THIS PACKAGE IS A TRUST BOUNDARY.
//
// This binary talks DIRECTLY to an Odoo using a credential its local operator configured. It does
// not authenticate an ASAO Actor, does not evaluate a CapabilityGrant, does not scope anything to an
// ASAO Organization, and writes nothing to ASAO's central audit trail — because none of those things
// exists in this product. Anything holding this binary and the configured credential can do, through
// the canonical AERP surface, whatever that Odoo user can do. That is the deal, it is stated in the
// root command's own help text and published as a machine-readable document by `aerp trust`, and it
// is why this is a SEPARATE EXECUTABLE rather than a flag on `asao`: a governed control plane that
// grows a convenient `--no-auth` has, from that moment, an auth bypass.
//
// WHAT IT SHARES WITH THE GOVERNED PRODUCT, WHICH IS ALMOST EVERYTHING THAT MATTERS. The operation
// catalog, the semantic ids, the input contract and its typing, catalog target rules, the Odoo
// mapping, the RPC transport, the normalized outcome document and the provider error taxonomy are
// all the ONE canonical implementation in packages/odoo, reached through the direct composition and
// rendered through packages/cli/internal/aerpsurface. There is no second catalog and no second
// executor in this package — grep it: it contains no operation id at all.
//
// WHAT IT DOES NOT SHARE, AND CANNOT REACH. This package does not import
// packages/cli/internal/commands, packages/cli/internal/runtimeenv or packages/cli/internal/authority,
// so the standalone binary contains no governed command tree, no canonical Postgres pool and no
// authority guard to be tricked into skipping. Symmetrically, nothing in the governed tree imports
// this package, so no `asao` command — succeeding, failing, or degrading — can reach a local
// profile. Both directions are asserted by standaloneboundary_test.go rather than left to review.
package standalone

import (
	"context"
	"regexp"
	"runtime"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/aerpsurface"
	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

// Program is the executable name this tree publishes everywhere a command path is rendered.
const Program = "aerp"

// Version is the standalone tool's own version. It is deliberately the SAME constant the governed
// CLI reports (clicore.CLIVersion): the two ship from one repository at one commit, and giving them
// independent version numbers would invite a reader to think they carry different AERP semantics
// when the whole design is that they carry identical ones.
const Version = clicore.CLIVersion

// buildCommit and buildDate are ldflags-injectable build provenance, exactly as the governed binary
// has. They default to "unknown" for a plain `go build` and for this module's tests. releaseVersion is
// the published release this binary is (the public repository's tag, e.g. 0.2.1); cliVersion is the
// command contract it speaks, shared with the governed CLI.
var (
	buildCommit    = "unknown"
	buildDate      = "unknown"
	releaseVersion = "dev"
)

// localAuthority is the authority declaration EVERY standalone leaf carries, and it is one shared
// value rather than a literal per command so that "they really do all declare the same posture" is
// checkable at a glance.
//
// Authentication NONE and an empty Capability are the honest values, not a weakening: this product
// has no actor to authenticate and no grant vocabulary to check against, so publishing a
// requiredCapability here would advertise an authorization contract nothing implements — to exactly
// the machine readers least able to verify it. ScopeKind names what actually bounds a call: the
// local profile, and nothing else.
var localAuthority = &clicore.Authority{
	Authentication: clicore.AuthenticationNone,
	ScopeKind:      "local-profile",
}

// destructiveLocalAuthority is localAuthority for the one command family that can carry out a
// catalog DESTRUCTIVE_GUARDED operation. Same absence of governance; an honest worst-case flag.
var destructiveLocalAuthority = &clicore.Authority{
	Authentication: clicore.AuthenticationNone,
	ScopeKind:      "local-profile",
	Destructive:    true,
}

// programConfig binds the ONE shared AERP CLI surface to the selected executable name. The
// historical aerp binary and the public odoo-cli binary therefore render different command paths while
// sharing the same catalog, executor, trust boundary and local profile format.
type programConfig struct {
	name    string
	surface aerpsurface.Surface
}

func newProgramConfig(program string) programConfig {
	program = strings.TrimSpace(program)
	if program == "" {
		program = Program
	}
	return programConfig{
		name: program,
		surface: aerpsurface.Surface{
			CatalogListCommand:     program + " catalog list",
			CatalogDescribeCommand: program + " catalog describe",
			DoctorCommand:          program + " doctor --probe",
			TargetHint:             "Name the provider-native record with --provider-target-id.",
			EntityMediation:        false,
			IncludeSurface:         true,
			AccessDeniedRemediation: "The Odoo refused this profile's credentials, or refused this operation for that user. Check the material behind the profile's credentialRef with " +
				program + " doctor --output json, which reports whether it resolves without printing it.",
		},
	}
}

// odooFlavour is true for the public Odoo client, "odoo-cli" (named "odoo" before 2026-10-11, a name
// it gave up because Odoo's own server installs an `odoo` executable). It adds live schema
// discovery, the raw call and the admin group to the tree.
func odooFlavour(program string) bool {
	return program == "odoo-cli" || program == "odoo"
}

// local marks a leaf as executing entirely in this process under the local operator's authority.
// Every leaf in this tree passes through it, and declareBoundaries fails the build if one does not.
func local(command *clicore.Command) *clicore.Command {
	command.ExecutionBoundary = clicore.ExecutionBoundaryLocalOperator
	return command
}

// Root builds the standalone command tree.
//
// Notice what it does NOT wire: root.Guard stays nil, and root.OutcomeRecorder stays nil. In the
// governed tree those are the authorization evaluator and the audit recorder. Here there is nothing
// to put in them — and leaving them nil is a stronger statement than any comment, because
// clicore.Run's Guard call is the only place authorization could ever happen and there is nothing
// for it to call.
func Root() *clicore.Command {
	return RootFor(Program)
}

// RootFor builds the same standalone command tree under a caller-selected binary name. This lets
// the public Odoo binary and the historical AERP binary share one command/catalog implementation;
// the selected name is presentation metadata and never changes routing or authority.
func RootFor(program string) *clicore.Command {
	config := newProgramConfig(program)
	root := &clicore.Command{
		Program: config.name,
		Summary: "Standalone AERP — run canonical ERP operations directly against a local Odoo.",
		Description: `aerp is the STANDALONE AERP tool. It executes the same canonical ERP operation catalog the
ASAO platform governs, against an Odoo you configure yourself, with no ASAO account and no ASAO
services involved.

TRUST BOUNDARY — READ THIS BEFORE AUTOMATING ANYTHING WITH IT.

This tool acts with the full authority of the Odoo credential in the selected profile. It does not
authenticate an ASAO Actor, does not evaluate an ASAO CapabilityGrant, is not scoped to an ASAO
Organization, and writes no ASAO central audit record — none of those exist in this product. An
agent, script or person that can run this binary and read its profile's credential can do anything
that Odoo user can do through the canonical AERP surface, including destructive operations. Run
"aerp trust --output json" for that statement as a machine-readable document.

The governed product is the other binary. "asao" reaches an ERP only after authenticating an actor,
authorizing a capability at organization scope and recording the decision. If you need
organization-scoped authorization, multi-user governance or a central audit trail, use asao. This
tool is for a single trusted operator: a developer, a local automation, an AI agent running under
your own OS identity, or a LAB.

WHAT IS IDENTICAL TO THE GOVERNED PRODUCT. The operation catalog, every semantic id, every input
contract, provider mapping, and the normalized result document. An operation means the same thing
here as it does there; only the trust and routing envelope differs.

WHAT IS ABSENT HERE BY DESIGN. Canonical business identity (--entity-kind/--entity-id): resolving a
business entity to a provider record needs ASAO canonical state, so this tool names provider records
directly with --provider-target-id. ASAO Edge routing: reaching an Odoo the cloud cannot dial depends
on ASAO authority provenance and durable cloud-side command state, and a local imitation of it would
be a governance claim this product does not back. Managed Odoo provisioning: creating and running
ERP infrastructure is ASAO product functionality. This tool connects to an Odoo you already have.

GETTING STARTED. Record a profile with "aerp profile set", check it with "aerp doctor --output json",
discover operations with "aerp catalog list --output json", then run one with "aerp execute".

CONFIGURATION AND CREDENTIALS. A profile is CONFIGURATION — endpoint, database, login, and a
credential REFERENCE. It never contains the password. Material is resolved at use time from
AERP_CREDENTIAL_<REFERENCE>, OS keyring service asao-odoo, or from a credential file named by
AERP_CREDENTIAL_FILE, which must be readable by its owner alone. No command prints credential material, ever.

MACHINE CONTRACT. Every command supports --output json: stdout carries exactly one JSON document,
success and failure alike, and every diagnostic goes to stderr. "aerp commands list --output json"
returns this entire command tree in one document, which is the intended entry point for an agent
that has never seen this tool. Errors carry a stable machine code, a category and an exit code;
branch on "code".`,
	}
	root.Children = []*clicore.Command{
		commandsGroup(root),
		versionCommand(config),
		trustCommand(),
		doctorCommand(config.name),
		profileGroup(config.name),
		credentialGroup(config.name),
		catalogGroup(config),
		provenanceCommand(),
		planCommand(config),
		executeCommand(config),
		specGroup(config),
		reviewGroup(config),
		controlsGroup(config),
		routinesGroup(config),
		updateCommand(config),
		mcpGroup(config),
		completionGroup(root),
	}
	if odooFlavour(config.name) {
		// Generic reads of any model sit with the raw call: the odoo-cli product offers them, the
		// historical aerp name keeps the catalog as its only contract.
		root.Children = append(root.Children, callCommand(config), adminGroup(config), exploreGroup(config), reportGroup(config))
		root.ErrorAdvisor = reportAdvisor(config.name)
	}
	mountOperationCommands(root, config)
	openAgentSurface(root)
	rewriteProgramReferences(root, config.name)
	declareBoundaries(root)
	clicore.Prepare(root)
	return root
}

// rewriteProgramReferences keeps command metadata honest for a renamed executable while leaving
// product identifiers such as AERP_CREDENTIAL_ and aerp.profiles/v1 untouched.
func rewriteProgramReferences(root *clicore.Command, program string) {
	if program == Program {
		return
	}
	token := regexp.MustCompile("(^|[^A-Za-z0-9_])aerp($|[^A-Za-z0-9_.])")
	rewrite := func(value string) string {
		return token.ReplaceAllStringFunc(value, func(match string) string {
			index := strings.Index(match, Program)
			if index < 0 {
				return match
			}
			return match[:index] + program + match[index+len(Program):]
		})
	}
	var walk func(*clicore.Command)
	walk = func(command *clicore.Command) {
		command.Summary = rewrite(command.Summary)
		command.Description = rewrite(command.Description)
		for index := range command.Examples {
			command.Examples[index] = rewrite(command.Examples[index])
		}
		for index := range command.Flags {
			command.Flags[index].Description = rewrite(command.Flags[index].Description)
		}
		for _, child := range command.Children {
			walk(child)
		}
	}
	walk(root)
}

// declareBoundaries is the fail-closed assembly check. Unlike the governed tree's counterpart it
// assigns nothing: a standalone leaf that forgot to call local() is a PANIC at construction, because
// there is no safe default to give it — every leaf in this tree runs under the local operator's
// authority and a leaf that did not say so is a leaf nobody classified.
func declareBoundaries(command *clicore.Command) {
	if !command.IsGroup() {
		if command.ExecutionBoundary != clicore.ExecutionBoundaryLocalOperator {
			panic("standalone: leaf " + command.Name + " did not declare the LOCAL_OPERATOR execution boundary")
		}
		if command.Authority == nil {
			panic("standalone: leaf " + command.Name + " declares no authority posture")
		}
	}
	for _, child := range command.Children {
		declareBoundaries(child)
	}
}

func versionCommand(config programConfig) *clicore.Command {
	return local(&clicore.Command{
		Name:       "version",
		Summary:    "Report this tool's version, the machine-contract version, and that it is the standalone product.",
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Examples:   []string{config.name + " version --output json"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			compatibility := direct.CatalogProvenance().CompatibilityIdentity
			return map[string]interface{}{
				"program": config.name,
				// product is the first field an agent should branch on. Both binaries report a
				// version and an apiVersion; only this one says which product it is.
				// STANDALONE_AERP is the intentional product identity for both historical aerp
				// and public odoo binaries, ensuring canonical semantic compatibility across both entry points.
				"product":                  "STANDALONE_AERP",
				"governance":               "NONE",
				"releaseVersion":           releaseVersion,
				"cliVersion":               Version,
				"apiVersion":               clicore.APIVersion,
				"configSchema":             localprofile.SchemaVersion,
				"catalogCount":             compatibility.CatalogCount,
				"catalogIdentity":          compatibility.CatalogIdentity,
				"executionBuildIdentity":   compatibility.ExecutionBuildIdentity,
				"semanticContractIdentity": compatibility.SemanticContractIdentity,
				"buildCommit":              buildCommit,
				"buildDate":                buildDate,
				"os":                       runtime.GOOS,
				"arch":                     runtime.GOARCH,
				"governedCounterpart": "asao — the ASAO control plane, which reaches an ERP only after " +
					"authenticating an actor, authorizing a capability and recording the decision",
			}, nil
		},
	})
}

func commandsGroup(root *clicore.Command) *clicore.Command {
	list := local(&clicore.Command{
		Name:    "list",
		Summary: "Publish the whole command tree as one discovery document.",
		Description: `Returns every command, its flags, its accepted values, its declared side effect, and the
exit-code contract — in one document. This is the intended entry point for an agent that has never
seen this tool: it needs no source access and no second call to become able to use it.

Every leaf publishes executionBoundary LOCAL_OPERATOR and authentication NONE, and no leaf publishes
a requiredCapability, because this product enforces none. That absence is the machine-readable form
of this tool's trust boundary; "aerp trust --output json" states it in prose a caller can log.`,
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Examples:   []string{"aerp commands list --output json"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			return map[string]interface{}{
				"program":    root.Program,
				"product":    "STANDALONE_AERP",
				"cliVersion": Version,
				"trust":      TrustBoundary(),
				"tree":       clicore.Specify(root, true),
				"exitCodes":  clicore.ExitCodes(),
				"outputContract": map[string]interface{}{
					"jsonMode":    "stdout carries exactly one JSON document; stderr carries diagnostics only",
					"envelope":    []string{"apiVersion", "kind", "command", "status", "data", "error"},
					"kinds":       []string{clicore.KindResult, clicore.KindError},
					"errorFields": []string{"code", "category", "message", "exitCode", "resource", "context", "retryable", "remediation"},
				},
			}, nil
		},
	})
	return &clicore.Command{
		Name:     "commands",
		Summary:  "Discover this tool's own command surface.",
		Children: []*clicore.Command{list},
	}
}
