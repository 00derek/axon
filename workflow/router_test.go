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
	r := Router(classify, map[string]WorkflowStep{"booking": bookingStep, "general": generalStep})
	result, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{"intent": "booking"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["handler"] != "booking" {
		t.Errorf("expected %q, got %v", "booking", result.Data["handler"])
	}
}

func TestRouterSelectsAlternateRoute(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string { return "general" }
	r := Router(classify, map[string]WorkflowStep{
		"booking": Step("booking", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["handler"] = "booking"
			return state, nil
		}),
		"general": Step("general", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["handler"] = "general"
			return state, nil
		}),
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
	classify := func(ctx context.Context, state *WorkflowState) string { return "unknown" }
	r := Router(classify, map[string]WorkflowStep{
		"known": Step("known", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) { return state, nil }),
	})
	_, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for unknown route")
	}
}

func TestRouterPassesStateThroughToRoute(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string { return "target" }
	r := Router(classify, map[string]WorkflowStep{
		"target": Step("target", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
			state.Data["read_input"] = state.Input
			state.Data["read_existing"] = state.Data["existing"]
			return state, nil
		}),
	})
	result, err := r.Run(context.Background(), &WorkflowState{Input: "hello", Data: map[string]any{"existing": "value"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["read_input"] != "hello" {
		t.Errorf("expected %q, got %v", "hello", result.Data["read_input"])
	}
	if result.Data["read_existing"] != "value" {
		t.Errorf("expected existing data, got %v", result.Data["read_existing"])
	}
}

func TestRouterPropagatesStepError(t *testing.T) {
	classify := func(ctx context.Context, state *WorkflowState) string { return "fail" }
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
		func(ctx context.Context, state *WorkflowState) string { return state.Data["intent"].(string) },
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
	result, err := wf.Run(context.Background(), &WorkflowState{Input: "book", Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["result"] != "booked" {
		t.Errorf("expected %q, got %v", "booked", result.Data["result"])
	}
}
