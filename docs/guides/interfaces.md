# Interfaces

Axon defines four contracts that separate persistence and safety concerns from
agent logic: `HistoryStore`, `MemoryStore`, `Guard`, and `Embedder`. Each
interface lives in `github.com/axonframework/axon/interfaces`. Thread-safe
in-memory reference implementations are provided in the
`github.com/axonframework/axon/interfaces/inmemory` package for development and
testing. Bring your own implementations for production.

---

## Hook integration overview

The diagram below shows how stores and guards plug into the agent lifecycle via
`OnStart` and `OnFinish` hooks.

```
agent.Run(ctx, "user input")
│
├─ OnStart
│  ├─ Guard.Check(input) ──► blocked? → disable tools, change prompt
│  ├─ HistoryStore.LoadMessages(sessionID) ──► prepend to conversation
│  └─ MemoryStore.Search(userID, input) ──► inject as system message
│
├─ Agent loop (rounds)
│  └─ ... LLM generates, tools execute ...
│
└─ OnFinish
   ├─ HistoryStore.SaveMessages(sessionID, newMessages)
   └─ MemoryStore.Save(userID, extractedMemories)  ← async/optional
```

---

## 1. HistoryStore

`HistoryStore` manages short-term conversation history scoped to a session.

```go
type HistoryStore interface {
    SaveMessages(ctx context.Context, sessionID string, messages []kernel.Message) error
    LoadMessages(ctx context.Context, sessionID string, limit int) ([]kernel.Message, error)
    Clear(ctx context.Context, sessionID string) error
}
```

- `SaveMessages` appends messages to the session's history.
- `LoadMessages` returns the last `limit` messages. Pass `limit <= 0` to
  retrieve all messages.
- `Clear` removes the entire message history for a session.

### Session model

Each session accumulates turns. `LoadMessages` returns the most recent slice;
`SaveMessages` appends the new turn.

```
Session "user-123-abc"
┌──────────────────────────────────────────┐
│  SaveMessages(session, [user, assistant])│
│                                          │
│  Turn 1: [UserMsg, AssistantMsg]         │
│  Turn 2: [UserMsg, AssistantMsg]         │
│  Turn 3: [UserMsg, AssistantMsg]         │
│  ...                                     │
│                                          │
│  LoadMessages(session, limit=20)         │
│  → returns last 20 messages              │
└──────────────────────────────────────────┘
```

### In-memory implementation

```go
import "github.com/axonframework/axon/interfaces/inmemory"

store := inmemory.NewHistoryStore()
```

`inmemory.NewHistoryStore` returns a thread-safe store backed by an in-memory
map. It is suitable for development and testing but does not survive process
restarts.

### Integration pattern: OnStart / OnFinish hooks

The standard way to wire a `HistoryStore` into an agent is through lifecycle
hooks. The pattern loads prior history before the model sees the new user
message, and persists the completed turn after the model responds.

```go
kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithSystemPrompt(systemPrompt),

    // OnStart: prepend conversation history before the model call.
    kernel.OnStart(func(tc *kernel.TurnContext) {
        msgs, err := historyStore.LoadMessages(context.Background(), sessionID, 20)
        if err != nil || len(msgs) == 0 {
            return
        }
        // Insert history before the current user message (last element).
        current := tc.AgentCtx.Messages
        if len(current) > 0 {
            head := current[:len(current)-1]
            tail := current[len(current)-1:]
            tc.AgentCtx.Messages = append(append(head, msgs...), tail...)
        }
    }),

    // OnFinish: persist the new user/assistant turn.
    kernel.OnFinish(func(tc *kernel.TurnContext) {
        if tc.Result == nil || tc.Result.Text == "" {
            return
        }
        newMsgs := []kernel.Message{
            kernel.UserMsg(tc.Input),
            kernel.AssistantMsg(tc.Result.Text),
        }
        _ = historyStore.SaveMessages(context.Background(), sessionID, newMsgs)
    }),
)
```

This pattern is demonstrated in the restaurant bot example
(`examples/07-restaurant-bot/agent.go`), which also combines history loading
with a guard check in the same `OnStart` hook.

---

## 2. MemoryStore

`MemoryStore` manages long-term knowledge fragments about a user, persisted
across sessions.

```go
type MemoryStore interface {
    Save(ctx context.Context, userID string, memories []Memory) error
    Get(ctx context.Context, userID string) ([]Memory, error)
    Search(ctx context.Context, userID string, query string, topK int) ([]Memory, error)
    Delete(ctx context.Context, userID string, ids []string) error
}
```

Each fragment is a `Memory`:

```go
type Memory struct {
    ID        string    `json:"id"`
    Content   string    `json:"content"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

- `Save` persists one or more memories for a user. If a `Memory` has an empty
  `ID`, the implementation must auto-generate one.
- `Get` returns all memories for a user.
- `Search` returns up to `topK` memories matching `query` for a user. The
  matching strategy is implementation-defined (substring for the in-memory
  store, vector similarity for embedder-backed stores).
- `Delete` removes memories by ID. Unknown IDs are silently ignored.

### In-memory implementation

```go
import "github.com/axonframework/axon/interfaces/inmemory"

store := inmemory.NewMemoryStore()
```

