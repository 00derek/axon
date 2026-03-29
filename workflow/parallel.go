// workflow/parallel.go
package workflow

import (
	"context"
	"sync"

	kernel "github.com/axonframework/axon/kernel"
)

// parallelStep runs multiple steps concurrently, then merges results in declaration order.
type parallelStep struct {
	steps []WorkflowStep
}

// Parallel creates a step that runs all child steps concurrently.
// Each step receives a shallow copy of Data. After all complete, Data maps merge in declaration order.
// If two steps write to the same key, last in declaration order wins. Messages concatenated in order.
func Parallel(steps ...WorkflowStep) WorkflowStep {
	return &parallelStep{steps: steps}
}

func copyData(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func copyMessages(src []kernel.Message) []kernel.Message {
	if src == nil {
		return nil
	}
	dst := make([]kernel.Message, len(src))
	copy(dst, src)
	return dst
}

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

	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
	}

	merged := &WorkflowState{
		Input:    input.Input,
		Messages: copyMessages(input.Messages),
		Data:     copyData(input.Data),
	}

	for _, r := range results {
		if r.state == nil {
			continue
		}
		for k, v := range r.state.Data {
			merged.Data[k] = v
		}
		if len(r.state.Messages) > len(input.Messages) {
			newMsgs := r.state.Messages[len(input.Messages):]
			merged.Messages = append(merged.Messages, newMsgs...)
		}
	}

	return merged, nil
}
