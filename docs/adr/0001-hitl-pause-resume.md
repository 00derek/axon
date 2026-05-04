# ADR-0001: Human-in-the-Loop — Pause/Resume Primitives

- **Status:** Proposed
- **Date:** 2026-05-03
- **Issue:** [#14](https://github.com/00derek/axon/issues/14)

## Context

A turn in axon is one call to `agent.Run(ctx, input)` that runs a loop of rounds:
each round generates from the LLM and (optionally) executes tool calls in parallel
before feeding their results back. Today the loop runs to completion or returns an
error. There is no first-class way for a turn to **stop, hand control to a human,
and continue from where it left off** — either in-process (caller answers a
question) or out-of-process (a webhook arrives an hour later).

Human-in-the-loop (HITL) interactions are an uncomfortable fit for the current
shape because they break two assumptions:

1. **A turn is a single goroutine call.** The loop owns its stack; nothing in the
   public API lets it yield.
2. **`AgentContext` lives in memory for the duration of a turn.** Its
   `Messages`, `tools`, and `State` map are not designed to survive a
   serialize/restore cycle or a process restart.

This ADR enumerates the HITL scenarios axon should eventually support, classifies
each by the mechanic it requires, sketches a primitive for each mechanic, calls
out the trade-offs, and lands on a recommendation for how the work should ship.

No code is proposed here — this document scopes a future implementation issue.

## Domain vocabulary used here

- **Turn** — one `agent.Run(...)` call.
- **Round** — one LLM generation inside a turn, plus any tool calls it triggers.
- **`AgentContext`** — the live state passed through every hook: `Messages`,
  the tool registry, and the `State map[string]any` capabilities stash typed
  values into.
- **Hook** — `OnStart` / `OnFinish` / `PrepareRound` / `OnRoundFinish` /
  `OnToolStart` / `OnToolEnd`.
- **Capability** — opt-in package (like `plan/`) that contributes tools, hooks,
  and `State` entries via an `Enable(...) []AgentOption` constructor.

## Scenarios

| # | Scenario | Trigger point | Latency | Process survives? |
|---|---|---|---|---|
| 1 | Confirmation before irreversible tool call (send email, charge card, delete row) | `OnToolStart` | ms–s | usually yes |
| 2 | Clarification of ambiguous user input | mid-round, before a tool call or before the model commits | s | usually yes |
| 3 | Approval gate for high-cost / high-risk action | `OnToolStart` or after a planning round | s–min | maybe |
| 4 | Async tool — pause until external system signals completion via webhook | inside a tool's `Execute` | min–days | **no** |
| 5 | Compliance review — human signs off on the agent's output before it's returned | `OnFinish`, before `Result` is returned to caller | min–hours | **no** |
| 6 | Multi-turn structured intake — agent walks user through a form | between rounds, repeated | seconds per step, hours overall | **no** |

## Classification: two mechanics

The scenarios collapse into two distinct mechanics. Conflating them is the main
design risk.

### Mechanic A — Synchronous pause-and-ask

The turn yields control back up the stack without the goroutine dying. The
caller (an HTTP handler, a CLI, a workflow node) supplies an answer and the same
`Run` call resumes. Nothing has to serialize. The process can't die between
question and answer — if it does, the turn is lost and that's acceptable.

Fits: scenarios **1, 2, 3** when the human is online and answering in seconds.

### Mechanic B — Asynchronous suspend-and-resume

The turn ends. `AgentContext` (messages, tool registry identity, `State`) is
serialized into durable storage. A **resume token** is issued to the caller. A
second, future call to `agent.Resume(ctx, token, payload)` rehydrates the
context and continues the loop where it stopped. The original goroutine is
gone; the original process may be gone.

Fits: scenarios **4, 5, 6**, and **3** when approval may take hours.

The same scenario can fall in either bucket depending on deployment — a
confirmation prompt is sync in a CLI demo, async behind a Slack approval bot.
The primitive should let the **caller** pick; the agent author shouldn't have to
fork the agent for each.

## Proposed primitive shapes

### Sync — `kernel.Interrupt`

Any hook or tool may return a sentinel `*Interrupt` value. The kernel detects it,
unwinds the loop cleanly (no further rounds, no `OnFinish` for `Result`), and
returns it to the caller from `Run`. The caller answers and re-enters with
`Resume` on **the same `Agent`** in the same process.

Sketch (illustrative — not committing to exact names):

```go
type Interrupt struct {
    Reason  string         // "confirm_tool", "clarify", "approve", ...
    Prompt  string         // human-facing question
    Payload any            // tool-call params, plan step, etc.
}

// Returned from a hook by stashing on AgentContext, or from a tool by
// returning (nil, &Interrupt{...}). The kernel surfaces it as a typed error
// from Run so callers can type-assert without breaking the err contract.
func (a *Agent) Run(ctx context.Context, input string) (*Result, error)
//   ─► returns (*Result, *Interrupt) wrapped — caller checks errors.As

// Caller answers and resumes the same in-memory turn:
func (a *Agent) Resume(ctx context.Context, answer Answer) (*Result, error)
```

Properties:

- `AgentContext` stays in memory; nothing serializes.
- The `Run`/`Resume` pair lives in the same process; if the process dies, the
  turn is lost.
- Implementable today with a small kernel change: a typed error path that
  preserves the in-flight `AgentContext` on the `Agent` (or on a returned
  handle) until `Resume` is called.

### Async — `kernel.Suspension` + `Resumer`

When a tool or hook needs to wait for a webhook (or any out-of-process signal)
it returns a `*Suspension`. The kernel:

1. Marks the in-progress round as suspended.
2. Hands the live `AgentContext` to a pluggable `Resumer` for serialization.
3. Returns a `ResumeToken` (opaque string) to the caller.

Later, an HTTP handler or webhook receiver calls
`agent.Resume(ctx, token, payload)`. The kernel uses the same `Resumer` to
rehydrate `AgentContext` and re-enters the loop at the suspended round with
`payload` substituted for the missing tool result (or hook decision).

Sketch:

```go
type Suspension struct {
    Reason string
    // Where in the turn we're suspended — kernel fills these in.
    RoundNumber int
    ToolCallID  string // non-empty if suspended during a tool call
}

type ResumeToken string

type Resumer interface {
    // Save returns a token the caller can use to resume later.
    Save(ctx context.Context, snapshot *Snapshot) (ResumeToken, error)
    // Load rehydrates a snapshot from a token.
    Load(ctx context.Context, token ResumeToken) (*Snapshot, error)
    // Delete removes a completed snapshot.
    Delete(ctx context.Context, token ResumeToken) error
}

type Snapshot struct {
    Messages    []Message       // already JSON-serializable today
    State       map[string]any  // see "State serialization" below
    ToolNames   []string        // names only — tools are re-attached on resume
    Round       int
    Suspension  Suspension
}

// Wired in like any other AgentOption.
func WithResumer(r Resumer) AgentOption
```

Properties:

- The process can die. The `Resumer` (in-memory for tests, SQL/Redis/etc. for
  prod) owns durability.
- The **caller** owns the resume token — axon does not assume a particular
  transport (HTTP, queue, Slack callback). Callers correlate token ↔ external
  signal.
- Tools are **not** serialized. On resume the caller constructs a fresh `Agent`
  with the same tools registered; the snapshot's `ToolNames` is checked against
  the registry and the kernel rejects mismatches loudly. This avoids the
  unsolvable problem of serializing closures and live SDK clients.

## Trade-offs

### In-process pause vs. persisted suspend

| | Sync `Interrupt` | Async `Suspension` |
|---|---|---|
| Implementation cost | Small — a typed return path | Large — snapshot format, `Resumer` interface, tool re-attachment, token issuance |
| Survives process restart | No | Yes |
| Latency tolerated | seconds | unbounded |
| State serialization required | No | Yes |
| Best fit | CLI confirmation, interactive clarify | Webhook tools, compliance review, async approval |

Building only sync first is tempting — it's enough for scenarios 1 and 2 — but
scenarios 4 and 5 are the ones that actually require new primitives. Sync alone
risks getting baked into capability code that then can't grow into async
without rewrites.

### Who owns the resume token?

Three options:

1. **Caller owns it.** Kernel returns the token; caller persists the
   token-to-context-binding (e.g. in a `pending_approvals` table keyed by Slack
   message ID). **Recommended.** Keeps axon transport-agnostic.
2. **Resumer owns it.** Caller hands an opaque `correlation_id` to the
   `Resumer`, which mints and stores the token. Cleaner API, but locks token
   semantics into the `Resumer` interface.
3. **Kernel mints it but caller chooses storage.** Hybrid.

Option 1 maps well to axon's existing "BYO storage" stance (`Resumer` is just
storage for the snapshot, not for the routing).

