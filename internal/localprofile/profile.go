// Package localprofile is the standalone AERP tool's ENTIRE notion of "which Odoo, with which
// credential" — and it is deliberately nothing more than that.
//
// WHY IT EXISTS AT ALL. The governed `asao` product answers that question from canonical state: an
// Organization owns a ProviderConnection, whose endpoint, database and credential reference are
// records the platform is the authority for, and which a privileged runtime resolves after
// authenticating an Actor. A standalone operator has none of that and should not have to invent it:
// requiring somebody to create an Organization, a ProviderConnection and a CapabilityGrant before
// they can list employees out of the Odoo running on their own laptop would be governance theatre,
// because there is no second party for any of it to protect.
//
// WHAT IT IS NOT. It is not a second provider registry and it is not canonical state. A profile has
// no organization, no family/engine assertion, no status, no route, no scope and no lifecycle; it is
// a local config record naming one directly-reachable Odoo, and its only consumer is the standalone
// composition seam (aerpbridge.NewStandaloneExecutor). Nothing in the governed product reads this
// package, and nothing in this package can produce a ProviderConnection.
//
// CREDENTIAL SEPARATION IS THE LOAD-BEARING RULE HERE. The profile file holds CONFIGURATION —
// endpoint, database, login, and a credential REFERENCE. It never holds the password. Material is
// resolved at use time from the environment or from a separate, permission-checked credential file,
// and no function in this package returns material into anything that gets rendered: Profile has no
// password field for a `profile show` to leak, and the resolved secret exists only as a local
// variable on its way into the RPC client.
package localprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// FileVariable overrides where profiles are stored. Set it to keep profiles beside a project,
	// in a container image, or in a test.
	FileVariable = "AERP_PROFILE_FILE"
	// SelectorVariable names the profile to use when no --profile was given. It sits BELOW the
	// explicit flag and ABOVE the file's own recorded default, so a shell can select a profile for a
	// series of commands without editing configuration.
	SelectorVariable = "AERP_PROFILE"
	// CredentialFileVariable names a JSON file mapping credential reference -> material. It is
	// checked for permissions before it is read, because unlike the profile file it holds secrets.
	CredentialFileVariable = "AERP_CREDENTIAL_FILE"
	// CredentialEnvPrefix is the per-reference environment variable prefix: credential reference
	// "lab-a" is read from AERP_CREDENTIAL_LAB_A. It wins over the credential file, so a single
	// override needs no file edit — and so a CI runner can inject a secret with no file at all.
	CredentialEnvPrefix = "AERP_CREDENTIAL_"

	// SchemaVersion identifies the profile document. It is written on save and checked on load, so a
	// future incompatible layout is a clear refusal rather than a silent misread.
	SchemaVersion = "aerp.profiles/v1"
)

var (
	// ErrNoProfileFile reports that no profile store exists yet. It is a CONFIGURATION answer, not a
	// fault: a fresh install legitimately has none, and the remediation is to create one.
	ErrNoProfileFile = errors.New("local profile: no profile file exists")
	// ErrProfileNotFound reports a named profile the store does not define.
	ErrProfileNotFound = errors.New("local profile: no such profile")
	// ErrProfileAmbiguous refuses to GUESS which of several profiles was meant. There is no
	// "first profile" rule and no alphabetical winner: an unselected call against a store holding
	// two Odoos is refused, naming both, because picking one would eventually pick production.
	ErrProfileAmbiguous = errors.New("local profile: more than one profile exists and none was selected")
	// ErrProfileInvalid reports a profile file that cannot be read as one, or a profile record
	// missing something without which no Odoo is identified.
	ErrProfileInvalid = errors.New("local profile: profile is not usable")
	// ErrCredentialUnavailable reports a credential reference that resolved to nothing. Its message
	// names the reference and the variable that would supply it, and never any material.
	ErrCredentialUnavailable = errors.New("local profile: provider credential is unavailable")
	// ErrCredentialFilePermissive refuses to read a credential file that is group- or world-readable.
	// Refusing is correct rather than merely tidy: the file holds a password that can do everything
	// the Odoo user can do, and reading it anyway would silently accept a posture the operator
	// probably did not intend.
	ErrCredentialFilePermissive = errors.New("local profile: credential file is readable by more than its owner")
)

