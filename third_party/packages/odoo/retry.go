package odoo

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// RetryPolicy defines the bounded retry configuration for read operations.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryPolicy provides standard production retry parameters.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    2 * time.Second,
	}
}

func isConnectionReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset") || strings.Contains(msg, "econnreset")
}

func isProviderBusinessOrValidationError(err error) bool {
	if err == nil {
		return false
	}
	var odooErr *OdooError
	if errors.As(err, &odooErr) {
		return true
	}
	var structErr StructuredError
	if errors.As(err, &structErr) {
		return true
	}
	var de *destructiveImpactError
	if errors.As(err, &de) {
		return true
	}
	class := ClassifyOdooError(err.Error())
	switch class {
	case ErrClassAccessDenied,
		ErrClassValidationFailed,
		ErrClassBusinessPreconditionFailed,
		ErrClassReferentialConstraint,
		ErrClassInsufficientStockOrReservation,
		ErrClassNotFound,
		ErrClassDestructiveImpactRequiresConfirm:
		return true
	}
	if isAuthError(err) || strings.Contains(strings.ToLower(err.Error()), "authentication failed") {
		return true
	}
	return false
}

// IsRetryableError reports whether an error is transient and safe to retry on read operations.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "context canceled") || strings.Contains(msg, "deadline exceeded") {
		return false
	}
	if isProviderBusinessOrValidationError(err) {
		return false
	}
	if isConnectionReset(err) {
		return true
	}
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode == http.StatusServiceUnavailable
	}
	return false
}

func backoffWithJitter(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	if baseDelay <= 0 {
		return 0
	}
	shift := attempt - 1
	if shift > 30 {
		shift = 30
	}
	delay := baseDelay * (1 << shift)
	if maxDelay > 0 && (delay > maxDelay || delay < 0) {
		delay = maxDelay
	}
	// Jitter up to 25% of delay
	var jitter time.Duration
	if delay > 0 {
		maxJitter := int64(delay / 4)
		if maxJitter > 0 {
			if n, err := rand.Int(rand.Reader, big.NewInt(maxJitter)); err == nil {
				jitter = time.Duration(n.Int64())
			}
		}
	}
	total := delay + jitter
	if maxDelay > 0 && total > maxDelay {
		total = maxDelay
	}
	return total
}

// Retry executes fn with bounded exponential backoff and jitter. Only StrategyRead may retry.
// Mutations, method calls, wizards, module installs and raw calls execute exactly once.
func Retry(ctx context.Context, policy RetryPolicy, strategy ExecutionStrategy, fn func(context.Context) error) error {
	if strategy != StrategyRead {
		return fn(ctx)
	}

	maxAttempts := policy.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err

		if attempt >= maxAttempts || !IsRetryableError(err) {
			return lastErr
		}

		delay := backoffWithJitter(attempt, policy.BaseDelay, policy.MaxDelay)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return lastErr
}
