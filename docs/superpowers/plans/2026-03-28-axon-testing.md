# Axon Testing Package Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `axontest` package -- the testing and evaluation layer for the Axon agentic framework: MockLLM with round-based scripting, Run() with options, chainable assertions (tool + response + structural), ScoreCard with judge-LLM evaluation, and batch Eval().

**Architecture:** Single Go package `testing/` (import as `axontest`) depending only on `kernel/`. The package provides test helpers that wrap `kernel.Agent.Run()` and inspect `kernel.Result` to drive assertions. A `MockLLM` implementing `kernel.LLM` lets tests script LLM behavior per round. ScoreCard constructs prompts for a judge LLM and parses structured JSON output. All types build bottom-up: MockLLM -> Run/TestResult -> Assertions -> ScoreCard -> Eval.

**Tech Stack:** Go 1.25, depends only on `kernel/` (which is stdlib-only)

**Source spec:** `docs/superpowers/specs/2026-03-28-axon-framework-design.md`, Sections 5.1-5.6

---

## File Structure

```
testing/
├── go.mod              # module github.com/axonframework/axon/testing
├── mock.go             # MockLLM, MockResponseBuilder, NewMockLLM, OnRound
├── mock_test.go
├── run.go              # Run(), RunOption, TestResult, WithHistory, MockTool, WithMockLLM
├── run_test.go
├── assert.go           # ToolAssertion, ResponseAssertion, ExpectTool, ExpectResponse, ExpectRounds
├── assert_test.go
├── scorecard.go        # ScoreCard, Criterion, ScoreResult, Evaluate
├── scorecard_test.go
├── eval.go             # Eval(), Case, Expectation
├── eval_test.go
```

## Kernel Types Referenced (not yet implemented, but fully designed)

The testing package depends on these kernel types. Reference them freely -- they will exist when kernel is built.

```go
// kernel.LLM -- the interface MockLLM implements
type LLM interface {
    Generate(ctx context.Context, params GenerateParams) (Response, error)
    GenerateStream(ctx context.Context, params GenerateParams) (Stream, error)
    Model() string
}

// kernel.Agent -- what Run() wraps
type Agent struct { /* ... */ }
func NewAgent(opts ...AgentOption) *Agent
func (a *Agent) Run(ctx context.Context, input string) (*Result, error)

// kernel.Result -- what TestResult wraps
type Result struct {
    Text   string
    Rounds []RoundResult
    Usage  Usage
}
type RoundResult struct {
    Response  Response
    ToolCalls []ToolCallResult
}
type ToolCallResult struct {
    Name   string
    Params json.RawMessage
    Result any
    Error  error
}

// kernel.Response, kernel.ToolCall, kernel.Message, etc.
type Response struct {
    Text         string
    ToolCalls    []ToolCall
    Usage        Usage
    FinishReason string
}
type ToolCall struct {
    ID     string
    Name   string
    Params json.RawMessage
}
type Message struct {
    Role     Role
    Content  []ContentPart
    Metadata map[string]any
}
type GenerateParams struct {
    Messages []Message
    Tools    []Tool
    Options  GenerateOptions
}
type GenerateOptions struct {
    OutputSchema *Schema
}
type Schema struct {
    Type        string
    Description string
    Properties  map[string]Schema
    Required    []string
    Items       *Schema
    Enum        []string
    Minimum     *float64
    Maximum     *float64
}
```

---

### Task 1: Initialize Go module and MockLLM

**Files:**
- Create: `testing/go.mod`
- Create: `testing/mock.go`
- Create: `testing/mock_test.go`

MockLLM is foundational -- every other file depends on it for testing. It implements `kernel.LLM` and returns scripted responses by round number.

- [ ] **Step 1: Write the test file**

```go
// testing/mock_test.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestNewMockLLMModel(t *testing.T) {
	m := NewMockLLM()
	if m.Model() != "mock" {
		t.Errorf("expected model %q, got %q", "mock", m.Model())
	}
}

func TestMockLLMTextResponse(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithText("Hello from round 0").
		OnRound(1).RespondWithText("Hello from round 1")

	// Round 0
	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from round 0" {
		t.Errorf("round 0: expected %q, got %q", "Hello from round 0", resp.Text)
	}

	// Round 1
	resp, err = m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from round 1" {
		t.Errorf("round 1: expected %q, got %q", "Hello from round 1", resp.Text)
	}
}

func TestMockLLMToolCallResponse(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "thai"})

	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool name %q, got %q", "search", resp.ToolCalls[0].Name)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason %q, got %q", "tool_calls", resp.FinishReason)
	}

	var params map[string]any
	if err := json.Unmarshal(resp.ToolCalls[0].Params, &params); err != nil {
		t.Fatalf("failed to parse params: %v", err)
	}
	if params["query"] != "thai" {
		t.Errorf("expected query %q, got %v", "thai", params["query"])
	}
}

func TestMockLLMMultipleToolCalls(t *testing.T) {
	calls := []kernel.ToolCall{
		{ID: "c1", Name: "search", Params: json.RawMessage(`{"q":"a"}`)},
		{ID: "c2", Name: "reserve", Params: json.RawMessage(`{"id":"r1"}`)},
	}
	m := NewMockLLM().
		OnRound(0).RespondWithToolCalls(calls...)

	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" || resp.ToolCalls[1].Name != "reserve" {
		t.Errorf("unexpected tool names: %v, %v", resp.ToolCalls[0].Name, resp.ToolCalls[1].Name)
	}
}

func TestMockLLMErrorResponse(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithError(fmt.Errorf("rate limit exceeded"))

	_, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "rate limit exceeded" {
		t.Errorf("expected error %q, got %q", "rate limit exceeded", err.Error())
	}
}

func TestMockLLMUnconfiguredRound(t *testing.T) {
	m := NewMockLLM().
		OnRound(0).RespondWithText("only round 0")

	// Round 0 works
	resp, err := m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "only round 0" {
		t.Errorf("expected %q, got %q", "only round 0", resp.Text)
	}

	// Round 1 has no configured response -- should return empty text response
	resp, err = m.Generate(context.Background(), kernel.GenerateParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "" {
		t.Errorf("expected empty text for unconfigured round, got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason %q, got %q", "stop", resp.FinishReason)
	}
}

func TestMockLLMChainingReturnsParent(t *testing.T) {
	// Verify the builder chain returns *MockLLM so you can keep calling OnRound
	m := NewMockLLM()
	result := m.OnRound(0).RespondWithText("a")
	if result != m {
		t.Error("RespondWithText should return the parent MockLLM for chaining")
	}

	result = m.OnRound(1).RespondWithToolCall("search", map[string]any{})
	if result != m {
		t.Error("RespondWithToolCall should return the parent MockLLM for chaining")
	}

	result = m.OnRound(2).RespondWithError(fmt.Errorf("err"))
	if result != m {
		t.Error("RespondWithError should return the parent MockLLM for chaining")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v`
Expected: FAIL (package does not exist yet)

- [ ] **Step 3: Create go.mod**

```bash
mkdir -p /Users/derek/repo/axons/testing
cd /Users/derek/repo/axons/testing && go mod init github.com/axonframework/axon/testing
```

