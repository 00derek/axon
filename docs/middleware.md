# Middleware

Axon middleware lets you wrap any `kernel.LLM` with additional behavior — retries,
timeouts, logging, cost tracking — without changing your agent code. Routers and
cascades extend the same pattern to dispatch requests across multiple models.

---

## 1. The Middleware Pattern

`Middleware` is a function type:

```go
type Middleware func(kernel.LLM) kernel.LLM
```

Each middleware wraps an `LLM` and returns a new `LLM`. The agent has no
knowledge that it is talking to a wrapped model.

`Wrap` composes middleware in order. The first argument in the list becomes
the outermost layer — the one called first on every request:

```go
func Wrap(llm kernel.LLM, mw ...Middleware) kernel.LLM
```

```go
wrapped := middleware.Wrap(
    llm,
    middleware.WithRetry(3, 100*time.Millisecond),
    middleware.WithTimeout(5*time.Second),
    middleware.WithLogging(logger),
)
// Call order: WithRetry → WithTimeout → WithLogging → llm
```

```
middleware.Wrap(llm, WithRetry, WithTimeout, WithLogging)

  Agent calls Generate()
       │
       ▼
  ┌─────────────┐
  │  WithRetry   │  ← outermost: retries on failure
  │  ┌─────────┐ │
  │  │WithTimeout│ │  ← middle: adds deadline
  │  │ ┌──────┐ │ │
  │  │ │Logger│ │ │  ← innermost: logs the call
  │  │ │ ┌──┐ │ │ │
  │  │ │ │LLM│ │ │ │  ← actual LLM provider
  │  │ │ └──┘ │ │ │
  │  │ └──────┘ │ │
  │  └─────────┘ │
  └─────────────┘
```

The wrapped LLM satisfies `kernel.LLM` and is passed directly to
`kernel.WithModel`. From the agent's perspective nothing has changed.

> **Note on snippets below.** Provider examples assume an SDK client already
> exists, e.g.
>
> ```go
> import sdk "github.com/anthropics/anthropic-sdk-go"
> client := sdk.NewClient() // reads ANTHROPIC_API_KEY
> ```
>
> and then `anthropic.New(&client, ...)` wraps it into a `kernel.LLM`.

See `examples/04-middleware/` for a complete runnable example.

---

## 2. Built-in Middleware

### WithRetry

Retries failed `Generate` calls with exponential backoff and jitter.

```go
func WithRetry(maxAttempts int, baseDelay time.Duration) Middleware
```

The delay for attempt `n` (0-indexed):

```
delay = baseDelay * 2^n * (0.5 + rand(0, 0.5))
```

Context cancellation is respected between attempts. `GenerateStream` is not
retried because stream failures are not idempotent.

```go
llm := middleware.Wrap(
    anthropic.New(&client, sdk.ModelClaudeHaiku4_5),
    middleware.WithRetry(3, 500*time.Millisecond),
)
```

### WithTimeout

Wraps each LLM call with a `context.WithTimeout`. If the call exceeds the
deadline, it returns `context.DeadlineExceeded`.

```go
func WithTimeout(d time.Duration) Middleware
```

```go
llm := middleware.Wrap(
    anthropic.New(&client, sdk.ModelClaudeHaiku4_5),
    middleware.WithTimeout(10*time.Second),
)
```

For streaming, the timeout applies to the initial stream creation call, not
to reading chunks from the stream. Manage long-lived stream lifetimes with
your own context.

### WithLogging

Logs each LLM call using a `*slog.Logger`. Successful calls are logged at
`Info`; errors at `Error`. Logged attributes: `model`, `latency`,
`input_tokens`, `output_tokens`, `total_tokens`.

```go
func WithLogging(logger *slog.Logger) Middleware
```

```go
logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

llm := middleware.Wrap(
    anthropic.New(&client, sdk.ModelClaudeHaiku4_5),
    middleware.WithLogging(logger),
)
```

### WithCostTracker

Accumulates token usage across LLM calls into a `*CostTracker`. Only
successful calls contribute; errors are ignored.

```go
func WithCostTracker(tracker *CostTracker) Middleware
```

`CostTracker` fields:

```go
type CostTracker struct {
    TotalInputTokens  int
    TotalOutputTokens int
    EstimatedCost     float64

    // Optional: if set, EstimatedCost is updated after each successful call.
    CostFunc func(inputTokens, outputTokens int) float64
}
```

Read accumulated values through a lock-safe snapshot:

