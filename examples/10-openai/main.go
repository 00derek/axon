// examples/10-openai/main.go
//
// Minimal OpenAI example.
//
// Sends a single prompt to GPT via the Chat Completions API. Requires the
// OPENAI_API_KEY environment variable.
//
// Run with:
//
//	cd examples && OPENAI_API_KEY=... go run ./10-openai/
package main

import (
	"context"
	"fmt"
	"log"

	sdk "github.com/openai/openai-go/v3"

	"github.com/axonframework/axon/kernel"
	"github.com/axonframework/axon/providers/openai"
)

func main() {
	// NewClient reads OPENAI_API_KEY from the environment by default.
	client := sdk.NewClient()

	llm := openai.New(&client, string(sdk.ChatModelGPT5_2))

	agent := kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a concise assistant."),
	)

	result, err := agent.Run(context.Background(), "Say hello in one short sentence.")
	if err != nil {
		log.Fatalf("agent.Run failed: %v", err)
	}

	fmt.Println("GPT:", result.Text)
	fmt.Printf("Tokens: in=%d out=%d total=%d\n",
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.TotalTokens)
}
