# Agents

An `Agent` is Axon's core abstraction for driving an LLM through a multi-step loop.
It generates a response, executes any tool calls the model requests, feeds the results
back into the conversation, and repeats — until the model returns a plain text response
or a stop condition fires.

---

## 1. Creating an Agent

Use `kernel.NewAgent` with functional options. The only required option is a model;
everything else has a sensible default.

### Minimal example

```go
import (
    "context"
    "fmt"

    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/providers/anthropic"
)

func main() {
    llm := anthropic.New("claude-3-5-haiku-20241022")

    agent := kernel.NewAgent(
        kernel.WithModel(llm),
    )

    result, err := agent.Run(context.Background(), "What is the capital of France?")
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Text)
}
```

### Full example

```go
agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithSystemPrompt("You are a helpful assistant."),
    kernel.WithTools(searchTool, calculatorTool),
    kernel.WithMaxRounds(15),
)
```

**Available options:**

| Option | Description |
|---|---|
| `WithModel(llm)` | The `LLM` provider to use (required). |
| `WithSystemPrompt(prompt)` | System prompt injected at the start of every turn. |
| `WithTools(tools...)` | Tools the model may call during a turn. |
| `WithMaxRounds(n)` | Hard cap on the number of LLM calls per turn (default: 20). |

Hook options (`OnStart`, `OnFinish`, etc.) and `StopWhen` are covered in the sections below.

---

## 2. The Agent Loop

A **turn** is one call to `agent.Run()`. Within that turn the agent executes a loop where
each iteration is called a **round**. A round is one call to the LLM followed by
(optionally) executing whatever tool calls the model requested.

```
Run(ctx, input)
 │
 ├─ Build AgentContext (tools + system prompt + user message)
 ├─ Fire OnStart hooks
 │
 └─ Loop (up to maxRounds):
      ├─ Fire PrepareRound hooks
      ├─ Evaluate StopWhen conditions  →  break if any returns true
      ├─ model.Generate(messages, activeTools)
      │
      ├─ (tool calls present?)
      │    yes → execute tools in parallel, append results to context → next round
      │    no  → finalText = response.Text → break
      │
      └─ Fire OnRoundFinish hooks

 ├─ Fire OnFinish hooks
 └─ Return *Result
```

`WithMaxRounds(n)` sets the ceiling on how many rounds can execute. If the loop reaches
that ceiling without a plain-text response the agent returns whatever it has accumulated
so far. The default is 20.

---

## 3. Hooks

Hooks let you observe and modify agent execution at six points in the lifecycle.
Each hook function receives a context struct that exposes the state available at that
point. Multiple hooks of the same type can be registered; they are called in registration
order.

### Hook types

| Option | Context type | Called when |
|---|---|---|
| `OnStart(func(*TurnContext))` | `*TurnContext` | Before the loop starts |
| `OnFinish(func(*TurnContext))` | `*TurnContext` | After the loop ends |
| `PrepareRound(func(*RoundContext))` | `*RoundContext` | Before each LLM call |
| `OnRoundFinish(func(*RoundContext))` | `*RoundContext` | After each LLM response |
| `OnToolStart(func(*ToolContext))` | `*ToolContext` | Before tool execution |
| `OnToolEnd(func(*ToolContext))` | `*ToolContext` | After tool execution |

### Hook lifecycle timeline

The diagram below shows the execution order across a two-round turn — the first round
calls a tool, the second produces a plain-text response.

```
Turn Start
│
├─ OnStart(TurnContext)
│
├─ Round 0
│  ├─ PrepareRound(RoundContext)
│  ├─ LLM.Generate()
│  ├─ OnToolStart(ToolContext)  ─┐
│  ├─ Tool.Execute()             ├─ (for each tool call)
│  ├─ OnToolEnd(ToolContext)    ─┘
│  └─ OnRoundFinish(RoundContext)
│
├─ Round 1
│  ├─ PrepareRound(RoundContext)
│  ├─ LLM.Generate()
│  └─ OnRoundFinish(RoundContext)  ← text response, no tools
│
└─ OnFinish(TurnContext)
```

### Context fields

**`TurnContext`** — available in `OnStart` and `OnFinish`:

```go
type TurnContext struct {
    AgentCtx *AgentContext // live conversation state; mutate to alter behavior
    Input    string        // the original user message passed to Run()
    Result   *Result       // nil in OnStart; populated in OnFinish
}
```

**`RoundContext`** — available in `PrepareRound`, `OnRoundFinish`, and `StopWhen`:

```go
type RoundContext struct {
    AgentCtx     *AgentContext // live conversation state
    RoundNumber  int           // 0-based index of the current round
    LastResponse *Response     // nil in round 0; the previous LLM response otherwise
}
```