```go
tracker := middleware.NewCostTracker()
tracker.CostFunc = func(in, out int) float64 {
    return float64(in)*0.000001 + float64(out)*0.000002
}

llm := middleware.Wrap(
    anthropic.New(&client, sdk.ModelClaudeHaiku4_5),
    middleware.WithCostTracker(tracker),
)

// After agent.Run():
snap := tracker.Snapshot()
fmt.Printf("input=%d output=%d cost=$%.6f\n",
    snap.TotalInputTokens, snap.TotalOutputTokens, snap.EstimatedCost)
```

`Snapshot` returns a `CostTrackerSnapshot` — an immutable struct — so it is
safe to read concurrently while calls are in flight.

### WithMetrics

Forwards call metrics to any `MetricsCollector` implementation.

```go
type MetricsCollector interface {
    RecordLLMCall(model string, usage kernel.Usage, err error)
}

func WithMetrics(collector MetricsCollector) Middleware
```

Both successful and failed calls are recorded. Implement the interface to
bridge to any backend (Prometheus, StatsD, etc.):

```go
type promCollector struct{}

func (p *promCollector) RecordLLMCall(model string, usage kernel.Usage, err error) {
    if err != nil {
        errorCounter.WithLabelValues(model).Inc()
        return
    }
    tokenHistogram.WithLabelValues(model).Observe(float64(usage.TotalTokens))
}

llm := middleware.Wrap(
    anthropic.New(&client, sdk.ModelClaudeHaiku4_5),
    middleware.WithMetrics(&promCollector{}),
)
```

---

## 3. Model Routing

A router is itself a `kernel.LLM`. You pass it to `kernel.WithModel` like any
other model, and it dispatches each request to one of its registered models
based on conditions you define.

```go
func NewRouter(fallback kernel.LLM, routes ...Route) kernel.LLM
```

Each `Route` pairs a target model with a condition:

```go
type Route struct {
    Model     kernel.LLM
    Condition func(RouteContext) bool
}
```

Routes are evaluated in order. The first condition that returns `true` wins.
If no condition matches, the fallback is used.

```
NewRouter(fallback, route1, route2, route3)

  Request ──► route1.Condition? ──yes──► route1.Model
              │ no
              ▼
              route2.Condition? ──yes──► route2.Model
              │ no
              ▼
              route3.Condition? ──yes──► route3.Model
              │ no
              ▼
              fallback (default)
```

`RouteContext` exposes the request before it is sent to any model:

```go
type RouteContext struct {
    Params kernel.GenerateParams  // messages, tools, and other call parameters
    Ctx    context.Context
}
```

### Example: route by content keyword

```go
cheap := anthropic.New(&client, sdk.ModelClaudeHaiku4_5)
capable := anthropic.New(&client, sdk.ModelClaudeOpus4_5)

router := middleware.NewRouter(cheap,
    middleware.Route{
        Model: capable,
        Condition: func(rc middleware.RouteContext) bool {
            // Use the capable model for legal or compliance questions.
            for _, msg := range rc.Params.Messages {
                for _, part := range msg.Content {
                    if part.Text != nil && strings.Contains(*part.Text, "legal") {
                        return true
                    }
                }
            }
            return false
        },
    },
)

agent := kernel.NewAgent(kernel.WithModel(router))
```

---

## 4. Convenience Routers

Three helpers build common routing patterns on top of `NewRouter`.

### RouteByTokenCount

Routes to `large` when the estimated token count of the messages exceeds
`threshold`; uses `small` otherwise. Token count is estimated at ~4 characters
per token.

```go
func RouteByTokenCount(threshold int, small kernel.LLM, large kernel.LLM) kernel.LLM
```

```go
haiku := anthropic.New(&client, sdk.ModelClaudeHaiku4_5)
opus  := anthropic.New(&client, sdk.ModelClaudeOpus4_5)

// Requests with > 2000 estimated tokens go to opus.
router := middleware.RouteByTokenCount(2000, haiku, opus)
```

### RouteByToolCount

Routes to `complex` when the number of tools registered on the agent exceeds
`threshold`; uses `simple` otherwise.

```go
func RouteByToolCount(threshold int, simple kernel.LLM, complex kernel.LLM) kernel.LLM
```

```go
// Agents with > 2 tools use the capable model.
router := middleware.RouteByToolCount(2, haiku, opus)
```

### RoundRobin

Distributes calls evenly across the given models using an atomic counter.
Thread-safe for concurrent agents.

```go
func RoundRobin(models ...kernel.LLM) kernel.LLM
```

```go
replica1 := anthropic.New(&client, sdk.ModelClaudeHaiku4_5)
replica2 := anthropic.New(&client, sdk.ModelClaudeHaiku4_5)
replica3 := anthropic.New(&client, sdk.ModelClaudeHaiku4_5)

lb := middleware.RoundRobin(replica1, replica2, replica3)
```

