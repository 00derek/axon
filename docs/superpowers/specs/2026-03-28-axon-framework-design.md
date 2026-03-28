# Axon — Agentic Framework Design Spec

**Date:** 2026-03-28
**Author:** Derek + Claude
**Status:** Draft
**Origin:** Comparative analysis of existing agentic frameworks (Vercel AI SDK, internal production frameworks), taking the best patterns while eliminating overengineering.

---

## 1. Overview

Axon is a new agentic framework for building production AI agents. It combines the developer ergonomics of modern AI SDKs with production-grade capabilities learned from building real-world agent systems, while eliminating the complexity of both.

**Name:** Axon — the long fibers that transmit signals between neurons. Connects, fast, carries information.

**Languages:** Go (primary), Rust (future port from same spec).

**Design principles:**

1. **Kernel is tiny.** ~6 types, ~10 files. Portable to Rust in a week.
2. **Everything else is optional.** Use `kernel/` alone or add workflow, middleware, interfaces as needed.
3. **No framework abstractions where plain functions work.** No StateSetter, no Component, no Scheduler. Just functions composed in workflows.
4. **Typed where it matters, flexible where it doesn't.** Tool params are schema-validated via generics. Messages are structured. KV bags are gone.
5. **One repo, separate Go modules.** `providers/openai` is its own `go.mod` so importing Axon doesn't pull in every provider SDK.

**What Axon is NOT:**

- Not a transport layer (no SSE, WebSocket, gRPC — that's the application's concern)
- Not a deployment framework (no YAML catalogs, no service discovery)
- Not an observability platform (use OpenTelemetry directly)

---

## 2. Package Structure

```
axon/
├── kernel/          # Agent, Tool, LLM, Message, AgentContext, Stream
│   ├── agent.go
│   ├── context.go
│   ├── tool.go
│   ├── llm.go
│   ├── message.go
│   └── stream.go
│
├── workflow/        # Step, Parallel, Router, RetryUntil
│   ├── workflow.go
│   └── state.go
│
├── testing/         # axontest.Run, assertions, ScoreCard, MockLLM
│   ├── run.go
│   ├── assert.go
│   ├── scorecard.go
│   └── mock.go
│
├── middleware/      # LLM wrappers: retry, logging, metrics, router, cascade
│   ├── retry.go
│   ├── logger.go
│   ├── metrics.go
│   ├── timeout.go
│   ├── cost.go
│   ├── router.go
│   └── cascade.go
│
├── interfaces/      # HistoryStore, MemoryStore, Guard + in-memory impls
│   ├── history.go
│   ├── memory.go
│   ├── guard.go
│   └── inmemory/
│       ├── history.go
│       └── memory.go
│
├── providers/       # LLM adapters (separate Go modules)
│   ├── openai/
│   ├── anthropic/
│   └── google/
│
└── contrib/         # Optional packages (separate Go modules)
    ├── plan/        # Multi-step procedures
    └── mongo/       # HistoryStore + MemoryStore backed by MongoDB + Atlas vector search
```

**Dependency rules:**
- `kernel/` has zero external dependencies (stdlib only)
- `workflow/` depends only on `kernel/`
- `testing/` depends only on `kernel/`
- `middleware/` depends only on `kernel/`
- `interfaces/` depends only on `kernel/`
- `providers/*` each have their own `go.mod` (import provider SDK)
- `contrib/*` each have their own `go.mod` (import MongoDB driver, etc.)

---

## 3. Core Types (kernel/)

### 3.1 LLM Interface

```go
type LLM interface {
    Generate(ctx context.Context, params GenerateParams) (Response, error)
    GenerateStream(ctx context.Context, params GenerateParams) (Stream, error)
    Model() string
}

type GenerateParams struct {
    Messages    []Message
    Tools       []Tool
    Options     GenerateOptions
}

type GenerateOptions struct {
    Temperature    *float32
    MaxTokens      *int
    StopSequences  []string
    ToolChoice     ToolChoice       // auto, required, none, specific
    OutputSchema   *Schema          // structured output
    ReasoningLevel *string          // for thinking models
}

type Response struct {
    Text         string
    ToolCalls    []ToolCall
    Usage        Usage
    FinishReason string
}

type Usage struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int
    Latency      time.Duration
}
```

