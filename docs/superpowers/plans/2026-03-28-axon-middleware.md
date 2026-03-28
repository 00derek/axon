# Axon Middleware Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Axon middleware package -- LLM wrapping with composable middleware (logging, retry, timeout, metrics, cost tracking), a condition-based router, and convenience routing strategies (cascade, round-robin, token/tool routing).

**Architecture:** Single Go package `middleware/` that depends only on `kernel/`. Middleware is `func(kernel.LLM) kernel.LLM` -- a decorator pattern. The router IS a `kernel.LLM`. Convenience strategies (Cascade, RoundRobin, RouteByTokenCount, RouteByToolCount) build on top of the router. All middleware is applied at construction time; the agent never knows it is talking to a wrapped LLM.

**Tech Stack:** Go 1.25, stdlib + `github.com/axonframework/axon/kernel` (local replace directive)

**Source spec:** `docs/superpowers/specs/2026-03-28-axon-framework-design.md`, Sections 6.1--6.5

**Kernel dependency note:** The kernel package is not yet implemented but its types are fully designed. This plan references kernel types (`LLM`, `GenerateParams`, `Response`, `Usage`, `Stream`, `StreamEvent`, `Message`, `Tool`, `ToolCall`) as defined in the kernel implementation plan. All middleware files import `github.com/axonframework/axon/kernel` and use the `replace` directive to resolve it locally.

---

## File Structure

```
middleware/
├── go.mod              # module github.com/axonframework/axon/middleware
├── middleware.go        # Middleware type, Wrap function
├── middleware_test.go   # fakeLLM helper, Wrap tests
├── retry.go            # WithRetry (exponential backoff + jitter)
├── retry_test.go
├── logger.go           # WithLogging (slog.Logger)
├── logger_test.go
├── metrics.go          # WithMetrics, MetricsCollector interface
├── metrics_test.go
├── timeout.go          # WithTimeout (context.WithTimeout)
├── timeout_test.go
├── cost.go             # WithCostTracker, CostTracker (sync.Mutex)
├── cost_test.go
├── router.go           # NewRouter, Route, RouteContext
├── router_test.go
├── cascade.go          # Cascade, RouteByTokenCount, RouteByToolCount, RoundRobin
├── cascade_test.go
```

---

## Kernel Types Reference

These types from the kernel package are used throughout this plan. They are NOT implemented yet but their signatures are final.

```go
// kernel.LLM -- the interface all middleware wraps
type LLM interface {
    Generate(ctx context.Context, params GenerateParams) (Response, error)
    GenerateStream(ctx context.Context, params GenerateParams) (Stream, error)
    Model() string
}

// kernel.GenerateParams
type GenerateParams struct {
    Messages []Message
    Tools    []Tool
    Options  GenerateOptions
}

// kernel.Response
type Response struct {
    Text         string     `json:"text"`
    ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
    Usage        Usage      `json:"usage"`
    FinishReason string     `json:"finish_reason"`
}

// kernel.Usage
type Usage struct {
    InputTokens  int           `json:"input_tokens"`
    OutputTokens int           `json:"output_tokens"`
    TotalTokens  int           `json:"total_tokens"`
    Latency      time.Duration `json:"latency"`
}

// kernel.Stream
type Stream interface {
    Events() <-chan StreamEvent
    Text() <-chan string
    Response() Response
    Err() error
}

// kernel.Message, kernel.ToolCall, kernel.Tool, etc. -- see kernel plan for full definitions.
```

---

### Task 1: Initialize Go module, Middleware type, Wrap function, and fakeLLM test helper

**Files:**
- Create: `middleware/go.mod`
- Create: `middleware/middleware.go`
- Create: `middleware/middleware_test.go`

This task establishes the core abstraction: `Middleware` is a function that wraps a `kernel.LLM` and returns a new `kernel.LLM`. `Wrap` applies middleware in order (first middleware is outermost). The `fakeLLM` test helper implements `kernel.LLM` and is reused by every subsequent test file.

- [ ] **Step 1: Write the test file**

```go
// middleware/middleware_test.go
package middleware

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// fakeLLM implements kernel.LLM for testing.
type fakeLLM struct {
	model       string
	generateFn  func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error)
	streamFn    func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error)
	callCount   int
	lastParams  kernel.GenerateParams
	mu          sync.Mutex
}

func newFakeLLM(model string) *fakeLLM {
	return &fakeLLM{
		model: model,
		generateFn: func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
			return kernel.Response{
				Text:  "fake response",
				Usage: kernel.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}, nil
		},
	}
}

func (f *fakeLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	f.mu.Lock()
	f.callCount++
	f.lastParams = params
	fn := f.generateFn
	f.mu.Unlock()
	return fn(ctx, params)
}

func (f *fakeLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	f.mu.Lock()
	f.callCount++
	f.lastParams = params
	fn := f.streamFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return nil, nil
}

func (f *fakeLLM) Model() string {
	return f.model
}

// fakeStream implements kernel.Stream for testing.
type fakeStream struct {
	resp kernel.Response
	err  error
}

func (s *fakeStream) Events() <-chan kernel.StreamEvent {
	ch := make(chan kernel.StreamEvent)
	close(ch)
	return ch
}

func (s *fakeStream) Text() <-chan string {
	ch := make(chan string)
	close(ch)
	return ch
}

func (s *fakeStream) Response() kernel.Response {
	return s.resp
}

func (s *fakeStream) Err() error {
	return s.err
}

// --- Tests ---

func TestWrapNoMiddleware(t *testing.T) {
	f := newFakeLLM("test-model")
	wrapped := Wrap(f)

	// With no middleware, Wrap returns the original LLM.
	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "fake response" {
		t.Errorf("expected %q, got %q", "fake response", resp.Text)
	}
	if wrapped.Model() != "test-model" {
		t.Errorf("expected model %q, got %q", "test-model", wrapped.Model())
	}
}

func TestWrapSingleMiddleware(t *testing.T) {
	f := newFakeLLM("test-model")

	// Middleware that prefixes the response text.
	prefix := func(next kernel.LLM) kernel.LLM {
		return &prefixLLM{next: next, prefix: "MW:"}
	}

	wrapped := Wrap(f, prefix)
	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "MW:fake response" {
		t.Errorf("expected %q, got %q", "MW:fake response", resp.Text)
	}
}

func TestWrapMultipleMiddleware(t *testing.T) {
	f := newFakeLLM("test-model")

	mwA := func(next kernel.LLM) kernel.LLM {
		return &prefixLLM{next: next, prefix: "A:"}
	}
	mwB := func(next kernel.LLM) kernel.LLM {
		return &prefixLLM{next: next, prefix: "B:"}
	}

	// Wrap(llm, A, B) means A is outermost: A wraps B wraps llm.
	// Call flow: A.Generate -> B.Generate -> llm.Generate
	// B prefixes first: "B:fake response"
	// A prefixes that: "A:B:fake response"
	wrapped := Wrap(f, mwA, mwB)
	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "A:B:fake response" {
		t.Errorf("expected %q, got %q", "A:B:fake response", resp.Text)
	}
}

// prefixLLM is a test helper that prefixes response text.
type prefixLLM struct {
	next   kernel.LLM
	prefix string
}

func (p *prefixLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	resp, err := p.next.Generate(ctx, params)
	if err != nil {
		return resp, err
	}
	resp.Text = p.prefix + resp.Text
	return resp, nil
}

func (p *prefixLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return p.next.GenerateStream(ctx, params)
}

func (p *prefixLLM) Model() string {
	return p.next.Model()
}

// suppress unused import warnings -- json is used by other test files in this package
var _ = json.Marshal
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v`
Expected: FAIL (module and source files do not exist yet)

