// testing/scorecard_test.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestScoreCardAllPass(t *testing.T) {
	// Judge that always says conditions are met
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"Greeting is present","condition_met":true},{"reasoning":"Restaurant mentioned","condition_met":true}]`).
		OnRound(1).RespondWithText(`should not be called`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "The assistant greets the user", Score: 1},
			{Condition: "The assistant mentions a restaurant name", Score: 2},
		},
		PassingScore: 3,
	}

	messages := []kernel.Message{
		kernel.UserMsg("Find thai food"),
		kernel.AssistantMsg("Hello! I found Thai Basil restaurant for you."),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalScore != 3 {
		t.Errorf("expected total score 3, got %d", result.TotalScore)
	}
	if result.MaxScore != 3 {
		t.Errorf("expected max score 3, got %d", result.MaxScore)
	}
	if !result.Passed {
		t.Error("expected Passed=true")
	}
	if len(result.Details) != 2 {
		t.Fatalf("expected 2 details, got %d", len(result.Details))
	}
	if !result.Details[0].Met {
		t.Error("expected first criterion to be met")
	}
	if !result.Details[1].Met {
		t.Error("expected second criterion to be met")
	}
}

func TestScoreCardPartialPass(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"Greeting found","condition_met":true},{"reasoning":"No restaurant name","condition_met":false}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "The assistant greets the user", Score: 1},
			{Condition: "The assistant mentions a restaurant name", Score: 2},
		},
		PassingScore: 2,
	}

	messages := []kernel.Message{
		kernel.UserMsg("Find thai food"),
		kernel.AssistantMsg("Hello! Let me search for that."),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalScore != 1 {
		t.Errorf("expected total score 1, got %d", result.TotalScore)
	}
	if result.Passed {
		t.Error("expected Passed=false (score 1 < passing 2)")
	}
	if result.Details[0].Met != true {
		t.Error("expected first criterion met")
	}
	if result.Details[1].Met != false {
		t.Error("expected second criterion not met")
	}
}

func TestScoreCardAllFail(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"No greeting","condition_met":false},{"reasoning":"No name","condition_met":false}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Greets user", Score: 1},
			{Condition: "Names restaurant", Score: 1},
		},
		PassingScore: 1,
	}

	messages := []kernel.Message{
		kernel.UserMsg("Hi"),
		kernel.AssistantMsg("Error occurred."),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalScore != 0 {
		t.Errorf("expected total score 0, got %d", result.TotalScore)
	}
	if result.Passed {
		t.Error("expected Passed=false")
	}
}

func TestScoreCardJudgeError(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithError(fmt.Errorf("judge unavailable"))

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Something", Score: 1},
		},
		PassingScore: 1,
	}

	_, err := sc.Evaluate(context.Background(), judge, []kernel.Message{
		kernel.UserMsg("test"),
	})
	if err == nil {
		t.Fatal("expected error when judge fails")
	}
}

func TestScoreCardJudgeMalformedJSON(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`not valid json`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Something", Score: 1},
		},
		PassingScore: 1,
	}

	_, err := sc.Evaluate(context.Background(), judge, []kernel.Message{
		kernel.UserMsg("test"),
	})
	if err == nil {
		t.Fatal("expected error for malformed judge response")
	}
}

func TestScoreCardReasoningPreserved(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"The user said hello and the bot replied with hi","condition_met":true}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Greeting present", Score: 1},
		},
		PassingScore: 1,
	}

	messages := []kernel.Message{
		kernel.UserMsg("hello"),
		kernel.AssistantMsg("hi"),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Details[0].Reasoning != "The user said hello and the bot replied with hi" {
		t.Errorf("reasoning not preserved: %q", result.Details[0].Reasoning)
	}
}

func TestScoreCardZeroPassingScore(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"nope","condition_met":false}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Anything", Score: 1},
		},
		PassingScore: 0, // zero means always passes
	}

	messages := []kernel.Message{kernel.UserMsg("test")}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true when PassingScore is 0")
	}
}

// Verify the prompt format sent to the judge includes all criteria and messages.
func TestScoreCardPromptFormat(t *testing.T) {
	var capturedPrompt string

	captureLLM := &promptCapturingLLM{
		response: `[{"reasoning":"ok","condition_met":true},{"reasoning":"ok","condition_met":true}]`,
	}

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "User is greeted", Score: 1},
			{Condition: "Restaurant named", Score: 1},
		},
		PassingScore: 2,
	}

	messages := []kernel.Message{
		kernel.UserMsg("hello"),
		kernel.AssistantMsg("Hi! Thai Basil is great."),
	}

	sc.Evaluate(context.Background(), captureLLM, messages)
	capturedPrompt = captureLLM.lastPrompt

	// The prompt should mention the criteria
	if !containsStr(capturedPrompt, "User is greeted") {
		t.Error("prompt should contain criterion text")
	}
	if !containsStr(capturedPrompt, "Restaurant named") {
		t.Error("prompt should contain second criterion text")
	}
	// The prompt should include the conversation
	if !containsStr(capturedPrompt, "hello") {
		t.Error("prompt should contain user message")
	}
	if !containsStr(capturedPrompt, "Thai Basil") {
		t.Error("prompt should contain assistant message")
	}
}

// promptCapturingLLM captures the last prompt text sent to it.
type promptCapturingLLM struct {
	response   string
	lastPrompt string
}

func (p *promptCapturingLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	for _, msg := range params.Messages {
		p.lastPrompt += msg.TextContent()
	}
	return kernel.Response{Text: p.response, FinishReason: "stop"}, nil
}

func (p *promptCapturingLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (p *promptCapturingLLM) Model() string { return "prompt-capture" }

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Suppress unused import warning
var _ = json.Marshal
var _ = fmt.Sprintf
