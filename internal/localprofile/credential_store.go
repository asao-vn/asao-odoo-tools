package localprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	// KeyringService is the service name used for OS keyring storage.
	KeyringService = "asao-odoo"

	// Credential source names
	CredentialSourceEnvironment = "environment"
	CredentialSourceKeyring     = "keyring"
	CredentialSourceFile        = "file"
)

var (
	// ErrCredentialNotFound reports that a requested credential reference was not found in the store.
	ErrCredentialNotFound = errors.New("local profile: credential reference not found")
)

// SecretMaterial holds the login and password for an Odoo credential.
type SecretMaterial struct {
	Login    string `json:"login,omitempty"`
	Password string `json:"password"`
}

// SecretStore is the interface for storing and resolving credentials safely.
type SecretStore interface {
	Get(context.Context, string) (SecretMaterial, error)
	Put(context.Context, string, SecretMaterial) error
	Delete(context.Context, string) error
	Source() string
}

// KeyringProvider abstracts OS keyring operations so tests can run without desktop keyring daemons.
type KeyringProvider interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type osKeyringProvider struct{}

func (osKeyringProvider) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyringProvider) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (osKeyringProvider) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

// KeyringStore implements SecretStore backed by the OS keyring.
type KeyringStore struct {
	service  string
	provider KeyringProvider
}

// NewKeyringStore creates a KeyringStore for the given service and provider.
func NewKeyringStore(service string, provider KeyringProvider) *KeyringStore {
	if service == "" {
		service = KeyringService
	}
	if provider == nil {
		provider = osKeyringProvider{}
	}
	return &KeyringStore{
		service:  service,
		provider: provider,
	}
}

func (k *KeyringStore) Source() string {
	return CredentialSourceKeyring
}

func (k *KeyringStore) Get(_ context.Context, ref string) (SecretMaterial, error) {
	if strings.TrimSpace(ref) == "" {
		return SecretMaterial{}, fmt.Errorf("%w: empty reference", ErrCredentialNotFound)
	}
	raw, err := k.provider.Get(k.service, ref)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return SecretMaterial{}, fmt.Errorf("%w: reference %q not found in keyring", ErrCredentialNotFound, ref)
		}
		// Never expose raw keyring errors that might contain sensitive detail.
		return SecretMaterial{}, fmt.Errorf("%w: keyring unavailable", ErrCredentialUnavailable)
	}
	return parseRawSecret(raw, ref)
}

func (k *KeyringStore) Put(_ context.Context, ref string, material SecretMaterial) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("local profile: credential reference cannot be empty")
	}
	if material.Password == "" {
		return errors.New("local profile: password cannot be empty")
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return errors.New("local profile: failed to encode secret material")
	}
	if err := k.provider.Set(k.service, ref, string(encoded)); err != nil {
		// Never expose raw error with secret material.
		return fmt.Errorf("%w: keyring unavailable", ErrCredentialUnavailable)
	}
	return nil
}

func (k *KeyringStore) Delete(_ context.Context, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("%w: empty reference", ErrCredentialNotFound)
	}
	err := k.provider.Delete(k.service, ref)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("%w: reference %q not found in keyring", ErrCredentialNotFound, ref)
		}
		// Never expose raw keyring errors that might contain sensitive detail.
		return fmt.Errorf("%w: keyring unavailable", ErrCredentialUnavailable)
	}
	return nil
}

// FileSecretStore implements SecretStore backed by an owner-only JSON file (mode 0600).
type FileSecretStore struct {
	path string
}

// NewFileSecretStore creates a FileSecretStore targeting the given path (or DefaultCredentialPath if empty).
func NewFileSecretStore(path string) *FileSecretStore {
	return &FileSecretStore{path: path}
}

func (f *FileSecretStore) Source() string {
	return CredentialSourceFile
}

func (f *FileSecretStore) resolvePath() (string, error) {
	if f.path != "" {
		return f.path, nil
	}
	return DefaultCredentialPath()
}

func (f *FileSecretStore) Get(_ context.Context, ref string) (SecretMaterial, error) {
	path, err := f.resolvePath()
	if err != nil {
		return SecretMaterial{}, err
	}
	raw, err := readSecureCredentialFile(path, ref)
	if err != nil {
		return SecretMaterial{}, err
	}
	return parseRawSecret(raw, ref)
}