// Profile is one locally-configured Odoo. Every field is CONFIGURATION; none is a secret.
type Profile struct {
	// Name is the local selector. It is never sent to the provider and confers no authority.
	Name string `json:"-"`
	// Endpoint is the Odoo base URL, e.g. http://127.0.0.1:8069.
	Endpoint string `json:"endpoint"`
	// Database is the Odoo database name. It is topology, not credential material — exactly as it
	// is on a governed ProviderConnection — so material can never redirect a call to another
	// database than this record names.
	Database string `json:"database"`
	// Login is the Odoo user this profile acts as. A username is not a secret and belongs with the
	// rest of the configuration; it may also be carried inside the resolved material, which wins.
	Login string `json:"login,omitempty"`
	// CredentialRef names WHERE the password comes from. It is an opaque local reference, resolved
	// at use time by ResolveCredential; it is not the password and is safe to print.
	CredentialRef string `json:"credentialRef"`
	// Protocol is how to speak to this Odoo: "jsonrpc" (empty means it too) or "json2", Odoo 19's
	// replacement for the external API Odoo is removing, whose credential is an API key.
	Protocol string `json:"protocol,omitempty"`
	// AgentDataAccess is how much of this Odoo's data an agent sees through mcp serve (DEC-2026-0178):
	// "operate", "act" or "full"; empty means act. A server's --data-access may lower it, never raise it.
	AgentDataAccess string `json:"agentDataAccess,omitempty"`
}

// Protocols are the values Profile.Protocol accepts.
var Protocols = []string{"jsonrpc", "json2"}

// DataAccessLevels are the values Profile.AgentDataAccess accepts, from the least seen to the most.
var DataAccessLevels = []string{"operate", "act", "full"}

// EffectiveProtocol is the protocol a profile speaks: jsonrpc unless it says otherwise.
func (p Profile) EffectiveProtocol() string {
	if strings.TrimSpace(p.Protocol) == "" {
		return "jsonrpc"
	}
	return p.Protocol
}

// Store is the on-disk profile document.
type Store struct {
	Version string `json:"version"`
	// Default is the profile used when nothing else selects one. It is EXPLICIT: a store with two
	// profiles and no default refuses an unselected call rather than choosing.
	Default string `json:"defaultProfile,omitempty"`
	// Profiles is keyed by profile name.
	Profiles map[string]Profile `json:"profiles"`

	// path is where this store was loaded from, carried so diagnostics can report it.
	path string
}

// Path reports where this store lives, for diagnostics. Never a secret.
func (s *Store) Path() string { return s.path }

// Names lists every profile, deterministically.
func (s *Store) Names() []string {
	names := make([]string, 0, len(s.Profiles))
	for name := range s.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DefaultPath is where profiles live when FileVariable is unset. It follows the XDG convention and
// falls back to ~/.config, so the location is deterministic on any machine rather than dependent on
// the current working directory — a config file found by CWD is a config file an agent can be
// tricked into loading by being run somewhere else.
func DefaultPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(FileVariable)); override != "" {
		return override, nil
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "aerp", "profiles.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: no home directory, and %s is unset", ErrNoProfileFile, FileVariable)
	}
	return filepath.Join(home, ".config", "aerp", "profiles.json"), nil
}

// Load reads the store. A missing file is ErrNoProfileFile — a state, not a failure.
func Load() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNoProfileFile, path)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s could not be read", ErrProfileInvalid, path)
	}
	store := &Store{path: path}
	if err := json.Unmarshal(raw, store); err != nil {
		return nil, fmt.Errorf("%w: %s is not a JSON profile document", ErrProfileInvalid, path)
	}
	if store.Version != "" && store.Version != SchemaVersion {
		return nil, fmt.Errorf("%w: %s declares version %q, this build understands %q",
			ErrProfileInvalid, path, store.Version, SchemaVersion)
	}
	if store.Profiles == nil {
		store.Profiles = map[string]Profile{}
	}
	for name, profile := range store.Profiles {
		profile.Name = name
		store.Profiles[name] = profile
	}
	return store, nil
}

// LoadOrEmpty is Load, treating "no file yet" as an empty store. It is what `profile set` uses, so
// creating the first profile needs no separate init step.
func LoadOrEmpty() (*Store, error) {
	store, err := Load()
	if errors.Is(err, ErrNoProfileFile) {
		path, pathErr := DefaultPath()
		if pathErr != nil {
			return nil, pathErr
		}
		return &Store{Version: SchemaVersion, Profiles: map[string]Profile{}, path: path}, nil
	}
	return store, err
}

// Save writes the store back with owner-only permissions on both the file and the directory it
// creates. The document contains no secrets — that is enforced by Profile having no field for one —
// but it does describe which hosts this machine talks to, which is not something to publish either.
func (s *Store) Save() error {
	if s.path == "" {
		path, err := DefaultPath()
		if err != nil {
			return err
		}
		s.path = path
	}
	s.Version = SchemaVersion
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("%w: could not create %s", ErrProfileInvalid, filepath.Dir(s.path))
	}
	encoded, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: could not encode profile document", ErrProfileInvalid)
	}
	// O_CREATE with 0600 rather than a write-then-chmod: a chmod afterwards leaves a window in which
	// the file exists with the process umask's permissions.
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("%w: could not write %s", ErrProfileInvalid, s.path)
	}
	defer file.Close()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("%w: could not write %s", ErrProfileInvalid, s.path)
	}
	return nil
}

// Put records or replaces one profile.
func (s *Store) Put(profile Profile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if s.Profiles == nil {
		s.Profiles = map[string]Profile{}
	}
	s.Profiles[profile.Name] = profile
	return nil
}

