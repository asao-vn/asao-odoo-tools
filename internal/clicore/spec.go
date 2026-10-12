package clicore

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// APIVersion is the stable identity of the CLI's machine contract. Every JSON document the CLI
// emits carries it. A breaking change to the envelope, the error shape or the discovery document
// must change this string; additive fields must not.
const APIVersion = "cli.asao/v1"

// CLIVersion is the CLI's own semantic version — distinct from APIVersion, the machine contract
// version above. It is defined here, not in internal/commands (where it is re-exported as
// commands.Version for backward compatibility), because packages/cli/internal/authority must record
// it on every audit row it writes and cannot import internal/commands without creating an import
// cycle: commands.Root wires authority.NewGuard(env).Check onto its root command.
const CLIVersion = "1.0.0"

// ValueKind is the type of a flag value. It is published in the discovery document so a caller
// knows what to send without trial and error.
type ValueKind string

const (
	// KindString — a plain string.
	KindString ValueKind = "string"
	// KindInt — a base-10 integer.
	KindInt ValueKind = "integer"
	// KindBool — true/false. Passing the flag with no value means true.
	KindBool ValueKind = "boolean"
	// KindNumber — a floating-point number.
	KindNumber ValueKind = "number"
	// KindJSON — a JSON document, parsed by the CLI. Passed as a single argument; the CLI parses
	// it exactly once, so a caller must NOT pre-encode it as a JSON string containing JSON. The
	// historical CLI accepted double-encoded payloads and silently treated them as strings.
	KindJSON ValueKind = "json"
	// KindKeyValue — repeatable name=value pair, collected into a map. This is the ergonomic
	// alternative to hand-building a JSON object for simple field sets.
	KindKeyValue ValueKind = "keyvalue"
)

// Flag is one named input of a command.
type Flag struct {
	Name        string    `json:"name"`
	Kind        ValueKind `json:"kind"`
	Description string    `json:"description"`
	Required    bool      `json:"required,omitempty"`
	Default     string    `json:"default,omitempty"`
	// Enum is the closed set of accepted values, when there is one. Published so a caller never
	// has to discover a legal value by sending an illegal one.
	Enum []string `json:"enum,omitempty"`
	// Repeatable marks a flag that may appear more than once. KindKeyValue flags always are.
	Repeatable bool `json:"repeatable,omitempty"`
	// Sensitive marks a value that must never be echoed back in output or errors.
	Sensitive bool `json:"sensitive,omitempty"`
	// PersonOnly marks an input an AI agent may not supply through the tool protocol: its value must
	// come from the person's own configuration (e.g. `asao open --web`, the origin a link points at).
	// The tool projection leaves it out of the input schema and refuses it as an argument.
	PersonOnly bool `json:"personOnly,omitempty"`
	// ClientFile marks a flag whose value, as typed, is a path on the CALLER's machine. The client
	// (internal/dispatch) reads that file and sends its content in place of the path, so a command
	// executed by the remote authority runtime receives the text and never a path it could open on
	// the server. See clientfile.go for the limits and why the runtime never expands it.
	ClientFile *ClientFile `json:"clientFile,omitempty"`
}

func (f Flag) repeatable() bool { return f.Repeatable || f.Kind == KindKeyValue }

// SideEffect declares what running a command does, so an agent can tell a safe probe from a
// business mutation before running it.
type SideEffect string

const (
	// EffectNone — pure local computation. No network, no database, no provider.
	EffectNone SideEffect = "NONE"
	// EffectRead — reads canonical state and/or a provider, mutating nothing.
	EffectRead SideEffect = "READ"
	// EffectWriteCanonical — writes ASAO canonical state.
	EffectWriteCanonical SideEffect = "WRITE_CANONICAL"
	// EffectWriteProvider — causes a mutation in an external provider.
	EffectWriteProvider SideEffect = "WRITE_PROVIDER"
	// EffectWriteLocalConfig — writes configuration on the machine the command runs on, and nothing
	// else. No canonical state, no provider, no network.
	//
	// It is distinct from EffectWriteCanonical because that value means "writes ASAO canonical
	// state", which the standalone AERP tool (P5) has none of: `aerp profile set` writes a local
	// file. Reusing WRITE_CANONICAL there would have told a machine reader — in the one document it
	// trusts — that a local config edit touches the platform's system of record. It is distinct from
	// EffectNone for the opposite reason: something on disk really does change, so a caller reasoning
	// about idempotency or about what to snapshot before running a command needs to know.
	EffectWriteLocalConfig SideEffect = "WRITE_LOCAL_CONFIG"
	// EffectWriteLocalHost — acts on the machine the command runs on beyond configuration: writes
	// files there (a backup snapshot) and drives its local container runtime (a dump inside a database
	// container, a throwaway restore container). It contacts no ASAO service and no provider API.
	// Only the `asao server` family declares it (DEC-2026-0159), through ClientLocalHost.
	EffectWriteLocalHost SideEffect = "WRITE_LOCAL_HOST"
)

