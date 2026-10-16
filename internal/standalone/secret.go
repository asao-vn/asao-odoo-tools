package standalone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/cli/internal/secretinput"
)

// A secret is typed on a one-time local page, never in the chat (DEC-2026-0179).

// askOnPage asks the person for one secret on a one-time page on this machine. The address goes to
// standard error, for a person whose browser did not open; the value goes only to the caller.
func askOnPage(ctx context.Context, title, detail, label string) (string, error) {
	if strings.EqualFold(os.Getenv("ODOO_CLI_SECRET_INPUT"), "off") {
		return "", clicore.NewError(clicore.CategoryDenied, "SECRET_PAGE_OFF", "ODOO_CLI_SECRET_INPUT=off: secrets are not asked for on a page here").
			WithRemediation("Run the command in a terminal with --password-stdin.")
	}
	value, err := secretinput.Collect(ctx, secretinput.Request{
		Title: title, Detail: detail, Label: label, Theme: strings.ToLower(os.Getenv("ODOO_CLI_THEME")),
		Announce: func(address string) {
			fmt.Fprintf(os.Stderr, "Type it on the page that opened in your browser, or open %s on this machine (it works once, for 5 minutes).\n", address)
		},
	})
	switch {
	case errors.Is(err, secretinput.ErrTimeout):
		return "", clicore.NewError(clicore.CategoryDenied, "SECRET_PAGE_TIMEOUT", "nothing was typed on the page in 5 minutes; nothing was stored")
	case err != nil:
		return "", clicore.NewError(clicore.CategoryUnavailable, "SECRET_PAGE_UNAVAILABLE", err.Error()).
			WithRemediation("Run the command in a terminal with --password-stdin.")
	}
	return value, nil
}

// credentialDetail says on the page which Odoo a credential reference is for.
func credentialDetail(ref string) string {
	store, err := localprofile.Load()
	if err != nil {
		return "Stored in this computer's keyring, or its secure file."
	}
	var uses []string
	for _, name := range store.Names() {
		selection, err := store.Resolve(name)
		if err != nil {
			continue
		}
		if profile := selection.Profile; profile.CredentialRef == ref {
			uses = append(uses, fmt.Sprintf("profile %s (Odoo at %s, database %s)", profile.Name, profile.Endpoint, profile.Database))
		}
	}
	if len(uses) == 0 {
		return "Stored in this computer's keyring, or its secure file. No profile uses it yet."
	}
	return "For " + strings.Join(uses, "; ") + ". Stored in this computer's keyring, or its secure file."
}

func secretGroup(config programConfig) *clicore.Command {
	env := local(&clicore.Command{
		Name:    "env",
		Summary: "Write a secret into a .env file, typed by the person on a one-time page or read from standard input.",
		Description: `Writes or replaces the line NAME=value in a .env file (named .env, .env.<something> or <something>.env),
kept at mode 600. The value is typed by the person on a one-time page on this machine (--browser) or read from
standard input (--stdin). It is never a flag, never printed, and never in the result. An agent may ask for
it: the person confirms, types it on the page, and the agent gets only "stored" (DEC-2026-0179).`,
		SideEffect:  clicore.EffectWriteLocalFiles,
		Authority:   localAuthority,
		AgentAccess: clicore.AgentAccessConfirm,
		Flags: []clicore.Flag{
			{Name: "file", Kind: clicore.KindString, Required: true, Description: "The .env file to write, e.g. .env or deploy/.env.local."},
			{Name: "name", Kind: clicore.KindString, Required: true, Description: "The variable, e.g. ODOO_API_KEY."},
			{Name: "browser", Kind: clicore.KindBool, Description: "Ask the person for the value on a one-time page on this machine."},
			{Name: "stdin", Kind: clicore.KindBool, PersonOnly: true, Description: "Read the value from standard input."},
		},
		Examples: []string{config.name + " secret env --file .env --name ODOO_API_KEY --browser --output json"},
		Run: func(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
			return runSecretEnv(ctx, invocation)
		},
	})
	return &clicore.Command{
		Name:     "secret",
		Summary:  "Put a secret where it is needed without it passing through a chat (DEC-2026-0179).",
		Children: []*clicore.Command{env},
	}
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// envFileAllowed says whether a path names a .env file: what secret env may write, and nothing else.
func envFileAllowed(path string) error {
	base := filepath.Base(path)
	if base != ".env" && !strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".env") {
		return fmt.Errorf("%s is not a .env file (.env, .env.<something> or <something>.env)", path)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; secret env writes only a file it can see", path)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return fmt.Errorf("the folder of %s does not exist", path)
	}
	return nil
}

// envLine writes a value so that a .env reader gives it back: as it is when it is plain, in single
// quotes otherwise. A value with a line break or a single quote is refused.
func envLine(name, value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("the value spans lines")
	}
	if regexp.MustCompile(`^[A-Za-z0-9_./+=:@,%-]*$`).MatchString(value) {
		return name + "=" + value, nil
	}
	if strings.Contains(value, "'") {
		return "", fmt.Errorf("the value holds a single quote, which a .env file cannot keep safely")
	}
	return name + "='" + value + "'", nil
}

func runSecretEnv(ctx context.Context, invocation *clicore.Invocation) (interface{}, error) {
	path := strings.TrimSpace(invocation.String("file"))
	name := strings.TrimSpace(invocation.String("name"))
	if !envName.MatchString(name) {
		return nil, clicore.UsageError("INVALID_FLAG_VALUE", "--name %q is not a variable name", name).WithContextValue("flag", "name")
	}
	if err := envFileAllowed(path); err != nil {
		return nil, clicore.UsageError("SECRET_FILE_REFUSED", "%v", err).WithContextValue("flag", "file")
	}
	browserMode, _ := invocation.Bool("browser")
	stdinMode, _ := invocation.Bool("stdin")
	if browserMode == stdinMode {
		return nil, clicore.UsageError("INVALID_FLAG_VALUE", "pass exactly one of --browser and --stdin; a secret is never a flag value").WithContextValue("flag", "browser")
	}
	absolute, _ := filepath.Abs(path)
	var value string
	var err error
	if browserMode {
		value, err = askOnPage(ctx, name+" in "+filepath.Base(path), "Written into "+absolute+", kept readable by you alone.", name)
	} else {
		value, err = readPasswordFromStdin(ctx)
	}
	if err != nil {
		return nil, err
	}
	line, err := envLine(name, value)
	if err != nil {
		return nil, clicore.UsageError("SECRET_VALUE_REFUSED", "%v; nothing was written", err)
	}
	replaced, err := writeEnvLine(path, name, line)
	if err != nil {
		return nil, clicore.NewError(clicore.CategoryConfig, "SECRET_FILE_NOT_WRITTEN", fmt.Sprintf("could not write %s: %v", path, err))
	}
	return map[string]interface{}{"stored": true, "file": absolute, "name": name, "replaced": replaced}, nil
}

// writeEnvLine replaces the first NAME= line of the file (export NAME= too), or appends one, and
// keeps the file at mode 600.
func writeEnvLine(path, name, line string) (bool, error) {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	pattern := regexp.MustCompile(`^\s*(export\s+)?` + regexp.QuoteMeta(name) + `\s*=`)
	replaced := false
	for index, existing := range lines {
		if pattern.MatchString(existing) {
			lines[index] = line
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, line)
	}
	tmp := path + ".tmp-secret"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return false, err
	}
	return replaced, os.Rename(tmp, path)
}
