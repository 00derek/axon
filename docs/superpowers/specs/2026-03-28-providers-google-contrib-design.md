# Axon — Google Provider & Contrib Packages Design Spec

**Date:** 2026-03-28
**Author:** Derek + Claude
**Status:** Draft
**Origin:** Extension of the core Axon framework spec. Covers the Google LLM provider, contrib/plan (multi-step procedures), and contrib/mongo (MongoDB storage backends).

---

## 1. Google Provider (`providers/google/`)

### 1.1 Overview

Adapter implementing `kernel.LLM` using `google.golang.org/genai` SDK for Gemini models. Supports both Google AI Studio (API key) and Vertex AI (GCP auth) through the unified genai client.

**Module:** `github.com/axonframework/axon/providers/google` — separate `go.mod`, imports genai SDK + kernel.

### 1.2 Public API

```go
package google

import (
    "google.golang.org/genai"
    "github.com/axonframework/axon/kernel"
)

// GoogleLLM implements kernel.LLM for Google Gemini models.
type GoogleLLM struct { /* unexported */ }

// New creates a GoogleLLM using the given genai client and model name.
func New(client *genai.Client, model string, opts ...Option) *GoogleLLM

// Option configures a GoogleLLM at construction time.
type Option func(*GoogleLLM)

func WithSafetySettings(settings []*genai.SafetySetting) Option
func WithThinkingBudget(tokens int32) Option
func WithCachedContent(name string) Option
```

### 1.3 Translation Layer

The provider translates between Axon kernel types and the genai SDK types.

**Message mapping (`kernel.Message` -> `genai.Content`):**

| Axon | Google genai |
|------|-------------|
| `RoleSystem` | Extracted, set as `GenerateContentConfig.SystemInstruction` |
| `RoleUser` | `Content{Role: "user", Parts: [...]}` |
| `RoleAssistant` | `Content{Role: "model", Parts: [...]}` |
| `RoleTool` | `Content{Role: "tool", Parts: [FunctionResponse{...}]}` |
| `ContentPart.Text` | `genai.Text("...")` |
| `ContentPart.Image` | `genai.FileData{URI: url, MIMEType: mime}` or inline blob |
| `ContentPart.ToolCall` | `genai.FunctionCall{Name, Args}` |
| `ContentPart.ToolResult` | `genai.FunctionResponse{Name, Response}` |

**Tool mapping (`kernel.Tool` -> `genai.Tool`):**

Each `kernel.Tool` becomes a `genai.FunctionDeclaration` inside a single `genai.Tool`:

```
kernel.Tool.Name()        -> FunctionDeclaration.Name
kernel.Tool.Description() -> FunctionDeclaration.Description
kernel.Tool.Schema()      -> FunctionDeclaration.Parameters (Schema -> genai.Schema)
```

Schema translation (`kernel.Schema` -> `*genai.Schema`):

| Axon Schema field | genai.Schema field |
|---|---|
| Type | Type (mapped: "object"->Object, "string"->String, etc.) |
| Description | Description |
| Properties | Properties (recursive) |
| Required | Required |
| Items | Items |
| Enum | Enum |
| Minimum | Minimum |
| Maximum | Maximum |

**ToolChoice mapping (`kernel.ToolChoice` -> `genai.ToolConfig`):**

| Axon | Google |
|------|--------|
| `ToolChoiceAuto` | `FunctionCallingConfigAuto` |
| `ToolChoiceRequired` | `FunctionCallingConfigAny` |
| `ToolChoiceNone` | `FunctionCallingConfigNone` |
| `ToolChoiceForce("name")` | `FunctionCallingConfigAny` + `AllowedFunctionNames: ["name"]` |

**GenerateOptions mapping:**

| Axon | Google |
|------|--------|
| `Temperature` | `GenerateContentConfig.Temperature` |
| `MaxTokens` | `GenerateContentConfig.MaxOutputTokens` |
| `StopSequences` | `GenerateContentConfig.StopSequences` |
| `OutputSchema` | `GenerateContentConfig.ResponseSchema` + `ResponseMIMEType: "application/json"` |
| `ReasoningLevel` | `GenerateContentConfig.ThinkingConfig.ThinkingBudget` (map "low"->1024, "medium"->4096, "high"->16384, or pass numeric) |