### How `AgentContext.State` survives serialization

`State` is `map[string]any` and capabilities (like `plan`) put **typed structs**
in it. Three viable strategies:

1. **JSON tag contract.** Capabilities promise that whatever they put in
   `State` is `json.Marshal`-safe. The snapshot encodes `State` as
   `map[string]json.RawMessage`; on `Load`, each capability's `Enable(...)`
   re-registers a decoder for its key. The `plan` package's `Plan` struct is
   already `json`-tagged and round-trips cleanly.
2. **Capability-supplied codec.** Each capability registers a
   `Codec[K]{Marshal, Unmarshal}` with the kernel. More flexible (proto, msgpack)
   but more API surface.
3. **No persistence of `State`.** Force capabilities to externalize their state
   (database, Redis) and use `State` as a cache only. Pushes complexity onto
   capability authors but keeps the kernel small.

(1) is the lowest-friction starting point and mirrors what `plan` already does
de facto. (2) can replace it later without breaking callers if needed.

Tools and hooks are **not** persisted — they're code, not data. A snapshot
records tool **names** only; the resuming `Agent` must register matching tools
or `Resume` errors. This is the same compromise serverless workflow engines
make.

### Sync vs. async — which scenarios actually need which?

| Scenario | Sync sufficient? | Notes |
|---|---|---|
| 1. Confirmation | Yes | Often async in production |
| 2. Clarification | Yes | Almost always sync |
| 3. Approval | Sometimes | Async needed when reviewer is offline |
| 4. Async tool / webhook | **No** | Async is the whole point |
| 5. Compliance review | **No** | Reviewer is human-time |
| 6. Structured intake | Either | Sync if same session, async across sessions |

