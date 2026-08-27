package hmac

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Key represents a service key for HMAC signing/verification.
type Key struct {
	ID     string
	Secret []byte
}

// KeyProvider fetches keys for a service name.
type KeyProvider interface {
	GetKeys(ctx context.Context, serviceName string) (active Key, previous Key, err error)
}

// NonceStore checks and sets nonces to prevent replay attacks.
type NonceStore interface {
	CheckAndSet(ctx context.Context, nonce string, ttl time.Duration) (bool, error)
}

// Clock abstracts time for unit testing.
type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

type Environment string

const (
	EnvLocal      Environment = "local"
	EnvTest       Environment = "test"
	EnvStaging    Environment = "staging"
	EnvProduction Environment = "production"
)

// ReplayKeyFunc derives a custom replay key from callerService and nonce.
type ReplayKeyFunc func(callerService, nonce string) string

// ErrEmptyReplayKey is returned when the derived replay key is empty.
var ErrEmptyReplayKey = errors.New("empty replay key")

// Config defines HMAC parameters.
type Config struct {
	ClockSkew time.Duration
	NonceTTL  time.Duration
	Env       Environment
	ReplayKey ReplayKeyFunc
}

// InMemoryNonceStore is a thread-safe, in-memory implementation of NonceStore.
type InMemoryNonceStore struct {
	nonces map[string]time.Time
	mu     sync.RWMutex
}

func NewInMemoryNonceStore() *InMemoryNonceStore {
	return &InMemoryNonceStore{
		nonces: make(map[string]time.Time),
	}
}

func (s *InMemoryNonceStore) CheckAndSet(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	// Clean up expired nonces
	for k, expire := range s.nonces {
		if now.After(expire) {
			delete(s.nonces, k)
		}
	}

	if expire, exists := s.nonces[nonce]; exists && now.Before(expire) {
		return false, nil // Nonce has already been used and is not yet expired
	}

	s.nonces[nonce] = now.Add(ttl)
	return true, nil
}

// StaticKeyProvider is a simple helper for testing.
type StaticKeyProvider struct {
	Keys map[string][]Key
}

func (s *StaticKeyProvider) GetKeys(ctx context.Context, serviceName string) (Key, Key, error) {
	keys, ok := s.Keys[serviceName]
	if !ok || len(keys) == 0 {
		return Key{}, Key{}, fmt.Errorf("keys not found for service: %s", serviceName)
	}
	if len(keys) == 1 {
		return keys[0], Key{}, nil
	}
	return keys[0], keys[1], nil
}

// Signer signs outgoing requests.
type Signer struct {
	serviceName string
	keyID       string
	secret      []byte
	clock       Clock
}

func NewSigner(serviceName, keyID string, secret []byte) *Signer {
	return &Signer{
		serviceName: serviceName,
		keyID:       keyID,
		secret:      secret,
		clock:       RealClock{},
	}
}

// SetClock allows overriding the clock for tests.
func (s *Signer) SetClock(clock Clock) {
	s.clock = clock
}

// Sign generates the HMAC signature headers for the request.
func (s *Signer) Sign(method, path string, body []byte, nonce string) (map[string]string, error) {
	timestamp := strconv.FormatInt(s.clock.Now().Unix(), 10)
	bodyHash := HashBody(body)
	payload := GenerateSignaturePayload(method, path, timestamp, nonce, bodyHash)

	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(payload))
	signature := hex.EncodeToString(h.Sum(nil))

	return map[string]string{
		"x-service-name":      s.serviceName,
		"x-service-key-id":    s.keyID,
		"x-service-timestamp": timestamp,
		"x-service-nonce":     nonce,
		"x-service-signature": signature,
	}, nil
}

// Verifier validates incoming requests.
type Verifier struct {
	keyProvider KeyProvider
	nonceStore  NonceStore
	clock       Clock
	cfg         Config
}