// ExecutionBoundary declares where a leaf is permitted to execute. It is
// command-registration metadata, not client advice: the privileged runtime
// rejects CLIENT_LOCAL commands from the remote Execute contract using this
// same field and discovery projects it verbatim.
type ExecutionBoundary string

const (
	// ExecutionBoundaryClientLocal is a compiled, non-authoritative operation
	// such as help or catalog discovery. It never opens canonical repositories.
	ExecutionBoundaryClientLocal ExecutionBoundary = "CLIENT_LOCAL"
	// ExecutionBoundaryPrivilegedRuntime is an authority-bearing operation. In
	// production it executes only after remote authentication, authorization,
	// and audit in the privileged authority runtime.
	ExecutionBoundaryPrivilegedRuntime ExecutionBoundary = "PRIVILEGED_RUNTIME"
	// ExecutionBoundaryLocalOperator is P5's standalone boundary: the operation executes entirely in
	// the local process, with the authority of whatever provider credential the local operator
	// configured, and it passes through NO ASAO authority runtime because there is none in this
	// product.
	//
	// It is a THIRD value rather than a reuse of CLIENT_LOCAL deliberately. CLIENT_LOCAL means
	// "compiled, non-authoritative, touches no provider" and the governed remote Execute service
	// relies on exactly that meaning to decide what it may refuse; a standalone `aerp execute` DOES
	// mutate a provider, so calling it CLIENT_LOCAL would be false in the one document a machine
	// reader trusts. No governed tree leaf ever carries this value — the governed assembly
	// (internal/commands' declareExecutionBoundaries) can only produce the other two — so the
	// privileged runtime's PRIVILEGED_RUNTIME-only admission check is unaffected by its existence.
	ExecutionBoundaryLocalOperator ExecutionBoundary = "LOCAL_OPERATOR"
)

// AgentAccess declares whether, and how, an AI agent that reaches ASAO through the tool protocol
// (`asao mcp serve`) may invoke a leaf. It is a SURFACE decision, never an authorization: every call
// an agent makes is still authenticated and authorized by the privileged runtime against the
// person's own CapabilityGrants, exactly like the same command typed in a terminal. What it adds is
// the structural rule that some leaves are never offered to an agent at all, whatever the person
// holds — infrastructure, governance, credentials and legal attestations stay in the CLI, so a
// misconfigured grant can never hand an agent the platform's control plane.
//
// The governed tree assigns it at assembly from one declarative policy
// (internal/commands/agent_surface.go). An unassigned leaf is NONE: a newly registered command is
// CLI-only until somebody decides otherwise, which is the safe direction.
type AgentAccess string

const (
	// AgentAccessNone — CLI only. The tool projection never lists it and the MCP server refuses it.
	AgentAccessNone AgentAccess = "NONE"
	// AgentAccessRead — offered to an agent; the leaf reads and mutates nothing.
	AgentAccessRead AgentAccess = "READ"
	// AgentAccessWrite — offered to an agent; the leaf mutates. The agent host's own per-call
	// approval is the person's deliberate action, so the server supplies --operator-authority itself.
	AgentAccessWrite AgentAccess = "WRITE"
	// AgentAccessConfirm — offered to an agent, but the server asks the PERSON (MCP elicitation)
	// before relaying the call: public, financial, irreversible or health-record effects. When the
	// agent host cannot ask, the server refuses and returns the exact CLI line for the person to run.
	AgentAccessConfirm AgentAccess = "CONFIRM"
)

// HumanStep declares that a leaf's purpose is completed by a person on a dedicated ASAO page in a
// browser, after the command itself has run: `auth login` prints the /connect link, `social channel
// connect` returns the network's consent page. Published so a caller knows, before running it, that
// the result is a link to hand to a person rather than a finished outcome.
type HumanStep string

const (
	// HumanStepBrowser — the result carries a link a person must open to finish.
	HumanStepBrowser HumanStep = "BROWSER"
)