**Design decisions:**
- Single `GenerateParams` struct instead of variadic option functions — easier to inspect, test, serialize
- No `GenerateMulti` (best-of-N) — implement via middleware if needed
- `Stream` is a concrete type, not raw channels

### 3.2 Stream

```go
type Stream interface {
    Events() <-chan StreamEvent     // everything: text deltas, tool calls, usage
    Text() <-chan string            // just final text deltas (convenience)
    Response() Response             // full accumulated response (after stream ends)
    Err() error
}

type StreamEvent interface {
    streamEvent()  // marker
}

type TextDeltaEvent struct {
    Text string
}

type ToolStartEvent struct {
    ToolName string
    Params   json.RawMessage
}

type ToolEndEvent struct {
    ToolName string
    Result   any
    Error    error
}
```

**Streaming model:**
- Tool call iterations happen internally (buffered, not streamed to caller)
- Only the final text response streams out via `Text()`
- `Events()` gives everything for advanced callers (show "Searching..." in UI)
- Multiple tool calls in one round execute in parallel by default

### 3.3 Message

```go
type Role string
const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

type Message struct {
    Role       Role
    Content    []ContentPart
    Metadata   map[string]any    // framework-level (timestamps, IDs), not sent to LLM
}

type ContentPart struct {
    Text       *string
    Image      *ImageContent
    ToolCall   *ToolCall
    ToolResult *ToolResult
}

type ImageContent struct {
    URL      string
    MimeType string
}

type ToolCall struct {
    ID     string
    Name   string
    Params json.RawMessage
}

type ToolResult struct {
    ToolCallID string
    Name       string
    Content    string     // JSON-serialized tool output (what LLM sees)
    IsError    bool
}

// Convenience constructors
func SystemMsg(text string) Message
func UserMsg(text string) Message
func AssistantMsg(text string) Message
func ToolResultMsg(callID, name string, content any) Message
```

**Design decisions:**
- 4 roles only (no unknown, generic, or function — those add ambiguity without value)
- `ContentPart` is a tagged struct, not an interface — no serialization ceremony
- No mutex on Message — messages are value types
- `ToolResult.Content` is string, not `any` — it's what the LLM sees, always text
- `Metadata` for framework-level data that doesn't go to the LLM

### 3.4 Tool

```go
type Tool interface {
    Name() string
    Description() string
    Schema() Schema
    Execute(ctx context.Context, params json.RawMessage) (any, error)
}

// Helper: create a tool with typed params (no manual JSON parsing)
func NewTool[P any, R any](
    name string,
    description string,
    fn func(ctx context.Context, params P) (R, error),
) Tool
```

**Schema generation** from struct tags:

```go
type SearchParams struct {
    Query    string `json:"query" description:"What to search for"`
    Location string `json:"location" description:"Area to search in"`
    Limit    int    `json:"limit,omitempty" description:"Max results" required:"false" minimum:"1" maximum:"20"`
}

// Nested structs supported (2-3 levels)
type CreateEventParams struct {
    Title    string   `json:"title" description:"Event title"`
    Location Location `json:"location" description:"Where the event takes place"`
}

type Location struct {
    Name    string `json:"name"`
    Address string `json:"address,omitempty" required:"false"`
}
```

`NewTool` generates schema from `P`'s struct tags via `SchemaFrom[P]()` and handles deserialization automatically. Return value `R` is JSON-serialized for the LLM.

**Guided output** — when a tool needs to instruct the LLM on what to do next:

