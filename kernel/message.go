// kernel/message.go
package kernel

import (
	"encoding/json"
)

// Role represents a message participant type.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message in a conversation.
type Message struct {
	Role     Role           `json:"role"`
	Content  []ContentPart  `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TextContent returns the concatenated text from all text content parts.
func (m Message) TextContent() string {
	var s string
	for _, c := range m.Content {
		if c.Text != nil {
			s += *c.Text
		}
	}
	return s
}

// ContentPart is a tagged union — exactly one field should be set.
type ContentPart struct {
	Text       *string       `json:"text,omitempty"`
	Image      *ImageContent `json:"image,omitempty"`
	ToolCall   *ToolCall     `json:"tool_call,omitempty"`
	ToolResult *ToolResult   `json:"tool_result,omitempty"`
}

// ImageContent holds image data for multimodal messages.
type ImageContent struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type,omitempty"`
}

// ToolCall represents an LLM's request to invoke a tool.
type ToolCall struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ToolResult represents the output of a tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

func textPtr(s string) *string {
	return &s
}

// SystemMsg creates a system message with the given text.
func SystemMsg(text string) Message {
	return Message{
		Role:    RoleSystem,
		Content: []ContentPart{{Text: textPtr(text)}},
	}
}

// UserMsg creates a user message with the given text.
func UserMsg(text string) Message {
	return Message{
		Role:    RoleUser,
		Content: []ContentPart{{Text: textPtr(text)}},
	}
}

// AssistantMsg creates an assistant message with the given text.
func AssistantMsg(text string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: []ContentPart{{Text: textPtr(text)}},
	}
}

// ToolResultMsg creates a tool result message. The content value is JSON-serialized.
func ToolResultMsg(callID, name string, content any) Message {
	data, _ := json.Marshal(content)
	return Message{
		Role: RoleTool,
		Content: []ContentPart{{
			ToolResult: &ToolResult{
				ToolCallID: callID,
				Name:       name,
				Content:    string(data),
			},
		}},
	}
}
