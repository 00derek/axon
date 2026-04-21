// examples/01-minimal/main.go
//
// Minimal Axon agent example.
//
// This example shows the bare minimum needed to create and run an Axon agent:
//   - A MockLLM with a single scripted text response (no real API key needed)
//   - An agent configured with a model and a system prompt
//   - A single Run() call that drives the agentic loop
//
// Run with:
//
//	cd examples && go run ./01-minimal/
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/axonframework/axon/axontest"
	"github.com/axonframework/axon/kernel"
)

func main() {
	// Create a MockLLM and script round 0 to return a plain text response.
	// OnRound is 0-indexed: round 0 is the first (and only) call in this example.
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithText("Hello from Axon! The agent loop is working.")

	// Build the agent.
	// WithModel wires up the LLM.
	// WithSystemPrompt gives the agent its persona / instructions.
	agent := kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a helpful assistant."),
	)

	// Run the agent with a user message.
	// Run drives the loop until the LLM returns a text response (no tool calls).
	result, err := agent.Run(context.Background(), "Say hello.")
	if err != nil {
		log.Fatalf("agent.Run failed: %v", err)
	}

	// result.Text is the final text produced by the agent.
	fmt.Println("Agent response:", result.Text)
	fmt.Printf("Rounds completed: %d\n", len(result.Rounds))
}
