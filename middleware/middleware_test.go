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
