# Command Center Design Document

## 1. Executive Summary

The Command Center is a pure workflow orchestrator that decouples **strategy** (scheduling) from **execution** (components) through event-driven communication and copy-on-write state management. It replaces the monolithic V1 Chain executor with a composable, concurrent system where a pluggable `Scheduler` decides *what* to run and `Component` implementations decide *how* to run it.

**Primary file:** `lib/command_center/command_center.go`

The system follows an Observe-Reason-Act cycle: user input enters the event loop, the Scheduler observes a `SchedulerContext` snapshot, produces `TaskDefinition` declarations, the CommandCenter executes those tasks as goroutines, state is reconciled via copy-on-write, and result events are fed back into the loop for the next scheduling decision.

---

## 2. Architecture & Design Philosophy

### Five Design Principles

1. **Pure Orchestration** -- The CommandCenter contains zero business logic. It manages lifecycle, state, and routing. All domain work lives in Components.
2. **Pluggable Architecture** -- Schedulers and Components are interfaces. Swap scheduling strategy without touching execution code, and vice versa.
3. **Explicit State Management** -- Copy-on-write isolation: each task gets a deep copy of global state, modifies it locally, then merges only owned frames back. No shared mutable state during execution.
4. **Event-Driven Communication** -- Components communicate exclusively through typed Events flowing through a buffered channel. The Scheduler is a pure function of events and state.
5. **High-Concurrency Design** -- Multiple tasks run as independent goroutines. Streams (fan-in) and Signals (fan-out) provide safe concurrent data flow and coordination.

### Hierarchy

```
CommandCenter (Orchestrator)
  |
  +-- Scheduler (Strategy: "what to run next")
  |     |
  |     +-- LLMScheduler    (dynamic, LLM-driven)
  |     +-- GraphScheduler   (static DAG)
  |     +-- ChainScheduler   (V1 compatibility)
  |
  +-- Component (Work: "how to execute")
  |     |
  |     +-- ToolChatComponent, ChatComponent, SetupComponent, ...
  |
  +-- Streams (FanInRegistry: data flow)
  +-- Signals (FanOutRegistry: coordination)
```

### Observe-Reason-Act Cycle

```
                        +------------------+
    User Input -------> | StartInteraction |
                        +--------+---------+
                                 |
                                 v
                      +----------+----------+
                      |    Event Loop       |
                      |  (processEvents)    |
                      +----------+----------+
                                 |
               +-----------------+-----------------+
               |                                   |
               v                                   |
   +-----------+-----------+                       |
   | Build SchedulerContext |                       |
   | (state + tasks +      |                       |
   |  events snapshot)     |                       |
   +-----------+-----------+                       |
               |                                   |
               v                                   |
   +-----------+-----------+                       |
   | Scheduler.Schedule()  |  <-- OBSERVE + REASON |
   | returns TaskDefinitions                       |
   +-----------+-----------+                       |
               |                                   |
               v                                   |
   +-----------+-----------+                       |
   | Execute tasks as      |  <-- ACT             |
   | goroutines            |                       |
   +-----------+-----------+                       |
               |                                   |
               v                                   |
   +-----------+-----------+                       |
   | Copy-on-write state   |                       |
   | reconciliation        |                       |
   +-----------+-----------+                       |
               |                                   |
               v                                   |
   +-----------+-----------+                       |
   | Emit result Events    +--------->-------------+
   +----------+------------+    (fed back into loop)
              |
              v
        isFinished()?
         yes => return
```

---

## 3. Core Types

All types are in package `commandcenter`.

### CommandCenter

```go
// File: lib/command_center/command_center.go

type CommandCenter struct {
    scheduler  Scheduler
    components map[string]Component
    model      lynx.LLM
    state      *lynx.State            // Global state for the entire execution

    eventChan  chan eventBatch         // Buffered channel, capacity 100
    eventLog   []Event                // Full audit trail

    streams    *FanInRegistry
    signals    *FanOutRegistry
    bufferSize int                    // Default buffer size for streams: 100
    tasks      map[string]*TaskRun
    mu         sync.RWMutex
}
```

### Event

```go
// File: lib/command_center/command_center.go

type Event struct {
    Type      EventType      `json:"type"`
    Data      map[string]any `json:"data,omitempty"`
    Timestamp time.Time      `json:"timestamp"`
    source    string         `json:"-"`

    // Tracing linkage
    spanID  string
    traceID string
}
```

### eventBatch

```go
// File: lib/command_center/command_center.go

type eventBatch struct {
    Source  string
    Events  []Event
    spanID  string
    traceID string
    span    tracing.Span
}
```

### TaskDefinition

```go
// File: lib/command_center/scheduler.go

type TaskDefinition struct {
    ID          string        `json:"id"`
    Component   Component     `json:"-"`
    ParentTasks []string      `json:"parent_tasks,omitempty"`
    Attempts    int           `json:"attempts"`     // TODO: not yet used
    Timeout     time.Duration `json:"timeout"`

    // Tracing context for causality tracking
    CreatedBySchedulerSpanID string `json:"created_by_scheduler_span_id,omitempty"`
    SchedulerTraceID         string `json:"scheduler_trace_id,omitempty"`
}
```

### TaskRun

