# Workflows

A workflow orchestrates agents and functions **above** the agent level — it is
the layer that sequences, parallelizes, and routes the work that agents
(and any other logic) perform. Workflows do not replace agents; they
coordinate them.

---

## 1. What is a Workflow

The `workflow` package provides a small set of composable primitives. Every
primitive implements the same interface:

```go
type WorkflowStep interface {
    Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error)
}
```

`WorkflowState` is the envelope passed from step to step:

```go
type WorkflowState struct {
    Input    string           // the original user-facing input string
    Messages []kernel.Message // conversation history, if any
    Data     map[string]any   // arbitrary key/value bag for passing data between steps
}
```

- `Input` is read-only by convention. Steps read it to understand what the
  workflow was asked to do.
- `Messages` carries conversation history. Steps can append messages; parallel
  steps can each accumulate new messages that are merged after the group
  completes.
- `Data` is the main channel for passing structured data between steps. Any
  step can read and write keys freely.

`Data` is initialized automatically on first use — you never need to allocate
it yourself.

---

## 2. Step Primitives

### `Step`

`Step` wraps a named function as a `WorkflowStep`. It is the building block
for all custom logic.

```go
func Step(name string, fn func(context.Context, *WorkflowState) (*WorkflowState, error)) WorkflowStep
```

A step receives a pointer to the current state, mutates it (or not), and
returns it. Returning the same pointer is fine.

```go
greet := workflow.Step("greet", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    s.Data["greeting"] = "Hello, " + s.Input + "!"
    return s, nil
})
```

### `NewWorkflow`

`NewWorkflow` composes steps into a sequential pipeline. Each step receives
the state that the previous step returned.

```go
func NewWorkflow(steps ...WorkflowStep) WorkflowStep
```

```
NewWorkflow(step1, step2, step3)

  Input ──► step1 ──► step2 ──► step3 ──► Output
            state     state     state
            flows     flows     flows
            through   through   through
```

```go
enhance := workflow.Step("enhance", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    base, _ := s.Data["greeting"].(string)
    s.Data["greeting"] = base + " Welcome to Axon."
    return s, nil
})

wf := workflow.NewWorkflow(greet, enhance)

state, err := wf.Run(context.Background(), &workflow.WorkflowState{Input: "World"})
// state.Data["greeting"] == "Hello, World! Welcome to Axon."
```

Because `NewWorkflow` returns a `WorkflowStep`, workflows can be nested inside
other workflows.

### `Parallel`

`Parallel` runs all child steps concurrently. Each step receives its own
shallow copy of `Data` and `Messages`. After all steps complete their results
are merged back into a single state (see Section 4 for merge semantics).

```go
func Parallel(steps ...WorkflowStep) WorkflowStep
```

```
Parallel(stepA, stepB, stepC)

                ┌─► stepA ──► Data: {user: "Alice"}     ─┐
                │                                          │
  Input ────────┼─► stepB ──► Data: {prefs: "dark"}      ─┼──► Merge (declaration order)
                │                                          │     Data: {user, prefs, history}
                └─► stepC ──► Data: {history: "3 convos"} ─┘
```

```go
fetchUser := workflow.Step("fetch-user", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    s.Data["user"] = "alice (id=42)"
    return s, nil
})

fetchPrefs := workflow.Step("fetch-prefs", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    s.Data["prefs"] = "theme=dark"
    return s, nil
})

fetchHistory := workflow.Step("fetch-history", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    s.Data["history"] = "last login: 2026-03-27"
    return s, nil
})

// All three fetches run concurrently; their distinct keys merge cleanly.
wf := workflow.NewWorkflow(
    workflow.Parallel(fetchUser, fetchPrefs, fetchHistory),
    summarize, // runs after Parallel completes with merged state
)
```

### `Router`

`Router` dispatches to one of several named steps based on a classifier
function. If the classifier returns a key that has no matching route, `Run`
returns an error.

```go
func Router(
    classify func(context.Context, *WorkflowState) string,
    routes   map[string]WorkflowStep,
) WorkflowStep
```

```
Router(classify, routes)

  Input ──► classify(ctx, state) ──► "technical" ──► routes["technical"]
                                  ──► "general"  ──► routes["general"]
                                  ──► "billing"  ──► routes["billing"]
                                  ──► (unknown)  ──► error
```

```go
router := workflow.Router(
    func(_ context.Context, s *workflow.WorkflowState) string {
        intent, _ := s.Data["intent"].(string)
        return intent
    },
    map[string]workflow.WorkflowStep{
        "technical": technicalHandler,
        "general":   generalHandler,
    },
)
```

If the classifier returns an unknown key the error message is:
`workflow router: no route for key "<key>"`.

### `RetryUntil`

