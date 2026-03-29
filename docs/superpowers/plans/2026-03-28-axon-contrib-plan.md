# Axon Contrib/Plan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `contrib/plan` package -- structured multi-step procedure tracking for agents. A Plan is injected into the LLM context each round as a system message. The LLM follows the plan and advances steps via built-in `mark_step` and `add_note` tools.

**Architecture:** Single Go package `plan/` inside `contrib/plan/`. Depends only on `kernel/`. The `Attach(p *Plan)` function returns `[]kernel.AgentOption` that wire hooks (`OnStart`, `PrepareRound`) and tools (`mark_step`, `add_note`) into any agent. The plan is a plain struct -- user owns persistence.

**Tech Stack:** Go 1.25.2, stdlib + `github.com/axonframework/axon/kernel` (local replace directive)

**Source spec:** `docs/superpowers/specs/2026-03-28-providers-google-contrib-design.md`, Section 2

---

## File Structure

```
contrib/plan/
├── go.mod          # module github.com/axonframework/axon/contrib/plan
├── plan.go         # Plan, Step, StepStatus, New
├── plan_test.go    # Unit tests for Plan, Step, New
├── format.go       # Format(*Plan) string -- plan -> system message text
├── format_test.go  # Unit tests for Format
├── attach.go       # Attach(*Plan) []kernel.AgentOption -- hooks + tools
├── attach_test.go  # Integration tests using kernel.NewAgent + fakeLLM
```

---

## Kernel Types Reference

These types from the kernel package are used throughout this plan. They are already implemented.

```go
// kernel.AgentOption -- function that configures an Agent
type AgentOption func(*Agent)

// Hook constructors (each returns AgentOption):
func OnStart(fn func(*TurnContext)) AgentOption
func PrepareRound(fn func(*RoundContext)) AgentOption
func WithTools(tools ...Tool) AgentOption

// kernel.TurnContext -- available in OnStart/OnFinish
type TurnContext struct {
    AgentCtx *AgentContext
    Input    string
    Result   *Result
}

// kernel.RoundContext -- available in PrepareRound/OnRoundFinish
type RoundContext struct {
    AgentCtx     *AgentContext
    RoundNumber  int
    LastResponse *Response
}

// kernel.AgentContext -- conversation state
func (c *AgentContext) AddMessages(msgs ...Message)
func (c *AgentContext) SetSystemPrompt(prompt string)

// kernel.Tool -- tool interface
type Tool interface {
    Name() string
    Description() string
    Schema() Schema
    Execute(ctx context.Context, params json.RawMessage) (any, error)
}

// kernel.NewTool -- typed tool constructor (auto-generates Schema from struct tags)
func NewTool[P any, R any](name, description string, fn func(ctx context.Context, params P) (R, error)) Tool

// kernel.SystemMsg -- creates a system message
func SystemMsg(text string) Message

// kernel.NewAgent -- creates an agent from options
func NewAgent(opts ...AgentOption) *Agent

// fakeLLM pattern (from kernel tests) -- scripted responses per round
type fakeLLM struct { responses []Response; callCount int }
```

**Key behavior:** When `tool.Execute()` returns a non-nil `error`, the agent serializes it as a tool result message with `IsError: true`. This is how `mark_step` signals invalid step names to the LLM without crashing the agent loop.

---

### Task 1: Initialize Go module, Plan/Step types, and New constructor

**Files:**
- Create: `contrib/plan/go.mod`
- Create: `contrib/plan/plan.go`
- Create: `contrib/plan/plan_test.go`

This task establishes the core data types: `Plan`, `Step`, `StepStatus` constants, and the `New()` constructor. All steps start as `StatusPending`.

- [ ] **Step 1: Write the test file**

