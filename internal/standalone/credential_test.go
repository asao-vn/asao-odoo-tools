package standalone_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
)

type unavailableKeyringStore struct{}

func (unavailableKeyringStore) Source() string {
	return localprofile.CredentialSourceKeyring
}

func (unavailableKeyringStore) Get(context.Context, string) (localprofile.SecretMaterial, error) {
	return localprofile.SecretMaterial{}, localprofile.ErrCredentialUnavailable
}

func (unavailableKeyringStore) Put(context.Context, string, localprofile.SecretMaterial) error {
	return localprofile.ErrCredentialUnavailable
}

func (unavailableKeyringStore) Delete(context.Context, string) error {
	return localprofile.ErrCredentialUnavailable
}

type hardKeyringStore struct{}

func (hardKeyringStore) Source() string {
	return localprofile.CredentialSourceKeyring
}

func (hardKeyringStore) Get(context.Context, string) (localprofile.SecretMaterial, error) {
	return localprofile.SecretMaterial{}, errors.New("dbus connection dropped")
}

func (hardKeyringStore) Put(context.Context, string, localprofile.SecretMaterial) error {
	return errors.New("dbus connection dropped")
}

func (hardKeyringStore) Delete(context.Context, string) error {
	return errors.New("dbus connection dropped")
}

type memoryKeyringStore struct {
	items map[string]localprofile.SecretMaterial
}

func newMemoryKeyringStore() *memoryKeyringStore {
	return &memoryKeyringStore{items: make(map[string]localprofile.SecretMaterial)}
}

func (m *memoryKeyringStore) Source() string {
	return localprofile.CredentialSourceKeyring
}

func (m *memoryKeyringStore) Get(_ context.Context, ref string) (localprofile.SecretMaterial, error) {
	item, ok := m.items[ref]
	if !ok {
		return localprofile.SecretMaterial{}, localprofile.ErrCredentialNotFound
	}
	return item, nil
}

func (m *memoryKeyringStore) Put(_ context.Context, ref string, mat localprofile.SecretMaterial) error {
	m.items[ref] = mat
	return nil
}

func (m *memoryKeyringStore) Delete(_ context.Context, ref string) error {
	if _, ok := m.items[ref]; !ok {
		return localprofile.ErrCredentialNotFound
	}
	delete(m.items, ref)
	return nil
}

func runWithStdin(t *testing.T, stdin io.Reader, args ...string) (int, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	ctx := standalone.ContextWithStdin(context.Background(), stdin)
	code := clicore.Run(ctx, standalone.RootFor("odoo"), args, clicore.Streams{Out: out, Diagnostics: diag})
	return code, out, diag
}

// TestCredentialSetRequiresPasswordStdinFlag checks that --password-stdin is mandatory.
func TestCredentialSetRequiresPasswordStdinFlag(t *testing.T) {
	code, out, _ := runWithStdin(t, strings.NewReader("secret\n"), "credential", "set", "--ref", "lab-a", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("credential set without --password-stdin must fail")
	}
	err := decodeError(t, out)
	if err["code"] != "MISSING_REQUIRED_FLAG" {
		t.Fatalf("expected MISSING_REQUIRED_FLAG, got %v", err["code"])
	}
}

// TestCredentialSetRejectsPasswordStdinFalse checks that --password-stdin=false is rejected as a usage error.
func TestCredentialSetRejectsPasswordStdinFalse(t *testing.T) {
	code, out, _ := runWithStdin(t, strings.NewReader("secret\n"),
		"credential", "set", "--ref", "lab-a", "--password-stdin=false", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("credential set with --password-stdin=false must fail")
	}
	if code != clicore.ExitUsage {
		t.Fatalf("expected exit code %d (ExitUsage), got %d; out=%s", clicore.ExitUsage, code, out.String())
	}
	err := decodeError(t, out)
	if err["category"] != string(clicore.CategoryUsage) {
		t.Fatalf("expected category USAGE, got %v", err["category"])
	}
	if err["code"] != "INVALID_FLAG_VALUE" {
		t.Fatalf("expected INVALID_FLAG_VALUE, got %v", err["code"])
	}
}

// TestCredentialSetNoPasswordFlagPublished ensures no password CLI flag exists.
func TestCredentialSetNoPasswordFlagPublished(t *testing.T) {
	tree := discoveryTree(t)
	var credSetSpec *clicore.CommandSpec
	walk(tree, func(spec clicore.CommandSpec) {
		if len(spec.Path) == 2 && spec.Path[0] == "credential" && spec.Path[1] == "set" {
			credSetSpec = &spec
		}
		for _, flag := range spec.Flags {
			if flag.Name == "password" {
				t.Fatalf("command %s publishes --password flag", spec.Command)
			}
		}
	})
	if credSetSpec == nil {
		t.Fatal("credential set command not found in discovery tree")
	}
	for _, flag := range credSetSpec.Flags {
		if flag.Name == "password" {
			t.Fatalf("credential set publishes --password flag")
		}
	}
}