// AuthenticationRequirement declares whether a command may run with no actor credential presented at
// all. See Authority and packages/cli/internal/authority, which is the only thing that reads it.
type AuthenticationRequirement string

const (
	// AuthenticationRequired — an authenticated Actor is mandatory. This is the default posture for
	// anything that is not pure discovery/diagnostics or the one unauthenticated bootstrap command.
	AuthenticationRequired AuthenticationRequirement = "REQUIRED"
	// AuthenticationNone — no credential is required or consulted. Reserved for commands that must
	// stay reachable before any actor exists at all (help, `commands list`, `doctor`,
	// `capability boundary`, `auth actor register`) — an agent that cannot yet authenticate must
	// still be able to discover HOW to.
	AuthenticationNone AuthenticationRequirement = "NONE"
)

// Authority declares, for exactly one leaf command, what packages/cli/internal/authority must check
// before that command's Run executes. clicore itself never evaluates this — it only carries and
// publishes it (see CommandSpec/Specify below); the single evaluator lives in
// packages/cli/internal/authority precisely so this package never imports runtimeenv or a bridge
// package (see run.go's Guard doc comment for the full reasoning).
//
// Every leaf command in the real tree (internal/commands) declares a non-nil Authority — a tree test
// (commands_test.go's TestTreeInvariants) enforces this, because a command that can ship with no
// declared authority is a command that can ship ungoverned.
type Authority struct {
	// Authentication is REQUIRED or NONE. See AuthenticationRequirement's own doc comment.
	Authentication AuthenticationRequirement
	// Capability is the canonical capability key this command requires. Empty when authentication
	// alone governs the command (e.g. `org create`, `auth whoami`) or when CapabilityFrom resolves
	// it per-invocation instead. Exactly one of Capability / CapabilityFrom is ever set on a real
	// command — never both, never neither on a capability-gated one.
	Capability string
	// CapabilityFrom resolves the required capability from the invocation, for a command whose
	// governing capability is not fixed at authoring time: `aerp plan`/`aerp execute` read it from
	// the AERP operation catalog via --operation, and `capability show`/`capability authorize`
	// require no capability at all when --subject names the authenticated actor itself
	// (self-inspection) and governance.grant.administer otherwise. A CapabilityFrom closure may read
	// inv.ActorAsaoUserID — set by the guard immediately after authentication succeeds, before
	// CapabilityFrom is ever called — to make exactly that comparison without needing a
	// context.Context parameter on this function type.
	CapabilityFrom func(inv *Invocation) (string, error)
	// CapabilityFromFlag names the flag CapabilityFrom reads its answer from, published on the
	// discovery document as capabilityResolvedFrom so a caller can tell WHICH input decides the
	// required capability without reading source. Meaningful only when CapabilityFrom is set.
	CapabilityFromFlag string
	// ScopeKind is "organization" (the only kind V1 exercises) or "none" for a command with no
	// organization to scope authorization to (org create — there is no organization yet;
	// capability bootstrap — its own bounded rule stands in for a capability check; auth commands
	// that govern only the caller's own actor, never another organization's state).
	ScopeKind string
	// OrganizationFlag names the flag carrying the organization id, for a command whose organization
	// flag is not named "organization". Defaults to "organization" when ScopeKind == "organization"
	// and this is left empty. Meaningless (and left empty) when OrganizationResolvedAtRuntime is set
	// — such a command has no organization FLAG at all, by definition.
	OrganizationFlag string
	// OrganizationResolvedAtRuntime marks a command whose organization is not carried by any flag —
	// it is resolved by the command's own Run body from other input (today: `org show`, which
	// resolves an Organization by --slug or --created-by, then must authorize against whichever one
	// it found). ScopeKind is still "organization" and Capability/CapabilityFrom is still declared
	// normally for such a command — both remain true statements about what governs it, published
	// honestly on the discovery document exactly like every other leaf — but the GUARD (this
	// package's own evaluation, see packages/cli/internal/authority) cannot evaluate steps 7/9/10
	// itself: there is no organization id to authorize against until the command has done its own
	// resolution. For such a command the guard stops after authentication (steps 1-5) and returns
	// ALLOWED; the command itself is REQUIRED to call authority.AuthorizeResolvedOrganization —
	// which runs the IDENTICAL decision-and-audit code the guard would otherwise have run itself, so
	// there remains exactly ONE authorization evaluator in this codebase, never a second, ad-hoc one
	// living inside the command (see AuthorizeResolvedOrganization's own doc comment; this is the fix
	// for WAVE 7 DEFECT A of the V1 production-authority-closure milestone, where `org show`'s
	// pre-existing inline call to governancebridge.Authorize was exactly such a second path — real,
	// but invisible to both the audit trail and to `capability boundary`'s enforcement inventory).
	//
	// A tree test (commands_test.go's TestTreeInvariants) fails the build if any leaf declares a
	// capability at organization scope, has no flag matching OrganizationFlag (or "organization" when
	// OrganizationFlag is empty), and does not set this — so this exact class of drift cannot recur
	// silently a second time.
	OrganizationResolvedAtRuntime bool
	// Destructive marks a command family capable of an effect that cannot be undone by a later
	// ASAO-level call (today: exactly `aerp execute`, whose target operation may carry the AERP
	// catalog's DESTRUCTIVE_GUARDED strategy). It is a coarse, command-level signal, not a
	// per-invocation one — `aerp execute --operation people.employee.list` is not actually
	// destructive despite the command family being marked so, because which specific operation is
	// named is data the discovery document cannot see ahead of the call. Read it as "this command
	// CAN be destructive depending on its arguments", never as "this exact call will be".
	Destructive bool
}

