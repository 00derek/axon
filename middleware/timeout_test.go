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
