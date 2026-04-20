// testing/run_test.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// fakeTool is a minimal kernel.Tool for testing Run().
type fakeTool struct {
	name   string
	called bool
	result any
}

func (f *fakeTool) Name() string          { return f.name }
func (f *fakeTool) Description() string   { return "fake " + f.name }
func (f *fakeTool) Schema() kernel.Schema { return kernel.Schema{Type: "object"} }
func (f *fakeTool) Execute(ctx context.Context, params json.RawMessage) (any, error) {
	f.called = true
	return f.result, nil
}

func TestRunTextOnly(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello!")

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithSystemPrompt("You are helpful"),
	)

	result := Run(t, agent, "Hi")

	if result.Text() != "Hello!" {
		t.Errorf("expected %q, got %q", "Hello!", result.Text())
	}
	if result.RoundCount() != 1 {
		t.Errorf("expected 1 round, got %d", result.RoundCount())
	}
}

func TestRunWithToolCall(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "thai"}).
		OnRound(1).RespondWithText("Found Thai Basil!")

	searchTool := &fakeTool{name: "search", result: map[string]any{"name": "Thai Basil"}}

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithTools(searchTool),
	)

	result := Run(t, agent, "Find thai food")

	if result.Text() != "Found Thai Basil!" {
		t.Errorf("expected %q, got %q", "Found Thai Basil!", result.Text())
	}
	if result.RoundCount() != 2 {
		t.Errorf("expected 2 rounds, got %d", result.RoundCount())
	}
}

func TestRunWithMockTool(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "sushi"}).
		OnRound(1).RespondWithText("Found Sushi Place!")

	// Use a real tool that the agent registers, but MockTool overrides its execution
	realTool := &fakeTool{name: "search", result: "should not see this"}

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithTools(realTool),
	)

	result := Run(t, agent, "Find sushi",
		MockTool("search", map[string]any{"name": "Sushi Place"}),
	)

	if result.Text() != "Found Sushi Place!" {
		t.Errorf("expected %q, got %q", "Found Sushi Place!", result.Text())
	}
}

func TestRunWithHistory(t *testing.T) {
	// Verify that history messages are passed to the agent.
	// The mock LLM captures the GenerateParams, so we can check messages.
	var capturedParams kernel.GenerateParams

	captureLLM := &capturingMockLLM{
		response: kernel.Response{Text: "Got it", FinishReason: "stop"},
	}

	agent := kernel.NewAgent(
		kernel.WithModel(captureLLM),
	)

	history := []kernel.Message{
		kernel.UserMsg("previous question"),
		kernel.AssistantMsg("previous answer"),
	}

	Run(t, agent, "Follow up",
		WithHistory(history...),
	)

	capturedParams = captureLLM.lastParams
	// Should have: system prompt (if any) + history messages + user input
	// At minimum: history (2) + current user msg (1) = 3 messages
	foundPrevious := false
	for _, msg := range capturedParams.Messages {
		if msg.TextContent() == "previous question" {
			foundPrevious = true
		}
	}
	if !foundPrevious {
		t.Error("expected history messages to be included in LLM call")
	}
}

func TestRunWithMockLLMOption(t *testing.T) {
	// WithMockLLM overrides the agent's model entirely.
	originalLLM := NewMockLLM().
		OnRound(0).RespondWithText("from original")

	overrideLLM := NewMockLLM().
		OnRound(0).RespondWithText("from override")

	agent := kernel.NewAgent(
		kernel.WithModel(originalLLM),
	)

	result := Run(t, agent, "test",
		WithMockLLM(overrideLLM),
	)

	if result.Text() != "from override" {
		t.Errorf("expected %q, got %q", "from override", result.Text())
	}
}

func TestRunResultHasKernelResult(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello!")

	agent := kernel.NewAgent(kernel.WithModel(mock))
	result := Run(t, agent, "test")

	// TestResult should expose the underlying kernel.Result
	if result.Result == nil {
		t.Fatal("expected non-nil underlying kernel.Result")
	}
	if result.Result.Text != "Hello!" {
		t.Errorf("expected underlying text %q, got %q", "Hello!", result.Result.Text)
	}
}

// capturingMockLLM records the last GenerateParams it received.
type capturingMockLLM struct {
	response   kernel.Response
	lastParams kernel.GenerateParams
}

func (c *capturingMockLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	c.lastParams = params
	return c.response, nil
}

func (c *capturingMockLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (c *capturingMockLLM) Model() string { return "capturing-mock" }