// TestCredentialSetAndRemoveSafeFlow exercises the complete safe credential flow:
// - sets password via stdin
// - trims trailing newline, preserves password spaces
// - asserts output has only reference, source, and status
// - asserts neither stdout, stderr, nor profile file contains password
// - asserts profile is not modified by credential remove
func TestCredentialSetAndRemoveSafeFlow(t *testing.T) {
	restoreKeyring := localprofile.SetDefaultKeyringStore(unavailableKeyringStore{})
	defer restoreKeyring()

	// Use isolated temp files
	credFile := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credFile)

	profileFile := writeProfiles(t, `{"version":"aerp.profiles/v1","defaultProfile":"lab-a","profiles":{
	  "lab-a":{"endpoint":"http://127.0.0.1:8069","database":"lab-a","login":"admin","credentialRef":"lab-a"}}}`)

	const secretWithSpaces = "  my super secret password with spaces  "
	stdinContent := secretWithSpaces + "\n"

	// 1. Initially, profile show reports credentialResolves = false
	code, out, _ := runWithStdin(t, nil, "profile", "show", "--profile", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("profile show failed: %s", out)
	}
	data := decode(t, out)
	if data["credentialResolves"] != false {
		t.Fatalf("expected credentialResolves=false before setting, got %v", data["credentialResolves"])
	}

	// 2. Run credential set
	code, out, diag := runWithStdin(t, strings.NewReader(stdinContent),
		"credential", "set", "--ref", "lab-a", "--login", "admin", "--password-stdin", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("credential set failed (exit %d): %s\ndiag: %s", code, out, diag)
	}

	// Output must not contain the secret
	if strings.Contains(out.String(), secretWithSpaces) {
		t.Fatalf("stdout leaked password: %s", out)
	}
	if strings.Contains(diag.String(), secretWithSpaces) {
		t.Fatalf("stderr leaked password: %s", diag)
	}

	result := decode(t, out)
	// Must return ONLY reference, source, and status
	for k := range result {
		switch k {
		case "reference", "source", "status":
		default:
			t.Fatalf("credential set returned unexpected field %q: %v", k, result[k])
		}
	}
	if result["reference"] != "lab-a" {
		t.Fatalf("expected reference 'lab-a', got %v", result["reference"])
	}
	if result["status"] != "STORED" {
		t.Fatalf("expected status 'STORED', got %v", result["status"])
	}
	source, ok := result["source"].(string)
	if !ok || source != "file" {
		t.Fatalf("expected source 'file', got %v", result["source"])
	}

	// Verify profile file was NOT modified and contains NO secret
	profileBytes, err := os.ReadFile(profileFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(profileBytes), secretWithSpaces) {
		t.Fatalf("profile file contains secret material: %s", profileBytes)
	}

	// 3. Verify profile show now reports credentialResolves = true and exposes credentialSource
	code, out, diag = runWithStdin(t, nil, "profile", "show", "--profile", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("profile show failed: %s", out)
	}
	if strings.Contains(out.String(), secretWithSpaces) || strings.Contains(diag.String(), secretWithSpaces) {
		t.Fatal("profile show leaked secret material")
	}
	data = decode(t, out)
	if data["credentialResolves"] != true {
		t.Fatalf("expected credentialResolves=true, got %v", data["credentialResolves"])
	}
	if data["credentialSource"] != source {
		t.Fatalf("expected credentialSource=%q, got %v", source, data["credentialSource"])
	}

	// 4. Verify doctor reports credentialResolves and credentialSource
	code, out, diag = runWithStdin(t, nil, "doctor", "--profile", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("doctor failed: %s", out)
	}
	if strings.Contains(out.String(), secretWithSpaces) || strings.Contains(diag.String(), secretWithSpaces) {
		t.Fatal("doctor leaked secret material")
	}
	docData := decode(t, out)
	sel, ok := docData["selection"].(map[string]interface{})
	if !ok {
		t.Fatalf("doctor selection is not an object: %v", docData["selection"])
	}
	if sel["credentialResolves"] != true {
		t.Fatalf("doctor selected.credentialResolves = %v, want true", sel["credentialResolves"])
	}
	if sel["credentialSource"] != source {
		t.Fatalf("doctor selected.credentialSource = %v, want %s", sel["credentialSource"], source)
	}

	// 5. Verify the password resolved retains leading/trailing spaces (only trailing newline trimmed)
	store, err := localprofile.Load()
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Resolve("lab-a")
	if err != nil {
		t.Fatal(err)
	}
	resolvedMat, err := localprofile.ResolveCredential(p.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedMat.Password != secretWithSpaces {
		t.Fatalf("password spaces were modified: got %q, want %q", resolvedMat.Password, secretWithSpaces)
	}

	// 6. Run credential remove
	code, out, diag = runWithStdin(t, nil, "credential", "remove", "--ref", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("credential remove failed: %s", out)
	}
	if strings.Contains(out.String(), secretWithSpaces) || strings.Contains(diag.String(), secretWithSpaces) {
		t.Fatal("credential remove leaked secret material")
	}
	remResult := decode(t, out)
	if remResult["reference"] != "lab-a" || remResult["status"] != "REMOVED" || remResult["source"] != "file" {
		t.Fatalf("unexpected remove result: %v", remResult)
	}

	// Verify profile file was NOT modified by remove
	profileBytesAfter, err := os.ReadFile(profileFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(profileBytes) != string(profileBytesAfter) {
		t.Fatalf("credential remove modified profile file: before=%s, after=%s", profileBytes, profileBytesAfter)
	}

	// 7. Verify profile show now reports credentialResolves = false
	code, out, _ = runWithStdin(t, nil, "profile", "show", "--profile", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("profile show failed: %s", out)
	}
	data = decode(t, out)
	if data["credentialResolves"] != false {
		t.Fatalf("expected credentialResolves=false after remove, got %v", data["credentialResolves"])
	}
}

// TestEmptyPasswordFromStdinFailsCleanly checks that an empty password on stdin is refused.
func TestEmptyPasswordFromStdinFailsCleanly(t *testing.T) {
	code, out, _ := runWithStdin(t, strings.NewReader("\n"),
		"credential", "set", "--ref", "lab-empty", "--password-stdin", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("empty password must fail")
	}
	err := decodeError(t, out)
	if err["code"] != "EMPTY_PASSWORD" {
		t.Fatalf("expected EMPTY_PASSWORD, got %v", err["code"])
	}
}

// TestTrustBoundaryDocumentsCredentialSources ensures trust boundary reports keyring and sources.
func TestTrustBoundaryDocumentsCredentialSources(t *testing.T) {
	code, out, _ := runWithStdin(t, nil, "trust", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("trust failed: %s", out)
	}
	data := decode(t, out)
	handling, ok := data["credentialHandling"].(map[string]interface{})
	if !ok {
		t.Fatal("trust does not contain credentialHandling")
	}
	sources, ok := handling["credentialSources"].([]interface{})
	if !ok {
		t.Fatal("trust credentialHandling does not contain credentialSources list")
	}
	expectedSources := []string{"environment", "keyring", "file"}
	for _, expected := range expectedSources {
		found := false
		for _, s := range sources {
			if s == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("trust credentialSources missing %q: %v", expected, sources)
		}
	}
}

func TestCredentialRemove_UnavailableKeyringWithFileCredential(t *testing.T) {
	restoreKeyring := localprofile.SetDefaultKeyringStore(unavailableKeyringStore{})
	defer restoreKeyring()

	credFile := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credFile)
	if err := os.WriteFile(credFile, []byte(`{"lab-a":"my-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, diag := runWithStdin(t, nil, "credential", "remove", "--ref", "lab-a", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("credential remove failed: exit=%d, out=%s, diag=%s", code, out, diag)
	}
	res := decode(t, out)
	if res["status"] != "REMOVED" || res["source"] != "file" || res["reference"] != "lab-a" {
		t.Fatalf("unexpected remove response: %v", res)
	}

	raw, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "lab-a") {
		t.Fatalf("credential file still contains lab-a: %s", raw)
	}
}

func TestCredentialRemove_HardKeyringFailureWithFileCredential(t *testing.T) {
	restoreKeyring := localprofile.SetDefaultKeyringStore(hardKeyringStore{})
	defer restoreKeyring()

	credFile := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credFile)
	if err := os.WriteFile(credFile, []byte(`{"lab-a":"my-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runWithStdin(t, nil, "credential", "remove", "--ref", "lab-a", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("expected error on hard keyring failure even though file was attempted")
	}
	errResp := decodeError(t, out)
	if errResp["code"] == "CREDENTIAL_REMOVED" {
		t.Fatalf("expected error, got %v", errResp)
	}

	// Verify that file deletion was attempted and succeeded
	raw, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "lab-a") {
		t.Fatalf("file deletion was not attempted; file still contains lab-a: %s", raw)
	}
}

func TestCredentialRemove_KeyringSuccessWithFilePermissive(t *testing.T) {
	memKeyring := newMemoryKeyringStore()
	memKeyring.items["lab-a"] = localprofile.SecretMaterial{Login: "admin", Password: "keyring-secret"}
	restoreKeyring := localprofile.SetDefaultKeyringStore(memKeyring)
	defer restoreKeyring()

	credFile := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credFile)
	if err := os.WriteFile(credFile, []byte(`{"lab-a":"file-secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runWithStdin(t, nil, "credential", "remove", "--ref", "lab-a", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("expected error when credential file is permissive")
	}
	errResp := decodeError(t, out)
	if errResp["code"] != "PROVIDER_CREDENTIAL_FILE_PERMISSIVE" {
		t.Fatalf("expected PROVIDER_CREDENTIAL_FILE_PERMISSIVE, got %v", errResp["code"])
	}

	// Verify keyring was still deleted despite file error
	if _, ok := memKeyring.items["lab-a"]; ok {
		t.Fatal("expected keyring item to be deleted despite file permission error")
	}
}

func TestCredentialRemove_NoSourcesNotFound(t *testing.T) {
	memKeyring := newMemoryKeyringStore()
	restoreKeyring := localprofile.SetDefaultKeyringStore(memKeyring)
	defer restoreKeyring()

	credFile := filepath.Join(t.TempDir(), "nonexistent.json")
	t.Setenv(localprofile.CredentialFileVariable, credFile)

	code, out, _ := runWithStdin(t, nil, "credential", "remove", "--ref", "missing", "--output", "json")
	if code != clicore.ExitNotFound {
		t.Fatalf("expected exit code %d (ExitNotFound), got %d; out=%s", clicore.ExitNotFound, code, out.String())
	}
	errResp := decodeError(t, out)
	if errResp["code"] != "CREDENTIAL_NOT_FOUND" {
		t.Fatalf("expected CREDENTIAL_NOT_FOUND, got %v", errResp["code"])
	}
}

func TestCredentialSet_MalformedExistingFileFailsAndPreservesFile(t *testing.T) {
	restoreKeyring := localprofile.SetDefaultKeyringStore(unavailableKeyringStore{})
	defer restoreKeyring()

	credFile := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credFile)
	malformed := []byte("{\n  \"corrupted\": true,\n  syntax error here\n")
	if err := os.WriteFile(credFile, malformed, 0o600); err != nil {
		t.Fatal(err)
	}

	const secret = "should-not-be-in-output-or-file"
	code, out, diag := runWithStdin(t, strings.NewReader(secret+"\n"),
		"credential", "set", "--ref", "lab-a", "--password-stdin", "--output", "json")
	if code == clicore.ExitOK {
		t.Fatal("expected credential set to fail when credential file is malformed")
	}

	if strings.Contains(out.String(), secret) || strings.Contains(diag.String(), secret) {
		t.Fatal("secret leaked in stdout or stderr")
	}

	after, err := os.ReadFile(credFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, malformed) {
		t.Fatalf("credential file was modified on error: before=%q, after=%q", string(malformed), string(after))
	}
}

func TestCredentialSet_PreservesExistingFileReferences(t *testing.T) {
	restoreKeyring := localprofile.SetDefaultKeyringStore(unavailableKeyringStore{})
	defer restoreKeyring()

	credFile := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credFile)
	initial := []byte(`{"lab-a":"pass-a"}`)
	if err := os.WriteFile(credFile, initial, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, diag := runWithStdin(t, strings.NewReader("pass-b\n"),
		"credential", "set", "--ref", "lab-b", "--password-stdin", "--output", "json")
	if code != clicore.ExitOK {
		t.Fatalf("credential set failed: exit=%d out=%s diag=%s", code, out, diag)
	}

	fileStore := localprofile.NewFileSecretStore(credFile)
	matA, err := fileStore.Get(context.Background(), "lab-a")
	if err != nil || matA.Password != "pass-a" {
		t.Fatalf("lab-a lost or corrupted: mat=%+v err=%v", matA, err)
	}
	matB, err := fileStore.Get(context.Background(), "lab-b")
	if err != nil || matB.Password != "pass-b" {
		t.Fatalf("lab-b not saved: mat=%+v err=%v", matB, err)
	}
}