- [ ] **Step 3: Create go.mod**

Run:
```bash
cd /Users/derek/repo/axons/middleware && go mod init github.com/axonframework/axon/middleware
```

Then edit `middleware/go.mod` to add the kernel dependency and replace directive:

```
module github.com/axonframework/axon/middleware

go 1.25

require github.com/axonframework/axon/kernel v0.0.0

replace github.com/axonframework/axon/kernel => ../kernel
```

- [ ] **Step 4: Write the implementation**

```go
// middleware/middleware.go
package middleware

import "github.com/axonframework/axon/kernel"

// Middleware wraps a kernel.LLM, returning a new kernel.LLM with added behavior.
// Middleware is applied at construction time. The agent does not know it is
// talking to a wrapped LLM.
type Middleware func(kernel.LLM) kernel.LLM

// Wrap applies the given middleware to an LLM in order. The first middleware
// in the list is the outermost wrapper (called first on each request).
//
// Example:
//
//	wrapped := Wrap(llm, WithRetry(3, time.Second), WithLogging(logger))
//	// Call flow: WithRetry -> WithLogging -> llm
func Wrap(llm kernel.LLM, mw ...Middleware) kernel.LLM {
	// Apply in reverse so that mw[0] is outermost.
	for i := len(mw) - 1; i >= 0; i-- {
		llm = mw[i](llm)
	}
	return llm
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v`
Expected: PASS (all 3 tests)

- [ ] **Step 6: Commit**

```bash
git add middleware/
git commit -m "feat(middleware): add Middleware type, Wrap function, and fakeLLM test helper"
```

---

### Task 2: WithTimeout middleware

**Files:**
- Create: `middleware/timeout.go`
- Create: `middleware/timeout_test.go`

WithTimeout wraps the context with `context.WithTimeout` before calling the inner LLM. If the LLM call exceeds the timeout, the context cancellation propagates and the call returns `context.DeadlineExceeded`.

- [ ] **Step 1: Write the test file**

```go
// middleware/timeout_test.go
package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/axonframework/axon/kernel"
)

func TestWithTimeoutSuccess(t *testing.T) {
	f := newFakeLLM("test-model")

	wrapped := Wrap(f, WithTimeout(5*time.Second))
	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "fake response" {
		t.Errorf("expected %q, got %q", "fake response", resp.Text)
	}
}

func TestWithTimeoutExceeded(t *testing.T) {
	f := newFakeLLM("test-model")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		// Simulate a slow LLM call that respects context cancellation.
		select {
		case <-time.After(5 * time.Second):
			return kernel.Response{Text: "slow response"}, nil
		case <-ctx.Done():
			return kernel.Response{}, ctx.Err()
		}
	}

	wrapped := Wrap(f, WithTimeout(50*time.Millisecond))
	_, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestWithTimeoutStream(t *testing.T) {
	f := newFakeLLM("test-model")
	f.streamFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
		return &fakeStream{resp: kernel.Response{Text: "streamed"}}, nil
	}

	wrapped := Wrap(f, WithTimeout(5*time.Second))
	stream, err := wrapped.GenerateStream(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stream.Response().Text != "streamed" {
		t.Errorf("expected %q, got %q", "streamed", stream.Response().Text)
	}
}

func TestWithTimeoutModelPassthrough(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	wrapped := Wrap(f, WithTimeout(5*time.Second))
	if wrapped.Model() != "claude-sonnet" {
		t.Errorf("expected model %q, got %q", "claude-sonnet", wrapped.Model())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithTimeout`
Expected: FAIL (timeout.go does not exist yet)

- [ ] **Step 3: Write the implementation**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithTimeout`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add middleware/timeout.go middleware/timeout_test.go
git commit -m "feat(middleware): add WithTimeout middleware"
```

---

### Task 3: WithRetry middleware with exponential backoff and jitter

**Files:**
- Create: `middleware/retry.go`
- Create: `middleware/retry_test.go`

WithRetry wraps the LLM with retry logic. On failure, it retries up to `maxAttempts` times using exponential backoff with jitter. Only `Generate` is retried; `GenerateStream` is not retried (stream failures are not idempotent).

- [ ] **Step 1: Write the test file**

```go
// middleware/retry_test.go
package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/axonframework/axon/kernel"
)

func TestWithRetryNoError(t *testing.T) {
	f := newFakeLLM("test-model")

	wrapped := Wrap(f, WithRetry(3, 10*time.Millisecond))
	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "fake response" {
		t.Errorf("expected %q, got %q", "fake response", resp.Text)
	}
	f.mu.Lock()
	count := f.callCount
	f.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 call, got %d", count)
	}
}

func TestWithRetryEventualSuccess(t *testing.T) {
	f := newFakeLLM("test-model")
	attempts := 0
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		attempts++
		if attempts < 3 {
			return kernel.Response{}, errors.New("transient error")
		}
		return kernel.Response{Text: "success on attempt 3"}, nil
	}

	wrapped := Wrap(f, WithRetry(5, 1*time.Millisecond))
	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "success on attempt 3" {
		t.Errorf("expected %q, got %q", "success on attempt 3", resp.Text)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestWithRetryExhausted(t *testing.T) {
	f := newFakeLLM("test-model")
	callErr := errors.New("persistent error")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, callErr
	}

	wrapped := Wrap(f, WithRetry(3, 1*time.Millisecond))
	_, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, callErr) {
		t.Errorf("expected %v, got %v", callErr, err)
	}
	f.mu.Lock()
	count := f.callCount
	f.mu.Unlock()
	if count != 3 {
		t.Errorf("expected 3 calls, got %d", count)
	}
}

func TestWithRetryRespectsContextCancellation(t *testing.T) {
	f := newFakeLLM("test-model")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("fail")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	wrapped := Wrap(f, WithRetry(10, 1*time.Second))
	_, err := wrapped.Generate(ctx, kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestWithRetryStreamNotRetried(t *testing.T) {
	f := newFakeLLM("test-model")
	streamCalls := 0
	f.streamFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
		streamCalls++
		return nil, errors.New("stream error")
	}

	wrapped := Wrap(f, WithRetry(3, 1*time.Millisecond))
	_, err := wrapped.GenerateStream(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if streamCalls != 1 {
		t.Errorf("expected 1 stream call (no retry), got %d", streamCalls)
	}
}

func TestWithRetryModelPassthrough(t *testing.T) {
	f := newFakeLLM("claude-haiku")
	wrapped := Wrap(f, WithRetry(3, time.Second))
	if wrapped.Model() != "claude-haiku" {
		t.Errorf("expected model %q, got %q", "claude-haiku", wrapped.Model())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithRetry`