```go
// File: lib/command_center/scheduler.go

type TaskRun struct {
    Status     Status          `json:"status"`
    Definition *TaskDefinition `json:"definition,omitempty"`
    StartTime  time.Time       `json:"start_time"`
    EndTime    time.Time       `json:"end_time"`

    taskSpanID string          `json:"-"`
    traceID    string          `json:"-"`
    mu         sync.Mutex      `json:"-"`
}
```

### Status

```go
// File: lib/command_center/scheduler.go

type Status string

const (
    StatusUnknown   Status = "unknown"
    StatusPending   Status = "pending"
    StatusRunning   Status = "running"
    StatusSucceeded Status = "succeeded"
    StatusFailed    Status = "failed"
    StatusInvalid   Status = "invalid"
)
```

### SchedulerContext

```go
// File: lib/command_center/scheduler.go

type SchedulerContext struct {
    State           *lynx.State          `json:"-"`
    Tasks           map[string]*TaskRun  `json:"tasks"`
    Components      map[string]Component `json:"components"`
    Events          []Event              `json:"events"`
    ProcessedEvents []Event              `json:"processed_events"`
    mu              sync.RWMutex         `json:"-"`
}
```

### ComponentCapability & ResourceRequirement

```go
// File: lib/command_center/component.go

type ComponentCapability struct {
    Name               string                             `json:"name"`
    Description        string                             `json:"description"`
    StreamRequirements map[Stream]ResourceRequirement     `json:"-"`
    SignalRequirements map[SignalName]ResourceRequirement  `json:"-"`
}

type ResourceRequirement struct {
    IsRead   bool `json:"is_read"`   // true = read/wait, false = write/broadcast
    Required bool `json:"required"`  // true = must exist with counterparts
}
```

### EventTypes Hierarchy

```go
// File: lib/command_center/scheduler.go

var EventTypes = eventTypesStruct{
    Start: "start",
    Task: taskEvents{
        Succeeded: "task.succeeded",
        Failed:    "task.failed",
    },
    Component: componentEvents{
        Error:     "component.error",
        Succeeded: "component.succeeded",
    },
    Tool: toolEvents{
        Call:     "tool.call",
        Response: "tool.response",
    },
    Teardown: teardownEvents{
        HandlerError: "teardown.handler_error",
    },
}
```

### Stream & Signal Constants

```go
// File: lib/command_center/scheduler.go

type Stream string
const (
    StreamGlobal  Stream = "global"
    StreamChat    Stream = "chat"
    StreamTools   Stream = "tools"
    StreamIntents Stream = "intents"
    StreamBytes   Stream = "bytes"
)

type SignalName string
const (
    SignalGuardrailsComplete SignalName = "guardrails.complete"
    SignalShutdown           SignalName = "shutdown"
)
```

---

## 4. Component System

### Component Interface

```go
// File: lib/command_center/component.go

type Component interface {
    Execute(ctx context.Context, state *lynx.State, access *ScopedComponentAccess) (*lynx.State, []Event, error)
    Capability() ComponentCapability
}
```

Components are **stateless**. They receive an isolated copy of state, perform work, and return modified state plus events. They never maintain internal state between executions.

### ScopedComponentAccess -- Capability-Based Authorization

```go
// File: lib/command_center/component.go

type ScopedComponentAccess struct {
    componentName string
    capability    ComponentCapability
    streams       *FanInRegistry
    signals       *FanOutRegistry
}
```

Components can **only** access streams and signals they declared in their `Capability()`. Attempting to read a stream not declared as `IsRead: true` returns an authorization error.

**Methods:**

| Method | Purpose |
|--------|---------|
| `GetReader(ctx, Stream) (<-chan []byte, error)` | Claim exclusive reader for a declared read-stream |
| `ReleaseReader(logger, Stream) error` | Release exclusive reader claim |
| `GetWriter(logger, Stream) (StreamWriter, error)` | Get writer for a declared write-stream |
| `WaitForSignal(logger, SignalName) error` | Block until a declared read-signal fires |
| `BroadcastSignal(logger, SignalName, data any) error` | Fire a declared write-signal to all waiters |
| `Shutdown(ctx)` | Broadcast shutdown, close streams, close signals |

---

## 5. Built-in Components

### 5.1 ToolChatComponent

**File:** `lib/command_center/components.go`
**Purpose:** Full LLM interaction with concurrent text streaming and tool execution.

**Capability:**
```go
StreamRequirements: {
    StreamChat:    {IsRead: false, Required: true},   // writes chat messages
    StreamIntents: {IsRead: false, Required: false},  // writes intents
    StreamTools:   {IsRead: false, Required: false},  // writes tool calls
}
SignalRequirements: {
    SignalShutdown: {IsRead: true, Required: true},
}
```

**Execution flow:**
1. Call `GenerateStreamCallTools` to get streaming output channels.
2. Capture a single `aiResponseTimestamp` for all AI messages (text + tool calls) to maintain chronological ordering.
3. Launch concurrent goroutines via `errgroup`: chat text processing, intent forwarding, tool call processing, tool response processing, metrics collection.
4. Chat text chunks are streamed to `StreamChat` and accumulated into a final `AIMessage`.
5. Tool calls and responses are paired by `ToolCallID` and pushed to state as message pairs.
6. Returns `component.succeeded` event with `tool_count` so the scheduler can decide whether to iterate.

