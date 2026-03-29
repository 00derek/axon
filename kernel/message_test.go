// kernel/message_test.go
package kernel

import (
	"testing"
)

func TestSystemMsg(t *testing.T) {
	msg := SystemMsg("You are helpful")
	if msg.Role != RoleSystem {
		t.Errorf("expected role %q, got %q", RoleSystem, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(msg.Content))
	}
	if msg.Content[0].Text == nil || *msg.Content[0].Text != "You are helpful" {
		t.Errorf("expected text %q, got %v", "You are helpful", msg.Content[0].Text)
	}
}

func TestUserMsg(t *testing.T) {
	msg := UserMsg("Hello")
	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text == nil || *msg.Content[0].Text != "Hello" {
		t.Errorf("unexpected content: %+v", msg.Content)
	}
}

func TestAssistantMsg(t *testing.T) {
	msg := AssistantMsg("Hi there")
	if msg.Role != RoleAssistant {
		t.Errorf("expected role %q, got %q", RoleAssistant, msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text == nil || *msg.Content[0].Text != "Hi there" {
		t.Errorf("unexpected content: %+v", msg.Content)
	}
}

func TestToolResultMsg(t *testing.T) {
	type result struct {
		Count int `json:"count"`
	}
	msg := ToolResultMsg("call-123", "search", result{Count: 3})
	if msg.Role != RoleTool {
		t.Errorf("expected role %q, got %q", RoleTool, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(msg.Content))
	}
	tr := msg.Content[0].ToolResult
	if tr == nil {
		t.Fatal("expected ToolResult content part")
	}
	if tr.ToolCallID != "call-123" {
		t.Errorf("expected ToolCallID %q, got %q", "call-123", tr.ToolCallID)
	}
	if tr.Name != "search" {
		t.Errorf("expected Name %q, got %q", "search", tr.Name)
	}
	if tr.Content != `{"count":3}` {
		t.Errorf("expected Content %q, got %q", `{"count":3}`, tr.Content)
	}
	if tr.IsError {
		t.Error("expected IsError false")
	}
}

func TestToolResultMsgError(t *testing.T) {
	msg := ToolResultMsg("call-456", "search", "something went wrong")
	tr := msg.Content[0].ToolResult
	if tr.Content != `"something went wrong"` {
		t.Errorf("expected Content %q, got %q", `"something went wrong"`, tr.Content)
	}
}

func TestMessageTextHelper(t *testing.T) {
	msg := SystemMsg("hello")
	if msg.TextContent() != "hello" {
		t.Errorf("expected %q, got %q", "hello", msg.TextContent())
	}

	// Message with no text
	msg2 := Message{Role: RoleAssistant, Content: []ContentPart{{ToolCall: &ToolCall{ID: "1", Name: "test"}}}}
	if msg2.TextContent() != "" {
		t.Errorf("expected empty string, got %q", msg2.TextContent())
	}
}