Then edit `testing/go.mod` to add the kernel dependency with a replace directive:

```
module github.com/axonframework/axon/testing

go 1.25

require github.com/axonframework/axon/kernel v0.0.0

replace github.com/axonframework/axon/kernel => ../kernel
```

- [ ] **Step 4: Write the implementation**

```go
// testing/mock.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/axonframework/axon/kernel"
)

// MockLLM implements kernel.LLM with scripted responses per round.
// Use NewMockLLM() and OnRound() to configure expected behavior.
type MockLLM struct {
	mu        sync.Mutex
	responses map[int]mockResponse
	callCount int
}

type mockResponse struct {
	resp kernel.Response
	err  error
}

// NewMockLLM creates a new MockLLM with no configured responses.
func NewMockLLM() *MockLLM {
	return &MockLLM{
		responses: make(map[int]mockResponse),
	}
}

// Model returns "mock".
func (m *MockLLM) Model() string { return "mock" }

// Generate returns the scripted response for the current round.
// If no response is configured for the round, returns an empty text response with "stop".
func (m *MockLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	round := m.callCount
	m.callCount++

	if mr, ok := m.responses[round]; ok {
		if mr.err != nil {
			return kernel.Response{}, mr.err
		}
		return mr.resp, nil
	}

	// Unconfigured round: return empty stop response
	return kernel.Response{FinishReason: "stop"}, nil
}

// GenerateStream is not supported by MockLLM. Use Generate instead.
func (m *MockLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, fmt.Errorf("MockLLM does not support streaming; use Generate")
}

// OnRound begins configuring the response for a specific round number (0-indexed).
func (m *MockLLM) OnRound(n int) *MockResponseBuilder {
	return &MockResponseBuilder{mock: m, round: n}
}

// MockResponseBuilder configures a single round's response. All terminal methods
// return the parent *MockLLM for fluent chaining.
type MockResponseBuilder struct {
	mock  *MockLLM
	round int
}

// RespondWithText configures this round to return a text response.
func (b *MockResponseBuilder) RespondWithText(text string) *MockLLM {
	b.mock.responses[b.round] = mockResponse{
		resp: kernel.Response{
			Text:         text,
			FinishReason: "stop",
		},
	}
	return b.mock
}

// RespondWithToolCall configures this round to return a single tool call.
// Params are JSON-marshaled from the provided map.
func (b *MockResponseBuilder) RespondWithToolCall(name string, params map[string]any) *MockLLM {
	data, _ := json.Marshal(params)
	b.mock.responses[b.round] = mockResponse{
		resp: kernel.Response{
			ToolCalls: []kernel.ToolCall{{
				ID:     fmt.Sprintf("mock-call-%d-0", b.round),
				Name:   name,
				Params: data,
			}},
			FinishReason: "tool_calls",
		},
	}
	return b.mock
}

// RespondWithToolCalls configures this round to return multiple tool calls.
func (b *MockResponseBuilder) RespondWithToolCalls(calls ...kernel.ToolCall) *MockLLM {
	b.mock.responses[b.round] = mockResponse{
		resp: kernel.Response{
			ToolCalls:    calls,
			FinishReason: "tool_calls",
		},
	}
	return b.mock
}

// RespondWithError configures this round to return an error.
func (b *MockResponseBuilder) RespondWithError(err error) *MockLLM {
	b.mock.responses[b.round] = mockResponse{err: err}
	return b.mock
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v`
Expected: PASS (all 7 tests)

- [ ] **Step 6: Commit**

```bash
git add testing/
git commit -m "feat(testing): add MockLLM with round-based response scripting"
```

---

### Task 2: Run(), RunOption, and TestResult

**Files:**
- Create: `testing/run.go`
- Create: `testing/run_test.go`

Run() wraps `kernel.Agent.Run()` and returns a `*TestResult` that captures the kernel Result for assertions. RunOptions let callers inject history, mock tools, and mock LLMs.

- [ ] **Step 1: Write the test file**

```go
// testing/run_test.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// fakeTool is a minimal kernel.Tool for testing Run().
type fakeTool struct {
	name   string
	called bool
	result any
}

func (f *fakeTool) Name() string            { return f.name }
func (f *fakeTool) Description() string     { return "fake " + f.name }
func (f *fakeTool) Schema() kernel.Schema   { return kernel.Schema{Type: "object"} }
func (f *fakeTool) Execute(ctx context.Context, params json.RawMessage) (any, error) {
	f.called = true
	return f.result, nil
}

func TestRunTextOnly(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello!")

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithSystemPrompt("You are helpful"),
	)

	result := Run(t, agent, "Hi")

	if result.Text() != "Hello!" {
		t.Errorf("expected %q, got %q", "Hello!", result.Text())
	}
	if result.RoundCount() != 1 {
		t.Errorf("expected 1 round, got %d", result.RoundCount())
	}
}

func TestRunWithToolCall(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "thai"}).
		OnRound(1).RespondWithText("Found Thai Basil!")

	searchTool := &fakeTool{name: "search", result: map[string]any{"name": "Thai Basil"}}

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithTools(searchTool),
	)

	result := Run(t, agent, "Find thai food")

	if result.Text() != "Found Thai Basil!" {
		t.Errorf("expected %q, got %q", "Found Thai Basil!", result.Text())
	}
	if result.RoundCount() != 2 {
		t.Errorf("expected 2 rounds, got %d", result.RoundCount())
	}
}

func TestRunWithMockTool(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "sushi"}).
		OnRound(1).RespondWithText("Found Sushi Place!")

	// Use a real tool that the agent registers, but MockTool overrides its execution
	realTool := &fakeTool{name: "search", result: "should not see this"}

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithTools(realTool),
	)

	result := Run(t, agent, "Find sushi",
		MockTool("search", map[string]any{"name": "Sushi Place"}),
	)

	if result.Text() != "Found Sushi Place!" {
		t.Errorf("expected %q, got %q", "Found Sushi Place!", result.Text())
	}
}

func TestRunWithHistory(t *testing.T) {
	// Verify that history messages are passed to the agent.
	// The mock LLM captures the GenerateParams, so we can check messages.
	var capturedParams kernel.GenerateParams

	captureLLM := &capturingMockLLM{
		response: kernel.Response{Text: "Got it", FinishReason: "stop"},
	}

	agent := kernel.NewAgent(
		kernel.WithModel(captureLLM),
	)

	history := []kernel.Message{
		kernel.UserMsg("previous question"),
		kernel.AssistantMsg("previous answer"),
	}

	Run(t, agent, "Follow up",
		WithHistory(history...),
	)

	capturedParams = captureLLM.lastParams
	// Should have: system prompt (if any) + history messages + user input
	// At minimum: history (2) + current user msg (1) = 3 messages
	foundPrevious := false
	for _, msg := range capturedParams.Messages {
		if msg.TextContent() == "previous question" {
			foundPrevious = true
		}
	}
	if !foundPrevious {
		t.Error("expected history messages to be included in LLM call")
	}
}

func TestRunWithMockLLMOption(t *testing.T) {
	// WithMockLLM overrides the agent's model entirely.
	originalLLM := NewMockLLM().
		OnRound(0).RespondWithText("from original")

	overrideLLM := NewMockLLM().
		OnRound(0).RespondWithText("from override")

	agent := kernel.NewAgent(
		kernel.WithModel(originalLLM),
	)

	result := Run(t, agent, "test",
		WithMockLLM(overrideLLM),
	)

	if result.Text() != "from override" {
		t.Errorf("expected %q, got %q", "from override", result.Text())
	}
}

func TestRunResultHasKernelResult(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello!")

	agent := kernel.NewAgent(kernel.WithModel(mock))
	result := Run(t, agent, "test")

	// TestResult should expose the underlying kernel.Result
	if result.Result == nil {
		t.Fatal("expected non-nil underlying kernel.Result")
	}
	if result.Result.Text != "Hello!" {
		t.Errorf("expected underlying text %q, got %q", "Hello!", result.Result.Text)
	}
}

// capturingMockLLM records the last GenerateParams it received.
type capturingMockLLM struct {
	response   kernel.Response
	lastParams kernel.GenerateParams
}

func (c *capturingMockLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	c.lastParams = params
	return c.response, nil
}

func (c *capturingMockLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (c *capturingMockLLM) Model() string { return "capturing-mock" }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestRun"`
Expected: FAIL (Run not defined)

