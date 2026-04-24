// kernel/agent.go
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

const defaultMaxRounds = 20

// Agent is the core agentic loop: generates LLM responses, executes tool calls,
// and repeats until a text response or stop condition.
type Agent struct {
	model        LLM
	tools        []Tool
	systemPrompt string
	hooks        agentHooks
	stopConds    []StopCondition
	maxRounds    int
}

// StopCondition is evaluated before each round. If any returns true, the loop stops.
type StopCondition func(ctx *RoundContext) bool

// TurnContext is available to OnStart and OnFinish hooks.
type TurnContext struct {
	AgentCtx *AgentContext
	Input    string
	Result   *Result
}

// RoundContext is available to PrepareRound, OnRoundFinish, and StopWhen.
type RoundContext struct {
	AgentCtx     *AgentContext
	RoundNumber  int
	LastResponse *Response
}

// ToolContext is available to OnToolStart and OnToolEnd hooks.
type ToolContext struct {
	ToolName string
	Params   json.RawMessage
	Result   any
	Error    error
}

// Result is the output of an Agent.Run() call.
type Result struct {
	Text   string
	Rounds []RoundResult
	Usage  Usage
}

// RoundResult captures what happened in a single round.
type RoundResult struct {
	Response  Response
	ToolCalls []ToolCallResult
}

// ToolCallResult captures a single tool execution.
type ToolCallResult struct {
	Name   string
	Params json.RawMessage
	Result any
	Error  error
}

// agentHooks holds all registered hook functions.
type agentHooks struct {
	onStart       []func(*TurnContext)
	onFinish      []func(*TurnContext)
	prepareRound  []func(*RoundContext)
	onRoundFinish []func(*RoundContext)
	onToolStart   []func(*ToolContext)
	onToolEnd     []func(*ToolContext)
}

// AgentOption configures an Agent.
type AgentOption func(*Agent)

func WithModel(llm LLM) AgentOption              { return func(a *Agent) { a.model = llm } }
func WithTools(tools ...Tool) AgentOption        { return func(a *Agent) { a.tools = tools } }
func WithSystemPrompt(prompt string) AgentOption { return func(a *Agent) { a.systemPrompt = prompt } }
func WithMaxRounds(n int) AgentOption            { return func(a *Agent) { a.maxRounds = n } }

func OnStart(fn func(*TurnContext)) AgentOption {
	return func(a *Agent) { a.hooks.onStart = append(a.hooks.onStart, fn) }
}

func OnFinish(fn func(*TurnContext)) AgentOption {
	return func(a *Agent) { a.hooks.onFinish = append(a.hooks.onFinish, fn) }
}

func PrepareRound(fn func(*RoundContext)) AgentOption {
	return func(a *Agent) { a.hooks.prepareRound = append(a.hooks.prepareRound, fn) }
}

func OnRoundFinish(fn func(*RoundContext)) AgentOption {
	return func(a *Agent) { a.hooks.onRoundFinish = append(a.hooks.onRoundFinish, fn) }
}

func OnToolStart(fn func(*ToolContext)) AgentOption {
	return func(a *Agent) { a.hooks.onToolStart = append(a.hooks.onToolStart, fn) }
}

func OnToolEnd(fn func(*ToolContext)) AgentOption {
	return func(a *Agent) { a.hooks.onToolEnd = append(a.hooks.onToolEnd, fn) }
}

func StopWhen(fn StopCondition) AgentOption {
	return func(a *Agent) { a.stopConds = append(a.stopConds, fn) }
}

// CloneWith creates a copy of the agent with additional options applied.
func (a *Agent) CloneWith(opts ...AgentOption) *Agent {
	clone := *a
	clone.tools = append([]Tool(nil), a.tools...)
	clone.hooks.onStart = append(a.hooks.onStart[0:0:0], a.hooks.onStart...)
	clone.hooks.onFinish = append(a.hooks.onFinish[0:0:0], a.hooks.onFinish...)
	clone.hooks.prepareRound = append(a.hooks.prepareRound[0:0:0], a.hooks.prepareRound...)
	clone.hooks.onRoundFinish = append(a.hooks.onRoundFinish[0:0:0], a.hooks.onRoundFinish...)
	clone.hooks.onToolStart = append(a.hooks.onToolStart[0:0:0], a.hooks.onToolStart...)
	clone.hooks.onToolEnd = append(a.hooks.onToolEnd[0:0:0], a.hooks.onToolEnd...)
	for _, opt := range opts {
		opt(&clone)
	}
	return &clone
}

