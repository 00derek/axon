// middleware/timeout.go
package middleware

import (
	"context"
	"time"

	"github.com/axonframework/axon/kernel"
)

// WithTimeout returns middleware that wraps each LLM call with a context timeout.
// If the LLM call takes longer than d, the context is cancelled and the call
// returns context.DeadlineExceeded.
func WithTimeout(d time.Duration) Middleware {
	return func(next kernel.LLM) kernel.LLM {
		return &timeoutLLM{next: next, timeout: d}
	}
}

type timeoutLLM struct {
	next    kernel.LLM
	timeout time.Duration
}

func (t *timeoutLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	return t.next.Generate(ctx, params)
}

func (t *timeoutLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	// Note: cancel is deferred here. For long-lived streams the caller should
	// manage their own context. The timeout applies to the initial stream creation.
	defer cancel()
	return t.next.GenerateStream(ctx, params)
}

func (t *timeoutLLM) Model() string {
	return t.next.Model()
}
