# Axon Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Axon workflow package -- sequential composition, parallel execution with deterministic state merging, routing, retry loops, and conditional branching. All built on WorkflowStep interface + WorkflowState.

**Architecture:** Separate Go module `workflow/` depending only on `kernel/`. WorkflowState carries input, messages, and a string-keyed data map between steps. Every construct (Step, Parallel, Router, RetryUntil, Conditional, NewWorkflow) implements the same WorkflowStep interface so they compose arbitrarily.

**Tech Stack:** Go 1.25, depends only on `kernel/` (stdlib + kernel types)

**Source spec:** `docs/superpowers/specs/2026-03-28-axon-framework-design.md`, Section 4

---

## File Structure

```
workflow/
├── go.mod              # module github.com/axonframework/axon/workflow
├── workflow.go         # WorkflowState, WorkflowStep interface, NewWorkflow, Step
├── workflow_test.go    # Tests for basic workflow, Step, sequential composition
├── parallel.go         # Parallel step with data merge
├── parallel_test.go
├── router.go           # Router step
├── router_test.go
├── control.go          # RetryUntil, Conditional
├── control_test.go
```

---

### Task 1: Initialize Go module, WorkflowState, WorkflowStep interface, Step, and NewWorkflow

**Files:**
- Create: `workflow/go.mod`
- Create: `workflow/workflow.go`
- Create: `workflow/workflow_test.go`

- [ ] **Step 1: Write the test file**

```go
// workflow/workflow_test.go
package workflow

import (
	"context"
	"testing"
)

func TestStepRuns(t *testing.T) {
	s := Step("greet", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["greeting"] = "hello " + state.Input
		return state, nil
	})

	result, err := s.Run(context.Background(), &WorkflowState{
		Input: "world",
		Data:  map[string]any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["greeting"] != "hello world" {
		t.Errorf("expected %q, got %v", "hello world", result.Data["greeting"])
	}
}

func TestStepNilDataInitialized(t *testing.T) {
	s := Step("init", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["key"] = "value"
		return state, nil
	})

	result, err := s.Run(context.Background(), &WorkflowState{Input: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["key"] != "value" {
		t.Errorf("expected %q, got %v", "value", result.Data["key"])
	}
}

func TestNewWorkflowSequential(t *testing.T) {
	step1 := Step("step1", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["order"] = "1"
		return state, nil
	})
	step2 := Step("step2", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["order"] = state.Data["order"].(string) + ",2"
		return state, nil
	})
	step3 := Step("step3", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["order"] = state.Data["order"].(string) + ",3"
		return state, nil
	})

	wf := NewWorkflow(step1, step2, step3)
	result, err := wf.Run(context.Background(), &WorkflowState{
		Input: "test",
		Data:  map[string]any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["order"] != "1,2,3" {
		t.Errorf("expected %q, got %v", "1,2,3", result.Data["order"])
	}
}

func TestNewWorkflowPassesStateBetweenSteps(t *testing.T) {
	step1 := Step("produce", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["value"] = 42
		return state, nil
	})
	step2 := Step("consume", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		v := state.Data["value"].(int)
		state.Data["doubled"] = v * 2
		return state, nil
	})

	wf := NewWorkflow(step1, step2)
	result, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["doubled"] != 84 {
		t.Errorf("expected 84, got %v", result.Data["doubled"])
	}
}

func TestNewWorkflowPropagatesError(t *testing.T) {
	step1 := Step("ok", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})
	step2 := Step("fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		return nil, context.DeadlineExceeded
	})
	step3 := Step("never", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["should_not_run"] = true
		return state, nil
	})

	wf := NewWorkflow(step1, step2, step3)
	_, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNewWorkflowPreservesMessages(t *testing.T) {
	step1 := Step("add-msg", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Messages = append(state.Messages, kernel.UserMsg("hello"))
		return state, nil
	})
	step2 := Step("check-msg", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		if len(state.Messages) != 1 {
			state.Data["error"] = "expected 1 message"
		} else {
			state.Data["text"] = state.Messages[0].TextContent()
		}
		return state, nil
	})

	wf := NewWorkflow(step1, step2)
	result, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["text"] != "hello" {
		t.Errorf("expected %q, got %v", "hello", result.Data["text"])
	}
}

func TestNewWorkflowRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	step := Step("should-fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})

	wf := NewWorkflow(step)
	_, err := wf.Run(ctx, &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

func TestNewWorkflowSingleStep(t *testing.T) {
	s := Step("only", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["solo"] = true
		return state, nil
	})

	wf := NewWorkflow(s)
	result, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["solo"] != true {
		t.Errorf("expected true, got %v", result.Data["solo"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v`