```go
// contrib/plan/plan_test.go
package plan

import (
	"testing"
)

func TestNewCreatesWithPendingSteps(t *testing.T) {
	p := New("Booking", "Help user book a restaurant",
		Step{Name: "gather", Description: "Ask preferences"},
		Step{Name: "search", Description: "Find restaurants"},
		Step{Name: "confirm", Description: "Confirm booking", NeedsUserInput: true},
	)

	if p.Name != "Booking" {
		t.Errorf("expected name %q, got %q", "Booking", p.Name)
	}
	if p.Goal != "Help user book a restaurant" {
		t.Errorf("expected goal %q, got %q", "Help user book a restaurant", p.Goal)
	}
	if len(p.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(p.Steps))
	}
	for i, s := range p.Steps {
		if s.Status != StatusPending {
			t.Errorf("step %d: expected status %q, got %q", i, StatusPending, s.Status)
		}
	}
	if p.Notes == nil {
		t.Error("expected Notes map to be initialized, got nil")
	}
}

func TestNewPreservesStepFields(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First", NeedsUserInput: true, CanRepeat: true},
		Step{Name: "s2", Description: "Second"},
	)

	s1 := p.Steps[0]
	if s1.Name != "s1" {
		t.Errorf("expected name %q, got %q", "s1", s1.Name)
	}
	if s1.Description != "First" {
		t.Errorf("expected description %q, got %q", "First", s1.Description)
	}
	if !s1.NeedsUserInput {
		t.Error("expected NeedsUserInput true")
	}
	if !s1.CanRepeat {
		t.Error("expected CanRepeat true")
	}

	s2 := p.Steps[1]
	if s2.NeedsUserInput {
		t.Error("expected NeedsUserInput false for s2")
	}
}

func TestNewNoSteps(t *testing.T) {
	p := New("Empty", "No steps")
	if len(p.Steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(p.Steps))
	}
	if p.Notes == nil {
		t.Error("expected Notes map to be initialized")
	}
}

func TestStepStatusConstants(t *testing.T) {
	// Verify the string values match spec.
	if StatusPending != "pending" {
		t.Errorf("StatusPending = %q", StatusPending)
	}
	if StatusActive != "active" {
		t.Errorf("StatusActive = %q", StatusActive)
	}
	if StatusDone != "done" {
		t.Errorf("StatusDone = %q", StatusDone)
	}
	if StatusSkipped != "skipped" {
		t.Errorf("StatusSkipped = %q", StatusSkipped)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test ./... -v`
Expected: FAIL (module and source files do not exist yet)

- [ ] **Step 3: Create go.mod**

```
module github.com/axonframework/axon/contrib/plan

go 1.25.2

require github.com/axonframework/axon/kernel v0.0.0

replace github.com/axonframework/axon/kernel => ../../kernel
```

- [ ] **Step 4: Write the implementation**

```go
// contrib/plan/plan.go
package plan

// StepStatus represents the current state of a plan step.
type StepStatus string

const (
	StatusPending StepStatus = "pending"
	StatusActive  StepStatus = "active"
	StatusDone    StepStatus = "done"
	StatusSkipped StepStatus = "skipped"
)

// Plan is a structured multi-step procedure for an agent to follow.
// It is a plain struct that serializes cleanly with encoding/json.
type Plan struct {
	Name  string         `json:"name"`
	Goal  string         `json:"goal"`
	Steps []Step         `json:"steps"`
	Notes map[string]any `json:"notes"`
}

// Step is a single step in a Plan.
type Step struct {
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Status         StepStatus `json:"status"`
	NeedsUserInput bool       `json:"needs_user_input,omitempty"`
	CanRepeat      bool       `json:"can_repeat,omitempty"`
}

// New creates a Plan with the given steps. All steps start as StatusPending.
// Notes is initialized to an empty map.
func New(name, goal string, steps ...Step) *Plan {
	s := make([]Step, len(steps))
	for i, step := range steps {
		step.Status = StatusPending
		s[i] = step
	}
	return &Plan{
		Name:  name,
		Goal:  goal,
		Steps: s,
		Notes: make(map[string]any),
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test ./... -v`
Expected: PASS -- all 4 tests green

- [ ] **Step 6: Commit**

```bash
cd /Users/derek/repo/axons && git add contrib/plan/go.mod contrib/plan/plan.go contrib/plan/plan_test.go
git commit -m "contrib/plan: add Plan, Step, StepStatus types and New constructor

Establishes core data model for structured multi-step procedure tracking.
Plan holds name, goal, ordered steps, and a notes map. All steps
initialize to StatusPending. Step carries Name, Description, Status,
NeedsUserInput, and CanRepeat fields."
```

