# Contrib Packages

Contrib packages are optional extensions to Axon that live in separate Go
modules under the `contrib/` directory. Each package has its own `go.mod`,
so importing one does not pull its dependencies into your main module. Take
only what you need.

| Package | Module | Purpose |
|---|---|---|
| `contrib/plan` | `github.com/axonframework/axon/contrib/plan` | Multi-step procedures driven by the LLM |
| `contrib/mongo` | `github.com/axonframework/axon/contrib/mongo` | MongoDB-backed `HistoryStore` and `MemoryStore` |

---

## 1. contrib/plan — Multi-Step Procedures

### When to use

Use `contrib/plan` when the agent must follow a **fixed sequence of steps**
that the user or the system defines up front:

- Flows with five or more distinct phases (gather info → validate → confirm → execute → summarize)
- Procedures that must be resumable if the conversation is interrupted
- Workflows where the LLM must record intermediate results for use in later steps
- Any scenario where you need the current progress to be visible to the model at every turn

**Skip it** for simple 1–3 round interactions. If the agent just needs to call
a tool and return an answer, a plain `kernel.NewAgent` with `kernel.WithTools`
is enough.

---

### Creating a plan

```go
import "github.com/axonframework/axon/contrib/plan"

p := plan.New(
    "hotel-booking",
    "Help the user find and book a hotel room",
    plan.Step{
        Name:           "gather_preferences",
        Description:    "Ask for destination, dates, and budget",
        NeedsUserInput: true,
    },
    plan.Step{
        Name:        "search_hotels",
        Description: "Search available hotels matching the preferences",
    },
    plan.Step{
        Name:           "confirm_selection",
        Description:    "Present options and confirm which hotel the user wants",
        NeedsUserInput: true,
    },
    plan.Step{
        Name:        "make_booking",
        Description: "Submit the reservation and return a confirmation number",
    },
    plan.Step{
        Name:        "send_summary",
        Description: "Send a confirmation email with booking details",
    },
)
```

`plan.New(name, goal, steps...)` initializes all steps to `StatusPending` and
sets `Notes` to an empty map.

#### Step fields

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Unique identifier for the step; used by the `mark_step` tool |
| `Description` | `string` | What the LLM must do at this step |
| `Status` | `StepStatus` | `pending` / `active` / `done` / `skipped` |
| `NeedsUserInput` | `bool` | Hints to the LLM that it should wait for the user before advancing |
| `CanRepeat` | `bool` | Hints to the LLM that this step may be revisited |

---

### Attaching a plan to an agent

```go
import (
    "github.com/axonframework/axon/contrib/plan"
    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/providers/anthropic"
)

llm := anthropic.New("claude-3-5-haiku-20241022")

baseOpts := []kernel.AgentOption{
    kernel.WithModel(llm),
    kernel.WithSystemPrompt("You are a hotel booking assistant."),
    kernel.WithTools(searchHotels, makeReservation, sendEmail),
}

agent := kernel.NewAgent(append(baseOpts, plan.Attach(p)...)...)
```

`plan.Attach(p)` returns a `[]kernel.AgentOption` slice. Spread it into
`kernel.NewAgent` alongside your other options.

---

### How it works

`plan.Attach` wires three things into the agent:

1. **`OnStart` hook** — captures the existing system prompt and activates the
   first pending step.
2. **`PrepareRound` hook** — before every LLM call, calls `plan.Format(p)` and
   appends the rendered plan to the system prompt. The plan text is replaced
   in-place each round so only one up-to-date copy exists in the conversation.
3. **Two tools** — `mark_step` and `add_note` (described below).

The LLM reads the plan in every system prompt and uses it to decide which
action to take next. The active step (`[>]` marker) tells the model what it
should be doing right now.

---

### mark_step tool

The LLM calls `mark_step` to record progress on a step.

```
Parameters:
  step_name  — name of the step to update
  status     — "done", "skipped", or "active"
```

When a step is marked `done` or `skipped`, `mark_step` **automatically
activates the next pending step** in the list. The tool returns a message
confirming the transition, e.g. `"Step 'search_hotels' marked as done. Next: 'confirm_selection'"`.

