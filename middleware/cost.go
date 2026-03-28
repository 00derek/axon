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