- [ ] **Step 3: Write the implementation**

```go
// testing/run.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// TestResult wraps a kernel.Result with assertion helpers.
type TestResult struct {
	Result *kernel.Result
	t      *testing.T
}

// Text returns the final text response.
func (r *TestResult) Text() string {
	if r.Result == nil {
		return ""
	}
	return r.Result.Text
}

// RoundCount returns the number of rounds executed.
func (r *TestResult) RoundCount() int {
	if r.Result == nil {
		return 0
	}
	return len(r.Result.Rounds)
}

// RunOption configures a test Run() call.
type RunOption func(*runConfig)

type runConfig struct {
	history   []kernel.Message
	mockTools map[string]any // tool name -> canned response
	mockLLM   kernel.LLM
}

// WithHistory prepends conversation history before the user input.
func WithHistory(msgs ...kernel.Message) RunOption {
	return func(c *runConfig) {
		c.history = append(c.history, msgs...)
	}
}

// MockTool replaces a tool's execution with a canned response.
// The tool must already be registered on the agent. MockTool intercepts
// the tool call via an OnToolStart hook and replaces the tool implementation.
func MockTool(name string, response any) RunOption {
	return func(c *runConfig) {
		if c.mockTools == nil {
			c.mockTools = make(map[string]any)
		}
		c.mockTools[name] = response
	}
}

// WithMockLLM overrides the agent's LLM with the provided one.
func WithMockLLM(llm kernel.LLM) RunOption {
	return func(c *runConfig) {
		c.mockLLM = llm
	}
}

// Run executes an agent with the given input and options, returning a TestResult
// for assertion chaining. Calls t.Fatal on agent execution errors.
func Run(t *testing.T, agent *kernel.Agent, input string, opts ...RunOption) *TestResult {
	t.Helper()

	cfg := &runConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build agent options to create a test-configured clone.
	// We reconstruct the agent with overrides applied.
	var agentOpts []kernel.AgentOption

	// If a mock LLM is provided, use it. Otherwise re-use the agent's model.
	if cfg.mockLLM != nil {
		agentOpts = append(agentOpts, kernel.WithModel(cfg.mockLLM))
	}

	// Inject history via OnStart hook
	if len(cfg.history) > 0 {
		agentOpts = append(agentOpts, kernel.OnStart(func(ctx *kernel.TurnContext) {
			// Prepend history before the user message that Run already added.
			// AgentContext.Messages at this point: [system_prompt?, user_msg]
			// We want: [system_prompt?, ...history, user_msg]
			existing := ctx.AgentCtx.Messages
			var systemMsgs []kernel.Message
			var rest []kernel.Message
			for _, m := range existing {
				if m.Role == kernel.RoleSystem {
					systemMsgs = append(systemMsgs, m)
				} else {
					rest = append(rest, m)
				}
			}
			var reordered []kernel.Message
			reordered = append(reordered, systemMsgs...)
			reordered = append(reordered, cfg.history...)
			reordered = append(reordered, rest...)
			ctx.AgentCtx.Messages = reordered
		}))
	}

	// Inject mock tools by replacing the registered tool with a mock wrapper
	if len(cfg.mockTools) > 0 {
		agentOpts = append(agentOpts, kernel.OnStart(func(ctx *kernel.TurnContext) {
			allTools := ctx.AgentCtx.AllTools()
			var replaced []kernel.Tool
			for _, tool := range allTools {
				if mockResp, ok := cfg.mockTools[tool.Name()]; ok {
					replaced = append(replaced, &mockedTool{
						name:     tool.Name(),
						desc:     tool.Description(),
						schema:   tool.Schema(),
						response: mockResp,
					})
				} else {
					replaced = append(replaced, tool)
				}
			}
			// Replace tools on the context by clearing and re-adding
			ctx.AgentCtx.ReplaceTools(replaced)
		}))
	}

	// Build the test agent using CloneWith which copies base config + appends options.
	testAgent := agent.CloneWith(agentOpts...)

	result, err := testAgent.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("axontest.Run failed: %v", err)
	}

	return &TestResult{
		Result: result,
		t:      t,
	}
}

// mockedTool wraps a tool to always return a fixed response.
type mockedTool struct {
	name     string
	desc     string
	schema   kernel.Schema
	response any
}

func (m *mockedTool) Name() string          { return m.name }
func (m *mockedTool) Description() string   { return m.desc }
func (m *mockedTool) Schema() kernel.Schema { return m.schema }
func (m *mockedTool) Execute(ctx context.Context, params json.RawMessage) (any, error) {
	return m.response, nil
}
```

**Note on kernel API requirements:** This implementation assumes two methods that must exist on `kernel.Agent` and `kernel.AgentContext`:

1. `agent.CloneWith(opts ...AgentOption) *Agent` -- creates a copy of the agent with additional options applied. This is needed so `Run()` can override the LLM and inject hooks without mutating the original agent. **This must be added to the kernel plan as a small addition to agent.go.**

2. `agentCtx.ReplaceTools(tools []Tool)` -- replaces the entire tool set. Needed for MockTool to swap implementations. **This must be added to kernel/context.go.**

These are minimal additions (3-5 lines each) to the kernel. Document them here so the kernel implementer knows:

```go
// kernel/agent.go -- add this method
func (a *Agent) CloneWith(opts ...AgentOption) *Agent {
    clone := *a
    clone.tools = append([]Tool(nil), a.tools...)
    clone.hooks = a.hooks // hooks are append-only, safe to share base
    for _, opt := range opts {
        opt(&clone)
    }
    return &clone
}

// kernel/context.go -- add this method
func (c *AgentContext) ReplaceTools(tools []Tool) {
    c.tools = tools
    c.disabled = make(map[string]bool)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestRun"`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Commit**

```bash
git add testing/run.go testing/run_test.go
git commit -m "feat(testing): add Run() with TestResult, WithHistory, MockTool, WithMockLLM"
```

---

### Task 3: Tool assertions (ExpectTool, ToolAssertion)