**Key detail:** Uses `context.WithCancelCause` for shutdown signal handling -- the component can exit even if streams are slow.

---

### 5.2 ChatComponent

**File:** `lib/command_center/components.go`
**Purpose:** Text-only LLM generation without tool calling. Used for summarization or final responses.

**Capability:**
```go
StreamRequirements: {
    StreamChat: {IsRead: false, Required: true},
}
SignalRequirements: {
    SignalShutdown: {IsRead: true, Required: true},
}
```

**Execution flow:**
1. Get full message history from state via `state.Context.GetContext()`.
2. Call `Model.GenerateStream` for text-only streaming.
3. Stream chunks to `StreamChat` writer and accumulate into `AIMessage`.
4. Push final message to state.
5. Returns `component.succeeded` with `tool_count: 0`.

---

### 5.3 ToolComponent

**File:** `lib/command_center/components.go`
**Purpose:** Tool-only execution. Calls LLM with forced tool use -- no conversational text output.

**Struct fields:**
```go
type ToolComponent struct {
    Model            lynx.LLM
    ForceToolUse     bool // Require at least one tool call
    MaxToolCalls     int  // Maximum tool calls per execution
    SystemPromptOnly bool // Only use system prompts, no persona
}
```

**Capability:**
```go
Name: "ToolOnly"
SignalRequirements: {
    SignalShutdown: {IsRead: true, Required: true},
}
```

**Execution flow:**
1. Call `GenerateCallTools` (non-streaming) with `WithForcedToolCall(true)`.
2. Push tool call + response message pairs to state.
3. Returns `component.succeeded` with content and tool_responses in event data.

---

### 5.4 MissionManagerComponent

**File:** `lib/command_center/components.go`
**Purpose:** Multi-step planning via todo-list semantics. Decomposes complex requests into structured missions and steps.

**Capability:** Inherits from `ToolComponent`.
```go
Name: "MissionManager"
Description: "Helps plan tasks and missions via todo-list semantics."
```

**Execution flow:**
1. Initialize `state.Missions` if nil.
2. Enable planner tools (NewMission, AddSteps) on state.
3. Push a temporary system prompt for mission planning.
4. Delegate to `ToolComponent.Execute` with forced tool use and max 10 tool calls.
5. Disable planner tools and remove temporary prompt.
6. Returns `component.succeeded` with `mission_count`.

---

### 5.5 SetupComponent

**File:** `lib/command_center/components.go`
**Purpose:** Runs initial state setters to configure the execution environment from user input. Typically the first component to execute.

**Struct fields:**
```go
type SetupComponent struct {
    StateSetters []lynx.StateSetter
}
```

**Capability:**
```go
Name: "Setup"
Description: "Runs initial state setters based on user input."
// No stream or signal requirements
```

**Execution flow:**
1. Get current turn input via `state.Context.GetCurrentInput()`.
2. Run `SuggestTraversal` on **all** state setters **concurrently** via `errgroup`.
3. Execute the resulting traversals **sequentially** (order matters for state mutation).
4. Returns `component.succeeded`.

---

### 5.6 TearDownComponent

**File:** `lib/command_center/components.go`
**Purpose:** Handles end-of-interaction cleanup: persistence, stream draining, and coordinated shutdown.

**Struct fields:**
```go
type TearDownComponent struct {
    PostInteractionHandlers []InteractionHandler
    ShutdownStreams         []Stream
    StreamRegistry          *FanInRegistry
}
```

**Capability:**
```go
Name: "Finish"
Description: "Finish the interaction, run cleanup and persistence."
SignalRequirements: {
    SignalShutdown: {IsRead: false, Required: true},  // Broadcasts shutdown
}
```

**Execution flow:**
1. Build `memory.Interaction` from current interaction messages.
2. Run all `PostInteractionHandlers` (e.g., MemStore persistence, MLflow logging). Handler errors produce `teardown.handler_error` events but do not fail the component.
3. For each stream in `ShutdownStreams`: close the writer, then wait (up to 2s) for reader to release (drain).
4. Broadcast `SignalShutdown` to all waiting components.
5. Returns `component.succeeded`.

---

### 5.7 TextProcessorComponent

**File:** `lib/command_center/components.go`
**Purpose:** Processes raw chat text through configurable pipelines (sentence chunking, emoji removal, TTS preparation).

**Struct fields:**
```go
type TextProcessorComponent struct {
    ReadStream    Stream
    WriteStream   Stream
    TextPipelines []streams.Pipeline[string, []byte]
}
```

**Capability:**
```go
Name: "TextProcessor"
StreamRequirements: {
    ReadStream:  {IsRead: true, Required: true},
    WriteStream: {IsRead: false, Required: true},
}
SignalRequirements: {
    SignalShutdown: {IsRead: true, Required: true},
}
```

**Execution flow:**
1. Claim reader on `ReadStream`, get writer for `WriteStream`.
2. Convert `[]byte` channel to `string` channel.
3. If single pipeline: direct `Process()`. If multiple: `streams.FanThrough` with concurrency.
4. Forward processed bytes to `WriteStream` with retry on timeout (100ms timeout, retry with 100ms sleep).
5. Listen for shutdown signal concurrently.

---

