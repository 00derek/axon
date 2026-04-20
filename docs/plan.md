# Plans

A **plan** is an agent-authored multi-step procedure. The `plan` package adds
planning as an opt-in capability: the agent gains four tools
(`create_plan`, `append_step`, `mark_step`, `add_note`) and the plan text is
injected into the system prompt every round so the LLM can see its progress.

**Import:** `github.com/axonframework/axon/plan`

**See also:** [`examples/08-plan/`](../examples/08-plan/) for a runnable demo.

---

## 1. Mental Model

Three layers coexist in Axon:

| Layer | Who authors the structure | What it is |
|---|---|---|
| **Tool** | Developer | A single action the LLM can invoke |
| **Plan** | LLM (or developer) | A sequence of steps *inside one agent run* |
| **Workflow** | Developer | A sequence of nodes *across agents and functions* |

A plan lives **inside** one `agent.Run()`. A workflow lives **above** agents
and can include plan-capable agents as nodes. Plans are not workflow nodes —
they are what a single agent does while its node is running.

```
Workflow (developer-authored)
  │
  ├─ Node A: plain function
  ├─ Node B: plan-capable agent  ◄── inside this agent run, a plan
  │                                   coordinates the agent's steps
  └─ Node C: another agent
```

---

## 2. When to Use a Plan

Reach for a plan when:

- The agent needs to coordinate 5+ distinct steps in one conversation.
- You want an **auditable** record of progress (the `Plan` struct is inspectable in Go).
- You want to **resume** across sessions — the plan serializes cleanly and lives on `AgentContext.State`.
- You want the current progress visible to the LLM every round.

Skip it when:

- The flow is 1–3 tool calls. The LLM handles that naturally.
- You already know every step upfront and the code should enforce the order — that's a **workflow**, not a plan.

---

## 3. Two Flows

### Developer-seeded

You know the procedure upfront. Pass steps to `plan.New`:

```go
import (
    "github.com/axonframework/axon/kernel"
    "github.com/axonframework/axon/plan"
)

p := plan.New("hotel-booking", "Book a hotel room",
    plan.Step{Name: "gather", Description: "Ask for dates and budget", NeedsUserInput: true},
    plan.Step{Name: "search", Description: "Search hotels matching preferences"},
    plan.Step{Name: "confirm", Description: "Confirm the selected hotel", NeedsUserInput: true},
)

agent := kernel.NewAgent(append(
    plan.Enable(p),
    kernel.WithModel(llm),
)...)
```

The agent activates the first pending step on start and uses `mark_step` to
advance through the fixed sequence.

### Agent-seeded

The agent drafts its own plan. Start empty:

```go
p := plan.Empty()

agent := kernel.NewAgent(append(
    plan.Enable(p),
    kernel.WithModel(llm),
    kernel.WithSystemPrompt("You are a travel planner."),
)...)

result, err := agent.Run(ctx, "Help me plan a trip to Kyoto in April")
```

In round 0 the LLM reads the empty-plan placeholder in the system prompt,
reasons about the user's goal, and calls `create_plan` to propose a list of
steps. Subsequent rounds execute the agent's own procedure.

### Which to choose

| If… | Use |
|---|---|
| The sequence is the same every time | `plan.New(...)` — or a `workflow` if code should own it |
| Steps depend on the user's goal | `plan.Empty()` — let the LLM decide |
| You want to nudge the LLM toward a structure but keep flexibility | Either — the agent can always `append_step` mid-flight |

---

## 4. Step Fields

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Unique identifier used by `mark_step` |
| `Description` | `string` | What the LLM must do at this step |
| `Status` | `StepStatus` | `pending` / `active` / `done` / `skipped` |
| `NeedsUserInput` | `bool` | Signals the LLM it should wait for the user |
| `CanRepeat` | `bool` | Signals the step may run more than once |

### Status transitions

```
                 ┌──────────┐
                 │ pending  │
                 └────┬─────┘
                      │ (auto-activated when previous step completes)
                      ▼
                 ┌──────────┐
          ┌──────│  active  │──────┐
          │      └──────────┘      │
          ▼                        ▼
    ┌──────────┐            ┌──────────┐
    │   done   │            │ skipped  │
    └──────────┘            └──────────┘
          │                        │
          └───────┬────────────────┘
                  ▼
        next pending step auto-activates
```

---

## 5. The Four Tools

`plan.Enable` registers four tools. The LLM picks from them depending on
what the plan currently looks like.

### `create_plan`

Propose a plan. Call only when no plan exists yet.

```json
{
  "name": "trip-planner",
  "goal": "Help the user plan a trip",
  "steps": [
    {"name": "gather", "description": "Ask for destination and dates"},
    {"name": "search", "description": "Find flights", "needs_user_input": false}
  ]
}
```

Errors if the plan already has steps. After creation the first step is
auto-activated.

### `append_step`