**Files:**
- Create: `testing/assert.go`
- Create: `testing/assert_test.go`

ToolAssertion provides chainable assertions about tool calls within the TestResult. It scans all RoundResult.ToolCalls for matching tool names.

- [ ] **Step 1: Write the test file**

```go
// testing/assert_test.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// --- helpers for building TestResults in assertion tests ---

func makeTestResult(t *testing.T, rounds []kernel.RoundResult, text string) *TestResult {
	return &TestResult{
		Result: &kernel.Result{
			Text:   text,
			Rounds: rounds,
		},
		t: t,
	}
}

func makeToolCallResult(name string, params map[string]any) kernel.ToolCallResult {
	data, _ := json.Marshal(params)
	return kernel.ToolCallResult{
		Name:   name,
		Params: data,
		Result: "ok",
	}
}

// --- ExpectTool tests ---

func TestExpectToolCalled(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
	}, "done")

	result.ExpectTool("search").Called(t)
}

func TestExpectToolNotCalled(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
	}, "done")

	result.ExpectTool("reserve").NotCalled(t)
}

func TestExpectToolCalledTimes(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "sushi"}),
		}},
	}, "done")

	result.ExpectTool("search").CalledTimes(t, 2)
}

func TestExpectToolWithParam(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai", "location": "SF"}),
		}},
	}, "done")

	result.ExpectTool("search").
		WithParam("query", "thai").
		WithParam("location", "SF").
		Called(t)
}

func TestExpectToolWithParamNoMatch(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "thai"}),
		}},
	}, "done")

	// WithParam filters calls -- "sushi" should not match, so Called should fail.
	// We use a sub-test to capture the failure without failing the parent.
	subT := &testing.T{}
	result.ExpectTool("search").
		WithParam("query", "sushi").
		Called(subT)
	// subT would have failed, but we can't inspect it easily.
	// Instead, test the count directly: should be 0 matching calls.
	assertion := result.ExpectTool("search").WithParam("query", "sushi")
	if len(assertion.matchingCalls()) != 0 {
		t.Error("expected 0 matching calls for query=sushi")
	}
}

func TestExpectToolWithParamMatchLLMJudge(t *testing.T) {
	// WithParamMatch uses a judge LLM to evaluate criteria.
	// We use a MockLLM that always says "yes" as the judge.
	judgeLLM := NewMockLLM().
		OnRound(0).RespondWithText(`{"reasoning":"The query is about food","condition_met":true}`)

	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "best thai restaurants near me"}),
		}},
	}, "done")

	result.ExpectTool("search").
		WithParamMatch("query", judgeLLM, "The query is about food").
		Called(t)
}

func TestExpectToolCalledMultipleRounds(t *testing.T) {
	// Tool called in round 0 and round 2, not in round 1
	result := makeTestResult(t, []kernel.RoundResult{
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "a"}),
		}},
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("reserve", map[string]any{"id": "r1"}),
		}},
		{ToolCalls: []kernel.ToolCallResult{
			makeToolCallResult("search", map[string]any{"query": "b"}),
		}},
	}, "done")

	result.ExpectTool("search").CalledTimes(t, 2)
	result.ExpectTool("reserve").CalledTimes(t, 1)
}

// --- ExpectResponse tests ---

func TestExpectResponseContains(t *testing.T) {
	result := makeTestResult(t, nil, "I found Thai Basil restaurant for you!")
	result.ExpectResponse().Contains(t, "Thai Basil")
}

func TestExpectResponseNotContains(t *testing.T) {
	result := makeTestResult(t, nil, "I found Thai Basil restaurant for you!")
	result.ExpectResponse().NotContains(t, "Sushi")
}

func TestExpectResponseSatisfiesLLMJudge(t *testing.T) {
	judgeLLM := NewMockLLM().
		OnRound(0).RespondWithText(`{"reasoning":"The response mentions a restaurant","condition_met":true}`)

	result := makeTestResult(t, nil, "I found Thai Basil restaurant for you!")
	result.ExpectResponse().Satisfies(t, judgeLLM, "The response mentions a specific restaurant name")
}

// --- ExpectRounds tests ---

func TestExpectRounds(t *testing.T) {
	result := makeTestResult(t, []kernel.RoundResult{
		{}, {}, {},
	}, "done")

	result.ExpectRounds(t, 3)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestExpect"`
Expected: FAIL (ExpectTool, ExpectResponse, ExpectRounds not defined)

- [ ] **Step 3: Write the implementation**

