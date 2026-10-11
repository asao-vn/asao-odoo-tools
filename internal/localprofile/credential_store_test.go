package localprofile_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/localprofile"
)

// FakeSecretStore is a test double implementing SecretStore.
type FakeSecretStore struct {
	source  string
	items   map[string]localprofile.SecretMaterial
	getErr  error
	putErr  error
	delErr  error
	unavail bool
}

func NewFakeSecretStore(source string) *FakeSecretStore {
	return &FakeSecretStore{
		source: source,
		items:  make(map[string]localprofile.SecretMaterial),
	}
}

func (f *FakeSecretStore) Source() string {
	return f.source
}

func (f *FakeSecretStore) Get(_ context.Context, ref string) (localprofile.SecretMaterial, error) {
	if f.unavail {
		return localprofile.SecretMaterial{}, localprofile.ErrCredentialUnavailable
	}
	if f.getErr != nil {
		return localprofile.SecretMaterial{}, f.getErr
	}
	item, ok := f.items[ref]
	if !ok {
		return localprofile.SecretMaterial{}, localprofile.ErrCredentialNotFound
	}
	return item, nil
}

func (f *FakeSecretStore) Put(_ context.Context, ref string, material localprofile.SecretMaterial) error {
	if f.unavail {
		return localprofile.ErrCredentialUnavailable
	}
	if f.putErr != nil {
		return f.putErr
	}
	f.items[ref] = material
	return nil
}

func (f *FakeSecretStore) Delete(_ context.Context, ref string) error {
	if f.unavail {
		return localprofile.ErrCredentialUnavailable
	}
	if f.delErr != nil {
		return f.delErr
	}
	if _, ok := f.items[ref]; !ok {
		return localprofile.ErrCredentialNotFound
	}
	delete(f.items, ref)
	return nil
}

func TestPrecedence_EnvOverKeyringOverFile(t *testing.T) {
	ctx := context.Background()
	profile := localprofile.Profile{Name: "lab", Login: "admin", CredentialRef: "lab"}

	// Set up all 3 layers with different passwords
	const (
		envPassword     = "env-password"
		keyringPassword = "keyring-password"
		filePassword    = "file-password"
	)

	// Layer 1: Env var
	t.Setenv("AERP_CREDENTIAL_LAB", envPassword)

	// Layer 2: Keyring
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.items["lab"] = localprofile.SecretMaterial{Login: "admin", Password: keyringPassword}

	// Layer 3: File
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"lab":"`+filePassword+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	// 1. Env wins when all 3 are present
	mat, src, err := localprofile.ResolveCredentialWithStores(ctx, profile, os.Getenv, fakeKeyring, fileStore)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if src != localprofile.CredentialSourceEnvironment {
		t.Fatalf("expected source %q, got %q", localprofile.CredentialSourceEnvironment, src)
	}
	if mat.Password != envPassword {
		t.Fatalf("expected password %q, got %q", envPassword, mat.Password)
	}

	// 2. Keyring wins over file when env is unset
	t.Setenv("AERP_CREDENTIAL_LAB", "")
	mat, src, err = localprofile.ResolveCredentialWithStores(ctx, profile, os.Getenv, fakeKeyring, fileStore)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if src != localprofile.CredentialSourceKeyring {
		t.Fatalf("expected source %q, got %q", localprofile.CredentialSourceKeyring, src)
	}
	if mat.Password != keyringPassword {
		t.Fatalf("expected password %q, got %q", keyringPassword, mat.Password)
	}

	// 3. File is used when env and keyring are absent
	delete(fakeKeyring.items, "lab")
	mat, src, err = localprofile.ResolveCredentialWithStores(ctx, profile, os.Getenv, fakeKeyring, fileStore)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if src != localprofile.CredentialSourceFile {
		t.Fatalf("expected source %q, got %q", localprofile.CredentialSourceFile, src)
	}
	if mat.Password != filePassword {
		t.Fatalf("expected password %q, got %q", filePassword, mat.Password)
	}
}