`RetryUntil` runs `body` repeatedly until the `until` predicate returns `true`.
The predicate is checked **before** the first run: if the condition is already
satisfied the body never executes.

```go
func RetryUntil(
    name  string,
    body  WorkflowStep,
    until func(context.Context, *WorkflowState) bool,
) WorkflowStep
```

```
RetryUntil("name", body, until)

  ┌──────────────────────────┐
  │                          │
  ▼                          │
  until(ctx, state)?         │
  ├─ true  ──► return state  │
  └─ false ──► body.Run() ───┘
```

```go
draft := workflow.Step("draft", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    // call an agent, store result
    result, err := draftAgent.Run(ctx, s.Input)
    if err != nil {
        return nil, err
    }
    s.Data["draft"] = result.Text
    s.Data["length"] = len(result.Text)
    return s, nil
})

polished := workflow.RetryUntil(
    "polish-until-long-enough",
    draft,
    func(_ context.Context, s *workflow.WorkflowState) bool {
        n, _ := s.Data["length"].(int)
        return n >= 500
    },
)
```

Always ensure the predicate can eventually become `true`, or use a context
deadline to bound the loop.

### `Conditional`

`Conditional` executes `ifTrue` or `ifFalse` based on a check function. Either
branch may be `nil`, in which case the input state is passed through unchanged.

```go
func Conditional(
    check   func(context.Context, *WorkflowState) bool,
    ifTrue  WorkflowStep,
    ifFalse WorkflowStep,
) WorkflowStep
```

```
Conditional(check, ifTrue, ifFalse)

  check(ctx, state)?
  ├─ true  ──► ifTrue.Run(state)  ──► output
  └─ false ──► ifFalse.Run(state) ──► output
                (or pass-through if nil)
```

```go
cacheHit := workflow.Conditional(
    func(_ context.Context, s *workflow.WorkflowState) bool {
        _, ok := s.Data["cached_result"]
        return ok
    },
    returnCached,  // ifTrue: return the cached answer directly
    callAgent,     // ifFalse: run the agent and cache the result
)
```

---

## 3. Agent as a Workflow Step

Agents are used inside `Step` functions. Create the agent outside the step
(so it is reused across calls) and call `agent.Run()` inside the function body.
Store whatever you need from the result in `Data`.

```go
import (
    "context"

    sdk "github.com/anthropics/anthropic-sdk-go"

    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/providers/anthropic"
    "github.com/axonframework/axon/workflow"
)

// Create the agent once, outside the step.
client := sdk.NewClient() // reads ANTHROPIC_API_KEY
llm := anthropic.New(&client, sdk.ModelClaudeHaiku4_5)
summarizer := kernel.NewAgent(
    kernel.WithModel(llm),
    kernel.WithSystemPrompt("Summarize the following text concisely."),
)

summarizeStep := workflow.Step("summarize", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    result, err := summarizer.Run(ctx, s.Input)
    if err != nil {
        return nil, err
    }
    s.Data["summary"] = result.Text
    return s, nil
})
```

### Plan-capable agents as nodes

An agent with the `plan` capability enabled is a workflow node like any
other — no special adapter needed. The workflow advances when the agent's
`Run` returns, which happens when the agent's plan completes (or the model
terminates early). Plans live **inside** an agent node; the workflow
orchestrates nodes, not individual steps.

```go
import "github.com/axonframework/axon/plan"

tripStep := workflow.Step("trip", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    p := plan.Empty() // agent will draft its own procedure
    agent := kernel.NewAgent(append(
        plan.Enable(p),
        kernel.WithModel(llm),
        kernel.WithSystemPrompt("You are a travel planner."),
    )...)

    result, err := agent.Run(ctx, s.Input)
    if err != nil {
        return nil, err
    }
    s.Data["trip_plan"] = p           // downstream steps can inspect p.Steps / p.Notes
    s.Data["trip_text"] = result.Text
    return s, nil
})

wf := workflow.NewWorkflow(classifyStep, tripStep, notifyStep)
```

For multi-agent pipelines the output of one agent step becomes the input of the
next by writing to and reading from `Data`:

```go
classifyStep := workflow.Step("classify", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    result, err := classifyAgent.Run(ctx, s.Input)
    if err != nil {
        return nil, err
    }
    s.Data["category"] = result.Text
    return s, nil
})

respondStep := workflow.Step("respond", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    category, _ := s.Data["category"].(string)
    prompt := "Category: " + category + "\nQuestion: " + s.Input
    result, err := respondAgent.Run(ctx, prompt)
    if err != nil {
        return nil, err
    }
    s.Data["response"] = result.Text
    return s, nil
})

wf := workflow.NewWorkflow(classifyStep, respondStep)
```

---

## 4. Parallel State Merging

When `Parallel` runs, each child step receives an independent **shallow copy**
of `Data` and `Messages`. The steps run concurrently and cannot see each
other's writes while executing.

