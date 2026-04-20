package anthropic

import (
	"context"
	"fmt"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/axonframework/axon/kernel"
)

// fakeMessagesClient implements messagesClient for testing.
type fakeMessagesClient struct {
	newFn       func(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) (*sdk.Message, error)
	newStreamFn func(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) *ssestream.Stream[sdk.MessageStreamEventUnion]
}

func (f *fakeMessagesClient) New(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) (*sdk.Message, error) {
	if f.newFn != nil {
		return f.newFn(ctx, body, opts...)
	}
	return nil, fmt.Errorf("New not configured")
}

func (f *fakeMessagesClient) NewStreaming(ctx context.Context, body sdk.MessageNewParams, opts ...option.RequestOption) *ssestream.Stream[sdk.MessageStreamEventUnion] {
	if f.newStreamFn != nil {
		return f.newStreamFn(ctx, body, opts...)
	}
	// Return a stream with a pre-filled error so callers see "not configured".
	return ssestream.NewStream[sdk.MessageStreamEventUnion](nil, fmt.Errorf("NewStreaming not configured"))
}

// --- New ---

func TestNew(t *testing.T) {
	llm := New(nil, "claude-opus-4-5")
	if llm.Model() != "claude-opus-4-5" {
		t.Errorf("expected model 'claude-opus-4-5', got %q", llm.Model())
	}
	if llm.maxTokens != defaultMaxTokens {
		t.Errorf("expected default max tokens %d, got %d", defaultMaxTokens, llm.maxTokens)
	}
}

func TestNew_WithOptions(t *testing.T) {
	md := sdk.MetadataParam{UserID: sdk.String("user-123")}
	llm := New(nil, "claude-opus-4-5",
		WithMaxTokens(2048),
		WithMetadata(md),
	)
	if llm.maxTokens != 2048 {
		t.Errorf("expected max tokens 2048, got %d", llm.maxTokens)
	}
	if llm.metadata == nil || !llm.metadata.UserID.Valid() || llm.metadata.UserID.Value != "user-123" {
		t.Error("expected metadata to be applied")
	}
}

// --- Generate ---

func TestGenerate_TextResponse(t *testing.T) {
	fake := &fakeMessagesClient{
		newFn: func(_ context.Context, body sdk.MessageNewParams, _ ...option.RequestOption) (*sdk.Message, error) {
			if body.Model != "claude-opus-4-5" {
				t.Errorf("unexpected model: %q", body.Model)
			}
			if body.MaxTokens != defaultMaxTokens {
				t.Errorf("expected default max tokens, got %d", body.MaxTokens)
			}
			msg := &sdk.Message{StopReason: "end_turn"}
			// Simulate a text content block by unmarshaling from JSON.
			if err := msg.UnmarshalJSON([]byte(`{
				"id":"msg_1","type":"message","role":"assistant",
				"content":[{"type":"text","text":"Hello from Claude"}],
				"stop_reason":"end_turn",
				"usage":{"input_tokens":8,"output_tokens":4}
			}`)); err != nil {
				t.Fatalf("fake unmarshal: %v", err)
			}
			return msg, nil
		},
	}
	llm := &AnthropicLLM{mc: fake, model: "claude-opus-4-5", maxTokens: defaultMaxTokens}

	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from Claude" {
		t.Errorf("expected 'Hello from Claude', got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 8 {
		t.Errorf("expected 8 input tokens, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 4 {
		t.Errorf("expected 4 output tokens, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 12 {
		t.Errorf("expected 12 total tokens, got %d", resp.Usage.TotalTokens)
	}
	if resp.Usage.Latency <= 0 {
		t.Errorf("expected non-zero latency")
	}
}

func TestGenerate_ToolCallResponse(t *testing.T) {
	fake := &fakeMessagesClient{
		newFn: func(_ context.Context, _ sdk.MessageNewParams, _ ...option.RequestOption) (*sdk.Message, error) {
			msg := &sdk.Message{}
			if err := msg.UnmarshalJSON([]byte(`{
				"id":"msg_2","type":"message","role":"assistant",
				"content":[{"type":"tool_use","id":"toolu_1","name":"search","input":{"query":"weather"}}],
				"stop_reason":"tool_use",
				"usage":{"input_tokens":5,"output_tokens":3}
			}`)); err != nil {
				t.Fatalf("fake unmarshal: %v", err)
			}
			return msg, nil
		},
	}
	llm := &AnthropicLLM{mc: fake, model: "claude-opus-4-5", maxTokens: defaultMaxTokens}

	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("What's the weather?")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected 'tool_calls', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "toolu_1" {
		t.Errorf("expected ID preserved from API, got %q", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected name 'search', got %q", resp.ToolCalls[0].Name)
	}
	if string(resp.ToolCalls[0].Params) != `{"query":"weather"}` {
		t.Errorf("expected params passthrough, got %s", string(resp.ToolCalls[0].Params))
	}
}

func TestGenerate_APIError(t *testing.T) {
	fake := &fakeMessagesClient{
		newFn: func(_ context.Context, _ sdk.MessageNewParams, _ ...option.RequestOption) (*sdk.Message, error) {
			return nil, fmt.Errorf("quota exceeded")
		},
	}
	llm := &AnthropicLLM{mc: fake, model: "claude-opus-4-5", maxTokens: defaultMaxTokens}

	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got[:len("anthropic generate:")] != "anthropic generate:" {
		t.Errorf("expected 'anthropic generate:' prefix, got %q", got)
	}
}

func TestGenerate_SystemPromptSeparation(t *testing.T) {
	var capturedSystemLen int
	var capturedMsgCount int
	fake := &fakeMessagesClient{
		newFn: func(_ context.Context, body sdk.MessageNewParams, _ ...option.RequestOption) (*sdk.Message, error) {
			capturedSystemLen = len(body.System)
			capturedMsgCount = len(body.Messages)
			msg := &sdk.Message{}
			_ = msg.UnmarshalJSON([]byte(`{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
			return msg, nil
		},
	}
	llm := &AnthropicLLM{mc: fake, model: "claude-opus-4-5", maxTokens: defaultMaxTokens}

	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{
			kernel.SystemMsg("be concise"),
			kernel.UserMsg("hello"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedSystemLen != 1 {
		t.Errorf("expected 1 system block, got %d", capturedSystemLen)
	}
	if capturedMsgCount != 1 {
		t.Errorf("expected 1 message (user only, system extracted), got %d", capturedMsgCount)
	}
}
