// Package release provides cryptographic release manifest verification, artifact signature checks,
// and update selection for the standalone Odoo CLI.
//
// Cryptographic Payload Specification:
// The detached signature in Artifact.Signature is an Ed25519 signature computed strictly over the
// exact raw bytes of the binary artifact.
// The Artifact.SHA256 digest is the standard lowercase hexadecimal SHA-256 checksum over those same
// exact raw bytes.
//
// Security Notice:
// Release verification functions never log artifact binary bytes, authentication tokens,
// or credential material.
package release

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// ManifestSchema is the current schema identifier for Odoo release manifests.
	ManifestSchema = "odoo.release-manifest/v1"
	// LegacyManifestSchema supports backward-compatibility with historical manifests.
	LegacyManifestSchema = "aerp.release-manifest/v1"

	// ProductIdentityStandaloneAERP is the product identity for the standalone binary.
	ProductIdentityStandaloneAERP = "STANDALONE_AERP"
	// ProductIdentityStandaloneODOO is the alternative accepted product identity.
	ProductIdentityStandaloneODOO = "STANDALONE_ODOO"

	// MaxManifestSizeBytes bounds manifest response payload sizes to prevent denial of service.
	MaxManifestSizeBytes = 1 << 20 // 1 MB
	// MaxArtifactSizeBytes bounds downloadable artifact binary sizes.
	MaxArtifactSizeBytes = 200 << 20 // 200 MB
	// DefaultHTTPTimeout is the bounded timeout for release HTTP requests.
	DefaultHTTPTimeout = 15 * time.Second
)

var (
	semverPattern  = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	channelPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	hex64Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Supported operating systems and architectures.
var (
	SupportedOS   = map[string]bool{"linux": true, "darwin": true, "windows": true}
	SupportedArch = map[string]bool{"amd64": true, "arm64": true}
)

// Manifest defines the top-level signed release catalog document.
type Manifest struct {
	Schema   string     `json:"schema"`
	Product  string     `json:"product"`
	Channel  string     `json:"channel"`
	Releases []Artifact `json:"releases"`
}

// Artifact defines a single binary release artifact with cryptographic checksums and compatibility identities.
type Artifact struct {
	Version                  string `json:"version"`
	OS                       string `json:"os"`
	Arch                     string `json:"arch"`
	URL                      string `json:"url"`
	SHA256                   string `json:"sha256"`
	Signature                string `json:"signature"`
	CatalogIdentity          string `json:"catalogIdentity"`
	SemanticContractIdentity string `json:"semanticContractIdentity"`
	ExecutionBuildIdentity   string `json:"executionBuildIdentity"`
}

// IsLoopbackHost checks whether a hostname represents a loopback network address.
func IsLoopbackHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Validate verifies that the manifest conforms strictly to the release schema and security rules.
func (m Manifest) Validate() error {
	schema := strings.TrimSpace(m.Schema)
	if schema != ManifestSchema && schema != LegacyManifestSchema {
		return fmt.Errorf("invalid manifest schema %q; expected %q", m.Schema, ManifestSchema)
	}

	product := strings.TrimSpace(m.Product)
	if product != ProductIdentityStandaloneAERP {
		return fmt.Errorf("invalid manifest product %q; expected %q", m.Product, ProductIdentityStandaloneAERP)
	}

	channel := strings.TrimSpace(m.Channel)
	if channel == "" || !channelPattern.MatchString(channel) {
		return fmt.Errorf("invalid manifest channel %q", m.Channel)
	}

	if len(m.Releases) == 0 {
		return errors.New("manifest releases list must not be empty")
	}

	seen := make(map[string]bool, len(m.Releases))
	for i, rel := range m.Releases {
		if err := rel.Validate(); err != nil {
			return fmt.Errorf("release entry [%d] (%s/%s %s) invalid: %w", i, rel.OS, rel.Arch, rel.Version, err)
		}
		sv, err := ParseSemver(rel.Version)
		if err != nil {
			return fmt.Errorf("release entry [%d] (%s/%s %s) invalid semver: %w", i, rel.OS, rel.Arch, rel.Version, err)
		}
		key := fmt.Sprintf("%s/%s/%s", sv.Identity(), rel.OS, rel.Arch)
		if seen[key] {
			return fmt.Errorf("duplicate or ambiguous release entry for %s/%s version %s (canonical: %s)", rel.OS, rel.Arch, rel.Version, sv.Identity())
		}
		seen[key] = true
	}

	return nil
}

// Validate verifies that an artifact entry contains valid versions, platforms, hashes, signatures, and URLs.
func (a Artifact) Validate() error {
	v := strings.TrimSpace(a.Version)
	if _, err := ParseSemver(v); err != nil {
		return fmt.Errorf("malformed semver version %q: %w", a.Version, err)
	}

	osName := strings.TrimSpace(a.OS)
	if !SupportedOS[osName] {
		return fmt.Errorf("unsupported operating system %q", a.OS)
	}

	arch := strings.TrimSpace(a.Arch)
	if !SupportedArch[arch] {
		return fmt.Errorf("unsupported architecture %q", a.Arch)
	}

	u, err := url.Parse(strings.TrimSpace(a.URL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("malformed artifact URL %q", RedactURL(a.URL))
	}
	if u.User != nil {
		return errors.New("artifact URL must not contain user credentials")
	}
	if u.Scheme != "https" {
		if u.Scheme != "http" || !IsLoopbackHost(u.Hostname()) {
			return fmt.Errorf("artifact URL %q must use HTTPS (loopback HTTP allowed only for tests)", RedactURL(a.URL))
		}
	}

	if !sha256Pattern.MatchString(a.SHA256) {
		return fmt.Errorf("invalid SHA-256 %q: must be exactly 64 lowercase hex characters", a.SHA256)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(a.Signature))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid base64 Ed25519 signature for artifact version %s", a.Version)
	}

	if !hex64Pattern.MatchString(strings.TrimSpace(a.CatalogIdentity)) {
		return fmt.Errorf("invalid catalogIdentity %q: must be 64 hex characters", a.CatalogIdentity)
	}

	if strings.TrimSpace(a.SemanticContractIdentity) == "" {
		return errors.New("semanticContractIdentity must not be empty")
	}

	if strings.TrimSpace(a.ExecutionBuildIdentity) == "" {
		return errors.New("executionBuildIdentity must not be empty")
	}

	return nil
}

// FindRelease searches the manifest for an artifact matching the requested channel, OS, and architecture.
// If multiple matching versions exist, the release with the highest semver is returned.
func (m Manifest) FindRelease(channel, goos, goarch string) (*Artifact, error) {
	if m.Channel != "" && channel != "" && !strings.EqualFold(m.Channel, channel) {
		return nil, fmt.Errorf("manifest channel %q does not match requested channel %q", m.Channel, channel)
	}

	var candidates []Artifact
	for _, rel := range m.Releases {
		if rel.OS == goos && rel.Arch == goarch {
			candidates = append(candidates, rel)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no release found for %s/%s", goos, goarch)
	}

	best := candidates[0]
	for _, candidate := range candidates[1:] {
		cmp, err := CompareSemver(candidate.Version, best.Version)
		if err == nil && cmp > 0 {
			best = candidate
		}
	}
	return &best, nil
}

// RedactURL strips query strings, fragments, and user credentials from a raw URL for safe
// presentation in structured output, errors, and diagnostics.
func RedactURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		if idx := strings.IndexAny(rawURL, "?#"); idx != -1 {
			return rawURL[:idx]
		}
		return rawURL
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

// SanitizeURLError redacts URLs contained in *url.Error to prevent exposing query-string
// or fragment secrets from error messages.
func SanitizeURLError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		sanitized := *urlErr
		sanitized.URL = RedactURL(urlErr.URL)
		return &sanitized
	}
	return err
}

