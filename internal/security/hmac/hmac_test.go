package hmac

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type StaticClock struct {
	T time.Time
}

func (s StaticClock) Now() time.Time {
	return s.T
}

func TestHMACVerificationFlow(t *testing.T) {
	now := time.Now()
	clock := StaticClock{T: now}

	activeKey := Key{ID: "key-v1", Secret: []byte("active-secret")}
	prevKey := Key{ID: "key-v0", Secret: []byte("prev-secret")}

	kp := &StaticKeyProvider{
		Keys: map[string][]Key{
			"test-service": {activeKey, prevKey},
		},
	}

	ns := NewInMemoryNonceStore()
	cfg := Config{
		ClockSkew: 60 * time.Second,
		NonceTTL:  300 * time.Second,
		Env:       EnvLocal,
	}

	signer := NewSigner("test-service", "key-v1", activeKey.Secret)
	signer.SetClock(clock)

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(clock)

	method := "POST"
	path := "/v1/users"
	body := []byte(`{"name":"alice"}`)
	nonce := "random-nonce-123"

	// 1. Successful signature check with active key
	headers, err := signer.Sign(method, path, body, nonce)
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}

	ctx := context.Background()
	if err := verifier.Verify(ctx, headers, method, path, body); err != nil {
		t.Errorf("expected valid signature, got: %v", err)
	}

	// 2. Replay attack rejection (reused nonce)
	if err := verifier.Verify(ctx, headers, method, path, body); err == nil {
		t.Error("expected error due to reused nonce, got nil")
	}

	// 3. Signature check with previous key (key rotation)
	prevSigner := NewSigner("test-service", "key-v0", prevKey.Secret)
	prevSigner.SetClock(clock)

	headersPrev, err := prevSigner.Sign(method, path, body, "another-nonce-456")
	if err != nil {
		t.Fatalf("failed to sign with prev key: %v", err)
	}

	if err := verifier.Verify(ctx, headersPrev, method, path, body); err != nil {
		t.Errorf("expected valid signature from previous key, got: %v", err)
	}

	// 4. Invalid signature check
	badHeaders := map[string]string{
		"x-service-name":      "test-service",
		"x-service-key-id":    "key-v1",
		"x-service-timestamp": strconv.FormatInt(now.Unix(), 10),
		"x-service-nonce":     "bad-nonce",
		"x-service-signature": "bad-sig",
	}
	if err := verifier.Verify(ctx, badHeaders, method, path, body); err == nil {
		t.Error("expected error for invalid signature, got nil")
	}

	// 5. Out of clock skew check (timestamp too old)
	oldClock := StaticClock{T: now.Add(-120 * time.Second)}
	oldSigner := NewSigner("test-service", "key-v1", activeKey.Secret)
	oldSigner.SetClock(oldClock)

	headersOld, err := oldSigner.Sign(method, path, body, "nonce-skew-1")
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	if err := verifier.Verify(ctx, headersOld, method, path, body); err == nil {
		t.Error("expected error for expired timestamp (too old), got nil")
	}

	// 6. Out of clock skew check (timestamp in the future)
	futureClock := StaticClock{T: now.Add(120 * time.Second)}
	futureSigner := NewSigner("test-service", "key-v1", activeKey.Secret)
	futureSigner.SetClock(futureClock)

	headersFuture, err := futureSigner.Sign(method, path, body, "nonce-skew-2")
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	if err := verifier.Verify(ctx, headersFuture, method, path, body); err == nil {
		t.Error("expected error for timestamp too far in future, got nil")
	}
}