```go
// testing/assert.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// --- Tool Assertions ---

// ToolAssertion provides chainable assertions about tool calls within a TestResult.
type ToolAssertion struct {
	result   *TestResult
	toolName string
	filters  []toolFilter
}

type toolFilter func(kernel.ToolCallResult) bool

// ExpectTool begins a tool assertion chain for the named tool.
func (r *TestResult) ExpectTool(name string) *ToolAssertion {
	return &ToolAssertion{
		result:   r,
		toolName: name,
	}
}

// matchingCalls returns all tool calls that match the tool name and all filters.
func (a *ToolAssertion) matchingCalls() []kernel.ToolCallResult {
	var matches []kernel.ToolCallResult
	if a.result.Result == nil {
		return matches
	}
	for _, round := range a.result.Result.Rounds {
		for _, tc := range round.ToolCalls {
			if tc.Name != a.toolName {
				continue
			}
			allMatch := true
			for _, f := range a.filters {
				if !f(tc) {
					allMatch = false
					break
				}
			}
			if allMatch {
				matches = append(matches, tc)
			}
		}
	}
	return matches
}

// Called asserts the tool was called at least once (matching all filters).
func (a *ToolAssertion) Called(t *testing.T) {
	t.Helper()
	if len(a.matchingCalls()) == 0 {
		t.Errorf("expected tool %q to be called, but it was not", a.toolName)
	}
}

// NotCalled asserts the tool was never called (matching all filters).
func (a *ToolAssertion) NotCalled(t *testing.T) {
	t.Helper()
	calls := a.matchingCalls()
	if len(calls) > 0 {
		t.Errorf("expected tool %q to not be called, but it was called %d time(s)", a.toolName, len(calls))
	}
}

// CalledTimes asserts the tool was called exactly n times (matching all filters).
func (a *ToolAssertion) CalledTimes(t *testing.T, n int) {
	t.Helper()
	calls := a.matchingCalls()
	if len(calls) != n {
		t.Errorf("expected tool %q to be called %d time(s), got %d", a.toolName, n, len(calls))
	}
}

// WithParam adds a filter: only match tool calls where the parameter key equals the value.
// Value is compared via JSON serialization for type-safe equality.
// Returns the same ToolAssertion for chaining.
func (a *ToolAssertion) WithParam(key string, value any) *ToolAssertion {
	expectedJSON, _ := json.Marshal(value)
	a.filters = append(a.filters, func(tc kernel.ToolCallResult) bool {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(tc.Params, &params); err != nil {
			return false
		}
		actual, ok := params[key]
		if !ok {
			return false
		}
		return string(actual) == string(expectedJSON)
	})
	return a
}

// WithParamMatch adds a filter: uses a judge LLM to evaluate whether the parameter value
// satisfies the given criteria. The judge is called with a prompt containing the parameter
// value and the criteria, and must respond with JSON: {"reasoning":"...","condition_met":bool}.
// Returns the same ToolAssertion for chaining.
func (a *ToolAssertion) WithParamMatch(key string, judge kernel.LLM, criteria string) *ToolAssertion {
	a.filters = append(a.filters, func(tc kernel.ToolCallResult) bool {
		var params map[string]json.RawMessage
		if err := json.Unmarshal(tc.Params, &params); err != nil {
			return false
		}
		actual, ok := params[key]
		if !ok {
			return false
		}

		prompt := fmt.Sprintf(
			"Evaluate whether this value satisfies the criteria.\n\nValue: %s\nCriteria: %s\n\nRespond with JSON: {\"reasoning\": \"...\", \"condition_met\": true/false}",
			string(actual), criteria,
		)

		resp, err := judge.Generate(context.Background(), kernel.GenerateParams{
			Messages: []kernel.Message{kernel.UserMsg(prompt)},
		})
		if err != nil {
			return false
		}

		var verdict struct {
			Reasoning    string `json:"reasoning"`
			ConditionMet bool   `json:"condition_met"`
		}
		if err := json.Unmarshal([]byte(resp.Text), &verdict); err != nil {
			return false
		}
		return verdict.ConditionMet
	})
	return a
}

// --- Response Assertions ---

// ResponseAssertion provides assertions about the final text response.
type ResponseAssertion struct {
	result *TestResult
}

// ExpectResponse begins a response assertion chain.
func (r *TestResult) ExpectResponse() *ResponseAssertion {
	return &ResponseAssertion{result: r}
}

// Contains asserts the response text contains the given substring.
func (a *ResponseAssertion) Contains(t *testing.T, substring string) {
	t.Helper()
	if !strings.Contains(a.result.Text(), substring) {
		t.Errorf("expected response to contain %q, got %q", substring, a.result.Text())
	}
}

// NotContains asserts the response text does not contain the given substring.
func (a *ResponseAssertion) NotContains(t *testing.T, substring string) {
	t.Helper()
	if strings.Contains(a.result.Text(), substring) {
		t.Errorf("expected response to not contain %q, got %q", substring, a.result.Text())
	}
}

// Satisfies asserts the response satisfies the given criteria according to a judge LLM.
// The judge is called with a prompt containing the response and criteria, and must respond
// with JSON: {"reasoning":"...","condition_met":bool}.
func (a *ResponseAssertion) Satisfies(t *testing.T, judge kernel.LLM, criteria string) {
	t.Helper()

	prompt := fmt.Sprintf(
		"Evaluate whether this response satisfies the criteria.\n\nResponse: %s\nCriteria: %s\n\nRespond with JSON: {\"reasoning\": \"...\", \"condition_met\": true/false}",
		a.result.Text(), criteria,
	)

	resp, err := judge.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg(prompt)},
	})
	if err != nil {
		t.Fatalf("judge LLM failed: %v", err)
	}

	var verdict struct {
		Reasoning    string `json:"reasoning"`
		ConditionMet bool   `json:"condition_met"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &verdict); err != nil {
		t.Fatalf("failed to parse judge response %q: %v", resp.Text, err)
	}

	if !verdict.ConditionMet {
		t.Errorf("response did not satisfy criteria %q. Judge reasoning: %s", criteria, verdict.Reasoning)
	}
}

// --- Structural Assertions ---

// ExpectRounds asserts the agent executed exactly n rounds.
func (r *TestResult) ExpectRounds(t *testing.T, n int) {
	t.Helper()
	actual := r.RoundCount()
	if actual != n {
		t.Errorf("expected %d rounds, got %d", n, actual)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestExpect"`
Expected: PASS (all 11 tests)

- [ ] **Step 5: Commit**

```bash
git add testing/assert.go testing/assert_test.go
git commit -m "feat(testing): add ToolAssertion, ResponseAssertion, and ExpectRounds"
```

---

### Task 4: ScoreCard with judge-LLM evaluation

**Files:**
- Create: `testing/scorecard.go`
- Create: `testing/scorecard_test.go`

ScoreCard evaluates a conversation against a list of criteria using a judge LLM. Each criterion is evaluated independently. The judge is prompted to reason before rendering a verdict (reduces evaluation errors). ScoreResult reports pass/fail per criterion plus total score.

- [ ] **Step 1: Write the test file**

```go
// testing/scorecard_test.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestScoreCardAllPass(t *testing.T) {
	// Judge that always says conditions are met
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"Greeting is present","condition_met":true},{"reasoning":"Restaurant mentioned","condition_met":true}]`).
		OnRound(1).RespondWithText(`should not be called`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "The assistant greets the user", Score: 1},
			{Condition: "The assistant mentions a restaurant name", Score: 2},
		},
		PassingScore: 3,
	}

	messages := []kernel.Message{
		kernel.UserMsg("Find thai food"),
		kernel.AssistantMsg("Hello! I found Thai Basil restaurant for you."),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalScore != 3 {
		t.Errorf("expected total score 3, got %d", result.TotalScore)
	}
	if result.MaxScore != 3 {
		t.Errorf("expected max score 3, got %d", result.MaxScore)
	}
	if !result.Passed {
		t.Error("expected Passed=true")
	}
	if len(result.Details) != 2 {
		t.Fatalf("expected 2 details, got %d", len(result.Details))
	}
	if !result.Details[0].Met {
		t.Error("expected first criterion to be met")
	}
	if !result.Details[1].Met {
		t.Error("expected second criterion to be met")
	}
}

func TestScoreCardPartialPass(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"Greeting found","condition_met":true},{"reasoning":"No restaurant name","condition_met":false}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "The assistant greets the user", Score: 1},
			{Condition: "The assistant mentions a restaurant name", Score: 2},
		},
		PassingScore: 2,
	}

	messages := []kernel.Message{
		kernel.UserMsg("Find thai food"),
		kernel.AssistantMsg("Hello! Let me search for that."),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalScore != 1 {
		t.Errorf("expected total score 1, got %d", result.TotalScore)
	}
	if result.Passed {
		t.Error("expected Passed=false (score 1 < passing 2)")
	}
	if result.Details[0].Met != true {
		t.Error("expected first criterion met")
	}
	if result.Details[1].Met != false {
		t.Error("expected second criterion not met")
	}
}

func TestScoreCardAllFail(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"No greeting","condition_met":false},{"reasoning":"No name","condition_met":false}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Greets user", Score: 1},
			{Condition: "Names restaurant", Score: 1},
		},
		PassingScore: 1,
	}

	messages := []kernel.Message{
		kernel.UserMsg("Hi"),
		kernel.AssistantMsg("Error occurred."),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalScore != 0 {
		t.Errorf("expected total score 0, got %d", result.TotalScore)
	}
	if result.Passed {
		t.Error("expected Passed=false")
	}
}

func TestScoreCardJudgeError(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithError(fmt.Errorf("judge unavailable"))

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Something", Score: 1},
		},
		PassingScore: 1,
	}

	_, err := sc.Evaluate(context.Background(), judge, []kernel.Message{
		kernel.UserMsg("test"),
	})
	if err == nil {
		t.Fatal("expected error when judge fails")
	}
}