```go
type Guided[T any] struct {
    Data     T
    Guidance string
}

func Guide[T any](data T, format string, args ...any) Guided[T]

// Usage
searchTool := axon.NewTool("search_restaurants",
    "Search for restaurants",
    func(ctx context.Context, p SearchParams) (axon.Guided[[]Restaurant], error) {
        results := search(p.Query)
        return axon.Guide(results, "Found %d restaurants. Ask user to pick one.", len(results)), nil
    },
)
```

When a tool returns `Guided[T]`, the serialized `Data` plus `Guidance` text are combined into the `ToolResult.Content` that the LLM reads.

**Design decisions:**
- No `StreamCall` — tools execute and return, only the LLM streams
- No `EphemeralTool` — create a new tool with `NewTool()` if you need a variant
- No raw string params — generics handle deserialization
- No `PromptRegistry` for per-model descriptions — simplification; can add later if needed

### 3.5 AgentContext

```go
type AgentContext struct {
    Messages  []Message
    tools     []Tool
    disabled  map[string]bool
}

// Message management
func (c *AgentContext) AddMessages(msgs ...Message)
func (c *AgentContext) SystemPrompt() string
func (c *AgentContext) SetSystemPrompt(prompt string)
func (c *AgentContext) LastUserMessage() *Message

// Tool management
func (c *AgentContext) EnableTools(names ...string)     // filter to only these
func (c *AgentContext) DisableTools(names ...string)    // remove these from active set
func (c *AgentContext) AddTools(tools ...Tool)          // add new tools
func (c *AgentContext) ActiveTools() []Tool             // currently enabled tools
func (c *AgentContext) AllTools() []Tool                // all registered including disabled
```

**Design decisions:**
- No FrameStack — tools enabled/disabled directly via a `disabled` set. If you need time-based expiry, do it in a `PrepareRound` hook. Explicit is better than magic.
- No KV map — external context goes in system messages, tool-to-tool data flows through conversation history, request-scoped internal data goes in Go's `context.Context`.
- No ToolManager/ContextManager/StepManager hierarchy — flat structure.
- No copy-on-write — passed by pointer. Workflow handles isolation if needed.

### 3.6 Agent

```go
type Agent struct {
    model        LLM
    tools        []Tool
    systemPrompt string
    hooks        hooks
    stopConds    []StopCondition
    maxRounds    int               // default 20
}

type StopCondition func(ctx *RoundContext) bool

// Construction
func NewAgent(opts ...AgentOption) *Agent

// Options
func WithModel(llm LLM) AgentOption
func WithTools(tools ...Tool) AgentOption
func WithSystemPrompt(prompt string) AgentOption
func WithMaxRounds(n int) AgentOption

// Hooks (all optional, composable — multiple of same type run in declaration order)
func OnStart(fn func(*TurnContext)) AgentOption
func OnFinish(fn func(*TurnContext)) AgentOption
func PrepareRound(fn func(*RoundContext)) AgentOption
func OnRoundFinish(fn func(*RoundContext)) AgentOption
func OnToolStart(fn func(*ToolContext)) AgentOption
func OnToolEnd(fn func(*ToolContext)) AgentOption
func StopWhen(fn StopCondition) AgentOption

// Execution
func (a *Agent) Run(ctx context.Context, input string) (*Result, error)
func (a *Agent) Stream(ctx context.Context, input string) (*StreamResult, error)

// StreamResult wraps Stream with final Result access
type StreamResult struct {
    Stream                    // embed Stream interface for Events()/Text()
    Result() *Result          // available after stream completes
}
```

**Hook context types:**

```go
type TurnContext struct {
    AgentCtx  *AgentContext
    Input     string
    Result    *Result           // nil in OnStart, populated in OnFinish
}

type RoundContext struct {
    AgentCtx     *AgentContext   // can modify tools and messages
    RoundNumber  int
    LastResponse *Response       // nil on first round
}

type ToolContext struct {
    ToolName  string
    Params    json.RawMessage
    Result    any               // nil in OnToolStart
    Error     error             // nil in OnToolStart
}
```

