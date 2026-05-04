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
// Each turn is dispatched through a single session.Session value, which owns
// history loading, persistence, and per-session metrics.
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
	"github.com/axonframework/axon/session"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

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

	cfg := NewDefaultConfig(llm, logger)
	agent := NewRestaurantAgent(cfg)

	// One Session value owns history load, persistence, and metrics for the
	// whole conversation. The agent itself is stateless across turns.
	sess := &session.Session{
		ID:      "demo-session-001",
		UserID:  "demo-user",
		History: cfg.HistoryStore,
		Metrics: session.NewSessionMetrics(),
	}

	ctx := context.Background()

	queries := []string{
		"Find me Italian restaurants downtown",
		"Can I see the menu for Bella Trattoria?",
		"Book a table for 2 at Bella Trattoria tonight at 7 PM",
	}
	headers := []string{
		"=== Turn 1: Find Italian restaurants ===",
		"=== Turn 2: Check the menu ===",
		"=== Turn 3: Make a reservation ===",
	}

	for i, q := range queries {
		fmt.Println(headers[i])
		fmt.Println("User:", q)
		result, err := sess.Run(ctx, agent, q)
		if err != nil {
			log.Fatalf("Turn %d failed: %v", i+1, err)
		}
		fmt.Println("Assistant:", result.Text)
		fmt.Println()
	}

	fmt.Println("=== Session Summary ===")
	fmt.Println(FormatCostSummary(cfg.CostTracker))
	snap := sess.Metrics.Snapshot()
	fmt.Printf("Session metrics — runs: %d, input tokens: %d, output tokens: %d, total latency: %v\n",
		snap.RunCount, snap.InputTokens, snap.OutputTokens, snap.TotalLatency)
}