### 5.8 StreamGatingComponent

**File:** `lib/command_center/guardrails_components.go`
**Purpose:** LLM-based input guardrails. Validates user input and gates the text byte stream (OPEN/CLOSED).

**Struct fields:**
```go
type StreamGatingComponent struct {
    Model         lynx.LLM
    GuardrailType string // "maximum", "tampering", "toxic", "profanity", "sensitive", "social_engineering"
}
```

**Capability:**
```go
Name: "StreamGating"
StreamRequirements: {
    StreamBytes:  {IsRead: true, Required: true},
    StreamGlobal: {IsRead: false, Required: true},
}
SignalRequirements: {
    SignalGuardrailsComplete: {IsRead: false, Required: false},
    SignalShutdown:           {IsRead: true, Required: false},
}
```

**Execution flow:**
1. Get user message from state.
2. Select guardrail prompt based on `GuardrailType`.
3. Call LLM with structured output (`inputValidation{RaiseAlarm bool, AlarmReason string}`).
4. If alarm raised: gate **CLOSED** -- drain and drop all bytes from `StreamBytes`.
5. If no alarm: gate **OPEN** -- forward all bytes from `StreamBytes` to `StreamGlobal` with retry.
6. Broadcast `SignalGuardrailsComplete` with `GuardrailResult`.
7. Wait for shutdown signal, then return.

---

### 5.9 streamDrainComponent

**File:** `lib/command_center/components.go`
**Purpose:** Forward one or more streams to an external output channel (typically a client socket).

**Struct fields:**
```go
type streamDrainComponent struct {
    readStreams    []Stream
    outputChannel chan<- []byte
}
```

**Capability:**
```go
Name: "StreamDrain"
StreamRequirements: {
    <each readStream>: {IsRead: true, Required: true},
}
SignalRequirements: {
    SignalShutdown: {IsRead: true, Required: true},
}
```

**Execution flow:**
1. Launch a goroutine per `readStream` via `errgroup`.
2. Each goroutine claims a reader and forwards data to `outputChannel`.
3. All goroutines exit on shutdown signal or stream close.

**Constructor:** `NewStreamDrainComponent(readStreams []Stream, outputChannel chan<- []byte)` -- if `outputChannel` is nil, creates a draining channel automatically.

---

### 5.10 StreamForwarderComponent

**File:** `lib/command_center/components.go`
**Purpose:** Simple stream-to-stream forwarding. Reads from one stream and writes to another.

**Struct fields:**
```go
type StreamForwarderComponent struct {
    ReadStream  Stream
    WriteStream Stream
}
```

**Capability:**
```go
Name: "StreamForwarder(<ReadStream>-><WriteStream>)"
StreamRequirements: {
    ReadStream:  {IsRead: true, Required: true},
    WriteStream: {IsRead: false, Required: true},
}
SignalRequirements: {
    SignalShutdown: {IsRead: true, Required: true},
}
```

**Execution flow:** Read from input stream, write to output stream, track forwarded message count. Exit on stream close or shutdown signal.

---

### 5.11 WaitComponent

**File:** `lib/command_center/components.go`
**Purpose:** No-op placeholder. Used as a synchronization point in graph schedulers.

**Capability:**
```go
Name: "Wait"
Description: "A component that does nothing."
// No stream or signal requirements
```

---

## 6. Scheduler System

### Scheduler Interface

```go
// File: lib/command_center/scheduler.go

type Scheduler interface {
    Schedule(ctx context.Context, pctx *SchedulerContext) (<-chan TaskDefinition, error)
    RequiredCapabilities() SchedulerRequirements
}

type SchedulerRequirements struct {
    Components []ComponentCapability
    Streams    []Stream
}
```

The `Schedule` method returns a **channel** of `TaskDefinition`, allowing schedulers to emit tasks incrementally.

---

### 6.1 LLMScheduler -- Dynamic LLM-Driven Planning

**File:** `lib/command_center/schedulers.go`

```go
type LLMScheduler struct {
    Model    lynx.LLM
    MaxTasks int32
    counter  atomic.Int32
    shutdown bool
}
```

**RequiredCapabilities:** Setup, StreamGating, TextProcessor, TearDown, StreamDrain. Streams: global, chat, tools, bytes.

**Scheduling logic:**

1. On `Start` event: schedule Setup, ToolChat, StreamGating, TextProcessor, and StreamDrain simultaneously.
2. On `component.succeeded`:
   - Infrastructure components (StreamGating, TextProcessor, StreamDrain): continue, no action.
   - TearDown finished: set `shutdown = true`, stop scheduling.
   - Any other component: invoke `chooseAction` to let LLM decide next.
3. `chooseAction` builds a prompt from the marshaled `SchedulerContext`, lists available components, and uses structured output (`structure.ChooseOne`) to select the next component.
4. `MaxTasks` enforced via `atomic.Int32` counter.

**Key detail:** Each `chooseAction` call adds LLM latency. For simple workflows, this overhead is unnecessary -- use ChainScheduler instead.

---

### 6.2 GraphScheduler -- Static DAG Execution

**File:** `lib/command_center/schedulers.go`

```go
type GraphScheduler struct {
    nodes map[string]TaskDefinition
    edges map[string]map[string]struct{}   // parent -> set(child)
    mu    sync.RWMutex
}
```

