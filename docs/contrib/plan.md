# contrib/plan — Multi-Step Procedures

`contrib/plan` lets the agent follow a fixed sequence of steps defined up front.
The plan is injected into the system prompt every round so the LLM always knows
where it is, and it gets `mark_step` / `add_note` tools to record progress.

**Import:** `github.com/axonframework/axon/contrib/plan`

**See also:** [examples/08-plan/](../../examples/08-plan/) for a runnable demo.

---

## When to use

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

## Creating a plan

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

### Step fields

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Unique identifier for the step; used by the `mark_step` tool |
| `Description` | `string` | What the LLM must do at this step |
| `Status` | `StepStatus` | `pending` / `active` / `done` / `skipped` |
| `NeedsUserInput` | `bool` | Hints to the LLM that it should wait for the user before advancing |
| `CanRepeat` | `bool` | Hints to the LLM that this step may be revisited |

### Step status transitions

```
                 ┌──────────┐
                 │ pending  │
                 └────┬─────┘
                      │ (auto-activate when previous step completes)
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

## Attaching a plan to an agent

```go
import (
    "github.com/axonframework/axon/contrib/plan"
    "github.com/axonframework/axon/kernel"
)

planOpts := plan.Attach(p)

agent := kernel.NewAgent(append(planOpts,
    kernel.WithModel(llm),
    kernel.WithSystemPrompt("You are a hotel booking assistant."),
)...)
```

`plan.Attach(p)` returns a `[]kernel.AgentOption` slice. Spread it into
`kernel.NewAgent` alongside your other options.

---

## How it works

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

```
plan.Attach(p) returns 3 AgentOptions:

  ┌─ OnStart hook ──────────── activates first pending step
  │
  ├─ PrepareRound hook ─────── injects plan text into system prompt
  │                             (updated each round to reflect changes)
  │
  └─ WithTools ─────────────── registers mark_step + add_note tools

Agent loop with plan:

  OnStart: activate step 1
  │
  ├─ Round 0
  │  ├─ PrepareRound: inject plan into system prompt
  │  ├─ LLM reads plan, calls mark_step("gather", "done")
  │  └─ mark_step auto-activates step 2
  │
  ├─ Round 1
  │  ├─ PrepareRound: inject updated plan (step 1 ✓, step 2 active)
  │  ├─ LLM reads plan, does work, calls mark_step("search", "done")
  │  └─ mark_step auto-activates step 3
  │
  └─ ... continues until all steps done
```

---

## mark_step tool

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

## add_note tool

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

## plan.Format

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

Status markers:

```
[✓] gather  — Ask preferences           ← done
[✓] search  — Find flights              ← done
[>] present — Show options to user       ← active (current)
[ ] confirm — Book the selected flight   ← pending
[-] extras  — Add hotel/car              ← skipped
```

---

## Full example

See [`examples/08-plan/main.go`](../../examples/08-plan/main.go) for a
complete runnable example that walks through a 4-step trip booking procedure
with `mark_step`, `add_note`, and `plan.Format` output at each stage.
