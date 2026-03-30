# contrib/mongo — MongoDB Storage

`contrib/mongo` provides MongoDB-backed implementations of `interfaces.HistoryStore`
and `interfaces.MemoryStore`. It lives in its own module so the MongoDB driver
(`go.mongodb.org/mongo-driver/v2`) is not pulled into your dependency graph
unless you import this package.

**Import:** `github.com/axonframework/axon/contrib/mongo`

---

## HistoryStore

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

---

## MemoryStore

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

## Setup example

Connect to MongoDB, create both stores, and wire them into an agent via hooks.

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
