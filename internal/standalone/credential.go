package standalone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

type stdinContextKey struct{}

// ContextWithStdin attaches an io.Reader to context for reading password input in tests.
func ContextWithStdin(ctx context.Context, r io.Reader) context.Context {
	return context.WithValue(ctx, stdinContextKey{}, r)
}

// StdinFromContext retrieves the io.Reader attached to context, or nil if none.
func StdinFromContext(ctx context.Context) io.Reader {
	if r, ok := ctx.Value(stdinContextKey{}).(io.Reader); ok && r != nil {
		return r
	}
	return nil
}

func readPasswordFromStdin(ctx context.Context) (string, error) {
	r := StdinFromContext(ctx)
	if r == nil {
		r = os.Stdin
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("could not read password from stdin: %w", err)
	}
	raw := string(data)
	if strings.HasSuffix(raw, "\r\n") {
		raw = strings.TrimSuffix(raw, "\r\n")
	} else if strings.HasSuffix(raw, "\n") {
		raw = strings.TrimSuffix(raw, "\n")
	}
	if len(raw) == 0 {
		return "", clicore.UsageError("EMPTY_PASSWORD", "password supplied via stdin is empty").
			WithRemediation("Supply a non-empty password on standard input.")
	}
	return raw, nil
}

func credentialGroup(program string) *clicore.Command {
	return &clicore.Command{
		Name:    "credential",
		Summary: "Manage stored credentials for local Odoo profiles.",
		Description: `Credentials (passwords) are kept separate from profiles.
They can be stored in the OS keyring (service asao-odoo) or in an owner-only credential file (mode 0600).
Passwords are read strictly from stdin and are never accepted as command-line flags or printed in output.`,
		Children: []*clicore.Command{
			credentialSetCommand(program),
			credentialRemoveCommand(program),
		},
	}
}

func credentialSetCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:        "set",
		Summary:     "Store an Odoo credential safely.",
		SideEffect:  clicore.EffectWriteLocalConfig,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessConfirm,
		Description: `Stores a credential (password or API key) in the OS keyring (service asao-odoo) or fallback secure file.
It is read from standard input (--password-stdin) or typed by the person on a one-time page on this
machine (--browser, DEC-2026-0179). It is never accepted as a command-line flag, never echoed, and never
appears in output or logs. With --browser an agent may ask for it: the person confirms, types it on the
page, and the agent gets only "stored". ODOO_CLI_SECRET_INPUT=off refuses the page; ODOO_CLI_THEME=plain
drops its colours.`,
		Flags: []clicore.Flag{
			{Name: "ref", Kind: clicore.KindString, Required: true, Description: "Credential reference name (e.g. lab-a)."},
			{Name: "login", Kind: clicore.KindString, Description: "Odoo user/login associated with this credential."},
			{Name: "password-stdin", Kind: clicore.KindBool, PersonOnly: true, Description: "Read password from standard input. Passwords must never be provided as CLI flags or arguments."},
			{Name: "browser", Kind: clicore.KindBool, Description: "Ask the person for it on a one-time page on this machine (127.0.0.1, one submission, 5 minutes)."},
		},
		Examples: []string{
			"echo -n 'password' | " + program + " credential set --ref lab-a --login admin --password-stdin --output json",
			program + " credential set --ref lab-a --login admin --browser --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			ref, err := invocation.RequiredString("ref")
			if err != nil {
				return nil, err
			}
			stdinMode, err := invocation.Bool("password-stdin")
			if err != nil {
				return nil, err
			}
			browserMode, err := invocation.Bool("browser")
			if err != nil {
				return nil, err
			}
			if stdinMode == browserMode {
				return nil, clicore.UsageError("INVALID_FLAG_VALUE", "pass exactly one of --password-stdin and --browser; a password is never a flag value").
					WithContextValue("flag", "password-stdin").
					WithRemediation("Pass --password-stdin to supply it on standard input, or --browser to type it on a one-time page.")
			}
			login := invocation.String("login")
			var password string
			if browserMode {
				password, err = askOnPage(ctx, "Odoo credential "+ref, credentialDetail(ref), "Password or API key")
			} else {
				password, err = readPasswordFromStdin(ctx)
			}
			if err != nil {
				return nil, err
			}
			material := localprofile.SecretMaterial{
				Login:    login,
				Password: password,
			}
			source, err := localprofile.PutCredential(ctx, ref, material)
			if err != nil {
				return nil, classifyProfile(err, ref, program)
			}
			return map[string]interface{}{
				"reference": ref,
				"source":    source,
				"status":    "STORED",
			}, nil
		},
	})
}

func credentialRemoveCommand(program string) *clicore.Command {
	return local(&clicore.Command{
		Name:       "remove",
		Summary:    "Remove a stored credential.",
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Description: `Removes a stored credential from the OS keyring or credential file.
It does not modify the profile configuration file.`,
		Flags: []clicore.Flag{
			{Name: "ref", Kind: clicore.KindString, Required: true, Description: "Credential reference name to remove."},
		},
		Examples: []string{
			program + " credential remove --ref lab-a --output json",
		},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			ref, err := invocation.RequiredString("ref")
			if err != nil {
				return nil, err
			}
			source, err := localprofile.DeleteCredential(ctx, ref)
			if err != nil {
				if errors.Is(err, localprofile.ErrCredentialNotFound) {
					return nil, clicore.NewError(clicore.CategoryNotFound, "CREDENTIAL_NOT_FOUND", fmt.Sprintf("no stored credential found for reference %q", ref)).
						WithResource("aerp.credential", ref).
						WithRemediation("Run `" + program + " profile show --output json` to inspect configured references.")
				}
				return nil, classifyProfile(err, ref, program)
			}
			return map[string]interface{}{
				"reference": ref,
				"source":    source,
				"status":    "REMOVED",
			}, nil
		},
	})
}
