// examples/07-restaurant-bot/main.go
//
// CLI entry point for the restaurant bot example.
//
// Runs a scripted three-turn demo conversation that shows:
//   - Turn 1: searching for Italian restaurants
//   - Turn 2: browsing the menu at Bella Trattoria
//   - Turn 3: making a reservation
//
// The agent uses MockLLM so the example runs without a real LLM API key.
//
// Run with:
//
//	cd examples && go run ./07-restaurant-bot/
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/axonframework/axon/axontest"
)

func main() {
	// -------------------------------------------------------------------------
	// Set up a structured logger that writes to stdout.
	// -------------------------------------------------------------------------
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// -------------------------------------------------------------------------
	// Build a MockLLM with scripted responses for each LLM call (round).
	//
	// The agent loop calls the LLM once per round. When the LLM returns tool
	// calls, the agent executes them and calls the LLM again with the results.
	// Round numbering is global across all agent.Run() calls on the same MockLLM.
	//
	// Turn 1 — "Find Italian restaurants":
	//   Round 0: LLM calls search_restaurants
	//   Round 1: LLM receives results, returns final text
	//
	// Turn 2 — "Show me the menu for Bella Trattoria":
	//   Round 2: LLM calls get_menu
	//   Round 3: LLM receives menu, returns final text
	//
	// Turn 3 — "Book a table for 2 at 7 PM":
	//   Round 4: LLM calls make_reservation
	//   Round 5: LLM receives confirmation, returns final text
	// -------------------------------------------------------------------------
	llm := axontest.NewMockLLM().
		// Turn 1 — search
		OnRound(0).RespondWithToolCall("search_restaurants", map[string]any{
		"query":    "italian",
		"location": "downtown",
	}).
		OnRound(1).RespondWithText(
		"I found some great Italian options! Bella Trattoria stands out with a 4.7 rating "+
			"and mid-range pricing ($$). Would you like to see their menu or make a reservation?",
	).
		// Turn 2 — menu
		OnRound(2).RespondWithToolCall("get_menu", map[string]any{
		"restaurant": "Bella Trattoria",
	}).
		OnRound(3).RespondWithText(
		"Bella Trattoria's menu looks delicious! Highlights include Margherita Pizza ($14), "+
			"Fettuccine Alfredo ($18), and Tiramisu for dessert ($8). Shall I make a reservation?",
	).
		// Turn 3 — reservation
		OnRound(4).RespondWithToolCall("make_reservation", map[string]any{
		"restaurant": "Bella Trattoria",
		"party_size": 2,
		"time":       "7:00 PM",
	}).
		OnRound(5).RespondWithText(
		"Your reservation at Bella Trattoria for 2 guests at 7:00 PM is confirmed! " +
			"Your confirmation code is RES-BEL-0211. Enjoy your dinner!",
	)

	// -------------------------------------------------------------------------
	// Build the agent from a default config (in-memory history, blocklist guard).
	// -------------------------------------------------------------------------
	cfg := NewDefaultConfig(llm, logger)
	agent := NewRestaurantAgent(cfg, "demo-session-001")

	ctx := context.Background()

	// -------------------------------------------------------------------------
	// Turn 1: Search for Italian restaurants.
	// -------------------------------------------------------------------------
	fmt.Println("=== Turn 1: Find Italian restaurants ===")
	query1 := "Find me Italian restaurants downtown"
	fmt.Println("User:", query1)

	result1, err := agent.Run(ctx, query1)
	if err != nil {
		log.Fatalf("Turn 1 failed: %v", err)
	}
	fmt.Println("Assistant:", result1.Text)
	fmt.Println()

	// -------------------------------------------------------------------------
	// Turn 2: Check the menu at Bella Trattoria.
	// -------------------------------------------------------------------------
	fmt.Println("=== Turn 2: Check the menu ===")
	query2 := "Can I see the menu for Bella Trattoria?"
	fmt.Println("User:", query2)

	result2, err := agent.Run(ctx, query2)
	if err != nil {
		log.Fatalf("Turn 2 failed: %v", err)
	}
	fmt.Println("Assistant:", result2.Text)
	fmt.Println()

	// -------------------------------------------------------------------------
	// Turn 3: Make a reservation.
	// -------------------------------------------------------------------------
	fmt.Println("=== Turn 3: Make a reservation ===")
	query3 := "Book a table for 2 at Bella Trattoria tonight at 7 PM"
	fmt.Println("User:", query3)

	result3, err := agent.Run(ctx, query3)
	if err != nil {
		log.Fatalf("Turn 3 failed: %v", err)
	}
	fmt.Println("Assistant:", result3.Text)
	fmt.Println()

	// -------------------------------------------------------------------------
	// Print accumulated cost summary.
	// -------------------------------------------------------------------------
	fmt.Println("=== Session Summary ===")
	fmt.Println(FormatCostSummary(cfg.CostTracker))
}