**`ToolContext`** — available in `OnToolStart` and `OnToolEnd`:

```go
type ToolContext struct {
    ToolName string          // name of the tool being called
    Params   json.RawMessage // raw JSON params from the model
    Result   any             // nil in OnToolStart; populated in OnToolEnd
    Error    error           // nil unless the tool returned an error
}
```

### Context availability at a glance

```
TurnContext (OnStart/OnFinish)
├─ AgentCtx  → modify tools, messages, system prompt
├─ Input     → the user's input string
└─ Result    → nil in OnStart, populated in OnFinish

RoundContext (PrepareRound/OnRoundFinish)
├─ AgentCtx     → modify tools, messages
├─ RoundNumber  → which round (0-indexed)
└─ LastResponse → nil on first round

ToolContext (OnToolStart/OnToolEnd)
├─ ToolName  → which tool
├─ Params    → raw JSON params
├─ Result    → nil in OnToolStart
└─ Error     → nil in OnToolStart
```

### Example: all six hooks

```go
agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithTools(myTools...),

    // OnStart fires before the first round. Use it to load history,
    // run guards, or modify the initial context.
    kernel.OnStart(func(tc *kernel.TurnContext) {
        log.Printf("turn started: input=%q", tc.Input)
    }),

    // OnFinish fires after the loop. tc.Result is populated here.
    kernel.OnFinish(func(tc *kernel.TurnContext) {
        if tc.Result != nil {
            log.Printf("turn finished: rounds=%d tokens=%d",
                len(tc.Result.Rounds), tc.Result.Usage.TotalTokens)
        }
    }),

    // PrepareRound fires before each LLM call. Use it to swap tools,
    // inject context, or log which round is starting.
    kernel.PrepareRound(func(rc *kernel.RoundContext) {
        log.Printf("starting round %d", rc.RoundNumber)
    }),

    // OnRoundFinish fires after the LLM responds. rc.LastResponse is set.
    kernel.OnRoundFinish(func(rc *kernel.RoundContext) {
        if rc.LastResponse != nil {
            log.Printf("round %d done: tool_calls=%d finish_reason=%s",
                rc.RoundNumber,
                len(rc.LastResponse.ToolCalls),
                rc.LastResponse.FinishReason)
        }
    }),

    // OnToolStart fires just before a tool executes.
    kernel.OnToolStart(func(tc *kernel.ToolContext) {
        log.Printf("tool start: %s params=%s", tc.ToolName, tc.Params)
    }),

    // OnToolEnd fires after a tool returns. tc.Result and tc.Error are set.
    kernel.OnToolEnd(func(tc *kernel.ToolContext) {
        if tc.Error != nil {
            log.Printf("tool error: %s error=%v", tc.ToolName, tc.Error)
        } else {
            log.Printf("tool end: %s", tc.ToolName)
        }
    }),
)
```

The restaurant-bot example (`examples/07-restaurant-bot/agent.go`) shows a realistic
use of `OnStart` to run a guard and load conversation history, `OnFinish` to persist
the new turn to a history store, and `OnToolStart`/`OnToolEnd` for structured logging.

---

## 4. Stop Conditions

`StopWhen` registers a predicate evaluated before each round. If any predicate returns
`true` the loop stops immediately, before the LLM is called for that round.

```go
StopWhen(func(rc *kernel.RoundContext) bool) AgentOption
```

`rc` is the same `RoundContext` passed to `PrepareRound`.

### Stop on round limit

```go
kernel.StopWhen(func(rc *kernel.RoundContext) bool {
    return rc.RoundNumber >= 5
})
```

This is equivalent to `WithMaxRounds(5)` but expressed as a stop condition, which
lets you compose it with other conditions.

### Stop on cost budget

Track token usage via `OnRoundFinish` and halt when a budget is exceeded:

```go
var totalTokens int

agent := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.OnRoundFinish(func(rc *kernel.RoundContext) {
        if rc.LastResponse != nil {
            totalTokens += rc.LastResponse.Usage.TotalTokens
        }
    }),
    kernel.StopWhen(func(rc *kernel.RoundContext) bool {
        return totalTokens > 10_000
    }),
)
```

### Stop when a specific tool was called

```go
kernel.StopWhen(func(rc *kernel.RoundContext) bool {
    if rc.LastResponse == nil {
        return false
    }
    for _, tc := range rc.LastResponse.ToolCalls {
        if tc.Name == "finalize_order" {
            return true
        }
    }
    return false
})
```

Multiple `StopWhen` options are OR-combined: the loop stops if any one of them returns
`true`.

---

## 5. AgentContext