func TestKeyringFallbackToFileWhenUnavailable(t *testing.T) {
	ctx := context.Background()
	profile := localprofile.Profile{Name: "lab", Login: "admin", CredentialRef: "lab"}

	t.Setenv("AERP_CREDENTIAL_LAB", "")

	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.unavail = true

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"lab":"fallback-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	mat, src, err := localprofile.ResolveCredentialWithStores(ctx, profile, os.Getenv, fakeKeyring, fileStore)
	if err != nil {
		t.Fatalf("resolve with unavailable keyring failed: %v", err)
	}
	if src != localprofile.CredentialSourceFile {
		t.Fatalf("expected source %q, got %q", localprofile.CredentialSourceFile, src)
	}
	if mat.Password != "fallback-secret" {
		t.Fatalf("expected password %q, got %q", "fallback-secret", mat.Password)
	}
}

func TestPermissiveFileRefusalDoesNotExposeSecret(t *testing.T) {
	ctx := context.Background()
	profile := localprofile.Profile{Name: "lab", Login: "admin", CredentialRef: "lab"}

	t.Setenv("AERP_CREDENTIAL_LAB", "")

	const secret = "should-not-be-in-error"
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"lab":"`+secret+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	_, _, err := localprofile.ResolveCredentialWithStores(ctx, profile, os.Getenv, nil, fileStore)
	if !errors.Is(err, localprofile.ErrCredentialFilePermissive) {
		t.Fatalf("expected ErrCredentialFilePermissive, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed secret material: %v", err)
	}
}

func TestMissingMaterialReturnsActionableErrorWithoutSecret(t *testing.T) {
	ctx := context.Background()
	profile := localprofile.Profile{Name: "lab", Login: "admin", CredentialRef: "lab-ref"}

	t.Setenv("AERP_CREDENTIAL_LAB_REF", "")

	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fileStore := localprofile.NewFileSecretStore(filepath.Join(t.TempDir(), "nonexistent.json"))

	_, _, err := localprofile.ResolveCredentialWithStores(ctx, profile, os.Getenv, fakeKeyring, fileStore)
	if !errors.Is(err, localprofile.ErrCredentialUnavailable) {
		t.Fatalf("expected ErrCredentialUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "AERP_CREDENTIAL_LAB_REF") {
		t.Fatalf("error should name the environment variable, got: %v", err)
	}
}

func TestSecretStoreDelete(t *testing.T) {
	ctx := context.Background()
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.items["lab"] = localprofile.SecretMaterial{Login: "admin", Password: "xyz"}

	err := fakeKeyring.Delete(ctx, "lab")
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	_, err = fakeKeyring.Get(ctx, "lab")
	if !errors.Is(err, localprofile.ErrCredentialNotFound) {
		t.Fatalf("expected ErrCredentialNotFound after delete, got %v", err)
	}

	// Delete on non-existent reference
	err = fakeKeyring.Delete(ctx, "nonexistent")
	if !errors.Is(err, localprofile.ErrCredentialNotFound) {
		t.Fatalf("expected ErrCredentialNotFound for missing ref, got %v", err)
	}
}

func TestFileSecretStoreOwnerOnlyMode(t *testing.T) {
	ctx := context.Background()
	credPath := filepath.Join(t.TempDir(), "nested", "credentials.json")
	fileStore := localprofile.NewFileSecretStore(credPath)

	const secret = "owner-only-pass"
	err := fileStore.Put(ctx, "lab", localprofile.SecretMaterial{Login: "admin", Password: secret})
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}

	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("credential file mode is %04o, want 0600", mode)
	}

	dirInfo, err := os.Stat(filepath.Dir(credPath))
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("credential directory mode is %04o, must reject group/other bits", mode)
	}

	// Read it back
	mat, err := fileStore.Get(ctx, "lab")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if mat.Password != secret {
		t.Fatalf("got password %q, want %q", mat.Password, secret)
	}

	// Delete it
	err = fileStore.Delete(ctx, "lab")
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	_, err = fileStore.Get(ctx, "lab")
	if !errors.Is(err, localprofile.ErrCredentialUnavailable) && !errors.Is(err, localprofile.ErrCredentialNotFound) {
		t.Fatalf("expected not found/unavailable after delete, got %v", err)
	}
}

