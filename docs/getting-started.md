# Getting Started with Axon

This guide walks you through building your first AI agent with Axon.

## Prerequisites

- Go 1.25 or later
- An LLM provider API key (Google Gemini shown here), OR use the built-in MockLLM for learning

## Installation

Install the packages you need:

```bash
# Core (required)
go get github.com/axonframework/axon/kernel

# Pick a provider
go get github.com/axonframework/axon/providers/google

# Optional packages (add as needed)
go get github.com/axonframework/axon/middleware
go get github.com/axonframework/axon/workflow
go get github.com/axonframework/axon/testing
go get github.com/axonframework/axon/interfaces
```

## Your First Agent

The simplest possible agent — no tools, just conversation:

```go
// See: examples/01-minimal/main.go
package main

import (
	"context"
	"fmt"

	"github.com/axonframework/axon/kernel"
	axontest "github.com/axonframework/axon/testing"
)

func main() {
	// MockLLM lets you run examples without an API key
	llm := axontest.NewMockLLM()
	llm.OnRound(0).RespondWithText("Hello! I'm your Axon agent. How can I help?")

	agent := kernel.NewAgent(
		kernel.WithModel(llm),
		kernel.WithSystemPrompt("You are a helpful assistant."),
	)

	result, err := agent.Run(context.Background(), "Hi there!")
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Text)
	// Output: Hello! I'm your Axon agent. How can I help?
}
```

**What's happening:**
1. `kernel.NewAgent()` creates an agent with functional options
2. `WithModel()` provides the LLM (here a mock; in production, use a real provider)
3. `WithSystemPrompt()` sets the system message
4. `agent.Run()` sends user input and returns the complete response

## Adding Tools

Tools let your agent take actions. Define them with typed parameters:

```go
// See: examples/02-tools/main.go

type AddParams struct {
	A float64 `json:"a" description:"First number"`
	B float64 `json:"b" description:"Second number"`
}

addTool := kernel.NewTool("add", "Add two numbers together",
	func(ctx context.Context, p AddParams) (float64, error) {
		return p.A + p.B, nil
	},
)
```

`NewTool[P, R]()` automatically:
- Generates a JSON Schema from your struct tags
- Deserializes LLM parameters into your typed struct
- Serializes the return value for the LLM to read

Wire the tool into an agent:

```go
agent := kernel.NewAgent(
	kernel.WithModel(llm),
	kernel.WithTools(addTool, multiplyTool),
	kernel.WithSystemPrompt("You are a calculator. Use tools to compute answers."),
)

result, err := agent.Run(ctx, "What is 2 + 3?")
```

The agent loop automatically handles tool calls: the LLM requests a tool, Axon executes it,
feeds the result back, and the LLM generates a final text response.

## Using a Real LLM Provider

Replace the mock with Google Gemini:

```go
import (
	"github.com/axonframework/axon/providers/google"
	"google.golang.org/genai"
)

client, err := genai.NewClient(context.Background(), nil) // uses GOOGLE_API_KEY env var
if err != nil {
	panic(err)
}
llm := google.New(client, "gemini-2.0-flash")

agent := kernel.NewAgent(
	kernel.WithModel(llm),
	// ...
)
```

## Next Steps

- [Tools Guide](tools.md) — schema tags, nested structs, Guided responses
- [Agents Guide](agents.md) — hooks, stop conditions, AgentContext
- [Streaming Guide](streaming.md) — real-time event streaming
- [Middleware Guide](middleware.md) — retry, logging, cost tracking, model routing
- [Workflow Guide](workflow.md) — orchestrate multi-agent pipelines
- [Testing Guide](testing.md) — mock, assert, evaluate agent behavior
- [Examples](../examples/) — runnable code for every feature