**Naming:**
- **Turn** = one complete agent invocation (user sends input, agent responds). Method is `.Run()` but hook context is `TurnContext`.
- **Round** = one LLM generation cycle within a turn (generate → maybe tool calls → done). A turn may have multiple rounds.
- `ToolContext` does NOT have `AgentContext` — tool hooks are for observation (logging, metrics). State mutation belongs in `PrepareRound`.

**Agent loop (internal):**

```
Run(ctx, input):
  1. Build AgentContext: system prompt + user message + tools
  2. Fire OnStart hooks
  3. Loop (max maxRounds):
     a. Fire PrepareRound hooks (can modify AgentCtx.Tools, AgentCtx.Messages)
     b. Check StopWhen conditions → break if any true
     c. LLM.GenerateStream(messages, activeTools)
     d. If tool calls:
        - For each tool: fire OnToolStart → execute → fire OnToolEnd
        - Multiple tools execute in parallel
        - Append tool call + result to AgentCtx.Messages
        - Fire OnRoundFinish
        - Continue loop
     e. If text only:
        - Stream text to caller (if streaming mode)
        - Fire OnRoundFinish
        - Break loop (final response)
  4. Fire OnFinish hooks
  5. Return Result
```

**Result:**

```go
type Result struct {
    Text    string
    Rounds  []RoundResult       // full trace of every round
    Usage   Usage               // aggregated across all rounds
}

type RoundResult struct {
    Response  Response
    ToolCalls []ToolCallResult
}

type ToolCallResult struct {
    Name   string
    Params json.RawMessage
    Result any
    Error  error
}
```

---

## 4. Workflow (workflow/)

Workflow orchestrates agents and functions at the top level. It does not run inside an agent — it runs above agents. An Agent can be a step in a Workflow.

```go
type WorkflowStep interface {
    Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error)
}

type WorkflowState struct {
    Input      string
    Messages   []Message
    Data       map[string]any    // pass data between steps
}
```

**Step constructors:**

```go
func Step(name string, fn func(context.Context, *WorkflowState) (*WorkflowState, error)) WorkflowStep
func Parallel(steps ...WorkflowStep) WorkflowStep
func Router(classify func(context.Context, *WorkflowState) string, routes map[string]WorkflowStep) WorkflowStep
func RetryUntil(name string, body WorkflowStep, until func(context.Context, *WorkflowState) bool) WorkflowStep
func Conditional(check func(context.Context, *WorkflowState) bool, ifTrue WorkflowStep, ifFalse WorkflowStep) WorkflowStep
```

**An Agent is a WorkflowStep:**

```go
func (a *Agent) RunWorkflowStep(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
    result, err := a.Run(ctx, input.Input)
    return &WorkflowState{
        Messages: result.State.Messages,
        Data:     map[string]any{"result": result},
    }, err
}
```

**Example — parallel prep, routing, and cleanup as a single workflow:**

```go
workflow := NewWorkflow(
    Parallel(
        Step("classify", classifyIntent),
        Step("load-history", loadHistory),
        Step("load-memories", loadUserMemories),
    ),
    Router(getIntent, map[string]WorkflowStep{
        "booking": bookingAgent.RunWorkflowStep,
        "music":   musicAgent.RunWorkflowStep,
        "general": generalAgent.RunWorkflowStep,
    }),
    Step("persist", saveToDatabase),
)
```

**Parallel state merging:** Parallel steps run concurrently. Each receives a copy of `WorkflowState.Data`. Mutations are merged sequentially in declaration order. If two steps write to the same key, declaration order wins. This is deterministic and documented.

**What Workflow does NOT do:**
- No stream/signal system
- No component registration or capability validation
- No event loop or scheduler
- No LLM-driven scheduling
- No copy-on-write state isolation (just copies Data map for parallel steps)