**API:**
```go
func (gp *GraphScheduler) AddNode(task TaskDefinition)
func (gp *GraphScheduler) AddEdge(parentID, childID string) error
```

**Scheduling logic:**
- On `Start` event: schedule all root nodes (nodes with no parents).
- On `component.succeeded` from node X: schedule all children of X.
- **Cycles are allowed** -- a child can eventually trigger an ancestor. A task is eligible only if not currently pending/running.
- Completed tasks may be re-scheduled on subsequent triggers, matching LangGraph's repeated node execution model.

---

### 6.3 ChainScheduler -- V1 Compatibility

**File:** `lib/command_center/schedulers.go`

```go
type ChainScheduler struct {
    MaxToolChatIterations int32          // Default: 5
    toolChatIterations    atomic.Int32
}
```

**Fixed sequence:**
```
Start --> Setup + TextProcessor + StreamDrain
  |
  v
Setup succeeded --> Guardrails (if present) OR ToolChat
  |
  v
Guardrails succeeded --> ToolChat
  |
  v
ToolChat succeeded:
  - No tool calls?           --> TearDown
  - Tools called, iter < max --> ToolChat (loop)
  - iter == max              --> Chat (final response)
  - iter > max               --> TearDown
  |
  v
TearDown succeeded --> Done
```

**Use case:** Migration path from V1 Chain to V2 CommandCenter while maintaining identical execution semantics.

---

## 7. Stream & Signal System

### Streams -- FanInRegistry (Multi-Producer, Single-Consumer)

**File:** `lib/command_center/fan_in_registry.go`

```go
type FanInRegistry struct {
    declaredReaders map[Stream][]string         // topology: who wants to read
    declaredWriters map[Stream][]string         // topology: who wants to write
    channels        map[Stream]chan []byte       // runtime channels
    wrappers        map[Stream]*SafeChannelWriter
    readers         map[Stream]string           // exclusive reader claim
    bufferSizes     map[Stream]int
    mu              sync.RWMutex
}
```

**Key behaviors:**
- **Auto-creation:** Streams are created automatically when both a reader and writer have been declared.
- **Exclusive reader claim:** Only one component can read from a stream at a time via `ClaimReader`.
- **Safe writers:** `SafeChannelWriter` prevents "send on closed channel" panics via atomic bool + RWMutex.
- **Slow consumer detection:** `SafeChannelReader` monitors delivery latency.

**SlowConsumerConfig:**
```go
type SlowConsumerConfig struct {
    SlowThreshold  time.Duration   // 1 second (default)
    ReportInterval time.Duration   // 5 seconds (default)
}
```

When delivery to the consumer exceeds `SlowThreshold`, a tracing span `SlowConsumerDetected` is created. Reports repeat every `ReportInterval` while slow. A `SlowConsumerRecovered` span is emitted on recovery.

**Stream constants:**

| Stream | Purpose |
|--------|---------|
| `StreamGlobal` ("global") | Gated output bound for the client |
| `StreamChat` ("chat") | Raw LLM text chunks |
| `StreamTools` ("tools") | Serialized tool calls and responses |
| `StreamIntents` ("intents") | LLM-detected user intents |
| `StreamBytes` ("bytes") | Processed text bytes (post-pipeline) |

---

### Signals -- FanOutRegistry (Single-Producer, Multi-Consumer Broadcast)

**File:** `lib/command_center/fan_out_registry.go`

```go
type FanOutRegistry struct {
    declaredReaders map[SignalName][]string
    declaredWriters map[SignalName][]string
    signals         map[SignalName]*Signal
    mu              sync.RWMutex
}

type Signal struct {
    name      SignalName
    started   bool
    done      chan struct{}
    completed bool
    data      any            // Optional data payload
    mu        sync.RWMutex
}
```

**Key behaviors:**
- **Auto-creation:** Signals are created when both readers and writers declare.
- **Broadcast with data:** `Broadcast(signalName, data)` closes the `done` channel and stores optional payload.
- **Idempotent check:** `IsCompleted` returns status without blocking. Double-broadcast returns an error.
- **Multiple waiters:** Any number of goroutines can `Wait` on the same signal; all wake when it fires.
- **WaitWithTimeout:** `WaitWithTimeout(signalName, timeout)` for time-bounded coordination.

**Signal constants:**

| Signal | Purpose |
|--------|---------|
| `SignalGuardrailsComplete` ("guardrails.complete") | StreamGatingComponent finished analysis |
| `SignalShutdown` ("shutdown") | Coordinated system shutdown |

---

## 8. Event System

Events are the sole communication mechanism between the execution layer and the scheduling layer.

**Event structure:**
- `Type`: dot-notation string (e.g., `"task.succeeded"`, `"tool.call"`)
- `Data`: arbitrary key-value payload
- `Timestamp`: when the event was created
- `source`: (unexported) which component produced the event
- `spanID`, `traceID`: (unexported) tracing linkage

**eventBatch:** Internal envelope that wraps one or more events with a synthetic `EventBatch.Received` tracing span. This lets the scheduler link to a single span per batch rather than per event.

**EventLog:** The CommandCenter maintains a complete audit trail via `cc.eventLog`. Accessible via the thread-safe `EventLog()` method which returns a copy of all processed events.

