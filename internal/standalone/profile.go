package standalone

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

// PROFILE COMMANDS.
//
// A profile is how this product answers the one question the governed product answers from canonical
// state: which Odoo, with which credential. Everything here is local configuration management — no
// command in this file contacts a provider, and none of them can print a password, because the
// Profile type they render has no field that holds one.

// profileFlag is the explicit selector, and it is the TOP of the precedence order documented on
// localprofile.Store.Resolve. It is shared by every command that runs against a profile so the two
// execution commands and the diagnostic cannot disagree about how a profile is chosen.
var profileFlag = clicore.Flag{
	Name: "profile", Kind: clicore.KindString,
	Description: "Local profile to run against. OPTIONAL, and resolved in this exact order when omitted: " +
		localprofile.SelectorVariable + " in the environment, then the profile file's own defaultProfile, " +
		"then — only if there is exactly one — the sole profile. A store holding several profiles with no " +
		"default and no selection is REFUSED as ambiguous, naming every candidate, and no Odoo is contacted. " +
		"There is no fallback to any process-global ODOO_URL/ODOO_DB style variable at any step.",
}

func profileGroup(program string) *clicore.Command {
	return &clicore.Command{
		Name:    "profile",
		Summary: "Manage the local Odoo profiles this tool runs against.",
		Description: `A profile names one directly-reachable Odoo: its endpoint, its database, the login to act
as, and a REFERENCE to the credential. The password is never stored in the profile — it is resolved
at use time from an environment variable or from a separate credential file. See "aerp doctor" to
check that a profile resolves end to end without printing anything secret.

Profiles are ordinary local configuration. They are not ASAO ProviderConnections: they carry no
organization, no status, no route and no governance, and nothing in the ASAO platform reads them.`,
		Children: []*clicore.Command{
			profileListCommand(program),
			profileShowCommand(program),
			profileSetCommand(program),
			profileUseCommand(program),
			profileRemoveCommand(program),
		},
	}
}

func profileListCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:       "list",
		Summary:    "List every configured profile.",
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Examples:   []string{"aerp profile list --output json"},
		Run: func(_ context.Context, _ *clicore.Invocation) (interface{}, error) {
			store, err := localprofile.Load()
			if err != nil {
				return nil, classifyProfile(err, "", program)
			}
			items := []interface{}{}
			for _, name := range store.Names() {
				items = append(items, describeProfile(store.Profiles[name]))
			}
			return map[string]interface{}{
				"profileFile":    store.Path(),
				"defaultProfile": store.Default,
				"items":          items,
				"count":          len(items),
			}, nil
		},
	})
}

func profileShowCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:    "show",
		Summary: "Show one profile's configuration, and whether its credential resolves.",
		Description: `Reports the profile exactly as configured plus a BOOLEAN saying whether its credential
reference currently resolves. It never prints the credential itself, and there is no flag that makes
it: the value is fetched only on the path into the Odoo client.`,
		SideEffect: clicore.EffectNone,
		Authority:  localAuthority,
		Flags:      []clicore.Flag{profileFlag},
		Examples:   []string{"aerp profile show --profile lab-a --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			selection, err := resolveProfile(invocation, program)
			if err != nil {
				return nil, err
			}
			described := describeProfile(selection.Profile)
			described["selectedBy"] = selection.Source
			return described, nil
		},
	})
}

func profileSetCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:    "set",
		Summary: "Create or replace one local profile.",
		Description: `Writes the profile file with owner-only permissions, creating it if it does not exist.

--credential-ref names WHERE the password comes from; it is not the password and there is no flag
that accepts one. A secret passed on a command line ends up in the shell history and in the process
table of every other user on the machine, so this tool does not offer that.

--protocol json2 speaks Odoo 19's JSON-2 API instead of the JSON-RPC API Odoo is removing (Odoo
Online in 21.1, on-premise Odoo in 22). Its credential is an API key, created in Odoo under
Preferences > Account Security (from Odoo 20 on, with the scope RPC: a key of another scope is
refused as invalid), and supplied exactly as a password would be.

After setting a profile, supply its material and check it:

  export AERP_CREDENTIAL_<REFERENCE>='<password or API key>'
  aerp doctor --output json`,
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Flags: []clicore.Flag{
			{Name: "name", Kind: clicore.KindString, Required: true, Description: "Local profile name, e.g. `lab-a`. This is a local selector only; it is never sent to the provider."},
			{Name: "endpoint", Kind: clicore.KindString, Required: true, Description: "Odoo base URL, e.g. http://127.0.0.1:8069."},
			{Name: "database", Kind: clicore.KindString, Required: true, Description: "Odoo database name. Topology, not credential material."},
			{Name: "login", Kind: clicore.KindString, Description: "Odoo user to act as. Optional when the resolved credential material itself names a login."},
			{Name: "credential-ref", Kind: clicore.KindString, Description: "Reference the password is resolved by, at use time. Defaults to the profile name. NOT the password."},
			{Name: "protocol", Kind: clicore.KindString, Default: "jsonrpc", Enum: localprofile.Protocols, Description: "How to speak to Odoo: jsonrpc, or json2 (Odoo 19 and later; the credential is then an API key)."},
			{Name: "default", Kind: clicore.KindBool, Description: "Also record this profile as the file's default, used when no --profile and no " + localprofile.SelectorVariable + " selects one."},
		},
		Examples: []string{
			"aerp profile set --name lab-a --endpoint http://127.0.0.1:8069 --database aerp_lab_a --login admin --output json",
		},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := invocation.RequiredString("name")
			if err != nil {
				return nil, err
			}
			endpoint, err := invocation.RequiredString("endpoint")
			if err != nil {
				return nil, err
			}
			database, err := invocation.RequiredString("database")
			if err != nil {
				return nil, err
			}
			credentialRef := invocation.String("credential-ref")
			if credentialRef == "" {
				credentialRef = name
			}
			makeDefault, err := invocation.Bool("default")
			if err != nil {
				return nil, err
			}
			store, err := localprofile.LoadOrEmpty()
			if err != nil {
				return nil, classifyProfile(err, name, program)
			}
			protocol := invocation.String("protocol")
			if protocol == localprofile.Protocols[0] {
				protocol = "" // the default is not written, so older readers keep reading the file
			}
			profile := localprofile.Profile{
				Name: name, Endpoint: endpoint, Database: database,
				Login: invocation.String("login"), CredentialRef: credentialRef, Protocol: protocol,
			}
			if err := store.Put(profile); err != nil {
				return nil, classifyProfile(err, name, program)
			}
			// The default is recorded ONLY when the operator asked for it. Making the first profile
			// the default automatically would be convenient exactly once and wrong afterwards: adding
			// a second profile later would leave unselected calls silently continuing to the first,
			// which is the same "quietly picks one" hazard the ambiguity refusal exists to prevent —
			// just deferred to the moment a second Odoo appears. A store with one profile already
			// resolves unambiguously (SOLE_PROFILE) without any default at all.
			if makeDefault {
				store.Default = name
			}
			if err := store.Save(); err != nil {
				return nil, classifyProfile(err, name, program)
			}
			described := describeProfile(profile)
			described["profileFile"] = store.Path()
			described["defaultProfile"] = store.Default
			// The one thing a fresh profile is missing is its material, and saying so here — with the
			// exact variable name — is the difference between one more command and a support round trip.
			if !localprofile.CredentialAvailable(profile) {
				described["warnings"] = []string{
					"This profile's credential does not resolve yet. Set " +
						localprofile.EnvVariableFor(credentialRef) + ", store it with `" + program + " credential set --ref " + credentialRef + " --password-stdin`, or add the reference to the file named by " +
						localprofile.CredentialFileVariable + ", then run `" + program + " doctor --output json`.",
				}
			}
			return described, nil
		},
	})
}

func profileUseCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:    "use",
		Summary: "Record which profile is used when none is selected.",
		Description: `Sets the profile file's defaultProfile. It is the ONLY way this tool acquires a default:
there is no implicit "first profile" rule, so a store holding several profiles with no recorded
default refuses an unselected call rather than picking one.`,
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Flags: []clicore.Flag{
			{Name: "name", Kind: clicore.KindString, Required: true, Description: "Profile to make the default."},
		},
		Examples: []string{"aerp profile use --name lab-a --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := invocation.RequiredString("name")
			if err != nil {
				return nil, err
			}
			store, err := localprofile.Load()
			if err != nil {
				return nil, classifyProfile(err, name, program)
			}
			if _, ok := store.Profiles[name]; !ok {
				return nil, classifyProfile(fmt.Errorf("%w: %q (known: %s)",
					localprofile.ErrProfileNotFound, name, strings.Join(store.Names(), ", ")), name, program)
			}
			store.Default = name
			if err := store.Save(); err != nil {
				return nil, classifyProfile(err, name, program)
			}
			return map[string]interface{}{"profileFile": store.Path(), "defaultProfile": store.Default}, nil
		},
	})
}

func profileRemoveCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:    "remove",
		Summary: "Delete one local profile.",
		Description: `Removes the profile from the local file. It deletes no credential material — this tool
never wrote any — and it touches nothing in the Odoo the profile pointed at.`,
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Flags: []clicore.Flag{
			{Name: "name", Kind: clicore.KindString, Required: true, Description: "Profile to delete."},
		},
		Examples: []string{"aerp profile remove --name lab-a --output json"},
		Run: func(_ context.Context, invocation *clicore.Invocation) (interface{}, error) {
			name, err := invocation.RequiredString("name")
			if err != nil {
				return nil, err
			}
			store, err := localprofile.Load()
			if err != nil {
				return nil, classifyProfile(err, name, program)
			}
			if err := store.Remove(name); err != nil {
				return nil, classifyProfile(err, name, program)
			}
			if err := store.Save(); err != nil {
				return nil, classifyProfile(err, name, program)
			}
			return map[string]interface{}{
				"removed": name, "profileFile": store.Path(),
				"defaultProfile": store.Default, "remaining": store.Names(),
			}, nil
		},
	})
}

