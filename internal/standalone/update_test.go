package standalone_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/release"
	"github.com/asao-platform/atoc/packages/cli/internal/standalone"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

func generateTestKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate Ed25519 key: %v", err)
	}
	return pub, priv
}

func signData(data []byte, priv ed25519.PrivateKey) string {
	sig := ed25519.Sign(priv, data)
	return base64.StdEncoding.EncodeToString(sig)
}

func hashData(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// makeExecutableFixture creates a script that mocks the Odoo binary responding to `version --output json`.
func makeExecutableFixture(t *testing.T, dir, filename, version, prod, catID, semID, buildID string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	content := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "version" ]; then
  echo '{"apiVersion":"cli.asao/v1","kind":"result","command":"version","status":"OK","data":{"program":"odoo","product":"%s","cliVersion":"%s","os":"%s","arch":"%s","catalogIdentity":"%s","semanticContractIdentity":"%s","executionBuildIdentity":"%s"}}'
  exit 0
fi
echo "unsupported command" >&2
exit 1
`, prod, version, runtime.GOOS, runtime.GOARCH, catID, semID, buildID)

	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("failed to create fixture executable: %v", err)
	}
	return path
}

func makeCustomExecutableFixture(t *testing.T, dir, filename, jsonResponse string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	content := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "version" ]; then
  echo '%s'
  exit 0
fi
echo "unsupported command" >&2
exit 1
`, jsonResponse)

	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("failed to create custom fixture executable: %v", err)
	}
	return path
}

func TestVersionOutputFieldsAndProductIdentity(t *testing.T) {
	for _, prog := range []string{"odoo", "aerp"} {
		t.Run("program_"+prog, func(t *testing.T) {
			out, diag := &bytes.Buffer{}, &bytes.Buffer{}
			code := clicore.Run(context.Background(), standalone.RootFor(prog), []string{"version", "--output", "json"}, clicore.Streams{Out: out, Diagnostics: diag})
			if code != clicore.ExitOK {
				t.Fatalf("version exit %d: %s", code, diag.String())
			}

			data := decode(t, out)
			if data["program"] != prog {
				t.Errorf("program = %v, want %s", data["program"], prog)
			}
			// Both binaries must intentionally publish STANDALONE_AERP as the canonical product identity
			if data["product"] != "STANDALONE_AERP" {
				t.Errorf("product = %v, want STANDALONE_AERP", data["product"])
			}

			requiredFields := []string{
				"program",
				"product",
				"cliVersion",
				"apiVersion",
				"configSchema",
				"buildCommit",
				"buildDate",
				"catalogIdentity",
				"semanticContractIdentity",
				"executionBuildIdentity",
				"os",
				"arch",
			}
			for _, f := range requiredFields {
				val, ok := data[f]
				if !ok || val == "" {
					t.Errorf("missing or empty field %q in version output", f)
				}
			}

			if data["os"] != runtime.GOOS {
				t.Errorf("os = %v, want %s", data["os"], runtime.GOOS)
			}
			if data["arch"] != runtime.GOARCH {
				t.Errorf("arch = %v, want %s", data["arch"], runtime.GOARCH)
			}
		})
	}
}

func TestUpdate_MissingManifestConfiguration(t *testing.T) {
	t.Setenv("ODOO_MANIFEST_URL", "")
	t.Setenv("AERP_MANIFEST_URL", "")

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{"update", "--check", "--output", "json"}, clicore.Streams{Out: out, Diagnostics: diag})
	if code != clicore.ExitConfig {
		t.Fatalf("exit = %d, want ExitConfig (%d)", code, clicore.ExitConfig)
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("failed to decode error envelope: %v\n%s", err, out.String())
	}
	if envelope.Error == nil || envelope.Error.Code != "MANIFEST_URL_REQUIRED" {
		t.Fatalf("error code = %v, want MANIFEST_URL_REQUIRED", envelope.Error)
	}
}