Expected: FAIL (retry.go does not exist yet)

- [ ] **Step 3: Write the implementation**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithRetry`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Commit**

```bash
git add middleware/retry.go middleware/retry_test.go
git commit -m "feat(middleware): add WithRetry middleware with exponential backoff and jitter"
```

---

### Task 4: WithLogging middleware

**Files:**
- Create: `middleware/logger.go`
- Create: `middleware/logger_test.go`

WithLogging logs LLM calls using the stdlib `slog.Logger`. It logs the model name, latency, token usage, and errors. To make the logging middleware testable, the test uses `slog.New` with a custom handler that captures log records.

- [ ] **Step 1: Write the test file**

```go
// middleware/logger_test.go
package middleware

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/axonframework/axon/kernel"
)

// captureHandler captures slog records for testing.
type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func TestWithLoggingSuccess(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{
			Text:  "hello",
			Usage: kernel.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		}, nil
	}

	handler := &captureHandler{}
	logger := slog.New(handler)

	wrapped := Wrap(f, WithLogging(logger))
	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello" {
		t.Errorf("expected %q, got %q", "hello", resp.Text)
	}

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(handler.records))
	}

	rec := handler.records[0]
	if rec.Level != slog.LevelInfo {
		t.Errorf("expected level Info, got %v", rec.Level)
	}
	if rec.Message != "llm.generate" {
		t.Errorf("expected message %q, got %q", "llm.generate", rec.Message)
	}

	// Check that key attributes are present.
	attrs := map[string]any{}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	if attrs["model"] != "claude-sonnet" {
		t.Errorf("expected model attr %q, got %v", "claude-sonnet", attrs["model"])
	}
	if attrs["input_tokens"] != int64(10) {
		t.Errorf("expected input_tokens 10, got %v", attrs["input_tokens"])
	}
	if attrs["output_tokens"] != int64(20) {
		t.Errorf("expected output_tokens 20, got %v", attrs["output_tokens"])
	}
}

func TestWithLoggingError(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("api error")
	}

	handler := &captureHandler{}
	logger := slog.New(handler)

	wrapped := Wrap(f, WithLogging(logger))
	_, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(handler.records))
	}

	rec := handler.records[0]
	if rec.Level != slog.LevelError {
		t.Errorf("expected level Error, got %v", rec.Level)
	}

	attrs := map[string]any{}
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	if attrs["error"] != "api error" {
		t.Errorf("expected error attr %q, got %v", "api error", attrs["error"])
	}
}

func TestWithLoggingStream(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.streamFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
		return &fakeStream{resp: kernel.Response{Text: "streamed"}}, nil
	}

	handler := &captureHandler{}
	logger := slog.New(handler)

	wrapped := Wrap(f, WithLogging(logger))
	stream, err := wrapped.GenerateStream(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stream.Response().Text != "streamed" {
		t.Errorf("expected %q, got %q", "streamed", stream.Response().Text)
	}

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(handler.records))
	}
	if handler.records[0].Message != "llm.generate_stream" {
		t.Errorf("expected message %q, got %q", "llm.generate_stream", handler.records[0].Message)
	}
}