// describeProfile is the ONE projection of a profile into an output document, so there is exactly
// one place to check that no secret is rendered — and the check is trivial, because
// localprofile.Profile has no secret field to render.
func describeProfile(profile localprofile.Profile) map[string]interface{} {
	source, resolves := localprofile.CredentialStatus(profile)
	return map[string]interface{}{
		"name":     profile.Name,
		"endpoint": profile.Endpoint,
		"database": profile.Database,
		"login":    profile.Login,
		"protocol": profile.EffectiveProtocol(),
		// credentialRef is a REFERENCE and is safe to print. credentialVariable publishes the exact
		// environment variable that supplies it, because an operator must be able to learn that
		// without guessing the mangling rule. credentialResolves is a boolean, never an echo.
		// credentialSource exposes the selected source (environment, keyring, or file).
		"credentialRef":      profile.CredentialRef,
		"credentialVariable": localprofile.EnvVariableFor(profile.CredentialRef),
		"credentialResolves": resolves,
		"credentialSource":   source,
	}
}

// resolveProfile applies the documented precedence order and maps every refusal onto this product's
// error contract.
func resolveProfile(invocation *clicore.Invocation, program string) (localprofile.Selection, error) {
	store, err := localprofile.Load()
	if err != nil {
		return localprofile.Selection{}, classifyProfile(err, invocation.String("profile"), program)
	}
	selection, err := store.Resolve(invocation.String("profile"))
	if err != nil {
		return localprofile.Selection{}, classifyProfile(err, invocation.String("profile"), program)
	}
	return selection, nil
}

// classifyProfile maps local configuration failures onto the CLI error contract.
//
// These codes are this product's own — the governed product has no profiles and could never emit
// them — but they are deliberately shaped like every other error this platform publishes: a stable
// code, a category that decides the exit code, the resource the failure is about, and a remediation
// naming the exact next command. No message here contains credential material; the localprofile
// package guarantees that at the source.
func classifyProfile(err error, name, program string) error {
	if err == nil {
		return nil
	}
	var cliErr *clicore.Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	switch {
	case errors.Is(err, localprofile.ErrNoProfileFile):
		return clicore.NewError(clicore.CategoryConfig, "PROFILE_FILE_NOT_FOUND", err.Error()).
			WithRemediation("Create one with `" + program + " profile set --name <name> --endpoint <url> --database <db> --login <user>`. Set " +
				localprofile.FileVariable + " to keep profiles somewhere other than the default location.")
	case errors.Is(err, localprofile.ErrProfileNotFound):
		return clicore.NewError(clicore.CategoryNotFound, "PROFILE_NOT_FOUND", err.Error()).
			WithResource("aerp.profile", name).
			WithRemediation("Run `" + program + " profile list --output json` to see every configured profile.")
	case errors.Is(err, localprofile.ErrProfileAmbiguous):
		return clicore.NewError(clicore.CategoryConflict, "PROFILE_AMBIGUOUS", err.Error()).
			WithRemediation("Several profiles are configured and none was selected. Re-run with --profile <name>, set " +
				localprofile.SelectorVariable + ", or record a default with `" + program + " profile use --name <name>`. Nothing was contacted.")
	case errors.Is(err, localprofile.ErrProfileInvalid):
		return clicore.NewError(clicore.CategoryConfig, "PROFILE_INVALID", err.Error()).
			WithResource("aerp.profile", name).
			WithRemediation("A profile needs an endpoint, an odoo database and a credential reference. Rewrite it with `" + program + " profile set`.")
	case errors.Is(err, localprofile.ErrCredentialFilePermissive):
		return clicore.NewError(clicore.CategoryConfig, "PROVIDER_CREDENTIAL_FILE_PERMISSIVE", err.Error()).
			WithResource("aerp.profile", name).
			WithRemediation("The credential file holds a password that can do everything the Odoo user can do, so it is refused rather than read while other local accounts can read it.")
	case errors.Is(err, localprofile.ErrCredentialNotFound):
		return clicore.NewError(clicore.CategoryNotFound, "PROVIDER_CREDENTIAL_NOT_FOUND", err.Error()).
			WithResource("aerp.profile", name).
			WithRemediation("Store the credential with `" + program + " credential set --ref " + name + " --password-stdin`.")
	case errors.Is(err, localprofile.ErrCredentialUnavailable):
		return clicore.NewError(clicore.CategoryConfig, "PROVIDER_CREDENTIAL_UNAVAILABLE", err.Error()).
			WithResource("aerp.profile", name).
			WithRemediation("Run `" + program + " profile show --output json` for the exact environment variable this profile's credential reference reads from. Nothing was contacted.")
	}
	return clicore.InternalError(err)
}