func TestPutCredentialKeyringFallback(t *testing.T) {
	ctx := context.Background()
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.unavail = true
	restore := localprofile.SetDefaultKeyringStore(fakeKeyring)
	defer restore()

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credPath)

	src, err := localprofile.PutCredential(ctx, "test-ref", localprofile.SecretMaterial{Login: "admin", Password: "fallback-pass"})
	if err != nil {
		t.Fatalf("PutCredential failed: %v", err)
	}
	if src != localprofile.CredentialSourceFile {
		t.Fatalf("expected fallback source %q, got %q", localprofile.CredentialSourceFile, src)
	}

	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("fallback credential file mode is %04o, want 0600", mode)
	}
}

func TestKeyringErrorRedaction(t *testing.T) {
	ctx := context.Background()
	// Mock a failing keyring provider that outputs errors with sensitive details
	mockProvider := &mockFailingKeyringProvider{}
	store := localprofile.NewKeyringStore("asao-odoo", mockProvider)

	err := store.Put(ctx, "ref", localprofile.SecretMaterial{Password: "super-secret-password-12345"})
	if err == nil {
		t.Fatal("expected error from failing keyring provider")
	}
	if strings.Contains(err.Error(), "super-secret-password-12345") {
		t.Fatalf("keyring Put error leaked secret material: %v", err)
	}

	_, err = store.Get(ctx, "ref")
	if err == nil {
		t.Fatal("expected error from failing keyring provider")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("keyring Get error leaked secret material: %v", err)
	}

	err = store.Delete(ctx, "ref")
	if err == nil {
		t.Fatal("expected error from failing keyring provider")
	}
	if !errors.Is(err, localprofile.ErrCredentialUnavailable) {
		t.Fatalf("expected ErrCredentialUnavailable, got %v", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("keyring Delete error leaked secret material: %v", err)
	}
}

func TestDeleteCredential_UnavailableKeyringFileDeleted(t *testing.T) {
	ctx := context.Background()
	const secret = "fallback-secret"
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"lab":"`+secret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.unavail = true

	source, err := localprofile.DeleteCredentialWithStores(ctx, "lab", fakeKeyring, fileStore)
	if err != nil {
		t.Fatalf("expected DeleteCredentialWithStores to succeed with unavailable keyring and valid file credential, got err: %v", err)
	}
	if source != localprofile.CredentialSourceFile {
		t.Fatalf("expected source %q, got %q", localprofile.CredentialSourceFile, source)
	}

	// Verify the file was deleted
	_, fileErr := fileStore.Get(ctx, "lab")
	if !errors.Is(fileErr, localprofile.ErrCredentialNotFound) && !errors.Is(fileErr, localprofile.ErrCredentialUnavailable) {
		t.Fatalf("expected credential to have been deleted from file store, got err: %v", fileErr)
	}
}

func TestDeleteCredential_UnavailableKeyringNoSources(t *testing.T) {
	ctx := context.Background()
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.unavail = true

	fileStore := localprofile.NewFileSecretStore(filepath.Join(t.TempDir(), "nonexistent.json"))

	source, err := localprofile.DeleteCredentialWithStores(ctx, "missing", fakeKeyring, fileStore)
	if err == nil {
		t.Fatal("expected DeleteCredentialWithStores to fail when keyring is unavailable and file has no credential")
	}
	if !errors.Is(err, localprofile.ErrCredentialUnavailable) {
		t.Fatalf("expected ErrCredentialUnavailable, got %v", err)
	}
	if source != "" {
		t.Fatalf("expected empty source on failure, got %q", source)
	}
}

func TestDeleteCredentialPartialFailure_KeyringErrorFileDeleted(t *testing.T) {
	ctx := context.Background()
	const secret = "file-secret-should-not-leak"
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"lab":"`+secret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.delErr = errors.New("keyring daemon connection refused")

	source, err := localprofile.DeleteCredentialWithStores(ctx, "lab", fakeKeyring, fileStore)
	if err == nil {
		t.Fatal("expected DeleteCredential to fail when keyring deletion fails")
	}
	if source != "" {
		t.Fatalf("expected empty source on failure, got %q", source)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed secret material: %v", err)
	}

	// Verify the useful behavior: file deletion was still attempted and succeeded
	_, fileErr := fileStore.Get(ctx, "lab")
	if !errors.Is(fileErr, localprofile.ErrCredentialNotFound) && !errors.Is(fileErr, localprofile.ErrCredentialUnavailable) {
		t.Fatalf("expected credential to have been deleted from file store despite keyring failure, got err: %v", fileErr)
	}
}

func TestDeleteCredentialPartialFailure_KeyringDeletedFilePermissive(t *testing.T) {
	ctx := context.Background()
	const (
		keyringSecret = "keyring-secret-should-not-leak"
		fileSecret    = "file-secret-should-not-leak"
	)
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.items["lab"] = localprofile.SecretMaterial{Login: "admin", Password: keyringSecret}

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"lab":"`+fileSecret+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	source, err := localprofile.DeleteCredentialWithStores(ctx, "lab", fakeKeyring, fileStore)
	if err == nil {
		t.Fatal("expected DeleteCredential to fail when file store is permissive")
	}
	if !errors.Is(err, localprofile.ErrCredentialFilePermissive) {
		t.Fatalf("expected ErrCredentialFilePermissive, got %v", err)
	}
	if source != "" {
		t.Fatalf("expected empty source on failure, got %q", source)
	}
	if strings.Contains(err.Error(), keyringSecret) || strings.Contains(err.Error(), fileSecret) {
		t.Fatalf("error exposed secret material: %v", err)
	}

	// Verify keyring was still deleted despite file failure
	_, keyringGetErr := fakeKeyring.Get(ctx, "lab")
	if !errors.Is(keyringGetErr, localprofile.ErrCredentialNotFound) {
		t.Fatalf("expected credential to have been deleted from keyring store despite file failure, got err: %v", keyringGetErr)
	}
}

func TestDeleteCredentialAllSourcesAbsent(t *testing.T) {
	ctx := context.Background()
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fileStore := localprofile.NewFileSecretStore(filepath.Join(t.TempDir(), "nonexistent.json"))

	source, err := localprofile.DeleteCredentialWithStores(ctx, "missing", fakeKeyring, fileStore)
	if err == nil {
		t.Fatal("expected DeleteCredential to fail when ref absent from all sources")
	}
	if !errors.Is(err, localprofile.ErrCredentialNotFound) {
		t.Fatalf("expected ErrCredentialNotFound, got %v", err)
	}
	if source != "" {
		t.Fatalf("expected empty source on failure, got %q", source)
	}
}

func TestDeleteCredentialBothSourcesDeleted(t *testing.T) {
	ctx := context.Background()
	const secret = "shared-secret"
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.items["lab"] = localprofile.SecretMaterial{Login: "admin", Password: secret}

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(credPath, []byte(`{"lab":"`+secret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	source, err := localprofile.DeleteCredentialWithStores(ctx, "lab", fakeKeyring, fileStore)
	if err != nil {
		t.Fatalf("expected DeleteCredential to succeed, got %v", err)
	}
	if source != localprofile.CredentialSourceKeyring {
		t.Fatalf("expected source %q, got %q", localprofile.CredentialSourceKeyring, source)
	}

	// Both should be deleted
	if _, err := fakeKeyring.Get(ctx, "lab"); !errors.Is(err, localprofile.ErrCredentialNotFound) {
		t.Fatalf("expected keyring item to be deleted, got %v", err)
	}
	if _, err := fileStore.Get(ctx, "lab"); !errors.Is(err, localprofile.ErrCredentialNotFound) && !errors.Is(err, localprofile.ErrCredentialUnavailable) {
		t.Fatalf("expected file item to be deleted, got %v", err)
	}
}

type mockFailingKeyringProvider struct{}

func (m *mockFailingKeyringProvider) Get(service, user string) (string, error) {
	return "", errors.New("dbus failure: connection refused")
}

func (m *mockFailingKeyringProvider) Set(service, user, password string) error {
	return errors.New("dbus failure while setting secret: " + password)
}

func (m *mockFailingKeyringProvider) Delete(service, user string) error {
	return errors.New("dbus failure while deleting secret: super-secret-delete-material-12345")
}

func TestFileSecretStore_Put_MalformedExistingFileRemainsByteForByteUnchanged(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name    string
		content []byte
	}{
		{
			name:    "broken_syntax",
			content: []byte("{\n  \"valid_ref\": \"valid_secret\",\n  BROKEN_SYNTAX_WITHOUT_QUOTES\n"),
		},
		{
			name:    "truncated_json",
			content: []byte("{\"valid_ref\": \"valid_secret\","),
		},
		{
			name:    "json_array_not_object",
			content: []byte("[\"first\", \"second\"]\n"),
		},
		{
			name:    "json_string_not_object",
			content: []byte("\"just-a-string\"\n"),
		},
		{
			name:    "json_number_not_object",
			content: []byte("12345\n"),
		},
		{
			name:    "json_null",
			content: []byte("null\n"),
		},
		{
			name:    "empty_file",
			content: []byte(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			credPath := filepath.Join(t.TempDir(), "credentials.json")
			if err := os.WriteFile(credPath, tc.content, 0o600); err != nil {
				t.Fatal(err)
			}
			fileStore := localprofile.NewFileSecretStore(credPath)

			const newSecret = "super-secret-password-12345"
			err := fileStore.Put(ctx, "new-ref", localprofile.SecretMaterial{
				Login:    "admin",
				Password: newSecret,
			})
			if err == nil {
				t.Fatal("expected Put to fail on malformed existing credential file")
			}
			if !errors.Is(err, localprofile.ErrCredentialUnavailable) && !errors.Is(err, localprofile.ErrProfileInvalid) {
				t.Fatalf("expected classified error (ErrCredentialUnavailable or ErrProfileInvalid), got %v", err)
			}
			if strings.Contains(err.Error(), newSecret) {
				t.Fatalf("error leaked secret material: %v", err)
			}

			// File must remain byte-for-byte unchanged
			afterBytes, readErr := os.ReadFile(credPath)
			if readErr != nil {
				t.Fatalf("could not read back credential file: %v", readErr)
			}
			if !bytes.Equal(afterBytes, tc.content) {
				t.Fatalf("file content was modified!\nbefore: %q\nafter:  %q", string(tc.content), string(afterBytes))
			}

			// Mode must remain 0600
			info, statErr := os.Stat(credPath)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Fatalf("credential file mode changed to %04o, want 0600", mode)
			}
		})
	}
}

func TestFileSecretStore_Put_PreservesExistingEntries(t *testing.T) {
	ctx := context.Background()
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	initialContent := []byte(`{
  "existing-string-ref": "password-alpha",
  "existing-object-ref": {
    "login": "operator",
    "password": "password-beta"
  }
}`)
	if err := os.WriteFile(credPath, initialContent, 0o600); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	// Add a new reference
	err := fileStore.Put(ctx, "new-ref", localprofile.SecretMaterial{
		Login:    "admin",
		Password: "password-gamma",
	})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Verify all 3 references are resolvable and correct
	mat1, err := fileStore.Get(ctx, "existing-string-ref")
	if err != nil {
		t.Fatalf("failed to get existing-string-ref: %v", err)
	}
	if mat1.Password != "password-alpha" {
		t.Fatalf("existing-string-ref password modified: got %q, want %q", mat1.Password, "password-alpha")
	}

	mat2, err := fileStore.Get(ctx, "existing-object-ref")
	if err != nil {
		t.Fatalf("failed to get existing-object-ref: %v", err)
	}
	if mat2.Login != "operator" || mat2.Password != "password-beta" {
		t.Fatalf("existing-object-ref modified: got %+v, want login=operator password=password-beta", mat2)
	}

	mat3, err := fileStore.Get(ctx, "new-ref")
	if err != nil {
		t.Fatalf("failed to get new-ref: %v", err)
	}
	if mat3.Login != "admin" || mat3.Password != "password-gamma" {
		t.Fatalf("new-ref mismatch: got %+v", mat3)
	}

	// Now update one existing reference, ensuring others are still preserved
	err = fileStore.Put(ctx, "existing-string-ref", localprofile.SecretMaterial{
		Login:    "admin-new",
		Password: "password-alpha-updated",
	})
	if err != nil {
		t.Fatalf("updating existing-string-ref failed: %v", err)
	}

	mat1Updated, err := fileStore.Get(ctx, "existing-string-ref")
	if err != nil {
		t.Fatalf("failed to get updated existing-string-ref: %v", err)
	}
	if mat1Updated.Password != "password-alpha-updated" || mat1Updated.Login != "admin-new" {
		t.Fatalf("updated reference mismatch: got %+v", mat1Updated)
	}

	// Verify existing-object-ref and new-ref are still intact
	mat2Check, err := fileStore.Get(ctx, "existing-object-ref")
	if err != nil {
		t.Fatalf("existing-object-ref lost after updating another ref: %v", err)
	}
	if mat2Check.Login != "operator" || mat2Check.Password != "password-beta" {
		t.Fatalf("existing-object-ref modified: got %+v", mat2Check)
	}

	mat3Check, err := fileStore.Get(ctx, "new-ref")
	if err != nil {
		t.Fatalf("new-ref lost after updating another ref: %v", err)
	}
	if mat3Check.Login != "admin" || mat3Check.Password != "password-gamma" {
		t.Fatalf("new-ref modified: got %+v", mat3Check)
	}
}

func TestFileSecretStore_Put_PermissiveFileRemainsUnchanged(t *testing.T) {
	ctx := context.Background()
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	initialContent := []byte(`{"existing": "keep-me"}`)
	if err := os.WriteFile(credPath, initialContent, 0o644); err != nil {
		t.Fatal(err)
	}
	fileStore := localprofile.NewFileSecretStore(credPath)

	const secret = "should-not-be-written"
	err := fileStore.Put(ctx, "new-ref", localprofile.SecretMaterial{Password: secret})
	if err == nil {
		t.Fatal("expected error on permissive file")
	}
	if !errors.Is(err, localprofile.ErrCredentialFilePermissive) {
		t.Fatalf("expected ErrCredentialFilePermissive, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret material: %v", err)
	}

	// File content must remain byte-for-byte unchanged
	afterBytes, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterBytes, initialContent) {
		t.Fatalf("permissive file was modified: before=%q, after=%q", string(initialContent), string(afterBytes))
	}
}

