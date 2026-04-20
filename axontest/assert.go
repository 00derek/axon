// testing/assert.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// --- Tool Assertions ---

// ToolAssertion provides chainable assertions about tool calls within a TestResult.
type ToolAssertion struct {
	result   *TestResult
	toolName string
	filters  []toolFilter
}

type toolFilter func(kernel.ToolCallResult) bool

// ExpectTool begins a tool assertion chain for the named tool.
func (r *TestResult) ExpectTool(name string) *ToolAssertion {
	return &ToolAssertion{
		result:   r,
		toolName: name,
	}
}

// matchingCalls returns all tool calls that match the tool name and all filters.
func (a *ToolAssertion) matchingCalls() []kernel.ToolCallResult {
	var matches []kernel.ToolCallResult
	if a.result.Result == nil {
		return matches
	}
	for _, round := range a.result.Result.Rounds {
		for _, tc := range round.ToolCalls {
			if tc.Name != a.toolName {
				continue
			}
			allMatch := true
			for _, f := range a.filters {
				if !f(tc) {
					allMatch = false
					break
				}
			}
			if allMatch {
				matches = append(matches, tc)
			}
		}
	}
	return matches
}

// Called asserts the tool was called at least once (matching all filters).
func (a *ToolAssertion) Called(t *testing.T) {
	t.Helper()
	if len(a.matchingCalls()) == 0 {
		t.Errorf("expected tool %q to be called, but it was not", a.toolName)
	}
}

// NotCalled asserts the tool was never called (matching all filters).
func (a *ToolAssertion) NotCalled(t *testing.T) {
	t.Helper()
	calls := a.matchingCalls()
	if len(calls) > 0 {
		t.Errorf("expected tool %q to not be called, but it was called %d time(s)", a.toolName, len(calls))
	}
}

// CalledTimes asserts the tool was called exactly n times (matching all filters).
func (a *ToolAssertion) CalledTimes(t *testing.T, n int) {
	t.Helper()
	calls := a.matchingCalls()
	if len(calls) != n {
		t.Errorf("expected tool %q to be called %d time(s), got %d", a.toolName, n, len(calls))
	}
}

// WithParam adds a filter: only match tool calls where the parameter key equals the value.
// Value is compared via JSON serialization for type-safe equality.
// Returns the same ToolAssertion for chaining.
func (a *ToolAssertion) WithParam(key string, value any) *ToolAssertion {
	expectedJSON, _ := json.Marshal(value)
	a.filters = append(a.filters, func(tc kernel.ToolCallResult) bool {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(tc.Params, &params); err != nil {
			return false
		}
		actual, ok := params[key]
		if !ok {
			return false
		}
		return string(actual) == string(expectedJSON)
	})
	return a
}

// WithParamMatch adds a filter: uses a judge LLM to evaluate whether the parameter value
// satisfies the given criteria. The judge is called with a prompt containing the parameter
// value and the criteria, and must respond with JSON: {"reasoning":"...","condition_met":bool}.
// Returns the same ToolAssertion for chaining.
func (a *ToolAssertion) WithParamMatch(key string, judge kernel.LLM, criteria string) *ToolAssertion {
	a.filters = append(a.filters, func(tc kernel.ToolCallResult) bool {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(tc.Params, &params); err != nil {
			return false
		}
		actual, ok := params[key]
		if !ok {
			return false
		}

		prompt := fmt.Sprintf(
			"Evaluate whether this value satisfies the criteria.\n\nValue: %s\nCriteria: %s\n\nRespond with JSON: {\"reasoning\": \"...\", \"condition_met\": true/false}",
			string(actual), criteria,
		)

		resp, err := judge.Generate(context.Background(), kernel.GenerateParams{
			Messages: []kernel.Message{kernel.UserMsg(prompt)},
		})
		if err != nil {
			return false
		}

		var verdict struct {
			Reasoning    string `json:"reasoning"`
			ConditionMet bool   `json:"condition_met"`
		}
		if err := json.Unmarshal([]byte(resp.Text), &verdict); err != nil {
			return false
		}
		return verdict.ConditionMet
	})
	return a
}

// --- Response Assertions ---

// ResponseAssertion provides assertions about the final text response.
type ResponseAssertion struct {
	result *TestResult
}

// ExpectResponse begins a response assertion chain.
func (r *TestResult) ExpectResponse() *ResponseAssertion {
	return &ResponseAssertion{result: r}
}

// Contains asserts the response text contains the given substring.
func (a *ResponseAssertion) Contains(t *testing.T, substring string) {
	t.Helper()
	if !strings.Contains(a.result.Text(), substring) {
		t.Errorf("expected response to contain %q, got %q", substring, a.result.Text())
	}
}

// NotContains asserts the response text does not contain the given substring.
func (a *ResponseAssertion) NotContains(t *testing.T, substring string) {
	t.Helper()
	if strings.Contains(a.result.Text(), substring) {
		t.Errorf("expected response to not contain %q, got %q", substring, a.result.Text())
	}
}

// Satisfies asserts the response satisfies the given criteria according to a judge LLM.
// The judge is called with a prompt containing the response and criteria, and must respond
// with JSON: {"reasoning":"...","condition_met":bool}.
func (a *ResponseAssertion) Satisfies(t *testing.T, judge kernel.LLM, criteria string) {
	t.Helper()

	prompt := fmt.Sprintf(
		"Evaluate whether this response satisfies the criteria.\n\nResponse: %s\nCriteria: %s\n\nRespond with JSON: {\"reasoning\": \"...\", \"condition_met\": true/false}",
		a.result.Text(), criteria,
	)

	resp, err := judge.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg(prompt)},
	})
	if err != nil {
		t.Fatalf("judge LLM failed: %v", err)
	}

	var verdict struct {
		Reasoning    string `json:"reasoning"`
		ConditionMet bool   `json:"condition_met"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &verdict); err != nil {
		t.Fatalf("failed to parse judge response %q: %v", resp.Text, err)
	}

	if !verdict.ConditionMet {
		t.Errorf("response did not satisfy criteria %q. Judge reasoning: %s", criteria, verdict.Reasoning)
	}
}

// --- Structural Assertions ---

// ExpectRounds asserts the agent executed exactly n rounds.
func (r *TestResult) ExpectRounds(t *testing.T, n int) {
	t.Helper()
	actual := r.RoundCount()
	if actual != n {
		t.Errorf("expected %d rounds, got %d", n, actual)
	}
}