---

### Task 2: Plan formatting (format.go + format_test.go)

**Files:**
- Create: `contrib/plan/format.go`
- Create: `contrib/plan/format_test.go`

The `Format` function converts a `*Plan` into the structured text the LLM sees each round. Uses checkbox-style markers: `[✓]` done, `[>]` active, `[ ]` pending, `[-]` skipped. Appends "(needs user input)" and "(repeatable)" annotations. Includes Notes section if non-empty.

- [ ] **Step 1: Write the test file**

```go
// contrib/plan/format_test.go
package plan

import (
	"strings"
	"testing"
)

func TestFormatAllPending(t *testing.T) {
	p := New("Booking", "Help user book",
		Step{Name: "gather", Description: "Ask preferences"},
		Step{Name: "search", Description: "Find options"},
	)

	got := Format(p)

	assertContains(t, got, "## Current Plan: Booking")
	assertContains(t, got, "Goal: Help user book")
	assertContains(t, got, "[ ] gather")
	assertContains(t, got, "[ ] search")
}

func TestFormatMixedStatuses(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
		Step{Name: "s3", Description: "Third"},
		Step{Name: "s4", Description: "Fourth"},
	)
	p.Steps[0].Status = StatusDone
	p.Steps[1].Status = StatusActive
	// s3 stays pending
	p.Steps[3].Status = StatusSkipped

	got := Format(p)

	assertContains(t, got, "[✓] s1 — First")
	assertContains(t, got, "[>] s2 — Second")
	assertContains(t, got, "[ ] s3 — Third")
	assertContains(t, got, "[-] s4 — Fourth")
}

func TestFormatNeedsUserInput(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "confirm", Description: "Get confirmation", NeedsUserInput: true},
	)

	got := Format(p)
	assertContains(t, got, "(needs user input)")
}

func TestFormatCanRepeat(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "retry", Description: "Try again", CanRepeat: true},
	)

	got := Format(p)
	assertContains(t, got, "(repeatable)")
}

func TestFormatBothAnnotations(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s", Description: "Both", NeedsUserInput: true, CanRepeat: true},
	)

	got := Format(p)
	assertContains(t, got, "(needs user input)")
	assertContains(t, got, "(repeatable)")
}

func TestFormatWithNotes(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)
	p.Notes["cuisine"] = "Italian"
	p.Notes["party_size"] = 4

	got := Format(p)

	assertContains(t, got, "Notes:")
	assertContains(t, got, "cuisine: Italian")
	assertContains(t, got, "party_size: 4")
}

func TestFormatNoNotesSection(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)
	// Notes is empty map

	got := Format(p)

	if strings.Contains(got, "Notes:") {
		t.Error("expected no Notes section when notes map is empty")
	}
}

func TestFormatNoSteps(t *testing.T) {
	p := New("Empty", "No steps")

	got := Format(p)
	assertContains(t, got, "## Current Plan: Empty")
	assertContains(t, got, "Goal: No steps")
}

// assertContains is a test helper that checks if got contains want.
func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("expected output to contain %q\ngot:\n%s", want, got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test -run TestFormat -v`
Expected: FAIL (format.go does not exist)

- [ ] **Step 3: Write the implementation**