func TestWithLoggingModelPassthrough(t *testing.T) {
	f := newFakeLLM("claude-opus")
	handler := &captureHandler{}
	logger := slog.New(handler)

	wrapped := Wrap(f, WithLogging(logger))
	if wrapped.Model() != "claude-opus" {
		t.Errorf("expected model %q, got %q", "claude-opus", wrapped.Model())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithLogging`
Expected: FAIL (logger.go does not exist yet)

- [ ] **Step 3: Write the implementation**

```go
// middleware/logger.go
package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/axonframework/axon/kernel"
)

// WithLogging returns middleware that logs each LLM call using the provided
// slog.Logger. Successful calls are logged at Info level; errors at Error level.
// Logged attributes: model, latency, input_tokens, output_tokens, total_tokens.
func WithLogging(logger *slog.Logger) Middleware {
	return func(next kernel.LLM) kernel.LLM {
		return &loggingLLM{next: next, logger: logger}
	}
}

type loggingLLM struct {
	next   kernel.LLM
	logger *slog.Logger
}

func (l *loggingLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	start := time.Now()
	resp, err := l.next.Generate(ctx, params)
	elapsed := time.Since(start)

	if err != nil {
		l.logger.LogAttrs(ctx, slog.LevelError, "llm.generate",
			slog.String("model", l.next.Model()),
			slog.Duration("latency", elapsed),
			slog.String("error", err.Error()),
		)
		return resp, err
	}

	l.logger.LogAttrs(ctx, slog.LevelInfo, "llm.generate",
		slog.String("model", l.next.Model()),
		slog.Duration("latency", elapsed),
		slog.Int64("input_tokens", int64(resp.Usage.InputTokens)),
		slog.Int64("output_tokens", int64(resp.Usage.OutputTokens)),
		slog.Int64("total_tokens", int64(resp.Usage.TotalTokens)),
	)
	return resp, nil
}

func (l *loggingLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	start := time.Now()
	stream, err := l.next.GenerateStream(ctx, params)
	elapsed := time.Since(start)

	if err != nil {
		l.logger.LogAttrs(ctx, slog.LevelError, "llm.generate_stream",
			slog.String("model", l.next.Model()),
			slog.Duration("latency", elapsed),
			slog.String("error", err.Error()),
		)
		return stream, err
	}

	l.logger.LogAttrs(ctx, slog.LevelInfo, "llm.generate_stream",
		slog.String("model", l.next.Model()),
		slog.Duration("latency", elapsed),
	)
	return stream, nil
}

func (l *loggingLLM) Model() string {
	return l.next.Model()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithLogging`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add middleware/logger.go middleware/logger_test.go
git commit -m "feat(middleware): add WithLogging middleware using slog"
```

---

### Task 5: WithMetrics middleware

**Files:**
- Create: `middleware/metrics.go`
- Create: `middleware/metrics_test.go`

WithMetrics records each LLM call via a `MetricsCollector` interface. The collector receives the model name, usage, and any error. This allows plugging in any metrics backend (statsd, prometheus, etc.).

- [ ] **Step 1: Write the test file**

```go
// middleware/metrics_test.go
package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// fakeCollector implements MetricsCollector for testing.
type fakeCollector struct {
	mu    sync.Mutex
	calls []metricsCall
}

type metricsCall struct {
	model string
	usage kernel.Usage
	err   error
}

func (c *fakeCollector) RecordLLMCall(model string, usage kernel.Usage, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, metricsCall{model: model, usage: usage, err: err})
}

func TestWithMetricsSuccess(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{
			Text:  "hello",
			Usage: kernel.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		}, nil
	}

	collector := &fakeCollector{}
	wrapped := Wrap(f, WithMetrics(collector))

	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello" {
		t.Errorf("expected %q, got %q", "hello", resp.Text)
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.calls) != 1 {
		t.Fatalf("expected 1 metrics call, got %d", len(collector.calls))
	}

	mc := collector.calls[0]
	if mc.model != "claude-sonnet" {
		t.Errorf("expected model %q, got %q", "claude-sonnet", mc.model)
	}
	if mc.usage.InputTokens != 10 {
		t.Errorf("expected input_tokens 10, got %d", mc.usage.InputTokens)
	}
	if mc.usage.OutputTokens != 20 {
		t.Errorf("expected output_tokens 20, got %d", mc.usage.OutputTokens)
	}
	if mc.err != nil {
		t.Errorf("expected nil error, got %v", mc.err)
	}
}

func TestWithMetricsError(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	apiErr := errors.New("rate limited")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, apiErr
	}

	collector := &fakeCollector{}
	wrapped := Wrap(f, WithMetrics(collector))

	_, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.calls) != 1 {
		t.Fatalf("expected 1 metrics call, got %d", len(collector.calls))
	}
	if collector.calls[0].err != apiErr {
		t.Errorf("expected error %v, got %v", apiErr, collector.calls[0].err)
	}
}

func TestWithMetricsMultipleCalls(t *testing.T) {
	f := newFakeLLM("test-model")
	collector := &fakeCollector{}
	wrapped := Wrap(f, WithMetrics(collector))

	for i := 0; i < 5; i++ {
		_, _ = wrapped.Generate(context.Background(), kernel.GenerateParams{})
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.calls) != 5 {
		t.Errorf("expected 5 metrics calls, got %d", len(collector.calls))
	}
}

func TestWithMetricsModelPassthrough(t *testing.T) {
	f := newFakeLLM("claude-haiku")
	collector := &fakeCollector{}
	wrapped := Wrap(f, WithMetrics(collector))
	if wrapped.Model() != "claude-haiku" {
		t.Errorf("expected model %q, got %q", "claude-haiku", wrapped.Model())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithMetrics`
Expected: FAIL (metrics.go does not exist yet)

- [ ] **Step 3: Write the implementation**

```go
// middleware/metrics.go
package middleware

import (
	"context"

	"github.com/axonframework/axon/kernel"
)

// MetricsCollector is the interface for recording LLM call metrics.
// Implementations can forward to any metrics backend (statsd, prometheus, etc.).
type MetricsCollector interface {
	RecordLLMCall(model string, usage kernel.Usage, err error)
}

// WithMetrics returns middleware that records each LLM call via the collector.
// The collector receives the model name, usage statistics, and any error.
// Both successful and failed calls are recorded.
func WithMetrics(collector MetricsCollector) Middleware {
	return func(next kernel.LLM) kernel.LLM {
		return &metricsLLM{next: next, collector: collector}
	}
}

type metricsLLM struct {
	next      kernel.LLM
	collector MetricsCollector
}

func (m *metricsLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	resp, err := m.next.Generate(ctx, params)
	m.collector.RecordLLMCall(m.next.Model(), resp.Usage, err)
	return resp, err
}

func (m *metricsLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return m.next.GenerateStream(ctx, params)
}

func (m *metricsLLM) Model() string {
	return m.next.Model()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestWithMetrics`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add middleware/metrics.go middleware/metrics_test.go
git commit -m "feat(middleware): add WithMetrics middleware and MetricsCollector interface"
```

---

### Task 6: WithCostTracker middleware

**Files:**
- Create: `middleware/cost.go`
- Create: `middleware/cost_test.go`

CostTracker is a thread-safe accumulator for token usage and estimated cost. WithCostTracker returns middleware that updates the tracker after each successful Generate call. The tracker uses a `sync.Mutex` for thread safety.

- [ ] **Step 1: Write the test file**

```go
// middleware/cost_test.go
package middleware

import (
	"context"
	"sync"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestCostTrackerBasic(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{
			Text:  "hello",
			Usage: kernel.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		}, nil
	}

	tracker := NewCostTracker()
	wrapped := Wrap(f, WithCostTracker(tracker))

	_, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.TotalInputTokens != 100 {
		t.Errorf("expected input tokens 100, got %d", tracker.TotalInputTokens)
	}
	if tracker.TotalOutputTokens != 50 {
		t.Errorf("expected output tokens 50, got %d", tracker.TotalOutputTokens)
	}
}

func TestCostTrackerAccumulates(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{
			Text:  "hello",
			Usage: kernel.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		}, nil
	}

	tracker := NewCostTracker()
	wrapped := Wrap(f, WithCostTracker(tracker))

	for i := 0; i < 10; i++ {
		_, _ = wrapped.Generate(context.Background(), kernel.GenerateParams{})
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.TotalInputTokens != 100 {
		t.Errorf("expected input tokens 100, got %d", tracker.TotalInputTokens)
	}
	if tracker.TotalOutputTokens != 50 {
		t.Errorf("expected output tokens 50, got %d", tracker.TotalOutputTokens)
	}
}

func TestCostTrackerConcurrentSafety(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{
			Text:  "hello",
			Usage: kernel.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		}, nil
	}

	tracker := NewCostTracker()
	wrapped := Wrap(f, WithCostTracker(tracker))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = wrapped.Generate(context.Background(), kernel.GenerateParams{})
		}()
	}
	wg.Wait()

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.TotalInputTokens != 100 {
		t.Errorf("expected input tokens 100, got %d", tracker.TotalInputTokens)
	}
	if tracker.TotalOutputTokens != 100 {
		t.Errorf("expected output tokens 100, got %d", tracker.TotalOutputTokens)
	}
}

func TestCostTrackerIgnoresErrors(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	callCount := 0
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		callCount++
		if callCount%2 == 0 {
			return kernel.Response{}, errors.New("fail")
		}
		return kernel.Response{
			Text:  "hello",
			Usage: kernel.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		}, nil
	}

	tracker := NewCostTracker()
	wrapped := Wrap(f, WithCostTracker(tracker))

	for i := 0; i < 4; i++ {
		_, _ = wrapped.Generate(context.Background(), kernel.GenerateParams{})
	}

	// Calls 1, 3 succeed; calls 2, 4 fail. Only 2 calls accumulate tokens.
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.TotalInputTokens != 20 {
		t.Errorf("expected input tokens 20, got %d", tracker.TotalInputTokens)
	}
	if tracker.TotalOutputTokens != 10 {
		t.Errorf("expected output tokens 10, got %d", tracker.TotalOutputTokens)
	}
}

func TestCostTrackerSnapshot(t *testing.T) {
	tracker := NewCostTracker()
	tracker.mu.Lock()
	tracker.TotalInputTokens = 500
	tracker.TotalOutputTokens = 200
	tracker.EstimatedCost = 0.0035
	tracker.mu.Unlock()

	snap := tracker.Snapshot()
	if snap.TotalInputTokens != 500 {
		t.Errorf("expected input tokens 500, got %d", snap.TotalInputTokens)
	}
	if snap.TotalOutputTokens != 200 {
		t.Errorf("expected output tokens 200, got %d", snap.TotalOutputTokens)
	}
	if snap.EstimatedCost != 0.0035 {
		t.Errorf("expected cost 0.0035, got %f", snap.EstimatedCost)
	}
}

