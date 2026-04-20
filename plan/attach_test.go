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

func TestEnableOnStartActivatesFirstStep(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
	)

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

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

func TestEnableOnStartSkipsNonPending(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
	)
	// Pre-mark first step done (simulating a resumed plan).
	p.Steps[0].Status = StatusDone

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

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

func TestEnableOnStartAllDone(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)
	p.Steps[0].Status = StatusDone

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "nothing to do")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should remain done, no crash.
	if p.Steps[0].Status != StatusDone {
		t.Errorf("expected step done, got %q", p.Steps[0].Status)
	}
}

func TestEnableStoresPlanInState(t *testing.T) {
	p := New("Test", "Goal", Step{Name: "s1", Description: "First"})

	var capturedState *Plan
	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	captureHook := kernel.OnStart(func(tc *kernel.TurnContext) {
		// Read after Enable's OnStart fires; hook order is registration order.
		if v, ok := tc.AgentCtx.State[StateKey]; ok {
			if pp, ok := v.(*Plan); ok {
				capturedState = pp
			}
		}
	})

	opts := append(Enable(p), kernel.WithModel(llm), captureHook)
	agent := kernel.NewAgent(opts...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedState == nil {
		t.Fatal("expected plan stored in ctx.State[\"plan\"]")
	}
	if capturedState != p {
		t.Error("expected stored plan to be the same pointer as the one passed to Enable")
	}
}

// --- PrepareRound Hook Tests ---

func TestEnablePrepareRoundInjectsPlanMessage(t *testing.T) {
	p := New("Booking", "Book a restaurant",
		Step{Name: "gather", Description: "Ask preferences"},
		Step{Name: "search", Description: "Find options"},
	)

	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a booking assistant."),
	}, Enable(p)...)...)

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

func TestEnablePrepareRoundInjectsEnforcementDirective(t *testing.T) {
	p := New("Test", "Goal", Step{Name: "s1", Description: "First"})
	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llm.mu.Lock()
	params := llm.captured[0]
	llm.mu.Unlock()

	found := false
	for _, msg := range params.Messages {
		if msg.Role == kernel.RoleSystem && strings.Contains(msg.TextContent(), "mark_step") {
			found = true
			if !strings.Contains(msg.TextContent(), "every step is done or skipped") {
				t.Errorf("expected enforcement directive in system prompt:\n%s", msg.TextContent())
			}
			break
		}
	}
	if !found {
		t.Error("expected enforcement directive in system prompt")
	}
}

func TestEnablePreservesBaseSystemPrompt(t *testing.T) {
	p := New("Test", "Goal", Step{Name: "s1", Description: "First"})
	llm := newFakeLLM(kernel.Response{Text: "done", FinishReason: "stop"})

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a helpful assistant."),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	llm.mu.Lock()
	params := llm.captured[0]
	llm.mu.Unlock()

	var sys string
	for _, msg := range params.Messages {
		if msg.Role == kernel.RoleSystem {
			sys = msg.TextContent()
			break
		}
	}
	if !strings.Contains(sys, "You are a helpful assistant.") {
		t.Errorf("expected base prompt preserved, got:\n%s", sys)
	}
	if !strings.Contains(sys, "## Current Plan:") {
		t.Errorf("expected plan appended to base prompt, got:\n%s", sys)
	}
}

// --- create_plan Tool Tests ---