When all steps are complete, the return message says `"All steps complete."`.

---

### add_note tool

The LLM calls `add_note` to store a key-value pair in `plan.Notes`. Notes
persist across rounds and are visible in the formatted plan, so the model can
reference data collected in earlier steps.

```
Parameters:
  key    — note key (e.g. "destination")
  value  — note value (e.g. "Paris, France")
```

Access notes directly in Go if you need them after the agent run:

```go
destination := p.Notes["destination"]
checkIn     := p.Notes["check_in"]
```

---

### plan.Format

`plan.Format(p)` renders the plan as structured text. You normally do not need
to call this directly — `plan.Attach` calls it automatically before each round.
It is useful for logging or debugging.

```go
fmt.Println(plan.Format(p))
```

Example output:

```
## Current Plan: hotel-booking
Goal: Help the user find and book a hotel room

[✓] gather_preferences — Ask for destination, dates, and budget (needs user input)
[>] search_hotels — Search available hotels matching the preferences
[ ] confirm_selection — Present options and confirm which hotel the user wants (needs user input)
[ ] make_booking — Submit the reservation and return a confirmation number
[ ] send_summary — Send a confirmation email with booking details

Notes:
- budget: under $200/night
- destination: Paris, France
```

Status markers: `[✓]` done, `[>]` active, `[ ]` pending, `[-]` skipped.

---

### Full example: hotel booking procedure

```go
package main

import (
    "context"
    "fmt"

    "github.com/axonframework/axon/contrib/plan"
    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/providers/anthropic"
)

func main() {
    llm := anthropic.New("claude-3-5-haiku-20241022")

    p := plan.New(
        "hotel-booking",
        "Help the user find and book a hotel room",
        plan.Step{
            Name:           "gather_preferences",
            Description:    "Ask for destination, dates, and budget",
            NeedsUserInput: true,
        },
        plan.Step{
            Name:        "search_hotels",
            Description: "Search available hotels matching the preferences",
        },
        plan.Step{
            Name:           "confirm_selection",
            Description:    "Present the top results and ask the user to pick one",
            NeedsUserInput: true,
        },
        plan.Step{
            Name:        "make_booking",
            Description: "Submit the reservation and return the confirmation number",
        },
    )

    baseOpts := []kernel.AgentOption{
        kernel.WithModel(llm),
        kernel.WithSystemPrompt("You are a helpful hotel booking assistant."),
        kernel.WithTools(searchHotels, makeReservation),
    }

    agent := kernel.NewAgent(append(baseOpts, plan.Attach(p)...)...)

    result, err := agent.Run(context.Background(), "I need a hotel in Paris next weekend.")
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Text)

    // Inspect accumulated notes after the run.
    fmt.Printf("Destination: %v\n", p.Notes["destination"])
    fmt.Printf("Check-in: %v\n", p.Notes["check_in"])
}
```

---

## 2. contrib/mongo — MongoDB Storage

`contrib/mongo` provides MongoDB-backed implementations of `interfaces.HistoryStore`
and `interfaces.MemoryStore`. It lives in its own module
(`github.com/axonframework/axon/contrib/mongo`) so the MongoDB driver
(`go.mongodb.org/mongo-driver/v2`) is not pulled into your main framework
dependency graph unless you import this package.

### NewHistoryStore

```go
import (
    "go.mongodb.org/mongo-driver/v2/mongo"
    contribmongo "github.com/axonframework/axon/contrib/mongo"
)

historyStore := contribmongo.NewHistoryStore(db, "conversation_history")
```

`NewHistoryStore(db *mongo.Database, collection string) interfaces.HistoryStore`

Each conversation session is stored as a single document keyed on `session_id`.
New messages are appended via `$push` with upsert, so `SaveMessages` never
overwrites existing history. `LoadMessages` returns the last `limit` messages
using MongoDB's `$slice` projection; pass `limit <= 0` to retrieve all messages.

**Recommended index:**