It's function composition with parallelism, routing, and loops.

**Relationship between Agent and Workflow:**
- Workflow orchestrates BETWEEN agents (top level)
- Agent orchestrates BETWEEN tools (inner level)
- They don't nest — an Agent can be a Workflow step, but a Workflow does not run inside an Agent

---

## 5. Testing (testing/)

### 5.1 Run and Assert

```go
// Run an agent and get a testable result
func Run(t *testing.T, agent *Agent, input string, opts ...RunOption) *TestResult

// Options
func WithHistory(msgs ...Message) RunOption
func MockTool(name string, response any) RunOption
func WithMockLLM(responses ...MockResponse) RunOption
```

### 5.2 Assertions

```go
// Tool assertions — chainable
func (r *TestResult) ExpectTool(name string) *ToolAssertion

func (a *ToolAssertion) Called(t *testing.T)
func (a *ToolAssertion) NotCalled(t *testing.T)
func (a *ToolAssertion) CalledTimes(t *testing.T, n int)
func (a *ToolAssertion) WithParam(key string, value any) *ToolAssertion
func (a *ToolAssertion) WithParamMatch(key string, judge LLM, criteria string) *ToolAssertion

// Response assertions
func (r *TestResult) ExpectResponse() *ResponseAssertion

func (a *ResponseAssertion) Contains(t *testing.T, substring string)
func (a *ResponseAssertion) NotContains(t *testing.T, substring string)
func (a *ResponseAssertion) Satisfies(t *testing.T, judge LLM, criteria string)

// Structural assertions
func (r *TestResult) ExpectRounds(t *testing.T, n int)
```

### 5.3 ScoreCard (batch evaluation)

```go
type ScoreCard struct {
    Criteria     []Criterion
    PassingScore int
}

type Criterion struct {
    Condition string     // "The assistant confirms the reservation"
    Score     int
}

func (sc *ScoreCard) Evaluate(ctx context.Context, judge LLM, messages []Message) (*ScoreResult, error)
```

Judge evaluates with structured output — for each criterion: `{reasoning: "...", condition_met: true/false}`. Reasoning before verdict reduces evaluation errors.

### 5.4 MockLLM

```go
func NewMockLLM() *MockLLM

func (m *MockLLM) OnRound(n int) *MockResponseBuilder

func (b *MockResponseBuilder) RespondWithText(text string) *MockLLM
func (b *MockResponseBuilder) RespondWithToolCall(name string, params map[string]any) *MockLLM
func (b *MockResponseBuilder) RespondWithToolCalls(calls ...ToolCall) *MockLLM
func (b *MockResponseBuilder) RespondWithError(err error) *MockLLM
```

### 5.5 Batch Evaluation

```go
func Eval(t *testing.T, agent *Agent, judge LLM, cases []Case)

type Case struct {
    Input   string
    History []Message
    Expect  *Expectation
}
```

### 5.6 Test Organization

Use Go's built-in test infrastructure for organization:

```go
func TestBooking_Search(t *testing.T)       { /* ... */ }
func TestBooking_MultiTurn(t *testing.T)    { /* ... */ }
func TestMusic_PlaySong(t *testing.T)       { /* ... */ }

// go test -run TestBooking        ← run all booking tests
// go test -run MultiTurn          ← run all multi-turn tests
// go test -short                  ← skip expensive LLM tests
```

No custom tag filtering system — Go subtests, `-run` flag, and `testing.Short()` already handle this.

---

## 6. Middleware (middleware/)

### 6.1 Pattern

```go
type Middleware func(LLM) LLM

func Wrap(llm LLM, mw ...Middleware) LLM
```

Middleware wraps the LLM interface. Applied at construction time, not per-retrieval. The agent doesn't know it's talking to a wrapped LLM.

### 6.2 Built-in Middleware