**Response mapping (`genai.GenerateContentResponse` -> `kernel.Response`):**

- `Text`: concatenate all `genai.Text` parts from the first candidate
- `ToolCalls`: extract `genai.FunctionCall` parts, map to `kernel.ToolCall` (generate call ID if Google doesn't provide one)
- `Usage`: map `UsageMetadata` (PromptTokenCount->InputTokens, CandidatesTokenCount->OutputTokens, TotalTokenCount->TotalTokens)
- `FinishReason`: map `genai.FinishReason` to string ("stop", "tool_calls", "max_tokens", "safety")

### 1.4 Streaming

`GenerateStream` uses `client.Models.GenerateContentStream()` which returns an iterator. The provider wraps this into a `kernel.Stream` implementation:

- Each chunk's text parts emit `TextDeltaEvent` to `Events()` and text to `Text()`
- Tool calls in streaming chunks are accumulated and reported as complete `ToolCall`s in the final `Response()`
- The stream accumulates all chunks to build the final `Response` (text, usage, finish reason)
- `Err()` captures any iterator/API error

### 1.5 What it does NOT do

- No retry logic (use `middleware.WithRetry`)
- No cost tracking (use `middleware.WithCostTracker`)
- No caching layer
- No provider-specific tool execution
- No per-call option overrides (constructor-time only)

---

## 2. Contrib/Plan (`contrib/plan/`)

### 2.1 Overview

Structured multi-step procedure tracking for agents that need to follow long flows (5+ steps). The plan is injected into the LLM context each round as a system message. The LLM follows the plan and advances steps via a built-in `mark_step` tool.

Most agents don't need this. Simple 1-3 round tool interactions work without Plan. Use Plan for long procedural flows where the LLM might lose track.

**Module:** `github.com/axonframework/axon/contrib/plan` — separate `go.mod`, imports kernel only.

### 2.2 Types

```go
package plan

type Plan struct {
    Name   string
    Goal   string
    Steps  []Step
    Notes  map[string]any
}

type Step struct {
    Name           string
    Description    string
    Status         StepStatus
    NeedsUserInput bool
    CanRepeat      bool
}

type StepStatus string

const (
    StatusPending  StepStatus = "pending"
    StatusActive   StepStatus = "active"
    StatusDone     StepStatus = "done"
    StatusSkipped  StepStatus = "skipped"
)
```

### 2.3 Public API

```go
// New creates a Plan with the given steps (all start as pending).
func New(name, goal string, steps ...Step) *Plan

// Attach returns AgentOptions that wire the plan into an agent via hooks and tools.
// Spread into NewAgent: plan.Attach(p)...
func Attach(p *Plan) []kernel.AgentOption
```

### 2.4 How Attach Works

`Attach(p)` returns a slice of `kernel.AgentOption`:

1. **`OnStart` hook** — sets the first pending step to `StatusActive`.

2. **`PrepareRound` hook** — injects the plan's current state as an appended system message each round. The message is formatted as structured text:

```
## Current Plan: {Name}
Goal: {Goal}

[✓] step_name — Description
[>] active_step — Description
[ ] pending_step — Description (needs user input)
[-] skipped_step — Description

Notes:
- key: value
```

3. **`mark_step` tool** — registered automatically. Parameters:
   - `step_name` (string, required): name of the step to update
   - `status` (string, required, enum: "done", "skipped", "active"): new status

   The tool updates `Plan.Steps` in place. When a step is marked `done` or `skipped`, the next pending step is automatically set to `active`. Returns confirmation text like `"Step 'search_restaurants' marked as done. Next: 'present_options'"`. If the step name is not found, returns an error string (not a Go error — the tool result is marked `IsError: true` so the LLM can self-correct).

4. **`add_note` tool** — registered automatically. Parameters:
   - `key` (string, required): note key
   - `value` (string, required): note value

   Stores intermediate data in `Plan.Notes` for reference in later steps. Returns confirmation.

### 2.5 Usage Examples

**Basic usage:**

```go
p := plan.New("Restaurant Booking", "Help user find and book a restaurant",
    plan.Step{Name: "gather_preferences", Description: "Ask about cuisine, location, party size, date/time"},
    plan.Step{Name: "search_restaurants", Description: "Search for matching restaurants"},
    plan.Step{Name: "present_options", Description: "Show top 3 options and let user pick"},
    plan.Step{Name: "confirm_booking", Description: "Confirm reservation details with user", NeedsUserInput: true},
    plan.Step{Name: "make_reservation", Description: "Call reservation API"},
    plan.Step{Name: "send_confirmation", Description: "Send booking confirmation to user"},
)

agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithSystemPrompt("You are a restaurant booking assistant."),
    kernel.WithTools(searchTool, reserveTool, confirmTool),
    plan.Attach(p)...,
)

result, err := agent.Run(ctx, "I want to book a nice Italian place for 4 people this Friday")
```

**What the LLM sees each round (injected by PrepareRound):**

```
## Current Plan: Restaurant Booking
Goal: Help user find and book a restaurant

[✓] gather_preferences — Ask about cuisine, location, party size, date/time
[>] search_restaurants — Search for matching restaurants
[ ] present_options — Show top 3 options and let user pick
[ ] confirm_booking — Confirm reservation details with user (needs user input)
[ ] make_reservation — Call reservation API
[ ] send_confirmation — Send booking confirmation to user

Notes:
- cuisine: Italian
- party_size: 4
- date: this Friday
```

**Resumability (user-managed persistence):**

```go
// Save plan state after a turn
agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithTools(myTools...),
    plan.Attach(p)...,
    kernel.OnFinish(func(ctx *kernel.TurnContext) {
        data, _ := json.Marshal(p) // Plan is a plain struct, serializes cleanly
        store.Save(sessionID, data)
    }),
)

// Restore plan state in a later session
data, _ := store.Load(sessionID)
var p plan.Plan
json.Unmarshal(data, &p)
agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithTools(myTools...),
    plan.Attach(&p)...,
)
```

### 2.6 What it does NOT do

- No LLM-driven plan generation (user defines steps)
- No built-in persistence (user serializes Plan struct)
- No concurrent step execution (steps are sequential)
- No automatic step advancement without LLM calling `mark_step`

---

## 3. Contrib/Mongo (`contrib/mongo/`)

### 3.1 Overview

MongoDB-backed implementations of `interfaces.HistoryStore` and `interfaces.MemoryStore`. MemoryStore uses Atlas Vector Search for semantic memory retrieval.

**Module:** `github.com/axonframework/axon/contrib/mongo` — separate `go.mod`, imports mongo driver + kernel + interfaces.

### 3.2 New Interface: Embedder

Added to the `interfaces/` package (not contrib/mongo) since embeddings are a general AI primitive.

```go
// interfaces/embedder.go
package interfaces

import "context"

// Embedder generates vector embeddings for text.
// Batch-oriented: accepts multiple texts and returns one embedding per text.
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
}
```

### 3.3 Public API

```go
package mongo

import (
    "go.mongodb.org/mongo-driver/v2/mongo"
    "github.com/axonframework/axon/interfaces"
)

// NewHistoryStore creates a MongoDB-backed HistoryStore.
func NewHistoryStore(db *mongo.Database, collection string) interfaces.HistoryStore

// NewMemoryStore creates a MongoDB-backed MemoryStore with vector search.
// Requires an Atlas Search index on the embedding field (see documentation).
func NewMemoryStore(db *mongo.Database, collection string, embedder interfaces.Embedder) interfaces.MemoryStore
```

### 3.4 HistoryStore Collection Schema

Collection name: user-provided (e.g., `"conversation_history"`).

Document structure:
```json
{
    "_id": "<auto>",
    "session_id": "string",
    "messages": [
        {
            "role": "user",
            "content": [{"text": "Hello"}],
            "metadata": {}
        }
    ],
    "updated_at": "datetime"
}
```

- **One document per session** — messages array grows via `$push`
- **SaveMessages**: `UpdateOne` with `$push: {messages: {$each: newMsgs}}` and `$set: {updated_at: now}`, upsert=true
- **LoadMessages** with limit: `FindOne` with `$slice: -N` projection on messages array. Limit <= 0 returns all.
- **Clear**: `DeleteOne` by session_id

**Required index:** `{session_id: 1}` (unique)

### 3.5 MemoryStore Collection Schema

Collection name: user-provided (e.g., `"memories"`).

Document structure:
```json
{
    "_id": "<auto>",
    "user_id": "string",
    "memory_id": "string",
    "content": "string",
    "embedding": [0.1, 0.2, ...],
    "created_at": "datetime",
    "updated_at": "datetime"
}
```

- **One document per memory**
- **Save**: calls `embedder.Embed` for all memories in batch, then `BulkWrite` with upserts keyed on `(user_id, memory_id)`. Auto-generates memory_id if empty.
- **Get**: `Find` by user_id, return all memories (without embeddings — projection excludes embedding field)
- **Search**: `$vectorSearch` aggregation using Atlas Vector Search index, filtered by user_id, limited to topK
- **Delete**: `DeleteMany` with `user_id` + `memory_id: {$in: ids}`

**Required indexes:**
- `{user_id: 1, memory_id: 1}` (unique compound)
- Atlas Vector Search index on `embedding` field (user must create via Atlas UI/API)

### 3.6 Atlas Vector Search Index Definition

Users must create this index manually. Document in package README:

```json
{
    "type": "vectorSearch",
    "definition": {
        "fields": [
            {
                "type": "vector",
                "path": "embedding",
                "numDimensions": 768,
                "similarity": "cosine"
            },
            {
                "type": "filter",
                "path": "user_id"
            }
        ]
    }
}
```

Note: `numDimensions` depends on the embedding model. 768 is for `text-embedding-004`. The user adjusts this to match their embedder.

### 3.7 What it does NOT do

- No automatic index creation
- No TTL/expiry on messages
- No transactions
- No connection pooling (user provides `*mongo.Database`)
- No reference Embedder implementation (user provides their own)
- No migration tooling

---

## 4. Package Structure (additions to framework)

```
axon/
├── kernel/              # (existing, no changes)
├── workflow/            # (existing, no changes)
├── testing/             # (existing, no changes)
├── middleware/           # (existing, no changes)
├── interfaces/          # (existing + new embedder.go)
│   ├── embedder.go      # NEW: Embedder interface
│   └── ...
│
├── providers/
│   └── google/          # NEW
│       ├── go.mod
│       ├── google.go    # GoogleLLM, New, Options
│       ├── convert.go   # Message/Tool/Schema translation
│       ├── stream.go    # Stream implementation
│       └── *_test.go
│
└── contrib/
    ├── plan/            # NEW
    │   ├── go.mod
    │   ├── plan.go      # Plan, Step, StepStatus, New
    │   ├── attach.go    # Attach, hooks, mark_step tool, add_note tool
    │   ├── format.go    # Plan -> system message formatting
    │   └── *_test.go
    │
    └── mongo/           # NEW
        ├── go.mod
        ├── history.go   # MongoDB HistoryStore
        ├── memory.go    # MongoDB MemoryStore with vector search
        └── *_test.go
```

## 5. Dependency Rules

- `providers/google` depends on: `kernel/`, `google.golang.org/genai`
- `contrib/plan` depends on: `kernel/` only
- `contrib/mongo` depends on: `kernel/`, `interfaces/`, `go.mongodb.org/mongo-driver/v2`
- `interfaces/embedder.go` depends on: nothing (stdlib only, same as existing interfaces)