func TestCostTrackerModelPassthrough(t *testing.T) {
	f := newFakeLLM("claude-opus")
	tracker := NewCostTracker()
	wrapped := Wrap(f, WithCostTracker(tracker))
	if wrapped.Model() != "claude-opus" {
		t.Errorf("expected model %q, got %q", "claude-opus", wrapped.Model())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run "TestCostTracker"`
Expected: FAIL (cost.go does not exist yet)

- [ ] **Step 3: Write the implementation**

```go
// middleware/cost.go
package middleware

import (
	"context"
	"sync"

	"github.com/axonframework/axon/kernel"
)

// CostTracker accumulates token usage and estimated cost across LLM calls.
// It is thread-safe via an embedded sync.Mutex.
//
// EstimatedCost is not automatically calculated -- callers can set pricing
// externally or register a CostFunc. The tracker only accumulates raw token counts.
type CostTracker struct {
	mu                sync.Mutex
	TotalInputTokens  int
	TotalOutputTokens int
	EstimatedCost     float64

	// CostFunc optionally calculates cost from token counts.
	// If nil, EstimatedCost is not updated automatically.
	CostFunc func(inputTokens, outputTokens int) float64
}

// NewCostTracker creates a new, zero-valued CostTracker.
func NewCostTracker() *CostTracker {
	return &CostTracker{}
}

// Snapshot returns a point-in-time copy of the tracker's values.
func (ct *CostTracker) Snapshot() CostTrackerSnapshot {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return CostTrackerSnapshot{
		TotalInputTokens:  ct.TotalInputTokens,
		TotalOutputTokens: ct.TotalOutputTokens,
		EstimatedCost:     ct.EstimatedCost,
	}
}

// CostTrackerSnapshot is an immutable copy of a CostTracker's state.
type CostTrackerSnapshot struct {
	TotalInputTokens  int
	TotalOutputTokens int
	EstimatedCost     float64
}

// record adds the usage from a single call to the tracker.
func (ct *CostTracker) record(usage kernel.Usage) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.TotalInputTokens += usage.InputTokens
	ct.TotalOutputTokens += usage.OutputTokens
	if ct.CostFunc != nil {
		ct.EstimatedCost += ct.CostFunc(usage.InputTokens, usage.OutputTokens)
	}
}

// WithCostTracker returns middleware that accumulates token usage in the tracker.
// Only successful calls contribute to the tracker. Errors are ignored.
func WithCostTracker(tracker *CostTracker) Middleware {
	return func(next kernel.LLM) kernel.LLM {
		return &costLLM{next: next, tracker: tracker}
	}
}

type costLLM struct {
	next    kernel.LLM
	tracker *CostTracker
}

func (c *costLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	resp, err := c.next.Generate(ctx, params)
	if err == nil {
		c.tracker.record(resp.Usage)
	}
	return resp, err
}

func (c *costLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return c.next.GenerateStream(ctx, params)
}

func (c *costLLM) Model() string {
	return c.next.Model()
}
```

- [ ] **Step 4: Add missing import to cost_test.go**

The `TestCostTrackerIgnoresErrors` function uses `errors.New`. Ensure the import is present:

```go
// At the top of middleware/cost_test.go, the import block should include:
import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/axonframework/axon/kernel"
)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run "TestCostTracker"`
Expected: PASS (all 6 tests)

- [ ] **Step 6: Commit**

```bash
git add middleware/cost.go middleware/cost_test.go
git commit -m "feat(middleware): add WithCostTracker middleware and thread-safe CostTracker"
```

---

### Task 7: NewRouter, Route, and RouteContext

**Files:**
- Create: `middleware/router.go`
- Create: `middleware/router_test.go`

The router is a `kernel.LLM`. It evaluates each route's condition in order; the first match wins. If no condition matches, the fallback LLM is used. The router delegates both `Generate` and `GenerateStream`.

- [ ] **Step 1: Write the test file**

```go
// middleware/router_test.go
package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestRouterFallback(t *testing.T) {
	fallback := newFakeLLM("fallback-model")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "fallback"}, nil
	}

	// No routes -- always falls back.
	router := NewRouter(fallback)
	resp, err := router.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "fallback" {
		t.Errorf("expected %q, got %q", "fallback", resp.Text)
	}
}

func TestRouterFirstMatchWins(t *testing.T) {
	fallback := newFakeLLM("fallback")
	modelA := newFakeLLM("model-a")
	modelA.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "from A"}, nil
	}
	modelB := newFakeLLM("model-b")
	modelB.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "from B"}, nil
	}

	router := NewRouter(fallback,
		Route{
			Model: modelA,
			Condition: func(rc RouteContext) bool {
				return len(rc.Params.Messages) > 5
			},
		},
		Route{
			Model: modelB,
			Condition: func(rc RouteContext) bool {
				return len(rc.Params.Messages) > 0
			},
		},
	)

	// 3 messages: matches B (not A, which requires >5).
	params := kernel.GenerateParams{
		Messages: make([]kernel.Message, 3),
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "from B" {
		t.Errorf("expected %q, got %q", "from B", resp.Text)
	}

	// 10 messages: matches A first.
	params.Messages = make([]kernel.Message, 10)
	resp, err = router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "from A" {
		t.Errorf("expected %q, got %q", "from A", resp.Text)
	}
}

func TestRouterStream(t *testing.T) {
	fallback := newFakeLLM("fallback")
	modelA := newFakeLLM("model-a")
	modelA.streamFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
		return &fakeStream{resp: kernel.Response{Text: "stream from A"}}, nil
	}

	router := NewRouter(fallback,
		Route{
			Model: modelA,
			Condition: func(rc RouteContext) bool {
				return true // Always matches.
			},
		},
	)

	stream, err := router.GenerateStream(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stream.Response().Text != "stream from A" {
		t.Errorf("expected %q, got %q", "stream from A", stream.Response().Text)
	}
}

func TestRouterContextPassthrough(t *testing.T) {
	type ctxKey string
	key := ctxKey("test-key")

	fallback := newFakeLLM("fallback")
	modelA := newFakeLLM("model-a")

	var capturedCtx context.Context
	modelA.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		capturedCtx = ctx
		return kernel.Response{Text: "ok"}, nil
	}

	router := NewRouter(fallback,
		Route{
			Model: modelA,
			Condition: func(rc RouteContext) bool {
				// Verify context is available in the condition.
				return rc.Ctx.Value(key) == "hello"
			},
		},
	)

	ctx := context.WithValue(context.Background(), key, "hello")
	_, err := router.Generate(ctx, kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCtx.Value(key) != "hello" {
		t.Error("expected context value to be passed through to the matched model")
	}
}

func TestRouterPropagatesErrors(t *testing.T) {
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("fallback error")
	}

	router := NewRouter(fallback)
	_, err := router.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "fallback error" {
		t.Errorf("expected %q, got %q", "fallback error", err.Error())
	}
}