```go
func WithLogging(logger *slog.Logger) Middleware
func WithRetry(maxAttempts int, baseDelay time.Duration) Middleware
func WithMetrics(collector MetricsCollector) Middleware
func WithTimeout(d time.Duration) Middleware
func WithCostTracker(tracker *CostTracker) Middleware

type MetricsCollector interface {
    RecordLLMCall(model string, usage Usage, err error)
}

type CostTracker struct {
    TotalInputTokens  int
    TotalOutputTokens int
    EstimatedCost     float64
}
```

### 6.3 Router

The router IS an LLM. Routes to other LLMs based on conditions:

```go
type Route struct {
    Model     LLM
    Condition func(RouteContext) bool
}

type RouteContext struct {
    Params GenerateParams
    Ctx    context.Context
}

func NewRouter(fallback LLM, routes ...Route) LLM
```

First matching condition wins. No match → fallback.

### 6.4 Convenience Strategies

Built on top of `NewRouter`:

```go
func RouteByTokenCount(threshold int, small LLM, large LLM) LLM
func RouteByToolCount(threshold int, simple LLM, complex LLM) LLM
func RoundRobin(models ...LLM) LLM

// Cascade: try cheap model, escalate if quality check fails
func Cascade(primary LLM, fallback LLM, shouldEscalate func(Response) bool) LLM
```

### 6.5 Composition

```go
llm := middleware.Wrap(
    anthropic.New("claude-sonnet"),
    middleware.WithRetry(3, time.Second),
    middleware.WithTimeout(30 * time.Second),
    middleware.WithLogging(logger),
    middleware.WithMetrics(statsd),
)

// Router with middleware on individual models
cheap := middleware.Wrap(anthropic.New("claude-haiku"), middleware.WithTimeout(5*time.Second))
expensive := middleware.Wrap(anthropic.New("claude-opus"), middleware.WithTimeout(60*time.Second))
llm := middleware.Cascade(cheap, expensive, func(r Response) bool {
    return len(r.Text) < 50
})
```

---

## 7. Interfaces (interfaces/)

Optional capability interfaces with reference implementations. Import what you need.

### 7.1 HistoryStore (short-term conversation messages)

```go
type HistoryStore interface {
    SaveMessages(ctx context.Context, sessionID string, messages []Message) error
    LoadMessages(ctx context.Context, sessionID string, limit int) ([]Message, error)
    Clear(ctx context.Context, sessionID string) error
}
```

### 7.2 MemoryStore (long-term user knowledge)

```go
type MemoryStore interface {
    Save(ctx context.Context, userID string, memories []Memory) error
    Get(ctx context.Context, userID string) ([]Memory, error)
    Search(ctx context.Context, userID string, query string, topK int) ([]Memory, error)
    Delete(ctx context.Context, userID string, ids []string) error
}

type Memory struct {
    ID        string
    Content   string        // "User has a daughter named Emma who likes soccer"
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

### 7.3 Guard

```go
type Guard interface {
    Check(ctx context.Context, input string) (GuardResult, error)
}

type GuardResult struct {
    Allowed bool
    Reason  string
}
```

### 7.4 Reference Implementations

```
interfaces/inmemory/
├── history.go    # In-memory HistoryStore (for development/testing)
└── memory.go     # In-memory MemoryStore (search = naive string matching)
```

```go
func NewBlocklistGuard(blocked []string) Guard
```

### 7.5 Usage in Agent Hooks

```go
store := inmemory.NewHistoryStore()
memStore := inmemory.NewMemoryStore()
guard := interfaces.NewBlocklistGuard([]string{"ignore previous"})