func (f *FileSecretStore) Put(_ context.Context, ref string, material SecretMaterial) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("local profile: credential reference cannot be empty")
	}
	if material.Password == "" {
		return errors.New("local profile: password cannot be empty")
	}
	path, err := f.resolvePath()
	if err != nil {
		return err
	}
	entries := map[string]json.RawMessage{}
	info, statErr := os.Stat(path)
	if statErr == nil {
		if info.IsDir() {
			return fmt.Errorf("%w: %s is a directory", ErrCredentialUnavailable, path)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			return fmt.Errorf("%w: %s is mode %04o; run `chmod 600 %s`", ErrCredentialFilePermissive, path, mode, path)
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("%w: %s could not be read", ErrCredentialUnavailable, path)
		}
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(raw, &parsed); err != nil || parsed == nil {
			return fmt.Errorf("%w: %s is not a JSON object", ErrCredentialUnavailable, path)
		}
		entries = parsed
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("%w: %s could not be read", ErrCredentialUnavailable, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("%w: could not create directory %s", ErrProfileInvalid, filepath.Dir(path))
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return errors.New("local profile: failed to encode secret material")
	}
	entries[ref] = json.RawMessage(encoded)

	marshaled, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return errors.New("local profile: failed to format credential file")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("%w: could not write %s", ErrProfileInvalid, path)
	}
	defer file.Close()
	if _, err := file.Write(append(marshaled, '\n')); err != nil {
		return fmt.Errorf("%w: could not write %s", ErrProfileInvalid, path)
	}
	return nil
}

func (f *FileSecretStore) Delete(_ context.Context, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return fmt.Errorf("%w: empty reference", ErrCredentialNotFound)
	}
	path, err := f.resolvePath()
	if err != nil {
		return err
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrCredentialNotFound, path)
		}
		return statErr
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%w: %s is mode %04o; run `chmod 600 %s`", ErrCredentialFilePermissive, path, mode, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	entries := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("%w: %s is not a JSON object", ErrCredentialUnavailable, path)
	}
	if _, ok := entries[ref]; !ok {
		return fmt.Errorf("%w: %s defines no reference %q", ErrCredentialNotFound, path, ref)
	}
	delete(entries, ref)

	marshaled, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("%w: could not write %s", ErrProfileInvalid, path)
	}
	defer file.Close()
	if _, err := file.Write(append(marshaled, '\n')); err != nil {
		return fmt.Errorf("%w: could not write %s", ErrProfileInvalid, path)
	}
	return nil
}

// DefaultCredentialPath is the fallback file path when AERP_CREDENTIAL_FILE is unset.
func DefaultCredentialPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(CredentialFileVariable)); override != "" {
		return override, nil
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "aerp", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: no home directory, and %s is unset", ErrCredentialUnavailable, CredentialFileVariable)
	}
	return filepath.Join(home, ".config", "aerp", "credentials.json"), nil
}

func parseRawSecret(raw, ref string) (SecretMaterial, error) {
	var material SecretMaterial
	var document struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(raw), &document) == nil && document.Password != "" {
		material.Password = document.Password
		material.Login = document.Login
	} else {
		material.Password = raw
	}
	if material.Password == "" {
		return SecretMaterial{}, fmt.Errorf("%w: reference %q resolved to an empty password", ErrCredentialUnavailable, ref)
	}
	return material, nil
}

func readSecureCredentialFile(path, credentialRef string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: reference %q — set %s, or point %s at a credential file",
			ErrCredentialUnavailable, credentialRef, EnvVariableFor(credentialRef), CredentialFileVariable)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%w: reference %q — %s names %s, which could not be read",
			ErrCredentialUnavailable, credentialRef, CredentialFileVariable, path)
	}
	// 0o077 is every group and other bit.
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return "", fmt.Errorf("%w: %s is mode %04o; run `chmod 600 %s`", ErrCredentialFilePermissive, path, mode, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%w: reference %q — %s could not be read", ErrCredentialUnavailable, credentialRef, path)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", fmt.Errorf("%w: %s is not a JSON object of credential references", ErrCredentialUnavailable, path)
	}
	entry, ok := entries[credentialRef]
	if !ok {
		return "", fmt.Errorf("%w: %s defines no reference %q", ErrCredentialUnavailable, path, credentialRef)
	}
	var asString string
	if json.Unmarshal(entry, &asString) == nil {
		return asString, nil
	}
	return string(entry), nil
}