`RoundRobin` panics at construction time if the models slice is empty.

---

## 5. Cascade

`Cascade` tries a primary model first. If the primary call errors, or if a
quality check function returns `true` for the response, the fallback model is
called instead.

```go
func Cascade(
    primary        kernel.LLM,
    fallback        kernel.LLM,
    shouldEscalate func(kernel.Response) bool,
) kernel.LLM
```

```
Cascade(primary, fallback, shouldEscalate)

  Request ──► primary.Generate()
              │
              ▼
              shouldEscalate(response)?
              ├─ false ──► return response  (cheap model was good enough)
              └─ true  ──► fallback.Generate() ──► return response
```

This is useful for a "try cheap first, escalate on low quality" pattern:

```go
haiku := anthropic.New(&client, sdk.ModelClaudeHaiku4_5)
opus  := anthropic.New(&client, sdk.ModelClaudeOpus4_5)

// Escalate if the response is very short, which may indicate the cheap model
// didn't have enough context to give a complete answer.
llm := middleware.Cascade(haiku, opus, func(resp kernel.Response) bool {
    return len(resp.Text) < 100
})
```

`GenerateStream` always uses the primary model; escalation does not apply to
streams.

---

## 6. Composition

Because every middleware and router satisfies `kernel.LLM`, they compose
freely. You can apply middleware to individual models before routing, or apply
middleware to the router itself.

```
  Agent
    │
    ▼
  Cascade(cheap, expensive, qualityCheck)
    │                    │
    ▼                    ▼
  cheap stack          expensive stack
  ┌──────────┐         ┌──────────┐
  │  Retry   │         │  Retry   │
  │ Timeout  │         │ Timeout  │
  │  Logger  │         │  Logger  │
  │ Haiku LLM│         │ Opus LLM │
  └──────────┘         └──────────┘
```

### Middleware per model, then cascade

```go
cheap := middleware.Wrap(
    anthropic.New(&client, sdk.ModelClaudeHaiku4_5),
    middleware.WithRetry(3, 100*time.Millisecond),
    middleware.WithTimeout(5*time.Second),
)

expensive := middleware.Wrap(
    anthropic.New(&client, sdk.ModelClaudeOpus4_5),
    middleware.WithTimeout(30*time.Second),
)

llm := middleware.Cascade(cheap, expensive, func(resp kernel.Response) bool {
    return len(resp.Text) < 50
})

agent := kernel.NewAgent(kernel.WithModel(llm))
```

### Shared logging and cost tracking around a router

Apply middleware to the router as a whole so every request — regardless of
which model handles it — is logged and counted:

```go
tracker := middleware.NewCostTracker()
logger  := slog.New(slog.NewTextHandler(os.Stdout, nil))

router := middleware.RouteByTokenCount(2000, haiku, opus)

llm := middleware.Wrap(
    router,
    middleware.WithLogging(logger),
    middleware.WithCostTracker(tracker),
)

agent := kernel.NewAgent(kernel.WithModel(llm))
```

### Full example: retry on cheap + timeout on expensive + cascade

```go
import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "time"

    sdk "github.com/anthropics/anthropic-sdk-go"

    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/middleware"
    "github.com/axonframework/axon/providers/anthropic"
)

func buildLLM() kernel.LLM {
    client := sdk.NewClient() // reads ANTHROPIC_API_KEY

    // Cheap model: up to 3 retries, 5 s ceiling per attempt.
    cheap := middleware.Wrap(
        anthropic.New(&client, sdk.ModelClaudeHaiku4_5),
        middleware.WithRetry(3, 200*time.Millisecond),
        middleware.WithTimeout(5*time.Second),
    )

    // Capable model: no retries, but a generous timeout.
    capable := middleware.Wrap(
        anthropic.New(&client, sdk.ModelClaudeOpus4_5),
        middleware.WithTimeout(60*time.Second),
    )

    // Escalate when the cheap model gives a suspiciously short answer.
    llm := middleware.Cascade(cheap, capable, func(resp kernel.Response) bool {
        return len(resp.Text) < 80
    })

    // Wrap the cascade with shared logging and cost tracking.
    tracker := middleware.NewCostTracker()
    tracker.CostFunc = func(in, out int) float64 {
        return float64(in)*0.000001 + float64(out)*0.000002
    }

    logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

    return middleware.Wrap(llm,
        middleware.WithLogging(logger),
        middleware.WithCostTracker(tracker),
    )
}

func main() {
    agent := kernel.NewAgent(
        kernel.WithModel(buildLLM()),
        kernel.WithSystemPrompt("You are a helpful assistant."),
    )

    result, err := agent.Run(context.Background(), "Explain the CAP theorem.")
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Text)
}
```
