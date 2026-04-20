package openai

import (
	"context"
	"fmt"
	"testing"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/axonframework/axon/kernel"
)

// fakeChatCompletionsClient implements chatCompletionsClient for tests.
type fakeChatCompletionsClient struct {
	newFn       func(ctx context.Context, body sdk.ChatCompletionNewParams, opts ...option.RequestOption) (*sdk.ChatCompletion, error)
	newStreamFn func(ctx context.Context, body sdk.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[sdk.ChatCompletionChunk]
}

func (f *fakeChatCompletionsClient) New(ctx context.Context, body sdk.ChatCompletionNewParams, opts ...option.RequestOption) (*sdk.ChatCompletion, error) {
	if f.newFn != nil {
		return f.newFn(ctx, body, opts...)
	}
	return nil, fmt.Errorf("New not configured")
}

func (f *fakeChatCompletionsClient) NewStreaming(ctx context.Context, body sdk.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[sdk.ChatCompletionChunk] {
	if f.newStreamFn != nil {
		return f.newStreamFn(ctx, body, opts...)
	}
	return ssestream.NewStream[sdk.ChatCompletionChunk](nil, fmt.Errorf("NewStreaming not configured"))
}

// --- New / Options ---

func TestNew(t *testing.T) {
	llm := New(nil, "gpt-5")
	if llm.Model() != "gpt-5" {
		t.Errorf("expected model 'gpt-5', got %q", llm.Model())
	}
}

func TestNew_WithOptions(t *testing.T) {
	llm := New(nil, "gpt-5",
		WithUser("alice"),
		WithReasoningEffort("low"),
		WithStore(true),
	)
	if llm.user != "alice" {
		t.Error("expected user")
	}
	if llm.reasoningEffort != "low" {
		t.Error("expected reasoning effort")
	}
	if llm.store == nil || !*llm.store {
		t.Error("expected store=true")
	}
}

// --- Generate ---

func TestGenerate_TextResponse(t *testing.T) {
	fake := &fakeChatCompletionsClient{
		newFn: func(_ context.Context, body sdk.ChatCompletionNewParams, _ ...option.RequestOption) (*sdk.ChatCompletion, error) {
			if string(body.Model) != "gpt-5" {
				t.Errorf("unexpected model %q", body.Model)
			}
			resp := &sdk.ChatCompletion{}
			if err := resp.UnmarshalJSON([]byte(`{
				"id":"chatcmpl-1","object":"chat.completion","created":0,"model":"gpt-5",
				"choices":[{"index":0,"message":{"role":"assistant","content":"Hello from OpenAI","refusal":""},"finish_reason":"stop","logprobs":null}],
				"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}
			}`)); err != nil {
				t.Fatalf("fake unmarshal: %v", err)
			}
			return resp, nil
		},
	}
	llm := &OpenAILLM{cc: fake, model: "gpt-5"}

	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from OpenAI" {
		t.Errorf("expected 'Hello from OpenAI', got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 8 || resp.Usage.OutputTokens != 4 || resp.Usage.TotalTokens != 12 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
	if resp.Usage.Latency <= 0 {
		t.Error("expected non-zero latency")
	}
}

func TestGenerate_ToolCallResponse(t *testing.T) {
	fake := &fakeChatCompletionsClient{
		newFn: func(_ context.Context, _ sdk.ChatCompletionNewParams, _ ...option.RequestOption) (*sdk.ChatCompletion, error) {
			resp := &sdk.ChatCompletion{}
			if err := resp.UnmarshalJSON([]byte(`{
				"id":"chatcmpl-2","object":"chat.completion","created":0,"model":"gpt-5",
				"choices":[{"index":0,"message":{"role":"assistant","content":"","refusal":"",
					"tool_calls":[{"id":"call_xyz","type":"function","function":{"name":"search","arguments":"{\"q\":\"weather\"}"}}]},
					"finish_reason":"tool_calls","logprobs":null}],
				"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}
			}`)); err != nil {
				t.Fatalf("fake unmarshal: %v", err)
			}
			return resp, nil
		},
	}
	llm := &OpenAILLM{cc: fake, model: "gpt-5"}
	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("what's the weather?")},
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
	if resp.ToolCalls[0].ID != "call_xyz" {
		t.Errorf("expected ID preserved, got %q", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected 'search', got %q", resp.ToolCalls[0].Name)
	}
	if string(resp.ToolCalls[0].Params) != `{"q":"weather"}` {
		t.Errorf("expected JSON string passthrough, got %q", string(resp.ToolCalls[0].Params))
	}
}

func TestGenerate_APIError(t *testing.T) {
	fake := &fakeChatCompletionsClient{
		newFn: func(_ context.Context, _ sdk.ChatCompletionNewParams, _ ...option.RequestOption) (*sdk.ChatCompletion, error) {
			return nil, fmt.Errorf("rate limited")
		},
	}
	llm := &OpenAILLM{cc: fake, model: "gpt-5"}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got[:len("openai generate:")] != "openai generate:" {
		t.Errorf("expected prefix, got %q", got)
	}
}

func TestGenerate_SystemStaysInline(t *testing.T) {
	var msgsLen int
	var firstIsSystem bool
	fake := &fakeChatCompletionsClient{
		newFn: func(_ context.Context, body sdk.ChatCompletionNewParams, _ ...option.RequestOption) (*sdk.ChatCompletion, error) {
			msgsLen = len(body.Messages)
			if len(body.Messages) > 0 {
				firstIsSystem = body.Messages[0].OfSystem != nil
			}
			resp := &sdk.ChatCompletion{}
			_ = resp.UnmarshalJSON([]byte(`{"id":"c","object":"chat.completion","created":0,"model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok","refusal":""},"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return resp, nil
		},
	}
	llm := &OpenAILLM{cc: fake, model: "gpt-5"}

	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{
			kernel.SystemMsg("be brief"),
			kernel.UserMsg("hi"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgsLen != 2 {
		t.Errorf("expected system to stay inline (2 messages), got %d", msgsLen)
	}
	if !firstIsSystem {
		t.Error("expected first message to be system variant")
	}
}