```go
// contrib/plan/format.go
package plan

import (
	"fmt"
	"sort"
	"strings"
)

// Format renders a Plan as structured text for injection into the LLM context.
//
// Output format:
//
//	## Current Plan: {Name}
//	Goal: {Goal}
//
//	[✓] step_name — Description
//	[>] active_step — Description
//	[ ] pending_step — Description (needs user input)
//	[-] skipped_step — Description
//
//	Notes:
//	- key: value
func Format(p *Plan) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## Current Plan: %s\n", p.Name)
	fmt.Fprintf(&b, "Goal: %s\n", p.Goal)

	if len(p.Steps) > 0 {
		b.WriteString("\n")
		for _, s := range p.Steps {
			marker := statusMarker(s.Status)
			fmt.Fprintf(&b, "%s %s — %s", marker, s.Name, s.Description)

			if s.NeedsUserInput {
				b.WriteString(" (needs user input)")
			}
			if s.CanRepeat {
				b.WriteString(" (repeatable)")
			}
			b.WriteString("\n")
		}
	}

	if len(p.Notes) > 0 {
		b.WriteString("\nNotes:\n")
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(p.Notes))
		for k := range p.Notes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- %s: %v\n", k, p.Notes[k])
		}
	}

	return b.String()
}

// statusMarker returns the checkbox marker for a given StepStatus.
func statusMarker(s StepStatus) string {
	switch s {
	case StatusDone:
		return "[✓]"
	case StatusActive:
		return "[>]"
	case StatusSkipped:
		return "[-]"
	default:
		return "[ ]"
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test -run TestFormat -v`
Expected: PASS -- all 8 format tests green

- [ ] **Step 5: Commit**

```bash
cd /Users/derek/repo/axons && git add contrib/plan/format.go contrib/plan/format_test.go
git commit -m "contrib/plan: add Format function for plan-to-text rendering

Renders Plan state as structured text with checkbox markers for each
step status ([✓] done, [>] active, [ ] pending, [-] skipped).
Appends annotations for NeedsUserInput and CanRepeat. Notes section
is included only when non-empty, with keys sorted for determinism."
```

---

### Task 3: Attach function -- OnStart hook, PrepareRound hook, mark_step tool, add_note tool

**Files:**
- Create: `contrib/plan/attach.go`
- Create: `contrib/plan/attach_test.go`

This is the main integration surface. `Attach(p)` returns `[]kernel.AgentOption` containing:
1. An `OnStart` hook that activates the first pending step
2. A `PrepareRound` hook that injects the formatted plan as a system message
3. A `mark_step` tool (updates step status, auto-advances next step)
4. An `add_note` tool (stores key-value pairs in Plan.Notes)

The tests use `kernel.NewAgent` with a `fakeLLM` to verify the full integration through the agent loop.

- [ ] **Step 1: Write the test file**