func TestScoreCardJudgeMalformedJSON(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`not valid json`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Something", Score: 1},
		},
		PassingScore: 1,
	}

	_, err := sc.Evaluate(context.Background(), judge, []kernel.Message{
		kernel.UserMsg("test"),
	})
	if err == nil {
		t.Fatal("expected error for malformed judge response")
	}
}

func TestScoreCardReasoningPreserved(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"The user said hello and the bot replied with hi","condition_met":true}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Greeting present", Score: 1},
		},
		PassingScore: 1,
	}

	messages := []kernel.Message{
		kernel.UserMsg("hello"),
		kernel.AssistantMsg("hi"),
	}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Details[0].Reasoning != "The user said hello and the bot replied with hi" {
		t.Errorf("reasoning not preserved: %q", result.Details[0].Reasoning)
	}
}

func TestScoreCardZeroPassingScore(t *testing.T) {
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"nope","condition_met":false}]`)

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "Anything", Score: 1},
		},
		PassingScore: 0, // zero means always passes
	}

	messages := []kernel.Message{kernel.UserMsg("test")}

	result, err := sc.Evaluate(context.Background(), judge, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true when PassingScore is 0")
	}
}

// Verify the prompt format sent to the judge includes all criteria and messages.
func TestScoreCardPromptFormat(t *testing.T) {
	var capturedPrompt string

	captureLLM := &promptCapturingLLM{
		response: `[{"reasoning":"ok","condition_met":true},{"reasoning":"ok","condition_met":true}]`,
	}

	sc := &ScoreCard{
		Criteria: []Criterion{
			{Condition: "User is greeted", Score: 1},
			{Condition: "Restaurant named", Score: 1},
		},
		PassingScore: 2,
	}

	messages := []kernel.Message{
		kernel.UserMsg("hello"),
		kernel.AssistantMsg("Hi! Thai Basil is great."),
	}

	sc.Evaluate(context.Background(), captureLLM, messages)
	capturedPrompt = captureLLM.lastPrompt

	// The prompt should mention the criteria
	if !containsStr(capturedPrompt, "User is greeted") {
		t.Error("prompt should contain criterion text")
	}
	if !containsStr(capturedPrompt, "Restaurant named") {
		t.Error("prompt should contain second criterion text")
	}
	// The prompt should include the conversation
	if !containsStr(capturedPrompt, "hello") {
		t.Error("prompt should contain user message")
	}
	if !containsStr(capturedPrompt, "Thai Basil") {
		t.Error("prompt should contain assistant message")
	}
}

// promptCapturingLLM captures the last prompt text sent to it.
type promptCapturingLLM struct {
	response   string
	lastPrompt string
}

func (p *promptCapturingLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	for _, msg := range params.Messages {
		p.lastPrompt += msg.TextContent()
	}
	return kernel.Response{Text: p.response, FinishReason: "stop"}, nil
}

func (p *promptCapturingLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (p *promptCapturingLLM) Model() string { return "prompt-capture" }

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Suppress unused import warning
var _ = json.Marshal
var _ = fmt.Sprintf
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestScoreCard"`
Expected: FAIL (ScoreCard not defined)

- [ ] **Step 3: Write the implementation**

```go
// testing/scorecard.go
package axontest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/axonframework/axon/kernel"
)

// ScoreCard evaluates a conversation against a set of criteria using a judge LLM.
type ScoreCard struct {
	Criteria     []Criterion
	PassingScore int
}

// Criterion is a single evaluation condition with an associated score.
type Criterion struct {
	Condition string // Human-readable description, e.g. "The assistant confirms the reservation"
	Score     int    // Points awarded if the condition is met
}

// ScoreResult holds the evaluation outcome.
type ScoreResult struct {
	TotalScore int
	MaxScore   int
	Passed     bool
	Details    []CriterionResult
}

// CriterionResult captures the evaluation of a single criterion.
type CriterionResult struct {
	Condition string
	Score     int
	Met       bool
	Reasoning string
}

// judgeVerdict is the expected JSON structure from the judge LLM per criterion.
type judgeVerdict struct {
	Reasoning    string `json:"reasoning"`
	ConditionMet bool   `json:"condition_met"`
}

// Evaluate runs the judge LLM against the conversation for all criteria.
// The judge receives a single prompt with all criteria and must return a JSON array
// with one verdict per criterion: [{"reasoning":"...","condition_met":true/false}, ...].
// Reasoning-before-verdict ordering reduces judge evaluation errors.
func (sc *ScoreCard) Evaluate(ctx context.Context, judge kernel.LLM, messages []kernel.Message) (*ScoreResult, error) {
	prompt := buildEvalPrompt(sc.Criteria, messages)

	resp, err := judge.Generate(ctx, kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg(prompt)},
	})
	if err != nil {
		return nil, fmt.Errorf("judge LLM call failed: %w", err)
	}

	var verdicts []judgeVerdict
	if err := json.Unmarshal([]byte(resp.Text), &verdicts); err != nil {
		return nil, fmt.Errorf("failed to parse judge response as JSON array: %w (response: %q)", err, resp.Text)
	}

	if len(verdicts) != len(sc.Criteria) {
		return nil, fmt.Errorf("judge returned %d verdicts but expected %d (one per criterion)", len(verdicts), len(sc.Criteria))
	}

	var totalScore, maxScore int
	details := make([]CriterionResult, len(sc.Criteria))

	for i, criterion := range sc.Criteria {
		maxScore += criterion.Score
		detail := CriterionResult{
			Condition: criterion.Condition,
			Score:     criterion.Score,
			Met:       verdicts[i].ConditionMet,
			Reasoning: verdicts[i].Reasoning,
		}
		if detail.Met {
			totalScore += criterion.Score
		}
		details[i] = detail
	}

	return &ScoreResult{
		TotalScore: totalScore,
		MaxScore:   maxScore,
		Passed:     totalScore >= sc.PassingScore,
		Details:    details,
	}, nil
}

// buildEvalPrompt constructs the evaluation prompt for the judge LLM.
func buildEvalPrompt(criteria []Criterion, messages []kernel.Message) string {
	var b strings.Builder

	b.WriteString("You are an evaluation judge. Evaluate the following conversation against each criterion.\n\n")

	// Format conversation
	b.WriteString("## Conversation\n\n")
	for _, msg := range messages {
		b.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, msg.TextContent()))
	}

	// Format criteria
	b.WriteString("\n## Criteria\n\n")
	for i, c := range criteria {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, c.Condition))
	}

	// Format output instructions
	b.WriteString("\n## Instructions\n\n")
	b.WriteString("For each criterion above, evaluate whether it is met by the conversation.\n")
	b.WriteString("Think step-by-step in the reasoning field BEFORE rendering your verdict.\n")
	b.WriteString("Respond with a JSON array (one element per criterion, in order):\n\n")
	b.WriteString("```json\n")
	b.WriteString("[{\"reasoning\": \"your step-by-step analysis\", \"condition_met\": true}, ...]\n")
	b.WriteString("```\n\n")
	b.WriteString("Respond with ONLY the JSON array. No other text.")

	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestScoreCard"`