Expected: FAIL (package does not exist yet)

- [ ] **Step 3: Create go.mod**

```bash
mkdir -p /Users/derek/repo/axons/workflow
cd /Users/derek/repo/axons/workflow && go mod init github.com/axonframework/axon/workflow
```

Then edit the generated `go.mod` to add the kernel dependency:

```
// workflow/go.mod
module github.com/axonframework/axon/workflow

go 1.25

require github.com/axonframework/axon/kernel v0.0.0

replace github.com/axonframework/axon/kernel => ../kernel
```

- [ ] **Step 4: Write the implementation**

```go
// workflow/workflow.go
package workflow

import (
	"context"

	kernel "github.com/axonframework/axon/kernel"
)

// WorkflowState carries data between workflow steps.
type WorkflowState struct {
	Input    string
	Messages []kernel.Message
	Data     map[string]any
}

// initData ensures the Data map is non-nil.
func (s *WorkflowState) initData() {
	if s.Data == nil {
		s.Data = make(map[string]any)
	}
}

// WorkflowStep is the unit of composition in a workflow.
// Every construct (Step, Parallel, Router, RetryUntil, Conditional, NewWorkflow)
// implements this interface so they nest and compose freely.
type WorkflowStep interface {
	Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error)
}

// stepFunc is a named function step.
type stepFunc struct {
	name string
	fn   func(context.Context, *WorkflowState) (*WorkflowState, error)
}

// Step creates a WorkflowStep from a named function.
func Step(name string, fn func(context.Context, *WorkflowState) (*WorkflowState, error)) WorkflowStep {
	return &stepFunc{name: name, fn: fn}
}

func (s *stepFunc) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()
	return s.fn(ctx, input)
}

// sequentialWorkflow runs steps one after another, passing state through.
type sequentialWorkflow struct {
	steps []WorkflowStep
}

// NewWorkflow composes steps sequentially. The output of each step becomes
// the input of the next. If any step returns an error, execution stops.
func NewWorkflow(steps ...WorkflowStep) WorkflowStep {
	return &sequentialWorkflow{steps: steps}
}

func (w *sequentialWorkflow) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()
	state := input
	for _, step := range w.steps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		state, err = step.Run(ctx, state)
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v`
Expected: PASS (all 8 tests)

- [ ] **Step 6: Commit**

```bash
git add workflow/
git commit -m "feat(workflow): add WorkflowState, WorkflowStep, Step, and NewWorkflow"
```

---

### Task 2: Parallel step with deterministic data merge

**Files:**
- Create: `workflow/parallel.go`
- Create: `workflow/parallel_test.go`

- [ ] **Step 1: Write the test file**

