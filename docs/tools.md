# Tools

A `Tool` is an action the LLM can invoke during a turn. When the model decides to call
a tool, the agent executes it, serializes the result, and feeds it back into the
conversation before the next round.

---

## 1. Defining Tools

Use `kernel.NewTool` to create a tool from a typed parameter struct and a handler
function. The two type parameters are:

- `P` — the parameter type (a Go struct). Its fields become the JSON Schema the LLM
  uses to construct the call.
- `R` — the return type. Any JSON-serializable value works, including `Guided[T]`
  (see section 3).

```go
func NewTool[P any, R any](
    name        string,
    description string,
    fn          func(ctx context.Context, params P) (R, error),
) Tool
```

`NewTool` automatically:

1. Generates a JSON Schema from `P`'s struct tags.
2. Deserializes the LLM's JSON payload into a `P` value before calling `fn`.
3. Serializes the returned `R` value into the string the LLM receives as the tool
   result.

### Complete example

```go
type SearchParams struct {
    Query    string `json:"query"    description:"Search terms to look up"`
    MaxItems int    `json:"max_items" description:"Maximum number of results to return" minimum:"1" maximum:"50"`
}

type SearchResult struct {
    Title string `json:"title"`
    URL   string `json:"url"`
}

searchTool := kernel.NewTool[SearchParams, []SearchResult](
    "search",
    "Search the web and return a list of matching pages.",
    func(ctx context.Context, p SearchParams) ([]SearchResult, error) {
        return mySearchClient.Search(ctx, p.Query, p.MaxItems)
    },
)
```

Register the tool with an agent via `kernel.WithTools`:

```go
agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithTools(searchTool),
)
```

---

## 2. Schema Generation

`kernel.SchemaFrom[T]()` inspects a Go struct and produces a `kernel.Schema` — the
JSON Schema subset Axon sends to the LLM. `NewTool` calls this automatically, but you
can call it directly to inspect what gets generated.

```go
schema := kernel.SchemaFrom[SearchParams]()
```

### Struct tags

| Tag | JSON Schema field | Notes |
|---|---|---|
| `json:"name"` | property key | Overrides the field name in the schema |
| `description:"..."` | `description` | Human-readable hint for the LLM |
| `required:"false"` | removes from `required` | All fields are required by default |
| `enum:"a,b,c"` | `enum` | Comma-separated list of allowed values |
| `minimum:"n"` | `minimum` | Numeric lower bound (integers and floats) |
| `maximum:"n"` | `maximum` | Numeric upper bound (integers and floats) |

