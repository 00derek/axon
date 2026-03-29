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

// suppress unused import warning for time
var _ = time.Second
