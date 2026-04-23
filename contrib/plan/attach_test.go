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
	// Attach returns OnStart and PrepareRound; tools are registered via OnStart.
	if len(opts) < 2 {
		t.Fatalf("expected at least 2 agent options, got %d", len(opts))
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

// TestAttachDoesNotClobberUserTools verifies that combining plan.Attach with
// kernel.WithTools(userTool) preserves both plan tools and user tools.
// Previously, WithTools replaced the tool slice, silently dropping plan tools.
func TestAttachDoesNotClobberUserTools(t *testing.T) {
	p := New("test", "goal", Step{Name: "s1", Description: "step"})

	userTool := kernel.NewTool(
		"my_tool", "A user-defined tool",
		func(ctx context.Context, _ struct{}) (string, error) { return "ok", nil },
	)

	llm := newFakeLLM(kernel.Response{Text: "ok", FinishReason: "stop"})

	// WithTools(userTool) comes after Attach — the collision pattern.
	opts := append(Attach(p), kernel.WithModel(llm), kernel.WithTools(userTool))
	agent := kernel.NewAgent(opts...)

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llm.mu.Lock()
	params := llm.captured[0]
	llm.mu.Unlock()

	toolNames := make(map[string]bool)
	for _, tool := range params.Tools {
		toolNames[tool.Name()] = true
	}

	for _, name := range []string{"mark_step", "add_note", "my_tool"} {
		if !toolNames[name] {
			t.Errorf("expected tool %q visible to LLM, got: %v", name, toolNames)
		}
	}
}
