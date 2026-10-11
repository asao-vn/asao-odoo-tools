package release_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/release"
)

func generateKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ephemeral Ed25519 key: %v", err)
	}
	return pub, priv
}

func signBytes(data []byte, priv ed25519.PrivateKey) string {
	sig := ed25519.Sign(priv, data)
	return base64.StdEncoding.EncodeToString(sig)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestLoadManifest_Success(t *testing.T) {
	_, priv := generateKeyPair(t)
	dummyBinary := []byte("#!/bin/sh\necho test\n")
	artifactSig := signBytes(dummyBinary, priv)
	artifactHash := sha256Hex(dummyBinary)

	manifestData := release.Manifest{
		Schema:  release.ManifestSchema,
		Product: release.ProductIdentityStandaloneAERP,
		Channel: "stable",
		Releases: []release.Artifact{
			{
				Version:                  "1.2.0",
				OS:                       "linux",
				Arch:                     "amd64",
				URL:                      "http://127.0.0.1:8080/odoo-linux-amd64",
				SHA256:                   artifactHash,
				Signature:                artifactSig,
				CatalogIdentity:          strings.Repeat("a", 64),
				SemanticContractIdentity: "contract-v1",
				ExecutionBuildIdentity:   "build-v1",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manifestData)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m, err := release.LoadManifest(ctx, server.URL)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}

	if m.Schema != release.ManifestSchema {
		t.Fatalf("Schema = %v, want %v", m.Schema, release.ManifestSchema)
	}
	if len(m.Releases) != 1 {
		t.Fatalf("Releases count = %d, want 1", len(m.Releases))
	}

	rel, err := m.FindRelease("stable", "linux", "amd64")
	if err != nil {
		t.Fatalf("FindRelease failed: %v", err)
	}
	if rel.Version != "1.2.0" {
		t.Fatalf("Version = %v, want 1.2.0", rel.Version)
	}
}

func TestLoadManifest_RejectsInsecureExternalHTTP(t *testing.T) {
	ctx := context.Background()
	_, err := release.LoadManifest(ctx, "http://example.com/manifest.json")
	if err == nil {
		t.Fatal("expected error for non-HTTPS, non-loopback URL, got nil")
	}
	if !strings.Contains(err.Error(), "insecure non-HTTPS") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestLoadManifest_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := release.LoadManifest(ctx, server.URL)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestManifestValidation_StrictRules(t *testing.T) {
	validArtifact := release.Artifact{
		Version:                  "1.0.0",
		OS:                       "linux",
		Arch:                     "amd64",
		URL:                      "https://releases.example.com/bin",
		SHA256:                   strings.Repeat("0", 64),
		Signature:                base64.StdEncoding.EncodeToString(make([]byte, 64)),
		CatalogIdentity:          strings.Repeat("b", 64),
		SemanticContractIdentity: "contract",
		ExecutionBuildIdentity:   "build",
	}

	tests := []struct {
		name      string
		modify    func(m *release.Manifest)
		wantError string
	}{
		{
			name: "malformed schema",
			modify: func(m *release.Manifest) {
				m.Schema = "bad.schema/v1"
			},
			wantError: "invalid manifest schema",
		},
		{
			name: "invalid product",
			modify: func(m *release.Manifest) {
				m.Product = "SOME_OTHER_PRODUCT"
			},
			wantError: "invalid manifest product",
		},
		{
			name: "empty channel",
			modify: func(m *release.Manifest) {
				m.Channel = ""
			},
			wantError: "invalid manifest channel",
		},
		{
			name: "empty releases list",
			modify: func(m *release.Manifest) {
				m.Releases = nil
			},
			wantError: "manifest releases list must not be empty",
		},
		{
			name: "duplicate release entry",
			modify: func(m *release.Manifest) {
				m.Releases = []release.Artifact{validArtifact, validArtifact}
			},
			wantError: "duplicate or ambiguous release entry",
		},
		{
			name: "malformed semver",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.Version = "invalid-version"
				m.Releases = []release.Artifact{bad}
			},
			wantError: "malformed semver",
		},
		{
			name: "unsupported OS",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.OS = "solaris"
				m.Releases = []release.Artifact{bad}
			},
			wantError: "unsupported operating system",
		},
		{
			name: "unsupported arch",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.Arch = "mips"
				m.Releases = []release.Artifact{bad}
			},
			wantError: "unsupported architecture",
		},
		{
			name: "invalid lowercase sha256 (uppercase)",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.SHA256 = strings.ToUpper(strings.Repeat("a", 64))
				m.Releases = []release.Artifact{bad}
			},
			wantError: "invalid SHA-256",
		},
		{
			name: "invalid sha256 length",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.SHA256 = strings.Repeat("a", 63)
				m.Releases = []release.Artifact{bad}
			},
			wantError: "invalid SHA-256",
		},
		{
			name: "invalid base64 signature",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.Signature = "not-base64!"
				m.Releases = []release.Artifact{bad}
			},
			wantError: "invalid base64 Ed25519 signature",
		},
		{
			name: "unsafe URL with userinfo",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.URL = "https://user:password@example.com/bin"
				m.Releases = []release.Artifact{bad}
			},
			wantError: "must not contain user credentials",
		},
		{
			name: "empty catalogIdentity",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.CatalogIdentity = ""
				m.Releases = []release.Artifact{bad}
			},
			wantError: "invalid catalogIdentity",
		},
		{
			name: "empty semanticContractIdentity",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.SemanticContractIdentity = ""
				m.Releases = []release.Artifact{bad}
			},
			wantError: "semanticContractIdentity must not be empty",
		},
		{
			name: "empty executionBuildIdentity",
			modify: func(m *release.Manifest) {
				bad := validArtifact
				bad.ExecutionBuildIdentity = ""
				m.Releases = []release.Artifact{bad}
			},
			wantError: "executionBuildIdentity must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := release.Manifest{
				Schema:   release.ManifestSchema,
				Product:  release.ProductIdentityStandaloneAERP,
				Channel:  "stable",
				Releases: []release.Artifact{validArtifact},
			}
			tc.modify(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantError)
			}
		})
	}
}

