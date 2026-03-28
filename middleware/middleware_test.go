// middleware/middleware_test.go
package middleware

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

// fakeLLM implements kernel.LLM for testing.
type fakeLLM struct {
	model      string
	generateFn func(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error)
	streamFn   func(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error)
	callCount  int
	lastParams kernel.GenerateParams
	mu         sync.Mutex
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

// --- Integration tests ---

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