```go
// contrib/plan/attach_test.go
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// fakeLLM implements kernel.LLM for testing. Returns scripted responses per round.
type fakeLLM struct {
	responses []kernel.Response
	callCount int
	mu        sync.Mutex
	// captured stores GenerateParams from each call for inspection.
	captured []kernel.GenerateParams
}

func newFakeLLM(responses ...kernel.Response) *fakeLLM {
	return &fakeLLM{responses: responses}
}

func (f *fakeLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = append(f.captured, params)
	if f.callCount >= len(f.responses) {
		return kernel.Response{Text: "no more responses", FinishReason: "stop"}, nil
	}
	resp := f.responses[f.callCount]
	f.callCount++
	return resp, nil
}

func (f *fakeLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, fmt.Errorf("streaming not implemented in fakeLLM")
}

func (f *fakeLLM) Model() string { return "fake" }

// --- OnStart Hook Tests ---

func TestAttachOnStartActivatesFirstStep(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
	)

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Steps[0].Status != StatusActive {
		t.Errorf("expected first step active, got %q", p.Steps[0].Status)
	}
	if p.Steps[1].Status != StatusPending {
		t.Errorf("expected second step pending, got %q", p.Steps[1].Status)
	}
}

func TestAttachOnStartSkipsNonPending(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
	)
	// Pre-mark first step done (simulating a resumed plan).
	p.Steps[0].Status = StatusDone

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "resume")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Steps[0].Status != StatusDone {
		t.Errorf("expected first step done, got %q", p.Steps[0].Status)
	}
	if p.Steps[1].Status != StatusActive {
		t.Errorf("expected second step active, got %q", p.Steps[1].Status)
	}
}

func TestAttachOnStartAllDone(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)
	p.Steps[0].Status = StatusDone

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "nothing to do")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should remain done, no crash.
	if p.Steps[0].Status != StatusDone {
		t.Errorf("expected step done, got %q", p.Steps[0].Status)
	}
}

// --- PrepareRound Hook Tests ---

func TestAttachPrepareRoundInjectsPlanMessage(t *testing.T) {
	p := New("Booking", "Book a restaurant",
		Step{Name: "gather", Description: "Ask preferences"},
		Step{Name: "search", Description: "Find options"},
	)

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a booking assistant."),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "book something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Inspect the messages sent to the LLM.
	llm.mu.Lock()
	params := llm.captured[0]
	llm.mu.Unlock()

	// Find a system message containing the plan.
	found := false
	for _, msg := range params.Messages {
		if msg.Role == kernel.RoleSystem {
			text := msg.TextContent()
			if strings.Contains(text, "## Current Plan: Booking") {
				found = true
				// After OnStart, first step should be active.
				if !strings.Contains(text, "[>] gather") {
					t.Errorf("expected gather to be active in plan message:\n%s", text)
				}
				break
			}
		}
	}
	if !found {
		t.Error("expected plan system message in LLM params")
	}
}

// --- mark_step Tool Tests ---

func TestAttachMarkStepDoneAdvancesNext(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
		Step{Name: "s3", Description: "Third"},
	)

	llm := newFakeLLM(
		// Round 0: LLM calls mark_step to mark s1 done.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "mark_step",
				Params: json.RawMessage(`{"step_name":"s1","status":"done"}`),
			}},
			FinishReason: "tool_calls",
		},
		// Round 1: LLM responds with text.
		kernel.Response{Text: "moved on", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Steps[0].Status != StatusDone {
		t.Errorf("s1: expected done, got %q", p.Steps[0].Status)
	}
	if p.Steps[1].Status != StatusActive {
		t.Errorf("s2: expected active, got %q", p.Steps[1].Status)
	}
	if p.Steps[2].Status != StatusPending {
		t.Errorf("s3: expected pending, got %q", p.Steps[2].Status)
	}
}

func TestAttachMarkStepSkipped(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
	)

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "mark_step",
				Params: json.RawMessage(`{"step_name":"s1","status":"skipped"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "skipped", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Steps[0].Status != StatusSkipped {
		t.Errorf("s1: expected skipped, got %q", p.Steps[0].Status)
	}
	if p.Steps[1].Status != StatusActive {
		t.Errorf("s2: expected active, got %q", p.Steps[1].Status)
	}
}