Expected: PASS (all 8 tests)

- [ ] **Step 5: Commit**

```bash
git add testing/scorecard.go testing/scorecard_test.go
git commit -m "feat(testing): add ScoreCard with judge-LLM evaluation"
```

---

### Task 5: Batch evaluation with Eval()

**Files:**
- Create: `testing/eval.go`
- Create: `testing/eval_test.go`

Eval() runs a batch of test cases against an agent and checks expectations using assertions and an optional ScoreCard judge. Each Case specifies input, optional history, and an Expectation. Expectation holds optional fields: response substring checks, tool call checks, round count, and a ScoreCard.

- [ ] **Step 1: Write the test file**

```go
// testing/eval_test.go
package axontest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
)

func TestEvalSingleCaseTextCheck(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello there!")

	agent := kernel.NewAgent(kernel.WithModel(mock))

	Eval(t, agent, nil, []Case{
		{
			Name:  "greeting",
			Input: "Hi",
			Expect: &Expectation{
				ResponseContains:    []string{"Hello"},
				ResponseNotContains: []string{"Error"},
			},
		},
	})
}

func TestEvalSingleCaseRoundCount(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Done")

	agent := kernel.NewAgent(kernel.WithModel(mock))

	rounds := 1
	Eval(t, agent, nil, []Case{
		{
			Name:  "simple response",
			Input: "Test",
			Expect: &Expectation{
				Rounds: &rounds,
			},
		},
	})
}

func TestEvalToolExpectations(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithToolCall("search", map[string]any{"query": "thai"}).
		OnRound(1).RespondWithText("Found it!")

	searchTool := &fakeTool{name: "search", result: "ok"}

	agent := kernel.NewAgent(
		kernel.WithModel(mock),
		kernel.WithTools(searchTool),
	)

	Eval(t, agent, nil, []Case{
		{
			Name:  "tool usage",
			Input: "Find thai food",
			Expect: &Expectation{
				ToolCalled:    []string{"search"},
				ToolNotCalled: []string{"reserve"},
			},
		},
	})
}

func TestEvalWithScoreCard(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Hello! Thai Basil is a great restaurant.")

	// Judge for ScoreCard
	judge := NewMockLLM().
		OnRound(0).RespondWithText(`[{"reasoning":"Greeting found","condition_met":true},{"reasoning":"Restaurant named","condition_met":true}]`)

	agent := kernel.NewAgent(kernel.WithModel(mock))

	Eval(t, agent, judge, []Case{
		{
			Name:  "quality check",
			Input: "Find food",
			Expect: &Expectation{
				ScoreCard: &ScoreCard{
					Criteria: []Criterion{
						{Condition: "Greets user", Score: 1},
						{Condition: "Names restaurant", Score: 2},
					},
					PassingScore: 3,
				},
			},
		},
	})
}

func TestEvalWithHistory(t *testing.T) {
	captureLLM := &capturingMockLLM{
		response: kernel.Response{Text: "Follow up answer", FinishReason: "stop"},
	}

	agent := kernel.NewAgent(kernel.WithModel(captureLLM))

	Eval(t, agent, nil, []Case{
		{
			Name:  "with history",
			Input: "Follow up question",
			History: []kernel.Message{
				kernel.UserMsg("First question"),
				kernel.AssistantMsg("First answer"),
			},
			Expect: &Expectation{
				ResponseContains: []string{"Follow up"},
			},
		},
	})
}

func TestEvalMultipleCases(t *testing.T) {
	// Multiple cases each get their own fresh MockLLM via WithMockLLM
	// Since each case uses the agent's model, we need separate mocks per case.
	// Eval resets the mock between cases by using Run() which calls agent.Run().
	// For this test, we use a simple approach: the agent's LLM just echoes.
	echoLLM := &echoMockLLM{}
	agent := kernel.NewAgent(kernel.WithModel(echoLLM))

	Eval(t, agent, nil, []Case{
		{
			Name:  "case 1",
			Input: "Alpha",
			Expect: &Expectation{
				ResponseContains: []string{"Alpha"},
			},
		},
		{
			Name:  "case 2",
			Input: "Beta",
			Expect: &Expectation{
				ResponseContains: []string{"Beta"},
			},
		},
	})
}

func TestEvalNilExpectation(t *testing.T) {
	mock := NewMockLLM().
		OnRound(0).RespondWithText("Anything")

	agent := kernel.NewAgent(kernel.WithModel(mock))

	// Should not panic with nil Expect
	Eval(t, agent, nil, []Case{
		{
			Name:  "no expectations",
			Input: "test",
		},
	})
}

// echoMockLLM returns the last user message as the response text.
type echoMockLLM struct{}

func (e *echoMockLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	text := ""
	for i := len(params.Messages) - 1; i >= 0; i-- {
		if params.Messages[i].Role == kernel.RoleUser {
			text = params.Messages[i].TextContent()
			break
		}
	}
	return kernel.Response{Text: text, FinishReason: "stop"}, nil
}

func (e *echoMockLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	return nil, nil
}

func (e *echoMockLLM) Model() string { return "echo" }

// Suppress unused imports
var _ = json.Marshal
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestEval"`
Expected: FAIL (Eval, Case, Expectation not defined)

- [ ] **Step 3: Write the implementation**

```go
// testing/eval.go
package axontest

import (
	"context"
	"testing"

	"github.com/axonframework/axon/kernel"
)

// Case represents a single test case in a batch evaluation.
type Case struct {
	Name    string             // Descriptive name (used as t.Run subtest name)
	Input   string             // User input to send to the agent
	History []kernel.Message   // Optional conversation history to prepend
	Expect  *Expectation       // Optional assertions to check
}

// Expectation holds the expected outcomes for a Case.
// All fields are optional. Only non-nil/non-empty fields are checked.
type Expectation struct {
	// Response text assertions
	ResponseContains    []string // Response must contain each substring
	ResponseNotContains []string // Response must not contain any substring

	// Tool call assertions
	ToolCalled    []string // Each named tool must have been called at least once
	ToolNotCalled []string // Each named tool must not have been called

	// Structural assertions
	Rounds *int // If non-nil, asserts exact round count

	// Quality evaluation via judge LLM
	ScoreCard *ScoreCard // If non-nil, runs ScoreCard.Evaluate and asserts pass
}

// Eval runs a batch of test cases against an agent. Each case runs as a subtest.
// If judge is non-nil and a case's Expectation has a ScoreCard, the judge evaluates quality.
// If judge is nil and a ScoreCard is present, the ScoreCard's evaluation is skipped.
func Eval(t *testing.T, agent *kernel.Agent, judge kernel.LLM, cases []Case) {
	t.Helper()

	for _, tc := range cases {
		tc := tc // capture range variable
		name := tc.Name
		if name == "" {
			name = tc.Input
		}

		t.Run(name, func(t *testing.T) {
			// Build run options
			var opts []RunOption
			if len(tc.History) > 0 {
				opts = append(opts, WithHistory(tc.History...))
			}

			// Run the agent
			result := Run(t, agent, tc.Input, opts...)

			// Apply expectations
			if tc.Expect == nil {
				return
			}

			// Response contains
			for _, substr := range tc.Expect.ResponseContains {
				result.ExpectResponse().Contains(t, substr)
			}

			// Response not contains
			for _, substr := range tc.Expect.ResponseNotContains {
				result.ExpectResponse().NotContains(t, substr)
			}

			// Tool called
			for _, toolName := range tc.Expect.ToolCalled {
				result.ExpectTool(toolName).Called(t)
			}

			// Tool not called
			for _, toolName := range tc.Expect.ToolNotCalled {
				result.ExpectTool(toolName).NotCalled(t)
			}

			// Round count
			if tc.Expect.Rounds != nil {
				result.ExpectRounds(t, *tc.Expect.Rounds)
			}

			// ScoreCard evaluation
			if tc.Expect.ScoreCard != nil && judge != nil {
				// Reconstruct messages from the result for the judge.
				// The conversation is: history + user input + agent response.
				var messages []kernel.Message
				messages = append(messages, tc.History...)
				messages = append(messages, kernel.UserMsg(tc.Input))
				messages = append(messages, kernel.AssistantMsg(result.Text()))

				scoreResult, err := tc.Expect.ScoreCard.Evaluate(context.Background(), judge, messages)
				if err != nil {
					t.Fatalf("ScoreCard evaluation failed: %v", err)
				}
				if !scoreResult.Passed {
					t.Errorf("ScoreCard failed: scored %d/%d (need %d). Details:", scoreResult.TotalScore, scoreResult.MaxScore, tc.Expect.ScoreCard.PassingScore)
					for _, d := range scoreResult.Details {
						status := "PASS"
						if !d.Met {
							status = "FAIL"
						}
						t.Errorf("  [%s] %s (score: %d) - %s", status, d.Condition, d.Score, d.Reasoning)
					}
				}
			}
		})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/axons && go test ./testing/ -v -run "TestEval"`