`inmemory.NewMemoryStore` returns a thread-safe store backed by an in-memory
map. `Search` performs case-insensitive substring matching on `Memory.Content`.
`Save` auto-generates IDs in the format `mem-<counter>` and sets `CreatedAt`
and `UpdatedAt` to the current time when they are zero.

### Use case: cross-session preferences

Store user preferences or facts learned during a conversation and recall them
in future sessions:

```go
// After learning a preference:
err := memStore.Save(ctx, userID, []interfaces.Memory{
    {Content: "prefers outdoor seating"},
    {Content: "dietary restriction: vegetarian"},
})

// At the start of the next session, recall relevant context:
results, err := memStore.Search(ctx, userID, "seating", 5)
for _, m := range results {
    fmt.Println(m.Content)
}
```

---

## 3. Guard

`Guard` validates user input before it reaches the agent. Returning
`Allowed: false` lets the caller intercept the request and return a refusal
without invoking the model.

```go
type Guard interface {
    Check(ctx context.Context, input string) (GuardResult, error)
}

type GuardResult struct {
    Allowed bool   `json:"allowed"`
    Reason  string `json:"reason,omitempty"`
}
```

### NewBlocklistGuard

`NewBlocklistGuard` creates a `Guard` that rejects input containing any of the
provided phrases. Matching is case-insensitive substring matching. When
multiple phrases match, the one at the earliest position in the input causes
rejection.

```go
guard := interfaces.NewBlocklistGuard([]string{
    "ignore previous instructions",
    "jailbreak",
    "forget your instructions",
})

result, err := guard.Check(ctx, userInput)
if err != nil {
    // handle error
}
if !result.Allowed {
    fmt.Println("blocked:", result.Reason)
}
```

### Check flow

```
Guard.Check(ctx, "user input")
│
├─ Allowed: true  ──► agent proceeds normally
│                      (all tools available, normal prompt)
│
└─ Allowed: false ──► OnStart hook reacts:
   Reason: "blocked"   ├─ AgentCtx.DisableTools()
                        ├─ AgentCtx.SetSystemPrompt("I can't help with that.")
                        └─ Agent responds with safety message
```

### Integration pattern: OnStart hook

Wire the guard into an agent's `OnStart` hook. If the input is blocked, disable
tools so the model cannot take actions, and return early. The agent will still
respond, but without tool access.

```go
kernel.OnStart(func(tc *kernel.TurnContext) {
    result, err := guard.Check(context.Background(), tc.Input)
    if err != nil {
        logger.Error("guard check error", "error", err)
        return
    }
    if !result.Allowed {
        logger.Warn("input blocked by guard", "reason", result.Reason)
        tc.AgentCtx.DisableTools("search_restaurants", "make_reservation")
        return
    }

    // Guard passed — continue with history loading or other setup.
}),
```

The restaurant bot (`examples/07-restaurant-bot/agent.go`) demonstrates this
combined guard-then-history pattern in a single `OnStart` hook.

---

## 4. Embedder

`Embedder` generates vector embeddings for text. The interface is
batch-oriented: it accepts a slice of strings and returns one embedding vector
per input text.

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
}
```

`Embedder` is used by vector-search `MemoryStore` implementations that need to
convert query strings and memory content into vectors for similarity search.
The in-memory `MemoryStore` does not use an `Embedder`; you would provide one
when implementing a store backed by a vector database.

---

## 5. Implementing Your Own

For production workloads you will typically replace the in-memory
implementations with stores backed by a real database. The interface is small
and straightforward to implement. Here is a skeleton for a Redis-backed
`HistoryStore`:

```go
package redisstore

import (
    "context"
    "encoding/json"

    "github.com/axonframework/axon/interfaces"
    "github.com/axonframework/axon/kernel"
    "github.com/redis/go-redis/v9"
)

type historyStore struct {
    client *redis.Client
}

// NewHistoryStore creates a Redis-backed HistoryStore.
func NewHistoryStore(client *redis.Client) interfaces.HistoryStore {
    return &historyStore{client: client}
}

func (s *historyStore) SaveMessages(ctx context.Context, sessionID string, messages []kernel.Message) error {
    for _, msg := range messages {
        b, err := json.Marshal(msg)
        if err != nil {
            return err
        }
        if err := s.client.RPush(ctx, sessionID, b).Err(); err != nil {
            return err
        }
    }
    return nil
}

func (s *historyStore) LoadMessages(ctx context.Context, sessionID string, limit int) ([]kernel.Message, error) {
    var start int64 = 0
    if limit > 0 {
        start = -int64(limit)
    }
    vals, err := s.client.LRange(ctx, sessionID, start, -1).Result()
    if err != nil {
        return nil, err
    }
    msgs := make([]kernel.Message, 0, len(vals))
    for _, v := range vals {
        var msg kernel.Message
        if err := json.Unmarshal([]byte(v), &msg); err != nil {
            return nil, err
        }
        msgs = append(msgs, msg)
    }
    return msgs, nil
}

func (s *historyStore) Clear(ctx context.Context, sessionID string) error {
    return s.client.Del(ctx, sessionID).Err()
}
```

The same pattern applies to `MemoryStore`, `Guard`, and `Embedder`: implement
the interface, inject the dependency via your agent configuration struct, and
the agent code remains unchanged.
