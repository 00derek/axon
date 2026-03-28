// kernel/agent_stream.go
package kernel

import (
	"context"
	"fmt"
	"sync"
)

// StreamResult provides both streaming access and final result.
type StreamResult struct {
	textCh  chan string
	eventCh chan StreamEvent
	result  *Result
	err     error
	done    chan struct{}
	mu      sync.Mutex
}

// Text returns a channel of text deltas from the final response only.
func (sr *StreamResult) Text() <-chan string {
	return sr.textCh
}

// Events returns a channel of all events (tool starts, tool ends, text deltas).
func (sr *StreamResult) Events() <-chan StreamEvent {
	return sr.eventCh
}

// Result returns the final result after the stream completes. Blocks until done.
func (sr *StreamResult) Result() *Result {
	<-sr.done
	return sr.result
}

// Err returns any error that occurred during streaming. Blocks until done.
func (sr *StreamResult) Err() error {
	<-sr.done
	return sr.err
}

// Stream executes the agent loop in a goroutine, emitting events and text as they happen.
func (a *Agent) Stream(ctx context.Context, input string) (*StreamResult, error) {
	sr := &StreamResult{
		textCh:  make(chan string, 64),
		eventCh: make(chan StreamEvent, 64),
		done:    make(chan struct{}),
	}

	go func() {
		defer close(sr.textCh)
		defer close(sr.eventCh)
		defer close(sr.done)

		result, err := a.runWithEvents(ctx, input, sr)
		sr.mu.Lock()
		sr.result = result
		sr.err = err
		sr.mu.Unlock()
	}()

	return sr, nil
}

// runWithEvents is the same as Run but emits events to a StreamResult.
func (a *Agent) runWithEvents(ctx context.Context, input string, sr *StreamResult) (*Result, error) {
	agentCtx := NewAgentContext(a.tools)
	if a.systemPrompt != "" {
		agentCtx.SetSystemPrompt(a.systemPrompt)
	}
	agentCtx.AddMessages(UserMsg(input))

	turnCtx := &TurnContext{AgentCtx: agentCtx, Input: input}

	for _, fn := range a.hooks.onStart {
		fn(turnCtx)
	}

	var rounds []RoundResult
	var totalUsage Usage
	var lastResponse *Response
	var finalText string

	for round := 0; round < a.maxRounds; round++ {
		roundCtx := &RoundContext{
			AgentCtx:     agentCtx,
			RoundNumber:  round,
			LastResponse: lastResponse,
		}

		for _, fn := range a.hooks.prepareRound {
			fn(roundCtx)
		}

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

		params := GenerateParams{
			Messages: agentCtx.Messages,
			Tools:    agentCtx.ActiveTools(),
		}

		resp, err := a.model.Generate(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("llm generate round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)
		lastResponse = &resp

		roundResult := RoundResult{Response: resp}

		if len(resp.ToolCalls) > 0 {
			toolResults, err := a.executeToolCallsWithEvents(ctx, agentCtx, resp.ToolCalls, sr)
			if err != nil {
				return nil, err
			}
			roundResult.ToolCalls = toolResults

			for i, tc := range resp.ToolCalls {
				agentCtx.AddMessages(Message{
					Role:    RoleAssistant,
					Content: []ContentPart{{ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Params: tc.Params}}},
				})

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
			// Emit text as a single delta (since we're using Generate, not GenerateStream)
			sr.textCh <- resp.Text
			sr.eventCh <- TextDeltaEvent{Text: resp.Text}
		}

		rounds = append(rounds, roundResult)

		roundCtx.LastResponse = &resp
		for _, fn := range a.hooks.onRoundFinish {
			fn(roundCtx)
		}

		if len(resp.ToolCalls) == 0 {
			break
		}
	}

	result := &Result{
		Text:   finalText,
		Rounds: rounds,
		Usage:  totalUsage,
	}

	turnCtx.Result = result
	for _, fn := range a.hooks.onFinish {
		fn(turnCtx)
	}

	return result, nil
}

// executeToolCallsWithEvents runs tools and emits events to the stream.
func (a *Agent) executeToolCallsWithEvents(ctx context.Context, agentCtx *AgentContext, calls []ToolCall, sr *StreamResult) ([]ToolCallResult, error) {
	results := make([]ToolCallResult, len(calls))

	if len(calls) == 1 {
		result, err := a.executeSingleToolWithEvents(ctx, agentCtx, calls[0], sr)
		if err != nil {
			return nil, err
		}
		results[0] = result
		return results, nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, tc ToolCall) {
			defer wg.Done()
			result, err := a.executeSingleToolWithEvents(ctx, agentCtx, tc, sr)
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

func (a *Agent) executeSingleToolWithEvents(ctx context.Context, agentCtx *AgentContext, call ToolCall, sr *StreamResult) (ToolCallResult, error) {
	tool, ok := agentCtx.GetTool(call.Name)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("tool %q not found", call.Name)
	}

	toolCtx := &ToolContext{ToolName: call.Name, Params: call.Params}

	// Emit event + fire hooks
	sr.eventCh <- ToolStartEvent{ToolName: call.Name, Params: call.Params}
	for _, fn := range a.hooks.onToolStart {
		fn(toolCtx)
	}

	result, err := tool.Execute(ctx, call.Params)
	toolCtx.Result = result
	toolCtx.Error = err

	// Emit event + fire hooks
	sr.eventCh <- ToolEndEvent{ToolName: call.Name, Result: result, Error: err}
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
