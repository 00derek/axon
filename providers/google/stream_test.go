package google

import (
	"fmt"
	"iter"
	"testing"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

func TestStream_TextOnly(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []*genai.Part{{Text: "Hello "}}},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content:      &genai.Content{Parts: []*genai.Part{{Text: "world"}}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     genai.Ptr[int32](5),
				CandidatesTokenCount: genai.Ptr[int32](3),
				TotalTokenCount:      8,
			},
		},
	}

	iterator := makeTestIterator(chunks, nil)
	s := newStream(iterator)

	// Collect text deltas
	var texts []string
	for txt := range s.Text() {
		texts = append(texts, txt)
	}

	if len(texts) != 2 {
		t.Fatalf("expected 2 text chunks, got %d", len(texts))
	}
	if texts[0] != "Hello " {
		t.Errorf("expected first chunk 'Hello ', got %q", texts[0])
	}
	if texts[1] != "world" {
		t.Errorf("expected second chunk 'world', got %q", texts[1])
	}

	resp := s.Response()
	if resp.Text != "Hello world" {
		t.Errorf("expected full text 'Hello world', got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 5 {
		t.Errorf("expected 5 input tokens, got %d", resp.Usage.InputTokens)
	}
	if s.Err() != nil {
		t.Errorf("unexpected error: %v", s.Err())
	}
}

func TestStream_Events(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []*genai.Part{{Text: "chunk1"}}},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content:      &genai.Content{Parts: []*genai.Part{{Text: "chunk2"}}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
		},
	}

	iterator := makeTestIterator(chunks, nil)
	s := newStream(iterator)

	var events []kernel.StreamEvent
	for ev := range s.Events() {
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	delta1, ok := events[0].(kernel.TextDeltaEvent)
	if !ok {
		t.Fatalf("expected TextDeltaEvent, got %T", events[0])
	}
	if delta1.Text != "chunk1" {
		t.Errorf("expected 'chunk1', got %q", delta1.Text)
	}
}

func TestStream_WithToolCalls(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []*genai.Part{
					{Text: "Let me search for that. "},
				}},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{
						Name: "search",
						Args: map[string]any{"query": "weather"},
					}},
				}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     genai.Ptr[int32](10),
				CandidatesTokenCount: genai.Ptr[int32](8),
				TotalTokenCount:      18,
			},
		},
	}

	iterator := makeTestIterator(chunks, nil)
	s := newStream(iterator)

	// Drain events
	for range s.Events() {
	}

	resp := s.Response()
	if resp.Text != "Let me search for that. " {
		t.Errorf("expected text 'Let me search for that. ', got %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool call name 'search', got %q", resp.ToolCalls[0].Name)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason)
	}
}

func TestStream_Error(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []*genai.Part{{Text: "partial"}}},
			}},
		},
	}

	iterator := makeTestIterator(chunks, fmt.Errorf("connection reset"))
	s := newStream(iterator)

	// Drain
	for range s.Events() {
	}

	if s.Err() == nil {
		t.Fatal("expected error, got nil")
	}
	if s.Err().Error() != "connection reset" {
		t.Errorf("unexpected error: %v", s.Err())
	}

	// Partial text should still be available
	resp := s.Response()
	if resp.Text != "partial" {
		t.Errorf("expected partial text 'partial', got %q", resp.Text)
	}
}

func TestStream_Empty(t *testing.T) {
	iterator := makeTestIterator(nil, nil)
	s := newStream(iterator)

	for range s.Events() {
	}

	resp := s.Response()
	if resp.Text != "" {
		t.Errorf("expected empty text, got %q", resp.Text)
	}
	if s.Err() != nil {
		t.Errorf("unexpected error: %v", s.Err())
	}
}

// makeTestIterator creates a test iterator from a slice of responses and an optional final error.
func makeTestIterator(chunks []*genai.GenerateContentResponse, finalErr error) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		for _, chunk := range chunks {
			if !yield(chunk, nil) {
				return
			}
		}
		if finalErr != nil {
			yield(nil, finalErr)
		}
	}
}