func NewVerifier(kp KeyProvider, ns NonceStore, cfg Config) *Verifier {
	if cfg.ClockSkew <= 0 {
		cfg.ClockSkew = 60 * time.Second
	}
	if cfg.NonceTTL <= 0 {
		cfg.NonceTTL = 300 * time.Second
	}
	return &Verifier{
		keyProvider: kp,
		nonceStore:  ns,
		clock:       RealClock{},
		cfg:         cfg,
	}
}

// SetClock allows overriding the clock for tests.
func (v *Verifier) SetClock(clock Clock) {
	v.clock = clock
}

// Verify validates headers against the payload and secret.
func (v *Verifier) Verify(ctx context.Context, headers map[string]string, method, path string, body []byte) error {
	// Guard: in-memory nonce store is only allowed in local or test environments
	if _, isInMemory := v.nonceStore.(*InMemoryNonceStore); isInMemory {
		switch v.cfg.Env {
		case EnvLocal, EnvTest:
			// Allowed
		default:
			return fmt.Errorf("in-memory nonce store is not allowed in environment %q", v.cfg.Env)
		}
	}

	serviceName := headers["x-service-name"]
	keyID := headers["x-service-key-id"]
	timestampStr := headers["x-service-timestamp"]
	nonce := headers["x-service-nonce"]
	signature := headers["x-service-signature"]

	if serviceName == "" || keyID == "" || timestampStr == "" || nonce == "" || signature == "" {
		return errors.New("missing required HMAC headers")
	}

	// 1. Verify clock skew
	timestampUnix, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return errors.New("invalid x-service-timestamp format")
	}
	timestamp := time.Unix(timestampUnix, 0)
	now := v.clock.Now()

	diff := now.Sub(timestamp)
	if diff < 0 {
		diff = -diff
	}
	if diff > v.cfg.ClockSkew {
		return errors.New("timestamp outside allowed clock skew")
	}

	// 2. Fetch active and previous keys for the service
	activeKey, prevKey, err := v.keyProvider.GetKeys(ctx, serviceName)
	if err != nil {
		return fmt.Errorf("failed to retrieve service keys: %w", err)
	}

	bodyHash := HashBody(body)
	payload := GenerateSignaturePayload(method, path, timestampStr, nonce, bodyHash)

	// 3. Verify signature correctness first (against active or previous key)
	sigValid := false
	if activeKey.ID == keyID && v.checkSignature(payload, activeKey.Secret, signature) {
		sigValid = true
	} else if prevKey.ID == keyID && v.checkSignature(payload, prevKey.Secret, signature) {
		sigValid = true
	}

	if !sigValid {
		return errors.New("invalid signature")
	}

	// 4. Verify nonce replay ONLY after signature is validated
	replayKey := nonce
	if v.cfg.ReplayKey != nil {
		replayKey = v.cfg.ReplayKey(serviceName, nonce)
		if replayKey == "" {
			return ErrEmptyReplayKey
		}
	}
	ok, err := v.nonceStore.CheckAndSet(ctx, replayKey, v.cfg.NonceTTL)
	if err != nil {
		return fmt.Errorf("failed to verify nonce: %w", err)
	}
	if !ok {
		return errors.New("nonce replay detected")
	}

	return nil
}

func (v *Verifier) checkSignature(payload string, secret []byte, expectedSig string) bool {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(payload))
	actualSig := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(actualSig), []byte(expectedSig))
}

