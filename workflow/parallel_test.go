// workflow/parallel_test.go
package workflow

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	kernel "github.com/axonframework/axon/kernel"
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

	if elapsed > 90*time.Millisecond {
		t.Errorf("expected parallel execution (~50ms), took %v", elapsed)
	}

	peak1 := result.Data["peak1"].(int)
	peak2 := result.Data["peak2"].(int)
	if peak1 < 2 && peak2 < 2 {
		t.Errorf("expected concurrent execution (peak >= 2), got peak1=%d peak2=%d", peak1, peak2)
	}
}

func TestParallelReceivesCopyOfData(t *testing.T) {
	step1 := Step("reader", func(ctx context.Context, state *WorkflowState) (*WorkflowState, error) {
		time.Sleep(20 * time.Millisecond)
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
