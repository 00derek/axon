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

func TestFullWorkflowComposition(t *testing.T) {
	wf := NewWorkflow(
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
		Router(
			func(ctx context.Context, state *WorkflowState) string { return state.Data["intent"].(string) },
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
		Conditional(
			func(ctx context.Context, state *WorkflowState) bool { return state.Data["intent"] == "booking" },
			Step("send-confirmation", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
				state.Data["confirmation_sent"] = true
				return state, nil
			}),
			nil,
		),
	)
	result, err := wf.Run(context.Background(), &WorkflowState{Input: "book a table", Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
			func(ctx context.Context, state *WorkflowState) string { return state.Data["mode"].(string) },
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
