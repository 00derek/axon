// kernel/agent_stream_test.go
package kernel

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAgentStreamTextOnly(t *testing.T) {
	llm := newFakeLLM(Response{
		Text:         "Hello there!",
		FinishReason: "stop",
		Usage:        Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})

	agent := NewAgent(WithModel(llm), WithSystemPrompt("Be helpful"))

	sr, err := agent.Stream(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect all text
	var text string
	for chunk := range sr.Text() {
		text += chunk
	}

	if text != "Hello there!" {
		t.Errorf("expected %q, got %q", "Hello there!", text)
	}

	result := sr.Result()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestAgentStreamWithToolCalls(t *testing.T) {
	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) {
			return "found it", nil
		},
	)

	llm := newFakeLLM(
		Response{
			ToolCalls:    []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"test","location":"here"}`)}},
			FinishReason: "tool_calls",
		},
		Response{
			Text:         "Here are your results",
			FinishReason: "stop",
		},
	)

	agent := NewAgent(WithModel(llm), WithTools(searchTool))

	sr, err := agent.Stream(context.Background(), "search for something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect text (should only be final response)
	var text string
	for chunk := range sr.Text() {
		text += chunk
	}

	if text != "Here are your results" {
		t.Errorf("expected %q, got %q", "Here are your results", text)
	}
}

func TestAgentStreamEvents(t *testing.T) {
	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) {
			return "found", nil
		},
	)

	llm := newFakeLLM(
		Response{
			ToolCalls:    []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"test","location":"here"}`)}},
			FinishReason: "tool_calls",
		},
		Response{
			Text:         "Done!",
			FinishReason: "stop",
		},
	)

	agent := NewAgent(WithModel(llm), WithTools(searchTool))

	sr, err := agent.Stream(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for event := range sr.Events() {
		events = append(events, event)
	}

	// Should have: ToolStartEvent, ToolEndEvent, TextDeltaEvent
	hasToolStart := false
	hasToolEnd := false
	hasTextDelta := false
	for _, e := range events {
		switch e.(type) {
		case ToolStartEvent:
			hasToolStart = true
		case ToolEndEvent:
			hasToolEnd = true
		case TextDeltaEvent:
			hasTextDelta = true
		}
	}

	if !hasToolStart {
		t.Error("expected ToolStartEvent")
	}
	if !hasToolEnd {
		t.Error("expected ToolEndEvent")
	}
	if !hasTextDelta {
		t.Error("expected TextDeltaEvent")
	}
}