After all steps complete (or any one returns an error), results are merged:

1. **Data** — keys from each result's `Data` map are written into the merged
   state in **declaration order**. If two steps write the same key, the step
   declared **last** in the `Parallel(...)` call wins.

2. **Messages** — new messages appended by each step (those beyond the length
   of the original `Messages` slice) are concatenated in declaration order.

3. **Input** — always the original value; parallel steps cannot change it.

```go
// stepA writes Data["score"] = 10
// stepB writes Data["score"] = 20
// stepC writes Data["label"] = "ok"
wf := workflow.Parallel(stepA, stepB, stepC)
// After merge: Data["score"] == 20 (stepB, declared last), Data["label"] == "ok"
```

The merge is deterministic regardless of which step finishes first — order is
always based on position in the `Parallel(...)` call, not on execution timing.

**Practical guidance**: use distinct keys across parallel steps whenever
possible. Same-key conflicts are resolved silently by declaration order, which
can be surprising if the intent was to accumulate rather than overwrite.

---

## 5. Real-World Patterns

### Parallel data prep then agent

Fetch all context concurrently before calling an agent, so the agent has
everything it needs in one round:

```
  ┌─► loadHistory ──┐
  │                  │
  ├─► loadMemories ──┼──► classifyIntent ──► routeToAgent ──► saveHistory
  │                  │
  └─► checkGuard ───┘
       (parallel)         (sequential)        (sequential)      (sequential)
```

```go
wf := workflow.NewWorkflow(
    workflow.Parallel(fetchUser, fetchPrefs, fetchHistory),
    workflow.Step("respond", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
        user, _    := s.Data["user"].(string)
        prefs, _   := s.Data["prefs"].(string)
        history, _ := s.Data["history"].(string)

        prompt := fmt.Sprintf("User: %s\nPrefs: %s\nHistory: %s\nQuestion: %s",
            user, prefs, history, s.Input)

        result, err := responseAgent.Run(ctx, prompt)
        if err != nil {
            return nil, err
        }
        s.Data["response"] = result.Text
        return s, nil
    }),
)
```

### Classify then route to specialized agent

Run an inexpensive classification step first, then dispatch to a specialized
agent using `Router`:

```go
classify := workflow.Step("classify", func(_ context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    result, err := classifyAgent.Run(ctx, s.Input)
    if err != nil {
        return nil, err
    }
    s.Data["intent"] = result.Text // e.g. "billing", "support", "sales"
    return s, nil
})

wf := workflow.NewWorkflow(
    classify,
    workflow.Router(
        func(_ context.Context, s *workflow.WorkflowState) string {
            intent, _ := s.Data["intent"].(string)
            return intent
        },
        map[string]workflow.WorkflowStep{
            "billing": billingAgent,
            "support": supportAgent,
            "sales":   salesAgent,
        },
    ),
)
```

### Retry until quality threshold

Generate a draft, score it, and repeat until the score is high enough:

```go
generate := workflow.Step("generate", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    result, err := draftAgent.Run(ctx, s.Input)
    if err != nil {
        return nil, err
    }
    s.Data["draft"] = result.Text
    return s, nil
})

score := workflow.Step("score", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    draft, _ := s.Data["draft"].(string)
    result, err := scoringAgent.Run(ctx, "Score this on a scale 1-10:\n"+draft)
    if err != nil {
        return nil, err
    }
    s.Data["score"] = result.Text
    return s, nil
})

wf := workflow.RetryUntil(
    "draft-until-quality",
    workflow.NewWorkflow(generate, score),
    func(_ context.Context, s *workflow.WorkflowState) bool {
        score, _ := s.Data["score"].(string)
        return strings.HasPrefix(score, "9") || strings.HasPrefix(score, "10")
    },
)
```

### Multi-agent pipeline

Chain specialized agents so each builds on the previous agent's output:

```go
wf := workflow.NewWorkflow(
    workflow.Step("research", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
        result, err := researchAgent.Run(ctx, s.Input)
        if err != nil {
            return nil, err
        }
        s.Data["research"] = result.Text
        return s, nil
    }),
    workflow.Step("outline", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
        research, _ := s.Data["research"].(string)
        result, err := outlineAgent.Run(ctx, research)
        if err != nil {
            return nil, err
        }
        s.Data["outline"] = result.Text
        return s, nil
    }),
    workflow.Step("write", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
        outline, _ := s.Data["outline"].(string)
        result, err := writerAgent.Run(ctx, outline)
        if err != nil {
            return nil, err
        }
        s.Data["article"] = result.Text
        return s, nil
    }),
)
```

---

See `examples/05-workflow/` for a complete runnable example covering sequential
pipelines, parallel execution, and routing.