func TestVerifyArtifact_Success(t *testing.T) {
	pub, priv := generateKeyPair(t)
	content := []byte("binary executable payload")
	sha := sha256Hex(content)
	sig := signBytes(content, priv)

	artifact := release.Artifact{
		Version:                  "1.0.0",
		OS:                       "linux",
		Arch:                     "amd64",
		URL:                      "https://example.com/bin",
		SHA256:                   sha,
		Signature:                sig,
		CatalogIdentity:          strings.Repeat("c", 64),
		SemanticContractIdentity: "contract",
		ExecutionBuildIdentity:   "build",
	}

	err := release.VerifyArtifact(bytes.NewReader(content), artifact, pub)
	if err != nil {
		t.Fatalf("VerifyArtifact failed: %v", err)
	}
}

func TestVerifyArtifact_InvalidSignature(t *testing.T) {
	pub, _ := generateKeyPair(t)
	_, otherPriv := generateKeyPair(t)

	content := []byte("binary executable payload")
	sha := sha256Hex(content)
	sigFromOtherKey := signBytes(content, otherPriv)

	artifact := release.Artifact{
		Version:                  "1.0.0",
		OS:                       "linux",
		Arch:                     "amd64",
		URL:                      "https://example.com/bin",
		SHA256:                   sha,
		Signature:                sigFromOtherKey,
		CatalogIdentity:          strings.Repeat("c", 64),
		SemanticContractIdentity: "contract",
		ExecutionBuildIdentity:   "build",
	}

	err := release.VerifyArtifact(bytes.NewReader(content), artifact, pub)
	if err == nil {
		t.Fatal("expected signature verification failure, got nil")
	}
	if !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyArtifact_SHA256Mismatch(t *testing.T) {
	pub, priv := generateKeyPair(t)
	content := []byte("binary executable payload")
	tamperedContent := []byte("tampered binary payload")
	sha := sha256Hex(content)
	sig := signBytes(content, priv)

	artifact := release.Artifact{
		Version:                  "1.0.0",
		OS:                       "linux",
		Arch:                     "amd64",
		URL:                      "https://example.com/bin",
		SHA256:                   sha,
		Signature:                sig,
		CatalogIdentity:          strings.Repeat("c", 64),
		SemanticContractIdentity: "contract",
		ExecutionBuildIdentity:   "build",
	}

	err := release.VerifyArtifact(bytes.NewReader(tamperedContent), artifact, pub)
	if err == nil {
		t.Fatal("expected SHA-256 mismatch failure, got nil")
	}
	if !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFindRelease_ChannelSelectionAndVersion(t *testing.T) {
	m := release.Manifest{
		Schema:  release.ManifestSchema,
		Product: release.ProductIdentityStandaloneAERP,
		Channel: "stable",
		Releases: []release.Artifact{
			{
				Version:                  "1.0.0",
				OS:                       "linux",
				Arch:                     "amd64",
				URL:                      "https://example.com/1.0.0",
				SHA256:                   strings.Repeat("1", 64),
				Signature:                base64.StdEncoding.EncodeToString(make([]byte, 64)),
				CatalogIdentity:          strings.Repeat("1", 64),
				SemanticContractIdentity: "contract",
				ExecutionBuildIdentity:   "build",
			},
			{
				Version:                  "1.1.0",
				OS:                       "linux",
				Arch:                     "amd64",
				URL:                      "https://example.com/1.1.0",
				SHA256:                   strings.Repeat("2", 64),
				Signature:                base64.StdEncoding.EncodeToString(make([]byte, 64)),
				CatalogIdentity:          strings.Repeat("2", 64),
				SemanticContractIdentity: "contract",
				ExecutionBuildIdentity:   "build",
			},
		},
	}

	// Channel mismatch
	_, err := m.FindRelease("beta", "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("expected channel mismatch error, got: %v", err)
	}

	// Finds highest version
	rel, err := m.FindRelease("stable", "linux", "amd64")
	if err != nil {
		t.Fatalf("FindRelease failed: %v", err)
	}
	if rel.Version != "1.1.0" {
		t.Fatalf("expected version 1.1.0, got %s", rel.Version)
	}

	// Unsupported platform
	_, err = m.FindRelease("stable", "windows", "arm64")
	if err == nil || !strings.Contains(err.Error(), "no release found") {
		t.Fatalf("expected no release found error, got: %v", err)
	}
}

func TestSemverComparison(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0-rc2", "1.0.0-rc1", 1},
	}

	for _, tc := range tests {
		cmp, err := release.CompareSemver(tc.v1, tc.v2)
		if err != nil {
			t.Fatalf("CompareSemver(%q, %q) unexpected error: %v", tc.v1, tc.v2, err)
		}
		if cmp != tc.expected {
			t.Fatalf("CompareSemver(%q, %q) = %d, want %d", tc.v1, tc.v2, cmp, tc.expected)
		}
	}

	if !release.IsNewerVersion("1.0.0", "1.1.0") {
		t.Fatal("1.1.0 should be newer than 1.0.0")
	}
	if release.IsNewerVersion("1.1.0", "1.0.0") {
		t.Fatal("1.0.0 should not be newer than 1.1.0")
	}
}

func TestSemver2Precedence(t *testing.T) {
	// Tests SemVer 2.0 precedence specification §11:
	// alpha < alpha.1 < alpha.beta < beta < beta.2 < beta.11 < rc.1 < 1.0.0
	// numeric identifiers have lower precedence than non-numeric: 1.0.0-1 < 1.0.0-alpha
	// numeric prerelease identifiers: alpha.2 < alpha.10
	// build metadata ignored: 1.0.0+build1 == 1.0.0+build2
	precedenceChain := []string{
		"1.0.0-1",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.2",
		"1.0.0-alpha.10",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"2.0.0",
	}

	for i := 0; i < len(precedenceChain)-1; i++ {
		lower := precedenceChain[i]
		higher := precedenceChain[i+1]

		cmp, err := release.CompareSemver(lower, higher)
		if err != nil {
			t.Fatalf("CompareSemver(%q, %q) returned unexpected error: %v", lower, higher, err)
		}
		if cmp >= 0 {
			t.Errorf("expected %q < %q, got cmp = %d", lower, higher, cmp)
		}

		revCmp, err := release.CompareSemver(higher, lower)
		if err != nil {
			t.Fatalf("CompareSemver(%q, %q) returned unexpected error: %v", higher, lower, err)
		}
		if revCmp <= 0 {
			t.Errorf("expected %q > %q, got revCmp = %d", higher, lower, revCmp)
		}

		if !release.IsNewerVersion(lower, higher) {
			t.Errorf("expected IsNewerVersion(%q, %q) = true", lower, higher)
		}
		if release.IsNewerVersion(higher, lower) {
			t.Errorf("expected IsNewerVersion(%q, %q) = false", higher, lower)
		}
	}

	// Build metadata must NOT affect precedence
	buildPairs := [][2]string{
		{"1.0.0+build.1", "1.0.0+build.2"},
		{"1.0.0+20130313144700", "1.0.0+exp.sha.5114f85"},
		{"1.0.0-alpha+001", "1.0.0-alpha+002"},
	}
	for _, pair := range buildPairs {
		cmp, err := release.CompareSemver(pair[0], pair[1])
		if err != nil {
			t.Fatalf("CompareSemver(%q, %q) error: %v", pair[0], pair[1], err)
		}
		if cmp != 0 {
			t.Errorf("expected %q == %q, got cmp = %d", pair[0], pair[1], cmp)
		}
		if release.IsNewerVersion(pair[0], pair[1]) || release.IsNewerVersion(pair[1], pair[0]) {
			t.Errorf("build metadata should not make versions newer: %v", pair)
		}
	}
}

func TestSemverParseRejections(t *testing.T) {
	invalidVersions := []struct {
		v       string
		desc    string
		wantErr string
	}{
		{"01.0.0", "leading zero in major", "leading zero"},
		{"1.01.0", "leading zero in minor", "leading zero"},
		{"1.0.01", "leading zero in patch", "leading zero"},
		{"1.0.0-alpha.01", "leading zero in numeric prerelease", "leading zero"},
		{"1.0.0-01", "leading zero in purely numeric prerelease", "leading zero"},
		{"18446744073709551616.0.0", "major overflow uint64", "integer overflow"},
		{"1.18446744073709551616.0", "minor overflow uint64", "integer overflow"},
		{"1.0.18446744073709551616", "patch overflow uint64", "integer overflow"},
		{"1.0.0-alpha.18446744073709551616", "prerelease overflow uint64", "integer overflow"},
		{"1.0.0-alpha@beta", "invalid char in prerelease", "invalid prerelease identifier"},
		{"1.0.0+", "empty build metadata", "empty build metadata"},
		{"1.0.0-", "empty prerelease", "empty prerelease identifier"},
	}

	for _, tc := range invalidVersions {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := release.ParseSemver(tc.v)
			if err == nil {
				t.Fatalf("ParseSemver(%q) expected error containing %q, got nil", tc.v, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ParseSemver(%q) error = %v, want substring %q", tc.v, err, tc.wantErr)
			}
		})
	}
}

