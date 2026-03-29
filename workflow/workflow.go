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

// NewWorkflow composes steps sequentially.
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