// NewAgent creates a new Agent with the given options.
func NewAgent(opts ...AgentOption) *Agent {
	a := &Agent{maxRounds: defaultMaxRounds}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Run executes the agent loop synchronously and returns the result.
func (a *Agent) Run(ctx context.Context, input string) (*Result, error) {
	// Build AgentContext
	agentCtx := NewAgentContext(a.tools)
	if a.systemPrompt != "" {
		agentCtx.SetSystemPrompt(a.systemPrompt)
	}
	agentCtx.AddMessages(UserMsg(input))

	turnCtx := &TurnContext{AgentCtx: agentCtx, Input: input}

	// Fire OnStart hooks
	for _, fn := range a.hooks.onStart {
		fn(turnCtx)
	}

	var rounds []RoundResult
	var totalUsage Usage
	var lastResponse *Response
	var finalText string

	// Agent loop
	for round := 0; round < a.maxRounds; round++ {
		roundCtx := &RoundContext{
			AgentCtx:     agentCtx,
			RoundNumber:  round,
			LastResponse: lastResponse,
		}

		// Fire PrepareRound hooks
		for _, fn := range a.hooks.prepareRound {
			fn(roundCtx)
		}

		// Check stop conditions
		shouldStop := false
		for _, cond := range a.stopConds {
			if cond(roundCtx) {
				shouldStop = true
				break
			}
		}
		if shouldStop {
			break
		}

		// Build generate params
		params := GenerateParams{
			Messages: agentCtx.Messages,
			Tools:    agentCtx.ActiveTools(),
		}

		// Generate
		resp, err := a.model.Generate(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("llm generate round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)
		lastResponse = &resp

		roundResult := RoundResult{Response: resp}

		if len(resp.ToolCalls) > 0 {
			// Execute tool calls
			toolResults, err := a.executeToolCalls(ctx, agentCtx, resp.ToolCalls)
			if err != nil {
				return nil, err
			}
			roundResult.ToolCalls = toolResults

			// Add tool call and result messages to context
			for i, tc := range resp.ToolCalls {
				// Add the assistant's tool call message
				agentCtx.AddMessages(Message{
					Role:    RoleAssistant,
					Content: []ContentPart{{ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Params: tc.Params}}},
				})

				// Add the tool result message
				content := SerializeToolResult(toolResults[i].Result)
				isError := toolResults[i].Error != nil
				if isError {
					content = toolResults[i].Error.Error()
				}
				agentCtx.AddMessages(Message{
					Role: RoleTool,
					Content: []ContentPart{{ToolResult: &ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    content,
						IsError:    isError,
					}}},
				})
			}
		} else {
			finalText = resp.Text
		}

		rounds = append(rounds, roundResult)

		// Fire OnRoundFinish hooks
		roundCtx.LastResponse = &resp
		for _, fn := range a.hooks.onRoundFinish {
			fn(roundCtx)
		}

		// If no tool calls, we have the final text response — exit loop
		if len(resp.ToolCalls) == 0 {
			break
		}
	}

	result := &Result{
		Text:   finalText,
		Rounds: rounds,
		Usage:  totalUsage,
	}

	// Fire OnFinish hooks
	turnCtx.Result = result
	for _, fn := range a.hooks.onFinish {
		fn(turnCtx)
	}

	return result, nil
}

// executeToolCalls runs tool calls in parallel and returns results.
func (a *Agent) executeToolCalls(ctx context.Context, agentCtx *AgentContext, calls []ToolCall) ([]ToolCallResult, error) {
	results := make([]ToolCallResult, len(calls))

	if len(calls) == 1 {
		// Single tool call — no goroutine overhead
		result, err := a.executeSingleTool(ctx, agentCtx, calls[0])
		if err != nil {
			return nil, err
		}
		results[0] = result
		return results, nil
	}

	// Multiple tool calls — execute in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, tc ToolCall) {
			defer wg.Done()
			result, err := a.executeSingleTool(ctx, agentCtx, tc)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			results[idx] = result
		}(i, call)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// executeSingleTool finds and executes a single tool, firing hooks.
func (a *Agent) executeSingleTool(ctx context.Context, agentCtx *AgentContext, call ToolCall) (ToolCallResult, error) {
	tool, ok := agentCtx.GetTool(call.Name)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("tool %q not found", call.Name)
	}

	toolCtx := &ToolContext{
		ToolName: call.Name,
		Params:   call.Params,
	}

	// Fire OnToolStart hooks
	for _, fn := range a.hooks.onToolStart {
		fn(toolCtx)
	}

	// Execute
	result, err := tool.Execute(ctx, call.Params)

	toolCtx.Result = result
	toolCtx.Error = err

	// Fire OnToolEnd hooks
	for _, fn := range a.hooks.onToolEnd {
		fn(toolCtx)
	}

	return ToolCallResult{
		Name:   call.Name,
		Params: call.Params,
		Result: result,
		Error:  err,
	}, nil
}
