package google

import (
	"encoding/json"
	"fmt"
	"iter"
	"sync"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// stream implements kernel.Stream by wrapping a genai iterator.
type stream struct {
	eventCh chan kernel.StreamEvent
	textCh  chan string

	resp kernel.Response
	err  error
	done chan struct{}
	mu   sync.Mutex
}

// Compile-time check that stream implements kernel.Stream.
var _ kernel.Stream = (*stream)(nil)

// newStream creates a stream from a genai iterator and starts consuming it
// in a background goroutine. Text parts are emitted as TextDeltaEvents.
// Tool calls are accumulated. The final Response is built from all chunks.
func newStream(iterator iter.Seq2[*genai.GenerateContentResponse, error]) *stream {
	s := &stream{
		eventCh: make(chan kernel.StreamEvent, 64),
		textCh:  make(chan string, 64),
		done:    make(chan struct{}),
	}

	go s.consume(iterator)

	return s
}

// consume reads chunks from the iterator and populates the stream channels.
func (s *stream) consume(iterator iter.Seq2[*genai.GenerateContentResponse, error]) {
	defer close(s.eventCh)
	defer close(s.textCh)
	defer close(s.done)

	var (
		fullText     string
		toolCalls    []kernel.ToolCall
		finishReason string
		usage        kernel.Usage
		callIdx      int
	)

	for chunk, err := range iterator {
		if err != nil {
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
			break
		}

		if chunk == nil {
			continue
		}

		// Extract usage from the last chunk that has it.
		if chunk.UsageMetadata != nil {
			usage = kernel.Usage{
				InputTokens:  derefInt32(chunk.UsageMetadata.PromptTokenCount),
				OutputTokens: derefInt32(chunk.UsageMetadata.CandidatesTokenCount),
				TotalTokens:  int(chunk.UsageMetadata.TotalTokenCount),
			}
		}

		if len(chunk.Candidates) == 0 {
			continue
		}

		candidate := chunk.Candidates[0]

		if candidate.FinishReason != "" {
			finishReason = convertFinishReason(candidate.FinishReason)
		}

		if candidate.Content == nil {
			continue
		}

		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				txt := part.Text
				fullText += txt
				s.textCh <- txt
				s.eventCh <- kernel.TextDeltaEvent{Text: txt}
			}

			if part.FunctionCall != nil {
				params, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, kernel.ToolCall{
					ID:     fmt.Sprintf("call_%d", callIdx),
					Name:   part.FunctionCall.Name,
					Params: params,
				})
				callIdx++
			}
		}
	}

	// Override finish reason if tool calls were accumulated.
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if finishReason == "" {
		finishReason = "stop"
	}

	s.mu.Lock()
	s.resp = kernel.Response{
		Text:         fullText,
		ToolCalls:    toolCalls,
		Usage:        usage,
		FinishReason: finishReason,
	}
	s.mu.Unlock()
}

// Events returns the channel of stream events.
func (s *stream) Events() <-chan kernel.StreamEvent {
	return s.eventCh
}

// Text returns the channel of text deltas.
func (s *stream) Text() <-chan string {
	return s.textCh
}

// Response blocks until the stream completes and returns the accumulated response.
func (s *stream) Response() kernel.Response {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resp
}

// Err blocks until the stream completes and returns any error encountered.
func (s *stream) Err() error {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
