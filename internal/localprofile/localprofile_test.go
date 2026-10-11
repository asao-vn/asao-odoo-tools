// Package localprofile_test covers the two rules this package exists to enforce, at the level they
// are enforced: the profile store never persists a secret, and a credential is fetched only through a
// path that refuses an unsafe source.
//
// The standalone command tests exercise these through the CLI. These exercise them directly, because
// the file permissions and the material shapes are properties of the STORE, and a future caller that
// used the store without going through the CLI would still have to obey them.
package localprofile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

func useTempStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "profiles.json")
	t.Setenv(localprofile.FileVariable, path)
	return path
}

// TestSaveCreatesOwnerOnlyFileAndDirectory pins the permissions at creation rather than after a
// chmod: a write-then-chmod leaves a window in which the file exists with the process umask's bits.
func TestSaveCreatesOwnerOnlyFileAndDirectory(t *testing.T) {
	path := useTempStore(t)
	store, err := localprofile.LoadOrEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(localprofile.Profile{
		Name: "lab", Endpoint: "http://odoo.invalid", Database: "lab", Login: "admin", CredentialRef: "lab",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("profile file mode is %04o, want 0600", mode)
	}
	directory, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := directory.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("profile directory mode is %04o; it must not be group- or world-accessible", mode)
	}
}

// TestSavedDocumentHoldsNoSecret checks the credential-separation rule against the BYTES ON DISK. It
// is the check that stays true even if someone later adds a field to Profile.
func TestSavedDocumentHoldsNoSecret(t *testing.T) {
	const secret = "s3cret-material"
	path := useTempStore(t)
	t.Setenv("AERP_CREDENTIAL_LAB", secret)

	store, err := localprofile.LoadOrEmpty()
	if err != nil {
		t.Fatal(err)
	}
	profile := localprofile.Profile{
		Name: "lab", Endpoint: "http://odoo.invalid", Database: "lab", Login: "admin", CredentialRef: "lab",
	}
	if err := store.Put(profile); err != nil {
		t.Fatal(err)
	}
	// Resolve the credential first, so that any accidental write-back of resolved material would have
	// happened before the document is saved.
	if _, err := localprofile.ResolveCredential(profile); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the profile document contains credential material:\n%s", raw)
	}
}

