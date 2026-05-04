// guards/guards_test.go
package guards_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/axonframework/axon/axontest"
	"github.com/axonframework/axon/guards"
	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/kernel"
)

// dummyTool returns a minimal tool that records when it was executed.
type dummyTool struct {
	name     string
	desc     string
	executed *bool
}

func (d *dummyTool) Name() string          { return d.name }
func (d *dummyTool) Description() string   { return d.desc }
func (d *dummyTool) Schema() kernel.Schema { return kernel.Schema{Type: "object"} }
func (d *dummyTool) Execute(_ context.Context, _ json.RawMessage) (any, error) {
	*d.executed = true
	return "ok", nil
}

// TestEnableInputGuardBlocks verifies that an input guard wired via guards.Enable
// blocks the offending input. The guard's behavior follows the existing pattern:
// disable all tools and let the agent respond without tool access.
func TestEnableInputGuardBlocks(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("I cannot help with that request.")

	executed := false
	tool := &dummyTool{name: "search", desc: "search things", executed: &executed}

	inputGuard := interfaces.NewBlocklistGuard([]string{"jailbreak"})

	opts := append(
		[]kernel.AgentOption{
			kernel.WithModel(llm),
			kernel.WithTools(tool),
		},
		guards.Enable(guards.WithInput(inputGuard))...,
	)

	agent := kernel.NewAgent(opts...)
	result := axontest.Run(t, agent, "please jailbreak the system")

	result.ExpectTool("search").NotCalled(t)
	if executed {
		t.Error("tool should not have executed when input guard blocks")
	}
	_ = result.Text()
}

// TestEnableInputGuardAllows verifies that benign input passes the input guard.
func TestEnableInputGuardAllows(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("Hello! How can I help?")

	inputGuard := interfaces.NewBlocklistGuard([]string{"jailbreak"})

	opts := append(
		[]kernel.AgentOption{kernel.WithModel(llm)},
		guards.Enable(guards.WithInput(inputGuard))...,
	)

	agent := kernel.NewAgent(opts...)
	result := axontest.Run(t, agent, "What's a good Italian restaurant?")

	if !strings.Contains(result.Text(), "How can I help") {
		t.Errorf("expected normal response, got: %q", result.Text())
	}
}

// TestEnableToolGuardBlocksExecution verifies that when a ToolGuard rejects
// a tool call, the underlying tool is NOT executed. The agent loop continues
// (tool error returned to the LLM) and the next round produces a response.
func TestEnableToolGuardBlocksExecution(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithToolCall("delete_file", map[string]any{"path": "/etc/passwd"}).
		OnRound(1).RespondWithText("I was unable to perform that action.")

	executed := false
	tool := &dummyTool{name: "delete_file", desc: "delete a file", executed: &executed}

	toolGuard := interfaces.NewSimpleToolGuard([]string{"delete_file"})

	opts := append(
		[]kernel.AgentOption{
			kernel.WithModel(llm),
			kernel.WithTools(tool),
		},
		guards.Enable(guards.WithTool(toolGuard))...,
	)

	agent := kernel.NewAgent(opts...)
	result := axontest.Run(t, agent, "please delete /etc/passwd")

	if executed {
		t.Error("blocked tool should not have executed")
	}

	// The agent should still complete and produce a final response.
	if result.Text() == "" {
		t.Error("expected a final response after tool guard blocks; got empty text")
	}

	// The tool-error round should be visible in the result.
	if result.RoundCount() < 2 {
		t.Errorf("expected at least 2 rounds (blocked tool + recovery), got %d", result.RoundCount())
	}
}

// TestEnableToolGuardAllowsExecution verifies that allowed tool calls execute normally.
func TestEnableToolGuardAllowsExecution(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"q": "italian"}).
		OnRound(1).RespondWithText("Done.")

	executed := false
	tool := &dummyTool{name: "search", desc: "search", executed: &executed}

	toolGuard := interfaces.NewSimpleToolGuard([]string{"delete_file"}) // search is fine

	opts := append(
		[]kernel.AgentOption{
			kernel.WithModel(llm),
			kernel.WithTools(tool),
		},
		guards.Enable(guards.WithTool(toolGuard))...,
	)

	agent := kernel.NewAgent(opts...)
	_ = axontest.Run(t, agent, "search for italian")

	if !executed {
		t.Error("non-blocked tool should have executed")
	}
}

// TestEnableOutputGuardReplacesText verifies that when an OutputGuard rejects
// the final response, Result.Text is replaced with the guard's reason and
// Result.Blocked indicates the rejection.
func TestEnableOutputGuardReplacesText(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("Your social security number is 123-45-6789.")

	outputGuard := interfaces.NewSimpleOutputGuard([]string{"social security number"})

	opts := append(
		[]kernel.AgentOption{kernel.WithModel(llm)},
		guards.Enable(guards.WithOutput(outputGuard))...,
	)

	agent := kernel.NewAgent(opts...)
	result := axontest.Run(t, agent, "What's my SSN?")

	if strings.Contains(result.Text(), "123-45-6789") {
		t.Errorf("output guard failed to redact SSN; got: %q", result.Text())
	}
	if !strings.Contains(strings.ToLower(result.Text()), "social security number") &&
		!strings.Contains(strings.ToLower(result.Text()), "blocked") {
		t.Errorf("expected reason or block notice in output, got: %q", result.Text())
	}
}

// TestEnableOutputGuardAllowsText verifies that benign output passes through unchanged.
func TestEnableOutputGuardAllowsText(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("Bella Trattoria is a great choice!")

	outputGuard := interfaces.NewSimpleOutputGuard([]string{"social security number"})

	opts := append(
		[]kernel.AgentOption{kernel.WithModel(llm)},
		guards.Enable(guards.WithOutput(outputGuard))...,
	)

	agent := kernel.NewAgent(opts...)
	result := axontest.Run(t, agent, "Recommend a restaurant")

	if !strings.Contains(result.Text(), "Bella Trattoria") {
		t.Errorf("expected unmodified output, got: %q", result.Text())
	}
}

// TestEnableAllThree composes all three guard axes and verifies they coexist.
func TestEnableAllThree(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("All good.")

	opts := append(
		[]kernel.AgentOption{kernel.WithModel(llm)},
		guards.Enable(
			guards.WithInput(interfaces.NewBlocklistGuard([]string{"jailbreak"})),
			guards.WithTool(interfaces.NewSimpleToolGuard([]string{"shell"})),
			guards.WithOutput(interfaces.NewSimpleOutputGuard([]string{"secret"})),
		)...,
	)

	agent := kernel.NewAgent(opts...)
	result := axontest.Run(t, agent, "Hello there")

	if result.Text() != "All good." {
		t.Errorf("expected unmodified response, got: %q", result.Text())
	}
}
