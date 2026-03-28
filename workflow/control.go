// workflow/control.go
package workflow

import "context"

type retryUntilStep struct {
	name  string
	body  WorkflowStep
	until func(context.Context, *WorkflowState) bool
}

func RetryUntil(name string, body WorkflowStep, until func(context.Context, *WorkflowState) bool) WorkflowStep {
	return &retryUntilStep{name: name, body: body, until: until}
}

func (r *retryUntilStep) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()
	state := input
	for !r.until(ctx, state) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var err error
		state, err = r.body.Run(ctx, state)
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}

type conditionalStep struct {
	check   func(context.Context, *WorkflowState) bool
	ifTrue  WorkflowStep
	ifFalse WorkflowStep
}

func Conditional(check func(context.Context, *WorkflowState) bool, ifTrue WorkflowStep, ifFalse WorkflowStep) WorkflowStep {
	return &conditionalStep{check: check, ifTrue: ifTrue, ifFalse: ifFalse}
}

func (c *conditionalStep) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()
	if c.check(ctx, input) {
		if c.ifTrue == nil {
			return input, nil
		}
		return c.ifTrue.Run(ctx, input)
	}
	if c.ifFalse == nil {
		return input, nil
	}
	return c.ifFalse.Run(ctx, input)
}