```
Go struct                          JSON Schema
─────────────────                  ─────────────────
type SearchParams struct {    ──►  {
  Query string                       "type": "object",
    `json:"query"             ──►    "properties": {
     description:"search text" ──►     "query": {
     required:"true"`                    "type": "string",
                                         "description": "search text"
  Limit int                            },
    `json:"limit,omitempty"   ──►      "limit": {
     description:"max results" ──►       "type": "integer",
     required:"false"                    "description": "max results",
     minimum:"1"              ──►        "minimum": 1,
     maximum:"50"`            ──►        "maximum": 50
}                                      }
                                     },
                              ──►    "required": ["query"]
                                   }
```

### Required vs optional fields

Every exported struct field is required unless you add `required:"false"`:

```go
type QueryParams struct {
    Query  string `json:"query"  description:"The search query (required)"`
    Limit  int    `json:"limit"  description:"Max results"   required:"false"`
    Offset int    `json:"offset" description:"Pagination offset" required:"false"`
}
```

### Enum constraints

```go
type SortParams struct {
    Field     string `json:"field"     description:"Field to sort by"`
    Direction string `json:"direction" description:"Sort direction" enum:"asc,desc"`
}
```

### Numeric bounds

```go
type ReservationParams struct {
    PartySize int    `json:"party_size" description:"Number of guests" minimum:"1" maximum:"20"`
    Time      string `json:"time"       description:"Reservation time, e.g. '7:00 PM'"`
}
```

### Nested structs

Nested structs expand inline — each nested field gets its own schema entry:

```go
type DateRange struct {
    Start string `json:"start" description:"Start date in YYYY-MM-DD format"`
    End   string `json:"end"   description:"End date in YYYY-MM-DD format"`
}

type EventParams struct {
    Name  string    `json:"name"  description:"Event name to search for"`
    Range DateRange `json:"range" description:"Date range to search within"`
}
```

### Array / slice fields

Slice fields produce an `array` schema whose `items` type is derived from the slice's
element type:

```go
type BatchParams struct {
    IDs []string `json:"ids" description:"List of record IDs to fetch"`
}
```

---

## 3. Guided Responses

`Guided[T]` wraps a tool result with a short instruction for the LLM. It lets the tool
author shape how the model presents or acts on the data, without coupling that logic to
the agent's system prompt.

```go
type Guided[T any] struct {
    Data     T
    Guidance string
}
```

Use `kernel.Guide` to construct one:

```go
func Guide[T any](data T, format string, args ...any) Guided[T]
```

`Guide` works like `fmt.Sprintf` — the `format` and `args` follow the same rules.

### When to use

Use `Guided[T]` when the tool result requires context-specific presentation or follow-up
action that a static system prompt cannot anticipate. For example:

- Telling the model how to display a list of search results.
- Flagging an edge case that requires a different response than the normal path.
- Providing next-step suggestions that depend on the data returned.

For straightforward results where no additional guidance is needed, return a plain value.

### Example: consistent result presentation

```go
type Restaurant struct {
    Name    string  `json:"name"`
    Cuisine string  `json:"cuisine"`
    Rating  float64 `json:"rating"`
    Price   string  `json:"price"`
}

searchTool := kernel.NewTool[SearchRestaurantsParams, kernel.Guided[[]Restaurant]](
    "search_restaurants",
    "Search for restaurants by cuisine type or name.",
    func(_ context.Context, p SearchRestaurantsParams) (kernel.Guided[[]Restaurant], error) {
        results := findRestaurants(p.Query, p.Location)
        return kernel.Guide(
            results,
            "Present these restaurants in a friendly, readable list. "+
                "Highlight ratings and price range ($ = budget, $$$$ = fine dining). "+
                "Offer to get the menu or make a reservation for any of them.",
        ), nil
    },
)
```

### Example: guidance that varies by result

The restaurant bot's `make_reservation` tool returns different guidance for large
parties vs. standard bookings:

```go
reservationTool := kernel.NewTool[MakeReservationParams, kernel.Guided[Reservation]](
    "make_reservation",
    "Make a restaurant reservation. Returns a confirmation code.",
    func(_ context.Context, p MakeReservationParams) (kernel.Guided[Reservation], error) {
        res := bookReservation(p)

        if p.PartySize > 10 {
            return kernel.Guide(
                res,
                "This is a large party reservation (more than 10 guests). "+
                    "Inform the user their reservation is tentatively confirmed with code %s, "+
                    "but strongly advise them to call the restaurant directly to confirm "+
                    "special seating arrangements and deposit requirements.",
                res.Confirmation,
            ), nil
        }

        return kernel.Guide(
            res,
            "Reservation confirmed! Share the confirmation code %s with the user "+
                "and remind them to arrive a few minutes early.",
            res.Confirmation,
        ), nil
    },
)
```

### How SerializeToolResult handles Guided

`kernel.SerializeToolResult` is called internally by the agent after every tool
execution. For a `Guided[T]` result it produces:

```
<data JSON>

<guidance text>
```

For a plain result it produces only the JSON. You do not call `SerializeToolResult`
directly in normal usage; it runs automatically as part of the tool execution model.

```
Tool returns Guided[[]Restaurant]
│
├─ Data: [{name: "Pizza Palace", rating: 4.5}, ...]
│
├─ Guidance: "Found 3 restaurants. Ask user to pick one."
│
└─ SerializeToolResult combines them:
   ┌────────────────────────────────────────────┐
   │ [{"name":"Pizza Palace","rating":4.5},...] │ ← data (JSON)
   │                                            │
   │ ---                                        │
   │ Found 3 restaurants. Ask user to pick one. │ ← guidance
   └────────────────────────────────────────────┘
   This full text becomes ToolResult.Content — what the LLM reads.
```

---

## 4. Tool Execution Model

**Tools execute synchronously within a round.** When the LLM requests one or more tool
calls in a single response, the agent executes all of them in parallel, waits for every
result, and then appends the full set of results to the conversation before calling the
LLM again.

Key properties:

- **Parallel execution** — multiple tool calls in one round run concurrently. Write
  tool handlers to be safe for concurrent use (avoid shared mutable state).
- **Automatic serialization** — return values are converted to strings via
  `SerializeToolResult`. JSON-serializable types work out of the box.
- **Error propagation** — if a tool returns a non-nil error, the agent records
  `IsError=true` on the `ToolResult` and sends the error message back to the LLM so
  it can react (retry with different parameters, explain the failure, etc.).
- **Context threading** — the `context.Context` passed to `agent.Run` flows through to
  every tool handler. Use it for cancellation and deadline propagation.

```go
// Tool handlers receive the same context as agent.Run.
myTool := kernel.NewTool[MyParams, MyResult](
    "my_tool",
    "Does something useful.",
    func(ctx context.Context, p MyParams) (MyResult, error) {
        // ctx carries the deadline/cancellation from agent.Run.
        return callExternalService(ctx, p.Query)
    },
)
```

```
Agent Round (tool call)
│
├─ LLM returns ToolCall{Name: "search", Params: {"query": "pizza"}}
│
├─ Agent finds tool "search" by name
│
├─ NewTool deserializes JSON params into SearchParams struct
│
├─ fn(ctx, SearchParams{Query: "pizza"}) executes
│  └─ returns ([]Restaurant, nil)
│
├─ SerializeToolResult([]Restaurant) → JSON string
│
├─ Appended to messages as ToolResult{Content: "[{...}]"}
│
└─ Next round: LLM sees the tool result and responds
```

---

## 5. The Tool Interface

For advanced cases where `NewTool`'s generic constructor is not flexible enough,
implement `kernel.Tool` directly:

```go
type Tool interface {
    Name() string
    Description() string
    Schema() Schema
    Execute(ctx context.Context, params json.RawMessage) (any, error)
}
```

| Method | Purpose |
|---|---|
| `Name()` | The identifier the LLM uses to call this tool |
| `Description()` | Natural-language description of what the tool does |
| `Schema()` | JSON Schema for the tool's parameters |
| `Execute()` | The handler; receives raw JSON params, returns any serializable value |

### When to implement the interface directly

Use the `Tool` interface instead of `NewTool` when you need to:

- Build a schema programmatically rather than from a fixed struct (e.g., the set of
  properties is determined at runtime).
- Handle raw `json.RawMessage` params directly without deserialization.
- Wrap an existing object that already carries its own name and description.
- Generate multiple tools from a template or factory pattern.

### Example

```go
type DynamicTool struct {
    name        string
    description string
    schema      kernel.Schema
    handler     func(ctx context.Context, raw json.RawMessage) (any, error)
}

func (d *DynamicTool) Name() string             { return d.name }
func (d *DynamicTool) Description() string      { return d.description }
func (d *DynamicTool) Schema() kernel.Schema    { return d.schema }
func (d *DynamicTool) Execute(ctx context.Context, params json.RawMessage) (any, error) {
    return d.handler(ctx, params)
}
```

For the vast majority of tools, `NewTool` is simpler and less error-prone. Prefer it
unless you have a specific reason to implement the interface.
