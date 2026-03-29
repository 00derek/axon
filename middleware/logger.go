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
