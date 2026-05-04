// Package guards wires the three axes of agent safety — input, tool-parameter,
// and output — into a single capability. Spread Enable's result into NewAgent:
//
//	agent := kernel.NewAgent(append(baseOpts, guards.Enable(
//	    guards.WithInput(blocklist),
//	    guards.WithTool(toolPolicy),
//	    guards.WithOutput(piiRedactor),
//	)...)...)
//
// # Rejection behaviors
//
// Each axis has a clear, documented rejection behavior:
//
//   - Input guard (interfaces.Guard, OnStart): when an input is rejected, all
//     tools registered on the agent are disabled for the turn so the model
//     responds without taking any action. This mirrors the existing pattern in
//     examples/07-restaurant-bot. The agent still produces a (refusal) response.
//
//   - Tool-param guard (interfaces.ToolGuard, OnStart wraps tools): when a
//     pending tool call is rejected, the underlying tool is NOT executed.
//     Instead the wrapper returns an error containing the guard's Reason. The
//     kernel surfaces that error to the LLM as a tool-error message and the
//     agent loop continues — the model can then adjust its behavior on the
//     next round. (Option A from issue #17: continue the loop with a tool
//     error, not abort the whole Run.)
//
//   - Output guard (interfaces.OutputGuard, OnFinish): when the final response
//     is rejected, the agent's Result.Text is replaced with a sanitized message
//     ("[blocked: <reason>]") and the OutputBlocked flag is stashed in
//     AgentContext.State under StateOutputBlockedKey. The Run does NOT error;
//     callers can detect the rejection via state inspection or by recognizing
//     the [blocked:] prefix.
package guards

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/kernel"
)

// StateOutputBlockedKey is the AgentContext.State key set to true when the
// output guard rejected the final response. Callers can read
// agentCtx.State[guards.StateOutputBlockedKey] after Run to detect rejection.
const StateOutputBlockedKey = "guards.output_blocked"

// StateOutputBlockReasonKey is the AgentContext.State key holding the reason
// string from the output guard when StateOutputBlockedKey is true.
const StateOutputBlockReasonKey = "guards.output_block_reason"

// config aggregates the optional guards for a single Enable call.
type config struct {
	input  interfaces.Guard
	tool   interfaces.ToolGuard
	output interfaces.OutputGuard
}

// Option configures Enable.
type Option func(*config)

// WithInput attaches an input guard. Fired in OnStart; if rejected, all tools
// are disabled for the turn.
func WithInput(g interfaces.Guard) Option {
	return func(c *config) { c.input = g }
}

// WithTool attaches a tool-parameter guard. Each registered tool is wrapped so
// its Execute first calls the guard. Rejected calls return an error to the LLM
// (the agent loop continues).
func WithTool(g interfaces.ToolGuard) Option {
	return func(c *config) { c.tool = g }
}

// WithOutput attaches an output guard. Fired in OnFinish; if rejected, the
// final Result.Text is replaced with a sanitized message and a flag is set in
// AgentContext.State.
func WithOutput(g interfaces.OutputGuard) Option {
	return func(c *config) { c.output = g }
}

// Enable returns AgentOptions wiring the configured guards. Spread into
// kernel.NewAgent. Passing zero guards is valid and returns an empty slice.
func Enable(opts ...Option) []kernel.AgentOption {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	var agentOpts []kernel.AgentOption

	if cfg.input != nil {
		agentOpts = append(agentOpts, kernel.OnStart(func(tc *kernel.TurnContext) {
			result, err := cfg.input.Check(context.Background(), tc.Input)
			if err != nil {
				// Guard internal error: fail-closed by disabling tools, but do
				// not crash the agent. The model still produces a response.
				disableAllTools(tc.AgentCtx)
				return
			}
			if !result.Allowed {
				disableAllTools(tc.AgentCtx)
			}
		}))
	}

	if cfg.tool != nil {
		// Wrap registered tools at OnStart so guard runs before Execute.
		// Doing this in OnStart (rather than at construction time) lets the
		// wrapper see tools added by other capabilities (plan, etc.) that also
		// register their tools in OnStart, as long as guards.Enable is the LAST
		// option in the list. For tools registered earlier (e.g. via WithTools)
		// this still works because by OnStart time they're all in AgentCtx.
		guard := cfg.tool
		agentOpts = append(agentOpts, kernel.OnStart(func(tc *kernel.TurnContext) {
			all := tc.AgentCtx.AllTools()
			wrapped := make([]kernel.Tool, len(all))
			for i, t := range all {
				wrapped[i] = &guardedTool{inner: t, guard: guard}
			}
			tc.AgentCtx.ReplaceTools(wrapped)
		}))
	}

	if cfg.output != nil {
		guard := cfg.output
		agentOpts = append(agentOpts, kernel.OnFinish(func(tc *kernel.TurnContext) {
			if tc.Result == nil {
				return
			}
			res, err := guard.Check(context.Background(), tc.Result.Text)
			if err != nil {
				// Fail-closed: replace the text with a generic block notice.
				tc.Result.Text = fmt.Sprintf("[blocked: output guard error: %v]", err)
				if tc.AgentCtx.State == nil {
					tc.AgentCtx.State = make(map[string]any)
				}
				tc.AgentCtx.State[StateOutputBlockedKey] = true
				tc.AgentCtx.State[StateOutputBlockReasonKey] = err.Error()
				return
			}
			if !res.Allowed {
				tc.Result.Text = fmt.Sprintf("[blocked: %s]", res.Reason)
				if tc.AgentCtx.State == nil {
					tc.AgentCtx.State = make(map[string]any)
				}
				tc.AgentCtx.State[StateOutputBlockedKey] = true
				tc.AgentCtx.State[StateOutputBlockReasonKey] = res.Reason
			}
		}))
	}

	return agentOpts
}

// disableAllTools turns off every tool currently registered on the context.
// Used when the input guard rejects a turn.
func disableAllTools(ac *kernel.AgentContext) {
	names := make([]string, 0, len(ac.AllTools()))
	for _, t := range ac.AllTools() {
		names = append(names, t.Name())
	}
	if len(names) > 0 {
		ac.DisableTools(names...)
	}
}

// guardedTool wraps an inner Tool so that Execute first consults the ToolGuard.
// On rejection, Execute returns an error whose message is the guard Reason; the
// kernel converts this into a tool-error message back to the LLM, allowing the
// agent loop to continue.
type guardedTool struct {
	inner kernel.Tool
	guard interfaces.ToolGuard
}

func (g *guardedTool) Name() string          { return g.inner.Name() }
func (g *guardedTool) Description() string   { return g.inner.Description() }
func (g *guardedTool) Schema() kernel.Schema { return g.inner.Schema() }

func (g *guardedTool) Execute(ctx context.Context, params json.RawMessage) (any, error) {
	res, err := g.guard.Check(ctx, g.inner.Name(), params)
	if err != nil {
		return nil, fmt.Errorf("tool guard error: %w", err)
	}
	if !res.Allowed {
		return nil, fmt.Errorf("tool call blocked by guard: %s", res.Reason)
	}
	return g.inner.Execute(ctx, params)
}
