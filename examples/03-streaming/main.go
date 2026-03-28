// examples/03-streaming/main.go
//
// Streaming events example for Axon agents.
//
// This example demonstrates how to use agent.Stream() and handle streaming events:
//   - A "lookup" tool that returns information about a topic
//   - A MockLLM scripted to call "lookup" in round 0, then return a text answer in round 1
//   - agent.Stream() to start non-blocking execution
//   - Iterating over streamResult.Events() with a type switch to handle each event type:
//     TextDeltaEvent  — text produced by the LLM
//     ToolStartEvent  — a tool has started executing
//     ToolEndEvent    — a tool has finished executing
//   - streamResult.Result() to block until the stream is done and retrieve the final result
//
// Run with:
//
//	cd examples && go run ./03-streaming/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/axonframework/axon/kernel"
	axontest "github.com/axonframework/axon/testing"
)

// LookupParams holds parameters for the "lookup" tool.
type LookupParams struct {
	Topic string `json:"topic" description:"The topic to look up"`
}

func main() {
	// Define a "lookup" tool that returns a short fact about a topic.
	lookupTool := kernel.NewTool[LookupParams, string](
		"lookup",
		"Look up information about a topic and return a brief summary.",
		func(ctx context.Context, p LookupParams) (string, error) {
			facts := map[string]string{
				"golang":  "Go is a statically typed, compiled language designed at Google.",
				"axon":    "Axon is a lightweight agentic framework for Go.",
				"default": "No information found for that topic.",
			}
			if fact, ok := facts[p.Topic]; ok {
				return fact, nil
			}
			return facts["default"], nil
		},
	)

	// Script the mock LLM:
	//   Round 0 → call "lookup" with {topic: "axon"}
	//   Round 1 → return the final text answer using the tool result
	llm := axontest.NewMockLLM().
		OnRound(0).RespondWithToolCall("lookup", map[string]any{"topic": "axon"}).
		OnRound(1).RespondWithText("Based on the lookup: Axon is a lightweight agentic framework for Go. It provides a clean API for building agents that can call tools and process responses.")

	// Build the agent with the lookup tool.
	agent := kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a helpful assistant that looks up information on request."),
		kernel.WithTools(lookupTool),
	)

	// Start streaming execution. Stream() returns immediately; execution runs in a goroutine.
	streamResult, err := agent.Stream(context.Background(), "Tell me about Axon.")
	if err != nil {
		log.Fatalf("agent.Stream failed: %v", err)
	}

	fmt.Println("=== Streaming events ===")

	// Consume all events from the channel. The channel is closed when the agent loop finishes.
	for event := range streamResult.Events() {
		switch e := event.(type) {
		case kernel.ToolStartEvent:
			paramsJSON, _ := json.Marshal(e.Params)
			fmt.Printf("[ToolStart]  tool=%q params=%s\n", e.ToolName, paramsJSON)

		case kernel.ToolEndEvent:
			resultJSON, _ := json.Marshal(e.Result)
			if e.Error != nil {
				fmt.Printf("[ToolEnd]    tool=%q error=%v\n", e.ToolName, e.Error)
			} else {
				fmt.Printf("[ToolEnd]    tool=%q result=%s\n", e.ToolName, resultJSON)
			}

		case kernel.TextDeltaEvent:
			fmt.Printf("[TextDelta]  text=%q\n", e.Text)

		default:
			fmt.Printf("[Unknown]    event=%T\n", event)
		}
	}

	// Result() blocks until the stream is fully complete, then returns the final *Result.
	result := streamResult.Result()

	fmt.Println("\n=== Final result ===")
	fmt.Println("Agent response:", result.Text)
	fmt.Printf("Rounds completed: %d\n", len(result.Rounds))
}