// LoadManifest fetches and parses a Manifest from the given URL using context-bound HTTP.
// Non-HTTPS URLs are rejected in production; loopback HTTP is permitted only for testing.
func LoadManifest(ctx context.Context, manifestURL string) (Manifest, error) {
	manifestURL = strings.TrimSpace(manifestURL)
	if manifestURL == "" {
		return Manifest{}, errors.New("manifest URL is required")
	}

	u, err := url.Parse(manifestURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Manifest{}, fmt.Errorf("malformed manifest URL %q", RedactURL(manifestURL))
	}
	if u.User != nil {
		return Manifest{}, errors.New("manifest URL must not contain user credentials")
	}
	if u.Scheme != "https" {
		if u.Scheme != "http" || !IsLoopbackHost(u.Hostname()) {
			return Manifest{}, fmt.Errorf("insecure non-HTTPS manifest URL: %s", RedactURL(manifestURL))
		}
	}

	reqCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, DefaultHTTPTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to construct manifest request: %w", SanitizeURLError(err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "odoo-cli-release-loader")

	client := &http.Client{
		Timeout: DefaultHTTPTimeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("network request failed for manifest: %w", SanitizeURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("manifest HTTP request failed with status %d", resp.StatusCode)
	}

	// Strict bounded read: read up to MaxManifestSizeBytes + 1 to detect oversize payload
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, MaxManifestSizeBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to read manifest response body: %w", err)
	}
	if int64(len(bodyBytes)) > MaxManifestSizeBytes {
		return Manifest{}, fmt.Errorf("manifest payload exceeds maximum permitted size of %d bytes", MaxManifestSizeBytes)
	}

	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(bodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("failed to decode manifest JSON: %w", err)
	}

	// Reject any non-whitespace trailing data after the single manifest object
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		return Manifest{}, errors.New("unexpected trailing data after manifest JSON object")
	}

	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}

	return m, nil
}

