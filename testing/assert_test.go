// testing/assert_test.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// --- helpers for building TestResults in assertion tests ---

func makeTestResult(t *testing.T, rounds []kernel.RoundResult, text string) *TestResult {
	return &TestResult{
		Result: &kernel.Result{
			Text:   text,
			Rounds: rounds,
		},
		t: t,
	}
}

func makeToolCallResult(name string, params map[string]any) kernel.ToolCallResult {
	data, _ := json.Marshal(params)
	return kernel.ToolCallResult{
		Name:   name,
		Params: data,
		Result: "ok",
	}
}

// --- ExpectTool tests ---

func TestExpectToolCalled(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
	}, "done")

	result.ExpectTool("search").Called(t)
}

func TestExpectToolNotCalled(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
	}, "done")

	result.ExpectTool("reserve").NotCalled(t)
}

func TestExpectToolCalledTimes(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "sushi"}),
		}},
	}, "done")

	result.ExpectTool("search").CalledTimes(t, 2)
}

func TestExpectToolWithParam(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai", "location": "SF"}),
		}},
	}, "done")

	result.ExpectTool("search").
		WithParam("query", "thai").
		WithParam("location", "SF").
		Called(t)
}

func TestExpectToolWithParamNoMatch(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
	}, "done")

	// WithParam filters calls -- "sushi" should not match, so Called should fail.
	// We use a sub-test to capture the failure without failing the parent.
	subT := &testing.T{}
	result.ExpectTool("search").
		WithParam("query", "sushi").
		Called(subT)
	// subT would have failed, but we can't inspect it easily.
	// Instead, test the count directly: should be 0 matching calls.
	assertion := result.ExpectTool("search").WithParam("query", "sushi")
	if len(assertion.matchingCalls()) != 0 {
		t.Error("expected 0 matching calls for query=sushi")
	}
}

func TestExpectToolWithParamMatchLLMJudge(t *testing.T) {
	// WithParamMatch uses a judge LLM to evaluate criteria.
	// We use a MockLLM that always says "yes" as the judge.
	judgeLLM := NewMockLLM().
		OnRound(0).RespondWithText(`{"reasoning":"The query is about food","condition_met":true}`)

	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "best thai restaurants near me"}),
		}},
	}, "done")

	result.ExpectTool("search").
		WithParamMatch("query", judgeLLM, "The query is about food").
		Called(t)
}

func TestExpectToolCalledMultipleRounds(t *testing.T) {
	// Tool called in round 0 and round 2, not in round 1
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "a"}),
		}},
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("reserve", map[string]any{"id": "r1"}),
		}},
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "b"}),
		}},
	}, "done")

	result.ExpectTool("search").CalledTimes(t, 2)
	result.ExpectTool("reserve").CalledTimes(t, 1)
}

// --- ExpectResponse tests ---

func TestExpectResponseContains(t *testing.T) {
	result := makeTestResult(t, nil, "I found Thai Basil restaurant for you!")
	result.ExpectResponse().Contains(t, "Thai Basil")
}

func TestExpectResponseNotContains(t *testing.T) {
	result := makeTestResult(t, nil, "I found Thai Basil restaurant for you!")
	result.ExpectResponse().NotContains(t, "Sushi")
}

func TestExpectResponseSatisfiesLLMJudge(t *testing.T) {
	judgeLLM := NewMockLLM().
		OnRound(0).RespondWithText(`{"reasoning":"The response mentions a restaurant","condition_met":true}`)

	result := makeTestResult(t, nil, "I found Thai Basil restaurant for you!")
	result.ExpectResponse().Satisfies(t, judgeLLM, "The response mentions a specific restaurant name")
}

// --- ExpectRounds tests ---

func TestExpectRounds(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{}, {}, {},
	}, "done")

	result.ExpectRounds(t, 3)
}

// suppress unused import warning - context is used in assert.go
var _ = context.Background