// Command is one leaf or group in the command tree. A group has Children and no Run; a leaf has
// Run and no Children.
type Command struct {
	// Program is the executable name this tree is invoked as, and is meaningful ONLY on the ROOT
	// command. Empty means DefaultProgram ("asao"). The governed control plane leaves it empty; the
	// standalone AERP tool (P5) sets it, so that no document or remediation this binary emits ever
	// tells a caller to run `asao`. See program.go for why this is a property of the tree rather
	// than of the process.
	Program     string
	Name        string
	Summary     string
	Description string
	Flags       []Flag
	Examples    []string
	SideEffect  SideEffect
	// ExecutionBoundary is set by command registration. A client-local leaf is
	// never accepted by the remote Execute service; all other leaves default to
	// the privileged runtime boundary at tree assembly, a fail-closed default
	// for newly registered commands.
	ExecutionBoundary ExecutionBoundary
	// ClientLocalDiagnostic is the narrow documented exception to the ordinary
	// CLIENT_LOCAL invariant. Only doctor sets it: in explicit direct/LAB mode it
	// may probe dependencies, while remote mode probes the authority health
	// endpoint without database/provider disclosure. It is never a mutating
	// operation and cannot be set on a privileged-runtime leaf.
	ClientLocalDiagnostic bool
	// ClientLocalPairing is the second narrow, explicitly-declared exception to the ordinary
	// CLIENT_LOCAL invariant, added by P10 for exactly the `auth login`/`auth logout` pair.
	//
	// WHY IT CANNOT BE AN ORDINARY CLIENT_LOCAL LEAF. Those commands write this machine's own client
	// credential file, so their honest SideEffect is WRITE_LOCAL_CONFIG, and the invariant otherwise
	// requires NONE. WHY IT CANNOT BE A PRIVILEGED_RUNTIME LEAF EITHER: a pairing command's entire
	// job is to obtain a credential for THIS machine, which the privileged runtime cannot do on its
	// behalf and which by definition runs before any credential exists.
	//
	// WHAT IT PERMITS, AND ONLY THIS: SideEffect WRITE_LOCAL_CONFIG, and a network call to the
	// product API's pairing endpoints. It permits NO canonical repository access, NO provider access,
	// and NO mutation of anything but files on this machine. Authentication stays NONE — the point of
	// pairing is that the caller has no credential yet — and the runtime continues to refuse every
	// CLIENT_LOCAL leaf from the remote Execute contract, which is what keeps a pairing command from
	// being invokable against somebody else's runtime.
	ClientLocalPairing bool
	// ClientLocalServer is the third narrow CLIENT_LOCAL exception: a leaf that owns the process's
	// standard streams until its input closes. Only `asao mcp serve` sets it. The executable
	// (internal/dispatch) runs such a leaf itself instead of clicore.Run, because clicore.Run would
	// write a result envelope onto the very stream the protocol is using. Its own SideEffect stays
	// NONE: the server writes nothing itself, and every call it relays is an ordinary invocation of
	// another leaf, parsed, authorized and audited exactly as if it had been typed.
	ClientLocalServer bool
	// ClientLocalSettings is the fourth narrow CLIENT_LOCAL exception: a leaf that writes this CLI's
	// own settings file (`asao config ...`) and nothing else. SideEffect WRITE_LOCAL_CONFIG, no
	// network, no canonical state, no provider. It differs from ClientLocalPairing, which may also
	// call the product API's pairing endpoints.
	ClientLocalSettings bool
	// ClientLocalHost is the fifth narrow CLIENT_LOCAL exception: a leaf that operates THIS machine
	// (`asao server ...`, DEC-2026-0159): it inspects its disks and containers, writes backup snapshots
	// and drives its local container runtime. SideEffect READ, WRITE_LOCAL_HOST or WRITE_LOCAL_CONFIG
	// (the server configuration and its timers); never canonical state, a provider or an ASAO service.
	ClientLocalHost bool
	// AgentAccess is this leaf's tool-protocol exposure. See the AgentAccess type. Empty on a group
	// and on the standalone products' trees, which have no MCP projection.
	AgentAccess AgentAccess
	// AgentConfirmWhen narrows a WRITE leaf whose effect depends on an argument: the call needs the
	// person's confirmation only when one of these flags carries one of the listed values
	// (`social post create --type now` publishes; `--type draft` does not). Meaningful only with
	// AgentAccessWrite.
	AgentConfirmWhen map[string][]string
	// HumanStep is set on a leaf whose result is a link a person must open to finish. See HumanStep.
	HumanStep HumanStep
	// Run executes the command and returns the value that becomes the envelope's `data`. It must
	// return a *Error for anything it means to refuse.
	Run func(ctx context.Context, inv *Invocation) (interface{}, error)
	// Authority declares this leaf's authorization requirement. Nil is only valid on a group (which
	// has no Run to guard); every leaf must set it. See the Authority type's own doc comment.
	Authority *Authority
	// Guard is the ONE authorization evaluation seam clicore exposes, meaningful ONLY on the ROOT
	// command — see run.go's Guard type and Run's own doc comment for exactly where it runs and why.
	// A non-root Command may set it but Run never reads it there; commands.Root sets it exactly once,
	// on the tree's own root, to authority.NewGuard(env).Check.
	Guard Guard
	// ContextDefaulter fills in a flag the caller did not type from client-local convenience state,
	// BEFORE the Guard evaluates anything. Meaningful ONLY on the ROOT command.
	ContextDefaulter ContextDefaulter
	// ResultPersister records client-local state from a result the runtime has ALREADY returned
	// successfully. Meaningful ONLY on the ROOT command.
	ResultPersister ResultPersister
	// ClientContextWrite marks a privileged command whose successful RESULT updates client-local
	// state. The command itself decides nothing locally — the runtime authorizes and answers, and
	// the client then records what it was told. `asao org use` is the only user today.
	ClientContextWrite bool `json:"-"`
	// ContextualOrganization lets --organization be filled from the machine's active-organization
	// context when the caller did not type it. It is set only on commands where ambient state may
	// choose what you LOOK AT. It is deliberately absent from every command that hands out or
	// spends authority: ambient state may not choose who you grant a capability to.
	ContextualOrganization bool `json:"-"`
	// OutcomeRecorder is wired only on the privileged runtime root. It observes
	// the trusted post-Guard execution result; it is not callable by a command
	// or a client request and cannot participate in authorization decisions.
	OutcomeRecorder OutcomeRecorder

	// Operation is the semantic catalog operation id (e.g. people.employee.list) when this command is a generated projection.
	Operation string
	// TargetRule is the target requirement (NONE, REQUIRED, OPTIONAL) of the operation.
	TargetRule string
	// Readiness is the release readiness (CURRENT_VERIFIED, HISTORICAL_ONLY, UNVERIFIED) of the operation.
	Readiness string
	// Risk is the risk level (LOW, MEDIUM, HIGH, CRITICAL) of the operation.
	Risk string

	Children []*Command

	parent   *Command
	linkOnce sync.Once
}