func TestRouterModel(t *testing.T) {
	fallback := newFakeLLM("fallback-model")
	router := NewRouter(fallback)

	// Router's Model() returns "router" since it routes to multiple models.
	if router.Model() != "router" {
		t.Errorf("expected model %q, got %q", "router", router.Model())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestRouter`
Expected: FAIL (router.go does not exist yet)

- [ ] **Step 3: Write the implementation**

```go
// middleware/router.go
package middleware

import (
	"context"

	"github.com/axonframework/axon/kernel"
)

// RouteContext provides context for routing decisions.
type RouteContext struct {
	Params kernel.GenerateParams
	Ctx    context.Context
}

// Route pairs a condition with a target LLM. When the condition returns true,
// the request is routed to the paired Model.
type Route struct {
	Model     kernel.LLM
	Condition func(RouteContext) bool
}

// NewRouter creates an LLM that routes requests to different models based on
// conditions. Routes are evaluated in order; the first matching condition wins.
// If no condition matches, the fallback LLM is used.
//
// The router itself implements kernel.LLM, so it can be used anywhere an LLM
// is expected, including as a target inside another router.
func NewRouter(fallback kernel.LLM, routes ...Route) kernel.LLM {
	return &routerLLM{fallback: fallback, routes: routes}
}

type routerLLM struct {
	fallback kernel.LLM
	routes   []Route
}

func (r *routerLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	target := r.resolve(ctx, params)
	return target.Generate(ctx, params)
}

func (r *routerLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	target := r.resolve(ctx, params)
	return target.GenerateStream(ctx, params)
}

// Model returns "router" since the router delegates to multiple models.
func (r *routerLLM) Model() string {
	return "router"
}

// resolve evaluates routes in order and returns the first matching model,
// or the fallback if no condition matches.
func (r *routerLLM) resolve(ctx context.Context, params kernel.GenerateParams) kernel.LLM {
	rc := RouteContext{Params: params, Ctx: ctx}
	for _, route := range r.routes {
		if route.Condition(rc) {
			return route.Model
		}
	}
	return r.fallback
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run TestRouter`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Commit**

```bash
git add middleware/router.go middleware/router_test.go
git commit -m "feat(middleware): add NewRouter with condition-based LLM routing"
```

---

### Task 8: Convenience strategies -- Cascade, RouteByTokenCount, RouteByToolCount, RoundRobin

**Files:**
- Create: `middleware/cascade.go`
- Create: `middleware/cascade_test.go`

These are higher-level routing functions built on top of the router and direct LLM wrapping.

- **RouteByTokenCount**: routes to `large` if estimated token count exceeds threshold, else `small`. Token count is estimated by summing message text lengths.
- **RouteByToolCount**: routes to `complex` if the number of tools exceeds threshold, else `simple`.
- **RoundRobin**: distributes calls across models using an atomic counter.
- **Cascade**: tries the primary model first, then checks `shouldEscalate(response)`; if true, calls the fallback model instead.

- [ ] **Step 1: Write the test file**

```go
// middleware/cascade_test.go
package middleware

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// --- RouteByTokenCount ---

func TestRouteByTokenCountSmall(t *testing.T) {
	small := newFakeLLM("small")
	small.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "small response"}, nil
	}
	large := newFakeLLM("large")
	large.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "large response"}, nil
	}

	router := RouteByTokenCount(100, small, large)

	// Short message: under threshold.
	params := kernel.GenerateParams{
		Messages: []kernel.Message{
			{Content: []kernel.ContentPart{{Text: strPtr("hi")}}},
		},
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "small response" {
		t.Errorf("expected %q, got %q", "small response", resp.Text)
	}
}

func TestRouteByTokenCountLarge(t *testing.T) {
	small := newFakeLLM("small")
	small.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "small response"}, nil
	}
	large := newFakeLLM("large")
	large.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "large response"}, nil
	}

	router := RouteByTokenCount(10, small, large)

	// Long message: over threshold.
	longText := "this is a long message that exceeds the token threshold easily"
	params := kernel.GenerateParams{
		Messages: []kernel.Message{
			{Content: []kernel.ContentPart{{Text: &longText}}},
		},
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "large response" {
		t.Errorf("expected %q, got %q", "large response", resp.Text)
	}
}

// --- RouteByToolCount ---

func TestRouteByToolCountSimple(t *testing.T) {
	simple := newFakeLLM("simple")
	simple.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "simple response"}, nil
	}
	complex := newFakeLLM("complex")
	complex.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "complex response"}, nil
	}

	router := RouteByToolCount(3, simple, complex)

	// 1 tool: under threshold.
	params := kernel.GenerateParams{
		Tools: make([]kernel.Tool, 1),
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "simple response" {
		t.Errorf("expected %q, got %q", "simple response", resp.Text)
	}
}

func TestRouteByToolCountComplex(t *testing.T) {
	simple := newFakeLLM("simple")
	simple.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "simple response"}, nil
	}
	complex := newFakeLLM("complex")
	complex.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "complex response"}, nil
	}

	router := RouteByToolCount(3, simple, complex)

	// 5 tools: over threshold.
	params := kernel.GenerateParams{
		Tools: make([]kernel.Tool, 5),
	}
	resp, err := router.Generate(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "complex response" {
		t.Errorf("expected %q, got %q", "complex response", resp.Text)
	}
}

// --- RoundRobin ---

func TestRoundRobinDistribution(t *testing.T) {
	models := make([]*fakeLLM, 3)
	llms := make([]kernel.LLM, 3)
	for i := range models {
		m := newFakeLLM("")
		idx := i // capture for closure
		m.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
			return kernel.Response{Text: string(rune('A' + idx))}, nil
		}
		models[i] = m
		llms[i] = m
	}

	rr := RoundRobin(llms...)

	// Calls should cycle through A, B, C, A, B, C...
	expected := []string{"A", "B", "C", "A", "B", "C"}
	for i, exp := range expected {
		resp, err := rr.Generate(context.Background(), kernel.GenerateParams{})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if resp.Text != exp {
			t.Errorf("call %d: expected %q, got %q", i, exp, resp.Text)
		}
	}
}

func TestRoundRobinConcurrentSafety(t *testing.T) {
	var callCount atomic.Int64
	models := make([]kernel.LLM, 3)
	for i := range models {
		m := newFakeLLM("")
		m.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
			callCount.Add(1)
			return kernel.Response{Text: "ok"}, nil
		}
		models[i] = m
	}

	rr := RoundRobin(models...)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = rr.Generate(context.Background(), kernel.GenerateParams{})
		}()
	}
	wg.Wait()

	if callCount.Load() != 100 {
		t.Errorf("expected 100 total calls, got %d", callCount.Load())
	}
}

func TestRoundRobinModel(t *testing.T) {
	m := newFakeLLM("test")
	rr := RoundRobin(m)
	if rr.Model() != "round-robin" {
		t.Errorf("expected model %q, got %q", "round-robin", rr.Model())
	}
}

// --- Cascade ---

func TestCascadeNoEscalation(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "primary response"}, nil
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "fallback response"}, nil
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool {
		return false // Never escalate.
	})

	resp, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "primary response" {
		t.Errorf("expected %q, got %q", "primary response", resp.Text)
	}
}

