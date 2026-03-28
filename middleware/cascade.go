// middleware/cascade.go
package middleware

import (
	"context"
	"sync/atomic"

	"github.com/axonframework/axon/kernel"
)

// RouteByTokenCount returns an LLM that routes to `large` when the estimated
// token count of the messages exceeds the threshold, and to `small` otherwise.
//
// Token count is estimated at ~4 characters per token (rough heuristic).
// This is built on top of NewRouter.
func RouteByTokenCount(threshold int, small kernel.LLM, large kernel.LLM) kernel.LLM {
	return NewRouter(small,
		Route{
			Model: large,
			Condition: func(rc RouteContext) bool {
				return estimateTokens(rc.Params.Messages) > threshold
			},
		},
	)
}

// estimateTokens estimates the token count from messages by summing text
// character lengths and dividing by 4 (rough approximation).
func estimateTokens(messages []kernel.Message) int {
	chars := 0
	for _, msg := range messages {
		for _, part := range msg.Content {
			if part.Text != nil {
				chars += len(*part.Text)
			}
		}
	}
	return chars / 4
}

// RouteByToolCount returns an LLM that routes to `complex` when the number
// of tools exceeds the threshold, and to `simple` otherwise.
//
// This is built on top of NewRouter.
func RouteByToolCount(threshold int, simple kernel.LLM, complex kernel.LLM) kernel.LLM {
	return NewRouter(simple,
		Route{
			Model: complex,
			Condition: func(rc RouteContext) bool {
				return len(rc.Params.Tools) > threshold
			},
		},
	)
}

// RoundRobin returns an LLM that distributes calls evenly across the given
// models using an atomic counter. Thread-safe for concurrent use.
//
// RoundRobin does not use the router; it uses a direct atomic index.
func RoundRobin(models ...kernel.LLM) kernel.LLM {
	if len(models) == 0 {
		panic("middleware.RoundRobin: at least one model is required")
	}
	return &roundRobinLLM{models: models}
}

type roundRobinLLM struct {
	models  []kernel.LLM
	counter atomic.Uint64
}

func (rr *roundRobinLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	target := rr.next()
	return target.Generate(ctx, params)
}

func (rr *roundRobinLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	target := rr.next()
	return target.GenerateStream(ctx, params)
}

func (rr *roundRobinLLM) Model() string {
	return "round-robin"
}

func (rr *roundRobinLLM) next() kernel.LLM {
	idx := rr.counter.Add(1) - 1
	return rr.models[idx%uint64(len(rr.models))]
}

// Cascade returns an LLM that tries the primary model first. If the primary
// call returns an error, or if shouldEscalate returns true for the response,
// the fallback model is called instead.
//
// For GenerateStream, only the primary model is used (no escalation for streams).
func Cascade(primary kernel.LLM, fallback kernel.LLM, shouldEscalate func(kernel.Response) bool) kernel.LLM {
	return &cascadeLLM{primary: primary, fallback: fallback, shouldEscalate: shouldEscalate}
}

type cascadeLLM struct {
	primary        kernel.LLM
	fallback       kernel.LLM
	shouldEscalate func(kernel.Response) bool
}

func (c *cascadeLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	resp, err := c.primary.Generate(ctx, params)
	if err != nil {
		// Primary failed; try fallback.
		return c.fallback.Generate(ctx, params)
	}
	if c.shouldEscalate(resp) {
		// Primary succeeded but quality check failed; try fallback.
		return c.fallback.Generate(ctx, params)
	}
	return resp, nil
}

func (c *cascadeLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return c.primary.GenerateStream(ctx, params)
}

func (c *cascadeLLM) Model() string {
	return "cascade"
}