// Path is the full command path, e.g. ["aerp","catalog","describe"].
func (c *Command) Path() []string {
	if c.parent == nil {
		if c.Name == "" {
			return nil
		}
		return []string{c.Name}
	}
	return append(c.parent.Path(), c.Name)
}

// PathString renders the path the way a caller types it.
func (c *Command) PathString() string { return strings.Join(c.Path(), " ") }

// IsGroup reports whether this command only groups others.
func (c *Command) IsGroup() bool { return len(c.Children) > 0 }

// link wires parent pointers and sorts children, so paths and help are deterministic.
func (c *Command) link() {
	c.linkOnce.Do(func() {
		sort.Slice(c.Children, func(i, j int) bool { return c.Children[i].Name < c.Children[j].Name })
		for _, child := range c.Children {
			child.parent = c
			child.link()
		}
	})
}

// Prepare links and freezes the command tree's structural metadata before a
// shared runtime serves concurrent requests. Parse also calls this idempotent
// method, so one-shot direct CLI composition retains its existing behavior.
func Prepare(root *Command) {
	if root != nil {
		root.link()
	}
}

func (c *Command) child(name string) *Command {
	for _, candidate := range c.Children {
		if candidate.Name == name {
			return candidate
		}
	}
	return nil
}

func (c *Command) childNames() []string {
	names := make([]string, 0, len(c.Children))
	for _, child := range c.Children {
		names = append(names, child.Name)
	}
	return names
}