func TestHTTPMiddlewareAndRoundTripper(t *testing.T) {
	now := time.Now()
	clock := StaticClock{T: now}
	secret := []byte("shared-secret")

	kp := &StaticKeyProvider{
		Keys: map[string][]Key{
			"gateway": {{ID: "key-v1", Secret: secret}},
		},
	}
	ns := NewInMemoryNonceStore()

	signer := NewSigner("gateway", "key-v1", secret)
	signer.SetClock(clock)

	verifier := NewVerifier(kp, ns, Config{Env: EnvLocal})
	verifier.SetClock(clock)

	// Create test HTTP server/handler
	handlerExecuted := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerExecuted = true
		w.WriteHeader(http.StatusOK)
	})

	protectedHandler := verifier.HTTPMiddleware(testHandler)

	// Test case: Valid request using HTTPRoundTripper
	server := httptest.NewServer(protectedHandler)
	defer server.Close()

	client := &http.Client{
		Transport: NewHTTPRoundTripper(signer, nil),
	}

	req, _ := http.NewRequest("POST", server.URL+"/v1/resource", bytes.NewBuffer([]byte("request-body")))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	if !handlerExecuted {
		t.Error("expected handler to execute, but it did not")
	}

	// Test case: Invalid request without signing
	respBad, err := http.Post(server.URL+"/v1/resource", "application/json", bytes.NewBuffer([]byte("request-body")))
	if err != nil {
		t.Fatalf("client request failed: %v", err)
	}
	defer respBad.Body.Close()

	if respBad.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for unsigned request, got %d", respBad.StatusCode)
	}
}

func TestGRPCInterceptors(t *testing.T) {
	now := time.Now()
	clock := StaticClock{T: now}
	secret := []byte("shared-secret")

	kp := &StaticKeyProvider{
		Keys: map[string][]Key{
			"client-service": {{ID: "key-v1", Secret: secret}},
		},
	}
	ns := NewInMemoryNonceStore()

	signer := NewSigner("client-service", "key-v1", secret)
	signer.SetClock(clock)

	verifier := NewVerifier(kp, ns, Config{Env: EnvLocal})
	verifier.SetClock(clock)

	// Mock request message
	reqMsg := &emptypb.Empty{}

	// 1. Test Unary client and server interceptors together
	unaryClient := signer.UnaryClientInterceptor()
	unaryServer := verifier.UnaryServerInterceptor()

	// Intercept client call, which will generate outgoing metadata in context
	invokerCalled := false
	invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		invokerCalled = true

		// Extract outgoing metadata as incoming metadata for server interceptor
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("expected outgoing metadata in client context")
		}

		serverCtx := metadata.NewIncomingContext(context.Background(), md)

		// Invoke server interceptor
		handlerCalled := false
		handler := func(ctx context.Context, req interface{}) (interface{}, error) {
			handlerCalled = true
			return "response", nil
		}

		info := &grpc.UnaryServerInfo{FullMethod: method}
		resp, err := unaryServer(serverCtx, req, info, handler)
		if err != nil {
			return err
		}

		if resp != "response" {
			t.Errorf("expected response, got %v", resp)
		}
		if !handlerCalled {
			t.Error("expected server handler to be called")
		}

		return nil
	}

	err := unaryClient(context.Background(), "/proto.Service/Method", reqMsg, nil, nil, invoker)
	if err != nil {
		t.Errorf("client unary interceptor call failed: %v", err)
	}
	if !invokerCalled {
		t.Error("expected client invoker to be called")
	}
}

func TestInMemoryNonceStoreGuardrail(t *testing.T) {
	kp := &StaticKeyProvider{
		Keys: map[string][]Key{
			"test-service": {{ID: "key-v1", Secret: []byte("secret")}},
		},
	}
	ns := NewInMemoryNonceStore()

	tests := []struct {
		env     Environment
		wantErr bool
	}{
		{EnvLocal, false},
		{EnvTest, false},
		{"", true},
		{EnvStaging, true},
		{EnvProduction, true},
		{"unknown-env", true},
	}

	for _, tt := range tests {
		t.Run(string(tt.env), func(t *testing.T) {
			cfg := Config{
				Env:       tt.env,
				ClockSkew: 60 * time.Second,
			}
			verifier := NewVerifier(kp, ns, cfg)
			verifier.SetClock(StaticClock{T: time.Now()})

			// Set up a valid signature so we only trigger the env guardrail
			signer := NewSigner("test-service", "key-v1", []byte("secret"))
			signer.SetClock(StaticClock{T: time.Now()})
			h, err := signer.Sign("POST", "/path", []byte{}, "nonce-for-"+string(tt.env))
			if err != nil {
				t.Fatalf("failed to sign: %v", err)
			}

			err = verifier.Verify(context.Background(), h, "POST", "/path", []byte{})
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for env %q, got nil", tt.env)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error for env %q, got %v", tt.env, err)
				}
			}
		})
	}
}

