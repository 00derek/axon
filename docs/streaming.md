# Streaming

Axon agents support two execution modes: synchronous (`Run`) and streaming (`Stream`).
Both drive the same agent loop; the difference is in how you receive the output.

---

## 1. Run vs Stream

```
agent.Run(ctx, input)              agent.Stream(ctx, input)
│                                  │
│  (blocks until complete)         │  returns immediately
│                                  │
▼                                  ▼
*Result                            *StreamResult
├─ .Text                           ├─ .Events() ──► chan StreamEvent
├─ .Rounds                         ├─ .Text()   ──► chan string
└─ .Usage                          ├─ .Result() ──► *Result (blocks)
                                   └─ .Err()    ──► error
```

`agent.Run` blocks until the entire turn finishes and returns a `*Result`:

```go
result, err := agent.Run(ctx, "What is the capital of France?")
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Text)
```

`agent.Stream` starts execution in a goroutine and returns a `*StreamResult` immediately.
The agent loop runs concurrently; you read its output through channels:

```go
streamResult, err := agent.Stream(ctx, "What is the capital of France?")
if err != nil {
    log.Fatal(err)
}
// agent loop is already running; consume events here
```

**When to use `Run`**: simple request–response usage where you only need the final answer
and have no use for intermediate tool activity.

**When to use `Stream`**: streaming output to a user interface, displaying tool progress
in real time, logging tool calls as they happen, or any scenario where seeing partial
results before the turn completes has value.

Both modes return the same `*Result` value; `Stream` just surfaces it through a method
call rather than a direct return.

---

## 2. Event Types

The `StreamResult.Events()` method returns a `<-chan StreamEvent` that emits every
event in the order it occurs. Three concrete event types are defined in the `kernel`
package:

```
agent.Stream(ctx, "input")
│
├─ Round 0 (tool call round)
│  ├─► ToolStartEvent{ToolName: "search", Params: {...}}
│  │   ... tool executes ...
│  └─► ToolEndEvent{ToolName: "search", Result: [...]}
│
├─ Round 1 (text response round)
│  ├─► TextDeltaEvent{Text: "I found "}
│  ├─► TextDeltaEvent{Text: "3 restaurants"}
│  └─► TextDeltaEvent{Text: " for you."}
│
└─ Events() channel closes
   └─► Result() now returns the final *Result
```

### `TextDeltaEvent`

Emitted once per turn when the LLM produces its final text response (after all tool
rounds are complete).

```go
type TextDeltaEvent struct {
    Text string // the complete text produced in this round
}
```

### `ToolStartEvent`

Emitted immediately before a tool begins executing.

```go
type ToolStartEvent struct {
    ToolName string          // name of the tool being called
    Params   json.RawMessage // raw JSON parameters sent by the model
}
```

### `ToolEndEvent`

Emitted immediately after a tool finishes executing. `Error` is non-nil if the tool
returned an error.

```go
type ToolEndEvent struct {
    ToolName string // name of the tool that ran
    Result   any    // value returned by the tool (nil on error)
    Error    error  // non-nil if the tool failed
}
```

### Consuming events with a type switch

Drain `Events()` in a `for range` loop. The channel is closed when the agent loop
finishes, so the loop terminates naturally:

```go
for event := range streamResult.Events() {
    switch e := event.(type) {
    case kernel.ToolStartEvent:
        paramsJSON, _ := json.Marshal(e.Params)
        fmt.Printf("[ToolStart]  tool=%q params=%s\n", e.ToolName, paramsJSON)

    case kernel.ToolEndEvent:
        if e.Error != nil {
            fmt.Printf("[ToolEnd]    tool=%q error=%v\n", e.ToolName, e.Error)
        } else {
            resultJSON, _ := json.Marshal(e.Result)
            fmt.Printf("[ToolEnd]    tool=%q result=%s\n", e.ToolName, resultJSON)
        }

    case kernel.TextDeltaEvent:
        fmt.Printf("[TextDelta]  text=%q\n", e.Text)
    }
}
```

A complete runnable version of this pattern is in `examples/03-streaming/main.go`.

---

## 3. Convenience: Text Channel

```
  Stream starts
       │
       ▼
  Events() channel open ◄──── receives events as they happen
  Text() channel open   ◄──── receives text deltas only
       │
       ▼
  Agent loop completes
       │
       ▼
  Events() channel closes
  Text() channel closes
       │
       ▼
  Result() returns *Result
  Err() returns error (or nil)
```

If you only need the text output and have no interest in tool events, use
`StreamResult.Text()`. It returns a `<-chan string` that receives only text deltas,
with no tool noise:

```go
streamResult, err := agent.Stream(ctx, "Summarize the history of Go.")
if err != nil {
    log.Fatal(err)
}

for chunk := range streamResult.Text() {
    fmt.Print(chunk)
}
fmt.Println()
```

The `Text()` channel is closed when the agent loop finishes, just like `Events()`.
You can range over it directly without a separate done signal.

> **Note**: `Text()` and `Events()` are independent channels. Consuming one does not
> affect the other. If you range over both in separate goroutines, both receive their
> respective events concurrently.

---

## 4. Accessing the Final Result

`StreamResult.Result()` blocks until the agent loop is complete and then returns the
same `*Result` that `agent.Run` would have returned:

```go
// Drain events first (or in a goroutine), then get the result.
for event := range streamResult.Events() {
    // handle events
}

result := streamResult.Result()
fmt.Println("Final text:", result.Text)
fmt.Printf("Rounds: %d  Tokens: %d\n", len(result.Rounds), result.Usage.TotalTokens)
```

Because `Events()` is closed when the loop finishes, ranging over it to completion
means `Result()` returns without blocking. If you call `Result()` before the loop
finishes — for example from a separate goroutine — it will block until the loop is done.

```go
// Pattern: consume events in one goroutine, wait for result in another.
go func() {
    for event := range streamResult.Events() {
        // display tool progress
    }
}()

result := streamResult.Result() // blocks until done
```

The `Result` type is identical to what `Run` returns:

```go
type Result struct {
    Text   string        // final text from the model
    Rounds []RoundResult // one entry per agent round
    Usage  Usage         // aggregated token usage across all rounds
}
```

---

## 5. Error Handling

### Stream-level errors

`StreamResult.Err()` returns any error that caused the agent loop to fail. Like
`Result()`, it blocks until the loop is done:

```go
streamResult, err := agent.Stream(ctx, input)
if err != nil {
    // Stream() itself failed (e.g., configuration error before the loop started)
    log.Fatal(err)
}

// Drain events
for event := range streamResult.Events() {
    // handle events
}

// Check for errors from inside the loop
if err := streamResult.Err(); err != nil {
    log.Printf("agent loop error: %v", err)
}
```

`Stream()` itself returns an error only for setup failures before the goroutine starts.
Errors that occur during the loop (such as a failed LLM call) are captured and returned
by `streamResult.Err()`.

### Tool errors via `ToolEndEvent`

Individual tool failures do not abort the agent loop. They are reported through
`ToolEndEvent.Error` while the loop continues. Check this field when you need to act
on tool-level failures in real time:

```go
for event := range streamResult.Events() {
    switch e := event.(type) {
    case kernel.ToolEndEvent:
        if e.Error != nil {
            log.Printf("tool %q failed: %v", e.ToolName, e.Error)
        }
    }
}
```

The same error is also visible after the turn completes via `result.Rounds`:

```go
result := streamResult.Result()
for _, round := range result.Rounds {
    for _, tc := range round.ToolCalls {
        if tc.Error != nil {
            log.Printf("tool %q error: %v", tc.Name, tc.Error)
        }
    }
}
```