```go
// workflow/parallel_test.go
package workflow

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelMergesData(t *testing.T) {
	step1 := Step("a", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["from_a"] = "alpha"
		return state, nil
	})
	step2 := Step("b", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["from_b"] = "beta"
		return state, nil
	})

	p := Parallel(step1, step2)
	result, err := p.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["from_a"] != "alpha" {
		t.Errorf("expected %q, got %v", "alpha", result.Data["from_a"])
	}
	if result.Data["from_b"] != "beta" {
		t.Errorf("expected %q, got %v", "beta", result.Data["from_b"])
	}
}

func TestParallelDeclarationOrderWins(t *testing.T) {
	// Both steps write to "winner". Declaration order means step1 runs first
	// in merge order, but step2 overwrites it. Last in declaration order wins.
	step1 := Step("first", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["winner"] = "first"
		return state, nil
	})
	step2 := Step("second", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["winner"] = "second"
		return state, nil
	})

	p := Parallel(step1, step2)
	result, err := p.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Declaration order: step1 merged first, step2 merged second.
	// step2 overwrites step1's "winner" key. Last writer wins.
	if result.Data["winner"] != "second" {
		t.Errorf("expected %q (last in declaration order), got %v", "second", result.Data["winner"])
	}
}

func TestParallelRunsConcurrently(t *testing.T) {
	var running atomic.Int32

	step1 := Step("slow1", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		running.Add(1)
		time.Sleep(50 * time.Millisecond)
		state.Data["peak1"] = int(running.Load())
		running.Add(-1)
		return state, nil
	})
	step2 := Step("slow2", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		running.Add(1)
		time.Sleep(50 * time.Millisecond)
		state.Data["peak2"] = int(running.Load())
		running.Add(-1)
		return state, nil
	})

	p := Parallel(step1, step2)
	start := time.Now()
	result, err := p.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// If truly parallel, elapsed should be ~50ms, not ~100ms
	if elapsed > 90*time.Millisecond {
		t.Errorf("expected parallel execution (~50ms), took %v", elapsed)
	}

	// At least one step should have observed 2 concurrent goroutines
	peak1 := result.Data["peak1"].(int)
	peak2 := result.Data["peak2"].(int)
	if peak1 < 2 && peak2 < 2 {
		t.Errorf("expected concurrent execution (peak >= 2), got peak1=%d peak2=%d", peak1, peak2)
	}
}

func TestParallelReceivesCopyOfData(t *testing.T) {
	// Each parallel step gets a copy; mutations from one step should not be
	// visible to another during execution.
	step1 := Step("reader", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		// Sleep briefly so step2 has time to write
		time.Sleep(20 * time.Millisecond)
		// step2's write to "from_writer" should NOT be visible here
		_, found := state.Data["from_writer"]
		state.Data["saw_writer"] = found
		return state, nil
	})
	step2 := Step("writer", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["from_writer"] = "written"
		return state, nil
	})

	p := Parallel(step1, step2)
	result, err := p.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"initial": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["saw_writer"] != false {
		t.Errorf("step1 should not see step2's writes during parallel execution, got %v", result.Data["saw_writer"])
	}
	// After merge, both results should be present
	if result.Data["from_writer"] != "written" {
		t.Errorf("expected merged data from writer step")
	}
}

func TestParallelPropagatesFirstError(t *testing.T) {
	step1 := Step("ok", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})
	step2 := Step("fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		return nil, context.DeadlineExceeded
	})

	p := Parallel(step1, step2)
	_, err := p.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected error from parallel step")
	}
}

func TestParallelPreservesInputState(t *testing.T) {
	step1 := Step("a", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["a"] = "from_a"
		return state, nil
	})
	step2 := Step("b", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["b"] = "from_b"
		return state, nil
	})

	p := Parallel(step1, step2)
	result, err := p.Run(context.Background(), &WorkflowState{
		Input: "original",
		Data:  map[string]any{"existing": "keep"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Input != "original" {
		t.Errorf("expected Input preserved, got %q", result.Input)
	}
	if result.Data["existing"] != "keep" {
		t.Errorf("expected existing data preserved, got %v", result.Data["existing"])
	}
}

func TestParallelMergesMessages(t *testing.T) {
	step1 := Step("a", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Messages = append(state.Messages, kernel.UserMsg("from a"))
		return state, nil
	})
	step2 := Step("b", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Messages = append(state.Messages, kernel.UserMsg("from b"))
		return state, nil
	})

	p := Parallel(step1, step2)
	result, err := p.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Messages from all steps should be collected in declaration order
	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}
	if result.Messages[0].TextContent() != "from a" {
		t.Errorf("expected first message %q, got %q", "from a", result.Messages[0].TextContent())
	}
	if result.Messages[1].TextContent() != "from b" {
		t.Errorf("expected second message %q, got %q", "from b", result.Messages[1].TextContent())
	}
}

func TestParallelSingleStep(t *testing.T) {
	s := Step("only", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["solo"] = true
		return state, nil
	})

	p := Parallel(s)
	result, err := p.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["solo"] != true {
		t.Errorf("expected true, got %v", result.Data["solo"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -run TestParallel`
Expected: FAIL (Parallel not defined)

- [ ] **Step 3: Write the implementation**

