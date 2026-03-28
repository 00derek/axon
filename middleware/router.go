// middleware/router.go
package middleware

import (
	"context"

	"github.com/axonframework/axon/kernel"
)

// RouteContext provides context for routing decisions.
type RouteContext struct {
	Params kernel.GenerateParams
	Ctx    context.Context
}

// Route pairs a condition with a target LLM. When the condition returns true,
// the request is routed to the paired Model.
type Route struct {
	Model     kernel.LLM
	Condition func(RouteContext) bool
}

// NewRouter creates an LLM that routes requests to different models based on
// conditions. Routes are evaluated in order; the first matching condition wins.
// If no condition matches, the fallback LLM is used.
//
// The router itself implements kernel.LLM, so it can be used anywhere an LLM
// is expected, including as a target inside another router.
func NewRouter(fallback kernel.LLM, routes ...Route) kernel.LLM {
	return &routerLLM{fallback: fallback, routes: routes}
}

type routerLLM struct {
	fallback kernel.LLM
	routes   []Route
}

func (r *routerLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	target := r.resolve(ctx, params)
	return target.Generate(ctx, params)
}

func (r *routerLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	target := r.resolve(ctx, params)
	return target.GenerateStream(ctx, params)
}

// Model returns "router" since the router delegates to multiple models.
func (r *routerLLM) Model() string {
	return "router"
}

// resolve evaluates routes in order and returns the first matching model,
// or the fallback if no condition matches.
func (r *routerLLM) resolve(ctx context.Context, params kernel.GenerateParams) kernel.LLM {
	rc := RouteContext{Params: params, Ctx: ctx}
	for _, route := range r.routes {
		if route.Condition(rc) {
			return route.Model
		}
	}
	return r.fallback
}