func TestSignatureValidatedBeforeNonce(t *testing.T) {
	now := time.Now()
	clock := StaticClock{T: now}
	secret := []byte("secret")

	kp := &StaticKeyProvider{
		Keys: map[string][]Key{
			"test-service": {{ID: "key-v1", Secret: secret}},
		},
	}
	ns := NewInMemoryNonceStore()
	cfg := Config{
		ClockSkew: 60 * time.Second,
		NonceTTL:  300 * time.Second,
		Env:       EnvLocal,
	}

	signer := NewSigner("test-service", "key-v1", secret)
	signer.SetClock(clock)

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(clock)

	// Generate headers for a valid request
	nonce := "non-poisoned-nonce"
	validHeaders, _ := signer.Sign("POST", "/path", []byte{}, nonce)

	// Create a copy of headers with an invalid signature
	invalidHeaders := make(map[string]string)
	for k, v := range validHeaders {
		invalidHeaders[k] = v
	}
	invalidHeaders["x-service-signature"] = "totally-invalid-signature"

	ctx := context.Background()

	// 1. Verify with invalid signature — should fail
	err1 := verifier.Verify(ctx, invalidHeaders, "POST", "/path", []byte{})
	if err1 == nil {
		t.Error("expected invalid signature verification to fail")
	}

	// 2. Verify with valid signature using the SAME nonce
	// If the nonce was poisoned/consumed in step 1, this will fail with nonce replay.
	// If the signature check comes first, the nonce was not consumed, so this succeeds.
	err2 := verifier.Verify(ctx, validHeaders, "POST", "/path", []byte{})
	if err2 != nil {
		t.Errorf("expected valid signature verification to succeed, got: %v", err2)
	}
}

func TestGRPCInterceptorsMetadataPreservation(t *testing.T) {
	now := time.Now()
	clock := StaticClock{T: now}
	secret := []byte("shared-secret")

	signer := NewSigner("client-service", "key-v1", secret)
	signer.SetClock(clock)

	// 1. Unary Client Interceptor
	unaryClient := signer.UnaryClientInterceptor()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("traceparent", "00-123456-7890-01", "custom-header", "custom-value"))

	invokerCalled := false
	invoker := func(invCtx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		invokerCalled = true

		md, ok := metadata.FromOutgoingContext(invCtx)
		if !ok {
			t.Fatal("expected outgoing metadata")
		}

		// Verify existing headers survived
		if md.Get("traceparent")[0] != "00-123456-7890-01" {
			t.Errorf("traceparent was lost or mutated")
		}
		if md.Get("custom-header")[0] != "custom-value" {
			t.Errorf("custom-header was lost")
		}

		// Verify HMAC headers exist
		if md.Get("x-service-name")[0] != "client-service" {
			t.Errorf("x-service-name HMAC header is missing")
		}
		return nil
	}

	reqMsg := &emptypb.Empty{}
	err := unaryClient(ctx, "/proto.Service/Method", reqMsg, nil, nil, invoker)
	if err != nil {
		t.Fatalf("unary client call failed: %v", err)
	}
	if !invokerCalled {
		t.Fatal("invoker was not called")
	}

	// 2. Stream Client Interceptor
	streamClient := signer.StreamClientInterceptor()
	streamerCalled := false
	streamer := func(strCtx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		streamerCalled = true

		md, ok := metadata.FromOutgoingContext(strCtx)
		if !ok {
			t.Fatal("expected outgoing metadata in stream context")
		}

		// Verify existing headers survived
		if md.Get("traceparent")[0] != "00-123456-7890-01" {
			t.Errorf("traceparent was lost in stream interceptor")
		}
		if md.Get("custom-header")[0] != "custom-value" {
			t.Errorf("custom-header was lost in stream interceptor")
		}

		// Verify HMAC headers exist
		if md.Get("x-service-name")[0] != "client-service" {
			t.Errorf("x-service-name HMAC header is missing in stream interceptor")
		}
		return nil, nil
	}

	_, err = streamClient(ctx, &grpc.StreamDesc{}, nil, "/proto.Service/MethodStream", streamer)
	if err != nil {
		t.Fatalf("stream client call failed: %v", err)
	}
	if !streamerCalled {
		t.Fatal("streamer was not called")
	}
}

