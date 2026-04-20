package openai

import (
	"encoding/json"
	"fmt"
	"testing"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
)

type fakeDecoder struct {
	events []ssestream.Event
	idx    int
	err    error
}

func (f *fakeDecoder) Next() bool {
	if f.idx >= len(f.events) {
		return false
	}
	f.idx++
	return true
}
func (f *fakeDecoder) Event() ssestream.Event { return f.events[f.idx-1] }
func (f *fakeDecoder) Close() error           { return nil }
func (f *fakeDecoder) Err() error             { return f.err }

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// OpenAI uses SSE with Type="" (bare data: lines) for chat completion chunks,
// not a named event type like Anthropic. We set Type="completion" so the
// ssestream dispatcher recognizes the event.
func chunk(t *testing.T, data any) ssestream.Event {
	return ssestream.Event{Type: "completion", Data: mustMarshal(t, data)}
}

func fakeStream(t *testing.T, events []ssestream.Event, err error) *ssestream.Stream[sdk.ChatCompletionChunk] {
	return ssestream.NewStream[sdk.ChatCompletionChunk](&fakeDecoder{events: events, err: err}, nil)
}

func TestStream_TextOnly(t *testing.T) {
	events := []ssestream.Event{
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"content": "Hello "},
			}},
		}),
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{"content": "world"},
			}},
		}),
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{map[string]any{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			}},
		}),
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{},
			"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
		}),
	}
	s := newStream(fakeStream(t, events, nil))

	var collected string
	for txt := range s.Text() {
		collected += txt
	}
	resp := s.Response()

	if collected != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", collected)
	}
	if resp.Text != "Hello world" {
		t.Errorf("expected accumulated text, got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 5 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestStream_MultiToolCall_ArgumentsSplitAcrossChunks(t *testing.T) {
	// Tool 0 gets header (id, name) in first delta and arguments across two.
	// Tool 1 is interleaved with its own argument fragments.
	events := []ssestream.Event{
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []any{
						map[string]any{"index": 0, "id": "call_a", "function": map[string]any{"name": "first", "arguments": ""}},
					},
				},
			}},
		}),
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []any{
						map[string]any{"index": 0, "function": map[string]any{"arguments": `{"q":`}},
					},
				},
			}},
		}),
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []any{
						map[string]any{"index": 1, "id": "call_b", "function": map[string]any{"name": "second", "arguments": `{"x":1}`}},
						map[string]any{"index": 0, "function": map[string]any{"arguments": `"weather"}`}},
					},
				},
			}},
		}),
		chunk(t, map[string]any{
			"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "gpt-5",
			"choices": []any{map[string]any{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "tool_calls",
			}},
		}),
	}
	s := newStream(fakeStream(t, events, nil))

	// Drain text channel (nothing expected).
	for range s.Text() {
	}
	resp := s.Response()

	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected 'tool_calls', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_a" || resp.ToolCalls[0].Name != "first" {
		t.Errorf("unexpected tool 0: %+v", resp.ToolCalls[0])
	}
	if string(resp.ToolCalls[0].Params) != `{"q":"weather"}` {
		t.Errorf("expected reconstructed args, got %q", string(resp.ToolCalls[0].Params))
	}
	if resp.ToolCalls[1].ID != "call_b" || resp.ToolCalls[1].Name != "second" {
		t.Errorf("unexpected tool 1: %+v", resp.ToolCalls[1])
	}
	if string(resp.ToolCalls[1].Params) != `{"x":1}` {
		t.Errorf("unexpected tool 1 args: %q", string(resp.ToolCalls[1].Params))
	}
}

func TestStream_DecoderError(t *testing.T) {
	s := newStream(fakeStream(t, nil, fmt.Errorf("boom")))
	for range s.Text() {
	}
	if err := s.Err(); err == nil {
		t.Fatal("expected error")
	}
}