Add a step at the end mid-flight. Useful when the agent discovers work it
didn't anticipate.

```json
{"name": "confirm", "description": "Confirm the booking", "needs_user_input": true}
```

The new step starts as `pending`.

### `mark_step`

Update a step's status.

```json
{"step_name": "search", "status": "done"}
```

`status` is one of `done`, `skipped`, or `active`. When marked `done` or
`skipped`, the next pending step auto-activates.

### `add_note`

Store a key-value pair that persists in `p.Notes` and is visible in the
formatted plan every round.

```json
{"key": "destination", "value": "Kyoto, Japan"}
```

Access notes in Go after the run:

```go
destination := p.Notes["destination"]
```

---

## 6. What `Enable` Wires In

`plan.Enable(p)` returns `[]kernel.AgentOption`. Spread into `NewAgent`:

```go
agent := kernel.NewAgent(append(plan.Enable(p), kernel.WithModel(llm))...)
```

Under the hood it installs three pieces:

```
plan.Enable(p) returns:

  ┌─ OnStart hook ──────────── stashes plan in ctx.State["plan"];
  │                              activates first pending step (if any)
  │
  ├─ PrepareRound hook ─────── renders Format(p) into the system prompt
  │                              every round, plus an enforcement directive
  │                              urging the LLM to finish all steps
  │
  └─ WithTools ─────────────── registers create_plan, append_step,
                                 mark_step, add_note
```

---

## 7. Storage: `AgentContext.State`

The active plan is stashed on `AgentContext.State` under the key `plan.StateKey`
(which is the literal `"plan"`). This makes the plan inspectable from any hook
and serializable alongside conversation history.

```go
kernel.OnFinish(func(tc *kernel.TurnContext) {
    if v, ok := tc.AgentCtx.State[plan.StateKey]; ok {
        if p, ok := v.(*plan.Plan); ok {
            // Persist p for resume, or log its progress.
        }
    }
})
```

To resume, load the persisted plan and pass it back to `plan.Enable` on the
next run. The `OnStart` hook will activate the first pending step it finds,
so resumption works out of the box.

---

## 8. Composition with Workflows

A plan-capable agent is a `workflow.Step` via a trivial adapter. No special
API needed — just call `agent.Run` inside the step body:

```go
tripStep := workflow.Step("trip", func(ctx context.Context, s *workflow.WorkflowState) (*workflow.WorkflowState, error) {
    p := plan.Empty()
    agent := kernel.NewAgent(append(plan.Enable(p), kernel.WithModel(llm))...)

    result, err := agent.Run(ctx, s.Input)
    if err != nil {
        return nil, err
    }
    s.Data["trip_plan"] = p       // inspectable by later steps
    s.Data["trip_text"] = result.Text
    return s, nil
})

wf := workflow.NewWorkflow(classifyStep, tripStep, notifyStep)
```

The workflow waits for `agent.Run` to return — which happens when the agent's
plan completes (or the model terminates early). Plans nest cleanly inside
nodes; they do not replace nodes.

---

## 9. Format Output

`plan.Format(p)` renders the plan for debugging or logging. `plan.Enable`
calls it automatically before each round.

```
## Current Plan: trip-booking
Goal: Help the user book a trip from NYC to Paris

[✓] gather — Ask the user about travel dates and budget
[✓] search — Search for flights matching preferences
[>] present — Present the top 3 options (needs user input)
[ ] confirm — Finalize booking (needs user input)

Notes:
- best_flight: StarWings $189, departs 6:15 AM
- budget: under $300
```

Status markers: `[✓]` done, `[>]` active, `[ ]` pending, `[-]` skipped.

For an empty plan (agent-seeded, before `create_plan` is called), Format
renders a placeholder directing the LLM to call `create_plan`.

---

## 10. Enforcement

Enforcement is **prompt-based**. Every round `Enable`'s `PrepareRound` hook
appends a directive to the system prompt:

> Follow the plan. Call create_plan first if no plan exists, then use
> mark_step to advance as you finish each step. Do not end the conversation
> until every step is done or skipped.

Modern LLMs respect this reliably. If the model stops before finishing,
check `p.IsComplete()` and decide whether to re-run the agent with the
partially-completed plan.

```go
if !p.IsComplete() {
    // LLM bailed early — prompt the user or re-run with a nudge.
}
```

Strict enforcement (forcing the kernel loop to continue when the LLM returns
text-only with an incomplete plan) would require a new kernel hook and is
deliberately out of scope.

---

## 11. Full Example

See [`examples/08-plan/main.go`](../examples/08-plan/main.go) for a runnable
demo of the agent-seeded flow:

1. Start with `plan.Empty()`.
2. Round 0: agent calls `create_plan` to draft its own procedure.
3. Rounds 1–N: agent executes via `mark_step` and records notes via `add_note`.
4. Mid-flight: agent calls `append_step` to add work it didn't anticipate.
5. Final round: agent returns a natural-language summary.