// VerifyArtifact verifies the integrity and authenticity of the artifact bytes read from r:
// 1. Computes the SHA-256 checksum over the exact artifact bytes and verifies match with artifact.SHA256.
// 2. Verifies the detached Ed25519 signature over those exact artifact bytes using the supplied public key.
// 3. Enforces bounded read, detecting and rejecting streams larger than MaxArtifactSizeBytes.
func VerifyArtifact(r io.Reader, artifact Artifact, pubKey ed25519.PublicKey) error {
	if len(pubKey) != ed25519.PublicKeySize {
		return errors.New("invalid Ed25519 public key size")
	}
	if err := artifact.Validate(); err != nil {
		return fmt.Errorf("invalid artifact metadata: %w", err)
	}

	// Read up to MaxArtifactSizeBytes + 1 to detect oversized stream
	data, err := io.ReadAll(io.LimitReader(r, MaxArtifactSizeBytes+1))
	if err != nil {
		return fmt.Errorf("failed to read artifact data: %w", err)
	}
	if int64(len(data)) > MaxArtifactSizeBytes {
		return fmt.Errorf("artifact payload exceeds maximum permitted size of %d bytes", MaxArtifactSizeBytes)
	}

	hasher := sha256.New()
	hasher.Write(data)
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != strings.ToLower(artifact.SHA256) {
		return fmt.Errorf("SHA-256 mismatch: calculated %s, expected %s", actualHash, artifact.SHA256)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(artifact.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return errors.New("invalid base64 Ed25519 signature format")
	}

	if !ed25519.Verify(pubKey, data, sigBytes) {
		return errors.New("detached Ed25519 signature verification failed")
	}

	return nil
}

// Semver represents parsed semantic version components according to SemVer 2.0.
type Semver struct {
	Major                 uint64
	Minor                 uint64
	Patch                 uint64
	Prerelease            string
	Build                 string
	PrereleaseIdentifiers []string
	BuildIdentifiers      []string
}

// Identity returns the canonical semantic version identity according to SemVer 2.0.
// Leading 'v' or 'V' and build metadata are excluded, so semantically equivalent versions
// (such as "1.0.0", "v1.0.0", and "1.0.0+exp.sha") produce the identical string "1.0.0".
func (s Semver) Identity() string {
	base := fmt.Sprintf("%d.%d.%d", s.Major, s.Minor, s.Patch)
	if len(s.PrereleaseIdentifiers) > 0 {
		base += "-" + strings.Join(s.PrereleaseIdentifiers, ".")
	}
	return base
}

func isAllDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isValidIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' {
			continue
		}
		return false
	}
	return true
}

// ParseSemver parses a semver string conforming strictly to SemVer 2.0.
// It accepts an optional leading 'v' or 'V'.
// Leading zeroes in numeric core and prerelease identifiers are rejected.
// Numeric components exceeding 64-bit unsigned integers return an error.
func ParseSemver(v string) (Semver, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")

	if v == "" {
		return Semver{}, errors.New("empty semver string")
	}

	var rawBuild string
	var buildParts []string
	if plusIdx := strings.IndexByte(v, '+'); plusIdx != -1 {
		rawBuild = v[plusIdx+1:]
		v = v[:plusIdx]
		if rawBuild == "" {
			return Semver{}, fmt.Errorf("empty build metadata in %q", v)
		}
		for _, part := range strings.Split(rawBuild, ".") {
			if part == "" || !isValidIdentifier(part) {
				return Semver{}, fmt.Errorf("invalid build metadata identifier %q", part)
			}
			buildParts = append(buildParts, part)
		}
	}

	var rawPre string
	var prereleaseParts []string
	if hyphenIdx := strings.IndexByte(v, '-'); hyphenIdx != -1 {
		rawPre = v[hyphenIdx+1:]
		v = v[:hyphenIdx]
		if rawPre == "" {
			return Semver{}, fmt.Errorf("empty prerelease identifier in %q", v)
		}
		for _, part := range strings.Split(rawPre, ".") {
			if part == "" || !isValidIdentifier(part) {
				return Semver{}, fmt.Errorf("invalid prerelease identifier %q", part)
			}
			if isAllDigits(part) {
				if len(part) > 1 && part[0] == '0' {
					return Semver{}, fmt.Errorf("numeric prerelease identifier %q must not have leading zero", part)
				}
				if _, err := strconv.ParseUint(part, 10, 64); err != nil {
					return Semver{}, fmt.Errorf("numeric prerelease identifier %q integer overflow: %w", part, err)
				}
			}
			prereleaseParts = append(prereleaseParts, part)
		}
	}

	coreParts := strings.Split(v, ".")
	if len(coreParts) != 3 {
		return Semver{}, fmt.Errorf("malformed semver string %q; expected X.Y.Z core format", v)
	}

	for _, part := range coreParts {
		if part == "" || !isAllDigits(part) {
			return Semver{}, fmt.Errorf("invalid core version identifier %q", part)
		}
		if len(part) > 1 && part[0] == '0' {
			return Semver{}, fmt.Errorf("numeric core version component %q must not have leading zero", part)
		}
	}

	maj, err := strconv.ParseUint(coreParts[0], 10, 64)
	if err != nil {
		return Semver{}, fmt.Errorf("major version %q integer overflow: %w", coreParts[0], err)
	}
	min, err := strconv.ParseUint(coreParts[1], 10, 64)
	if err != nil {
		return Semver{}, fmt.Errorf("minor version %q integer overflow: %w", coreParts[1], err)
	}
	pat, err := strconv.ParseUint(coreParts[2], 10, 64)
	if err != nil {
		return Semver{}, fmt.Errorf("patch version %q integer overflow: %w", coreParts[2], err)
	}

	return Semver{
		Major:                 maj,
		Minor:                 min,
		Patch:                 pat,
		Prerelease:            rawPre,
		Build:                 rawBuild,
		PrereleaseIdentifiers: prereleaseParts,
		BuildIdentifiers:      buildParts,
	}, nil
}

