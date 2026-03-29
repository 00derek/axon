// testing/eval.go
package axontest

import (
	"context"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// Case represents a single test case in a batch evaluation.
type Case struct {
	Name    string           // Descriptive name (used as t.Run subtest name)
	Input   string           // User input to send to the agent
	History []kernel.Message // Optional conversation history to prepend
	Expect  *Expectation     // Optional assertions to check
}

// Expectation holds the expected outcomes for a Case.
// All fields are optional. Only non-nil/non-empty fields are checked.
type Expectation struct {
	// Response text assertions
	ResponseContains    []string // Response must contain each substring
	ResponseNotContains []string // Response must not contain any substring

	// Tool call assertions
	ToolCalled    []string // Each named tool must have been called at least once
	ToolNotCalled []string // Each named tool must not have been called

	// Structural assertions
	Rounds *int // If non-nil, asserts exact round count

	// Quality evaluation via judge LLM
	ScoreCard *ScoreCard // If non-nil, runs ScoreCard.Evaluate and asserts pass
}

// Eval runs a batch of test cases against an agent. Each case runs as a subtest.
// If judge is non-nil and a case's Expectation has a ScoreCard, the judge evaluates quality.
// If judge is nil and a ScoreCard is present, the ScoreCard's evaluation is skipped.
func Eval(t *testing.T, agent *kernel.Agent, judge kernel.LLM, cases []Case) {
	t.Helper()

	for _, tc := range cases {
		tc := tc // capture range variable
		name := tc.Name
		if name == "" {
			name = tc.Input
		}

		t.Run(name, func(t *testing.T) {
			// Build run options
			var opts []RunOption
			if len(tc.History) > 0 {
				opts = append(opts, WithHistory(tc.History...))
			}

			// Run the agent
			result := Run(t, agent, tc.Input, opts...)

			// Apply expectations
			if tc.Expect == nil {
				return
			}

			// Response contains
			for _, substr := range tc.Expect.ResponseContains {
				result.ExpectResponse().Contains(t, substr)
			}

			// Response not contains
			for _, substr := range tc.Expect.ResponseNotContains {
				result.ExpectResponse().NotContains(t, substr)
			}

			// Tool called
			for _, toolName := range tc.Expect.ToolCalled {
				result.ExpectTool(toolName).Called(t)
			}

			// Tool not called
			for _, toolName := range tc.Expect.ToolNotCalled {
				result.ExpectTool(toolName).NotCalled(t)
			}

			// Round count
			if tc.Expect.Rounds != nil {
				result.ExpectRounds(t, *tc.Expect.Rounds)
			}

			// ScoreCard evaluation
			if tc.Expect.ScoreCard != nil && judge != nil {
				// Reconstruct messages from the result for the judge.
				// The conversation is: history + user input + agent response.
				var messages []kernel.Message
				messages = append(messages, tc.History...)
				messages = append(messages, kernel.UserMsg(tc.Input))
				messages = append(messages, kernel.AssistantMsg(result.Text()))

				scoreResult, err := tc.Expect.ScoreCard.Evaluate(context.Background(), judge, messages)
				if err != nil {
					t.Fatalf("ScoreCard evaluation failed: %v", err)
				}
				if !scoreResult.Passed {
					t.Errorf("ScoreCard failed: scored %d/%d (need %d). Details:", scoreResult.TotalScore, scoreResult.MaxScore, tc.Expect.ScoreCard.PassingScore)
					for _, d := range scoreResult.Details {
						status := "PASS"
						if !d.Met {
							status = "FAIL"
						}
						t.Errorf("  [%s] %s (score: %d) - %s", status, d.Condition, d.Score, d.Reasoning)
					}
				}
			}
		})
	}
}