func TestCascadeWithEscalation(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "low quality"}, nil
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "high quality"}, nil
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool {
		return r.Text == "low quality" // Escalate on low quality.
	})

	resp, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "high quality" {
		t.Errorf("expected %q, got %q", "high quality", resp.Text)
	}
}

func TestCascadePrimaryError(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("primary failed")
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "fallback saved us"}, nil
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool {
		return false
	})

	// When primary errors, cascade should escalate to fallback.
	resp, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "fallback saved us" {
		t.Errorf("expected %q, got %q", "fallback saved us", resp.Text)
	}
}

func TestCascadeBothFail(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("primary failed")
	}
	fallback := newFakeLLM("fallback")
	fallback.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{}, errors.New("fallback also failed")
	}

	c := Cascade(primary, fallback, func(r kernel.Response) bool { return false })

	_, err := c.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "fallback also failed" {
		t.Errorf("expected %q, got %q", "fallback also failed", err.Error())
	}
}

func TestCascadeStream(t *testing.T) {
	primary := newFakeLLM("primary")
	primary.streamFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
		return &fakeStream{resp: kernel.Response{Text: "streamed from primary"}}, nil
	}
	fallback := newFakeLLM("fallback")

	c := Cascade(primary, fallback, func(r kernel.Response) bool { return false })

	stream, err := c.GenerateStream(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stream.Response().Text != "streamed from primary" {
		t.Errorf("expected %q, got %q", "streamed from primary", stream.Response().Text)
	}
}

func TestCascadeModel(t *testing.T) {
	primary := newFakeLLM("primary")
	fallback := newFakeLLM("fallback")
	c := Cascade(primary, fallback, func(r kernel.Response) bool { return false })
	if c.Model() != "cascade" {
		t.Errorf("expected model %q, got %q", "cascade", c.Model())
	}
}

// strPtr is a test helper.
func strPtr(s string) *string {
	return &s
}

// suppress unused import warnings
var _ = json.Marshal
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run "TestRouteBy|TestRoundRobin|TestCascade"`
Expected: FAIL (cascade.go does not exist yet)

- [ ] **Step 3: Write the implementation**

```go
// middleware/cascade.go
package middleware

import (
	"context"
	"sync/atomic"

	"github.com/axonframework/axon/kernel"
)

// RouteByTokenCount returns an LLM that routes to `large` when the estimated
// token count of the messages exceeds the threshold, and to `small` otherwise.
//
// Token count is estimated at ~4 characters per token (rough heuristic).
// This is built on top of NewRouter.
func RouteByTokenCount(threshold int, small kernel.LLM, large kernel.LLM) kernel.LLM {
	return NewRouter(small,
		Route{
			Model: large,
			Condition: func(rc RouteContext) bool {
				return estimateTokens(rc.Params.Messages) > threshold
			},
		},
	)
}

// estimateTokens estimates the token count from messages by summing text
// character lengths and dividing by 4 (rough approximation).
func estimateTokens(messages []kernel.Message) int {
	chars := 0
	for _, msg := range messages {
		for _, part := range msg.Content {
			if part.Text != nil {
				chars += len(*part.Text)
			}
		}
	}
	return chars / 4
}

// RouteByToolCount returns an LLM that routes to `complex` when the number
// of tools exceeds the threshold, and to `simple` otherwise.
//
// This is built on top of NewRouter.
func RouteByToolCount(threshold int, simple kernel.LLM, complex kernel.LLM) kernel.LLM {
	return NewRouter(simple,
		Route{
			Model: complex,
			Condition: func(rc RouteContext) bool {
				return len(rc.Params.Tools) > threshold
			},
		},
	)
}

// RoundRobin returns an LLM that distributes calls evenly across the given
// models using an atomic counter. Thread-safe for concurrent use.
//
// RoundRobin does not use the router; it uses a direct atomic index.
func RoundRobin(models ...kernel.LLM) kernel.LLM {
	if len(models) == 0 {
		panic("middleware.RoundRobin: at least one model is required")
	}
	return &roundRobinLLM{models: models}
}

type roundRobinLLM struct {
	models  []kernel.LLM
	counter atomic.Uint64
}

func (rr *roundRobinLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	target := rr.next()
	return target.Generate(ctx, params)
}

func (rr *roundRobinLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	target := rr.next()
	return target.GenerateStream(ctx, params)
}

func (rr *roundRobinLLM) Model() string {
	return "round-robin"
}

func (rr *roundRobinLLM) next() kernel.LLM {
	idx := rr.counter.Add(1) - 1
	return rr.models[idx%uint64(len(rr.models))]
}

// Cascade returns an LLM that tries the primary model first. If the primary
// call returns an error, or if shouldEscalate returns true for the response,
// the fallback model is called instead.
//
// For GenerateStream, only the primary model is used (no escalation for streams).
func Cascade(primary kernel.LLM, fallback kernel.LLM, shouldEscalate func(kernel.Response) bool) kernel.LLM {
	return &cascadeLLM{primary: primary, fallback: fallback, shouldEscalate: shouldEscalate}
}

type cascadeLLM struct {
	primary        kernel.LLM
	fallback       kernel.LLM
	shouldEscalate func(kernel.Response) bool
}

func (c *cascadeLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	resp, err := c.primary.Generate(ctx, params)
	if err != nil {
		// Primary failed; try fallback.
		return c.fallback.Generate(ctx, params)
	}
	if c.shouldEscalate(resp) {
		// Primary succeeded but quality check failed; try fallback.
		return c.fallback.Generate(ctx, params)
	}
	return resp, nil
}

func (c *cascadeLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return c.primary.GenerateStream(ctx, params)
}

func (c *cascadeLLM) Model() string {
	return "cascade"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -run "TestRouteBy|TestRoundRobin|TestCascade"`
Expected: PASS (all 13 tests)

- [ ] **Step 5: Commit**

```bash
git add middleware/cascade.go middleware/cascade_test.go
git commit -m "feat(middleware): add Cascade, RouteByTokenCount, RouteByToolCount, RoundRobin strategies"
```

---

### Task 9: Full integration test -- compose middleware and router

**Files:**
- Add to: `middleware/middleware_test.go`

This verifies that all pieces compose correctly: middleware wrapping, routing, and cascading work together as shown in spec section 6.5.

- [ ] **Step 1: Add integration tests to middleware_test.go**

Append the following tests to `middleware/middleware_test.go`:

```go
func TestCompositionRetryWithTimeout(t *testing.T) {
	f := newFakeLLM("test-model")
	attempts := 0
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		attempts++
		if attempts < 2 {
			return kernel.Response{}, errors.New("transient")
		}
		return kernel.Response{Text: "recovered"}, nil
	}

	wrapped := Wrap(f,
		WithRetry(3, 1*time.Millisecond),
		WithTimeout(5*time.Second),
	)

	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("expected %q, got %q", "recovered", resp.Text)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestCompositionFullStack(t *testing.T) {
	f := newFakeLLM("claude-sonnet")
	f.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{
			Text:  "hello world",
			Usage: kernel.Usage{InputTokens: 50, OutputTokens: 25, TotalTokens: 75},
		}, nil
	}

	handler := &captureHandler{}
	logger := slog.New(handler)
	tracker := NewCostTracker()
	collector := &fakeCollector{}

	wrapped := Wrap(f,
		WithRetry(3, 1*time.Millisecond),
		WithTimeout(5*time.Second),
		WithLogging(logger),
		WithMetrics(collector),
		WithCostTracker(tracker),
	)

	resp, err := wrapped.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", resp.Text)
	}

	// Verify logging occurred.
	if len(handler.records) != 1 {
		t.Errorf("expected 1 log record, got %d", len(handler.records))
	}

	// Verify metrics recorded.
	collector.mu.Lock()
	if len(collector.calls) != 1 {
		t.Errorf("expected 1 metrics call, got %d", len(collector.calls))
	}
	collector.mu.Unlock()

	// Verify cost tracked.
	snap := tracker.Snapshot()
	if snap.TotalInputTokens != 50 {
		t.Errorf("expected input tokens 50, got %d", snap.TotalInputTokens)
	}
}

