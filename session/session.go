// Package session introduces Session as a first-class owner of conversation
// lifecycle: history loading, message persistence, and per-session metrics.
//
// A Session ties together the durable conversation state — short-term history
// via interfaces.HistoryStore and long-term knowledge via interfaces.MemoryStore —
// with a SessionMetrics counter and a free-form State map. Session.Run wraps
// kernel.Agent.Run and orchestrates load → seed → run → persist → measure so
// callers do not have to wire history hooks by hand.
package session

import (
	"context"
	"sync"
	"time"

	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/kernel"
)

// HistoryWindow is the default number of prior messages loaded from the
// HistoryStore at the start of each Session.Run call.
const HistoryWindow = 50

// Session is the owner of a single conversation's lifecycle.
//
// A Session is sequential: a single Session.Run is not safe for concurrent
// invocation against itself. Distinct Session values may be Run concurrently —
// the SessionMetrics struct is concurrency-safe.
type Session struct {
	// ID identifies this conversation in the HistoryStore.
	ID string

	// UserID identifies the user (used as the MemoryStore key).
	UserID string

	// History is the short-term message store. May be nil — Run will skip
	// loading and persistence when nil.
	History interfaces.HistoryStore

	// Memory is the long-term knowledge store. May be nil — Run does not
	// touch Memory directly; tools and hooks consume it via Session.State.
	Memory interfaces.MemoryStore

	// State is a free-form bag for tool/hook coordination. May be nil.
	State map[string]any

	// Metrics tracks token usage, latency, and run count for this session.
	// May be nil — Run will skip metrics recording.
	Metrics *SessionMetrics
}

// Run executes one turn of the agent against this session's input.
//
// The orchestration is:
//  1. Load up to HistoryWindow prior messages from Session.History (if set).
//  2. Clone the agent with an OnStart hook that prepends those messages
//     into the AgentContext (after any system prompt, before the new user
//     message that the agent itself adds).
//  3. Run the cloned agent.
//  4. Persist the new user input plus the assistant's text response back
//     to Session.History (if set and the result has text).
//  5. Update Session.Metrics with token usage and elapsed wall-clock time.
//
// A nil History or nil Metrics is tolerated; nothing panics on either.
func (s *Session) Run(ctx context.Context, agent *kernel.Agent, input string) (*kernel.Result, error) {
	start := time.Now()

	// Step 1: load prior messages. A nil store skips this entirely.
	var prior []kernel.Message
	if s.History != nil {
		loaded, err := s.History.LoadMessages(ctx, s.ID, HistoryWindow)
		if err != nil {
			return nil, err
		}
		prior = loaded
	}

	// Step 2: clone the agent with a prepend-history hook (only if there
	// is anything to prepend — keeps the no-history path zero-overhead).
	runAgent := agent
	if len(prior) > 0 {
		runAgent = agent.CloneWith(kernel.OnStart(func(tc *kernel.TurnContext) {
			seedHistory(tc.AgentCtx, prior)
		}))
	}

	// Step 3: run the agent.
	result, err := runAgent.Run(ctx, input)
	if err != nil {
		return nil, err
	}

	// Step 4: persist new turn (skip if no text — e.g. an early-returning
	// guard scenario where the assistant produced no content).
	if s.History != nil && result != nil && result.Text != "" {
		newMsgs := []kernel.Message{
			kernel.UserMsg(input),
			kernel.AssistantMsg(result.Text),
		}
		if err := s.History.SaveMessages(ctx, s.ID, newMsgs); err != nil {
			return result, err
		}
	}

	// Step 5: update metrics.
	if s.Metrics != nil && result != nil {
		s.Metrics.record(result.Usage, time.Since(start))
	}

	return result, nil
}

// seedHistory inserts prior messages into agentCtx between any leading
// system messages and the just-added user message.
//
// At hook-fire time the context layout is:
//
//	[system?, user_input]
//
// We want:
//
//	[system?, ...prior, user_input]
func seedHistory(agentCtx *kernel.AgentContext, prior []kernel.Message) {
	if len(prior) == 0 {
		return
	}
	existing := agentCtx.Messages
	var systemMsgs []kernel.Message
	var rest []kernel.Message
	for _, m := range existing {
		if m.Role == kernel.RoleSystem {
			systemMsgs = append(systemMsgs, m)
		} else {
			rest = append(rest, m)
		}
	}
	merged := make([]kernel.Message, 0, len(systemMsgs)+len(prior)+len(rest))
	merged = append(merged, systemMsgs...)
	merged = append(merged, prior...)
	merged = append(merged, rest...)
	agentCtx.Messages = merged
}

// ---------------------------------------------------------------------------
// SessionMetrics
// ---------------------------------------------------------------------------

// SessionMetrics is a concurrency-safe counter for one Session's activity.
//
// Multiple sessions running in parallel each have their own SessionMetrics
// instance. A single Session.Run is sequential, but tooling may read the
// snapshot from another goroutine while a run is in flight — every access
// is serialized through an internal mutex.
type SessionMetrics struct {
	mu           sync.Mutex
	inputTokens  int
	outputTokens int
	totalLatency time.Duration
	runCount     int
	lastActivity time.Time
}

// NewSessionMetrics creates a zero-valued, ready-to-use SessionMetrics.
func NewSessionMetrics() *SessionMetrics {
	return &SessionMetrics{}
}

// MetricsSnapshot is an immutable point-in-time view of a SessionMetrics.
type MetricsSnapshot struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	TotalLatency time.Duration
	RunCount     int
	LastActivity time.Time
}

// Snapshot returns a copy of the current metrics. Safe to call concurrently.
func (m *SessionMetrics) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return MetricsSnapshot{
		InputTokens:  m.inputTokens,
		OutputTokens: m.outputTokens,
		TotalTokens:  m.inputTokens + m.outputTokens,
		TotalLatency: m.totalLatency,
		RunCount:     m.runCount,
		LastActivity: m.lastActivity,
	}
}

// record adds the usage and elapsed time from a single Run call.
func (m *SessionMetrics) record(usage kernel.Usage, elapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputTokens += usage.InputTokens
	m.outputTokens += usage.OutputTokens
	m.totalLatency += elapsed
	m.runCount++
	m.lastActivity = time.Now()
}
