package odoo_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/asao-platform/atoc/packages/odoo"
)

func TestRetryPolicy_StrategyRead_Retries(t *testing.T) {
	t.Run("retry on connection reset until success", func(t *testing.T) {
		var attempts int32
		policy := odoo.RetryPolicy{
			MaxAttempts: 3,
			BaseDelay:   time.Millisecond,
			MaxDelay:    5 * time.Millisecond,
		}
		err := odoo.Retry(context.Background(), policy, odoo.StrategyRead, func(ctx context.Context) error {
			val := atomic.AddInt32(&attempts, 1)
			if val < 3 {
				return syscall.ECONNRESET
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected success on 3rd attempt, got: %v", err)
		}
		if attempts != 3 {
			t.Fatalf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("retry on connection reset exhausted", func(t *testing.T) {
		var attempts int32
		policy := odoo.RetryPolicy{
			MaxAttempts: 3,
			BaseDelay:   time.Millisecond,
			MaxDelay:    5 * time.Millisecond,
		}
		connErr := errors.New("read: connection reset by peer")
		err := odoo.Retry(context.Background(), policy, odoo.StrategyRead, func(ctx context.Context) error {
			atomic.AddInt32(&attempts, 1)
			return connErr
		})
		if !errors.Is(err, connErr) {
			t.Fatalf("expected connErr returned, got: %v", err)
		}
		if attempts != 3 {
			t.Fatalf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("retry on HTTP 429", func(t *testing.T) {
		var attempts int32
		policy := odoo.RetryPolicy{
			MaxAttempts: 3,
			BaseDelay:   time.Millisecond,
			MaxDelay:    5 * time.Millisecond,
		}
		http429 := &odoo.HTTPStatusError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"}
		err := odoo.Retry(context.Background(), policy, odoo.StrategyRead, func(ctx context.Context) error {
			val := atomic.AddInt32(&attempts, 1)
			if val < 2 {
				return http429
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
		if attempts != 2 {
			t.Fatalf("expected 2 attempts, got %d", attempts)
		}
	})

	t.Run("retry on HTTP 503", func(t *testing.T) {
		var attempts int32
		policy := odoo.RetryPolicy{
			MaxAttempts: 3,
			BaseDelay:   time.Millisecond,
			MaxDelay:    5 * time.Millisecond,
		}
		http503 := &odoo.HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
		err := odoo.Retry(context.Background(), policy, odoo.StrategyRead, func(ctx context.Context) error {
			atomic.AddInt32(&attempts, 1)
			return http503
		})
		if !errors.Is(err, http503) {
			t.Fatalf("expected http503 returned, got: %v", err)
		}
		if attempts != 3 {
			t.Fatalf("expected 3 attempts, got %d", attempts)
		}
	})
}

func TestRetryPolicy_NoRetryOnBusinessOrValidation(t *testing.T) {
	testCases := []struct {
		name string
		err  error
	}{
		{
			name: "validation error",
			err:  &odoo.OdooError{Code: 200, Message: "ValidationError: invalid field values"},
		},
		{
			name: "access denied error",
			err:  &odoo.OdooError{Code: 200, Message: "Access Denied: cannot be called remotely"},
		},
		{
			name: "authentication error",
			err:  errors.New("odoo authentication failed for login \"admin\""),
		},
		{
			name: "business precondition error",
			err:  &odoo.OdooError{Code: 200, Message: "UserError: required to validate this document"},
		},
		{
			name: "referential constraint error",
			err:  &odoo.OdooError{Code: 200, Message: "violates foreign key constraint"},
		},
		{
			name: "not found error",
			err:  &odoo.OdooError{Code: 200, Message: "record does not exist"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var attempts int32
			policy := odoo.RetryPolicy{
				MaxAttempts: 3,
				BaseDelay:   time.Millisecond,
				MaxDelay:    5 * time.Millisecond,
			}
			err := odoo.Retry(context.Background(), policy, odoo.StrategyRead, func(ctx context.Context) error {
				atomic.AddInt32(&attempts, 1)
				return tc.err
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("expected original error %v, got %v", tc.err, err)
			}
			if attempts != 1 {
				t.Fatalf("expected exactly 1 attempt for %s, got %d", tc.name, attempts)
			}
		})
	}
}

func TestRetryPolicy_MutationsNeverRetry(t *testing.T) {
	nonReadStrategies := []struct {
		name     string
		strategy odoo.ExecutionStrategy
	}{
		{"field_write", odoo.StrategyFieldWrite},
		{"method_call", odoo.StrategyMethodCall},
		{"wizard", odoo.StrategyWizard},
		{"destructive_guarded", odoo.StrategyDestructiveGuarded},
		{"module_install", odoo.StrategyModuleInstall},
		{"unknown_raw", odoo.ExecutionStrategy("RAW_CALL")},
	}

	for _, tc := range nonReadStrategies {
		t.Run(tc.name+" does not retry HTTP 503", func(t *testing.T) {
			var attempts int32
			policy := odoo.RetryPolicy{
				MaxAttempts: 3,
				BaseDelay:   time.Millisecond,
				MaxDelay:    5 * time.Millisecond,
			}
			http503 := &odoo.HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
			err := odoo.Retry(context.Background(), policy, tc.strategy, func(ctx context.Context) error {
				atomic.AddInt32(&attempts, 1)
				return http503
			})
			if !errors.Is(err, http503) {
				t.Fatalf("expected http503, got: %v", err)
			}
			if attempts != 1 {
				t.Fatalf("expected exactly 1 attempt for strategy %s, got %d", tc.strategy, attempts)
			}
		})

		t.Run(tc.name+" does not retry connection reset", func(t *testing.T) {
			var attempts int32
			policy := odoo.RetryPolicy{
				MaxAttempts: 3,
				BaseDelay:   time.Millisecond,
				MaxDelay:    5 * time.Millisecond,
			}
			err := odoo.Retry(context.Background(), policy, tc.strategy, func(ctx context.Context) error {
				atomic.AddInt32(&attempts, 1)
				return syscall.ECONNRESET
			})
			if !errors.Is(err, syscall.ECONNRESET) {
				t.Fatalf("expected ECONNRESET, got: %v", err)
			}
			if attempts != 1 {
				t.Fatalf("expected exactly 1 attempt for strategy %s, got %d", tc.strategy, attempts)
			}
		})
	}
}

func TestRetryPolicy_ContextCancellationStopsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var attempts int32
	policy := odoo.RetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    500 * time.Millisecond,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- odoo.Retry(ctx, policy, odoo.StrategyRead, func(callCtx context.Context) error {
			atomic.AddInt32(&attempts, 1)
			// Cancel after first failure
			cancel()
			return syscall.ECONNRESET
		})
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if attempts != 1 {
			t.Fatalf("expected 1 attempt before cancellation stopped loop, got %d", attempts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Retry to abort on context cancellation")
	}
}

func TestHTTPStatusError_DoesNotLeakBody(t *testing.T) {
	err := &odoo.HTTPStatusError{StatusCode: 503, Status: "Service Unavailable"}
	msg := err.Error()
	if msg != "odoo transport: HTTP 503 (Service Unavailable)" {
		t.Fatalf("unexpected error message: %q", msg)
	}
}
