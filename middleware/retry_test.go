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
