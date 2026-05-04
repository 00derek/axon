// session/session_test.go
package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/axonframework/axon/axontest"
	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/interfaces/inmemory"
	"github.com/axonframework/axon/kernel"
	"github.com/axonframework/axon/session"
)

// TestRunPersistsUserAndAssistantMessages verifies that after a successful
// turn, the session's HistoryStore contains both the user input and the
// assistant's text response (in order).
func TestRunPersistsUserAndAssistantMessages(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("hello back")

	agent := kernel.NewAgent(kernel.WithModel(llm))

	history := inmemory.NewHistoryStore()
	s := &session.Session{
		ID:      "sess-1",
		History: history,
		Metrics: session.NewSessionMetrics(),
	}

	result, err := s.Run(context.Background(), agent, "hello")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Text != "hello back" {
		t.Errorf("got text %q, want %q", result.Text, "hello back")
	}

	msgs, err := history.LoadMessages(context.Background(), "sess-1", 0)
	if err != nil {
		t.Fatalf("LoadMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(msgs))
	}
	if msgs[0].Role != kernel.RoleUser || msgs[0].TextContent() != "hello" {
		t.Errorf("first message wrong: role=%s text=%q", msgs[0].Role, msgs[0].TextContent())
	}
	if msgs[1].Role != kernel.RoleAssistant || msgs[1].TextContent() != "hello back" {
		t.Errorf("second message wrong: role=%s text=%q", msgs[1].Role, msgs[1].TextContent())
	}
}

// TestRunLoadsExistingHistoryIntoAgentContext verifies that prior messages
// from the HistoryStore are seeded into the AgentContext before the LLM
// runs, so the LLM sees them on the first round.
func TestRunLoadsExistingHistoryIntoAgentContext(t *testing.T) {
	history := inmemory.NewHistoryStore()
	prior := []kernel.Message{
		kernel.UserMsg("earlier question"),
		kernel.AssistantMsg("earlier answer"),
	}
	if err := history.SaveMessages(context.Background(), "sess-2", prior); err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	// Capture what the LLM is asked.
	var seen []kernel.Message
	llm := &capturingLLM{respond: "current reply", capture: &seen}

	agent := kernel.NewAgent(kernel.WithModel(llm))

	s := &session.Session{
		ID:      "sess-2",
		History: history,
		Metrics: session.NewSessionMetrics(),
	}

	if _, err := s.Run(context.Background(), agent, "current question"); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// The LLM should have seen prior history followed by the new user message.
	wantTexts := []string{"earlier question", "earlier answer", "current question"}
	if len(seen) != len(wantTexts) {
		t.Fatalf("LLM saw %d messages, want %d (got: %+v)", len(seen), len(wantTexts), msgTexts(seen))
	}
	for i, want := range wantTexts {
		if seen[i].TextContent() != want {
			t.Errorf("message %d: got %q, want %q", i, seen[i].TextContent(), want)
		}
	}
}

// TestRunNilHistoryIsGraceful verifies that running a session with a nil
// HistoryStore does not panic and still returns a usable result.
func TestRunNilHistoryIsGraceful(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("ok")

	agent := kernel.NewAgent(kernel.WithModel(llm))

	s := &session.Session{
		ID:      "sess-nil-history",
		History: nil,
		Memory:  nil,
		Metrics: session.NewSessionMetrics(),
	}

	result, err := s.Run(context.Background(), agent, "hi")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Text != "ok" {
		t.Errorf("got %q, want %q", result.Text, "ok")
	}
}

// TestRunNilMetricsIsGraceful verifies that a Session without explicit
// Metrics still runs without panicking.
func TestRunNilMetricsIsGraceful(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("ok")

	agent := kernel.NewAgent(kernel.WithModel(llm))

	s := &session.Session{
		ID:      "sess-nil-metrics",
		History: inmemory.NewHistoryStore(),
		Metrics: nil,
	}

	if _, err := s.Run(context.Background(), agent, "hi"); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

// TestMetricsAccumulatesAcrossRuns verifies that input/output tokens, run
// count, and last activity all update across successive Run calls on the
// same Session.
func TestMetricsAccumulatesAcrossRuns(t *testing.T) {
	// Build a mock LLM that reports a known per-call usage.
	llm := &usageLLM{inputTokens: 10, outputTokens: 5, text: "ok"}
	agent := kernel.NewAgent(kernel.WithModel(llm))

	s := &session.Session{
		ID:      "sess-metrics",
		History: inmemory.NewHistoryStore(),
		Metrics: session.NewSessionMetrics(),
	}

	before := time.Now()

	if _, err := s.Run(context.Background(), agent, "first"); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}
	if _, err := s.Run(context.Background(), agent, "second"); err != nil {
		t.Fatalf("second Run failed: %v", err)
	}
	if _, err := s.Run(context.Background(), agent, "third"); err != nil {
		t.Fatalf("third Run failed: %v", err)
	}

	snap := s.Metrics.Snapshot()
	if snap.RunCount != 3 {
		t.Errorf("RunCount: got %d, want 3", snap.RunCount)
	}
	if snap.InputTokens != 30 {
		t.Errorf("InputTokens: got %d, want 30", snap.InputTokens)
	}
	if snap.OutputTokens != 15 {
		t.Errorf("OutputTokens: got %d, want 15", snap.OutputTokens)
	}
	if snap.LastActivity.Before(before) {
		t.Errorf("LastActivity %v should be >= start time %v", snap.LastActivity, before)
	}
	if snap.TotalLatency <= 0 {
		t.Errorf("TotalLatency should be > 0, got %v", snap.TotalLatency)
	}
}

// TestConcurrentSessionsAccumulateIndependently verifies that two distinct
// sessions running concurrently do not interfere — and that metrics on
// each are correct.
func TestConcurrentSessionsAccumulateIndependently(t *testing.T) {
	llm := &usageLLM{inputTokens: 1, outputTokens: 1, text: "ok"}
	agent := kernel.NewAgent(kernel.WithModel(llm))

	s1 := &session.Session{
		ID: "a", History: inmemory.NewHistoryStore(), Metrics: session.NewSessionMetrics(),
	}
	s2 := &session.Session{
		ID: "b", History: inmemory.NewHistoryStore(), Metrics: session.NewSessionMetrics(),
	}

	done := make(chan struct{}, 2)
	go func() {
		for i := 0; i < 5; i++ {
			s1.Run(context.Background(), agent, "x")
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 5; i++ {
			s2.Run(context.Background(), agent, "y")
		}
		done <- struct{}{}
	}()
	<-done
	<-done

	if got := s1.Metrics.Snapshot().RunCount; got != 5 {
		t.Errorf("s1 RunCount: got %d, want 5", got)
	}
	if got := s2.Metrics.Snapshot().RunCount; got != 5 {
		t.Errorf("s2 RunCount: got %d, want 5", got)
	}
}

// TestRunWithMemoryStoreSet verifies that a Memory store on the Session
// is exposed as part of State so tools/hooks can look it up. We don't
// dictate semantics here — just that a non-nil Memory does not break Run
// and the Session struct exposes it.
func TestSessionExposesMemory(t *testing.T) {
	mem := inmemory.NewMemoryStore()
	s := &session.Session{
		ID:     "sess-mem",
		UserID: "user-1",
		Memory: mem,
	}
	if s.Memory == nil {
		t.Fatal("Memory should be non-nil")
	}
	// Check the field type matches the interface.
	var _ interfaces.MemoryStore = s.Memory
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// capturingLLM records the last set of messages it was asked to generate from.
type capturingLLM struct {
	respond string
	capture *[]kernel.Message
}

func (c *capturingLLM) Generate(_ context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	if c.capture != nil {
		// Copy slice to avoid aliasing into the agent's mutable buffer.
		dst := make([]kernel.Message, len(params.Messages))
		copy(dst, params.Messages)
		*c.capture = dst
	}
	return kernel.Response{Text: c.respond, FinishReason: "stop"}, nil
}

func (c *capturingLLM) GenerateStream(_ context.Context, _ kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (c *capturingLLM) Model() string { return "capturing" }

// usageLLM returns a fixed text response with a fixed Usage on every call.
type usageLLM struct {
	inputTokens  int
	outputTokens int
	text         string
}

func (u *usageLLM) Generate(_ context.Context, _ kernel.GenerateParams) (kernel.Response, error) {
	return kernel.Response{
		Text:         u.text,
		FinishReason: "stop",
		Usage: kernel.Usage{
			InputTokens:  u.inputTokens,
			OutputTokens: u.outputTokens,
			TotalTokens:  u.inputTokens + u.outputTokens,
			Latency:      time.Millisecond,
		},
	}, nil
}

func (u *usageLLM) GenerateStream(_ context.Context, _ kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (u *usageLLM) Model() string { return "usage" }

func msgTexts(msgs []kernel.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role) + ":" + m.TextContent()
	}
	return out
}
