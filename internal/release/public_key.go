package release

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
)

// EmbeddedReleasePublicKeyHex is the hardcoded release verification public key.
// In production builds, this key verifies the detached Ed25519 signature of downloaded binaries.
const EmbeddedReleasePublicKeyHex = "6f52e379b34a66e4a2d48a1c97a8e29a3a936a71e2ef604d55b0a39ffc4e88db"

var (
	testKeyMu     sync.RWMutex
	testPublicKey ed25519.PublicKey
)

// SetPublicKeyForTesting sets an ephemeral public key for hermetic unit testing.
// It returns a cleanup function that restores the prior test key state.
func SetPublicKeyForTesting(key ed25519.PublicKey) func() {
	testKeyMu.Lock()
	prior := testPublicKey
	testPublicKey = key
	testKeyMu.Unlock()
	return func() {
		testKeyMu.Lock()
		testPublicKey = prior
		testKeyMu.Unlock()
	}
}

// GetPublicKey returns the Ed25519 public key used for release artifact verification.
// In production update verification, the embedded public key is the immutable trust root.
// Environment variables are intentionally NOT inspected here to prevent malicious or accidental
// hijacking of the release trust root on user systems.
// Hermetic tests may inject an ephemeral key via SetPublicKeyForTesting.
func GetPublicKey() (ed25519.PublicKey, error) {
	testKeyMu.RLock()
	if len(testPublicKey) == ed25519.PublicKeySize {
		defer testKeyMu.RUnlock()
		return testPublicKey, nil
	}
	testKeyMu.RUnlock()

	return ParsePublicKey(EmbeddedReleasePublicKeyHex)
}

// ParsePublicKey parses a 32-byte Ed25519 public key from hex or base64 string.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	// Try hex encoding first (64 hex characters = 32 bytes)
	if len(s) == 64 {
		b, err := hex.DecodeString(s)
		if err == nil && len(b) == ed25519.PublicKeySize {
			return ed25519.PublicKey(b), nil
		}
	}
	// Try standard base64 encoding
	b, err := base64.StdEncoding.DecodeString(s)
	if err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	return nil, errors.New("invalid Ed25519 public key format; expected 32-byte hex or base64")
}