agent := axon.NewAgent(
    OnStart(func(ctx *TurnContext) {
        // Guard
        if result, _ := guard.Check(goCtx, ctx.Input); !result.Allowed {
            ctx.AgentCtx.SetSystemPrompt("Respond with: I can't help with that.")
            ctx.AgentCtx.DisableTools()
            return
        }
        // Load history
        history, _ := store.LoadMessages(goCtx, sessionID, 50)
        ctx.AgentCtx.AddMessages(history...)
        // Load memories
        memories, _ := memStore.Search(goCtx, userID, ctx.Input, 5)
        if len(memories) > 0 {
            ctx.AgentCtx.AddMessages(axon.SystemMsg(formatMemories(memories)))
        }
    }),
    OnFinish(func(ctx *TurnContext) {
        store.SaveMessages(goCtx, sessionID, ctx.AgentCtx.Messages)
        go extractAndSaveMemories(goCtx, memStore, userID, ctx.AgentCtx.Messages)
    }),
)
```

### 7.6 What Axon Does NOT Ship

| Capability | Why it's out of scope |
|---|---|
| Multiple storage backends | Framework ships contracts, not implementations |
| PII sanitization | Application concern |
| Auth middleware (OAuth, JWKS) | HTTP middleware, not agent framework |
| Memory extraction strategies | Application strategy, not framework |
| Encryption layer | Storage implementation detail |

---

## 8. Contrib Packages

### 8.1 contrib/plan — Multi-Step Procedures

For agents that need structured multi-step tracking beyond what the LLM naturally handles (5+ step flows, resumability, auditability).

```go
type Plan struct {
    Name   string
    Goal   string
    Steps  []Step
    Notes  map[string]any    // scratchpad for intermediate data
}

type Step struct {
    Name           string
    Description    string
    Status         StepStatus    // pending, active, done, skipped
    NeedsUserInput bool
    CanRepeat      bool
}
```

Activated via tool output, injected into LLM context as a system message, updated via a built-in `mark_step` tool. The LLM reads the plan each round and follows it.

Most agents don't need this. Simple tool interactions (1-3 rounds) work without Plan. Use Plan for long procedural flows where the LLM might lose track.

### 8.2 contrib/mongo — MongoDB Storage

```go
// Implements interfaces.HistoryStore
func NewHistoryStore(db *mongo.Database, collection string) interfaces.HistoryStore

// Implements interfaces.MemoryStore (with Atlas vector search)
func NewMemoryStore(db *mongo.Database, collection string, embedder Embedder) interfaces.MemoryStore
```

Separate `go.mod` — importing Axon does not pull in the MongoDB driver.

---

## 9. Observability

No custom tracer interface. Use OpenTelemetry directly.

The agent loop instruments itself with OTel spans:

```
agent.turn
  ├── agent.round (round=0)
  │   ├── llm.generate (model=claude-sonnet, input_tokens=1200)
  │   ├── tool.execute (tool=search_restaurants)
  │   └── agent.round.finish (tool_calls=1)
  ├── agent.round (round=1)
  │   ├── llm.generate (model=claude-sonnet, input_tokens=1800)
  │   └── agent.round.finish (tool_calls=0, text=true)
  └── agent.turn.finish (rounds=2, total_tokens=3200)