```go
// workflow/parallel.go
package workflow

import (
	"context"
	"sync"

	kernel "github.com/axonframework/axon/kernel"
)

// parallelStep runs multiple steps concurrently, then merges results
// in declaration order.
type parallelStep struct {
	steps []WorkflowStep
}

// Parallel creates a step that runs all child steps concurrently.
//
// Each step receives a shallow copy of WorkflowState.Data so mutations
// in one step are not visible to others during execution.
//
// After all steps complete, Data maps are merged sequentially in
// declaration order. If two steps write to the same key, the later
// step in declaration order wins. This is deterministic.
//
// Messages are concatenated in declaration order.
// Input is preserved from the original state.
func Parallel(steps ...WorkflowStep) WorkflowStep {
	return &parallelStep{steps: steps}
}

// copyData creates a shallow copy of a map.
func copyData(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// copyMessages creates a copy of a message slice.
func copyMessages(src []kernel.Message) []kernel.Message {
	if src == nil {
		return nil
	}
	dst := make([]kernel.Message, len(src))
	copy(dst, src)
	return dst
}

// stepResult holds the outcome of a single parallel step.
type stepResult struct {
	state *WorkflowState
	err   error
}

func (p *parallelStep) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()

	n := len(p.steps)
	results := make([]stepResult, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i, step := range p.steps {
		// Each goroutine gets its own copy of Data and Messages.
		stepState := &WorkflowState{
			Input:    input.Input,
			Messages: copyMessages(input.Messages),
			Data:     copyData(input.Data),
		}
		go func(idx int, s WorkflowStep, state *WorkflowState) {
			defer wg.Done()
			out, err := s.Run(ctx, state)
			results[idx] = stepResult{state: out, err: err}
		}(i, step, stepState)
	}

	wg.Wait()

	// Check for errors -- return the first one in declaration order.
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
	}

	// Merge results in declaration order.
	merged := &WorkflowState{
		Input:    input.Input,
		Messages: copyMessages(input.Messages),
		Data:     copyData(input.Data),
	}

	for _, r := range results {
		if r.state == nil {
			continue
		}
		// Merge Data: later declaration order overwrites earlier.
		for k, v := range r.state.Data {
			merged.Data[k] = v
		}
		// Append Messages from each step in declaration order.
		// Only append messages that were added by this step (not the copies).
		if len(r.state.Messages) > len(input.Messages) {
			newMsgs := r.state.Messages[len(input.Messages):]
			merged.Messages = append(merged.Messages, newMsgs...)
		}
	}

	return merged, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -run TestParallel`
Expected: PASS (all 8 parallel tests)

- [ ] **Step 5: Run all workflow tests**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -count=1`
Expected: PASS (all tests -- workflow + parallel)

- [ ] **Step 6: Commit**

```bash
git add workflow/parallel.go workflow/parallel_test.go
git commit -m "feat(workflow): add Parallel step with deterministic data merge"
```

---

### Task 3: Router step

**Files:**
- Create: `workflow/router.go`
- Create: `workflow/router_test.go`

- [ ] **Step 1: Write the test file**

```go
// workflow/router_test.go
package workflow

import (
	"context"
	"fmt"
	"testing"
)

func TestRouterSelectsCorrectRoute(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string {
		return state.Data["intent"].(string)
	}

	bookingStep := Step("booking", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["handler"] = "booking"
		return state, nil
	})
	generalStep := Step("general", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["handler"] = "general"
		return state, nil
	})

	r := Router(classify, map[string]WorkflowStep{
		"booking": bookingStep,
		"general": generalStep,
	})

	result, err := r.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"intent": "booking"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["handler"] != "booking" {
		t.Errorf("expected %q, got %v", "booking", result.Data["handler"])
	}
}

func TestRouterSelectsAlternateRoute(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string {
		return "general"
	}

	bookingStep := Step("booking", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["handler"] = "booking"
		return state, nil
	})
	generalStep := Step("general", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["handler"] = "general"
		return state, nil
	})

	r := Router(classify, map[string]WorkflowStep{
		"booking": bookingStep,
		"general": generalStep,
	})

	result, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["handler"] != "general" {
		t.Errorf("expected %q, got %v", "general", result.Data["handler"])
	}
}

func TestRouterUnknownRouteErrors(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string {
		return "unknown"
	}

	r := Router(classify, map[string]WorkflowStep{
		"known": Step("known", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			return state, nil
		}),
	})

	_, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for unknown route")
	}
}

func TestRouterPassesStateThroughToRoute(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string {
		return "target"
	}

	r := Router(classify, map[string]WorkflowStep{
		"target": Step("target", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			// Verify we can read pre-existing data
			state.Data["read_input"] = state.Input
			state.Data["read_existing"] = state.Data["existing"]
			return state, nil
		}),
	})

	result, err := r.Run(context.Background(), &WorkflowState{
		Input: "hello",
		Data:  map[string]any{"existing": "value"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["read_input"] != "hello" {
		t.Errorf("expected Input %q, got %v", "hello", result.Data["read_input"])
	}
	if result.Data["read_existing"] != "value" {
		t.Errorf("expected existing data, got %v", result.Data["read_existing"])
	}
}

func TestRouterPropagatesStepError(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string {
		return "fail"
	}

	r := Router(classify, map[string]WorkflowStep{
		"fail": Step("fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			return nil, fmt.Errorf("step failed")
		}),
	})

	_, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected error from routed step")
	}
}

