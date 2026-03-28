// workflow/workflow_test.go
package workflow

import (
	"context"
	"testing"

	kernel "github.com/axonframework/axon/kernel"
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
