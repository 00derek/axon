// testing/mock.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/axonframework/axon/kernel"
)

// MockLLM implements kernel.LLM with scripted responses per round.
// Use NewMockLLM() and OnRound() to configure expected behavior.
type MockLLM struct {
	mu        sync.Mutex
	responses map[int]mockResponse
	callCount int
}

type mockResponse struct {
	resp kernel.Response
	err  error
}

// NewMockLLM creates a new MockLLM with no configured responses.
func NewMockLLM() *MockLLM {
	return &MockLLM{
		responses: make(map[int]mockResponse),
	}
}

// Model returns "mock".
func (m *MockLLM) Model() string { return "mock" }

// Generate returns the scripted response for the current round.
// If no response is configured for the round, returns an empty text response with "stop".
func (m *MockLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	round := m.callCount
	m.callCount++

	if mr, ok := m.responses[round]; ok {
		if mr.err != nil {
			return kernel.Response{}, mr.err
		}
		return mr.resp, nil
	}

	// Unconfigured round: return empty stop response
	return kernel.Response{FinishReason: "stop"}, nil
}

// GenerateStream is not supported by MockLLM. Use Generate instead.
func (m *MockLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, fmt.Errorf("MockLLM does not support streaming; use Generate")
}

// OnRound begins configuring the response for a specific round number (0-indexed).
func (m *MockLLM) OnRound(n int) *MockResponseBuilder {
	return &MockResponseBuilder{mock: m, round: n}
}

// MockResponseBuilder configures a single round's response. All terminal methods
// return the parent *MockLLM for fluent chaining.
type MockResponseBuilder struct {
	mock  *MockLLM
	round int
}

// RespondWithText configures this round to return a text response.
func (b *MockResponseBuilder) RespondWithText(text string) *MockLLM {
	b.mock.responses[b.round] = mockResponse{
		resp: kernel.Response{
			Text:         text,
			FinishReason: "stop",
		},
	}
	return b.mock
}

// RespondWithToolCall configures this round to return a single tool call.
// Params are JSON-marshaled from the provided map.
func (b *MockResponseBuilder) RespondWithToolCall(name string, params map[string]any) *MockLLM {
	data, _ := json.Marshal(params)
	b.mock.responses[b.round] = mockResponse{
		resp: kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     fmt.Sprintf("mock-call-%d-0", b.round),
				Name:   name,
				Params: data,
			}},
			FinishReason: "tool_calls",
		},
	}
	return b.mock
}

// RespondWithToolCalls configures this round to return multiple tool calls.
func (b *MockResponseBuilder) RespondWithToolCalls(calls ...kernel.ToolCall) *MockLLM {
	b.mock.responses[b.round] = mockResponse{
		resp: kernel.Response{
			ToolCalls:    calls,
			FinishReason: "tool_calls",
		},
	}
	return b.mock
}

// RespondWithError configures this round to return an error.
func (b *MockResponseBuilder) RespondWithError(err error) *MockLLM {
	b.mock.responses[b.round] = mockResponse{err: err}
	return b.mock
}
