// kernel/context_test.go
package kernel

import (
	"context"
	"encoding/json"
	"testing"
)

func makeDummyTool(name string) Tool {
	type empty struct{}
	return NewTool(name, "A "+name+" tool",
		func(ctx context.Context, p empty) (string, error) {
			return "ok", nil
		},
	)
}

func TestAgentContextAddMessages(t *testing.T) {
	ac := NewAgentContext(nil)
	ac.AddMessages(UserMsg("hello"), AssistantMsg("hi"))

	if len(ac.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ac.Messages))
	}
	if ac.Messages[0].Role != RoleUser {
		t.Errorf("expected first message role %q, got %q", RoleUser, ac.Messages[0].Role)
	}
}

func TestAgentContextSystemPrompt(t *testing.T) {
	ac := NewAgentContext(nil)

	// No system prompt initially
	if ac.SystemPrompt() != "" {
		t.Errorf("expected empty system prompt, got %q", ac.SystemPrompt())
	}

	// Set system prompt
	ac.SetSystemPrompt("You are helpful")
	if ac.SystemPrompt() != "You are helpful" {
		t.Errorf("expected %q, got %q", "You are helpful", ac.SystemPrompt())
	}

	// Overwrite system prompt
	ac.SetSystemPrompt("You are concise")
	if ac.SystemPrompt() != "You are concise" {
		t.Errorf("expected %q, got %q", "You are concise", ac.SystemPrompt())
	}

	// System prompt should be the first message
	if ac.Messages[0].Role != RoleSystem {
		t.Errorf("expected first message to be system, got %q", ac.Messages[0].Role)
	}

	// Should only have one system message
	systemCount := 0
	for _, m := range ac.Messages {
		if m.Role == RoleSystem {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Errorf("expected 1 system message, got %d", systemCount)
	}
}

func TestAgentContextLastUserMessage(t *testing.T) {
	ac := NewAgentContext(nil)
	ac.AddMessages(SystemMsg("system"), UserMsg("first"), AssistantMsg("response"), UserMsg("second"))

	last := ac.LastUserMessage()
	if last == nil {
		t.Fatal("expected non-nil last user message")
	}
	if last.TextContent() != "second" {
		t.Errorf("expected %q, got %q", "second", last.TextContent())
	}
}

func TestAgentContextLastUserMessageNone(t *testing.T) {
	ac := NewAgentContext(nil)
	ac.AddMessages(SystemMsg("system"))

	if ac.LastUserMessage() != nil {
		t.Error("expected nil when no user messages")
	}
}

func TestAgentContextToolManagement(t *testing.T) {
	tools := []Tool{
		makeDummyTool("search"),
		makeDummyTool("reserve"),
		makeDummyTool("music"),
	}
	ac := NewAgentContext(tools)

	// All tools active initially
	active := ac.ActiveTools()
	if len(active) != 3 {
		t.Fatalf("expected 3 active tools, got %d", len(active))
	}

	// Disable one
	ac.DisableTools("music")
	active = ac.ActiveTools()
	if len(active) != 2 {
		t.Fatalf("expected 2 active tools, got %d", len(active))
	}
	for _, tool := range active {
		if tool.Name() == "music" {
			t.Error("music should be disabled")
		}
	}

	// AllTools still returns all
	if len(ac.AllTools()) != 3 {
		t.Errorf("expected 3 total tools, got %d", len(ac.AllTools()))
	}
}

func TestAgentContextEnableTools(t *testing.T) {
	tools := []Tool{
		makeDummyTool("search"),
		makeDummyTool("reserve"),
		makeDummyTool("music"),
	}
	ac := NewAgentContext(tools)

	// Enable only search and reserve
	ac.EnableTools("search", "reserve")
	active := ac.ActiveTools()
	if len(active) != 2 {
		t.Fatalf("expected 2 active tools, got %d", len(active))
	}

	names := map[string]bool{}
	for _, tool := range active {
		names[tool.Name()] = true
	}
	if !names["search"] || !names["reserve"] {
		t.Errorf("expected search and reserve, got %v", names)
	}
}

func TestAgentContextAddTools(t *testing.T) {
	ac := NewAgentContext([]Tool{makeDummyTool("search")})
	ac.AddTools(makeDummyTool("weather"))

	if len(ac.AllTools()) != 2 {
		t.Errorf("expected 2 tools, got %d", len(ac.AllTools()))
	}
	if len(ac.ActiveTools()) != 2 {
		t.Errorf("expected 2 active tools, got %d", len(ac.ActiveTools()))
	}
}

func TestAgentContextDisableAllTools(t *testing.T) {
	tools := []Tool{makeDummyTool("a"), makeDummyTool("b")}
	ac := NewAgentContext(tools)
	ac.DisableTools("a", "b")

	if len(ac.ActiveTools()) != 0 {
		t.Errorf("expected 0 active tools, got %d", len(ac.ActiveTools()))
	}
}

func TestAgentContextGetTool(t *testing.T) {
	tools := []Tool{makeDummyTool("search"), makeDummyTool("reserve")}
	ac := NewAgentContext(tools)

	tool, ok := ac.GetTool("search")
	if !ok {
		t.Fatal("expected to find tool 'search'")
	}
	if tool.Name() != "search" {
		t.Errorf("expected tool name %q, got %q", "search", tool.Name())
	}

	_, ok = ac.GetTool("nonexistent")
	if ok {
		t.Error("expected not to find tool 'nonexistent'")
	}
}

// Ensure unused import doesn't break
var _ = json.Marshal
