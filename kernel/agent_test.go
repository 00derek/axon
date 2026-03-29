// kernel/agent_test.go
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// fakeLLM implements LLM for testing. Returns scripted responses per round.
type fakeLLM struct {
	responses []Response
	callCount int
	mu        sync.Mutex
}

func newFakeLLM(responses ...Response) *fakeLLM {
	return &fakeLLM{responses: responses}
}

func (f *fakeLLM) Generate(ctx context.Context, params GenerateParams) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.callCount >= len(f.responses) {
		return Response{Text: "no more responses", FinishReason: "stop"}, nil
	}
	resp := f.responses[f.callCount]
	f.callCount++
	return resp, nil
}

func (f *fakeLLM) GenerateStream(ctx context.Context, params GenerateParams) (Stream, error) {
	return nil, fmt.Errorf("streaming not implemented in fakeLLM")
}

func (f *fakeLLM) Model() string { return "fake" }

func TestAgentRunTextOnly(t *testing.T) {
	llm := newFakeLLM(Response{
		Text:         "Hello, how can I help?",
		FinishReason: "stop",
		Usage:        Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})

	agent := NewAgent(
		WithModel(llm),
		WithSystemPrompt("You are helpful"),
	)

	result, err := agent.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello, how can I help?" {
		t.Errorf("expected text %q, got %q", "Hello, how can I help?", result.Text)
	}
	if len(result.Rounds) != 1 {
		t.Errorf("expected 1 round, got %d", len(result.Rounds))
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestAgentRunWithToolCall(t *testing.T) {
	searchTool := NewTool("search", "Search things",
		func(ctx context.Context, p searchParams) ([]searchResult, error) {
			return []searchResult{{Name: "Thai Basil", ID: "r1"}}, nil
		},
	)

	llm := newFakeLLM(
		// Round 0: tool call
		Response{
			ToolCalls: []ToolCall{{
				ID:     "call-1",
				Name:   "search",
				Params: json.RawMessage(`{"query":"thai","location":"SF"}`),
			}},
			FinishReason: "tool_calls",
			Usage:        Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
		},
		// Round 1: text response
		Response{
			Text:         "I found Thai Basil for you!",
			FinishReason: "stop",
			Usage:        Usage{InputTokens: 30, OutputTokens: 8, TotalTokens: 38},
		},
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		WithSystemPrompt("You are a restaurant assistant"),
	)

	result, err := agent.Run(context.Background(), "Find thai food")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "I found Thai Basil for you!" {
		t.Errorf("expected final text, got %q", result.Text)
	}
	if len(result.Rounds) != 2 {
		t.Errorf("expected 2 rounds, got %d", len(result.Rounds))
	}
	if result.Usage.TotalTokens != 68 {
		t.Errorf("expected 68 total tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestAgentRunMaxRounds(t *testing.T) {
	// LLM always returns tool calls — agent should stop at max rounds
	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
		Response{ToolCalls: []ToolCall{{ID: "c2", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
		Response{ToolCalls: []ToolCall{{ID: "c3", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
	)

	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) {
			return "result", nil
		},
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		WithMaxRounds(3),
	)

	result, err := agent.Run(context.Background(), "search forever")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rounds) != 3 {
		t.Errorf("expected 3 rounds (max), got %d", len(result.Rounds))
	}
}

func TestAgentHooksOnStartOnFinish(t *testing.T) {
	var startCalled, finishCalled bool

	llm := newFakeLLM(Response{Text: "hi", FinishReason: "stop"})

	agent := NewAgent(
		WithModel(llm),
		OnStart(func(ctx *TurnContext) {
			startCalled = true
			if ctx.Input != "hello" {
				t.Errorf("OnStart: expected input %q, got %q", "hello", ctx.Input)
			}
		}),
		OnFinish(func(ctx *TurnContext) {
			finishCalled = true
			if ctx.Result == nil {
				t.Error("OnFinish: expected non-nil result")
			}
		}),
	)

	agent.Run(context.Background(), "hello")

	if !startCalled {
		t.Error("OnStart was not called")
	}
	if !finishCalled {
		t.Error("OnFinish was not called")
	}
}

func TestAgentHookPrepareRound(t *testing.T) {
	var roundNumbers []int

	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
		Response{Text: "done", FinishReason: "stop"},
	)

	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) { return "ok", nil },
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		PrepareRound(func(ctx *RoundContext) {
			roundNumbers = append(roundNumbers, ctx.RoundNumber)
		}),
	)

	agent.Run(context.Background(), "test")

	if len(roundNumbers) != 2 {
		t.Fatalf("expected PrepareRound called 2 times, got %d", len(roundNumbers))
	}
	if roundNumbers[0] != 0 || roundNumbers[1] != 1 {
		t.Errorf("expected round numbers [0, 1], got %v", roundNumbers)
	}
}

func TestAgentHookToolStartEnd(t *testing.T) {
	var toolStartName, toolEndName string
	var toolEndResult any

	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"test","location":"here"}`)}}, FinishReason: "tool_calls"},
		Response{Text: "done", FinishReason: "stop"},
	)

	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) { return "found it", nil },
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		OnToolStart(func(ctx *ToolContext) {
			toolStartName = ctx.ToolName
		}),
		OnToolEnd(func(ctx *ToolContext) {
			toolEndName = ctx.ToolName
			toolEndResult = ctx.Result
		}),
	)

	agent.Run(context.Background(), "test")

	if toolStartName != "search" {
		t.Errorf("OnToolStart: expected tool %q, got %q", "search", toolStartName)
	}
	if toolEndName != "search" {
		t.Errorf("OnToolEnd: expected tool %q, got %q", "search", toolEndName)
	}
	if toolEndResult != "found it" {
		t.Errorf("OnToolEnd: expected result %q, got %v", "found it", toolEndResult)
	}
}

func TestAgentStopWhen(t *testing.T) {
	llm := newFakeLLM(
		Response{Text: "round 0", FinishReason: "stop", Usage: Usage{TotalTokens: 5000}},
		Response{Text: "round 1", FinishReason: "stop", Usage: Usage{TotalTokens: 6000}},
	)

	agent := NewAgent(
		WithModel(llm),
		StopWhen(func(ctx *RoundContext) bool {
			// Stop after first round regardless
			return ctx.RoundNumber > 0
		}),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should only have 1 round because StopWhen fires before round 1
	if len(result.Rounds) != 1 {
		t.Errorf("expected 1 round, got %d", len(result.Rounds))
	}
}

func TestAgentToolNotFound(t *testing.T) {
	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "nonexistent", Params: json.RawMessage(`{}`)}}, FinishReason: "tool_calls"},
	)

	agent := NewAgent(WithModel(llm))

	_, err := agent.Run(context.Background(), "test")
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestAgentPrepareRoundModifiesTools(t *testing.T) {
	llm := newFakeLLM(Response{Text: "done", FinishReason: "stop"})

	searchTool := makeDummyTool("search")
	musicTool := makeDummyTool("music")

	var activeToolCount int

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool, musicTool),
		PrepareRound(func(ctx *RoundContext) {
			ctx.AgentCtx.EnableTools("search")
			activeToolCount = len(ctx.AgentCtx.ActiveTools())
		}),
	)

	agent.Run(context.Background(), "test")

	if activeToolCount != 1 {
		t.Errorf("expected 1 active tool after PrepareRound, got %d", activeToolCount)
	}
}

func TestAgentMultipleHooksSameType(t *testing.T) {
	var order []string

	llm := newFakeLLM(Response{Text: "ok", FinishReason: "stop"})

	agent := NewAgent(
		WithModel(llm),
		OnStart(func(ctx *TurnContext) { order = append(order, "first") }),
		OnStart(func(ctx *TurnContext) { order = append(order, "second") }),
	)

	agent.Run(context.Background(), "test")

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("expected hooks in declaration order, got %v", order)
	}
}

func TestAgentParallelToolExecution(t *testing.T) {
	var mu sync.Mutex
	var executionOrder []string

	tool1 := NewTool("tool_a", "Tool A",
		func(ctx context.Context, p struct{}) (string, error) {
			mu.Lock()
			executionOrder = append(executionOrder, "a")
			mu.Unlock()
			return "result_a", nil
		},
	)
	tool2 := NewTool("tool_b", "Tool B",
		func(ctx context.Context, p struct{}) (string, error) {
			mu.Lock()
			executionOrder = append(executionOrder, "b")
			mu.Unlock()
			return "result_b", nil
		},
	)

	llm := newFakeLLM(
		Response{
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "tool_a", Params: json.RawMessage(`{}`)},
				{ID: "c2", Name: "tool_b", Params: json.RawMessage(`{}`)},
			},
			FinishReason: "tool_calls",
		},
		Response{Text: "both done", FinishReason: "stop"},
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(tool1, tool2),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(executionOrder) != 2 {
		t.Errorf("expected both tools executed, got %v", executionOrder)
	}
	if result.Text != "both done" {
		t.Errorf("expected %q, got %q", "both done", result.Text)
	}
}