func TestLoadManifest_OversizedPayload(t *testing.T) {
	oversizedBody := make([]byte, release.MaxManifestSizeBytes+100)
	for i := range oversizedBody {
		oversizedBody[i] = ' '
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(oversizedBody)
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := release.LoadManifest(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error on oversized manifest payload, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum permitted size") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestLoadManifest_RejectsTrailingData(t *testing.T) {
	_, priv := generateKeyPair(t)
	dummyBinary := []byte("bin")
	artifactSig := signBytes(dummyBinary, priv)
	artifactHash := sha256Hex(dummyBinary)

	baseManifest := release.Manifest{
		Schema:  release.ManifestSchema,
		Product: release.ProductIdentityStandaloneAERP,
		Channel: "stable",
		Releases: []release.Artifact{
			{
				Version:                  "1.0.0",
				OS:                       "linux",
				Arch:                     "amd64",
				URL:                      "https://example.com/bin",
				SHA256:                   artifactHash,
				Signature:                artifactSig,
				CatalogIdentity:          strings.Repeat("a", 64),
				SemanticContractIdentity: "contract",
				ExecutionBuildIdentity:   "build",
			},
		},
	}
	encoded, _ := json.Marshal(baseManifest)

	// Test 1: trailing non-whitespace data (raw junk)
	serverJunk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append(encoded, []byte(" extra_trailing_junk")...))
	}))
	defer serverJunk.Close()

	_, err := release.LoadManifest(context.Background(), serverJunk.URL)
	if err == nil {
		t.Fatal("expected error on trailing raw data, got nil")
	}

	// Test 2: trailing JSON object
	serverJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append(encoded, []byte(` {"extra": 123}`)...))
	}))
	defer serverJSON.Close()

	_, err = release.LoadManifest(context.Background(), serverJSON.URL)
	if err == nil {
		t.Fatal("expected error on trailing JSON object, got nil")
	}

	// Test 3: trailing whitespace ONLY should succeed
	serverWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(append(encoded, []byte("\n  \t  \n")...))
	}))
	defer serverWS.Close()

	m, err := release.LoadManifest(context.Background(), serverWS.URL)
	if err != nil {
		t.Fatalf("expected trailing whitespace to be accepted, got error: %v", err)
	}
	if len(m.Releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(m.Releases))
	}
}

