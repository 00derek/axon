// testing/eval_test.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestEvalSingleCaseTextCheck(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello there!")

	agent := kernel.NewAgent(kernel.WithModel(mock))

	Eval(t, agent, nil, []Case{
		{
			Name:  "greeting",
			Input: "Hi",
			Expect: &Expectation{
				ResponseContains:    []string{"Hello"},
				ResponseNotContains: []string{"Error"},
			},
		},
	})
}

func TestEvalSingleCaseRoundCount(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Done")

	agent := kernel.NewAgent(kernel.WithModel(mock))

	rounds := 1
	Eval(t, agent, nil, []Case{
		{
			Name:  "simple response",
			Input: "Test",
			Expect: &Expectation{
				Rounds: &rounds,
			},
		},
	})
}

func TestEvalToolExpectations(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "thai"}).
		OnRound(1).RespondWithText("Found it!")

	searchTool := &fakeTool{name: "search", result: "ok"}

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithTools(searchTool),
	)

	Eval(t, agent, nil, []Case{
		{
			Name:  "tool usage",
			Input: "Find thai food",
			Expect: &Expectation{
				ToolCalled:    []string{"search"},
				ToolNotCalled: []string{"reserve"},
			},
		},
	})
}

func TestEvalWithScoreCard(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello! Thai Basil is a great restaurant.")

	// Judge for ScoreCard
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"Greeting found","condition_met":true},{"reasoning":"Restaurant named","condition_met":true}]`)

	agent := kernel.NewAgent(kernel.WithModel(mock))

	Eval(t, agent, judge, []Case{
		{
			Name:  "quality check",
			Input: "Find food",
			Expect: &Expectation{
				ScoreCard: &ScoreCard{
					Criteria: []Criterion{
						{Condition: "Greets user", Score: 1},
						{Condition: "Names restaurant", Score: 2},
					},
					PassingScore: 3,
				},
			},
		},
	})
}

func TestEvalWithHistory(t *testing.T) {
	captureLLM := &capturingMockLLM{
		response: kernel.Response{Text: "Follow up answer", FinishReason: "stop"},
	}

	agent := kernel.NewAgent(kernel.WithModel(captureLLM))

	Eval(t, agent, nil, []Case{
		{
			Name:  "with history",
			Input: "Follow up question",
			History: []kernel.Message{
				kernel.UserMsg("First question"),
				kernel.AssistantMsg("First answer"),
			},
			Expect: &Expectation{
				ResponseContains: []string{"Follow up"},
			},
		},
	})
}

func TestEvalMultipleCases(t *testing.T) {
	// Multiple cases each get their own fresh MockLLM via WithMockLLM
	// Since each case uses the agent's model, we need separate mocks per case.
	// Eval resets the mock between cases by using Run() which calls agent.Run().
	// For this test, we use a simple approach: the agent's LLM just echoes.
	echoLLM := &echoMockLLM{}
	agent := kernel.NewAgent(kernel.WithModel(echoLLM))

	Eval(t, agent, nil, []Case{
		{
			Name:  "case 1",
			Input: "Alpha",
			Expect: &Expectation{
				ResponseContains: []string{"Alpha"},
			},
		},
		{
			Name:  "case 2",
			Input: "Beta",
			Expect: &Expectation{
				ResponseContains: []string{"Beta"},
			},
		},
	})
}

func TestEvalNilExpectation(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Anything")

	agent := kernel.NewAgent(kernel.WithModel(mock))

	// Should not panic with nil Expect
	Eval(t, agent, nil, []Case{
		{
			Name:  "no expectations",
			Input: "test",
		},
	})
}

// echoMockLLM returns the last user message as the response text.
type echoMockLLM struct{}

func (e *echoMockLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	text := ""
	for i := len(params.Messages) - 1; i >= 0; i-- {
		if params.Messages[i].Role == kernel.RoleUser {
			text = params.Messages[i].TextContent()
			break
		}
	}
	return kernel.Response{Text: text, FinishReason: "stop"}, nil
}

func (e *echoMockLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (e *echoMockLLM) Model() string { return "echo" }

// Suppress unused imports
var _ = json.Marshal