// Remove deletes one profile, and clears the recorded default if it named that profile — leaving a
// default pointing at a profile that no longer exists would turn the next unselected call into a
// confusing not-found instead of an honest "nothing is selected".
func (s *Store) Remove(name string) error {
	if _, ok := s.Profiles[name]; !ok {
		return fmt.Errorf("%w: %q (known: %s)", ErrProfileNotFound, name, strings.Join(s.Names(), ", "))
	}
	delete(s.Profiles, name)
	if s.Default == name {
		s.Default = ""
	}
	return nil
}

// Validate refuses a profile that does not identify one Odoo.
func (p Profile) Validate() error {
	switch {
	case strings.TrimSpace(p.Name) == "":
		return fmt.Errorf("%w: a profile needs a name", ErrProfileInvalid)
	case strings.TrimSpace(p.Endpoint) == "":
		return fmt.Errorf("%w: profile %q records no endpoint", ErrProfileInvalid, p.Name)
	case strings.TrimSpace(p.Database) == "":
		return fmt.Errorf("%w: profile %q records no odoo database", ErrProfileInvalid, p.Name)
	case strings.TrimSpace(p.CredentialRef) == "":
		return fmt.Errorf("%w: profile %q records no credential reference", ErrProfileInvalid, p.Name)
	case p.Protocol != "" && p.Protocol != Protocols[0] && p.Protocol != Protocols[1]:
		return fmt.Errorf("%w: profile %q records protocol %q (want jsonrpc or json2)", ErrProfileInvalid, p.Name, p.Protocol)
	case p.AgentDataAccess != "" && p.AgentDataAccess != DataAccessLevels[0] && p.AgentDataAccess != DataAccessLevels[1] && p.AgentDataAccess != DataAccessLevels[2]:
		return fmt.Errorf("%w: profile %q records agentDataAccess %q (want operate, act or full)", ErrProfileInvalid, p.Name, p.AgentDataAccess)
	}
	return nil
}

// Selection records WHY a particular profile was chosen. It is reported on diagnostics and on every
// execution outcome's provider route, so a transcript can never leave "which Odoo did this hit, and
// who decided" ambiguous.
type Selection struct {
	Profile Profile
	// Source is one of: FLAG, ENVIRONMENT, FILE_DEFAULT, SOLE_PROFILE.
	Source string
}

// Selection sources, in precedence order.
const (
	SourceFlag        = "FLAG"
	SourceEnvironment = "ENVIRONMENT"
	SourceFileDefault = "FILE_DEFAULT"
	SourceSoleProfile = "SOLE_PROFILE"
)

// Resolve decides which profile a call runs against, deterministically, in ONE documented order:
//
//  1. --profile <name>          the explicit selector wins over everything
//  2. AERP_PROFILE              a shell/CI selection for a series of commands
//  3. defaultProfile            an explicit default the operator recorded in the file
//  4. the sole profile          unambiguous only because there is exactly one
//     otherwise                    REFUSED as ambiguous, naming every candidate
//
// There is deliberately NO fallback to process-global ODOO_URL/ODOO_DB-style variables at any step.
// The legacy erp-adapter (services/erp-adapter) is configured that way, and inheriting it here would
// mean a call with a valid but unselected profile store could silently execute against some other
// Odoo entirely — the exact ambiguity §24 exists to forbid.
func (s *Store) Resolve(explicit string) (Selection, error) {
	if name := strings.TrimSpace(explicit); name != "" {
		profile, err := s.lookup(name)
		return Selection{Profile: profile, Source: SourceFlag}, err
	}
	if name := strings.TrimSpace(os.Getenv(SelectorVariable)); name != "" {
		profile, err := s.lookup(name)
		if err != nil {
			return Selection{}, fmt.Errorf("%w (selected by %s)", err, SelectorVariable)
		}
		return Selection{Profile: profile, Source: SourceEnvironment}, nil
	}
	if name := strings.TrimSpace(s.Default); name != "" {
		profile, err := s.lookup(name)
		if err != nil {
			return Selection{}, fmt.Errorf("%w (named as the file's defaultProfile)", err)
		}
		return Selection{Profile: profile, Source: SourceFileDefault}, nil
	}
	names := s.Names()
	switch len(names) {
	case 0:
		return Selection{}, fmt.Errorf("%w: %s defines no profiles", ErrProfileNotFound, s.path)
	case 1:
		profile, err := s.lookup(names[0])
		return Selection{Profile: profile, Source: SourceSoleProfile}, err
	default:
		return Selection{}, fmt.Errorf("%w: %s defines %d profiles (%s)",
			ErrProfileAmbiguous, s.path, len(names), strings.Join(names, ", "))
	}
}

func (s *Store) lookup(name string) (Profile, error) {
	profile, ok := s.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("%w: %q (known: %s)", ErrProfileNotFound, name, strings.Join(s.Names(), ", "))
	}
	profile.Name = name
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}