// --- Test Helpers for Replay-Key Capability ---

type testNonceStoreSpy struct {
	CallCount int
	Keys      []string
	TTLs      []time.Duration
	Events    *[]string
	Seen      map[string]bool
	Result    bool
	Err       error
}

func (s *testNonceStoreSpy) CheckAndSet(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	s.CallCount++
	s.Keys = append(s.Keys, nonce)
	s.TTLs = append(s.TTLs, ttl)
	if s.Events != nil {
		*s.Events = append(*s.Events, "nonce_store")
	}
	if s.Seen == nil {
		s.Seen = make(map[string]bool)
	}
	if s.Seen[nonce] {
		return false, nil // Replay detected
	}
	s.Seen[nonce] = true
	return s.Result, s.Err
}

// Centralize missing capabilities to minimize compile errors
func withReplayKey(cfg Config, rkf ReplayKeyFunc) Config {
	cfg.ReplayKey = rkf
	return cfg
}

func isErrEmptyReplayKey(err error) bool {
	return errors.Is(err, ErrEmptyReplayKey)
}

// minimal grpc.ServerStream test double
type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *testServerStream) Context() context.Context {
	return s.ctx
}

// --- 9 Expected Required Tests ---

func TestVerifier_DefaultReplayKeyUsesRawNonce(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	ns := &testNonceStoreSpy{Result: true}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	// Config.ReplayKey is nil by default
	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("POST", "/path", nil, "nonce-rf003d-1")

	err := verifier.Verify(context.Background(), headers, "POST", "/path", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ns.CallCount != 1 {
		t.Errorf("expected store count 1, got %d", ns.CallCount)
	}
	if len(ns.Keys) == 0 || ns.Keys[0] != "nonce-rf003d-1" {
		t.Errorf("expected store key nonce-rf003d-1, got %v", ns.Keys)
	}
}

func TestVerifier_CustomReplayKeyUsesCallerAndNonce(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	ns := &testNonceStoreSpy{Result: true}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	var cbCount int
	var capturedCaller, capturedNonce string

	cfg = withReplayKey(cfg, func(callerService, nonce string) string {
		cbCount++
		capturedCaller = callerService
		capturedNonce = nonce
		return callerService + ":" + nonce
	})

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("POST", "/path", nil, "nonce-rf003d-1")

	err := verifier.Verify(context.Background(), headers, "POST", "/path", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cbCount != 1 {
		t.Errorf("expected callback count 1, got %d", cbCount)
	}
	if capturedCaller != "api-gateway" {
		t.Errorf("expected caller api-gateway, got %s", capturedCaller)
	}
	if capturedNonce != "nonce-rf003d-1" {
		t.Errorf("expected nonce nonce-rf003d-1, got %s", capturedNonce)
	}
	if ns.CallCount != 1 {
		t.Errorf("expected store count 1, got %d", ns.CallCount)
	}
	if len(ns.Keys) == 0 || ns.Keys[0] != "api-gateway:nonce-rf003d-1" {
		t.Errorf("expected store key api-gateway:nonce-rf003d-1, got %v", ns.Keys)
	}
}

func TestVerifier_ReplayKeyCalledAfterSignatureValidation(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	var events []string
	ns := &testNonceStoreSpy{Result: true, Events: &events}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	var cbCount int
	cfg = withReplayKey(cfg, func(callerService, nonce string) string {
		cbCount++
		events = append(events, "replay_callback")
		return callerService + ":" + nonce
	})

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("POST", "/path", nil, "nonce-rf003d-1")

	err := verifier.Verify(context.Background(), headers, "POST", "/path", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cbCount != 1 {
		t.Errorf("expected callback count 1, got %d", cbCount)
	}
	if ns.CallCount != 1 {
		t.Errorf("expected store count 1, got %d", ns.CallCount)
	}

	expectedEvents := "replay_callback,nonce_store"
	actualEvents := ""
	for i, e := range events {
		if i > 0 {
			actualEvents += ","
		}
		actualEvents += e
	}
	if actualEvents != expectedEvents {
		t.Errorf("expected event trace %s, got %s", expectedEvents, actualEvents)
	}
}

func TestVerifier_InvalidSignatureSkipsReplayKeyAndStore(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	var events []string
	ns := &testNonceStoreSpy{Result: true, Events: &events}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	var cbCount int
	cfg = withReplayKey(cfg, func(callerService, nonce string) string {
		cbCount++
		events = append(events, "replay_callback")
		return callerService + ":" + nonce
	})

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("POST", "/path", nil, "nonce-rf003d-1")
	headers["x-service-signature"] = "tampered" // tamper signature

	err := verifier.Verify(context.Background(), headers, "POST", "/path", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "invalid signature" {
		t.Errorf("expected exact error 'invalid signature', got '%v'", err)
	}

	if cbCount != 0 {
		t.Errorf("expected callback count 0, got %d", cbCount)
	}
	if ns.CallCount != 0 {
		t.Errorf("expected store count 0, got %d", ns.CallCount)
	}
	if len(events) != 0 {
		t.Errorf("expected empty event trace, got %v", events)
	}
}

func TestVerifier_EmptyReplayKeyFailsBeforeStore(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	ns := &testNonceStoreSpy{Result: true}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	var cbCount int
	cfg = withReplayKey(cfg, func(callerService, nonce string) string {
		cbCount++
		return "" // empty replay key
	})

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("POST", "/path", nil, "nonce-rf003d-1")

	err := verifier.Verify(context.Background(), headers, "POST", "/path", nil)
	if !isErrEmptyReplayKey(err) {
		t.Errorf("expected ErrEmptyReplayKey, got %v", err)
	}

	if cbCount != 1 {
		t.Errorf("expected callback count 1, got %d", cbCount)
	}
	if ns.CallCount != 0 {
		t.Errorf("expected store count 0, got %d", ns.CallCount)
	}
}

func TestVerifier_CustomReplayKeyReplayRejected(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	ns := &testNonceStoreSpy{Result: true}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	var cbCount int
	cfg = withReplayKey(cfg, func(callerService, nonce string) string {
		cbCount++
		return callerService + ":" + nonce
	})

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("POST", "/path", nil, "nonce-rf003d-1")

	// Call 1
	err1 := verifier.Verify(context.Background(), headers, "POST", "/path", nil)
	if err1 != nil {
		t.Fatalf("first call failed: %v", err1)
	}

	// Call 2 with exact same headers/nonce
	err2 := verifier.Verify(context.Background(), headers, "POST", "/path", nil)
	if err2 == nil {
		t.Fatal("expected error on second call, got nil")
	}
	if err2.Error() != "nonce replay detected" {
		t.Errorf("expected exact error 'nonce replay detected', got '%v'", err2)
	}

	if cbCount != 2 {
		t.Errorf("expected callback count 2, got %d", cbCount)
	}
	if ns.CallCount != 2 {
		t.Errorf("expected store count 2, got %d", ns.CallCount)
	}
}

func TestUnaryInterceptor_ReplayKeyFailurePreservesStatus(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	ns := &testNonceStoreSpy{Result: true}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	var cbCount int
	cfg = withReplayKey(cfg, func(callerService, nonce string) string {
		cbCount++
		return "" // triggers failure
	})

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("GRPC", "/service/Method", nil, "nonce-rf003d-1")

	md := metadata.New(nil)
	for k, v := range headers {
		md.Set(k, v)
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)

	interceptor := verifier.UnaryServerInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/service/Method"}

	var handlerCount int
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		handlerCount++
		return nil, nil
	}

	_, err := interceptor(ctx, &emptypb.Empty{}, info, handler)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", st.Code())
	}
	if st.Message() != "Unauthorized" {
		t.Errorf("expected Unauthorized, got %v", st.Message())
	}

	if cbCount != 1 {
		t.Errorf("expected callback count 1, got %d", cbCount)
	}
	if ns.CallCount != 0 {
		t.Errorf("expected store count 0, got %d", ns.CallCount)
	}
	if handlerCount != 0 {
		t.Errorf("expected handler count 0, got %d", handlerCount)
	}
}

func TestStreamInterceptor_ReplayKeyFailurePreservesStatus(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	ns := &testNonceStoreSpy{Result: true}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	var cbCount int
	cfg = withReplayKey(cfg, func(callerService, nonce string) string {
		cbCount++
		return ""
	})

	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("GRPC", "/service/MethodStream", nil, "nonce-rf003d-1")

	md := metadata.New(nil)
	for k, v := range headers {
		md.Set(k, v)
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)
	stream := &testServerStream{ctx: ctx}

	interceptor := verifier.StreamServerInterceptor()
	info := &grpc.StreamServerInfo{FullMethod: "/service/MethodStream"}

	var handlerCount int
	handler := func(srv interface{}, stream grpc.ServerStream) error {
		handlerCount++
		return nil
	}

	err := interceptor(nil, stream, info, handler)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", st.Code())
	}
	if st.Message() != "Unauthorized" {
		t.Errorf("expected Unauthorized, got %v", st.Message())
	}

	if cbCount != 1 {
		t.Errorf("expected callback count 1, got %d", cbCount)
	}
	if ns.CallCount != 0 {
		t.Errorf("expected store count 0, got %d", ns.CallCount)
	}
	if handlerCount != 0 {
		t.Errorf("expected handler count 0, got %d", handlerCount)
	}
}

func TestStreamInterceptor_DefaultReplayKeyCompatibility(t *testing.T) {
	kp := &StaticKeyProvider{Keys: map[string][]Key{"api-gateway": {{ID: "key-1", Secret: []byte("secret")}}}}
	ns := &testNonceStoreSpy{Result: true}
	cfg := Config{ClockSkew: time.Minute, NonceTTL: time.Minute, Env: EnvTest}

	// nil replay callback
	verifier := NewVerifier(kp, ns, cfg)
	verifier.SetClock(StaticClock{T: time.Now()})

	signer := NewSigner("api-gateway", "key-1", []byte("secret"))
	signer.SetClock(StaticClock{T: time.Now()})
	headers, _ := signer.Sign("GRPC", "/service/MethodStream", nil, "nonce-rf003d-1")

	md := metadata.New(nil)
	for k, v := range headers {
		md.Set(k, v)
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)
	stream := &testServerStream{ctx: ctx}

	interceptor := verifier.StreamServerInterceptor()
	info := &grpc.StreamServerInfo{FullMethod: "/service/MethodStream"}

	var handlerCount int
	handler := func(srv interface{}, stream grpc.ServerStream) error {
		handlerCount++
		return nil
	}

	err := interceptor(nil, stream, info, handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ns.CallCount != 1 {
		t.Errorf("expected store count 1, got %d", ns.CallCount)
	}
	if len(ns.Keys) == 0 || ns.Keys[0] != "nonce-rf003d-1" {
		t.Errorf("expected store key nonce-rf003d-1, got %v", ns.Keys)
	}
	if handlerCount != 1 {
		t.Errorf("expected handler count 1, got %d", handlerCount)
	}
}
