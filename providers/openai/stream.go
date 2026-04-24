package openai

import (
	"encoding/json"
	"sort"
	"sync"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/axonframework/axon/kernel"
)

// stream implements kernel.Stream atop OpenAI's SSE chunk stream. Text deltas
// are forwarded immediately; tool call arguments arrive as partial JSON
// deltas indexed by slot and must be accumulated per Index.
type stream struct {
	eventCh chan kernel.StreamEvent
	textCh  chan string

	resp kernel.Response
	err  error
	done chan struct{}
	mu   sync.Mutex
}

var _ kernel.Stream = (*stream)(nil)

// toolBuffer accumulates per-index tool-call data across chunks.
type toolBuffer struct {
	id      string
	name    string
	order   int
	argsBuf []byte
}

// newStream wraps a ChatCompletionChunk SSE stream and starts the consumer
// goroutine. Channels are buffered at 64 to match other providers.
func newStream(sseStream *ssestream.Stream[sdk.ChatCompletionChunk]) *stream {
	s := &stream{
		eventCh: make(chan kernel.StreamEvent, 64),
		textCh:  make(chan string, 64),
		done:    make(chan struct{}),
	}
	go s.consume(sseStream)
	return s
}

func (s *stream) consume(sseStream *ssestream.Stream[sdk.ChatCompletionChunk]) {
	defer close(s.eventCh)
	defer close(s.textCh)
	defer close(s.done)
	defer func() { _ = sseStream.Close() }()

	var (
		fullText     string
		tools        = make(map[int64]*toolBuffer)
		order        int
		finishReason string
		usage        kernel.Usage
		haveUsage    bool
	)

	for sseStream.Next() {
		chunk := sseStream.Current()

		// The final chunk under IncludeUsage carries Usage and an empty Choices.
		if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 || chunk.Usage.TotalTokens != 0 {
			usage = kernel.Usage{
				InputTokens:  int(chunk.Usage.PromptTokens),
				OutputTokens: int(chunk.Usage.CompletionTokens),
				TotalTokens:  int(chunk.Usage.TotalTokens),
			}
			haveUsage = true
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}

		if choice.Delta.Content != "" {
			txt := choice.Delta.Content
			fullText += txt
			s.textCh <- txt
			s.eventCh <- kernel.TextDeltaEvent{Text: txt}
		}

		for _, tc := range choice.Delta.ToolCalls {
			buf, ok := tools[tc.Index]
			if !ok {
				buf = &toolBuffer{order: order}
				order++
				tools[tc.Index] = buf
			}
			if tc.ID != "" {
				buf.id = tc.ID
			}
			if tc.Function.Name != "" {
				buf.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				buf.argsBuf = append(buf.argsBuf, tc.Function.Arguments...)
			}
		}
	}

	if err := sseStream.Err(); err != nil {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}

	// Flatten tool buffers, preserving index order rather than arrival order.
	indexes := make([]int64, 0, len(tools))
	for idx := range tools {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })

	toolCalls := make([]kernel.ToolCall, 0, len(indexes))
	for _, idx := range indexes {
		buf := tools[idx]
		args := buf.argsBuf
		if len(args) == 0 {
			args = []byte("{}")
		}
		toolCalls = append(toolCalls, kernel.ToolCall{
			ID:     buf.id,
			Name:   buf.name,
			Params: json.RawMessage(args),
		})
	}

	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	} else {
		finishReason = normalizeFinishReason(finishReason)
	}

	s.mu.Lock()
	s.resp = kernel.Response{
		Text:         fullText,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
	}
	if haveUsage {
		s.resp.Usage = usage
	}
	s.mu.Unlock()
}

// Events returns the channel of stream events.
func (s *stream) Events() <-chan kernel.StreamEvent { return s.eventCh }

// Text returns the channel of text deltas.
func (s *stream) Text() <-chan string { return s.textCh }

// Response blocks until the stream completes and returns the final response.
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
