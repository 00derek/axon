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