func TestUpdateCheck_DoesNotMutateTarget(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "odoo")
	initialContent := []byte("original binary content")
	if err := os.WriteFile(targetPath, initialContent, 0755); err != nil {
		t.Fatalf("failed to write initial target: %v", err)
	}

	compat := direct.CatalogProvenance().CompatibilityIdentity
	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	dummyBinary := []byte("#!/bin/sh\necho updated\n")
	artifactSig := signData(dummyBinary, priv)
	artifactHash := hashData(dummyBinary)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "9.9.9",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/bin",
					SHA256:                   artifactHash,
					Signature:                artifactSig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--check",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code != clicore.ExitOK {
		t.Fatalf("update --check exit = %d: %s", code, diag.String())
	}

	data := decode(t, out)
	if data["updateAvailable"] != true {
		t.Fatalf("updateAvailable = %v, want true", data["updateAvailable"])
	}
	if data["availableVersion"] != "9.9.9" {
		t.Fatalf("availableVersion = %v, want 9.9.9", data["availableVersion"])
	}

	// Target file must NOT have been mutated
	actualContent, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("failed to read target: %v", err)
	}
	if !bytes.Equal(actualContent, initialContent) {
		t.Fatal("target file was unexpectedly mutated during check")
	}

	// No backups or temporary files should exist
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read temp dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "odoo" {
		t.Fatalf("unexpected files in target dir: %v", entries)
	}
}

func TestUpdate_AtomicSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	// Initial binary fixture (version 1.0.0)
	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	// New binary fixture (version 2.0.0)
	newBinaryDir := t.TempDir()
	newBinaryPath := makeExecutableFixture(t, newBinaryDir, "odoo-new", "2.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	newBinaryBytes, _ := os.ReadFile(newBinaryPath)
	newBinaryHash := hashData(newBinaryBytes)
	newBinarySig := signData(newBinaryBytes, priv)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(newBinaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   newBinaryHash,
					Signature:                newBinarySig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code != clicore.ExitOK {
		t.Fatalf("update exit = %d: %s\nout: %s", code, diag.String(), out.String())
	}

	data := decode(t, out)
	if data["updated"] != true {
		t.Fatalf("updated = %v, want true", data["updated"])
	}
	if data["currentVersion"] != "2.0.0" {
		t.Fatalf("currentVersion = %v, want 2.0.0", data["currentVersion"])
	}

	// Verify target was replaced with new binary bytes
	updatedContent, _ := os.ReadFile(targetPath)
	if !bytes.Equal(updatedContent, newBinaryBytes) {
		t.Fatal("target was not updated with new binary contents")
	}

	// Verify versioned backup exists and retains initial content
	backupPath := data["backupPath"].(string)
	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("backup file %q does not exist: %v", backupPath, err)
	}
	if !bytes.Equal(backupContent, initialContent) {
		t.Fatal("backup content does not match original binary")
	}

	// Verify no temporary .tmp files left over
	entries, _ := os.ReadDir(tmpDir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".odoo-update-") {
			t.Fatalf("found leftover temp file: %s", entry.Name())
		}
	}
}

func TestUpdate_HashMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	correctBytes := []byte("#!/bin/sh\necho correct\n")
	tamperedBytes := []byte("#!/bin/sh\necho tampered\n")
	sig := signData(correctBytes, priv)
	hash := hashData(correctBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(tamperedBytes) // serve tampered content
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatalf("expected error on hash mismatch, got exit OK")
	}

	// Target remains intact
	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target was unexpectedly modified on hash failure")
	}

	// Temp files cleaned up
	entries, _ := os.ReadDir(tmpDir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".odoo-update-") {
			t.Fatalf("leftover temp file found: %s", entry.Name())
		}
	}
}

func TestUpdate_SignatureMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, _ := generateTestKeyPair(t)
	_, untrustedPriv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	binaryBytes := []byte("#!/bin/sh\necho updated\n")
	untrustedSig := signData(binaryBytes, untrustedPriv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(binaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                untrustedSig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected failure on untrusted signature")
	}

	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target modified despite invalid signature")
	}
}

