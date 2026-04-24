package anthropic

import (
	"encoding/json"
	"fmt"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// fakeDecoder implements ssestream.Decoder by replaying a prebuilt slice of
// events. The SSE payload bytes are generated via json.Marshal so the SDK's
// union unmarshaler can parse them.
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

func fakeStream(t *testing.T, events []ssestream.Event, err error) *ssestream.Stream[sdk.MessageStreamEventUnion] {
	t.Helper()
	return ssestream.NewStream[sdk.MessageStreamEventUnion](&fakeDecoder{events: events, err: err}, nil)
}

func TestStream_TextAndToolUse(t *testing.T) {
	events := []ssestream.Event{
		{Type: "message_start", Data: mustMarshal(t, map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant",
				"content":     []any{},
				"stop_reason": nil,
				"usage":       map[string]any{"input_tokens": 10, "output_tokens": 0},
			},
		})},
		{Type: "content_block_start", Data: mustMarshal(t, map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type": "text", "text": "",
			},
		})},
		{Type: "content_block_delta", Data: mustMarshal(t, map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hello "},
		})},
		{Type: "content_block_delta", Data: mustMarshal(t, map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "world"},
		})},
		{Type: "content_block_stop", Data: mustMarshal(t, map[string]any{
			"type": "content_block_stop", "index": 0,
		})},
		{Type: "content_block_start", Data: mustMarshal(t, map[string]any{
			"type":  "content_block_start",
			"index": 1,
			"content_block": map[string]any{
				"type": "tool_use", "id": "toolu_abc", "name": "search", "input": map[string]any{},
			},
		})},
		{Type: "content_block_delta", Data: mustMarshal(t, map[string]any{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"query":`},
		})},
		{Type: "content_block_delta", Data: mustMarshal(t, map[string]any{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `"weather"}`},
		})},
		{Type: "content_block_stop", Data: mustMarshal(t, map[string]any{
			"type": "content_block_stop", "index": 1,
		})},
		{Type: "message_delta", Data: mustMarshal(t, map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "tool_use"},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})},
		{Type: "message_stop", Data: mustMarshal(t, map[string]any{
			"type": "message_stop",
		})},
	}

	s := newStream(fakeStream(t, events, nil))

	// Drain text channel synchronously to collect deltas.
	var collected string
	for chunk := range s.Text() {
		collected += chunk
	}

	resp := s.Response()
	if collected != "Hello world" {
		t.Errorf("expected text chunks 'Hello world', got %q", collected)
	}
	if resp.Text != "Hello world" {
		t.Errorf("expected accumulated text 'Hello world', got %q", resp.Text)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish 'tool_calls', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "toolu_abc" {
		t.Errorf("expected ID 'toolu_abc', got %q", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected name 'search', got %q", resp.ToolCalls[0].Name)
	}
	if string(resp.ToolCalls[0].Params) != `{"query":"weather"}` {
		t.Errorf("expected reconstructed JSON, got %q", string(resp.ToolCalls[0].Params))
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
	if err := s.Err(); err != nil {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestStream_DecoderError(t *testing.T) {
	s := newStream(fakeStream(t, nil, fmt.Errorf("boom")))

	// Drain channels so the consumer goroutine exits.
	for range s.Text() {
	}
	if err := s.Err(); err == nil {
		t.Fatal("expected error")
	}
}

func TestStream_TextOnly(t *testing.T) {
	events := []ssestream.Event{
		{Type: "message_start", Data: mustMarshal(t, map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "m", "type": "message", "role": "assistant",
				"content":     []any{},
				"stop_reason": nil,
				"usage":       map[string]any{"input_tokens": 3, "output_tokens": 0},
			},
		})},
		{Type: "content_block_delta", Data: mustMarshal(t, map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "hi"},
		})},
		{Type: "message_delta", Data: mustMarshal(t, map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 1},
		})},
	}
	s := newStream(fakeStream(t, events, nil))
	for range s.Text() {
	}
	resp := s.Response()
	if resp.FinishReason != "stop" {
		t.Errorf("expected 'stop', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
}
