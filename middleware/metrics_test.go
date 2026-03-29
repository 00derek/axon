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