`AgentContext` is the live conversation state passed through every hook. It holds the
message history and the tool registry for the current turn. You can read and mutate it
from any hook.

### Methods

```go
// Message history
agentCtx.AddMessages(msgs ...Message)
agentCtx.SetSystemPrompt(prompt string)
agentCtx.SystemPrompt() string
agentCtx.LastUserMessage() *Message

// Tool management
agentCtx.EnableTools(names ...string)   // allow only these tools; disable the rest
agentCtx.DisableTools(names ...string)  // disable specific tools
agentCtx.AddTools(tools ...Tool)        // register new tools (enabled by default)
agentCtx.ActiveTools() []Tool           // tools currently visible to the model
agentCtx.AllTools() []Tool              // all registered tools regardless of state
agentCtx.GetTool(name string) (Tool, bool)
agentCtx.ReplaceTools(tools []Tool)     // replace the entire tool set
```

`ActiveTools()` is what the agent passes to the LLM on each round. `EnableTools` and
`DisableTools` let you narrow that set dynamically without deregistering tools.

### Dynamic tool scoping with PrepareRound

Use `PrepareRound` to make the active tool set depend on round number or any other
runtime state:

```go
kernel.PrepareRound(func(rc *kernel.RoundContext) {
    switch rc.RoundNumber {
    case 0:
        // First round: only allow searching
        rc.AgentCtx.EnableTools("search_restaurants", "get_weather")
    default:
        // Later rounds: allow all tools
        rc.AgentCtx.EnableTools(
            "search_restaurants", "get_weather", "get_menu", "make_reservation",
        )
    }
})
```

### Injecting context mid-turn

You can append messages from any hook. For example, `OnStart` can inject retrieved
documents before the first round:

```go
kernel.OnStart(func(tc *kernel.TurnContext) {
    docs := retriever.Search(tc.Input)
    tc.AgentCtx.AddMessages(kernel.SystemMsg("Relevant context:\n" + docs))
})
```

---

## 6. CloneWith

`CloneWith` creates a shallow copy of an agent with additional options applied. The
clone shares the original's model, system prompt, and hooks, but has an independent
tool slice so modifications do not affect the original.

```go
func (a *Agent) CloneWith(opts ...AgentOption) *Agent
```

This is useful for creating per-request variants or specializing a base agent for
different roles:

```go
base := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithSystemPrompt("You are a helpful assistant."),
    kernel.WithMaxRounds(10),
)

// Specialist variant with an extra tool and a tighter round limit.
specialist := base.CloneWith(
    kernel.WithTools(specializedTool),
    kernel.WithMaxRounds(5),
)
```

Because `CloneWith` appends to the clone, you can also layer hooks onto a base agent
without modifying it:

```go
debugAgent := base.CloneWith(
    kernel.OnRoundFinish(func(rc *kernel.RoundContext) {
        fmt.Printf("[debug] round %d\n", rc.RoundNumber)
    }),
)
```

---

## 7. Result

`agent.Run` returns `(*Result, error)`.

```go
type Result struct {
    Text   string        // final text response from the model
    Rounds []RoundResult // one entry per round executed
    Usage  Usage         // aggregated token usage across all rounds
}
```

### Usage

`Usage` accumulates token counts and latency across all rounds in the turn:

```go
fmt.Printf("input=%d output=%d total=%d latency=%s\n",
    result.Usage.InputTokens,
    result.Usage.OutputTokens,
    result.Usage.TotalTokens,
    result.Usage.Latency,
)
```

### Inspecting the tool call trace

Each `RoundResult` holds the LLM `Response` and a slice of `ToolCallResult` values
for any tools that were called in that round:

```go
type RoundResult struct {
    Response  kernel.Response
    ToolCalls []kernel.ToolCallResult
}

type ToolCallResult struct {
    Name   string
    Params json.RawMessage
    Result any
    Error  error
}
```

Walk `result.Rounds` to reconstruct exactly what happened:

```go
for i, round := range result.Rounds {
    if len(round.ToolCalls) == 0 {
        fmt.Printf("round %d: text response\n", i)
        continue
    }
    for _, tc := range round.ToolCalls {
        var params any
        _ = json.Unmarshal(tc.Params, &params)
        paramsJSON, _ := json.Marshal(params)

        if tc.Error != nil {
            fmt.Printf("round %d: tool=%q params=%s error=%v\n",
                i, tc.Name, paramsJSON, tc.Error)
        } else {
            resultJSON, _ := json.Marshal(tc.Result)
            fmt.Printf("round %d: tool=%q params=%s result=%s\n",
                i, tc.Name, paramsJSON, resultJSON)
        }
    }
}
```

See `examples/02-tools/main.go` for a complete runnable version of this trace.