// CompareSemver compares two semver strings according to SemVer 2.0 precedence:
// Returns >0 if v1 > v2, <0 if v1 < v2, and 0 if v1 == v2.
// Build metadata is ignored when determining precedence.
// Numeric identifiers have lower precedence than non-numeric identifiers.
// Numeric prerelease identifiers are compared numerically (e.g. alpha.2 < alpha.10).
func CompareSemver(v1, v2 string) (int, error) {
	s1, err := ParseSemver(v1)
	if err != nil {
		return 0, err
	}
	s2, err := ParseSemver(v2)
	if err != nil {
		return 0, err
	}

	if s1.Major != s2.Major {
		if s1.Major > s2.Major {
			return 1, nil
		}
		return -1, nil
	}
	if s1.Minor != s2.Minor {
		if s1.Minor > s2.Minor {
			return 1, nil
		}
		return -1, nil
	}
	if s1.Patch != s2.Patch {
		if s1.Patch > s2.Patch {
			return 1, nil
		}
		return -1, nil
	}

	// Normal version has greater precedence than a pre-release version (§11.2)
	hasPre1 := len(s1.PrereleaseIdentifiers) > 0
	hasPre2 := len(s2.PrereleaseIdentifiers) > 0

	if !hasPre1 && hasPre2 {
		return 1, nil
	}
	if hasPre1 && !hasPre2 {
		return -1, nil
	}
	if !hasPre1 && !hasPre2 {
		// Neither has prerelease; build metadata MUST be ignored (§10)
		return 0, nil
	}

	// Both have pre-release versions: compare dot-separated identifiers left-to-right (§11.4)
	limit := len(s1.PrereleaseIdentifiers)
	if len(s2.PrereleaseIdentifiers) < limit {
		limit = len(s2.PrereleaseIdentifiers)
	}

	for i := 0; i < limit; i++ {
		p1 := s1.PrereleaseIdentifiers[i]
		p2 := s2.PrereleaseIdentifiers[i]
		if p1 == p2 {
			continue
		}

		num1 := isAllDigits(p1)
		num2 := isAllDigits(p2)

		// Identifiers consisting of only digits are compared numerically
		if num1 && num2 {
			val1, _ := strconv.ParseUint(p1, 10, 64)
			val2, _ := strconv.ParseUint(p2, 10, 64)
			if val1 > val2 {
				return 1, nil
			}
			return -1, nil
		}

		// Numeric identifiers always have lower precedence than non-numeric identifiers
		if num1 && !num2 {
			return -1, nil
		}
		if !num1 && num2 {
			return 1, nil
		}

		// Identifiers with letters or hyphens are compared lexically in ASCII sort order
		if p1 > p2 {
			return 1, nil
		}
		return -1, nil
	}

	// Larger set of pre-release fields has a higher precedence than a smaller set
	if len(s1.PrereleaseIdentifiers) > len(s2.PrereleaseIdentifiers) {
		return 1, nil
	}
	if len(s1.PrereleaseIdentifiers) < len(s2.PrereleaseIdentifiers) {
		return -1, nil
	}

	// Build metadata does not affect precedence (§10)
	return 0, nil
}

// IsNewerVersion returns true if candidate is strictly newer than current according to semver.
func IsNewerVersion(current, candidate string) bool {
	cmp, err := CompareSemver(candidate, current)
	return err == nil && cmp > 0
}