type infiniteZeroReader struct{}

func (infiniteZeroReader) Read(p []byte) (int, error) {
	return len(p), nil
}

func TestVerifyArtifact_OversizedStream(t *testing.T) {
	pub, priv := generateKeyPair(t)
	content := []byte("payload")
	sig := signBytes(content, priv)
	sha := sha256Hex(content)

	artifact := release.Artifact{
		Version:                  "1.0.0",
		OS:                       "linux",
		Arch:                     "amd64",
		URL:                      "https://example.com/bin",
		SHA256:                   sha,
		Signature:                sig,
		CatalogIdentity:          strings.Repeat("c", 64),
		SemanticContractIdentity: "contract",
		ExecutionBuildIdentity:   "build",
	}

	// Reader providing more than MaxArtifactSizeBytes
	oversizedReader := io.LimitReader(infiniteZeroReader{}, release.MaxArtifactSizeBytes+10)

	err := release.VerifyArtifact(oversizedReader, artifact, pub)
	if err == nil {
		t.Fatal("expected error for stream exceeding MaxArtifactSizeBytes, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum permitted size") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestReleaseTrust_ProductionUsesEmbeddedKey(t *testing.T) {
	// Set untrusted key in environment variables
	untrustedPub, _ := generateKeyPair(t)
	untrustedHex := hex.EncodeToString(untrustedPub)
	t.Setenv("ODOO_RELEASE_PUBLIC_KEY", untrustedHex)
	t.Setenv("AERP_RELEASE_PUBLIC_KEY", untrustedHex)

	key, err := release.GetPublicKey()
	if err != nil {
		t.Fatalf("GetPublicKey failed: %v", err)
	}

	embeddedBytes, _ := hex.DecodeString(release.EmbeddedReleasePublicKeyHex)
	if !bytes.Equal(key, embeddedBytes) {
		t.Fatalf("GetPublicKey used environment key instead of embedded trust root! got %s, want %s",
			hex.EncodeToString(key), release.EmbeddedReleasePublicKeyHex)
	}
}

func TestReleaseTrust_SetPublicKeyForTestingRestoresPrior(t *testing.T) {
	pub1, _ := generateKeyPair(t)
	pub2, _ := generateKeyPair(t)

	cleanup1 := release.SetPublicKeyForTesting(pub1)
	current, _ := release.GetPublicKey()
	if !bytes.Equal(current, pub1) {
		t.Fatal("current test key should be pub1")
	}

	// Nested override
	cleanup2 := release.SetPublicKeyForTesting(pub2)
	current, _ = release.GetPublicKey()
	if !bytes.Equal(current, pub2) {
		t.Fatal("current test key should be pub2")
	}

	cleanup2()
	current, _ = release.GetPublicKey()
	if !bytes.Equal(current, pub1) {
		t.Fatal("cleanup2 should have restored prior test key pub1")
	}

	cleanup1()
	current, _ = release.GetPublicKey()
	embeddedBytes, _ := hex.DecodeString(release.EmbeddedReleasePublicKeyHex)
	if !bytes.Equal(current, embeddedBytes) {
		t.Fatal("cleanup1 should have restored embedded root key")
	}
}

func TestManifestValidation_SemanticallyEquivalentDuplicates(t *testing.T) {
	validArtifact := release.Artifact{
		Version:                  "1.0.0",
		OS:                       "linux",
		Arch:                     "amd64",
		URL:                      "https://releases.example.com/bin",
		SHA256:                   strings.Repeat("0", 64),
		Signature:                base64.StdEncoding.EncodeToString(make([]byte, 64)),
		CatalogIdentity:          strings.Repeat("b", 64),
		SemanticContractIdentity: "contract",
		ExecutionBuildIdentity:   "build",
	}

	ambiguousPairs := []struct {
		desc string
		v1   string
		v2   string
	}{
		{"1.0.0 and v1.0.0", "1.0.0", "v1.0.0"},
		{"v1.0.0 and 1.0.0", "v1.0.0", "1.0.0"},
		{"1.0.0 and V1.0.0", "1.0.0", "V1.0.0"},
		{"1.0.0 and 1.0.0+build1", "1.0.0", "1.0.0+build1"},
		{"1.0.0+build1 and 1.0.0+build2", "1.0.0+build1", "1.0.0+build2"},
		{"v1.2.3-rc.1 and 1.2.3-rc.1", "v1.2.3-rc.1", "1.2.3-rc.1"},
	}

	for _, tc := range ambiguousPairs {
		t.Run(tc.desc, func(t *testing.T) {
			a1 := validArtifact
			a1.Version = tc.v1
			a2 := validArtifact
			a2.Version = tc.v2

			m := release.Manifest{
				Schema:   release.ManifestSchema,
				Product:  release.ProductIdentityStandaloneAERP,
				Channel:  "stable",
				Releases: []release.Artifact{a1, a2},
			}
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected duplicate/ambiguous validation error for versions %q and %q, got nil", tc.v1, tc.v2)
			}
			if !strings.Contains(err.Error(), "duplicate or ambiguous") {
				t.Fatalf("expected 'duplicate or ambiguous' error, got: %v", err)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"https://example.com/bin", "https://example.com/bin"},
		{"https://example.com/bin?token=secret123", "https://example.com/bin"},
		{"https://example.com/bin#frag", "https://example.com/bin"},
		{"https://user:pass@example.com/bin?token=sec#frag", "https://example.com/bin"},
		{"http://127.0.0.1:8080/manifest.json?api_key=xyz", "http://127.0.0.1:8080/manifest.json"},
	}

	for _, tc := range tests {
		got := release.RedactURL(tc.input)
		if got != tc.expected {
			t.Errorf("RedactURL(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
