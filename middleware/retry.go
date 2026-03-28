// middleware/retry.go
package middleware

import (
	"context"
	"math"
	"math/rand/v2"
	"time"

	"github.com/axonframework/axon/kernel"
)

// WithRetry returns middleware that retries failed Generate calls up to
// maxAttempts times using exponential backoff with jitter.
//
// The backoff for attempt n (0-indexed) is:
//
//	delay = baseDelay * 2^n * (0.5 + rand(0, 0.5))
//
// GenerateStream is NOT retried because stream failures are not idempotent.
// Context cancellation is respected between retry attempts.
func WithRetry(maxAttempts int, baseDelay time.Duration) Middleware {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return func(next kernel.LLM) kernel.LLM {
		return &retryLLM{next: next, maxAttempts: maxAttempts, baseDelay: baseDelay}
	}
}

type retryLLM struct {
	next        kernel.LLM
	maxAttempts int
	baseDelay   time.Duration
}

func (r *retryLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	var lastErr error
	for attempt := 0; attempt < r.maxAttempts; attempt++ {
		// Respect context cancellation before each attempt.
		if ctx.Err() != nil {
			return kernel.Response{}, ctx.Err()
		}

		resp, err := r.next.Generate(ctx, params)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// Don't sleep after the last attempt.
		if attempt < r.maxAttempts-1 {
			delay := r.backoff(attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return kernel.Response{}, ctx.Err()
			}
		}
	}
	return kernel.Response{}, lastErr
}

func (r *retryLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	// Streams are not retried.
	return r.next.GenerateStream(ctx, params)
}

func (r *retryLLM) Model() string {
	return r.next.Model()
}

// backoff calculates exponential backoff with jitter for the given attempt.
// delay = baseDelay * 2^attempt * (0.5 + rand(0, 0.5))
func (r *retryLLM) backoff(attempt int) time.Duration {
	exp := math.Pow(2, float64(attempt))
	jitter := 0.5 + rand.Float64()*0.5
	return time.Duration(float64(r.baseDelay) * exp * jitter)
}