## Recommendation

**Ship HITL as a capability package, not a kernel feature — but with a small
kernel hook to make it possible.**

The kernel learns one new thing: how to **unwind** a turn cleanly from any hook
or tool, and re-enter it. Concretely, that's a typed signal value (call it
`Interrupt`) that any hook or tool can return, plus an `Agent.Resume(...)`
entry point. That's it. No notion of webhooks, tokens, or storage in the
kernel.

Everything else — the snapshot format, the `Resumer` interface, the
JSON-tag contract for `State`, the helpers for building "wait for webhook"
tools, the conventions for issuing resume tokens — lives in a `hitl/` package
that follows the existing capability pattern (`hitl.Enable(...)` returns
`[]kernel.AgentOption`, mirroring `plan/`).

Why this split:

- The kernel stays small. The pattern from #7 (kernel hosts mechanism, plan
  hosts policy) should hold for HITL too.
- Multiple HITL strategies can coexist as separate capabilities or sub-packages
  (`hitl/sync`, `hitl/webhook`, `hitl/queue`) without bloating the kernel.
- Capabilities can compose: `plan` + `hitl` together gives "plans whose steps
  may be human-gated" without either package knowing about the other —
  they coordinate through `AgentContext.State`.
- The existing `Resumer` storage backends (memory, sql, redis) become
  user-supplied implementations, same as how `interfaces.History` is
  user-supplied today.

Phasing:

1. **Kernel:** add `Interrupt` return path from hooks and tools, add
   `Agent.Resume`. (Small change. Unblocks sync HITL scenarios 1, 2, 3.)
2. **`hitl/` capability:** snapshot format, `Resumer` interface, in-memory
   reference implementation, helpers for the common "confirm tool call"
   pattern.
3. **Async tool helper:** `hitl.WebhookTool(...)` that wraps a tool to
   suspend-on-call and resume-on-payload. Covers scenario 4.
4. **Compliance review hook:** `hitl.ReviewBeforeReturn(...)` that suspends in
   `OnFinish`. Covers scenario 5.
5. **Structured intake:** documented pattern composing `plan` + `hitl`. No new
   primitives. Covers scenario 6.

## Open questions for the maintainer

1. **Naming.** `Interrupt` clashes with Go's signal vocabulary; `Pause` reads
   better but loses the "the loop genuinely stopped" connotation. Other
   options: `Yield`, `Halt`. Worth bikeshedding before kernel work starts.
2. **Resume entry point.** Is `Agent.Resume(ctx, ...)` correct, or should
   resume be a free function `kernel.Resume(ctx, agent, snapshot, payload)`?
   The free-function form is friendlier for the async case where the resuming
   process didn't construct the original `Agent` instance.
3. **Cancellation semantics during suspension.** If a turn is suspended for
   six hours, what does `ctx.Done()` mean? The original `ctx` is gone; the
   resuming call brings a new one. Probably the snapshot should record an
   absolute deadline (or none) and ignore the original ctx — but worth
   stating explicitly before code lands.
4. **Tool re-attachment strictness.** When resuming, should missing tools
   error, warn, or silently drop? Default to error; allow `WithLooseResume()`
   for migration scenarios.
5. **Streaming + suspend.** `kernel/agent_stream.go` exists. Does suspending
   mid-stream make sense, or should HITL only apply to non-streaming turns
   in v1? Recommend **non-streaming only** for v1 — streaming + suspend is a
   separate, harder problem.
6. **Plan + HITL coordination.** Should `hitl` know about `plan` enough to
   mark a step "blocked on human" automatically, or is that the agent
   author's job via existing `plan` tools? Lean toward keeping them
   independent and documenting the composition pattern.
