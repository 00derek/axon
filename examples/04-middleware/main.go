// examples/04-middleware/main.go
//
// Middleware composition example for Axon agents.
//
// This example demonstrates two patterns for wrapping an LLM with middleware:
//
//	Part 1 — Basic middleware composition:
//	  A MockLLM is wrapped with WithRetry, WithTimeout, WithLogging, and
//	  WithCostTracker using middleware.Wrap. The agent runs a query and the
//	  example prints the response along with the accumulated token usage from
//	  the CostTracker snapshot.
//
//	Part 2 — Model routing:
//	  Two MockLLMs ("cheap" and "expensive") are wired up with RouteByToolCount.
//	  Requests with few tools go to the cheap model; requests that exceed the
//	  tool threshold go to the expensive model. A simple agent run shows which
//	  model handled the request.
//
// Run with:
//
//	cd examples && go run ./04-middleware/
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/axonframework/axon/kernel"
	"github.com/axonframework/axon/middleware"
	axontest "github.com/axonframework/axon/testing"
)

func main() {
	part1BasicComposition()
	fmt.Println()
	part2ModelRouting()
}

// part1BasicComposition shows how to stack multiple middleware layers onto a
// single LLM using middleware.Wrap. Call order: WithRetry → WithTimeout →
// WithLogging → WithCostTracker → MockLLM.
func part1BasicComposition() {
	fmt.Println("=== Part 1: Basic Middleware Composition ===")

	// Create the underlying MockLLM with a scripted text response.
	mockLLM := axontest.NewMockLLM().
		OnRound(0).RespondWithText("The speed of light is approximately 299,792,458 metres per second.")

	// Create a structured logger that writes to stdout.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create a CostTracker to accumulate token usage across calls.
	// A CostFunc is registered so EstimatedCost is populated automatically.
	tracker := middleware.NewCostTracker()
	tracker.CostFunc = func(inputTokens, outputTokens int) float64 {
		// Hypothetical pricing: $0.001 per 1K input tokens, $0.002 per 1K output.
		return float64(inputTokens)*0.000001 + float64(outputTokens)*0.000002
	}

	// Wrap the LLM with all four middleware layers.
	// middleware.Wrap applies them so the first argument is the outermost wrapper.
	// Effective call order: retry → timeout → logging → cost → mockLLM
	wrappedLLM := middleware.Wrap(
		mockLLM,
		middleware.WithRetry(3, 100*time.Millisecond),
		middleware.WithTimeout(5*time.Second),
		middleware.WithLogging(logger),
		middleware.WithCostTracker(tracker),
	)

	// Build and run the agent with the wrapped LLM.
	agent := kernel.NewAgent(
		kernel.WithModel(wrappedLLM),
		kernel.WithSystemPrompt("You are a helpful science assistant."),
	)

	result, err := agent.Run(context.Background(), "What is the speed of light?")
	if err != nil {
		log.Fatalf("agent.Run failed: %v", err)
	}

	fmt.Println("Agent response:", result.Text)
	fmt.Printf("Rounds completed: %d\n", len(result.Rounds))

	// Print a snapshot of accumulated token usage and cost.
	snap := tracker.Snapshot()
	fmt.Printf("Token usage — input: %d, output: %d\n",
		snap.TotalInputTokens, snap.TotalOutputTokens)
	fmt.Printf("Estimated cost: $%.6f\n", snap.EstimatedCost)
}

// part2ModelRouting shows how to use RouteByToolCount to direct requests to
// different models based on how many tools are registered on the agent.
func part2ModelRouting() {
	fmt.Println("=== Part 2: Model Routing ===")

	// Two MockLLMs representing a cheap/fast model and an expensive/capable one.
	cheapLLM := axontest.NewMockLLM().
		OnRound(0).RespondWithText("[cheap-model] Handled a simple request with no tools.")

	expensiveLLM := axontest.NewMockLLM().
		OnRound(0).RespondWithText("[expensive-model] Handled a complex request requiring many tools.")

	// RouteByToolCount routes to expensiveLLM when the number of tools exceeds
	// the threshold (2). Otherwise cheapLLM is used as the fallback.
	router := middleware.RouteByToolCount(2, cheapLLM, expensiveLLM)

	// --- Sub-case A: agent with no tools → routed to cheap model ---
	fmt.Println("Sub-case A: agent with 0 tools (threshold=2, expect cheap-model)")

	agentSimple := kernel.NewAgent(
		kernel.WithModel(router),
		kernel.WithSystemPrompt("You are a helpful assistant."),
	)

	resultA, err := agentSimple.Run(context.Background(), "Just say hello.")
	if err != nil {
		log.Fatalf("agentSimple.Run failed: %v", err)
	}
	fmt.Println("Response:", resultA.Text)

	// --- Sub-case B: agent with 3 tools → routed to expensive model ---
	fmt.Println("\nSub-case B: agent with 3 tools (threshold=2, expect expensive-model)")

	// Reset the expensive mock so it can handle another round.
	expensiveLLM2 := axontest.NewMockLLM().
		OnRound(0).RespondWithText("[expensive-model] Handled a complex request requiring many tools.")

	router2 := middleware.RouteByToolCount(2, cheapLLM, expensiveLLM2)

	// Three trivial no-op tools to push the tool count above the threshold.
	tool1 := kernel.NewTool[struct{}, string]("tool_a", "Tool A", func(_ context.Context, _ struct{}) (string, error) { return "a", nil })
	tool2 := kernel.NewTool[struct{}, string]("tool_b", "Tool B", func(_ context.Context, _ struct{}) (string, error) { return "b", nil })
	tool3 := kernel.NewTool[struct{}, string]("tool_c", "Tool C", func(_ context.Context, _ struct{}) (string, error) { return "c", nil })

	agentComplex := kernel.NewAgent(
		kernel.WithModel(router2),
		kernel.WithSystemPrompt("You are a helpful assistant with many tools."),
		kernel.WithTools(tool1, tool2, tool3),
	)

	resultB, err := agentComplex.Run(context.Background(), "Do something complex.")
	if err != nil {
		log.Fatalf("agentComplex.Run failed: %v", err)
	}
	fmt.Println("Response:", resultB.Text)
}