func TestUpdate_CatalogIdentityMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	// Fixture outputs a DIFFERENT catalog identity
	newBinaryDir := t.TempDir()
	wrongCatID := strings.Repeat("f", 64)
	mismatchedBinaryPath := makeExecutableFixture(t, newBinaryDir, "odoo-wrong", "2.0.0", "STANDALONE_AERP",
		wrongCatID, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	binaryBytes, _ := os.ReadFile(mismatchedBinaryPath)
	sig := signData(binaryBytes, priv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(binaryBytes)
			return
		}
		// Manifest declared the expected catalogIdentity, but binary outputs wrongCatID
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected failure on catalog identity mismatch")
	}

	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target modified despite catalog identity mismatch")
	}
}

func TestUpdate_RefusesSystemPaths(t *testing.T) {
	for _, sysPath := range []string{"/usr/local/bin/odoo", "/usr/bin/odoo", "/bin/odoo"} {
		out, diag := &bytes.Buffer{}, &bytes.Buffer{}
		code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
			"update",
			"--manifest-url", "https://releases.example.com/manifest.json",
			"--target", sysPath,
			"--output", "json",
		}, clicore.Streams{Out: out, Diagnostics: diag})

		if code != clicore.ExitDenied {
			t.Errorf("path %s: exit = %d, want ExitDenied (%d)", sysPath, code, clicore.ExitDenied)
		}

		var envelope struct {
			Error *clicore.Error `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatalf("failed to decode envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "SYSTEM_INSTALLATION_PATH_REFUSED" {
			t.Fatalf("path %s: error code = %v, want SYSTEM_INSTALLATION_PATH_REFUSED", sysPath, envelope.Error)
		}
		if !strings.Contains(envelope.Error.Remediation, "installer") {
			t.Errorf("remediation %q does not mention rerun installer", envelope.Error.Remediation)
		}
	}
}

func TestUpdate_NoSecretLeakage(t *testing.T) {
	t.Setenv("AERP_CREDENTIAL_LAB", "super_secret_password_12345")
	t.Setenv("ODOO_PASSWORD", "odoo_top_secret_token_67890")

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	_ = clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", "https://invalid.example.com/nonexistent",
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	combined := out.String() + diag.String()
	for _, secret := range []string{"super_secret_password_12345", "odoo_top_secret_token_67890"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("detected secret leakage in command output: %s", secret)
		}
	}
}

func TestUpdate_ProbeVersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	// Downloaded binary reports version 2.1.0, but manifest claims 2.0.0
	mismatchedBinaryDir := t.TempDir()
	mismatchedPath := makeExecutableFixture(t, mismatchedBinaryDir, "odoo-mismatch", "2.1.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	binaryBytes, _ := os.ReadFile(mismatchedPath)
	sig := signData(binaryBytes, priv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(binaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected update to fail on cliVersion mismatch, got exit OK")
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || !strings.Contains(envelope.Error.Message, "cliVersion mismatch") {
		t.Fatalf("expected cliVersion mismatch error, got: %v", envelope.Error)
	}

	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target file was unexpectedly modified on probe version mismatch")
	}
}

func TestUpdate_ProbeProductMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	// Downloaded binary reports STANDALONE_ODOO (not the accepted canonical STANDALONE_AERP)
	mismatchedBinaryDir := t.TempDir()
	mismatchedPath := makeExecutableFixture(t, mismatchedBinaryDir, "odoo-wrong-prod", "2.0.0", "STANDALONE_ODOO",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	binaryBytes, _ := os.ReadFile(mismatchedPath)
	sig := signData(binaryBytes, priv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(binaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected update to fail on product mismatch, got exit OK")
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || !strings.Contains(envelope.Error.Message, "product mismatch") {
		t.Fatalf("expected product mismatch error, got: %v", envelope.Error)
	}

	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target file was unexpectedly modified on probe product mismatch")
	}
}

func TestUpdate_ProbeEnvelopeStatusMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	// Downloaded binary returns status != OK
	mismatchedBinaryDir := t.TempDir()
	customJSON := fmt.Sprintf(`{"apiVersion":"cli.asao/v1","kind":"result","command":"version","status":"ERROR","data":{"program":"odoo","product":"STANDALONE_AERP","cliVersion":"2.0.0","catalogIdentity":"%s","semanticContractIdentity":"%s","executionBuildIdentity":"%s"}}`,
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	mismatchedPath := makeCustomExecutableFixture(t, mismatchedBinaryDir, "odoo-err-status", customJSON)
	binaryBytes, _ := os.ReadFile(mismatchedPath)
	sig := signData(binaryBytes, priv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(binaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected update to fail on non-OK envelope status, got exit OK")
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || !strings.Contains(envelope.Error.Message, "non-OK status") {
		t.Fatalf("expected non-OK status error, got: %v", envelope.Error)
	}

	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target file was unexpectedly modified on probe status mismatch")
	}
}

func TestUpdate_ProbePlatformMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	// Downloaded binary claims os is darwin when running on linux
	wrongOS := "darwin"
	if runtime.GOOS == "darwin" {
		wrongOS = "linux"
	}
	mismatchedBinaryDir := t.TempDir()
	customJSON := fmt.Sprintf(`{"apiVersion":"cli.asao/v1","kind":"result","command":"version","status":"OK","data":{"program":"odoo","product":"STANDALONE_AERP","cliVersion":"2.0.0","os":"%s","arch":"%s","catalogIdentity":"%s","semanticContractIdentity":"%s","executionBuildIdentity":"%s"}}`,
		wrongOS, runtime.GOARCH, compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	mismatchedPath := makeCustomExecutableFixture(t, mismatchedBinaryDir, "odoo-wrong-os", customJSON)
	binaryBytes, _ := os.ReadFile(mismatchedPath)
	sig := signData(binaryBytes, priv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(binaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected update to fail on platform mismatch, got exit OK")
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || !strings.Contains(envelope.Error.Message, "os mismatch") {
		t.Fatalf("expected os mismatch error, got: %v", envelope.Error)
	}

	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target file was unexpectedly modified on platform mismatch")
	}
}

func TestUpdate_ManifestProductMismatch(t *testing.T) {
	compat := direct.CatalogProvenance().CompatibilityIdentity

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Manifest specifies Product other than STANDALONE_AERP
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"schema": "%s",
			"product": "STANDALONE_ODOO",
			"channel": "stable",
			"releases": [{
				"version": "2.0.0",
				"os": "%s",
				"arch": "%s",
				"url": "http://%s/bin",
				"sha256": "%s",
				"signature": "%s",
				"catalogIdentity": "%s",
				"semanticContractIdentity": "%s",
				"executionBuildIdentity": "%s"
			}]
		}`, release.ManifestSchema, runtime.GOOS, runtime.GOARCH, r.Host,
			strings.Repeat("0", 64), base64.StdEncoding.EncodeToString(make([]byte, 64)),
			compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)))
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--check",
		"--manifest-url", server.URL,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected manifest product mismatch to fail, got exit OK")
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || (!strings.Contains(envelope.Error.Code, "MANIFEST") && !strings.Contains(envelope.Error.Message, "product")) {
		t.Fatalf("expected product mismatch error code, got: %v", envelope.Error)
	}
}

