// examples/07-restaurant-bot/agent.go
//
// Agent construction for the restaurant bot example.
// Demonstrates middleware composition, a guard hook, and tool logging — with
// conversation history handled by the session.Session lifecycle owner so the
// agent itself never has to load or persist messages.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/axonframework/axon/interfaces"
	"github.com/axonframework/axon/interfaces/inmemory"
	"github.com/axonframework/axon/kernel"
	"github.com/axonframework/axon/middleware"
)

const systemPrompt = `You are a friendly restaurant assistant. You help users:
- Find restaurants by cuisine type or neighborhood
- Check weather to recommend indoor vs outdoor dining
- Browse menus and recommend dishes
- Make reservations

Always be warm, helpful, and concise. When presenting restaurant options, highlight
ratings and price ranges. When making reservations, confirm all details clearly.`

// BotConfig holds the dependencies needed to build a restaurant agent.
type BotConfig struct {
	LLM          kernel.LLM
	Logger       *slog.Logger
	HistoryStore interfaces.HistoryStore
	Guard        interfaces.Guard
	CostTracker  *middleware.CostTracker
}

// NewDefaultConfig creates a BotConfig with sensible defaults: an in-memory
// history store and a blocklist guard filtering a small set of off-topic phrases.
func NewDefaultConfig(llm kernel.LLM, logger *slog.Logger) BotConfig {
	return BotConfig{
		LLM:          llm,
		Logger:       logger,
		HistoryStore: inmemory.NewHistoryStore(),
		Guard: interfaces.NewBlocklistGuard([]string{
			"ignore previous instructions",
			"jailbreak",
			"forget your instructions",
		}),
		CostTracker: middleware.NewCostTracker(),
	}
}

// NewRestaurantAgent builds the restaurant bot agent.
//
// History load / persist used to live here as OnStart / OnFinish hooks; it is
// now the responsibility of session.Session.Run, so this constructor only
// concerns itself with what is genuinely agent-scoped: middleware, the guard,
// and tool logging.
func NewRestaurantAgent(cfg BotConfig) *kernel.Agent {
	wrappedLLM := middleware.Wrap(
		cfg.LLM,
		middleware.WithRetry(3, 200*time.Millisecond),
		middleware.WithTimeout(30*time.Second),
		middleware.WithLogging(cfg.Logger),
		middleware.WithCostTracker(cfg.CostTracker),
	)

	return kernel.NewAgent(
		kernel.WithModel(wrappedLLM),
		kernel.WithSystemPrompt(systemPrompt),
		kernel.WithTools(AllTools()...),
		kernel.WithMaxRounds(10),

		// OnStart: run the guard. If the input is blocked, disable all tools
		// so the agent can only return a refusal.
		kernel.OnStart(func(tc *kernel.TurnContext) {
			result, err := cfg.Guard.Check(context.Background(), tc.Input)
			if err != nil {
				cfg.Logger.Error("guard check error", "error", err)
				return
			}
			if !result.Allowed {
				cfg.Logger.Warn("input blocked by guard", "reason", result.Reason)
				tc.AgentCtx.DisableTools(
					"search_restaurants", "get_weather", "get_menu", "make_reservation",
				)
			}
		}),

		kernel.OnToolStart(func(tc *kernel.ToolContext) {
			cfg.Logger.Info("tool start", "tool", tc.ToolName)
		}),

		kernel.OnToolEnd(func(tc *kernel.ToolContext) {
			if tc.Error != nil {
				cfg.Logger.Error("tool error", "tool", tc.ToolName, "error", tc.Error)
			} else {
				cfg.Logger.Info("tool end", "tool", tc.ToolName)
			}
		}),
	)
}

// FormatCostSummary returns a human-readable summary of token usage from the
// cost tracker. EstimatedCost is only shown when a CostFunc was registered.
func FormatCostSummary(tracker *middleware.CostTracker) string {
	snap := tracker.Snapshot()
	total := snap.TotalInputTokens + snap.TotalOutputTokens
	summary := fmt.Sprintf(
		"Tokens used — input: %d, output: %d, total: %d",
		snap.TotalInputTokens, snap.TotalOutputTokens, total,
	)
	if snap.EstimatedCost > 0 {
		summary += fmt.Sprintf(" | estimated cost: $%.6f", snap.EstimatedCost)
	}
	return summary
}