```js
db.conversation_history.createIndex({ session_id: 1 }, { unique: true })
```

### NewMemoryStore

```go
import (
    "go.mongodb.org/mongo-driver/v2/mongo"
    contribmongo "github.com/axonframework/axon/contrib/mongo"
    "github.com/axonframework/axon/interfaces"
)

memoryStore := contribmongo.NewMemoryStore(db, "user_memories", embedder)
```

`NewMemoryStore(db *mongo.Database, collection string, embedder interfaces.Embedder) interfaces.MemoryStore`

Each memory is embedded via the provided `Embedder` and upserted using
`BulkWrite` keyed on `(user_id, memory_id)`. Memories with an empty `ID` get
an auto-generated identifier in the form `mem-<counter>`.

`Search` uses a MongoDB Atlas `$vectorSearch` aggregation pipeline to find the
`topK` most similar memories for a user. The collection must have:

- A unique compound index on `{user_id: 1, memory_id: 1}`
- An Atlas Vector Search index named `vector_index` on the `embedding` field

**Recommended indexes:**

```js
db.user_memories.createIndex({ user_id: 1, memory_id: 1 }, { unique: true })
// Atlas Vector Search index (created through the Atlas UI or API):
// { "fields": [{ "type": "vector", "path": "embedding", "numDimensions": 1536, "similarity": "cosine" }] }
```

---

### Setup example: connect, create stores, wire into agent

```go
package main

import (
    "context"
    "fmt"
    "log"

    "go.mongodb.org/mongo-driver/v2/mongo"
    "go.mongodb.org/mongo-driver/v2/mongo/options"

    contribmongo "github.com/axonframework/axon/contrib/mongo"
    "github.com/axonframework/axon/interfaces"
    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/providers/anthropic"
)

func main() {
    ctx := context.Background()

    // Connect to MongoDB.
    client, err := mongo.Connect(options.Client().ApplyURI("mongodb://localhost:27017"))
    if err != nil {
        log.Fatal(err)
    }
    defer client.Disconnect(ctx)

    db := client.Database("myapp")

    // Create stores.
    historyStore := contribmongo.NewHistoryStore(db, "conversation_history")
    memoryStore  := contribmongo.NewMemoryStore(db, "user_memories", myEmbedder)

    // Wire into agent hooks.
    sessionID := "user-session-123"
    userID    := "user-456"

    llm   := anthropic.New("claude-3-5-haiku-20241022")
    agent := kernel.NewAgent(
        kernel.WithModel(llm),
        kernel.WithSystemPrompt("You are a helpful assistant."),

        // Load conversation history before each turn.
        kernel.OnStart(func(tc *kernel.TurnContext) {
            msgs, err := historyStore.LoadMessages(ctx, sessionID, 20)
            if err != nil || len(msgs) == 0 {
                return
            }
            current := tc.AgentCtx.Messages
            if len(current) > 0 {
                head := current[:len(current)-1]
                tail := current[len(current)-1:]
                tc.AgentCtx.Messages = append(append(head, msgs...), tail...)
            }
        }),

        // Persist the completed turn after the model responds.
        kernel.OnFinish(func(tc *kernel.TurnContext) {
            if tc.Result == nil || tc.Result.Text == "" {
                return
            }
            newMsgs := []kernel.Message{
                kernel.UserMsg(tc.Input),
                kernel.AssistantMsg(tc.Result.Text),
            }
            _ = historyStore.SaveMessages(ctx, sessionID, newMsgs)
        }),
    )

    result, err := agent.Run(ctx, "What did we discuss last time?")
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result.Text)

    // Save a new memory.
    err = memoryStore.Save(ctx, userID, []interfaces.Memory{
        {Content: "prefers concise answers"},
    })
    if err != nil {
        log.Fatal(err)
    }

    // Recall relevant memories by semantic similarity.
    memories, err := memoryStore.Search(ctx, userID, "communication style", 5)
    if err != nil {
        log.Fatal(err)
    }
    for _, m := range memories {
        fmt.Println(m.Content)
    }
}
```
