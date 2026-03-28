package google

import (
	"iter"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// stream implements kernel.Stream by wrapping a genai iterator.
type stream struct {
	eventCh chan kernel.StreamEvent
	textCh  chan string
	resp    kernel.Response
	err     error
	done    chan struct{}
}

// Compile-time check that stream implements kernel.Stream.
var _ kernel.Stream = (*stream)(nil)

// newStream creates a stream from a genai iterator and starts consuming it
// in a background goroutine.
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

	// Placeholder: full implementation in Task 3.
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
	return s.resp
}

// Err blocks until the stream completes and returns any error encountered.
func (s *stream) Err() error {
	<-s.done
	return s.err
}
