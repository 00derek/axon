// testing/scorecard.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/axonframework/axon/kernel"
)

// ScoreCard evaluates a conversation against a set of criteria using a judge LLM.
type ScoreCard struct {
	Criteria     []Criterion
	PassingScore int
}

// Criterion is a single evaluation condition with an associated score.
type Criterion struct {
	Condition string // Human-readable description, e.g. "The assistant confirms the reservation"
	Score     int    // Points awarded if the condition is met
}

// ScoreResult holds the evaluation outcome.
type ScoreResult struct {
	TotalScore int
	MaxScore   int
	Passed     bool
	Details    []CriterionResult
}

// CriterionResult captures the evaluation of a single criterion.
type CriterionResult struct {
	Condition string
	Score     int
	Met       bool
	Reasoning string
}

// judgeVerdict is the expected JSON structure from the judge LLM per criterion.
type judgeVerdict struct {
	Reasoning    string `json:"reasoning"`
	ConditionMet bool   `json:"condition_met"`
}

// Evaluate runs the judge LLM against the conversation for all criteria.
// The judge receives a single prompt with all criteria and must return a JSON array
// with one verdict per criterion: [{"reasoning":"...","condition_met":true/false}, ...].
// Reasoning-before-verdict ordering reduces judge evaluation errors.
func (sc *ScoreCard) Evaluate(ctx context.Context, judge kernel.LLM, messages []kernel.Message) (*ScoreResult, error) {
	prompt := buildEvalPrompt(sc.Criteria, messages)

	resp, err := judge.Generate(ctx, kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg(prompt)},
	})
	if err != nil {
		return nil, fmt.Errorf("judge LLM call failed: %w", err)
	}

	var verdicts []judgeVerdict
	if err := json.Unmarshal([]byte(resp.Text), &verdicts); err != nil {
		return nil, fmt.Errorf("failed to parse judge response as JSON array: %w (response: %q)", err, resp.Text)
	}

	if len(verdicts) != len(sc.Criteria) {
		return nil, fmt.Errorf("judge returned %d verdicts but expected %d (one per criterion)", len(verdicts), len(sc.Criteria))
	}

	var totalScore, maxScore int
	details := make([]CriterionResult, len(sc.Criteria))

	for i, criterion := range sc.Criteria {
		maxScore += criterion.Score
		detail := CriterionResult{
			Condition: criterion.Condition,
			Score:     criterion.Score,
			Met:       verdicts[i].ConditionMet,
			Reasoning: verdicts[i].Reasoning,
		}
		if detail.Met {
			totalScore += criterion.Score
		}
		details[i] = detail
	}

	return &ScoreResult{
		TotalScore: totalScore,
		MaxScore:   maxScore,
		Passed:     totalScore >= sc.PassingScore,
		Details:    details,
	}, nil
}

// buildEvalPrompt constructs the evaluation prompt for the judge LLM.
func buildEvalPrompt(criteria []Criterion, messages []kernel.Message) string {
	var b strings.Builder

	b.WriteString("You are an evaluation judge. Evaluate the following conversation against each criterion.\n\n")

	// Format conversation
	b.WriteString("## Conversation\n\n")
	for _, msg := range messages {
		b.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, msg.TextContent()))
	}

	// Format criteria
	b.WriteString("\n## Criteria\n\n")
	for i, c := range criteria {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, c.Condition))
	}

	// Format output instructions
	b.WriteString("\n## Instructions\n\n")
	b.WriteString("For each criterion above, evaluate whether it is met by the conversation.\n")
	b.WriteString("Think step-by-step in the reasoning field BEFORE rendering your verdict.\n")
	b.WriteString("Respond with a JSON array (one element per criterion, in order):\n\n")
	b.WriteString("```json\n")
	b.WriteString("[{\"reasoning\": \"your step-by-step analysis\", \"condition_met\": true}, ...]\n")
	b.WriteString("```\n\n")
	b.WriteString("Respond with ONLY the JSON array. No other text.")

	return b.String()
}