func TestUpdate_RefusesSymlinkTarget(t *testing.T) {
	tmpDir := t.TempDir()
	realPath := filepath.Join(tmpDir, "real-odoo")
	initialContent := []byte("real-binary")
	if err := os.WriteFile(realPath, initialContent, 0755); err != nil {
		t.Fatalf("failed to write real target: %v", err)
	}

	symlinkPath := filepath.Join(tmpDir, "symlink-odoo")
	if err := os.Symlink(realPath, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", "https://releases.example.com/manifest.json",
		"--target", symlinkPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code != clicore.ExitDenied {
		t.Fatalf("expected ExitDenied (%d), got exit = %d: %s", clicore.ExitDenied, code, diag.String())
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || envelope.Error.Code != "TARGET_IS_SYMLINK" {
		t.Fatalf("expected TARGET_IS_SYMLINK error code, got: %v", envelope.Error)
	}

	// Verify real binary was not touched
	afterContent, _ := os.ReadFile(realPath)
	if !bytes.Equal(afterContent, initialContent) {
		t.Fatal("real binary modified through symlink")
	}
}

func TestUpdate_RefusesNonRegularTarget(t *testing.T) {
	tmpDir := t.TempDir()
	dirTarget := filepath.Join(tmpDir, "odoo-dir")
	if err := os.Mkdir(dirTarget, 0755); err != nil {
		t.Fatalf("failed to create directory target: %v", err)
	}

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", "https://releases.example.com/manifest.json",
		"--target", dirTarget,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code != clicore.ExitDenied {
		t.Fatalf("expected ExitDenied (%d), got exit = %d: %s", clicore.ExitDenied, code, diag.String())
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || envelope.Error.Code != "TARGET_NOT_REGULAR_FILE" {
		t.Fatalf("expected TARGET_NOT_REGULAR_FILE error code, got: %v", envelope.Error)
	}
}

func TestUpdate_DownloadOversizedStream(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			// Advertise Content-Length exceeding MaxArtifactSizeBytes
			w.Header().Set("Content-Length", fmt.Sprintf("%d", release.MaxArtifactSizeBytes+1024))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("oversized dummy"))
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   strings.Repeat("0", 64),
					Signature:                signData([]byte("dummy"), priv),
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", server.URL,
		"--target", targetPath,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code == clicore.ExitOK {
		t.Fatal("expected download of oversized artifact to fail, got exit OK")
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || envelope.Error.Code != "ARTIFACT_DOWNLOAD_FAILED" {
		t.Fatalf("expected ARTIFACT_DOWNLOAD_FAILED error code, got: %v", envelope.Error)
	}

	// Target remains untouched
	targetAfter, _ := os.ReadFile(targetPath)
	if !bytes.Equal(targetAfter, initialContent) {
		t.Fatal("target was unexpectedly modified on oversized artifact download")
	}

	// Temp files cleaned up
	entries, _ := os.ReadDir(tmpDir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".odoo-update-") {
			t.Fatalf("leftover temp file found: %s", entry.Name())
		}
	}
}

func TestUpdate_ManipulatedManifestAndBinaryAgreeingOnMismatchedIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	initialContent, _ := os.ReadFile(targetPath)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	// Both binary and manifest agree on a different catalog identity than current direct Odoo CLI
	foreignCatID := strings.Repeat("e", 64)
	foreignBinaryDir := t.TempDir()
	foreignBinaryPath := makeExecutableFixture(t, foreignBinaryDir, "odoo-foreign", "2.0.0", "STANDALONE_AERP",
		foreignCatID, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	binaryBytes, _ := os.ReadFile(foreignBinaryPath)
	sig := signData(binaryBytes, priv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/odoo" {
			_, _ = w.Write(binaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download/odoo",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          foreignCatID, // agrees with binary, differs from current CLI
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	// 1. Assert --check fails closed before reporting updateAvailable
	t.Run("check_mode_fails_closed", func(t *testing.T) {
		out, diag := &bytes.Buffer{}, &bytes.Buffer{}
		code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
			"update",
			"--check",
			"--manifest-url", server.URL,
			"--target", targetPath,
			"--output", "json",
		}, clicore.Streams{Out: out, Diagnostics: diag})

		if code != clicore.ExitConflict {
			t.Fatalf("expected ExitConflict (%d), got %d: %s", clicore.ExitConflict, code, diag.String())
		}

		var envelope struct {
			Error *clicore.Error `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatalf("failed to decode error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "COMPATIBILITY_MISMATCH" {
			t.Fatalf("error code = %v, want COMPATIBILITY_MISMATCH", envelope.Error)
		}

		// Target remains untouched
		afterContent, _ := os.ReadFile(targetPath)
		if !bytes.Equal(afterContent, initialContent) {
			t.Fatal("target modified during --check on compatibility mismatch")
		}
	})

	// 2. Assert mutating update fails closed before mutation and leaves target intact
	t.Run("mutating_update_fails_closed", func(t *testing.T) {
		out, diag := &bytes.Buffer{}, &bytes.Buffer{}
		code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
			"update",
			"--manifest-url", server.URL,
			"--target", targetPath,
			"--output", "json",
		}, clicore.Streams{Out: out, Diagnostics: diag})

		if code != clicore.ExitConflict {
			t.Fatalf("expected ExitConflict (%d), got %d: %s", clicore.ExitConflict, code, diag.String())
		}

		var envelope struct {
			Error *clicore.Error `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatalf("failed to decode error envelope: %v", err)
		}
		if envelope.Error == nil || envelope.Error.Code != "COMPATIBILITY_MISMATCH" {
			t.Fatalf("error code = %v, want COMPATIBILITY_MISMATCH", envelope.Error)
		}

		// Target remains untouched
		afterContent, _ := os.ReadFile(targetPath)
		if !bytes.Equal(afterContent, initialContent) {
			t.Fatal("target modified during mutating update on compatibility mismatch")
		}
	})
}

func TestUpdate_RedactsSecretsInURLs(t *testing.T) {
	tmpDir := t.TempDir()
	compat := direct.CatalogProvenance().CompatibilityIdentity

	targetPath := makeExecutableFixture(t, tmpDir, "odoo", "1.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)

	pub, priv := generateTestKeyPair(t)
	cleanup := release.SetPublicKeyForTesting(pub)
	defer cleanup()

	manifestSecret := "MANIFEST_SECRET_TOKEN_XYZ_12345"
	artifactSecret := "ARTIFACT_SECRET_TOKEN_ABC_67890"

	newBinaryDir := t.TempDir()
	newBinaryPath := makeExecutableFixture(t, newBinaryDir, "odoo-secret-test", "2.0.0", "STANDALONE_AERP",
		compat.CatalogIdentity, compat.SemanticContractIdentity, compat.ExecutionBuildIdentity)
	binaryBytes, _ := os.ReadFile(newBinaryPath)
	sig := signData(binaryBytes, priv)
	hash := hashData(binaryBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/download") {
			_, _ = w.Write(binaryBytes)
			return
		}
		m := release.Manifest{
			Schema:  release.ManifestSchema,
			Product: release.ProductIdentityStandaloneAERP,
			Channel: "stable",
			Releases: []release.Artifact{
				{
					Version:                  "2.0.0",
					OS:                       runtime.GOOS,
					Arch:                     runtime.GOARCH,
					URL:                      "http://" + r.Host + "/download?token=" + artifactSecret + "#artifact-frag",
					SHA256:                   hash,
					Signature:                sig,
					CatalogIdentity:          compat.CatalogIdentity,
					SemanticContractIdentity: compat.SemanticContractIdentity,
					ExecutionBuildIdentity:   compat.ExecutionBuildIdentity,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	manifestURLWithSecret := server.URL + "/manifest.json?api_key=" + manifestSecret + "#manifest-frag"

	// 1. Test update --check output and diagnostics
	t.Run("check_output_redacted", func(t *testing.T) {
		out, diag := &bytes.Buffer{}, &bytes.Buffer{}
		code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
			"update",
			"--check",
			"--manifest-url", manifestURLWithSecret,
			"--target", targetPath,
			"--output", "json",
		}, clicore.Streams{Out: out, Diagnostics: diag})

		if code != clicore.ExitOK {
			t.Fatalf("check exit = %d: %s", code, diag.String())
		}

		data := decode(t, out)
		urlVal, _ := data["url"].(string)
		if strings.Contains(urlVal, artifactSecret) || strings.Contains(urlVal, "artifact-frag") {
			t.Fatalf("artifact URL in check output exposed secret: %s", urlVal)
		}

		combined := out.String() + diag.String()
		if strings.Contains(combined, manifestSecret) {
			t.Fatalf("manifest secret exposed in check output/diagnostics: %s", combined)
		}
		if strings.Contains(combined, artifactSecret) {
			t.Fatalf("artifact secret exposed in check output/diagnostics: %s", combined)
		}
	})

	// 2. Test error output redaction on network failure
	t.Run("error_output_redacted", func(t *testing.T) {
		badURL := "https://invalid.releases.internal/manifest.json?key=" + manifestSecret + "#frag"
		out, diag := &bytes.Buffer{}, &bytes.Buffer{}
		code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
			"update",
			"--manifest-url", badURL,
			"--target", targetPath,
			"--output", "json",
		}, clicore.Streams{Out: out, Diagnostics: diag})

		if code == clicore.ExitOK {
			t.Fatal("expected failure on invalid host")
		}

		combined := out.String() + diag.String()
		if strings.Contains(combined, manifestSecret) {
			t.Fatalf("manifest secret exposed in error output/diagnostics: %s", combined)
		}
		if strings.Contains(combined, "#frag") {
			t.Fatalf("fragment exposed in error output/diagnostics: %s", combined)
		}
	})
}

func TestUpdate_RefusesSymlinkedParentDirectoryResolvingToSystemPath(t *testing.T) {
	// Find a real system binary in /usr/bin or /bin that is not a symlink itself
	var realSysPath string
	for _, candidate := range []string{"/usr/bin/env", "/usr/bin/head", "/usr/bin/tail", "/bin/echo"} {
		if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() && (info.Mode()&os.ModeSymlink == 0) {
			realSysPath = candidate
			break
		}
	}
	if realSysPath == "" {
		t.Skip("no real system binary available to test parent symlink resolution")
	}

	tmpDir := t.TempDir()
	sysParentDir := filepath.Dir(realSysPath)
	symDir := filepath.Join(tmpDir, "symlink-system-dir")
	if err := os.Symlink(sysParentDir, symDir); err != nil {
		t.Fatalf("failed to create symlink dir: %v", err)
	}

	targetViaSymlink := filepath.Join(symDir, filepath.Base(realSysPath))

	out, diag := &bytes.Buffer{}, &bytes.Buffer{}
	code := clicore.Run(context.Background(), standalone.RootFor("odoo"), []string{
		"update",
		"--manifest-url", "https://releases.example.com/manifest.json",
		"--target", targetViaSymlink,
		"--output", "json",
	}, clicore.Streams{Out: out, Diagnostics: diag})

	if code != clicore.ExitDenied {
		t.Fatalf("expected ExitDenied (%d), got exit = %d: %s", clicore.ExitDenied, code, diag.String())
	}

	var envelope struct {
		Error *clicore.Error `json:"error"`
	}
	_ = json.Unmarshal(out.Bytes(), &envelope)
	if envelope.Error == nil || envelope.Error.Code != "SYSTEM_INSTALLATION_PATH_REFUSED" {
		t.Fatalf("error code = %v, want SYSTEM_INSTALLATION_PATH_REFUSED", envelope.Error)
	}
}

func TestReleaseBuild_MismatchedKeyFailsClosed(t *testing.T) {
	repoRoot, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatalf("cannot determine repo root: %v", err)
	}

	buildScript := filepath.Join(repoRoot, "scripts/release/build-odoo.sh")
	if _, err := os.Stat(buildScript); err != nil {
		t.Skipf("build script not found at %s", buildScript)
	}

	tmpOut := t.TempDir()
	// Ephemeral 32-byte seed that generates a key differing from EmbeddedReleasePublicKeyHex
	mismatchedSeed := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

	cmd := exec.Command(buildScript, "--skip-readiness", "--out-dir", tmpOut)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "ODOO_RELEASE_SIGNING_KEY="+mismatchedSeed)

	out, err := cmd.CombinedOutput()
	outputStr := string(out)

	if err == nil {
		t.Fatalf("expected build-odoo.sh to fail closed on mismatched signing key, but exited 0!\nOutput:\n%s", outputStr)
	}

	if !strings.Contains(outputStr, "does not match embedded release public key trust root") {
		t.Fatalf("output does not mention public key mismatch with embedded trust root:\n%s", outputStr)
	}

	manifestPath := filepath.Join(tmpOut, "manifest.json")
	if _, statErr := os.Stat(manifestPath); statErr == nil {
		t.Fatalf("manifest.json was written despite signing key mismatch failure: %s", manifestPath)
	}
}