---

## 9. State Management -- Copy-on-Write

The CommandCenter uses copy-on-write semantics to provide task isolation while allowing state reconciliation.

### Execution Flow

```
Global State
    |
    | FullCopyTo(localState)     <-- deep copy with "created_by" tag
    v
Local State (isolated)
    |
    | Component.Execute(ctx, localState, access)
    |   ... component modifies localState ...
    v
Modified Local State
    |
    | PartialCopyTo(globalState)  <-- merge only frames owned by this task
    v
Global State (reconciled)
```

### Implementation

```go
// In executeTask():
localState := lynx.NewState(
    lynx.WithFramestackOpts(
        framestack.WithDefaultTags(map[string]string{"created_by": task.Definition.ID}),
    ),
)
_, err := cc.state.FullCopyTo(localState)
// ... component executes ...
frameCount, err := resultState.PartialCopyTo(cc.state)
```

### Benefits

| Property | Mechanism |
|----------|-----------|
| **Isolation** | Each task gets a deep copy; concurrent tasks cannot interfere |
| **Atomicity** | If a task fails, its state changes are never merged |
| **Auditability** | Every frame tagged with `created_by` for tracing origin |
| **Concurrency** | Only the merge step (`PartialCopyTo`) requires the global lock |

---

## 10. Execution Flow

### Step-by-Step

```
Run(ctx, input)
  |
  +-- 1. context.WithTimeout(ctx, 60s)
  +-- 2. tracing.StartSpan("CommandCenter.Run")
  +-- 3. Register SystemShutdown component for emergency cleanup (deferred)
  +-- 4. state.Context.Messages.StartInteraction(input)
  +-- 5. Launch processEvents goroutine
  +-- 6. Emit Start event: eventChan <- {Type: "start"}
  +-- 7. select { <-done | <-ctx.Done() }
  +-- 8. Return "Execution finished."

processEvents (goroutine):
  for {
    select {
    case batch := <-eventChan:
      +-- Build SchedulerContext snapshot
      +-- Start "Scheduler.Plan" span
      +-- scheduler.Schedule(ctx, &schedulerContext) --> channel of TaskDefinitions
      +-- Drain channel: addTask for each TaskDefinition
      +-- Finish scheduler span
      +-- runReadyTasks(ctx)
      +-- if isFinished(): return (closes done channel)
    case <-ctx.Done():
      return
    }
  }

executeTask (goroutine per task):
  +-- Lock task mutex
  +-- Set status = "running"
  +-- FullCopyTo: create isolated local state with "created_by" tag
  +-- Apply task-level timeout if specified
  +-- ValidateTask: check streams/signals exist
  +-- Create ScopedComponentAccess
  +-- Start "Task.Execute" span (linked to scheduler span via CreatedBySchedulerSpanID)
  +-- Start "Component.Execute" span
  +-- Component.Execute(ctx, localState, access)
  +-- PartialCopyTo: merge owned frames back to global state
  +-- Set status = "succeeded" or "failed"
  +-- Emit result events via eventChan
```

### Task Readiness

A task is ready when:
1. Its status is `"pending"`.
2. All `ParentTasks` have status `"succeeded"`.

---

## 11. Real-World Usage: Mobile Service

**File:** `svc/mobile/io/command_center_constructor.go`

The mobile service uses `CommandCenterConstructor` to wire up a CommandCenter with the ChainScheduler:

```go
// Create scheduler (ChainScheduler for V1 compatibility)
scheduler := &commandcenter.ChainScheduler{
    MaxToolChatIterations: 5,
}

// Create CommandCenter
commandCenter := commandcenter.NewCommandCenter(logger, scheduler, model)

// Register components
commandCenter.RegisterComponent(logger, commandcenter.NewStreamDrainComponent(
    []commandcenter.Stream{commandcenter.StreamBytes, commandcenter.StreamIntents},
    responseChannel,   // <-- SocketIO output channel
))
commandCenter.RegisterComponent(logger, &commandcenter.SetupComponent{
    StateSetters: append(baseStateSetters, sorter),
})
commandCenter.RegisterComponent(logger, commandcenter.ToolChatComponent{Model: model})
commandCenter.RegisterComponent(logger, commandcenter.TextProcessorComponent{
    ReadStream:    commandcenter.StreamChat,
    WriteStream:   commandcenter.StreamBytes,
    TextPipelines: getTextPipelines(chatMessage),
})
commandCenter.RegisterComponent(logger, commandcenter.TearDownComponent{
    PostInteractionHandlers: []commandcenter.InteractionHandler{
        chain.MemStoreHandler{MemStore: memStore, Conv: conv},
        chain.MLflowInteractionHandler{},
    },
    ShutdownStreams: []commandcenter.Stream{commandcenter.StreamChat, commandcenter.StreamBytes},
    StreamRegistry:  commandCenter.Streams(),
})

// Validate topology
if err := commandCenter.ValidateComponents(); err != nil {
    return nil, ctx, fmt.Errorf("failed to validate CommandCenter: %w", err)
}
```

**Data flow:**
```
LLM --> StreamChat --> TextProcessor --> StreamBytes --> StreamGating --> StreamGlobal
                                                                              |
StreamDrain <-- StreamBytes + StreamIntents                                    |
     |                                                                        |
     v                                                                        v
responseChannel (SocketIO) <--------- StreamDrain <---- StreamGlobal ---------+
```

