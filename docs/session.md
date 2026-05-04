# Session

A `Session` owns the lifecycle of a single conversation: short-term history,
long-term memory, free-form state, and per-conversation metrics. The
`session` package introduces `Session.Run` so applications no longer hand-roll
`OnStart` / `OnFinish` hooks just to load and persist messages.

The `session` package lives at `github.com/axonframework/axon/session` and is
its own Go module. The kernel does not depend on it; it depends on the kernel
and on `interfaces`.

---

## 1. Why a Session type?

Without `Session`, every multi-turn agent application repeats the same plumbing:

```go
// Pre-Session pattern — load history, prepend it, persist new turn.
agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.OnStart(func(tc *kernel.TurnContext) {
        msgs, _ := store.LoadMessages(ctx, sessionID, 20)
        // splice msgs in before the user message ...
    }),
    kernel.OnFinish(func(tc *kernel.TurnContext) {
        store.SaveMessages(ctx, sessionID, []kernel.Message{
            kernel.UserMsg(tc.Input),
            kernel.AssistantMsg(tc.Result.Text),
        })
    }),
)
```

That snippet recurs identically across every example, every chatbot, every
support agent. It also entangles persistence with agent construction —
swapping a session id at run-time means rebuilding the agent.

`Session.Run` factors that lifecycle out:

```go
sess := &session.Session{
    ID:      "user-42-thread-3",
    UserID:  "user-42",
    History: store,
    Metrics: session.NewSessionMetrics(),
}
result, err := sess.Run(ctx, agent, "Book a table for 2 tonight at 8 PM")
```

The agent is now stateless across turns. Sessions are values you create per
conversation, hold onto for as long as the conversation lives, and dispose of
when it ends.

---

## 2. The Session struct

```go
type Session struct {
    ID      string                    // session id (history key)
    UserID  string                    // user id (memory key)
    History interfaces.HistoryStore   // optional
    Memory  interfaces.MemoryStore    // optional
    State   map[string]any            // free-form bag for tools/hooks
    Metrics *SessionMetrics           // optional
}

func (s *Session) Run(
    ctx context.Context,
    agent *kernel.Agent,
    input string,
) (*kernel.Result, error)
```

Every field is optional. A `Session` with all-nil fields still runs — it just
won't load history, persist messages, or record metrics.

---

## 3. What Session.Run does

`Session.Run` orchestrates one turn:

```
sess.Run(ctx, agent, input)
│
├─ History.LoadMessages(ctx, sess.ID, HistoryWindow)
│      └── prior messages (if any)
│
├─ agent.CloneWith(OnStart hook that prepends prior into AgentContext)
│      └── runAgent.Run(ctx, input)
│              ├── system prompt
│              ├── ...prior history...
│              └── new user message
│
├─ History.SaveMessages(ctx, sess.ID, [user, assistant])
│
└─ Metrics.record(result.Usage, elapsed)
```

A few details worth noting:

- The default history window is `session.HistoryWindow` (50). Lower it by
  using a `HistoryStore` that already truncates, or fork the package if you
  need a different default.
- Hook clones are *only* taken when there is history to splice in. The
  zero-history path makes no copies.
- `SaveMessages` is skipped when `result.Text` is empty — for example when a
  guard short-circuited the turn before the LLM produced a response.
- `Memory` is exposed on the `Session` struct but `Session.Run` does not call
  it directly. Tools and hooks read `sess.Memory` through `State` or via a
  closure — whichever you prefer. This keeps memory semantics (semantic
  search? full retrieval?) in your hands.

---

## 4. SessionMetrics

```go
type SessionMetrics struct { /* unexported */ }

func NewSessionMetrics() *SessionMetrics
func (m *SessionMetrics) Snapshot() MetricsSnapshot

type MetricsSnapshot struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int
    TotalLatency time.Duration
    RunCount     int
    LastActivity time.Time
}
```

`SessionMetrics` is concurrency-safe. A single `Session.Run` invocation is
sequential — call it from one goroutine at a time per session — but distinct
sessions can run in parallel and read each other's snapshots safely.

`InputTokens` and `OutputTokens` come from the `kernel.Usage` returned by the
agent loop, so they reflect *every* round in the turn (the LLM may be invoked
several times per `Run` if tools are involved).

---

## 5. Comparison with `middleware.CostTracker`

`CostTracker` measures cost across an *LLM* — every call through the
middleware-wrapped client contributes, regardless of which session it came
from. `SessionMetrics` measures cost per *conversation*. They are
complementary; the restaurant bot example uses both.

---

## 6. Putting it together

```go
package main

import (
    "context"

    "github.com/axonframework/axon/interfaces/inmemory"
    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/session"
)

func main() {
    agent := kernel.NewAgent(
        kernel.WithModel(myLLM),
        kernel.WithSystemPrompt("You are a helpful assistant."),
        kernel.WithTools(myTools()...),
    )

    sess := &session.Session{
        ID:      "thread-42",
        UserID:  "alice",
        History: inmemory.NewHistoryStore(),
        Memory:  inmemory.NewMemoryStore(),
        Metrics: session.NewSessionMetrics(),
    }

    ctx := context.Background()
    sess.Run(ctx, agent, "Plan a 3-day trip to Tokyo")
    sess.Run(ctx, agent, "Make day 2 indoor activities only")
    sess.Run(ctx, agent, "Estimate the total cost")

    snap := sess.Metrics.Snapshot()
    log.Printf("turns=%d in=%d out=%d total=%v",
        snap.RunCount, snap.InputTokens, snap.OutputTokens, snap.TotalLatency)
}
```

Three turns, one `Session`, no hand-rolled hook plumbing.

See `examples/07-restaurant-bot` for a complete working example with
middleware, a guard, and `Session` driving the conversation.
