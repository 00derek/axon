// testing/run.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// TestResult wraps a kernel.Result with assertion helpers.
type TestResult struct {
	Result *kernel.Result
	t      *testing.T
}

// Text returns the final text response.
func (r *TestResult) Text() string {
	if r.Result == nil {
		return ""
	}
	return r.Result.Text
}

// RoundCount returns the number of rounds executed.
func (r *TestResult) RoundCount() int {
	if r.Result == nil {
		return 0
	}
	return len(r.Result.Rounds)
}

// RunOption configures a test Run() call.
type RunOption func(*runConfig)

type runConfig struct {
	history   []kernel.Message
	mockTools map[string]any // tool name -> canned response
	mockLLM   kernel.LLM
}

// WithHistory prepends conversation history before the user input.
func WithHistory(msgs ...kernel.Message) RunOption {
	return func(c *runConfig) {
		c.history = append(c.history, msgs...)
	}
}

// MockTool replaces a tool's execution with a canned response.
// The tool must already be registered on the agent. MockTool intercepts
// the tool call via an OnToolStart hook and replaces the tool implementation.
func MockTool(name string, response any) RunOption {
	return func(c *runConfig) {
		if c.mockTools == nil {
			c.mockTools = make(map[string]any)
		}
		c.mockTools[name] = response
	}
}

// WithMockLLM overrides the agent's LLM with the provided one.
func WithMockLLM(llm kernel.LLM) RunOption {
	return func(c *runConfig) {
		c.mockLLM = llm
	}
}

// Run executes an agent with the given input and options, returning a TestResult
// for assertion chaining. Calls t.Fatal on agent execution errors.
func Run(t *testing.T, agent *kernel.Agent, input string, opts ...RunOption) *TestResult {
	t.Helper()

	cfg := &runConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build agent options to create a test-configured clone.
	// We reconstruct the agent with overrides applied.
	var agentOpts []kernel.AgentOption

	// If a mock LLM is provided, use it. Otherwise re-use the agent's model.
	if cfg.mockLLM != nil {
		agentOpts = append(agentOpts, kernel.WithModel(cfg.mockLLM))
	}

	// Inject history via OnStart hook
	if len(cfg.history) > 0 {
		agentOpts = append(agentOpts, kernel.OnStart(func(ctx *kernel.TurnContext) {
			// Prepend history before the user message that Run already added.
			// AgentContext.Messages at this point: [system_prompt?, user_msg]
			// We want: [system_prompt?, ...history, user_msg]
			existing := ctx.AgentCtx.Messages
			var systemMsgs []kernel.Message
			var rest []kernel.Message
			for _, m := range existing {
				if m.Role == kernel.RoleSystem {
					systemMsgs = append(systemMsgs, m)
				} else {
					rest = append(rest, m)
				}
			}
			var reordered []kernel.Message
			reordered = append(reordered, systemMsgs...)
			reordered = append(reordered, cfg.history...)
			reordered = append(reordered, rest...)
			ctx.AgentCtx.Messages = reordered
		}))
	}

	// Inject mock tools by replacing the registered tool with a mock wrapper
	if len(cfg.mockTools) > 0 {
		agentOpts = append(agentOpts, kernel.OnStart(func(ctx *kernel.TurnContext) {
			allTools := ctx.AgentCtx.AllTools()
			var replaced []kernel.Tool
			for _, tool := range allTools {
				if mockResp, ok := cfg.mockTools[tool.Name()]; ok {
					replaced = append(replaced, &mockedTool{
						name:     tool.Name(),
						desc:     tool.Description(),
						schema:   tool.Schema(),
						response: mockResp,
					})
				} else {
					replaced = append(replaced, tool)
				}
			}
			// Replace tools on the context by clearing and re-adding
			ctx.AgentCtx.ReplaceTools(replaced)
		}))
	}

	// Build the test agent using CloneWith which copies base config + appends options.
	testAgent := agent.CloneWith(agentOpts...)

	result, err := testAgent.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("axontest.Run failed: %v", err)
	}

	return &TestResult{
		Result: result,
		t:      t,
	}
}

// mockedTool wraps a tool to always return a fixed response.
type mockedTool struct {
	name     string
	desc     string
	schema   kernel.Schema
	response any
}

func (m *mockedTool) Name() string          { return m.name }
func (m *mockedTool) Description() string   { return m.desc }
func (m *mockedTool) Schema() kernel.Schema { return m.schema }
func (m *mockedTool) Execute(ctx context.Context, params json.RawMessage) (any, error) {
	return m.response, nil
}
