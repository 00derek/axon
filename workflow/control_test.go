// workflow/control_test.go
package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRetryUntilConverges(t *testing.T) {
	body := Step("increment", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		count, _ := state.Data["count"].(int)
		state.Data["count"] = count + 1
		return state, nil
	})
	until := func(ctx context.Context, state *WorkflowState) bool { return state.Data["count"].(int) >= 3 }
	r := RetryUntil("count-to-3", body, until)
	result, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{"count": 0}})
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
	until := func(ctx context.Context, state *WorkflowState) bool { return true }
	r := RetryUntil("already-done", body, until)
	result, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ran := result.Data["ran"]; ran {
		t.Error("body should not run when condition is already satisfied")
	}
}

func TestRetryUntilPropagatesBodyError(t *testing.T) {
	body := Step("fail", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		return nil, fmt.Errorf("body failed")
	})
	until := func(ctx context.Context, state *WorkflowState) bool { return false }
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
	until := func(ctx context.Context, state *WorkflowState) bool { return false }
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
	until := func(ctx context.Context, state *WorkflowState) bool { return len(state.Data["trail"].(string)) >= 4 }
	r := RetryUntil("accumulate", body, until)
	result, err := r.Run(context.Background(), &WorkflowState{Data: map[string]any{"trail": ""}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["trail"] != "xxxx" {
		t.Errorf("expected %q, got %v", "xxxx", result.Data["trail"])
	}
}

func TestConditionalTrue(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool { return state.Data["flag"].(bool) }
	ifTrue := Step("true-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "true"
		return state, nil
	})
	ifFalse := Step("false-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "false"
		return state, nil
	})
	c := Conditional(check, ifTrue, ifFalse)
	result, err := c.Run(context.Background(), &WorkflowState{Data: map[string]any{"flag": true}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["branch"] != "true" {
		t.Errorf("expected %q, got %v", "true", result.Data["branch"])
	}
}

func TestConditionalFalse(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool { return state.Data["flag"].(bool) }
	ifTrue := Step("true-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "true"
		return state, nil
	})
	ifFalse := Step("false-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["branch"] = "false"
		return state, nil
	})
	c := Conditional(check, ifTrue, ifFalse)
	result, err := c.Run(context.Background(), &WorkflowState{Data: map[string]any{"flag": false}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["branch"] != "false" {
		t.Errorf("expected %q, got %v", "false", result.Data["branch"])
	}
}

func TestConditionalNilFalseBranch(t *testing.T) {
	check := func(ctx context.Context, state *WorkflowState) bool { return false }
	ifTrue := Step("true-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})
	c := Conditional(check, ifTrue, nil)
	result, err := c.Run(context.Background(), &WorkflowState{Data: map[string]any{"original": "kept"}})
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
	check := func(ctx context.Context, state *WorkflowState) bool { return true }
	ifFalse := Step("false-branch", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		state.Data["ran"] = true
		return state, nil
	})
	c := Conditional(check, nil, ifFalse)
	result, err := c.Run(context.Background(), &WorkflowState{Data: map[string]any{"original": "kept"}})
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
	check := func(ctx context.Context, state *WorkflowState) bool { return true }
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
		func(ctx context.Context, state *WorkflowState) bool { return state.Data["score"].(int) >= 80 },
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
		func(ctx context.Context, state *WorkflowState) bool { return state.Data["attempts"].(int) >= 2 },
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
