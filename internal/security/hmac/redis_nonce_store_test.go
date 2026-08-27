package hmac

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type mockRedisClient struct {
	redis.Cmdable
	store map[string]time.Time
	err   error
}

func (m *mockRedisClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(ctx)
	if m.err != nil {
		cmd.SetErr(m.err)
		return cmd
	}

	now := time.Now()
	if expire, exists := m.store[key]; exists && now.Before(expire) {
		cmd.SetVal(false)
		return cmd
	}

	m.store[key] = now.Add(expiration)
	cmd.SetVal(true)
	return cmd
}

func TestRedisNonceStore(t *testing.T) {
	ctx := context.Background()

	t.Run("first nonce succeeds", func(t *testing.T) {
		client := &mockRedisClient{
			store: make(map[string]time.Time),
		}
		store := NewRedisNonceStore(client, "test:")

		ok, err := store.CheckAndSet(ctx, "nonce1", 10*time.Second)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !ok {
			t.Error("expected CheckAndSet to return true for first use")
		}
	})

	t.Run("duplicate nonce fails", func(t *testing.T) {
		client := &mockRedisClient{
			store: make(map[string]time.Time),
		}
		store := NewRedisNonceStore(client, "test:")

		// First call
		ok, err := store.CheckAndSet(ctx, "nonce1", 10*time.Second)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !ok {
			t.Error("expected first call to succeed")
		}

		// Second call (duplicate)
		ok, err = store.CheckAndSet(ctx, "nonce1", 10*time.Second)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if ok {
			t.Error("expected CheckAndSet to return false for duplicate nonce")
		}
	})

	t.Run("redis error propagates", func(t *testing.T) {
		expectedErr := errors.New("redis connection refused")
		client := &mockRedisClient{
			err: expectedErr,
		}
		store := NewRedisNonceStore(client, "test:")

		ok, err := store.CheckAndSet(ctx, "nonce1", 10*time.Second)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if ok {
			t.Error("expected ok to be false on error")
		}
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected wrapped error containing %v, got %v", expectedErr, err)
		}
	})

	t.Run("empty nonce returns error", func(t *testing.T) {
		client := &mockRedisClient{
			store: make(map[string]time.Time),
		}
		store := NewRedisNonceStore(client, "test:")

		_, err := store.CheckAndSet(ctx, "", 10*time.Second)
		if err == nil {
			t.Error("expected error for empty nonce, got nil")
		}
	})
}
