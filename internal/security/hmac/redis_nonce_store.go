package hmac

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisNonceStore implements NonceStore using a Redis backend.
type RedisNonceStore struct {
	client redis.Cmdable
	prefix string
}

// NewRedisNonceStore creates a new RedisNonceStore.
func NewRedisNonceStore(client redis.Cmdable, prefix string) *RedisNonceStore {
	if prefix == "" {
		prefix = "hmac:nonce:"
	}
	return &RedisNonceStore{
		client: client,
		prefix: prefix,
	}
}

// CheckAndSet checks if the nonce exists. If not, it sets it with the given TTL and returns true.
// If the nonce already exists, it returns false.
func (r *RedisNonceStore) CheckAndSet(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	if nonce == "" {
		return false, fmt.Errorf("nonce cannot be empty")
	}
	key := r.prefix + nonce
	// SetNX sets the key only if it does not exist.
	// Returns true if set, false if key already exists.
	set, err := r.client.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis setnx failed: %w", err)
	}
	return set, nil
}