func TestPutCredential_KeyringFallback_MalformedFileRemainsUnchanged(t *testing.T) {
	ctx := context.Background()
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.unavail = true
	restore := localprofile.SetDefaultKeyringStore(fakeKeyring)
	defer restore()

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credPath)
	malformed := []byte("not valid json at all")
	if err := os.WriteFile(credPath, malformed, 0o600); err != nil {
		t.Fatal(err)
	}

	const secret = "secret-pass-keyring-fallback"
	src, err := localprofile.PutCredential(ctx, "ref", localprofile.SecretMaterial{Password: secret})
	if err == nil {
		t.Fatal("expected PutCredential to fail when fallback file is malformed")
	}
	if src != "" {
		t.Fatalf("expected empty source on failure, got %q", src)
	}
	if !errors.Is(err, localprofile.ErrCredentialUnavailable) && !errors.Is(err, localprofile.ErrProfileInvalid) {
		t.Fatalf("expected classified error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret material: %v", err)
	}

	after, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, malformed) {
		t.Fatalf("fallback file was modified: before=%q, after=%q", string(malformed), string(after))
	}
}

func TestPutCredential_KeyringFallback_PreservesExistingEntries(t *testing.T) {
	ctx := context.Background()
	fakeKeyring := NewFakeSecretStore(localprofile.CredentialSourceKeyring)
	fakeKeyring.unavail = true
	restore := localprofile.SetDefaultKeyringStore(fakeKeyring)
	defer restore()

	credPath := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(localprofile.CredentialFileVariable, credPath)
	initial := []byte("{\n  \"ref-a\": \"secret-a\"\n}\n")
	if err := os.WriteFile(credPath, initial, 0o600); err != nil {
		t.Fatal(err)
	}

	src, err := localprofile.PutCredential(ctx, "ref-b", localprofile.SecretMaterial{Password: "secret-b"})
	if err != nil {
		t.Fatalf("PutCredential failed: %v", err)
	}
	if src != localprofile.CredentialSourceFile {
		t.Fatalf("expected source file, got %q", src)
	}

	fileStore := localprofile.NewFileSecretStore(credPath)
	matA, err := fileStore.Get(ctx, "ref-a")
	if err != nil || matA.Password != "secret-a" {
		t.Fatalf("ref-a not preserved: mat=%+v, err=%v", matA, err)
	}
	matB, err := fileStore.Get(ctx, "ref-b")
	if err != nil || matB.Password != "secret-b" {
		t.Fatalf("ref-b not stored: mat=%+v, err=%v", matB, err)
	}
}