func TestCreatePlanOnEmptyPlan(t *testing.T) {
	p := Empty()

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:   "c1",
				Name: "create_plan",
				Params: json.RawMessage(`{
					"name": "greet-flow",
					"goal": "Greet the user and ask what they need",
					"steps": [
						{"name": "say_hello", "description": "Say hello"},
						{"name": "ask_intent", "description": "Ask the user's intent", "needs_user_input": true}
					]
				}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "hi", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Name != "greet-flow" {
		t.Errorf("expected plan name 'greet-flow', got %q", p.Name)
	}
	if p.Goal != "Greet the user and ask what they need" {
		t.Errorf("unexpected goal: %q", p.Goal)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(p.Steps))
	}
	if p.Steps[0].Status != StatusActive {
		t.Errorf("expected first step active after create_plan, got %q", p.Steps[0].Status)
	}
	if p.Steps[1].Status != StatusPending {
		t.Errorf("expected second step pending, got %q", p.Steps[1].Status)
	}
	if !p.Steps[1].NeedsUserInput {
		t.Error("expected NeedsUserInput preserved from create_plan input")
	}
}

func TestCreatePlanRejectsOnSeededPlan(t *testing.T) {
	p := New("existing", "Already here", Step{Name: "s1", Description: "First"})

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "c1",
				Name:   "create_plan",
				Params: json.RawMessage(`{"name":"new","goal":"new","steps":[{"name":"x","description":"y"}]}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "corrected", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Plan should be unchanged.
	if p.Name != "existing" {
		t.Errorf("expected plan name unchanged, got %q", p.Name)
	}
	if len(p.Steps) != 1 || p.Steps[0].Name != "s1" {
		t.Errorf("expected plan steps unchanged, got %+v", p.Steps)
	}

	// Check the tool result was an error.
	llm.mu.Lock()
	secondCall := llm.captured[1]
	llm.mu.Unlock()

	var gotErr bool
	for _, msg := range secondCall.Messages {
		if msg.Role != kernel.RoleTool {
			continue
		}
		for _, part := range msg.Content {
			if part.ToolResult != nil && part.ToolResult.Name == "create_plan" && part.ToolResult.IsError {
				gotErr = true
			}
		}
	}
	if !gotErr {
		t.Error("expected create_plan tool result to be IsError when plan already seeded")
	}
}

func TestCreatePlanRejectsEmptySteps(t *testing.T) {
	p := Empty()

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "c1",
				Name:   "create_plan",
				Params: json.RawMessage(`{"name":"x","goal":"y","steps":[]}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "retry later", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p.Steps) != 0 {
		t.Errorf("expected no steps added on rejected create_plan, got %d", len(p.Steps))
	}
}

// --- append_step Tool Tests ---

func TestAppendStepAddsPendingStep(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "c1",
				Name:   "append_step",
				Params: json.RawMessage(`{"name":"s2","description":"Discovered later"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "added", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p.Steps) != 2 {
		t.Fatalf("expected 2 steps after append, got %d", len(p.Steps))
	}
	if p.Steps[1].Name != "s2" {
		t.Errorf("expected appended step named 's2', got %q", p.Steps[1].Name)
	}
	if p.Steps[1].Status != StatusPending {
		t.Errorf("expected appended step pending, got %q", p.Steps[1].Status)
	}
}

func TestAppendStepRejectsDuplicate(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "c1",
				Name:   "append_step",
				Params: json.RawMessage(`{"name":"s1","description":"dup"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "corrected", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p.Steps) != 1 {
		t.Errorf("expected step count unchanged on duplicate append, got %d", len(p.Steps))
	}
}

// --- mark_step Tool Tests ---

func TestMarkStepDoneAdvancesNext(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
		Step{Name: "s2", Description: "Second"},
		Step{Name: "s3", Description: "Third"},
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
		kernel.Response{Text: "moved on", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

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

func TestMarkStepSkipped(t *testing.T) {
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
	}, Enable(p)...)...)

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

func TestMarkStepInvalidName(t *testing.T) {
	p := New("Test", "Goal",
		Step{Name: "s1", Description: "First"},
	)

	llm := newFakeLLM(
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     "call-1",
				Name:   "mark_step",
				Params: json.RawMessage(`{"step_name":"nonexistent","status":"done"}`),
			}},
			FinishReason: "tool_calls",
		},
		kernel.Response{Text: "corrected", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	result, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "corrected" {
		t.Errorf("expected LLM to self-correct, got %q", result.Text)
	}
	if p.Steps[0].Status != StatusActive {
		t.Errorf("s1: expected active (unchanged), got %q", p.Steps[0].Status)
	}
}

// --- add_note Tool Tests ---

func TestAddNote(t *testing.T) {
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
	}, Enable(p)...)...)

	_, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Notes["cuisine"] != "Italian" {
		t.Errorf("expected note cuisine=Italian, got %v", p.Notes["cuisine"])
	}
}

// --- Tool Registration ---

func TestEnableRegistersAllFourTools(t *testing.T) {
	p := Empty()

	llm := newFakeLLM(kernel.Response{Text: "ok", FinishReason: "stop"})
	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

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

	for _, name := range []string{"create_plan", "append_step", "mark_step", "add_note"} {
		if !toolNames[name] {
			t.Errorf("expected %s tool to be registered", name)
		}
	}
}

// --- Full integration (agent-seeded) ---

func TestAgentSeededFullFlow(t *testing.T) {
	p := Empty()

	llm := newFakeLLM(
		// Round 0: agent drafts its own plan.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:   "c1",
				Name: "create_plan",
				Params: json.RawMessage(`{
					"name": "booking",
					"goal": "Book Italian",
					"steps": [
						{"name": "gather", "description": "Ask preferences"},
						{"name": "search", "description": "Find options"}
					]
				}`),
			}},
			FinishReason: "tool_calls",
		},
		// Round 1: note + finish first step.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{
				{ID: "c2", Name: "add_note", Params: json.RawMessage(`{"key":"cuisine","value":"Italian"}`)},
				{ID: "c3", Name: "mark_step", Params: json.RawMessage(`{"step_name":"gather","status":"done"}`)},
			},
			FinishReason: "tool_calls",
		},
		// Round 2: append a step the agent didn't anticipate + finish search.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{
				{ID: "c4", Name: "append_step", Params: json.RawMessage(`{"name":"confirm","description":"Confirm booking"}`)},
				{ID: "c5", Name: "mark_step", Params: json.RawMessage(`{"step_name":"search","status":"done"}`)},
			},
			FinishReason: "tool_calls",
		},
		// Round 3: finish the appended step.
		kernel.Response{
			ToolCalls: []kernel.ToolCall{
				{ID: "c6", Name: "mark_step", Params: json.RawMessage(`{"step_name":"confirm","status":"done"}`)},
			},
			FinishReason: "tool_calls",
		},
		// Round 4: final text.
		kernel.Response{Text: "All done!", FinishReason: "stop"},
	)

	agent := kernel.NewAgent(append([]kernel.AgentOption{
		kernel.WithModel(llm),
	}, Enable(p)...)...)

	result, err := agent.Run(context.Background(), "Book Italian for 4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text != "All done!" {
		t.Errorf("expected final text 'All done!', got %q", result.Text)
	}
	if !p.IsComplete() {
		t.Errorf("expected plan complete, got steps %+v", p.Steps)
	}
	if len(p.Steps) != 3 {
		t.Errorf("expected 3 steps after append, got %d", len(p.Steps))
	}
	if p.Notes["cuisine"] != "Italian" {
		t.Errorf("expected cuisine=Italian note")
	}
}
