// examples/06-testing/agent_test.go
//
// Testing patterns example for Axon agents.
//
// This example demonstrates Axon's testing utilities from the axontest package:
//   - MockLLM with scripted per-round responses (RespondWithText, RespondWithToolCall)
//   - axontest.Run() to execute an agent in a test context
//   - ToolAssertion: Called, NotCalled, CalledTimes, WithParam
//   - ResponseAssertion: Contains
//   - WithHistory to inject prior conversation turns
//   - TestResult.ExpectRounds for structural assertions
//   - ScoreCard / Criterion struct construction for LLM-as-judge evaluation
//
// Run with:
//
//	cd examples && go test -v -short ./06-testing/
package testing_example

import (
	"context"
	"testing"

	"github.com/axonframework/axon/kernel"
	axontest "github.com/axonframework/axon/testing"
)

// --- Parameter structs ---

// SearchParams holds parameters for the "search" tool.
type SearchParams struct {
	Query string `json:"query" description:"Search query string"`
}

// BookParams holds parameters for the "book" tool.
type BookParams struct {
	Item string `json:"item" description:"Item to book or reserve"`
	Date string `json:"date" description:"Date for the reservation (YYYY-MM-DD)"`
}

// --- Tool constructors ---

// newSearchTool builds a search tool that returns a canned result list.
func newSearchTool() kernel.Tool {
	return kernel.NewTool[SearchParams, string](
		"search",
		"Search for information and return a list of relevant results.",
		func(ctx context.Context, p SearchParams) (string, error) {
			return "Result 1: Acme Hotel\nResult 2: Grand Inn\nResult 3: City Lodge", nil
		},
	)
}

// newBookTool builds a booking tool that confirms a reservation.
func newBookTool() kernel.Tool {
	return kernel.NewTool[BookParams, string](
		"book",
		"Book or reserve an item for a given date.",
		func(ctx context.Context, p BookParams) (string, error) {
			return "Reservation confirmed for " + p.Item + " on " + p.Date, nil
		},
	)
}

// newTestAgent creates an agent configured with both tools and the given LLM.
func newTestAgent(llm kernel.LLM) *kernel.Agent {
	return kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a helpful travel assistant with search and booking tools."),
		kernel.WithTools(newSearchTool(), newBookTool()),
	)
}

// --- Tests ---

// TestSearchToolIsCalled verifies the LLM calls search with the expected query
// and that the final response mentions a hotel.
func TestSearchToolIsCalled(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "hotels in Paris"}).
		OnRound(1).RespondWithText("I found several hotels in Paris for you.")

	agent := newTestAgent(llm)

	result := axontest.Run(t, agent, "Find me hotels in Paris")

	// Assert search was called with the correct query parameter.
	result.ExpectTool("search").
		WithParam("query", "hotels in Paris").
		Called(t)

	// Assert the response mentions hotels.
	result.ExpectResponse().Contains(t, "hotels")

	// Two rounds: one tool call, one text response.
	result.ExpectRounds(t, 2)
}

// TestBookToolNotCalledForSearch verifies the book tool is never invoked
// when the user only asks for a search.
func TestBookToolNotCalledForSearch(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "restaurants near me"}).
		OnRound(1).RespondWithText("Here are some restaurants near you.")

	agent := newTestAgent(llm)

	result := axontest.Run(t, agent, "Find restaurants near me")

	// Book should never have been called.
	result.ExpectTool("book").NotCalled(t)

	// Search should have been called exactly once.
	result.ExpectTool("search").CalledTimes(t, 1)
}

// TestSearchThenBook verifies a two-tool sequence: the agent first searches,
// then books based on the search results.
func TestSearchThenBook(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "hotels in Rome"}).
		OnRound(1).RespondWithToolCall("book", map[string]any{"item": "Acme Hotel", "date": "2025-06-15"}).
		OnRound(2).RespondWithText("Your stay at Acme Hotel on 2025-06-15 has been confirmed.")

	agent := newTestAgent(llm)

	result := axontest.Run(t, agent, "Find and book a hotel in Rome for June 15th")

	// Both tools should have been called.
	result.ExpectTool("search").Called(t)
	result.ExpectTool("book").WithParam("item", "Acme Hotel").Called(t)
	result.ExpectTool("book").WithParam("date", "2025-06-15").Called(t)

	// Three rounds: search, book, final text.
	result.ExpectRounds(t, 3)

	// Response confirms the booking.
	result.ExpectResponse().Contains(t, "confirmed")
}

// TestWithHistory demonstrates injecting prior conversation turns so the agent
// can continue an existing dialogue without re-running the whole session.
func TestWithHistory(t *testing.T) {
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("Based on our earlier discussion, I recommend the Grand Inn.")

	agent := newTestAgent(llm)

	// Inject a prior exchange: user asked about Paris, assistant answered.
	history := []kernel.Message{
		kernel.UserMsg("I'm looking for a hotel in Paris for next week."),
		kernel.AssistantMsg("I'd be happy to help you find a hotel in Paris!"),
	}

	result := axontest.Run(t, agent, "Which one do you recommend?",
		axontest.WithHistory(history...),
	)

	// The agent should reference the prior context in its answer.
	result.ExpectResponse().Contains(t, "Grand Inn")

	// Only one round needed — no tool calls required for this follow-up.
	result.ExpectRounds(t, 1)
}

// TestScoreCard demonstrates constructing a ScoreCard with evaluation criteria.
// In a real evaluation suite you would pass a live judge LLM to sc.Evaluate();
// here we verify the struct is well-formed and skip the live call with t.Skip.
func TestScoreCard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live LLM judge evaluation in short mode")
	}

	// Define evaluation criteria for a hotel-booking conversation.
	sc := axontest.ScoreCard{
		Criteria: []axontest.Criterion{
			{Condition: "The assistant confirms the reservation", Score: 30},
			{Condition: "The assistant mentions the hotel name", Score: 20},
			{Condition: "The assistant mentions the check-in date", Score: 20},
			{Condition: "The response is polite and professional", Score: 30},
		},
		PassingScore: 70,
	}

	// Verify the scorecard is configured correctly.
	if len(sc.Criteria) != 4 {
		t.Fatalf("expected 4 criteria, got %d", len(sc.Criteria))
	}

	totalMaxScore := 0
	for _, c := range sc.Criteria {
		totalMaxScore += c.Score
	}
	if totalMaxScore != 100 {
		t.Errorf("expected max score of 100, got %d", totalMaxScore)
	}

	// To evaluate against a real conversation, you would call:
	//
	//   messages := []kernel.Message{
	//       kernel.UserMsg("Book me a room at the Grand Inn for June 15th."),
	//       kernel.AssistantMsg("Your reservation at the Grand Inn on 2025-06-15 is confirmed!"),
	//   }
	//   scoreResult, err := sc.Evaluate(context.Background(), judgeLLM, messages)
	//
	// Then assert scoreResult.Passed == true.
	_ = context.Background() // imported for the example above
}