// TestUnknownSchemaVersionIsRefusedNotGuessed: a future layout must be a clear refusal, never a
// partial read that silently drops fields a newer writer added.
func TestUnknownSchemaVersionIsRefusedNotGuessed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte(`{"version":"aerp.profiles/v99","profiles":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(localprofile.FileVariable, path)
	if _, err := localprofile.Load(); !errors.Is(err, localprofile.ErrProfileInvalid) {
		t.Fatalf("loading a future schema version returned %v, want ErrProfileInvalid", err)
	}
}

// TestCredentialMaterialAcceptsBothAcceptedShapes. The object form matters for a specific reason: it
// is byte-identical to what a governed ProviderConnection's credentialRef resolves to, so an operator
// who already holds working credentials for an ASAO-managed Odoo can use them here unchanged.
func TestCredentialMaterialAcceptsBothAcceptedShapes(t *testing.T) {
	profile := localprofile.Profile{Name: "lab", Login: "profile-login", CredentialRef: "lab"}

	t.Run("plain string is the password, login comes from the profile", func(t *testing.T) {
		t.Setenv("AERP_CREDENTIAL_LAB", "just-the-password")
		material, err := localprofile.ResolveCredential(profile)
		if err != nil {
			t.Fatal(err)
		}
		if material.Login != "profile-login" || material.Password != "just-the-password" {
			t.Fatalf("resolved login=%q password-length=%d", material.Login, len(material.Password))
		}
	})

	t.Run("object form names its own user, which wins over the profile", func(t *testing.T) {
		t.Setenv("AERP_CREDENTIAL_LAB", `{"login":"material-login","password":"from-object"}`)
		material, err := localprofile.ResolveCredential(profile)
		if err != nil {
			t.Fatal(err)
		}
		if material.Login != "material-login" || material.Password != "from-object" {
			t.Fatalf("resolved login=%q password-length=%d", material.Login, len(material.Password))
		}
	})
}

// TestPermissiveCredentialFileIsRefusedAndNotRead checks BOTH halves: the refusal happens, and the
// error carries no material from the file it declined to trust.
func TestPermissiveCredentialFileIsRefusedAndNotRead(t *testing.T) {
	const secret = "readable-by-everyone"
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"lab":"`+secret+`"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv(localprofile.CredentialFileVariable, path)

	_, err := localprofile.ResolveCredential(localprofile.Profile{Name: "lab", Login: "admin", CredentialRef: "lab"})
	if !errors.Is(err, localprofile.ErrCredentialFilePermissive) {
		t.Fatalf("a group-readable credential file returned %v, want ErrCredentialFilePermissive", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the refusal echoed the material it declined to trust: %v", err)
	}
}

// TestEnvironmentWinsOverTheCredentialFile pins the documented order, and pins it with a file that
// WOULD resolve — otherwise the test would pass for the wrong reason.
func TestEnvironmentWinsOverTheCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(`{"lab":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(localprofile.CredentialFileVariable, path)
	t.Setenv("AERP_CREDENTIAL_LAB", "from-environment")

	material, err := localprofile.ResolveCredential(localprofile.Profile{Name: "lab", Login: "admin", CredentialRef: "lab"})
	if err != nil {
		t.Fatal(err)
	}
	if material.Password != "from-environment" {
		t.Fatal("the credential file won over the per-reference environment variable")
	}
}

// TestMissingCredentialNamesTheVariableThatWouldSupplyIt. An operator (or an agent) must be able to
// fix this from the error alone; the variable NAME is not a secret and withholding it helps nobody.
func TestMissingCredentialNamesTheVariableThatWouldSupplyIt(t *testing.T) {
	t.Setenv(localprofile.CredentialFileVariable, "")
	_, err := localprofile.ResolveCredential(localprofile.Profile{Name: "lab", Login: "admin", CredentialRef: "lab-a"})
	if !errors.Is(err, localprofile.ErrCredentialUnavailable) {
		t.Fatalf("got %v, want ErrCredentialUnavailable", err)
	}
	if want := localprofile.EnvVariableFor("lab-a"); !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal does not name %s: %v", want, err)
	}
}

// TestRemoveClearsADefaultThatNamedIt. A default pointing at a deleted profile would turn the next
// unselected call into a confusing not-found instead of an honest "nothing is selected".
func TestRemoveClearsADefaultThatNamedIt(t *testing.T) {
	useTempStore(t)
	store, err := localprofile.LoadOrEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(localprofile.Profile{Name: "lab", Endpoint: "http://x.invalid", Database: "lab", CredentialRef: "lab"}); err != nil {
		t.Fatal(err)
	}
	store.Default = "lab"
	if err := store.Remove("lab"); err != nil {
		t.Fatal(err)
	}
	if store.Default != "" {
		t.Fatalf("default is still %q after the profile it named was removed", store.Default)
	}
}

// TestResolveNeverConsultsProcessGlobalOdooVariables is §24 at the layer that decides it.
func TestResolveNeverConsultsProcessGlobalOdooVariables(t *testing.T) {
	t.Setenv("ODOO_URL", "http://smuggled.invalid")
	t.Setenv("ODOO_DB", "smuggled")
	useTempStore(t)

	store, err := localprofile.LoadOrEmpty()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := store.Put(localprofile.Profile{
			Name: name, Endpoint: "http://" + name + ".invalid", Database: name, CredentialRef: name,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Resolve(""); !errors.Is(err, localprofile.ErrProfileAmbiguous) {
		t.Fatalf("two profiles and no selection returned %v, want ErrProfileAmbiguous", err)
	}
}
