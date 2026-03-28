// middleware/cost_test.go
package middleware

import (
	"context"
	"errors"
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