---

## 12. Tracing Integration

### Tracing Provider Interface

**File:** `lib/command_center/tracing/tracing.go`

```go
type TracingProvider interface {
    StartSpan(ctx context.Context, operationName OperationName, opts ...SpanOption) (context.Context, Span)
    IsEnabled() bool
}

type Span interface {
    SetAttribute(key string, value any)
    AddEvent(name string, attributes map[string]any)
    RecordError(err error)
    Finish()
    FinishWithError(err error)
    SpanID() string
    TraceID() string
}
```

### Backend Implementations

| Backend | File | Notes |
|---------|------|-------|
| **NoOp** | `tracing/tracing.go` | Default; all operations are no-ops |
| **OpenTelemetry** | `tracing/opentelemetry.go` | Full OTel integration with span links, FOLLOWS_FROM |
| **Datadog** | `tracing/datadog.go` | DD-specific; span links emulated via tags |
| **Multi** | `tracing/multi.go` | Fan-out to multiple providers simultaneously |
| **Local (Jaeger)** | `local_tracing.go` | Dev convenience via OTLP HTTP to local Jaeger |
| **Databricks** | `databricks.go` (alias) | Delegates to `lib/databricks` package |

### Span Hierarchy

```
CommandCenter.Run
  +-- processEvents (goroutine)
       +-- EventBatch.Received
       +-- Scheduler.Plan
       |     +-- LLMScheduler.chooseAction (if LLMScheduler)
       +-- Task.Execute  [linked to Scheduler.Plan via CreatedBySchedulerSpanID]
            +-- Component.Execute [resource.name = component name]
                 +-- llm.generate_stream_call_tools.started
                 +-- toolchat.chat_processing.goroutine.*
                 +-- SlowConsumerDetected / SlowConsumerRecovered
                 +-- component.execution.completed
```

### Causality Tracking

`TaskDefinition.CreatedBySchedulerSpanID` and `SchedulerTraceID` link each task execution span to the scheduler decision that created it. This is implemented via:
- `tracing.AddSpanLink(ctx, traceID, spanID)` -- OpenTelemetry native span links
- `tracing.AddFollowsFromRef(ctx, traceID, spanID)` -- FOLLOWS_FROM semantic references

---

## 13. Validation & Error Handling

### ValidateTask

**File:** `lib/command_center/command_center.go`

Called before each task execution. Checks that every stream and signal declared in the component's `Capability()` actually exists in the registry:

```go
func (cc *CommandCenter) ValidateTask(logger, task TaskDefinition) error
```

If validation fails, the task is marked `StatusInvalid` and never executed.

### ValidateComponents

**File:** `lib/command_center/command_center.go`

Topology check across all registered components. Ensures required streams have both readers AND writers, and required signals have both waiters AND broadcasters:

```go
func (cc *CommandCenter) ValidateComponents() error
```

Called at construction time (see mobile service example) to fail fast on misconfiguration.

### Task Status Tracking

```
pending --> running --> succeeded
                   \-> failed
                   \-> invalid (validation failure at addTask time)
```

A task transitions to `running` only once (guarded by mutex). Failed tasks have their error stored in the result event. Invalid tasks are recorded but never executed.

---

## 14. Shutdown & Cleanup

### Coordinated Shutdown Sequence

```
1. TearDownComponent.Execute():
   a. Persist interaction (PostInteractionHandlers)
   b. Close stream writers (ShutdownStreams)
   c. Wait for readers to drain (2s timeout per stream)
   d. Broadcast SignalShutdown

2. All components with WaitForSignal(SignalShutdown):
   - ToolChat, Chat, TextProcessor, StreamGating, StreamDrain
   - Cancel internal contexts, exit goroutines

3. CommandCenter.Run() deferred cleanup:
   a. ScopedComponentAccess.Shutdown():
      i.   Broadcast SignalShutdown (idempotent, may already be broadcast)
      ii.  streams.Shutdown() -- close all channel wrappers, clear maps
      iii. signals.Shutdown() -- close uncompleted signal channels, clear maps
```

### Emergency Shutdown

If the 60-second context timeout fires before normal completion, `ctx.Done()` propagates cancellation to all goroutines, and the deferred `Shutdown()` in `Run()` ensures resource cleanup.

---

## 15. Testing

### StreamTestHarness

**File:** `lib/command_center/testing.go`

Provides automated stream and signal management for unit testing components in isolation:

```go
type StreamTestHarness struct {
    component       Component
    componentAccess *ScopedComponentAccess
    testAccess      *ScopedComponentAccess   // complementary access for test runner
    cleanup         func()
    streamInputs    map[Stream]StreamInput
    signalInputs    map[SignalName]SignalInput
    streamOutputs   map[Stream][]TimestampedPacket
    mu              sync.Mutex
    startTime       time.Time
}
```

**Usage pattern:**
```go
harness, err := NewStreamTestHarness(logger, myComponent,
    map[Stream]StreamInput{
        StreamChat: {Packets: [][]byte{[]byte("hello")}, Delay: 10 * time.Millisecond},
    },
    map[SignalName]SignalInput{
        SignalShutdown: {Delay: 1 * time.Second, Data: nil},
    },
)
defer harness.Cleanup()

state, events, err := harness.Execute(ctx, lynx.NewState())
outputs := harness.GetStreamOutputs(StreamBytes)
```

