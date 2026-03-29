// workflow/router.go
package workflow

import (
	"context"
	"fmt"
)

type routerStep struct {
	classify func(context.Context, *WorkflowState) string
	routes   map[string]WorkflowStep
}

func Router(classify func(context.Context, *WorkflowState) string, routes map[string]WorkflowStep) WorkflowStep {
	return &routerStep{classify: classify, routes: routes}
}

func (r *routerStep) Run(ctx context.Context, input *WorkflowState) (*WorkflowState, error) {
	input.initData()
	key := r.classify(ctx, input)
	step, ok := r.routes[key]
	if !ok {
		return nil, fmt.Errorf("workflow router: no route for key %q", key)
	}
	return step.Run(ctx, input)
}