// flag resolves a flag by name on this command, falling back to the global set.
func (c *Command) flag(name string) (Flag, bool) {
	for _, candidate := range c.Flags {
		if candidate.Name == name {
			return candidate, true
		}
	}
	for _, candidate := range GlobalFlags() {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return Flag{}, false
}

func (c *Command) flagNames() []string {
	names := make([]string, 0, len(c.Flags)+len(GlobalFlags()))
	for _, flag := range c.Flags {
		names = append(names, flag.Name)
	}
	for _, flag := range GlobalFlags() {
		names = append(names, flag.Name)
	}
	sort.Strings(names)
	return names
}

// GlobalFlags are accepted by every command. They are published on every command's help so a
// caller never has to know they are "global" to find them.
func GlobalFlags() []Flag {
	return []Flag{
		{
			Name: "output", Kind: KindString, Default: "text",
			Enum:        []string{"text", "json", "table", "yaml", "csv"},
			Description: "Output format. `json` emits exactly one JSON document on stdout and nothing else; all diagnostics go to stderr. `text` differs only for errors, which it renders for a human on stderr — a successful data result is printed as indented JSON in both modes. `csv` prints the records, or groups, of a READ operation as CSV; any other result prints as in `text`. `table` and `yaml` provide human-oriented layouts.",
		},
		{
			Name: "timeout", Kind: KindString,
			Description: "Maximum duration for command execution (e.g. 5s, 1m, 500ms). Bounded by per-operation limits.",
		},
		{
			Name: "help", Kind: KindBool,
			Description: "Describe this command instead of running it. Honours --output json, which emits the machine-readable command specification.",
		},
		{
			Name: "explain", Kind: KindBool,
			Description: "Explain the command contract, required capabilities, schema, and boundaries with zero execution side-effects and zero network mutation.",
		},
	}
}

// CommandSpec is the discovery projection of one command — what `--help --output json` and
// `asao commands list` publish. It is the document an agent reads instead of source.
type CommandSpec struct {
	Path        []string `json:"path"`
	Command     string   `json:"command"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	// Group is true for a command that only groups subcommands and cannot be run itself.
	Group      bool       `json:"group"`
	SideEffect SideEffect `json:"sideEffect,omitempty"`
	// RemoteAvailable, DirectAvailable and ProductionAvailable describe caller
	// composition availability, derived from trusted execution-boundary metadata
	// rather than a second client-side allow-list. CLIENT_LOCAL leaves remain
	// remote-mode available; ExecutionBoundary says they execute in the client.
	// Direct composition is explicitly LAB-only.
	RemoteAvailable     bool `json:"remoteAvailable,omitempty"`
	DirectAvailable     bool `json:"directAvailable,omitempty"`
	ProductionAvailable bool `json:"productionAvailable,omitempty"`
	// DirectProductionAllowed is deliberately explicit (rather than omitted):
	// every direct composition is LAB/development-only even where the command
	// itself is otherwise production available through the remote boundary.
	DirectProductionAllowed *bool             `json:"directProductionAllowed,omitempty"`
	ExecutionBoundary       ExecutionBoundary `json:"executionBoundary,omitempty"`
	ClientLocalDiagnostic   bool              `json:"clientLocalDiagnostic,omitempty"`
	// ClientLocalPairing projects Command.ClientLocalPairing so a machine reader can tell, from the
	// discovery document alone, that this leaf writes local configuration and talks to the product
	// API's pairing endpoints rather than executing in the privileged runtime.
	ClientLocalPairing bool `json:"clientLocalPairing,omitempty"`
	// ClientLocalServer and ClientLocalSettings project the third and fourth CLIENT_LOCAL exceptions.
	ClientLocalServer   bool `json:"clientLocalServer,omitempty"`
	ClientLocalSettings bool `json:"clientLocalSettings,omitempty"`
	// ClientLocalHost projects the fifth CLIENT_LOCAL exception: the leaf operates this machine.
	ClientLocalHost bool `json:"clientLocalHost,omitempty"`
	// AgentAccess and HumanStep publish the leaf's tool-protocol exposure and whether its result is a
	// link a person must open. agentAccess is published on every governed leaf (NONE included) so a
	// reader never has to interpret an absence.
	AgentAccess      AgentAccess         `json:"agentAccess,omitempty"`
	AgentConfirmWhen map[string][]string `json:"agentConfirmWhen,omitempty"`
	HumanStep        HumanStep           `json:"humanStep,omitempty"`
	// Authentication, RequiredCapability, CapabilityResolvedFrom, ScopeKind and Destructive are
	// additive discovery fields (V1 production-authority-closure milestone): together they publish
	// the REAL authority requirement of a leaf command — projected straight from its own Authority —
	// so an autonomous agent can tell, before ever running it, whether a credential is required,
	// which capability governs it (or which flag decides that dynamically), at what scope, and
	// whether it is destructive. APIVersion stays cli.asao/v1: these are additive fields on an
	// existing document, not a change to the envelope shape (see APIVersion's own doc comment on
	// what counts as breaking). Empty/omitted on a group, which has no Authority to project.
	Authentication         string `json:"authentication,omitempty"`
	RequiredCapability     string `json:"requiredCapability,omitempty"`
	CapabilityResolvedFrom string `json:"capabilityResolvedFrom,omitempty"`
	ScopeKind              string `json:"scopeKind,omitempty"`
	// OrganizationResolvedAtRuntime projects Authority.OrganizationResolvedAtRuntime verbatim — see
	// that field's own doc comment. Omitted (never rendered as explicit false) on every command that
	// does not set it: unlike Destructive, "absent" here is unambiguous — it means "this command's
	// organization, if any, comes from a flag", never "no Authority to project" (which omits
	// authentication/requiredCapability/scopeKind too, all together). WAVE 7 DEFECT A: an autonomous
	// caller reading this document must be able to see, without reading source, that `org show`
	// requires a capability at organization scope even though it publishes no --organization flag.
	OrganizationResolvedAtRuntime bool `json:"organizationResolvedAtRuntime,omitempty"`
	// Destructive is a *bool, not a bool, so it can be PUBLISHED EXPLICITLY (true or false) on every
	// leaf and OMITTED on every group. An autonomous agent must be able to read "is this operation
	// destructive" off the discovery document rather than infer it from absence — omitempty on a
	// plain bool would make a non-destructive leaf indistinguishable from a group that has no
	// Authority to project at all, and "absent" is a guess a caller has to interpret, never a fact it
	// can read. requiredCapability/capabilityResolvedFrom stay omitempty deliberately: "" there is
	// itself the meaningful answer ("no capability governs this / this leaf uses a fixed capability
	// key"), not an absence needing interpretation the way a missing destructive would be.
	Destructive *bool         `json:"destructive,omitempty"`
	Operation   string        `json:"operation,omitempty"`
	TargetRule  string        `json:"targetRule,omitempty"`
	Readiness   string        `json:"readiness,omitempty"`
	Risk        string        `json:"risk,omitempty"`
	Flags       []Flag        `json:"flags,omitempty"`
	Subcommands []string      `json:"subcommands,omitempty"`
	Examples    []string      `json:"examples,omitempty"`
	Children    []CommandSpec `json:"children,omitempty"`
}

// Specify projects one command. When deep is true the whole subtree is included, which is what
// makes `asao commands list --output json` a single self-contained discovery document.
func Specify(command *Command, deep bool) CommandSpec {
	spec := CommandSpec{
		Path:        command.Path(),
		Command:     qualify(command),
		Summary:     command.Summary,
		Description: command.Description,
		Group:       command.IsGroup(),
		SideEffect:  command.SideEffect,
		Examples:    command.Examples,
		Operation:   command.Operation,
		TargetRule:  command.TargetRule,
		Readiness:   command.Readiness,
		Risk:        command.Risk,
	}
	if !command.IsGroup() && command.ExecutionBoundary == ExecutionBoundaryLocalOperator {
		// A standalone leaf publishes its boundary and nothing about ASAO composition. remote/direct/
		// production availability and directProductionAllowed all describe how a GOVERNED caller may
		// compose the `asao` control plane; rendering them here would answer a question this product
		// does not have, and "directProductionAllowed: false" in particular would read as a claim
		// about a direct-vs-remote choice that standalone never offers.
		spec.ExecutionBoundary = command.ExecutionBoundary
		spec.Flags = append(append([]Flag{}, command.Flags...), GlobalFlags()...)
		sort.Slice(spec.Flags, func(i, j int) bool { return spec.Flags[i].Name < spec.Flags[j].Name })
		if authority := command.Authority; authority != nil {
			spec.Authentication = string(authority.Authentication)
			spec.ScopeKind = authority.ScopeKind
			destructive := authority.Destructive
			spec.Destructive = &destructive
		}
	} else if !command.IsGroup() {
		spec.ExecutionBoundary = command.ExecutionBoundary
		spec.ClientLocalDiagnostic = command.ClientLocalDiagnostic
		spec.ClientLocalPairing = command.ClientLocalPairing
		spec.ClientLocalServer = command.ClientLocalServer
		spec.ClientLocalSettings = command.ClientLocalSettings
		spec.ClientLocalHost = command.ClientLocalHost
		spec.AgentAccess = command.AgentAccess
		spec.AgentConfirmWhen = command.AgentConfirmWhen
		spec.HumanStep = command.HumanStep
		spec.DirectAvailable = true
		// A remote-mode caller can run both locally executed discovery leaves and
		// runtime-proxied authority leaves. executionBoundary, rather than this
		// availability bit, tells an autonomous caller which component executes.
		spec.RemoteAvailable = true
		directProductionAllowed := false
		spec.DirectProductionAllowed = &directProductionAllowed
		// CLIENT_LOCAL commands are compiled discovery/diagnostics and are
		// production-safe precisely because their tree invariant forbids
		// authentication and mutations. A runtime operation is production
		// available through remote composition only; direct remains LAB-only.
		spec.ProductionAvailable = command.ExecutionBoundary == ExecutionBoundaryClientLocal || command.ExecutionBoundary == ExecutionBoundaryPrivilegedRuntime
		spec.Flags = append(append([]Flag{}, command.Flags...), GlobalFlags()...)
		sort.Slice(spec.Flags, func(i, j int) bool { return spec.Flags[i].Name < spec.Flags[j].Name })
		if authority := command.Authority; authority != nil {
			spec.Authentication = string(authority.Authentication)
			switch {
			case authority.Capability != "":
				spec.RequiredCapability = authority.Capability
			case authority.CapabilityFrom != nil:
				spec.CapabilityResolvedFrom = authority.CapabilityFromFlag
			}
			spec.ScopeKind = authority.ScopeKind
			spec.OrganizationResolvedAtRuntime = authority.OrganizationResolvedAtRuntime
			destructive := authority.Destructive
			spec.Destructive = &destructive
		}
	}
	if command.IsGroup() {
		spec.Subcommands = command.childNames()
		if deep {
			for _, child := range command.Children {
				spec.Children = append(spec.Children, Specify(child, true))
			}
		}
	}
	return spec
}

// ExitCodeSpec documents one exit code in the discovery document.
type ExitCodeSpec struct {
	Code     int      `json:"code"`
	Category Category `json:"category"`
	Meaning  string   `json:"meaning"`
}

// ExitCodes is the published exit-code contract.
func ExitCodes() []ExitCodeSpec {
	return []ExitCodeSpec{
		{ExitOK, "", "The command did what it said."},
		{ExitInternal, CategoryInternal, "Unexpected fault inside the CLI."},
		{ExitUsage, CategoryUsage, "Malformed invocation: unknown command or flag, missing required flag, wrong value type. Nothing executed."},
		{ExitNotFound, CategoryNotFound, "A named resource does not exist, or is not visible in this organization."},
		{ExitConflict, CategoryConflict, "The request contradicts existing state, or is ambiguous."},
		{ExitDenied, CategoryDenied, "A well-formed request was refused by an authorization decision."},
		{ExitProvider, CategoryProvider, "The external provider was reached and reported a failure."},
		{ExitConfig, CategoryConfig, "Missing or malformed environment. Nothing executed."},
		{ExitUnavailable, CategoryUnavailable, "A dependency could not be reached at all."},
	}
}