func TestRouterWorksInWorkflow(t *testing.T) {
	classify := Step("classify", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		if state.Input == "book" {
			state.Data["intent"] = "booking"
		} else {
			state.Data["intent"] = "general"
		}
		return state, nil
	})

	router := Router(
		func(ctx context.Context, state *WorkflowState) string {
			return state.Data["intent"].(string)
		},
		map[string]WorkflowStep{
			"booking": Step("booking", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
				state.Data["result"] = "booked"
				return state, nil
			}),
			"general": Step("general", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
				state.Data["result"] = "answered"
				return state, nil
			}),
		},
	)

	wf := NewWorkflow(classify, router)
	result, err := wf.Run(context.Background(), &WorkflowState{
		Input: "book",
		Data:  map[string]any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["result"] != "booked" {
		t.Errorf("expected %q, got %v", "booked", result.Data["result"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -run TestRouter`
Expected: FAIL (Router not defined)

- [ ] **Step 3: Write the implementation**

```go
// workflow/router.go
package workflow

import (
	"context"
	"fmt"
)

// routerStep classifies input and dispatches to one of several routes.
type routerStep struct {
	classify func(context.Context, *WorkflowState) string
	routes   map[string]WorkflowStep
}

// Router creates a step that classifies the current state and dispatches
// to the matching route. The classify function returns a string key that
// must match one of the route map entries. If no route matches, an error
// is returned.
func Router(classify func(context.Context, *WorkflowState) string, routes map[string]WorkflowStep) WorkflowStep {
	return &routerStep{classify: classify, routes: routes}
}

func (r *routerStep) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()

	key := r.classify(ctx, input)
	step, ok := r.routes[key]
	if !ok {
		return nil, fmt.Errorf("workflow router: no route for key %q", key)
	}
	return step.Run(ctx, input)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -run TestRouter`
Expected: PASS (all 6 router tests)

- [ ] **Step 5: Commit**

```bash
git add workflow/router.go workflow/router_test.go
git commit -m "feat(workflow): add Router step for classification-based dispatch"
```

---

### Task 4: RetryUntil and Conditional control flow

**Files:**
- Create: `workflow/control.go`
- Create: `workflow/control_test.go`

- [ ] **Step 1: Write the test file**

```go
// workflow/control_test.go
package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// --- RetryUntil tests ---

func TestRetryUntilConverges(t *testing.T) {
	body := Step("increment", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		count, _ := state.Data["count"].(int)
		state.Data["count"] = count + 1
		return state, nil
	})

	until := func(ctx context.Context, state *WorkflowState) bool {
		return state.Data["count"].(int) >= 3
	}

	r := RetryUntil("count-to-3", body, until)
	result, err := r.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"count": 0},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["count"] != 3 {
		t.Errorf("expected 3, got %v", result.Data["count"])
	}
}

func TestRetryUntilAlreadySatisfied(t *testing.T) {
	body := Step("never-runs", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})

	until := func(ctx context.Context, state *WorkflowState) bool {
		return true // already done
	}

	r := RetryUntil("already-done", body, until)
	result, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Body should never execute since condition is already met
	if _, ran := result.Data["ran"]; ran {
		t.Error("body should not run when condition is already satisfied")
	}
}

func TestRetryUntilPropagatesBodyError(t *testing.T) {
	body := Step("fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		return nil, fmt.Errorf("body failed")
	})

	until := func(ctx context.Context, state *WorkflowState) bool {
		return false // never satisfied
	}

	r := RetryUntil("fail-loop", body, until)
	_, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected error from body")
	}
}

func TestRetryUntilRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	body := Step("spin", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		time.Sleep(20 * time.Millisecond)
		return state, nil
	})

	until := func(ctx context.Context, state *WorkflowState) bool {
		return false // never satisfied
	}

	r := RetryUntil("timeout-loop", body, until)
	_, err := r.Run(ctx, &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected context deadline error")
	}
}

func TestRetryUntilPassesStateBetweenIterations(t *testing.T) {
	body := Step("accumulate", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		trail, _ := state.Data["trail"].(string)
		state.Data["trail"] = trail + "x"
		return state, nil
	})

	until := func(ctx context.Context, state *WorkflowState) bool {
		return len(state.Data["trail"].(string)) >= 4
	}

	r := RetryUntil("accumulate", body, until)
	result, err := r.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"trail": ""},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["trail"] != "xxxx" {
		t.Errorf("expected %q, got %v", "xxxx", result.Data["trail"])
	}
}

// --- Conditional tests ---

func TestConditionalTrue(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool {
		return state.Data["flag"].(bool)
	}

	ifTrue := Step("true-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "true"
		return state, nil
	})
	ifFalse := Step("false-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "false"
		return state, nil
	})

	c := Conditional(check, ifTrue, ifFalse)
	result, err := c.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"flag": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["branch"] != "true" {
		t.Errorf("expected %q, got %v", "true", result.Data["branch"])
	}
}

