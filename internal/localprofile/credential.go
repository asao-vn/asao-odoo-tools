package localprofile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// CREDENTIAL RESOLUTION.
//
// The rule this file exists to enforce is one line long: a password is never written into, read
// from, or printed by anything that describes a profile. Configuration says WHICH credential; this
// says how to fetch it, at the last possible moment, straight into the RPC client.
//
// Three sources, in a fixed order, all of which keep material out of the profile document:
//
//  1. AERP_CREDENTIAL_<REF>   per-reference environment variable. Wins over everything, so CI runners
//     or shells can supply a secret without touching disk or keyring.
//  2. OS keyring service      service "asao-odoo", with account matching the credential reference.
//  3. AERP_CREDENTIAL_FILE    a JSON object mapping reference -> material, refused unless it is
//     readable by its owner alone (mode 0600).
//
// Material may be a plain string (the password, with the login coming from the profile) or a JSON
// object {"login":...,"password":...}. The object form exists so that the material for a standalone
// profile is byte-identical to what a governed ProviderConnection's credentialRef resolves to,
// which is what lets an operator who already holds working credentials for an ASAO-managed Odoo use
// them here without reformatting anything.

// Material is an alias for SecretMaterial for backward compatibility.
type Material = SecretMaterial

// EnvVariableFor reports which environment variable holds a reference's material. It is PUBLISHED
// by diagnostics — an operator must be able to learn which variable to set without guessing the
// mangling rule, and the variable NAME is not a secret.
func EnvVariableFor(credentialRef string) string {
	var builder strings.Builder
	builder.WriteString(CredentialEnvPrefix)
	for _, r := range strings.ToUpper(credentialRef) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

// WebCredentialRef is the reference of a profile's web password: the profile's own reference with
// ".web" (AERP_CREDENTIAL_<REF>_WEB, keyring account <ref>.web, file key <ref>.web). A REPORT
// operation logs a web session in with it, since Odoo serves report PDFs to no API key.
func WebCredentialRef(profile Profile) string {
	return profile.CredentialRef + ".web"
}

// ResolveWebCredential fetches a profile's web password, from the same sources in the same order.
func ResolveWebCredential(profile Profile) (SecretMaterial, error) {
	web := profile
	web.CredentialRef = WebCredentialRef(profile)
	return ResolveCredential(web)
}

// ResolveCredential fetches the material for one profile.
func ResolveCredential(profile Profile) (SecretMaterial, error) {
	material, _, err := ResolveCredentialWithSource(profile)
	return material, err
}

// ResolveCredentialWithSource fetches material and the selected source name ("environment", "keyring", or "file").
func ResolveCredentialWithSource(profile Profile) (SecretMaterial, string, error) {
	return ResolveCredentialWithStores(context.Background(), profile, os.Getenv, GetDefaultKeyringStore(), DefaultFileStore())
}

// ResolveCredentialWithStores resolves credentials following the fixed precedence order:
// 1. AERP_CREDENTIAL_<REF> environment variable
// 2. OS keyring service asao-odoo
// 3. AERP_CREDENTIAL_FILE (mode 0600)
func ResolveCredentialWithStores(ctx context.Context, profile Profile, envLookup func(string) string, keyringStore SecretStore, fileStore SecretStore) (SecretMaterial, string, error) {
	if strings.TrimSpace(profile.CredentialRef) == "" {
		return SecretMaterial{}, "", fmt.Errorf("%w: no credential reference", ErrCredentialUnavailable)
	}

	// 1. Environment variable wins over everything
	if envLookup == nil {
		envLookup = os.Getenv
	}
	envVar := EnvVariableFor(profile.CredentialRef)
	if raw := envLookup(envVar); raw != "" {
		material, err := parseRawSecret(raw, profile.CredentialRef)
		if err != nil {
			return SecretMaterial{}, "", err
		}
		material, err = applyProfileLogin(material, profile)
		if err != nil {
			return SecretMaterial{}, "", err
		}
		return material, CredentialSourceEnvironment, nil
	}

	// 2. OS Keyring (service asao-odoo)
	if keyringStore != nil {
		material, err := keyringStore.Get(ctx, profile.CredentialRef)
		if err == nil && material.Password != "" {
			material, err = applyProfileLogin(material, profile)
			if err != nil {
				return SecretMaterial{}, "", err
			}
			return material, CredentialSourceKeyring, nil
		}
	}

	// 3. Credential file (mode 0600; reject group/other bits)
	if fileStore != nil {
		material, err := fileStore.Get(ctx, profile.CredentialRef)
		if err != nil {
			if errors.Is(err, ErrCredentialFilePermissive) {
				return SecretMaterial{}, "", err
			}
		} else if material.Password != "" {
			material, err = applyProfileLogin(material, profile)
			if err != nil {
				return SecretMaterial{}, "", err
			}
			return material, CredentialSourceFile, nil
		}
	}

	return SecretMaterial{}, "", fmt.Errorf("%w: reference %q — set %s, or point %s at a credential file",
		ErrCredentialUnavailable, profile.CredentialRef, EnvVariableFor(profile.CredentialRef), CredentialFileVariable)
}

func applyProfileLogin(material SecretMaterial, profile Profile) (SecretMaterial, error) {
	if material.Login == "" {
		material.Login = profile.Login
	}
	if material.Login == "" {
		return SecretMaterial{}, fmt.Errorf("%w: reference %q resolved no odoo login, and profile %q records none",
			ErrCredentialUnavailable, profile.CredentialRef, profile.Name)
	}
	if material.Password == "" {
		return SecretMaterial{}, fmt.Errorf("%w: reference %q resolved to an empty password",
			ErrCredentialUnavailable, profile.CredentialRef)
	}
	return material, nil
}

// CredentialAvailable reports whether a reference resolves, WITHOUT returning what it resolved to.
func CredentialAvailable(profile Profile) bool {
	_, err := ResolveCredential(profile)
	return err == nil
}

// CredentialStatus reports the resolved source name and whether the credential resolves.
func CredentialStatus(profile Profile) (string, bool) {
	_, source, err := ResolveCredentialWithSource(profile)
	if err != nil {
		return "", false
	}
	return source, true
}