Expected: PASS (all 7 tests)

- [ ] **Step 5: Commit**

```bash
git add testing/eval.go testing/eval_test.go
git commit -m "feat(testing): add Eval() batch evaluation with Case and Expectation"
```

---

### Task 6: Self-review and final validation

This task verifies spec coverage, scans for placeholders, and checks type consistency.

- [ ] **Step 1: Spec coverage audit**

Run this checklist against the spec (Section 5.1-5.6):

```
Section 5.1 - Run and Assert:
  [x] Run(t, agent, input, opts...) -> *TestResult     (run.go)
  [x] WithHistory(msgs...)                              (run.go)
  [x] MockTool(name, response)                          (run.go)
  [x] WithMockLLM(responses...)                         (run.go)

Section 5.2 - Assertions:
  [x] ExpectTool(name) -> *ToolAssertion                (assert.go)
  [x] Called(t)                                          (assert.go)
  [x] NotCalled(t)                                      (assert.go)
  [x] CalledTimes(t, n)                                 (assert.go)
  [x] WithParam(key, value)                              (assert.go)
  [x] WithParamMatch(key, judge, criteria)               (assert.go)
  [x] ExpectResponse() -> *ResponseAssertion             (assert.go)
  [x] Contains(t, substring)                             (assert.go)
  [x] NotContains(t, substring)                          (assert.go)
  [x] Satisfies(t, judge, criteria)                      (assert.go)
  [x] ExpectRounds(t, n)                                 (assert.go)

Section 5.3 - ScoreCard:
  [x] ScoreCard{Criteria, PassingScore}                  (scorecard.go)
  [x] Criterion{Condition, Score}                        (scorecard.go)
  [x] Evaluate(ctx, judge, messages) -> (*ScoreResult, error)  (scorecard.go)
  [x] Judge uses reasoning-before-verdict format          (scorecard.go, buildEvalPrompt)

Section 5.4 - MockLLM:
  [x] NewMockLLM() -> *MockLLM                           (mock.go)
  [x] OnRound(n) -> *MockResponseBuilder                 (mock.go)
  [x] RespondWithText(text) -> *MockLLM                  (mock.go)
  [x] RespondWithToolCall(name, params) -> *MockLLM      (mock.go)
  [x] RespondWithToolCalls(calls...) -> *MockLLM         (mock.go)
  [x] RespondWithError(err) -> *MockLLM                  (mock.go)

Section 5.5 - Batch Evaluation:
  [x] Eval(t, agent, judge, cases)                       (eval.go)
  [x] Case{Input, History, Expect}                       (eval.go)

Section 5.6 - Test Organization:
  [x] Uses Go's built-in test infrastructure             (all *_test.go files)
  [x] No custom tag filtering                            (confirmed)
```

- [ ] **Step 2: Placeholder scan**

Run:
```bash
cd /Users/derek/repo/axons && grep -rn "TODO\|FIXME\|HACK\|XXX\|placeholder\|not implemented" testing/ --include="*.go"
```
Expected: Zero matches

- [ ] **Step 3: Type consistency check**

Run:
```bash
cd /Users/derek/repo/axons && go vet ./testing/
```
Expected: No issues

- [ ] **Step 4: Run full test suite**

Run:
```bash
cd /Users/derek/repo/axons && go test ./testing/ -v -count=1
```
Expected: All tests pass

- [ ] **Step 5: Final commit**

```bash
git add testing/
git commit -m "feat(testing): complete axontest package -- MockLLM, Run, assertions, ScoreCard, Eval"
```

---

## Kernel API Additions Required

The testing package requires two small additions to the kernel package that are not in the current kernel plan. These should be added before or alongside the testing implementation:

**1. `Agent.CloneWith(opts ...AgentOption) *Agent` in `kernel/agent.go`**

Creates a shallow copy of the agent with additional options applied. Needed by `axontest.Run()` to inject mock LLMs and hooks without mutating the original agent.

```go
func (a *Agent) CloneWith(opts ...AgentOption) *Agent {
    clone := *a
    clone.tools = append([]Tool(nil), a.tools...)
    clone.hooks = a.hooks
    for _, opt := range opts {
        opt(&clone)
    }
    return &clone
}
```

**2. `AgentContext.ReplaceTools(tools []Tool)` in `kernel/context.go`**

Replaces the entire tool set and clears the disabled set. Needed by `MockTool` to swap tool implementations at runtime.

```go
func (c *AgentContext) ReplaceTools(tools []Tool) {
    c.tools = tools
    c.disabled = make(map[string]bool)
}
```

---

## Summary

| Task | Files | Tests | Key Types |
|------|-------|-------|-----------|
| 1. MockLLM | mock.go, mock_test.go | 7 | MockLLM, MockResponseBuilder |
| 2. Run | run.go, run_test.go | 6 | TestResult, RunOption, WithHistory, MockTool, WithMockLLM |
| 3. Assertions | assert.go, assert_test.go | 11 | ToolAssertion, ResponseAssertion, ExpectRounds |
| 4. ScoreCard | scorecard.go, scorecard_test.go | 8 | ScoreCard, Criterion, ScoreResult, CriterionResult |
| 5. Eval | eval.go, eval_test.go | 7 | Eval, Case, Expectation |
| 6. Review | -- | validation | -- |
| **Total** | **10 .go files** | **39 tests** | -- |

Estimated implementation time: 30-45 minutes (6 tasks, 5-8 min each).