func TestConditionalFalse(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool {
		return state.Data["flag"].(bool)
	}

	ifTrue := Step("true-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "true"
		return state, nil
	})
	ifFalse := Step("false-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "false"
		return state, nil
	})

	c := Conditional(check, ifTrue, ifFalse)
	result, err := c.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"flag": false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["branch"] != "false" {
		t.Errorf("expected %q, got %v", "false", result.Data["branch"])
	}
}

func TestConditionalNilFalseBranch(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool {
		return false
	}

	ifTrue := Step("true-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})

	// nil ifFalse: when condition is false and no fallback, state passes through unchanged.
	c := Conditional(check, ifTrue, nil)
	result, err := c.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"original": "kept"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["original"] != "kept" {
		t.Errorf("expected original data preserved, got %v", result.Data["original"])
	}
	if _, ran := result.Data["ran"]; ran {
		t.Error("true branch should not have run")
	}
}

func TestConditionalNilTrueBranch(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool {
		return true
	}

	ifFalse := Step("false-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})

	// nil ifTrue: when condition is true and no handler, state passes through unchanged.
	c := Conditional(check, nil, ifFalse)
	result, err := c.Run(context.Background(), &WorkflowState{
		Data: map[string]any{"original": "kept"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["original"] != "kept" {
		t.Errorf("expected original data preserved, got %v", result.Data["original"])
	}
	if _, ran := result.Data["ran"]; ran {
		t.Error("false branch should not have run")
	}
}

func TestConditionalPropagatesError(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool {
		return true
	}

	ifTrue := Step("fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		return nil, fmt.Errorf("branch failed")
	})

	c := Conditional(check, ifTrue, nil)
	_, err := c.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected error from branch")
	}
}

func TestConditionalInWorkflow(t *testing.T) {
	setup := Step("setup", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["score"] = 85
		return state, nil
	})

	branch := Conditional(
		func(ctx context.Context, state *WorkflowState) bool {
			return state.Data["score"].(int) >= 80
		},
		Step("pass", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["result"] = "passed"
			return state, nil
		}),
		Step("fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["result"] = "failed"
			return state, nil
		}),
	)

	wf := NewWorkflow(setup, branch)
	result, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["result"] != "passed" {
		t.Errorf("expected %q, got %v", "passed", result.Data["result"])
	}
}

