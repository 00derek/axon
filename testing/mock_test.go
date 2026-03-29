// testing/mock_test.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestNewMockLLMModel(t *testing.T) {
	m := NewMockLLM()
	if m.Model() != "mock" {
		t.Errorf("expected model %q, got %q", "mock", m.Model())
	}
}

func TestMockLLMTextResponse(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithText("Hello from round 0").
		OnRound(1).RespondWithText("Hello from round 1")

	// Round 0
	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from round 0" {
		t.Errorf("round 0: expected %q, got %q", "Hello from round 0", resp.Text)
	}

	// Round 1
	resp, err = m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from round 1" {
		t.Errorf("round 1: expected %q, got %q", "Hello from round 1", resp.Text)
	}
}

func TestMockLLMToolCallResponse(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "thai"})

	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool name %q, got %q", "search", resp.ToolCalls[0].Name)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason %q, got %q", "tool_calls", resp.FinishReason)
	}

	var params map[string]any
	if err := json.Unmarshal(resp.ToolCalls[0].Params, &params); err != nil {
		t.Fatalf("failed to parse params: %v", err)
	}
	if params["query"] != "thai" {
		t.Errorf("expected query %q, got %v", "thai", params["query"])
	}
}

func TestMockLLMMultipleToolCalls(t *testing.T) {
	calls := []kernel.ToolCall{
		{ID: "c1", Name: "search", Params: json.RawMessage(`{"q":"a"}`)},
		{ID: "c2", Name: "reserve", Params: json.RawMessage(`{"id":"r1"}`)},
	}
	m := NewMockLLM().
		OnRound(0).RespondWithToolCalls(calls...)

	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" || resp.ToolCalls[1].Name != "reserve" {
		t.Errorf("unexpected tool names: %v, %v", resp.ToolCalls[0].Name, resp.ToolCalls[1].Name)
	}
}

func TestMockLLMErrorResponse(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithError(fmt.Errorf("rate limit exceeded"))

	_, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "rate limit exceeded" {
		t.Errorf("expected error %q, got %q", "rate limit exceeded", err.Error())
	}
}

func TestMockLLMUnconfiguredRound(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithText("only round 0")

	// Round 0 works
	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "only round 0" {
		t.Errorf("expected %q, got %q", "only round 0", resp.Text)
	}

	// Round 1 has no configured response -- should return empty text response
	resp, err = m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "" {
		t.Errorf("expected empty text for unconfigured round, got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason %q, got %q", "stop", resp.FinishReason)
	}
}

func TestMockLLMChainingReturnsParent(t *testing.T) {
	// Verify the builder chain returns *MockLLM so you can keep calling OnRound
	m := NewMockLLM()
	result := m.OnRound(0).RespondWithText("a")
	if result != m {
		t.Error("RespondWithText should return the parent MockLLM for chaining")
	}

	result = m.OnRound(1).RespondWithToolCall("search", map[string]any{})
	if result != m {
		t.Error("RespondWithToolCall should return the parent MockLLM for chaining")
	}

	result = m.OnRound(2).RespondWithError(fmt.Errorf("err"))
	if result != m {
		t.Error("RespondWithError should return the parent MockLLM for chaining")
	}
}
