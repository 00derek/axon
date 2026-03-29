// kernel/llm.go
package kernel

import (
	"context"
	"encoding/json"
	"time"
)

// LLM is the interface that all LLM providers implement.
type LLM interface {
	Generate(ctx context.Context, params GenerateParams) (Response, error)
	GenerateStream(ctx context.Context, params GenerateParams) (Stream, error)
	Model() string
}

// GenerateParams holds everything needed for an LLM call.
type GenerateParams struct {
	Messages []Message
	Tools    []Tool
	Options  GenerateOptions
}

// GenerateOptions holds optional generation parameters.
type GenerateOptions struct {
	Temperature    *float32   `json:"temperature,omitempty"`
	MaxTokens      *int       `json:"max_tokens,omitempty"`
	StopSequences  []string   `json:"stop_sequences,omitempty"`
	ToolChoice     ToolChoice `json:"tool_choice,omitempty"`
	OutputSchema   *Schema    `json:"output_schema,omitempty"`
	ReasoningLevel *string    `json:"reasoning_level,omitempty"`
}

// ToolChoice controls how the LLM uses tools.
type ToolChoice struct {
	Type     string `json:"type"`                // "auto", "required", "none", "tool"
	ToolName string `json:"tool_name,omitempty"` // only when Type == "tool"
}

var (
	ToolChoiceAuto     = ToolChoice{Type: "auto"}
	ToolChoiceRequired = ToolChoice{Type: "required"}
	ToolChoiceNone     = ToolChoice{Type: "none"}
)

// ToolChoiceForce creates a ToolChoice that forces a specific tool.
func ToolChoiceForce(name string) ToolChoice {
	return ToolChoice{Type: "tool", ToolName: name}
}

// Response is the complete result of a non-streaming LLM call.
type Response struct {
	Text         string     `json:"text"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        Usage      `json:"usage"`
	FinishReason string     `json:"finish_reason"`
}

// Usage tracks token consumption and timing for an LLM call.
type Usage struct {
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	TotalTokens  int           `json:"total_tokens"`
	Latency      time.Duration `json:"latency"`
}

// Add adds another Usage to this one (for aggregation across rounds).
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		TotalTokens:  u.TotalTokens + other.TotalTokens,
		Latency:      u.Latency + other.Latency,
	}
}

// StreamEvent is a marker interface for events emitted during streaming.
type StreamEvent interface {
	streamEvent()
}

// TextDeltaEvent is emitted when the LLM produces text.
type TextDeltaEvent struct {
	Text string
}

func (TextDeltaEvent) streamEvent() {}

// ToolStartEvent is emitted when a tool begins execution.
type ToolStartEvent struct {
	ToolName string
	Params   json.RawMessage
}

func (ToolStartEvent) streamEvent() {}

// ToolEndEvent is emitted when a tool completes execution.
type ToolEndEvent struct {
	ToolName string
	Result   any
	Error    error
}

func (ToolEndEvent) streamEvent() {}

// Stream is the interface for consuming streaming LLM output.
type Stream interface {
	Events() <-chan StreamEvent
	Text() <-chan string
	Response() Response
	Err() error
}