// HTTPMiddleware intercepts HTTP requests to verify their HMAC signatures.
func (v *Verifier) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bodyBytes []byte
		if r.Body != nil {
			var err error
			bodyBytes, err = io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}

		headers := map[string]string{
			"x-service-name":      r.Header.Get("X-Service-Name"),
			"x-service-key-id":    r.Header.Get("X-Service-Key-Id"),
			"x-service-timestamp": r.Header.Get("X-Service-Timestamp"),
			"x-service-nonce":     r.Header.Get("X-Service-Nonce"),
			"x-service-signature": r.Header.Get("X-Service-Signature"),
		}

		if err := v.Verify(r.Context(), headers, r.Method, r.URL.Path, bodyBytes); err != nil {
			// Return safe generic 401 without leaking internal details
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// HTTPRoundTripper signs outgoing HTTP requests.
type HTTPRoundTripper struct {
	signer *Signer
	next   http.RoundTripper
}

func NewHTTPRoundTripper(signer *Signer, next http.RoundTripper) *HTTPRoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &HTTPRoundTripper{
		signer: signer,
		next:   next,
	}
}

func (rt *HTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read body for signing: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	nonce := generateRandomString()
	headers, err := rt.signer.Sign(req.Method, req.URL.Path, bodyBytes, nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return rt.next.RoundTrip(req)
}

// UnaryServerInterceptor returns a gRPC unary interceptor for verifying HMAC signatures.
func (v *Verifier) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "Unauthorized")
		}

		headers := make(map[string]string)
		for k, vals := range md {
			if len(vals) > 0 {
				headers[k] = vals[0]
			}
		}

		reqBytes, err := marshalMessage(req)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "failed to marshal request")
		}

		if err := v.Verify(ctx, headers, "GRPC", info.FullMethod, reqBytes); err != nil {
			// Return safe generic Unauthenticated error
			return nil, status.Error(codes.Unauthenticated, "Unauthorized")
		}

		return handler(ctx, req)
	}
}

// UnaryClientInterceptor returns a gRPC unary interceptor for signing requests.
func (s *Signer) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		reqBytes, err := marshalMessage(req)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}

		nonce := generateRandomString()
		headers, err := s.Sign("GRPC", method, reqBytes, nonce)
		if err != nil {
			return fmt.Errorf("failed to sign request: %w", err)
		}

		// Preserve existing outgoing metadata
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		for k, v := range headers {
			md.Set(k, v)
		}

		outgoingCtx := metadata.NewOutgoingContext(ctx, md)

		return invoker(outgoingCtx, method, req, reply, cc, opts...)
	}
}

// StreamServerInterceptor returns a gRPC stream interceptor for verifying HMAC signatures.
func (v *Verifier) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return status.Error(codes.Unauthenticated, "Unauthorized")
		}

		headers := make(map[string]string)
		for k, vals := range md {
			if len(vals) > 0 {
				headers[k] = vals[0]
			}
		}

		// Stream handshake uses empty body bytes
		if err := v.Verify(ctx, headers, "GRPC", info.FullMethod, nil); err != nil {
			// Return safe generic Unauthenticated error
			return status.Error(codes.Unauthenticated, "Unauthorized")
		}

		return handler(srv, ss)
	}
}

// StreamClientInterceptor returns a gRPC stream interceptor for signing requests.
func (s *Signer) StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		nonce := generateRandomString()
		headers, err := s.Sign("GRPC", method, nil, nonce)
		if err != nil {
			return nil, fmt.Errorf("failed to sign stream request: %w", err)
		}

		// Preserve existing outgoing metadata
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		for k, v := range headers {
			md.Set(k, v)
		}

		outgoingCtx := metadata.NewOutgoingContext(ctx, md)

		return streamer(outgoingCtx, desc, cc, method, opts...)
	}
}

// Helpers

func GenerateSignaturePayload(method, path, timestamp, nonce, bodyHash string) string {
	return method + path + timestamp + nonce + bodyHash
}

func HashBody(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

func generateRandomString() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func marshalMessage(msg interface{}) ([]byte, error) {
	if msg == nil {
		return nil, nil
	}
	pMsg, ok := msg.(proto.Message)
	if !ok {
		return nil, errors.New("message is not a proto.Message")
	}
	opts := proto.MarshalOptions{Deterministic: true}
	return opts.Marshal(pMsg)
}