func TestCompositionCascadeWithMiddleware(t *testing.T) {
	cheap := newFakeLLM("haiku")
	cheap.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "cheap answer"}, nil
	}
	expensive := newFakeLLM("opus")
	expensive.generateFn = func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
		return kernel.Response{Text: "expensive answer"}, nil
	}

	// Wrap individual models with middleware before cascading.
	wrappedCheap := Wrap(cheap, WithTimeout(1*time.Second))
	wrappedExpensive := Wrap(expensive, WithTimeout(10*time.Second))

	cascaded := Cascade(wrappedCheap, wrappedExpensive, func(r kernel.Response) bool {
		return r.Text == "cheap answer" // Always escalate.
	})

	resp, err := cascaded.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "expensive answer" {
		t.Errorf("expected %q, got %q", "expensive answer", resp.Text)
	}
}
```

- [ ] **Step 2: Add missing imports to middleware_test.go**

Ensure the import block in `middleware/middleware_test.go` includes:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/axonframework/axon/kernel"
)
```

- [ ] **Step 3: Run the full test suite**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v`
Expected: PASS (all tests across all files)

- [ ] **Step 4: Run tests with race detector**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -race`
Expected: PASS with no race conditions (CostTracker uses sync.Mutex, RoundRobin uses atomic counter, fakeLLM uses sync.Mutex)

- [ ] **Step 5: Commit**

```bash
git add middleware/middleware_test.go
git commit -m "test(middleware): add integration tests for full middleware composition"
```

---

### Task 10: Self-review -- spec coverage, placeholder scan, type consistency

This is not a code task. It is a review checklist to verify completeness before considering the middleware package done.

- [ ] **Step 1: Spec coverage audit**

Verify every item from spec sections 6.1--6.5 is implemented:

| Spec Item | File | Status |
|-----------|------|--------|
| `type Middleware func(kernel.LLM) kernel.LLM` | `middleware.go` | Covered |
| `func Wrap(llm kernel.LLM, mw ...Middleware) kernel.LLM` | `middleware.go` | Covered |
| `func WithLogging(logger *slog.Logger) Middleware` | `logger.go` | Covered |
| `func WithRetry(maxAttempts int, baseDelay time.Duration) Middleware` | `retry.go` | Covered |
| `func WithMetrics(collector MetricsCollector) Middleware` | `metrics.go` | Covered |
| `func WithTimeout(d time.Duration) Middleware` | `timeout.go` | Covered |
| `func WithCostTracker(tracker *CostTracker) Middleware` | `cost.go` | Covered |
| `type MetricsCollector interface` | `metrics.go` | Covered |
| `type CostTracker struct` | `cost.go` | Covered |
| `type Route struct` | `router.go` | Covered |
| `type RouteContext struct` | `router.go` | Covered |
| `func NewRouter(fallback kernel.LLM, routes ...Route) kernel.LLM` | `router.go` | Covered |
| `func RouteByTokenCount(threshold int, small, large kernel.LLM) kernel.LLM` | `cascade.go` | Covered |
| `func RouteByToolCount(threshold int, simple, complex kernel.LLM) kernel.LLM` | `cascade.go` | Covered |
| `func RoundRobin(models ...kernel.LLM) kernel.LLM` | `cascade.go` | Covered |
| `func Cascade(primary, fallback kernel.LLM, shouldEscalate func(kernel.Response) bool) kernel.LLM` | `cascade.go` | Covered |

- [ ] **Step 2: Placeholder scan**

Run: `cd /Users/derek/repo/axons/middleware && grep -rn "TODO\|FIXME\|HACK\|XXX\|placeholder\|not implemented" *.go`
Expected: No results. Every function is fully implemented.

- [ ] **Step 3: Type consistency check**

Run: `cd /Users/derek/repo/axons/middleware && go vet ./...`
Expected: Clean (no vet warnings). All types match kernel interface signatures.

- [ ] **Step 4: Verify all files exist**

Run:
```bash
ls -la /Users/derek/repo/axons/middleware/
```

Expected files:
```
go.mod
middleware.go
middleware_test.go
retry.go
retry_test.go
logger.go
logger_test.go
metrics.go
metrics_test.go
timeout.go
timeout_test.go
cost.go
cost_test.go
router.go
router_test.go
cascade.go
cascade_test.go
```

- [ ] **Step 5: Final full test run**

Run: `cd /Users/derek/repo/axons/middleware && go test ./... -v -race -count=1`
Expected: All tests pass, no race conditions, no failures.

---

## Summary of Public API

After all tasks are complete, the `middleware` package exports:

```go
// Core
type Middleware func(kernel.LLM) kernel.LLM
func Wrap(llm kernel.LLM, mw ...Middleware) kernel.LLM

// Built-in middleware
func WithTimeout(d time.Duration) Middleware
func WithRetry(maxAttempts int, baseDelay time.Duration) Middleware
func WithLogging(logger *slog.Logger) Middleware
func WithMetrics(collector MetricsCollector) Middleware
func WithCostTracker(tracker *CostTracker) Middleware

// Metrics
type MetricsCollector interface {
    RecordLLMCall(model string, usage kernel.Usage, err error)
}

// Cost tracking
type CostTracker struct { ... }
type CostTrackerSnapshot struct { ... }
func NewCostTracker() *CostTracker
func (*CostTracker) Snapshot() CostTrackerSnapshot

// Router
type Route struct { Model kernel.LLM; Condition func(RouteContext) bool }
type RouteContext struct { Params kernel.GenerateParams; Ctx context.Context }
func NewRouter(fallback kernel.LLM, routes ...Route) kernel.LLM

// Convenience strategies
func RouteByTokenCount(threshold int, small, large kernel.LLM) kernel.LLM
func RouteByToolCount(threshold int, simple, complex kernel.LLM) kernel.LLM
func RoundRobin(models ...kernel.LLM) kernel.LLM
func Cascade(primary, fallback kernel.LLM, shouldEscalate func(kernel.Response) bool) kernel.LLM
```