func TestRetryUntilInWorkflow(t *testing.T) {
	setup := Step("setup", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["attempts"] = 0
		return state, nil
	})

	retry := RetryUntil("retry",
		Step("attempt", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["attempts"] = state.Data["attempts"].(int) + 1
			return state, nil
		}),
		func(ctx context.Context, state *WorkflowState) bool {
			return state.Data["attempts"].(int) >= 2
		},
	)

	finish := Step("finish", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["done"] = true
		return state, nil
	})

	wf := NewWorkflow(setup, retry, finish)
	result, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["attempts"] != 2 {
		t.Errorf("expected 2 attempts, got %v", result.Data["attempts"])
	}
	if result.Data["done"] != true {
		t.Errorf("expected done=true, got %v", result.Data["done"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -run "TestRetryUntil|TestConditional"`
Expected: FAIL (RetryUntil and Conditional not defined)

- [ ] **Step 3: Write the implementation**

```go
// workflow/control.go
package workflow

import (
	"context"
)

// retryUntilStep repeatedly runs a body step until a condition is met.
type retryUntilStep struct {
	name  string
	body  WorkflowStep
	until func(context.Context, *WorkflowState) bool
}

// RetryUntil creates a step that runs body repeatedly until the condition
// function returns true. The condition is checked before each iteration --
// if it is already true, the body never executes. State from each iteration
// is passed to the next.
//
// The loop respects context cancellation, checking ctx.Err() before each
// iteration. If the body returns an error, the loop stops immediately.
func RetryUntil(name string, body WorkflowStep, until func(context.Context, *WorkflowState) bool) WorkflowStep {
	return &retryUntilStep{name: name, body: body, until: until}
}

func (r *retryUntilStep) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()
	state := input

	for !r.until(ctx, state) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		state, err = r.body.Run(ctx, state)
		if err != nil {
			return nil, err
		}
	}

	return state, nil
}

// conditionalStep branches execution based on a boolean check.
type conditionalStep struct {
	check   func(context.Context, *WorkflowState) bool
	ifTrue  WorkflowStep
	ifFalse WorkflowStep
}

// Conditional creates a step that evaluates a check function and runs
// ifTrue when it returns true, or ifFalse when it returns false.
//
// Either branch may be nil. When the selected branch is nil, the input
// state is returned unchanged.
func Conditional(check func(context.Context, *WorkflowState) bool, ifTrue WorkflowStep, ifFalse WorkflowStep) WorkflowStep {
	return &conditionalStep{check: check, ifTrue: ifTrue, ifFalse: ifFalse}
}

func (c *conditionalStep) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()

	if c.check(ctx, input) {
		if c.ifTrue == nil {
			return input, nil
		}
		return c.ifTrue.Run(ctx, input)
	}

	if c.ifFalse == nil {
		return input, nil
	}
	return c.ifFalse.Run(ctx, input)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -run "TestRetryUntil|TestConditional"`
Expected: PASS (all 12 control flow tests)

- [ ] **Step 5: Run all workflow tests**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -count=1`
Expected: PASS (all tests across all files -- workflow, parallel, router, control)

- [ ] **Step 6: Commit**

```bash
git add workflow/control.go workflow/control_test.go
git commit -m "feat(workflow): add RetryUntil and Conditional control flow steps"
```

---

### Task 5: Integration test -- full workflow composition

This task adds an integration test that composes all step types into a realistic workflow, validating that they work together end-to-end. This goes in `workflow_test.go` since it exercises the top-level composition.

**Files:**
- Edit: `workflow/workflow_test.go` (append integration tests)

- [ ] **Step 1: Append integration tests to workflow_test.go**

Add the following tests at the end of `workflow/workflow_test.go`:

```go
func TestFullWorkflowComposition(t *testing.T) {
	// Simulates: parallel prep -> route based on intent -> conditional post-processing
	wf := NewWorkflow(
		// Phase 1: Parallel data loading
		Parallel(
			Step("classify", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
				if state.Input == "book a table" {
					state.Data["intent"] = "booking"
				} else {
					state.Data["intent"] = "general"
				}
				return state, nil
			}),
			Step("load-user", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
				state.Data["user"] = "alice"
				return state, nil
			}),
		),
		// Phase 2: Route to handler
		Router(
			func(ctx context.Context, state *WorkflowState) string {
				return state.Data["intent"].(string)
			},
			map[string]WorkflowStep{
				"booking": Step("book", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
					state.Data["booking_id"] = "BK-001"
					state.Data["status"] = "confirmed"
					return state, nil
				}),
				"general": Step("respond", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
					state.Data["response"] = "How can I help?"
					return state, nil
				}),
			},
		),
		// Phase 3: Conditional follow-up
		Conditional(
			func(ctx context.Context, state *WorkflowState) bool {
				return state.Data["intent"] == "booking"
			},
			Step("send-confirmation", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
				state.Data["confirmation_sent"] = true
				return state, nil
			}),
			nil,
		),
	)

	result, err := wf.Run(context.Background(), &WorkflowState{
		Input: "book a table",
		Data:  map[string]any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all phases executed correctly
	if result.Data["intent"] != "booking" {
		t.Errorf("intent: expected %q, got %v", "booking", result.Data["intent"])
	}
	if result.Data["user"] != "alice" {
		t.Errorf("user: expected %q, got %v", "alice", result.Data["user"])
	}
	if result.Data["booking_id"] != "BK-001" {
		t.Errorf("booking_id: expected %q, got %v", "BK-001", result.Data["booking_id"])
	}
	if result.Data["confirmation_sent"] != true {
		t.Errorf("confirmation_sent: expected true, got %v", result.Data["confirmation_sent"])
	}
}

func TestNestedWorkflowsCompose(t *testing.T) {
	inner := NewWorkflow(
		Step("inner1", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["inner1"] = true
			return state, nil
		}),
		Step("inner2", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["inner2"] = true
			return state, nil
		}),
	)

	outer := NewWorkflow(
		Step("before", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["before"] = true
			return state, nil
		}),
		inner,
		Step("after", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["after"] = true
			return state, nil
		}),
	)

	result, err := outer.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, key := range []string{"before", "inner1", "inner2", "after"} {
		if result.Data[key] != true {
			t.Errorf("expected %q=true, got %v", key, result.Data[key])
		}
	}
}

func TestRetryUntilWithConditionalExit(t *testing.T) {
	// Retry a workflow that internally uses Conditional to decide when to stop
	wf := NewWorkflow(
		Step("init", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["quality"] = 0
			return state, nil
		}),
		RetryUntil("improve",
			NewWorkflow(
				Step("refine", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
					q := state.Data["quality"].(int)
					state.Data["quality"] = q + 30
					return state, nil
				}),
				Conditional(
					func(ctx context.Context, state *WorkflowState) bool {
						return state.Data["quality"].(int) > 50
					},
					Step("log-good", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
						state.Data["quality_ok"] = true
						return state, nil
					}),
					nil,
				),
			),
			func(ctx context.Context, state *WorkflowState) bool {
				return state.Data["quality"].(int) >= 90
			},
		),
	)

	result, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["quality"].(int) < 90 {
		t.Errorf("expected quality >= 90, got %v", result.Data["quality"])
	}
	if result.Data["quality_ok"] != true {
		t.Errorf("expected quality_ok=true")
	}
}

func TestParallelInsideRouter(t *testing.T) {
	wf := NewWorkflow(
		Step("setup", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["mode"] = "multi"
			return state, nil
		}),
		Router(
			func(ctx context.Context, state *WorkflowState) string {
				return state.Data["mode"].(string)
			},
			map[string]WorkflowStep{
				"multi": Parallel(
					Step("task-a", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
						state.Data["a"] = "done"
						return state, nil
					}),
					Step("task-b", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
						state.Data["b"] = "done"
						return state, nil
					}),
				),
				"single": Step("solo", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
					state.Data["solo"] = "done"
					return state, nil
				}),
			},
		),
	)

	result, err := wf.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["a"] != "done" || result.Data["b"] != "done" {
		t.Errorf("expected both parallel tasks done, got a=%v b=%v", result.Data["a"], result.Data["b"])
	}
}
```

- [ ] **Step 2: Run integration tests**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -run "TestFullWorkflow|TestNested|TestRetryUntilWith|TestParallelInside"`
Expected: PASS (all 4 integration tests)

- [ ] **Step 3: Run complete test suite**

Run: `cd /Users/derek/repo/axons && go test ./workflow/ -v -count=1`
Expected: PASS (all tests -- 8 workflow + 8 parallel + 6 router + 12 control + 4 integration = 38 tests)

- [ ] **Step 4: Commit**

```bash
git add workflow/workflow_test.go
git commit -m "test(workflow): add integration tests for full workflow composition"
```

---

### Task 6: Run go vet and verify clean build

- [ ] **Step 1: Run go vet**

Run: `cd /Users/derek/repo/axons/workflow && go vet ./...`
Expected: No issues

- [ ] **Step 2: Run tests with race detector**

Run: `cd /Users/derek/repo/axons/workflow && go test -race -count=1 ./...`
Expected: PASS with no data races (particularly important for Parallel tests)

- [ ] **Step 3: Commit if any fixes were needed**

If vet or race detector revealed issues, fix and commit:

```bash
git add workflow/
git commit -m "fix(workflow): address vet/race findings"
```

---

## Self-Review

**Spec coverage (Section 4):**
- WorkflowStep interface -> Task 1 (workflow.go)
- WorkflowState struct (Input, Messages, Data) -> Task 1 (workflow.go)
- Step constructor -> Task 1 (workflow.go)
- NewWorkflow sequential composition -> Task 1 (workflow.go)
- Parallel with concurrent execution -> Task 2 (parallel.go)
- Parallel data merge in declaration order -> Task 2 (parallel.go, tested explicitly)
- Parallel data isolation (copy, not shared) -> Task 2 (parallel.go, tested explicitly)
- Router with classify function -> Task 3 (router.go)
- RetryUntil loop -> Task 4 (control.go)
- Conditional branching -> Task 4 (control.go)
- Composability (all types nest freely) -> Task 5 (integration tests)
- Context cancellation respected -> Tasks 1, 4 (tested in workflow_test.go, control_test.go)

**Spec exclusions honored:**
- No stream/signal system
- No component registration
- No event loop or scheduler
- No LLM-driven scheduling
- No copy-on-write state isolation (uses simple map copy for Parallel)

**Dependency rule:** workflow/ imports only kernel/ (for kernel.Message type)

**Placeholder scan:** No TBDs, TODOs, or "implement later" found. All code is complete.

**Type consistency:**
- WorkflowState.Messages uses kernel.Message (imported via `kernel "github.com/axonframework/axon/kernel"`)
- All step constructors return WorkflowStep interface
- All Run methods accept `context.Context` and `*WorkflowState`, return `(*WorkflowState, error)`
- Parallel uses `sync.WaitGroup` for concurrency, results array indexed by declaration order for determinism