```

If the application has configured an OTel provider (Datadog, Jaeger, stdout), it works. If not, OTel's no-op default kicks in. Zero framework configuration needed.

---

## 10. Design Decisions & Rationale

This section documents key design decisions and the reasoning behind them, informed by lessons from building production agent systems and studying existing frameworks.

### What we kept and why

| Pattern | Axon implementation | Rationale |
|---|---|---|
| Dynamic tool scoping | `AgentContext.EnableTools()` / `DisableTools()` in `PrepareRound` | Agents with 20+ tools need to scope what the LLM sees per-turn. Accuracy degrades above ~15 tools. |
| Pre-execution classification | Workflow `Parallel` + `Router` steps before Agent | Running classifiers (BERT, LLM, rule-based) before the agent loop reduces token waste and improves tool selection. |
| Composable pre/post hooks | `OnStart` / `OnFinish` + Workflow composition | State setup (load history, inject context) and cleanup (persist, extract memories) are universal patterns that should be first-class. |
| Tool mocking in tests | `axontest.MockTool()` | Agent tests need to intercept tool calls without hitting external services. |
| ScoreCard LLM-as-judge | `axontest.ScoreCard` with reasoning-before-verdict | Forcing the judge to reason before scoring reduces evaluation errors. Proven pattern. |
| Dual parameter validation | `WithParam` (exact) + `WithParamMatch` (LLM-judged) | Some params are deterministic ("cuisine" = "thai"), others are semantic ("query should mention Italian food"). |
| Multi-provider with routing | `providers/` packages + `middleware.NewRouter()` | Production systems use multiple models. The router-as-LLM pattern makes this transparent. |
| Typed tool parameters | `NewTool[P, R]()` with generics | Eliminates manual JSON parsing boilerplate that causes bugs. |
| Composable stop conditions | `StopWhen(func(*RoundContext) bool)` | A single max-rounds limit is insufficient. Cost budgets, quality thresholds, and custom conditions need composition. |
| Per-round hooks | `PrepareRound` / `OnRoundFinish` | Essential for dynamic tool scoping and observability. The agent loop is where most interesting behavior happens. |

### What we dropped and why

| Pattern | Why it was dropped |
|---|---|
| FrameStack / layered state with event-driven expiry | Powerful but imposes massive cognitive overhead. Direct enable/disable in PrepareRound hooks achieves the same result with plain code. |
| Generic state managers | A flat AgentContext with explicit methods is easier to understand than parameterized managers. |
| Ephemeral tool wrappers | Creating a new tool via `NewTool()` is simpler than a wrapper/reconstructor pattern with serialization edge cases. |
| Traversal closures for state mutation | Regular functions in hooks are more debuggable than closures returned from classifiers. |
| Separate Chain + Executor types | One Agent type does both. The split added indirection without benefit. |
| Event-driven orchestrator (scheduler/component/stream/signal) | Replaced by Workflow — plain function composition with parallelism. No registration, no capability validation, no event loop. |
| Copy-on-write state isolation | Workflow copies `Data` map for parallel steps. Full deep-copy of state per task was expensive and rarely needed. |
| goto-based retry logic | Standard `for` loops with `break`/`continue`. Control flow should be readable. |
| Untyped KV map on state | System messages for LLM-visible context, `context.Context` for internal request-scoped data. Two clear mechanisms instead of one ambiguous grab bag. |
| Multiple storage backend implementations | Framework ships interfaces + one in-memory reference. Production backends are the application's concern. |
| Transport layer (SSE, WebSocket, Socket.IO) | Agent framework produces output. How it reaches clients is a separate concern. |
| YAML model catalog | Compose in code. Configuration-driven model wiring adds a layer of indirection that makes debugging harder. |
| Provider-defined/executed tools | Application wraps provider-specific APIs as standard tools. The framework stays provider-agnostic. |

### Complexity budget

| Level | Core concepts |
|---|---|
| **Minimal viable usage** | Agent, Tool, LLM, Message (~4 concepts) |
| **With hooks** | + 7 hook types, AgentContext (~12 concepts) |
| **With orchestration** | + Workflow, Step, Parallel, Router (~16 concepts) |
| **Full framework** | + Middleware, HistoryStore, MemoryStore, Guard (~20 concepts) |

Progressive disclosure: you learn only what you need. Compare to enterprise frameworks that require understanding ~30 concepts before writing your first agent.

---

## 11. Non-Goals

- **Not a chat UI framework.** No React components, no frontend hooks. Axon is backend-only.
- **Not a model training/fine-tuning tool.** Axon uses models, doesn't train them.
- **Not a deployment platform.** No Docker, Kubernetes, serverless integration.
- **Not an all-in-one framework.** Axon is deliberately incomplete — it provides the core and interfaces, applications fill in the rest.
- **Not backwards-compatible with any existing framework.** Clean break, new API, new patterns.