**How it works:**
1. `CreateScopedComponentAccess` creates complementary access pairs: the component gets normal access, the test runner gets the inverse (reads what the component writes, writes what the component reads).
2. Input feeders write packets to streams the component reads from, with configurable delays.
3. Output collectors read from streams the component writes to, recording timestamped packets.
4. Signal broadcasters fire signals after configurable delays.

---

## 16. Chain vs. CommandCenter Comparison

| Dimension | V1 Chain | V2 CommandCenter |
|-----------|----------|-----------------|
| **Architecture** | Monolithic executor with inline logic | Pure orchestrator with pluggable components |
| **State model** | Shared mutable state passed through functions | Copy-on-write with isolated task state |
| **Parallelism** | Sequential execution only | Concurrent task goroutines with dependency tracking |
| **Composition** | Fixed pipeline stages | Arbitrary component graphs via Scheduler |
| **Control flow** | Hardcoded loop (Setup -> ToolChat x5 -> TearDown) | Event-driven; Scheduler decides dynamically |
| **Event model** | None; side effects inline | Typed events as first-class communication |
| **Tracing** | Ad hoc logging | Structured spans with causality links |
| **Timeout** | Per-stage | Global 60s + per-task configurable |
| **Retry** | Manual per-stage | `Attempts` field exists but not yet implemented |
| **Current usage** | Legacy mobile service | Mobile service (via ChainScheduler migration path) |

---

## 17. Comparison with LangGraph

| Dimension | CommandCenter | LangGraph |
|-----------|--------------|-----------|
| **Graph model** | Dynamic (LLMScheduler) or static (GraphScheduler) | Static graph with conditional edges |
| **State management** | Copy-on-write per task; `FullCopyTo` / `PartialCopyTo` | Shared mutable `TypedDict` with reducers |
| **Communication** | Typed Streams (data) + Signals (coordination) | Channels between nodes |
| **Concurrency** | Native goroutines; multiple tasks in parallel | Fan-out/fan-in via `Send` API |
| **Planning** | LLMScheduler uses LLM to choose next component | No built-in LLM planning; uses conditional edges |
| **Best for** | Dynamic multi-model orchestration with streaming | Deterministic agent workflows with checkpointing |

---

## 18. Design Critique

### Strengths

1. **Pure orchestration separation.** The CommandCenter has zero knowledge of LLMs, tools, or domain logic. All business concerns live in Components, making the system highly testable and composable.

2. **Pluggable scheduling.** Three scheduler implementations (LLM, Graph, Chain) demonstrate the interface's flexibility. New scheduling strategies require no changes to the execution layer.

3. **Copy-on-write state safety.** Task isolation via `FullCopyTo` with `created_by` tags eliminates data races without requiring fine-grained locking in components.

4. **Event-driven flexibility.** The event system provides a clean contract between execution and scheduling. Events carry structured data, tracing context, and source attribution.

5. **Stream/Signal separation.** Distinguishing data flow (FanIn streams) from coordination (FanOut signals) creates clear semantics: streams are for content, signals are for lifecycle.

6. **Comprehensive tracing.** Multi-backend support (OTel, Datadog, Multi) with span links and FOLLOWS_FROM references enables end-to-end causality tracking across async task boundaries.

### Weaknesses

1. **60-second hardcoded timeout.** `Run()` uses `context.WithTimeout(ctx, 60*time.Second)`. This should be configurable per use case -- a complex multi-step workflow may legitimately need more time.

2. **No built-in retry.** `TaskDefinition.Attempts` exists but is annotated with `// TODO: make attempts actually used`. Failed tasks are marked and forgotten; there is no automatic retry with backoff.

3. **`FullCopyTo` cost for large states.** Deep-copying the entire state for every task is expensive when state contains large message histories or tool outputs. Incremental copy or copy-on-write at the frame level would reduce overhead.

4. **Scheduler interface complexity.** The channel-based `Schedule` return type forces goroutine management onto every scheduler implementation. A simpler `[]TaskDefinition` return would suffice for most cases, with the channel variant as an opt-in for streaming schedulers.

5. **No dead-letter queue.** Failed or invalid tasks produce events but have no systematic retry, escalation, or dead-letter mechanism. In production, unrecoverable task failures can silently halt progress.

6. **Relationship to Chain unclear.** The codebase maintains both V1 Chain and V2 CommandCenter with the ChainScheduler bridging them. The migration path and deprecation timeline are not formalized, creating ambiguity about which system to use.

7. **LLMScheduler adds latency for simple workflows.** Every scheduling decision requires an LLM call (`chooseAction`). For predictable workflows, this adds seconds of latency per step. The fallback to ChainScheduler is pragmatic but means the LLMScheduler is rarely used in production.

8. **Stream buffer size not tunable per stream.** All streams are created with the same `bufferSize` (100). High-throughput streams (chat text) may benefit from larger buffers, while low-throughput signals could use smaller ones. The `FanInRegistry.createStream` method accepts a buffer size parameter, but `NewCommandCenter` hardcodes 100 for all streams.