var (
	defaultKeyringStoreMu sync.RWMutex
	defaultKeyringStore   SecretStore = NewKeyringStore(KeyringService, nil)
)

// SetDefaultKeyringStore overrides the default keyring store (e.g. with a fake in tests).
// Returns a cleanup function that restores the prior store.
func SetDefaultKeyringStore(s SecretStore) func() {
	defaultKeyringStoreMu.Lock()
	old := defaultKeyringStore
	defaultKeyringStore = s
	defaultKeyringStoreMu.Unlock()
	return func() {
		defaultKeyringStoreMu.Lock()
		defaultKeyringStore = old
		defaultKeyringStoreMu.Unlock()
	}
}

// GetDefaultKeyringStore returns the active default keyring store.
func GetDefaultKeyringStore() SecretStore {
	defaultKeyringStoreMu.RLock()
	defer defaultKeyringStoreMu.RUnlock()
	return defaultKeyringStore
}

// DefaultFileStore returns a FileSecretStore targeting the default credential path.
func DefaultFileStore() *FileSecretStore {
	return NewFileSecretStore("")
}

// PutCredential stores the secret material for ref. It tries Keyring first.
// If Keyring is unavailable, it falls back to the secure file store (mode 0600).
// It returns the selected source name ("keyring" or "file").
func PutCredential(ctx context.Context, ref string, material SecretMaterial) (string, error) {
	keyring := GetDefaultKeyringStore()
	if keyring != nil {
		err := keyring.Put(ctx, ref, material)
		if err == nil {
			return CredentialSourceKeyring, nil
		}
		// Keyring is unavailable; fall through to secure file store.
	}
	fileStore := DefaultFileStore()
	if err := fileStore.Put(ctx, ref, material); err != nil {
		return "", err
	}
	return CredentialSourceFile, nil
}

// DeleteCredential removes the secret material for ref from stored sources.
func DeleteCredential(ctx context.Context, ref string) (string, error) {
	return DeleteCredentialWithStores(ctx, ref, GetDefaultKeyringStore(), DefaultFileStore())
}

// DeleteCredentialWithStores removes the secret material for ref from the given stores.
// It attempts deletion from all configured sources (keyring and file).
// Keyring ErrCredentialUnavailable is treated as an optional source:
// if file deletion succeeds, file success is returned; if no source is deleted,
// a sanitized unavailable error is returned.
// Real store errors (such as a hard keyring error or a permissive/invalid credential file)
// make deletion incomplete, returning an error rather than reporting REMOVED even if another
// store was deleted.
// If all sources are absent without errors, ErrCredentialNotFound is returned.
func DeleteCredentialWithStores(ctx context.Context, ref string, keyringStore SecretStore, fileStore SecretStore) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("%w: empty reference", ErrCredentialNotFound)
	}

	var sources []string
	var keyringErr error
	var keyringUnavailable bool
	var keyringHardErr error

	var fileErr error

	if keyringStore != nil {
		if err := keyringStore.Delete(ctx, ref); err == nil {
			sources = append(sources, CredentialSourceKeyring)
		} else if errors.Is(err, ErrCredentialNotFound) {
			// absent from keyring
		} else if errors.Is(err, ErrCredentialUnavailable) {
			keyringUnavailable = true
			keyringErr = err
		} else {
			keyringHardErr = err
			keyringErr = err
		}
	}

	if fileStore != nil {
		if err := fileStore.Delete(ctx, ref); err == nil {
			sources = append(sources, CredentialSourceFile)
		} else if errors.Is(err, ErrCredentialNotFound) {
			// absent from file
		} else {
			fileErr = err
		}
	}

	if keyringHardErr != nil || fileErr != nil {
		if keyringHardErr != nil && fileErr != nil {
			return "", errors.Join(keyringHardErr, fileErr)
		}
		if keyringHardErr != nil {
			return "", keyringHardErr
		}
		return "", fileErr
	}

	if len(sources) > 0 {
		return sources[0], nil
	}

	if keyringUnavailable {
		if keyringErr != nil {
			return "", keyringErr
		}
		return "", fmt.Errorf("%w: keyring unavailable", ErrCredentialUnavailable)
	}

	return "", fmt.Errorf("%w: %q", ErrCredentialNotFound, ref)
}