func TestAttachMarkStepActivate(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
	)

	llm := newFakeLLM(
		// Mark s2 as active directly.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "mark_step",
				Params: json.RawMessage(`{"step_name":"s2","status":"active"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "ok", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// s2 should be active; s1 still had its OnStart activation but
	// the explicit mark_step to s2 sets it active.
	if p.Steps[1].Status != StatusActive {
		t.Errorf("s2: expected active, got %q", p.Steps[1].Status)
	}
}

func TestAttachMarkStepInvalidName(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)

	llm := newFakeLLM(
		// LLM tries to mark a nonexistent step.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "mark_step",
				Params: json.RawMessage(`{"step_name":"nonexistent","status":"done"}`),
			}},
			FinishReason: "tool_calls",
		},
		// LLM self-corrects and finishes.
		kernel.Response{Text: "corrected", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	result, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent should NOT crash. The error is sent back to the LLM as IsError.
	if result.Text != "corrected" {
		t.Errorf("expected LLM to self-correct, got %q", result.Text)
	}

	// The step should still be in its OnStart-activated state.
	if p.Steps[0].Status != StatusActive {
		t.Errorf("s1: expected active (unchanged), got %q", p.Steps[0].Status)
	}
}

func TestAttachMarkStepDoneLastStep(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "Only step"},
	)

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "mark_step",
				Params: json.RawMessage(`{"step_name":"s1","status":"done"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "all done", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Steps[0].Status != StatusDone {
		t.Errorf("s1: expected done, got %q", p.Steps[0].Status)
	}
}

func TestAttachMarkStepReturnMessage(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "gather", Description: "First"},
		Step{Name: "search", Description: "Second"},
	)

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "mark_step",
				Params: json.RawMessage(`{"step_name":"gather","status":"done"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "ok", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	result, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the tool result message contains step name and next step info.
	// Check the captured messages from the second LLM call (round 1).
	llm.mu.Lock()
	secondCallParams := llm.captured[1]
	llm.mu.Unlock()

	// Find the tool result message in the conversation.
	foundToolResult := false
	for _, msg := range secondCallParams.Messages {
		if msg.Role == kernel.RoleTool {
			for _, part := range msg.Content {
				if part.ToolResult != nil && part.ToolResult.Name == "mark_step" {
					foundToolResult = true
					content := part.ToolResult.Content
					if !strings.Contains(content, "gather") {
						t.Errorf("tool result should mention step name, got: %s", content)
					}
					if !strings.Contains(content, "done") {
						t.Errorf("tool result should mention status, got: %s", content)
					}
					if !strings.Contains(content, "search") {
						t.Errorf("tool result should mention next step, got: %s", content)
					}
					if part.ToolResult.IsError {
						t.Error("tool result should not be IsError for valid step")
					}
				}
			}
		}
	}
	if !foundToolResult {
		t.Error("expected mark_step tool result in conversation")
	}

	_ = result
}

// --- add_note Tool Tests ---

func TestAttachAddNote(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "add_note",
				Params: json.RawMessage(`{"key":"cuisine","value":"Italian"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "noted", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Notes["cuisine"] != "Italian" {
		t.Errorf("expected note cuisine=Italian, got %v", p.Notes["cuisine"])
	}
}

func TestAttachAddNoteOverwrite(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)
	p.Notes["cuisine"] = "Thai"

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "add_note",
				Params: json.RawMessage(`{"key":"cuisine","value":"Italian"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "updated", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Attach(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Notes["cuisine"] != "Italian" {
		t.Errorf("expected note overwritten to Italian, got %v", p.Notes["cuisine"])
	}
}

// --- Multi-step Integration Test ---

func TestAttachFullIntegration(t *testing.T) {
	p := New("Booking", "Book restaurant",
		Step{Name: "gather", Description: "Ask preferences"},
		Step{Name: "search", Description: "Find options"},
		Step{Name: "confirm", Description: "Confirm booking", NeedsUserInput: true},
	)

	llm := newFakeLLM(
		// Round 0: add a note and mark gather done.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{
				{ID: "c1", Name: "add_note", Params: json.RawMessage(`{"key":"cuisine","value":"Italian"}`)},
				{ID: "c2", Name: "mark_step", Params: json.RawMessage(`{"step_name":"gather","status":"done"}`)},
			},
			FinishReason: "tool_calls",
		},
		// Round 1: mark search done.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{
				{ID: "c3", Name: "mark_step", Params: json.RawMessage(`{"step_name":"search","status":"done"}`)},
			},
			FinishReason: "tool_calls",
		},
		// Round 2: mark confirm done and finish.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{
				{ID: "c4", Name: "mark_step", Params: json.RawMessage(`{"step_name":"confirm","status":"done"}`)},
			},
			FinishReason: "tool_calls",
		},
		// Round 3: final text.
		kernel.Response{Text: "All booked!", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a booking assistant."),
	}, Attach(p)...)...)

	result, err := agent.Run(context.Background(), "Book Italian for 4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All steps done.
	for _, s := range p.Steps {
		if s.Status != StatusDone {
			t.Errorf("step %q: expected done, got %q", s.Name, s.Status)
		}
	}

	// Note stored.
	if p.Notes["cuisine"] != "Italian" {
		t.Errorf("expected cuisine=Italian, got %v", p.Notes["cuisine"])
	}

	if result.Text != "All booked!" {
		t.Errorf("expected final text %q, got %q", "All booked!", result.Text)
	}

	// Verify the plan was injected into every round.
	llm.mu.Lock()
	capturedCount := len(llm.captured)
	llm.mu.Unlock()
	if capturedCount != 4 {
		t.Fatalf("expected 4 LLM calls, got %d", capturedCount)
	}

	// The last LLM call should have the plan with all steps done except confirm
	// (confirm was marked done by the tool call in round 2, but the PrepareRound
	// for round 3 runs AFTER that tool result was processed).
	llm.mu.Lock()
	lastParams := llm.captured[3]
	llm.mu.Unlock()

	planFound := false
	for _, msg := range lastParams.Messages {
		if msg.Role == kernel.RoleSystem {
			text := msg.TextContent()
			if strings.Contains(text, "## Current Plan: Booking") {
				planFound = true
				// All three steps should show as done.
				if !strings.Contains(text, "[✓] gather") {
					t.Errorf("expected gather done in final plan")
				}
				if !strings.Contains(text, "[✓] search") {
					t.Errorf("expected search done in final plan")
				}
				if !strings.Contains(text, "[✓] confirm") {
					t.Errorf("expected confirm done in final plan")
				}
				if !strings.Contains(text, "cuisine: Italian") {
					t.Errorf("expected cuisine note in final plan")
				}
			}
		}
	}
	if !planFound {
		t.Error("expected plan in final LLM call messages")
	}
}

// --- Tool Registration Tests ---

func TestAttachRegistersTools(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)

	opts := Attach(p)
	// Attach should return at least 3 options (OnStart, PrepareRound, WithTools).
	if len(opts) < 3 {
		t.Fatalf("expected at least 3 agent options, got %d", len(opts))
	}

	// Build an agent and verify the tools are registered.
	llm := newFakeLLM(kernel.Response{Text: "ok", FinishReason: "stop"})
	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, opts...)...)

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tools were available to the LLM.
	llm.mu.Lock()
	params := llm.captured[0]
	llm.mu.Unlock()

	toolNames := make(map[string]bool)
	for _, tool := range params.Tools {
		toolNames[tool.Name()] = true
	}

	if !toolNames["mark_step"] {
		t.Error("expected mark_step tool to be registered")
	}
	if !toolNames["add_note"] {
		t.Error("expected add_note tool to be registered")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test -run TestAttach -v`
Expected: FAIL (attach.go does not exist)

- [ ] **Step 3: Write the implementation**

```go
// contrib/plan/attach.go
package plan

import (
	"context"
	"fmt"
	"sync"

	"github.com/axonframework/axon/kernel"
)

// markStepParams is the typed parameter struct for the mark_step tool.
type markStepParams struct {
	StepName string `json:"step_name" description:"Name of the step to update"`
	Status   string `json:"status" description:"New status for the step" enum:"done,skipped,active"`
}

// addNoteParams is the typed parameter struct for the add_note tool.
type addNoteParams struct {
	Key   string `json:"key" description:"Note key"`
	Value string `json:"value" description:"Note value"`
}

// Attach returns AgentOptions that wire a Plan into an agent via hooks and tools.
// Spread into NewAgent: kernel.NewAgent(append(baseOpts, plan.Attach(p)...)...)
func Attach(p *Plan) []kernel.AgentOption {
	var mu sync.Mutex

	markStep := kernel.NewTool("mark_step",
		"Update a plan step's status. When a step is marked done or skipped, the next pending step is automatically activated.",
		func(ctx context.Context, params markStepParams) (string, error) {
			mu.Lock()
			defer mu.Unlock()

			status := StepStatus(params.Status)

			// Find the step by name.
			idx := -1
			for i, s := range p.Steps {
				if s.Name == params.StepName {
					idx = i
					break
				}
			}
			if idx == -1 {
				return "", fmt.Errorf("step %q not found in plan %q", params.StepName, p.Name)
			}

			p.Steps[idx].Status = status

			// Auto-advance: if done or skipped, activate the next pending step.
			if status == StatusDone || status == StatusSkipped {
				next := activateNextPending(p)
				if next != "" {
					return fmt.Sprintf("Step '%s' marked as %s. Next: '%s'", params.StepName, status, next), nil
				}
				return fmt.Sprintf("Step '%s' marked as %s. All steps complete.", params.StepName, status), nil
			}

			return fmt.Sprintf("Step '%s' marked as %s.", params.StepName, status), nil
		},
	)

	addNote := kernel.NewTool("add_note",
		"Store a key-value note in the plan for reference in later steps.",
		func(ctx context.Context, params addNoteParams) (string, error) {
			mu.Lock()
			defer mu.Unlock()

			p.Notes[params.Key] = params.Value
			return fmt.Sprintf("Note added: %s = %s", params.Key, params.Value), nil
		},
	)

	onStart := kernel.OnStart(func(tc *kernel.TurnContext) {
		mu.Lock()
		defer mu.Unlock()
		activateNextPending(p)
	})

	prepareRound := kernel.PrepareRound(func(rc *kernel.RoundContext) {
		mu.Lock()
		text := Format(p)
		mu.Unlock()
		rc.AgentCtx.AddMessages(kernel.SystemMsg(text))
	})

	return []kernel.AgentOption{
		onStart,
		prepareRound,
		kernel.WithTools(markStep, addNote),
	}
}

// activateNextPending finds the first pending step and sets it to active.
// Returns the name of the activated step, or empty string if none found.
// Caller must hold the mutex.
func activateNextPending(p *Plan) string {
	for i, s := range p.Steps {
		if s.Status == StatusPending {
			p.Steps[i].Status = StatusActive
			return s.Name
		}
	}
	return ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test ./... -v`
Expected: PASS -- all tests green (plan_test.go, format_test.go, attach_test.go)

- [ ] **Step 5: Commit**

```bash
cd /Users/derek/repo/axons && git add contrib/plan/attach.go contrib/plan/attach_test.go
git commit -m "contrib/plan: add Attach function with hooks and tools

Attach(p) returns []kernel.AgentOption that wires a Plan into an agent:
- OnStart hook activates the first pending step
- PrepareRound hook injects formatted plan state as a system message
- mark_step tool lets the LLM update step status with auto-advance
- add_note tool lets the LLM store key-value notes for later steps

Invalid step names in mark_step return an error (IsError: true) so the
LLM can self-correct. Plan mutations are mutex-protected."
```

---

### Task 4: Self-review -- run full test suite, verify coverage, check for issues

- [ ] **Step 1: Run all tests with verbose output**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test ./... -v`
Expected: All tests pass.

- [ ] **Step 2: Run tests with race detector**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test ./... -race`
Expected: No race conditions detected. The mutex in Attach protects all Plan mutations, and tool calls that run in parallel (like the add_note + mark_step combo in TestAttachFullIntegration) are safe.

- [ ] **Step 3: Check test coverage**

Run: `cd /Users/derek/repo/axons/contrib/plan && go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`
Expected: High coverage across all three files. Key paths covered:
- `plan.go`: New with steps, New without steps, status constants
- `format.go`: all status markers, annotations, notes present/absent
- `attach.go`: OnStart activation (first pending, skip done, all done), PrepareRound injection, mark_step (done, skipped, active, invalid name, last step), add_note (new, overwrite), tool registration

Clean up: `rm -f /Users/derek/repo/axons/contrib/plan/cover.out`

- [ ] **Step 4: Verify go vet passes**

Run: `cd /Users/derek/repo/axons/contrib/plan && go vet ./...`
Expected: No issues.

- [ ] **Step 5: Review checklist**

Verify these match the spec:
- [ ] `Plan` struct has `Name`, `Goal`, `Steps []Step`, `Notes map[string]any`
- [ ] `Step` struct has `Name`, `Description`, `Status StepStatus`, `NeedsUserInput`, `CanRepeat`
- [ ] `StepStatus` constants: `"pending"`, `"active"`, `"done"`, `"skipped"`
- [ ] `New` sets all steps to `StatusPending` and initializes `Notes`
- [ ] `Format` uses `[✓]`, `[>]`, `[ ]`, `[-]` markers
- [ ] `Attach` returns `[]kernel.AgentOption`
- [ ] `OnStart` activates first pending step
- [ ] `PrepareRound` adds plan as system message each round
- [ ] `mark_step` tool: params `{step_name, status}`, enum `done|skipped|active`, auto-advances
- [ ] `add_note` tool: params `{key, value}`, stores in `Plan.Notes`
- [ ] Invalid step name returns `error` (agent sets `IsError: true`)
- [ ] Plan struct is JSON-serializable for user-managed persistence
- [ ] No dependencies beyond `kernel/`
