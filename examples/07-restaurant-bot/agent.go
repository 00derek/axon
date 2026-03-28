// examples/07-restaurant-bot/agent.go
//
// Agent construction for the restaurant bot example.
// Demonstrates middleware composition, lifecycle hooks, guard integration,
// and history persistence in a single NewRestaurantAgent constructor.
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

// NewRestaurantAgent builds a fully configured restaurant bot agent.
// It stacks middleware (retry, timeout, logging, cost tracking), registers
// lifecycle hooks for guard checks, history load/save, and tool logging,
// and sets the restaurant assistant system prompt.
func NewRestaurantAgent(cfg BotConfig, sessionID string) *kernel.Agent {
	// Stack middleware: retry → timeout → logging → cost tracking → raw LLM.
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

		// OnStart: run the guard and, if allowed, prepend conversation history.
		kernel.OnStart(func(tc *kernel.TurnContext) {
			// Guard check
			result, err := cfg.Guard.Check(context.Background(), tc.Input)
			if err != nil {
				cfg.Logger.Error("guard check error", "error", err)
				return
			}
			if !result.Allowed {
				cfg.Logger.Warn("input blocked by guard", "reason", result.Reason)
				// Disable all tools so the agent returns a refusal without tool access.
				tc.AgentCtx.DisableTools(
					"search_restaurants", "get_weather", "get_menu", "make_reservation",
				)
				return
			}

			// Load prior history and prepend it to the conversation.
			msgs, err := cfg.HistoryStore.LoadMessages(context.Background(), sessionID, 20)
			if err != nil {
				cfg.Logger.Error("history load error", "session", sessionID, "error", err)
				return
			}
			if len(msgs) > 0 {
				cfg.Logger.Info("history loaded", "session", sessionID, "messages", len(msgs))
				// Insert history before the current user message (last element).
				current := tc.AgentCtx.Messages
				if len(current) > 0 {
					head := current[:len(current)-1]
					tail := current[len(current)-1:]
					tc.AgentCtx.Messages = append(append(head, msgs...), tail...)
				}
			}
		}),

		// OnFinish: persist the new turn (user input + assistant response) to history.
		kernel.OnFinish(func(tc *kernel.TurnContext) {
			if tc.Result == nil || tc.Result.Text == "" {
				return
			}
			newMsgs := []kernel.Message{
				kernel.UserMsg(tc.Input),
				kernel.AssistantMsg(tc.Result.Text),
			}
			if err := cfg.HistoryStore.SaveMessages(context.Background(), sessionID, newMsgs); err != nil {
				cfg.Logger.Error("history save error", "session", sessionID, "error", err)
			}
		}),

		// OnToolStart: log when a tool is about to be called.
		kernel.OnToolStart(func(tc *kernel.ToolContext) {
			cfg.Logger.Info("tool start", "tool", tc.ToolName)
		}),

		// OnToolEnd: log the outcome of a tool call.
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
