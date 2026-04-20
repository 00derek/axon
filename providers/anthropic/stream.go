package anthropic

import (
	"encoding/json"
	"sync"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"github.com/axonframework/axon/kernel"
)

// stream implements kernel.Stream atop Anthropic's SSE event stream.
// Text deltas are forwarded immediately; tool_use blocks accumulate JSON
// input across input_json_delta events and are finalized on content_block_stop.
type stream struct {
	eventCh chan kernel.StreamEvent
	textCh  chan string

	resp kernel.Response
	err  error
	done chan struct{}
	mu   sync.Mutex
}

// Compile-time check.
var _ kernel.Stream = (*stream)(nil)

// newStream wraps a MessageStreamEventUnion SSE stream and spawns a consumer
// goroutine. Channels are buffered at 64 to match the Google provider.
func newStream(sseStream *ssestream.Stream[sdk.MessageStreamEventUnion]) *stream {
	s := &stream{
		eventCh: make(chan kernel.StreamEvent, 64),
		textCh:  make(chan string, 64),
		done:    make(chan struct{}),
	}
	go s.consume(sseStream)
	return s
}

// toolBuffer accumulates a tool_use block's streamed JSON input.
type toolBuffer struct {
	id      string
	name    string
	order   int
	argsBuf []byte
}

// consume drains the SSE stream, emitting text deltas live and finalizing
// tool calls when their content blocks stop.
func (s *stream) consume(sseStream *ssestream.Stream[sdk.MessageStreamEventUnion]) {
	defer close(s.eventCh)
	defer close(s.textCh)
	defer close(s.done)
	defer func() { _ = sseStream.Close() }()

	var (
		fullText     string
		tools        = make(map[int64]*toolBuffer)
		toolOrder    int
		stopReason   sdk.StopReason
		inputTokens  int64
		outputTokens int64
	)

	for sseStream.Next() {
		event := sseStream.Current()

		switch event.Type {
		case "message_start":
			start := event.AsMessageStart()
			inputTokens = start.Message.Usage.InputTokens
			outputTokens = start.Message.Usage.OutputTokens
			if start.Message.StopReason != "" {
				stopReason = start.Message.StopReason
			}

		case "content_block_start":
			cbs := event.AsContentBlockStart()
			cb := cbs.ContentBlock
			if cb.Type == "tool_use" {
				tools[cbs.Index] = &toolBuffer{
					id:    cb.ID,
					name:  cb.Name,
					order: toolOrder,
				}
				toolOrder++
			}

		case "content_block_delta":
			cbd := event.AsContentBlockDelta()
			switch cbd.Delta.Type {
			case "text_delta":
				txt := cbd.Delta.Text
				if txt == "" {
					continue
				}
				fullText += txt
				s.textCh <- txt
				s.eventCh <- kernel.TextDeltaEvent{Text: txt}
			case "input_json_delta":
				if buf, ok := tools[cbd.Index]; ok {
					buf.argsBuf = append(buf.argsBuf, cbd.Delta.PartialJSON...)
				}
			}

		case "content_block_stop":
			// No action needed; finalization happens at stream close below.

		case "message_delta":
			md := event.AsMessageDelta()
			if md.Delta.StopReason != "" {
				stopReason = md.Delta.StopReason
			}
			// Cumulative usage deltas.
			if md.Usage.InputTokens > 0 {
				inputTokens = md.Usage.InputTokens
			}
			if md.Usage.OutputTokens > 0 {
				outputTokens = md.Usage.OutputTokens
			}

		case "message_stop":
			// Terminal event; loop naturally exits on next Next() call.
		}
	}

	if err := sseStream.Err(); err != nil {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}

	// Flatten tool buffers into the accumulated order.
	toolCalls := make([]kernel.ToolCall, len(tools))
	for _, buf := range tools {
		args := buf.argsBuf
		if len(args) == 0 {
			args = []byte("{}")
		}
		toolCalls[buf.order] = kernel.ToolCall{
			ID:     buf.id,
			Name:   buf.name,
			Params: json.RawMessage(args),
		}
	}

	finish := convertStopReason(stopReason)
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}

	s.mu.Lock()
	s.resp = kernel.Response{
		Text:         fullText,
		ToolCalls:    toolCalls,
		FinishReason: finish,
		Usage: kernel.Usage{
			InputTokens:  int(inputTokens),
			OutputTokens: int(outputTokens),
			TotalTokens:  int(inputTokens + outputTokens),
		},
	}
	s.mu.Unlock()
}

// Events returns the channel of stream events.
func (s *stream) Events() <-chan kernel.StreamEvent { return s.eventCh }

// Text returns the channel of text deltas.
func (s *stream) Text() <-chan string { return s.textCh }

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
