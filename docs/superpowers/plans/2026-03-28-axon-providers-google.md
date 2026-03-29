# Axon Google Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Google Gemini provider package -- an adapter implementing `kernel.LLM` using `google.golang.org/genai` (the unified Google AI SDK supporting both AI Studio and Vertex AI). Covers type conversion, streaming, construction-time options, and thorough unit tests of the translation layer.

**Architecture:** Single Go package `providers/google/` with a separate `go.mod`. The `GoogleLLM` struct wraps a `*genai.Client` and delegates to its `Models.GenerateContent` / `Models.GenerateContentStream` methods. A conversion layer translates between kernel types and genai types. Streaming wraps the genai iterator into `kernel.Stream`. Unit tests focus on the conversion functions (fully testable without an API); Generate/GenerateStream tests use a mockable internal interface over the genai client.

**Tech Stack:** Go 1.25.2, `google.golang.org/genai`, `github.com/axonframework/axon/kernel` (local replace directive)

**Source spec:** `docs/superpowers/specs/2026-03-28-providers-google-contrib-design.md`, Section 1 (Google Provider)

**Kernel dependency note:** The kernel package is already implemented. All provider files import `github.com/axonframework/axon/kernel` and use a `replace` directive to resolve it locally at `../../kernel`.

---

## File Structure

```
providers/google/
├── go.mod              # module github.com/axonframework/axon/providers/google
├── google.go           # GoogleLLM, New(), Option, Generate(), GenerateStream(), Model()
├── google_test.go      # Tests for New, Generate, GenerateStream via mock client
├── convert.go          # Message/Tool/Schema/ToolChoice translation functions
├── convert_test.go     # Exhaustive tests for all conversion functions
├── stream.go           # Stream implementation wrapping genai iterator
├── stream_test.go      # Tests for stream accumulation and event emission
```

---

## Kernel Types Reference

These types from the kernel package are used throughout this plan. They are already implemented.

```go
// kernel.LLM -- the interface this provider implements
type LLM interface {
    Generate(ctx context.Context, params GenerateParams) (Response, error)
    GenerateStream(ctx context.Context, params GenerateParams) (Stream, error)
    Model() string
}

// kernel.GenerateParams
type GenerateParams struct {
    Messages []Message
    Tools    []Tool
    Options  GenerateOptions
}

// kernel.GenerateOptions
type GenerateOptions struct {
    Temperature    *float32
    MaxTokens      *int
    StopSequences  []string
    ToolChoice     ToolChoice
    OutputSchema   *Schema
    ReasoningLevel *string
}

// kernel.ToolChoice
type ToolChoice struct {
    Type     string // "auto", "required", "none", "tool"
    ToolName string // only when Type == "tool"
}

// kernel.Response
type Response struct {
    Text         string
    ToolCalls    []ToolCall
    Usage        Usage
    FinishReason string
}

// kernel.Usage
type Usage struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int
    Latency      time.Duration
}

// kernel.Stream
type Stream interface {
    Events() <-chan StreamEvent
    Text()   <-chan string
    Response() Response
    Err()    error
}

// kernel.StreamEvent (marker interface), kernel.TextDeltaEvent
type TextDeltaEvent struct { Text string }

// kernel.Message, kernel.ContentPart, kernel.ToolCall, kernel.ToolResult, kernel.ImageContent
// kernel.Tool (interface: Name(), Description(), Schema(), Execute())
// kernel.Schema (Type, Description, Properties, Required, Items, Enum, Minimum, Maximum)
```

---

### Task 1: Initialize Go module and create the conversion layer

**Files:**
- Create: `providers/google/go.mod`
- Create: `providers/google/convert.go`
- Create: `providers/google/convert_test.go`

This is the foundation. All translation between kernel and genai types lives here. These functions are pure (no API calls) and fully unit-testable.

- [ ] **Step 1: Write convert_test.go**

```go
// providers/google/convert_test.go
package google

import (
	"encoding/json"
	"testing"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// --- Schema conversion tests ---

func TestConvertSchema_String(t *testing.T) {
	s := kernel.Schema{Type: "string", Description: "a name"}
	gs := convertSchema(s)
	if gs.Type != genai.TypeString {
		t.Errorf("expected TypeString, got %v", gs.Type)
	}
	if gs.Description != "a name" {
		t.Errorf("expected description %q, got %q", "a name", gs.Description)
	}
}

func TestConvertSchema_Integer(t *testing.T) {
	min := 1.0
	max := 100.0
	s := kernel.Schema{Type: "integer", Minimum: &min, Maximum: &max}
	gs := convertSchema(s)
	if gs.Type != genai.TypeInteger {
		t.Errorf("expected TypeInteger, got %v", gs.Type)
	}
	if gs.Minimum == nil || *gs.Minimum != 1.0 {
		t.Errorf("expected minimum 1.0, got %v", gs.Minimum)
	}
	if gs.Maximum == nil || *gs.Maximum != 100.0 {
		t.Errorf("expected maximum 100.0, got %v", gs.Maximum)
	}
}

func TestConvertSchema_Number(t *testing.T) {
	s := kernel.Schema{Type: "number"}
	gs := convertSchema(s)
	if gs.Type != genai.TypeNumber {
		t.Errorf("expected TypeNumber, got %v", gs.Type)
	}
}

func TestConvertSchema_Boolean(t *testing.T) {
	s := kernel.Schema{Type: "boolean"}
	gs := convertSchema(s)
	if gs.Type != genai.TypeBoolean {
		t.Errorf("expected TypeBoolean, got %v", gs.Type)
	}
}

func TestConvertSchema_Array(t *testing.T) {
	s := kernel.Schema{
		Type:  "array",
		Items: &kernel.Schema{Type: "string"},
	}
	gs := convertSchema(s)
	if gs.Type != genai.TypeArray {
		t.Errorf("expected TypeArray, got %v", gs.Type)
	}
	if gs.Items == nil || gs.Items.Type != genai.TypeString {
		t.Errorf("expected Items to be TypeString, got %v", gs.Items)
	}
}

func TestConvertSchema_Object(t *testing.T) {
	s := kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"name": {Type: "string", Description: "user name"},
			"age":  {Type: "integer"},
		},
		Required: []string{"name"},
	}
	gs := convertSchema(s)
	if gs.Type != genai.TypeObject {
		t.Errorf("expected TypeObject, got %v", gs.Type)
	}
	if len(gs.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(gs.Properties))
	}
	nameProp, ok := gs.Properties["name"]
	if !ok {
		t.Fatal("expected 'name' property")
	}
	if nameProp.Type != genai.TypeString {
		t.Errorf("expected name property TypeString, got %v", nameProp.Type)
	}
	if nameProp.Description != "user name" {
		t.Errorf("expected description %q, got %q", "user name", nameProp.Description)
	}
	if len(gs.Required) != 1 || gs.Required[0] != "name" {
		t.Errorf("expected Required [name], got %v", gs.Required)
	}
}

func TestConvertSchema_Enum(t *testing.T) {
	s := kernel.Schema{Type: "string", Enum: []string{"red", "green", "blue"}}
	gs := convertSchema(s)
	if len(gs.Enum) != 3 || gs.Enum[0] != "red" {
		t.Errorf("expected enum [red green blue], got %v", gs.Enum)
	}
}

func TestConvertSchema_Nil(t *testing.T) {
	s := kernel.Schema{}
	gs := convertSchema(s)
	if gs.Type != genai.TypeString {
		t.Errorf("expected default TypeString for empty type, got %v", gs.Type)
	}
}

// --- Tool conversion tests ---

type fakeKernelTool struct {
	name string
	desc string
	sch  kernel.Schema
}

func (f *fakeKernelTool) Name() string                                                   { return f.name }
func (f *fakeKernelTool) Description() string                                            { return f.desc }
func (f *fakeKernelTool) Schema() kernel.Schema                                          { return f.sch }
func (f *fakeKernelTool) Execute(_ context.Context, _ json.RawMessage) (any, error)      { return nil, nil }

func TestConvertTools(t *testing.T) {
	tools := []kernel.Tool{
		&fakeKernelTool{
			name: "search",
			desc: "Search the web",
			sch: kernel.Schema{
				Type: "object",
				Properties: map[string]kernel.Schema{
					"query": {Type: "string", Description: "search query"},
				},
				Required: []string{"query"},
			},
		},
		&fakeKernelTool{
			name: "calculate",
			desc: "Do math",
			sch: kernel.Schema{
				Type: "object",
				Properties: map[string]kernel.Schema{
					"expression": {Type: "string"},
				},
				Required: []string{"expression"},
			},
		},
	}

	result := convertTools(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 genai.Tool wrapper, got %d", len(result))
	}
	decls := result[0].FunctionDeclarations
	if len(decls) != 2 {
		t.Fatalf("expected 2 function declarations, got %d", len(decls))
	}
	if decls[0].Name != "search" {
		t.Errorf("expected first decl name 'search', got %q", decls[0].Name)
	}
	if decls[0].Description != "Search the web" {
		t.Errorf("expected description 'Search the web', got %q", decls[0].Description)
	}
	if decls[1].Name != "calculate" {
		t.Errorf("expected second decl name 'calculate', got %q", decls[1].Name)
	}
}

func TestConvertTools_Empty(t *testing.T) {
	result := convertTools(nil)
	if result != nil {
		t.Errorf("expected nil for empty tools, got %v", result)
	}
}

// --- ToolChoice conversion tests ---

func TestConvertToolChoice_Auto(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceAuto)
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for auto")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigAuto {
		t.Errorf("expected FunctionCallingConfigAuto, got %v", tc.FunctionCallingConfig.Mode)
	}
}

func TestConvertToolChoice_Required(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceRequired)
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for required")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigAny {
		t.Errorf("expected FunctionCallingConfigAny, got %v", tc.FunctionCallingConfig.Mode)
	}
}

func TestConvertToolChoice_None(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceNone)
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for none")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigNone {
		t.Errorf("expected FunctionCallingConfigNone, got %v", tc.FunctionCallingConfig.Mode)
	}
}

func TestConvertToolChoice_Force(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoiceForce("search"))
	if tc == nil {
		t.Fatal("expected non-nil ToolConfig for force")
	}
	if tc.FunctionCallingConfig.Mode != genai.FunctionCallingConfigAny {
		t.Errorf("expected FunctionCallingConfigAny, got %v", tc.FunctionCallingConfig.Mode)
	}
	if len(tc.FunctionCallingConfig.AllowedFunctionNames) != 1 ||
		tc.FunctionCallingConfig.AllowedFunctionNames[0] != "search" {
		t.Errorf("expected AllowedFunctionNames [search], got %v", tc.FunctionCallingConfig.AllowedFunctionNames)
	}
}

func TestConvertToolChoice_Zero(t *testing.T) {
	tc := convertToolChoice(kernel.ToolChoice{})
	if tc != nil {
		t.Errorf("expected nil for zero-value ToolChoice, got %v", tc)
	}
}

// --- Message conversion tests ---

func strPtr(s string) *string { return &s }

func TestConvertMessages_SystemExtracted(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleSystem, Content: []kernel.ContentPart{{Text: strPtr("Be helpful")}}},
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{{Text: strPtr("Hello")}}},
	}

	contents, sysInstruction := convertMessages(msgs)

	if sysInstruction == nil {
		t.Fatal("expected system instruction to be extracted")
	}
	if len(sysInstruction.Parts) != 1 {
		t.Fatalf("expected 1 system instruction part, got %d", len(sysInstruction.Parts))
	}
	if txt, ok := sysInstruction.Parts[0].(genai.Text); !ok || string(txt) != "Be helpful" {
		t.Errorf("expected system text 'Be helpful', got %v", sysInstruction.Parts[0])
	}

	if len(contents) != 1 {
		t.Fatalf("expected 1 content (user), got %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", contents[0].Role)
	}
}

func TestConvertMessages_MultipleSystemMerged(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleSystem, Content: []kernel.ContentPart{{Text: strPtr("Rule 1")}}},
		{Role: kernel.RoleSystem, Content: []kernel.ContentPart{{Text: strPtr("Rule 2")}}},
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{{Text: strPtr("Hi")}}},
	}

	_, sysInstruction := convertMessages(msgs)
	if sysInstruction == nil {
		t.Fatal("expected system instruction")
	}
	if len(sysInstruction.Parts) != 2 {
		t.Fatalf("expected 2 system parts, got %d", len(sysInstruction.Parts))
	}
}

func TestConvertMessages_UserText(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{
			{Text: strPtr("part one")},
			{Text: strPtr("part two")},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(contents[0].Parts))
	}
	if txt, ok := contents[0].Parts[0].(genai.Text); !ok || string(txt) != "part one" {
		t.Errorf("expected 'part one', got %v", contents[0].Parts[0])
	}
}

func TestConvertMessages_AssistantWithToolCall(t *testing.T) {
	params := json.RawMessage(`{"query":"test"}`)
	msgs := []kernel.Message{
		{Role: kernel.RoleAssistant, Content: []kernel.ContentPart{
			{ToolCall: &kernel.ToolCall{ID: "call-1", Name: "search", Params: params}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if contents[0].Role != "model" {
		t.Errorf("expected role 'model', got %q", contents[0].Role)
	}
	fc, ok := contents[0].Parts[0].(genai.FunctionCall)
	if !ok {
		t.Fatalf("expected FunctionCall part, got %T", contents[0].Parts[0])
	}
	if fc.Name != "search" {
		t.Errorf("expected function name 'search', got %q", fc.Name)
	}
	if fc.Args["query"] != "test" {
		t.Errorf("expected args query='test', got %v", fc.Args)
	}
}

func TestConvertMessages_ToolResult(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleTool, Content: []kernel.ContentPart{
			{ToolResult: &kernel.ToolResult{
				ToolCallID: "call-1",
				Name:       "search",
				Content:    `{"results":["a","b"]}`,
			}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("expected role 'user' for tool results, got %q", contents[0].Role)
	}
	fr, ok := contents[0].Parts[0].(genai.FunctionResponse)
	if !ok {
		t.Fatalf("expected FunctionResponse part, got %T", contents[0].Parts[0])
	}
	if fr.Name != "search" {
		t.Errorf("expected function name 'search', got %q", fr.Name)
	}
}

func TestConvertMessages_ImageURL(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{
			{Image: &kernel.ImageContent{URL: "gs://bucket/image.png", MimeType: "image/png"}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	fd, ok := contents[0].Parts[0].(genai.FileData)
	if !ok {
		t.Fatalf("expected FileData part, got %T", contents[0].Parts[0])
	}
	if fd.FileURI != "gs://bucket/image.png" {
		t.Errorf("expected URI 'gs://bucket/image.png', got %q", fd.FileURI)
	}
	if fd.MIMEType != "image/png" {
		t.Errorf("expected MIME 'image/png', got %q", fd.MIMEType)
	}
}

func TestConvertMessages_Empty(t *testing.T) {
	contents, sysInstruction := convertMessages(nil)
	if len(contents) != 0 {
		t.Errorf("expected 0 contents, got %d", len(contents))
	}
	if sysInstruction != nil {
		t.Errorf("expected nil system instruction, got %v", sysInstruction)
	}
}

// --- Response conversion tests ---

func TestConvertResponse_TextOnly(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []genai.Part{
					genai.Text("Hello "),
					genai.Text("world"),
				},
			},
			FinishReason: genai.FinishReasonStop,
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 5,
			TotalTokenCount:      15,
		},
	}

	resp := convertResponse(genaiResp)
	if resp.Text != "Hello world" {
		t.Errorf("expected text 'Hello world', got %q", resp.Text)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected input tokens 10, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("expected output tokens 5, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected total tokens 15, got %d", resp.Usage.TotalTokens)
	}
}

func TestConvertResponse_WithToolCalls(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []genai.Part{
					genai.FunctionCall{
						Name: "search",
						Args: map[string]any{"query": "golang"},
					},
					genai.FunctionCall{
						Name: "calculate",
						Args: map[string]any{"expr": "2+2"},
					},
				},
			},
			FinishReason: genai.FinishReasonStop,
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
	}

	resp := convertResponse(genaiResp)
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool call name 'search', got %q", resp.ToolCalls[0].Name)
	}
	if resp.ToolCalls[0].ID == "" {
		t.Error("expected non-empty tool call ID")
	}

	var params map[string]any
	if err := json.Unmarshal(resp.ToolCalls[0].Params, &params); err != nil {
		t.Fatalf("failed to unmarshal tool call params: %v", err)
	}
	if params["query"] != "golang" {
		t.Errorf("expected query 'golang', got %v", params["query"])
	}
}

func TestConvertResponse_NoCandidates(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates:    nil,
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{TotalTokenCount: 5},
	}

	resp := convertResponse(genaiResp)
	if resp.Text != "" {
		t.Errorf("expected empty text, got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
}

func TestConvertResponse_NilUsageMetadata(t *testing.T) {
	genaiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content:      &genai.Content{Parts: []genai.Part{genai.Text("hi")}},
			FinishReason: genai.FinishReasonStop,
		}},
		UsageMetadata: nil,
	}

	resp := convertResponse(genaiResp)
	if resp.Usage.InputTokens != 0 {
		t.Errorf("expected 0 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestConvertFinishReason(t *testing.T) {
	tests := []struct {
		in  genai.FinishReason
		out string
	}{
		{genai.FinishReasonStop, "stop"},
		{genai.FinishReasonMaxTokens, "max_tokens"},
		{genai.FinishReasonSafety, "safety"},
		{genai.FinishReasonRecitation, "safety"},
		{genai.FinishReason(999), "stop"},
	}
	for _, tt := range tests {
		got := convertFinishReason(tt.in)
		if got != tt.out {
			t.Errorf("convertFinishReason(%v) = %q, want %q", tt.in, got, tt.out)
		}
	}
}

// --- GenerateOptions conversion tests ---

func TestBuildConfig_Temperature(t *testing.T) {
	temp := float32(0.7)
	opts := kernel.GenerateOptions{Temperature: &temp}
	cfg := buildConfig(opts, nil, nil)
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", cfg.Temperature)
	}
}

func TestBuildConfig_MaxTokens(t *testing.T) {
	max := 1024
	opts := kernel.GenerateOptions{MaxTokens: &max}
	cfg := buildConfig(opts, nil, nil)
	if cfg.MaxOutputTokens == nil || *cfg.MaxOutputTokens != int32(1024) {
		t.Errorf("expected max output tokens 1024, got %v", cfg.MaxOutputTokens)
	}
}

func TestBuildConfig_StopSequences(t *testing.T) {
	opts := kernel.GenerateOptions{StopSequences: []string{"END", "STOP"}}
	cfg := buildConfig(opts, nil, nil)
	if len(cfg.StopSequences) != 2 || cfg.StopSequences[0] != "END" {
		t.Errorf("expected stop sequences [END STOP], got %v", cfg.StopSequences)
	}
}

func TestBuildConfig_OutputSchema(t *testing.T) {
	schema := &kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"answer": {Type: "string"},
		},
		Required: []string{"answer"},
	}
	opts := kernel.GenerateOptions{OutputSchema: schema}
	cfg := buildConfig(opts, nil, nil)
	if cfg.ResponseMIMEType != "application/json" {
		t.Errorf("expected ResponseMIMEType 'application/json', got %q", cfg.ResponseMIMEType)
	}
	if cfg.ResponseSchema == nil {
		t.Fatal("expected ResponseSchema to be set")
	}
	if cfg.ResponseSchema.Type != genai.TypeObject {
		t.Errorf("expected TypeObject, got %v", cfg.ResponseSchema.Type)
	}
}

func TestBuildConfig_ReasoningLevel(t *testing.T) {
	tests := []struct {
		level  string
		budget int32
	}{
		{"low", 1024},
		{"medium", 4096},
		{"high", 16384},
	}
	for _, tt := range tests {
		level := tt.level
		opts := kernel.GenerateOptions{ReasoningLevel: &level}
		cfg := buildConfig(opts, nil, nil)
		if cfg.ThinkingConfig == nil {
			t.Fatalf("expected ThinkingConfig for level %q", tt.level)
		}
		if cfg.ThinkingConfig.ThinkingBudget != tt.budget {
			t.Errorf("level %q: expected budget %d, got %d", tt.level, tt.budget, cfg.ThinkingConfig.ThinkingBudget)
		}
	}
}

func TestBuildConfig_CachedContent(t *testing.T) {
	cfg := buildConfig(kernel.GenerateOptions{}, nil, ptrStr("cached-abc"))
	if cfg.CachedContent != "cached-abc" {
		t.Errorf("expected cached content 'cached-abc', got %q", cfg.CachedContent)
	}
}

func ptrStr(s string) *string { return &s }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -v`
Expected: FAIL (package does not exist yet)

- [ ] **Step 3: Create go.mod**

Run:
```bash
mkdir -p /Users/derek/repo/axons/providers/google
cd /Users/derek/repo/axons/providers/google
go mod init github.com/axonframework/axon/providers/google
```

Then edit go.mod to add the replace directive and dependency:

```
module github.com/axonframework/axon/providers/google

go 1.25.2

require (
    github.com/axonframework/axon/kernel v0.0.0
    google.golang.org/genai v0.7.0
)

replace github.com/axonframework/axon/kernel => ../../kernel
```

Run: `cd /Users/derek/repo/axons/providers/google && go mod tidy`

Note: The exact genai version will be resolved by `go mod tidy`. Start with `v0.7.0` and let tidy pin the actual latest.

- [ ] **Step 4: Write convert.go implementation**

```go
// providers/google/convert.go
package google

import (
	"encoding/json"
	"fmt"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// convertSchema translates a kernel.Schema to a *genai.Schema recursively.
func convertSchema(s kernel.Schema) *genai.Schema {
	gs := &genai.Schema{
		Type:        mapSchemaType(s.Type),
		Description: s.Description,
		Enum:        s.Enum,
		Required:    s.Required,
		Minimum:     s.Minimum,
		Maximum:     s.Maximum,
	}

	if s.Items != nil {
		gs.Items = convertSchema(*s.Items)
	}

	if len(s.Properties) > 0 {
		gs.Properties = make(map[string]*genai.Schema, len(s.Properties))
		for name, prop := range s.Properties {
			gs.Properties[name] = convertSchema(prop)
		}
	}

	return gs
}

// mapSchemaType maps a JSON Schema type string to a genai.Type constant.
func mapSchemaType(t string) genai.Type {
	switch t {
	case "object":
		return genai.TypeObject
	case "string":
		return genai.TypeString
	case "integer":
		return genai.TypeInteger
	case "number":
		return genai.TypeNumber
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	default:
		return genai.TypeString
	}
}

// convertTools translates a slice of kernel.Tool into genai.Tool wrappers.
// All function declarations are grouped into a single genai.Tool.
func convertTools(tools []kernel.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]*genai.FunctionDeclaration, len(tools))
	for i, t := range tools {
		decls[i] = &genai.FunctionDeclaration{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  convertSchema(t.Schema()),
		}
	}

	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// convertToolChoice translates a kernel.ToolChoice to a *genai.ToolConfig.
// Returns nil for zero-value ToolChoice (no constraint).
func convertToolChoice(tc kernel.ToolChoice) *genai.ToolConfig {
	switch tc.Type {
	case "auto":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigAuto,
			},
		}
	case "required":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigAny,
			},
		}
	case "none":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigNone,
			},
		}
	case "tool":
		return &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigAny,
				AllowedFunctionNames: []string{tc.ToolName},
			},
		}
	default:
		return nil
	}
}

// convertMessages splits kernel messages into genai Contents and an optional
// system instruction. System messages are extracted and merged into a single
// Content for use as GenerateContentConfig.SystemInstruction. Tool result
// messages are mapped to role "user" with FunctionResponse parts, as required
// by the Gemini API.
func convertMessages(msgs []kernel.Message) ([]*genai.Content, *genai.Content) {
	var contents []*genai.Content
	var sysParts []genai.Part

	for _, msg := range msgs {
		switch msg.Role {
		case kernel.RoleSystem:
			for _, cp := range msg.Content {
				if cp.Text != nil {
					sysParts = append(sysParts, genai.Text(*cp.Text))
				}
			}

		case kernel.RoleUser:
			parts := convertContentParts(msg.Content)
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "user",
					Parts: parts,
				})
			}

		case kernel.RoleAssistant:
			parts := convertContentParts(msg.Content)
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "model",
					Parts: parts,
				})
			}

		case kernel.RoleTool:
			parts := convertContentParts(msg.Content)
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{
					Role:  "user",
					Parts: parts,
				})
			}
		}
	}

	var sysInstruction *genai.Content
	if len(sysParts) > 0 {
		sysInstruction = &genai.Content{Parts: sysParts}
	}

	return contents, sysInstruction
}

// convertContentParts translates kernel ContentParts to genai Parts.
func convertContentParts(parts []kernel.ContentPart) []genai.Part {
	var result []genai.Part
	for _, cp := range parts {
		switch {
		case cp.Text != nil:
			result = append(result, genai.Text(*cp.Text))

		case cp.Image != nil:
			result = append(result, genai.FileData{
				FileURI:  cp.Image.URL,
				MIMEType: cp.Image.MimeType,
			})

		case cp.ToolCall != nil:
			args := make(map[string]any)
			if len(cp.ToolCall.Params) > 0 {
				_ = json.Unmarshal(cp.ToolCall.Params, &args)
			}
			result = append(result, genai.FunctionCall{
				Name: cp.ToolCall.Name,
				Args: args,
			})

		case cp.ToolResult != nil:
			response := make(map[string]any)
			if err := json.Unmarshal([]byte(cp.ToolResult.Content), &response); err != nil {
				// If content is not valid JSON, wrap it as a string value
				response = map[string]any{"result": cp.ToolResult.Content}
			}
			result = append(result, genai.FunctionResponse{
				Name:     cp.ToolResult.Name,
				Response: response,
			})
		}
	}
	return result
}

// convertResponse translates a genai response into a kernel.Response.
// Text parts are concatenated. FunctionCall parts become kernel.ToolCalls
// with generated IDs. If tool calls are present, FinishReason is set to
// "tool_calls" regardless of what the API returned.
func convertResponse(resp *genai.GenerateContentResponse) kernel.Response {
	var kr kernel.Response

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]

		if candidate.Content != nil {
			for i, part := range candidate.Content.Parts {
				switch p := part.(type) {
				case genai.Text:
					kr.Text += string(p)
				case genai.FunctionCall:
					params, _ := json.Marshal(p.Args)
					kr.ToolCalls = append(kr.ToolCalls, kernel.ToolCall{
						ID:     fmt.Sprintf("call_%d", i),
						Name:   p.Name,
						Params: params,
					})
				}
			}
		}

		if len(kr.ToolCalls) > 0 {
			kr.FinishReason = "tool_calls"
		} else {
			kr.FinishReason = convertFinishReason(candidate.FinishReason)
		}
	} else {
		kr.FinishReason = "stop"
	}

	if resp.UsageMetadata != nil {
		kr.Usage = kernel.Usage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
			TotalTokens:  int(resp.UsageMetadata.TotalTokenCount),
		}
	}

	return kr
}

// convertFinishReason maps a genai.FinishReason to a kernel finish reason string.
func convertFinishReason(fr genai.FinishReason) string {
	switch fr {
	case genai.FinishReasonStop:
		return "stop"
	case genai.FinishReasonMaxTokens:
		return "max_tokens"
	case genai.FinishReasonSafety, genai.FinishReasonRecitation:
		return "safety"
	default:
		return "stop"
	}
}

// buildConfig constructs a GenerateContentConfig from kernel options and
// provider-level settings (safety settings, cached content name).
func buildConfig(
	opts kernel.GenerateOptions,
	safetySettings []*genai.SafetySetting,
	cachedContent *string,
) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{
		SafetySettings: safetySettings,
	}

	if opts.Temperature != nil {
		cfg.Temperature = genai.Ptr(float32(*opts.Temperature))
	}

	if opts.MaxTokens != nil {
		cfg.MaxOutputTokens = genai.Ptr(int32(*opts.MaxTokens))
	}

	if len(opts.StopSequences) > 0 {
		cfg.StopSequences = opts.StopSequences
	}

	if opts.OutputSchema != nil {
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseSchema = convertSchema(*opts.OutputSchema)
	}

	if opts.ReasoningLevel != nil {
		budget := mapReasoningBudget(*opts.ReasoningLevel)
		cfg.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget: budget,
		}
	}

	if cachedContent != nil {
		cfg.CachedContent = *cachedContent
	}

	return cfg
}

// mapReasoningBudget maps a reasoning level string to a thinking budget token count.
func mapReasoningBudget(level string) int32 {
	switch level {
	case "low":
		return 1024
	case "medium":
		return 4096
	case "high":
		return 16384
	default:
		return 4096
	}
}
```

- [ ] **Step 5: Run tests and verify they pass**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -run TestConvert -v`
Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -run TestBuildConfig -v`

Fix any compilation errors. The genai SDK types (genai.Text, genai.FunctionCall, etc.) and their field names must match the actual SDK. If `genai.Ptr` does not exist, use a local helper. If field names differ (e.g., `FileURI` vs `URI`), correct them to match the SDK.

- [ ] **Step 6: Verify all convert_test.go tests pass**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -v -count=1`

---

### Task 2: Implement GoogleLLM struct with Generate

**Files:**
- Create: `providers/google/google.go`
- Create: `providers/google/google_test.go`

The GoogleLLM struct wraps a `*genai.Client` and implements `kernel.LLM`. To enable unit testing without hitting the Gemini API, we define an internal `modelsClient` interface that abstracts the two genai methods we call. In production, `*genai.Client.Models` satisfies this interface. In tests, we use a fake.

- [ ] **Step 1: Write google_test.go**

```go
// providers/google/google_test.go
package google

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// fakeModelsClient implements modelsClient for testing.
type fakeModelsClient struct {
	generateFn       func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	generateStreamFn func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]
}

func (f *fakeModelsClient) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	if f.generateFn != nil {
		return f.generateFn(ctx, model, contents, config)
	}
	return nil, fmt.Errorf("GenerateContent not configured")
}

func (f *fakeModelsClient) GenerateContentStream(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
	if f.generateStreamFn != nil {
		return f.generateStreamFn(ctx, model, contents, config)
	}
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		yield(nil, fmt.Errorf("GenerateContentStream not configured"))
	}
}

// --- New tests ---

func TestNew(t *testing.T) {
	// New accepts a *genai.Client, but we test with nil since we override
	// the internal client in tests anyway.
	llm := New(nil, "gemini-2.0-flash")
	if llm.Model() != "gemini-2.0-flash" {
		t.Errorf("expected model 'gemini-2.0-flash', got %q", llm.Model())
	}
}

func TestNew_WithOptions(t *testing.T) {
	settings := []*genai.SafetySetting{
		{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
	}
	llm := New(nil, "gemini-2.0-flash",
		WithSafetySettings(settings),
		WithThinkingBudget(8192),
		WithCachedContent("cache-xyz"),
	)
	if llm.safetySettings[0].Category != genai.HarmCategoryHateSpeech {
		t.Error("expected safety setting to be applied")
	}
	if llm.thinkingBudget != 8192 {
		t.Errorf("expected thinking budget 8192, got %d", llm.thinkingBudget)
	}
	if llm.cachedContent == nil || *llm.cachedContent != "cache-xyz" {
		t.Error("expected cached content to be set")
	}
}

// --- Generate tests ---

func TestGenerate_TextResponse(t *testing.T) {
	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			if model != "gemini-2.0-flash" {
				t.Errorf("expected model 'gemini-2.0-flash', got %q", model)
			}
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []genai.Part{genai.Text("Hello from Gemini")},
					},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     8,
					CandidatesTokenCount: 4,
					TotalTokenCount:      12,
				},
			}, nil
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Hello from Gemini" {
		t.Errorf("expected text 'Hello from Gemini', got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 8 {
		t.Errorf("expected 8 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestGenerate_ToolCallResponse(t *testing.T) {
	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []genai.Part{
							genai.FunctionCall{
								Name: "search",
								Args: map[string]any{"query": "weather"},
							},
						},
					},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	resp, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("What is the weather?")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool name 'search', got %q", resp.ToolCalls[0].Name)
	}
}

func TestGenerate_APIError(t *testing.T) {
	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			return nil, fmt.Errorf("quota exceeded")
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "google generate: quota exceeded" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGenerate_SystemMessageExtracted(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig
	var capturedContents []*genai.Content

	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			capturedConfig = config
			capturedContents = contents
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content:      &genai.Content{Parts: []genai.Part{genai.Text("ok")}},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{
			kernel.SystemMsg("You are a helpful assistant"),
			kernel.UserMsg("Hello"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedConfig.SystemInstruction == nil {
		t.Fatal("expected system instruction in config")
	}
	if len(capturedContents) != 1 {
		t.Fatalf("expected 1 content (user only), got %d", len(capturedContents))
	}
	if capturedContents[0].Role != "user" {
		t.Errorf("expected user role, got %q", capturedContents[0].Role)
	}
}

func TestGenerate_WithTools(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig

	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			capturedConfig = config
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content:      &genai.Content{Parts: []genai.Part{genai.Text("ok")}},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	tool := &fakeKernelTool{
		name: "lookup",
		desc: "Look things up",
		sch:  kernel.Schema{Type: "object", Properties: map[string]kernel.Schema{"q": {Type: "string"}}, Required: []string{"q"}},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("find X")},
		Tools:    []kernel.Tool{tool},
		Options:  kernel.GenerateOptions{ToolChoice: kernel.ToolChoiceRequired},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedConfig.Tools) != 1 {
		t.Fatalf("expected 1 tool group, got %d", len(capturedConfig.Tools))
	}
	if capturedConfig.ToolConfig == nil {
		t.Fatal("expected ToolConfig to be set")
	}
	if capturedConfig.ToolConfig.FunctionCallingConfig.Mode != genai.FunctionCallingConfigAny {
		t.Errorf("expected FunctionCallingConfigAny, got %v", capturedConfig.ToolConfig.FunctionCallingConfig.Mode)
	}
}

func TestGenerate_WithOptions(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig

	fake := &fakeModelsClient{
		generateFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			capturedConfig = config
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content:      &genai.Content{Parts: []genai.Part{genai.Text("ok")}},
					FinishReason: genai.FinishReasonStop,
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
			}, nil
		},
	}

	temp := float32(0.5)
	maxTok := 2048
	level := "high"

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	_, err := llm.Generate(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("test")},
		Options: kernel.GenerateOptions{
			Temperature:    &temp,
			MaxTokens:      &maxTok,
			StopSequences:  []string{"DONE"},
			ReasoningLevel: &level,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedConfig.Temperature == nil || *capturedConfig.Temperature != 0.5 {
		t.Errorf("expected temperature 0.5, got %v", capturedConfig.Temperature)
	}
	if capturedConfig.MaxOutputTokens == nil || *capturedConfig.MaxOutputTokens != 2048 {
		t.Errorf("expected max tokens 2048, got %v", capturedConfig.MaxOutputTokens)
	}
	if capturedConfig.ThinkingConfig == nil || capturedConfig.ThinkingConfig.ThinkingBudget != 16384 {
		t.Errorf("expected thinking budget 16384, got %v", capturedConfig.ThinkingConfig)
	}
}
```

- [ ] **Step 2: Write google.go implementation**

```go
// providers/google/google.go
package google

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// modelsClient abstracts the genai client methods used by GoogleLLM.
// In production, genai.Client.Models satisfies this interface.
// In tests, a fake implementation is injected.
type modelsClient interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
	GenerateContentStream(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]
}

// GoogleLLM implements kernel.LLM for Google Gemini models via the genai SDK.
type GoogleLLM struct {
	mc             modelsClient
	model          string
	safetySettings []*genai.SafetySetting
	thinkingBudget int32
	cachedContent  *string
}

// Compile-time check that GoogleLLM implements kernel.LLM.
var _ kernel.LLM = (*GoogleLLM)(nil)

// New creates a GoogleLLM using the given genai client and model name.
// The client may be nil in testing scenarios where mc is overridden directly.
func New(client *genai.Client, model string, opts ...Option) *GoogleLLM {
	g := &GoogleLLM{
		model: model,
	}
	if client != nil {
		g.mc = client.Models
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Option configures a GoogleLLM at construction time.
type Option func(*GoogleLLM)

// WithSafetySettings configures safety settings applied to every request.
func WithSafetySettings(settings []*genai.SafetySetting) Option {
	return func(g *GoogleLLM) {
		g.safetySettings = settings
	}
}

// WithThinkingBudget sets a default thinking budget (overridden by ReasoningLevel in options).
func WithThinkingBudget(tokens int32) Option {
	return func(g *GoogleLLM) {
		g.thinkingBudget = tokens
	}
}

// WithCachedContent sets the cached content resource name for requests.
func WithCachedContent(name string) Option {
	return func(g *GoogleLLM) {
		g.cachedContent = &name
	}
}

// Model returns the model name.
func (g *GoogleLLM) Model() string {
	return g.model
}

// Generate makes a non-streaming call to the Gemini API and returns a kernel.Response.
func (g *GoogleLLM) Generate(ctx context.Context, params kernel.GenerateParams) (kernel.Response, error) {
	contents, sysInstruction := convertMessages(params.Messages)

	cfg := buildConfig(params.Options, g.safetySettings, g.cachedContent)
	cfg.SystemInstruction = sysInstruction

	if len(params.Tools) > 0 {
		cfg.Tools = convertTools(params.Tools)
		if tc := convertToolChoice(params.Options.ToolChoice); tc != nil {
			cfg.ToolConfig = tc
		}
	}

	if g.thinkingBudget > 0 && cfg.ThinkingConfig == nil {
		cfg.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget: g.thinkingBudget,
		}
	}

	start := time.Now()
	genaiResp, err := g.mc.GenerateContent(ctx, g.model, contents, cfg)
	latency := time.Since(start)

	if err != nil {
		return kernel.Response{}, fmt.Errorf("google generate: %w", err)
	}

	resp := convertResponse(genaiResp)
	resp.Usage.Latency = latency
	return resp, nil
}

// GenerateStream makes a streaming call to the Gemini API and returns a kernel.Stream.
func (g *GoogleLLM) GenerateStream(ctx context.Context, params kernel.GenerateParams) (kernel.Stream, error) {
	contents, sysInstruction := convertMessages(params.Messages)

	cfg := buildConfig(params.Options, g.safetySettings, g.cachedContent)
	cfg.SystemInstruction = sysInstruction

	if len(params.Tools) > 0 {
		cfg.Tools = convertTools(params.Tools)
		if tc := convertToolChoice(params.Options.ToolChoice); tc != nil {
			cfg.ToolConfig = tc
		}
	}

	if g.thinkingBudget > 0 && cfg.ThinkingConfig == nil {
		cfg.ThinkingConfig = &genai.ThinkingConfig{
			ThinkingBudget: g.thinkingBudget,
		}
	}

	iterator := g.mc.GenerateContentStream(ctx, g.model, contents, cfg)

	return newStream(iterator), nil
}
```

- [ ] **Step 3: Run tests and fix any issues**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -run TestNew -v`
Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -run TestGenerate -v`

Note: The `fakeModelsClient` and `fakeKernelTool` types are defined in test files. Since both `convert_test.go` and `google_test.go` are in the same package, `fakeKernelTool` must be defined only once. Move it to a shared test helper or keep it in `convert_test.go` (which is compiled alongside `google_test.go` in the same package). If there is a name collision, rename one.

Also verify the `iter.Seq2` import resolves correctly. In Go 1.25, `iter` is a standard library package. The genai SDK's `GenerateContentStream` returns `iter.Seq2[*genai.GenerateContentResponse, error]`. Confirm this signature matches the actual SDK by checking `go doc google.golang.org/genai.Models.GenerateContentStream` after `go mod tidy`.

- [ ] **Step 4: Run the full test suite**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -v -count=1`

---

### Task 3: Implement Stream wrapper

**Files:**
- Create: `providers/google/stream.go`
- Create: `providers/google/stream_test.go`

The stream wraps the genai iterator (`iter.Seq2[*genai.GenerateContentResponse, error]`) into a `kernel.Stream`. It runs the iterator in a goroutine, emitting `TextDeltaEvent` for each text chunk and accumulating the final `Response`.

- [ ] **Step 1: Write stream_test.go**

```go
// providers/google/stream_test.go
package google

import (
	"iter"
	"testing"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

func TestStream_TextOnly(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []genai.Part{genai.Text("Hello ")}},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content:      &genai.Content{Parts: []genai.Part{genai.Text("world")}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     5,
				CandidatesTokenCount: 3,
				TotalTokenCount:      8,
			},
		},
	}

	iterator := makeTestIterator(chunks, nil)
	s := newStream(iterator)

	// Collect text deltas
	var texts []string
	for txt := range s.Text() {
		texts = append(texts, txt)
	}

	if len(texts) != 2 {
		t.Fatalf("expected 2 text chunks, got %d", len(texts))
	}
	if texts[0] != "Hello " {
		t.Errorf("expected first chunk 'Hello ', got %q", texts[0])
	}
	if texts[1] != "world" {
		t.Errorf("expected second chunk 'world', got %q", texts[1])
	}

	resp := s.Response()
	if resp.Text != "Hello world" {
		t.Errorf("expected full text 'Hello world', got %q", resp.Text)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 5 {
		t.Errorf("expected 5 input tokens, got %d", resp.Usage.InputTokens)
	}
	if s.Err() != nil {
		t.Errorf("unexpected error: %v", s.Err())
	}
}

func TestStream_Events(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []genai.Part{genai.Text("chunk1")}},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content:      &genai.Content{Parts: []genai.Part{genai.Text("chunk2")}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
		},
	}

	iterator := makeTestIterator(chunks, nil)
	s := newStream(iterator)

	var events []kernel.StreamEvent
	for ev := range s.Events() {
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	delta1, ok := events[0].(kernel.TextDeltaEvent)
	if !ok {
		t.Fatalf("expected TextDeltaEvent, got %T", events[0])
	}
	if delta1.Text != "chunk1" {
		t.Errorf("expected 'chunk1', got %q", delta1.Text)
	}
}

func TestStream_WithToolCalls(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []genai.Part{
					genai.Text("Let me search for that. "),
				}},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []genai.Part{
					genai.FunctionCall{
						Name: "search",
						Args: map[string]any{"query": "weather"},
					},
				}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 8,
				TotalTokenCount:      18,
			},
		},
	}

	iterator := makeTestIterator(chunks, nil)
	s := newStream(iterator)

	// Drain events
	for range s.Events() {
	}

	resp := s.Response()
	if resp.Text != "Let me search for that. " {
		t.Errorf("expected text 'Let me search for that. ', got %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "search" {
		t.Errorf("expected tool call name 'search', got %q", resp.ToolCalls[0].Name)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason)
	}
}

func TestStream_Error(t *testing.T) {
	chunks := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{Parts: []genai.Part{genai.Text("partial")}},
			}},
		},
	}

	iterator := makeTestIterator(chunks, fmt.Errorf("connection reset"))
	s := newStream(iterator)

	// Drain
	for range s.Events() {
	}

	if s.Err() == nil {
		t.Fatal("expected error, got nil")
	}
	if s.Err().Error() != "connection reset" {
		t.Errorf("unexpected error: %v", s.Err())
	}

	// Partial text should still be available
	resp := s.Response()
	if resp.Text != "partial" {
		t.Errorf("expected partial text 'partial', got %q", resp.Text)
	}
}

func TestStream_Empty(t *testing.T) {
	iterator := makeTestIterator(nil, nil)
	s := newStream(iterator)

	for range s.Events() {
	}

	resp := s.Response()
	if resp.Text != "" {
		t.Errorf("expected empty text, got %q", resp.Text)
	}
	if s.Err() != nil {
		t.Errorf("unexpected error: %v", s.Err())
	}
}

// makeTestIterator creates a test iterator from a slice of responses and an optional final error.
func makeTestIterator(chunks []*genai.GenerateContentResponse, finalErr error) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		for _, chunk := range chunks {
			if !yield(chunk, nil) {
				return
			}
		}
		if finalErr != nil {
			yield(nil, finalErr)
		}
	}
}
```

Note: `stream_test.go` uses `fmt.Errorf` -- add the `"fmt"` import.

- [ ] **Step 2: Write stream.go implementation**

```go
// providers/google/stream.go
package google

import (
	"encoding/json"
	"fmt"
	"iter"
	"sync"

	"github.com/axonframework/axon/kernel"
	"google.golang.org/genai"
)

// stream implements kernel.Stream by wrapping a genai iterator.
type stream struct {
	eventCh chan kernel.StreamEvent
	textCh  chan string

	resp kernel.Response
	err  error
	done chan struct{}
	mu   sync.Mutex
}

// Compile-time check that stream implements kernel.Stream.
var _ kernel.Stream = (*stream)(nil)

// newStream creates a stream from a genai iterator and starts consuming it
// in a background goroutine. Text parts are emitted as TextDeltaEvents.
// Tool calls are accumulated. The final Response is built from all chunks.
func newStream(iterator iter.Seq2[*genai.GenerateContentResponse, error]) *stream {
	s := &stream{
		eventCh: make(chan kernel.StreamEvent, 64),
		textCh:  make(chan string, 64),
		done:    make(chan struct{}),
	}

	go s.consume(iterator)

	return s
}

// consume reads chunks from the iterator and populates the stream channels.
func (s *stream) consume(iterator iter.Seq2[*genai.GenerateContentResponse, error]) {
	defer close(s.eventCh)
	defer close(s.textCh)
	defer close(s.done)

	var (
		fullText     string
		toolCalls    []kernel.ToolCall
		finishReason string
		usage        kernel.Usage
		callIdx      int
	)

	for chunk, err := range iterator {
		if err != nil {
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
			break
		}

		if chunk == nil {
			continue
		}

		// Extract usage from the last chunk that has it
		if chunk.UsageMetadata != nil {
			usage = kernel.Usage{
				InputTokens:  int(chunk.UsageMetadata.PromptTokenCount),
				OutputTokens: int(chunk.UsageMetadata.CandidatesTokenCount),
				TotalTokens:  int(chunk.UsageMetadata.TotalTokenCount),
			}
		}

		if len(chunk.Candidates) == 0 {
			continue
		}

		candidate := chunk.Candidates[0]

		if candidate.FinishReason != 0 {
			finishReason = convertFinishReason(candidate.FinishReason)
		}

		if candidate.Content == nil {
			continue
		}

		for _, part := range candidate.Content.Parts {
			switch p := part.(type) {
			case genai.Text:
				txt := string(p)
				fullText += txt
				s.textCh <- txt
				s.eventCh <- kernel.TextDeltaEvent{Text: txt}

			case genai.FunctionCall:
				params, _ := json.Marshal(p.Args)
				toolCalls = append(toolCalls, kernel.ToolCall{
					ID:     fmt.Sprintf("call_%d", callIdx),
					Name:   p.Name,
					Params: params,
				})
				callIdx++
			}
		}
	}

	// Override finish reason if tool calls were accumulated
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if finishReason == "" {
		finishReason = "stop"
	}

	s.mu.Lock()
	s.resp = kernel.Response{
		Text:         fullText,
		ToolCalls:    toolCalls,
		Usage:        usage,
		FinishReason: finishReason,
	}
	s.mu.Unlock()
}

// Events returns the channel of stream events.
func (s *stream) Events() <-chan kernel.StreamEvent {
	return s.eventCh
}

// Text returns the channel of text deltas.
func (s *stream) Text() <-chan string {
	return s.textCh
}

// Response blocks until the stream completes and returns the accumulated response.
func (s *stream) Response() kernel.Response {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resp
}

// Err blocks until the stream completes and returns any error encountered.
func (s *stream) Err() error {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
```

- [ ] **Step 3: Run stream tests**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -run TestStream -v`

- [ ] **Step 4: Run full test suite to verify nothing is broken**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -v -count=1`

---

### Task 4: Add GenerateStream test coverage and verify complete integration

**Files:**
- Edit: `providers/google/google_test.go` (add streaming tests)

- [ ] **Step 1: Add GenerateStream tests to google_test.go**

Append the following tests to `providers/google/google_test.go`:

```go
func TestGenerateStream_TextOnly(t *testing.T) {
	fake := &fakeModelsClient{
		generateStreamFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
			return makeTestIterator([]*genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{{
						Content: &genai.Content{Parts: []genai.Part{genai.Text("Hello ")}},
					}},
				},
				{
					Candidates: []*genai.Candidate{{
						Content:      &genai.Content{Parts: []genai.Part{genai.Text("world")}},
						FinishReason: genai.FinishReasonStop,
					}},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:     5,
						CandidatesTokenCount: 3,
						TotalTokenCount:      8,
					},
				},
			}, nil)
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	stream, err := llm.GenerateStream(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain events
	var texts []string
	for txt := range stream.Text() {
		texts = append(texts, txt)
	}

	if len(texts) != 2 {
		t.Fatalf("expected 2 text chunks, got %d", len(texts))
	}

	resp := stream.Response()
	if resp.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", resp.Text)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Errorf("expected 8 total tokens, got %d", resp.Usage.TotalTokens)
	}
	if stream.Err() != nil {
		t.Errorf("unexpected error: %v", stream.Err())
	}
}

func TestGenerateStream_Error(t *testing.T) {
	fake := &fakeModelsClient{
		generateStreamFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
			return makeTestIterator(nil, fmt.Errorf("stream error"))
		},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	stream, err := llm.GenerateStream(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{kernel.UserMsg("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error from GenerateStream: %v", err)
	}

	// Drain
	for range stream.Events() {
	}

	if stream.Err() == nil {
		t.Fatal("expected stream error, got nil")
	}
}

func TestGenerateStream_WithSystemAndTools(t *testing.T) {
	var capturedConfig *genai.GenerateContentConfig

	fake := &fakeModelsClient{
		generateStreamFn: func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
			capturedConfig = config
			return makeTestIterator([]*genai.GenerateContentResponse{
				{
					Candidates: []*genai.Candidate{{
						Content:      &genai.Content{Parts: []genai.Part{genai.Text("ok")}},
						FinishReason: genai.FinishReasonStop,
					}},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{},
				},
			}, nil)
		},
	}

	tool := &fakeKernelTool{
		name: "lookup",
		desc: "Look things up",
		sch:  kernel.Schema{Type: "object"},
	}

	llm := &GoogleLLM{model: "gemini-2.0-flash", mc: fake}
	stream, err := llm.GenerateStream(context.Background(), kernel.GenerateParams{
		Messages: []kernel.Message{
			kernel.SystemMsg("Be concise"),
			kernel.UserMsg("test"),
		},
		Tools: []kernel.Tool{tool},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for range stream.Events() {
	}

	if capturedConfig.SystemInstruction == nil {
		t.Error("expected system instruction in streaming config")
	}
	if len(capturedConfig.Tools) != 1 {
		t.Errorf("expected 1 tool group, got %d", len(capturedConfig.Tools))
	}
}
```

- [ ] **Step 2: Run full suite**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -v -count=1`

Verify all tests pass. The expected total:
- convert_test.go: ~20 tests (schema, tool, toolchoice, message, response, config)
- google_test.go: ~10 tests (New, Generate variants, GenerateStream variants)
- stream_test.go: ~5 tests (text, events, tool calls, error, empty)

- [ ] **Step 3: Run `go vet` and check for lint issues**

Run: `cd /Users/derek/repo/axons/providers/google && go vet ./...`

Fix any issues reported.

---

### Task 5: Self-review and edge case hardening

- [ ] **Step 1: Review all files for completeness**

Verify the following by reading each file:

1. **go.mod** -- has correct module path, replace directive, go version
2. **convert.go** -- all kernel types mapped, schema recursion works, no panic paths
3. **google.go** -- implements all three LLM methods, options applied correctly, latency tracked
4. **stream.go** -- channels closed properly, no goroutine leak, both Events and Text usable independently

- [ ] **Step 2: Verify edge cases are covered**

Check these scenarios have test coverage (add tests if missing):

1. Empty message list passed to convertMessages
2. Message with mixed content parts (text + image in same message)
3. Schema with deeply nested objects (object inside object)
4. ToolChoice zero value returns nil (no constraint sent to API)
5. Response with no candidates
6. Response with nil UsageMetadata
7. Stream that yields zero chunks
8. Stream error after partial data

- [ ] **Step 3: Add a nested schema test if not already covered**

If not yet tested, add to convert_test.go:

```go
func TestConvertSchema_NestedObject(t *testing.T) {
	s := kernel.Schema{
		Type: "object",
		Properties: map[string]kernel.Schema{
			"address": {
				Type: "object",
				Properties: map[string]kernel.Schema{
					"street": {Type: "string"},
					"city":   {Type: "string"},
					"zip":    {Type: "string"},
				},
				Required: []string{"street", "city"},
			},
		},
		Required: []string{"address"},
	}
	gs := convertSchema(s)
	addr, ok := gs.Properties["address"]
	if !ok {
		t.Fatal("expected 'address' property")
	}
	if addr.Type != genai.TypeObject {
		t.Errorf("expected TypeObject for address, got %v", addr.Type)
	}
	if len(addr.Properties) != 3 {
		t.Errorf("expected 3 address properties, got %d", len(addr.Properties))
	}
	street, ok := addr.Properties["street"]
	if !ok {
		t.Fatal("expected 'street' sub-property")
	}
	if street.Type != genai.TypeString {
		t.Errorf("expected TypeString for street, got %v", street.Type)
	}
}
```

- [ ] **Step 4: Add a mixed content parts test if not already covered**

If not yet tested, add to convert_test.go:

```go
func TestConvertMessages_MixedParts(t *testing.T) {
	msgs := []kernel.Message{
		{Role: kernel.RoleUser, Content: []kernel.ContentPart{
			{Text: strPtr("Describe this image:")},
			{Image: &kernel.ImageContent{URL: "gs://bucket/photo.jpg", MimeType: "image/jpeg"}},
		}},
	}

	contents, _ := convertMessages(msgs)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if len(contents[0].Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(contents[0].Parts))
	}
	if _, ok := contents[0].Parts[0].(genai.Text); !ok {
		t.Errorf("expected first part to be Text, got %T", contents[0].Parts[0])
	}
	if _, ok := contents[0].Parts[1].(genai.FileData); !ok {
		t.Errorf("expected second part to be FileData, got %T", contents[0].Parts[1])
	}
}
```

- [ ] **Step 5: Final full test run**

Run: `cd /Users/derek/repo/axons/providers/google && go test ./... -v -count=1`
Run: `cd /Users/derek/repo/axons/providers/google && go vet ./...`

All tests must pass. No vet warnings.

- [ ] **Step 6: Verify interface compliance**

The compile-time checks in the source code ensure this, but verify manually:

```bash
cd /Users/derek/repo/axons/providers/google && go build ./...
```

This must succeed with zero errors, confirming `GoogleLLM` satisfies `kernel.LLM` and `stream` satisfies `kernel.Stream`.

---

## Implementation Notes

**genai SDK version:** The exact SDK version will be pinned by `go mod tidy`. The plan uses field names from the `google.golang.org/genai` unified SDK (not the older `cloud.google.com/go/vertexai` or `github.com/google/generative-ai-go` packages). If any field names differ from the actual SDK, adjust during implementation.

**`iter.Seq2` usage:** Go 1.25 has `iter` in stdlib. The genai SDK's `GenerateContentStream` returns `iter.Seq2[*genai.GenerateContentResponse, error]`. This is a function type: `func(yield func(*genai.GenerateContentResponse, error) bool)`. The stream consumer calls this function with a yield callback via range-over-func syntax.

**`genai.Ptr` helper:** The genai SDK may or may not export a `Ptr[T]` helper. If it does not exist, define a local generic helper:
```go
func ptr[T any](v T) *T { return &v }
```
And use it in place of `genai.Ptr`.

**Tool call IDs:** The Gemini API does not return tool call IDs. The provider generates synthetic IDs using `fmt.Sprintf("call_%d", index)`. This is deterministic per response, which is sufficient for the kernel's tool dispatch loop (it matches results back to calls by index/name, not by opaque ID).

**Stream channel sizing:** Both `eventCh` and `textCh` are buffered with capacity 64. This prevents the producer goroutine from blocking on slow consumers for typical response sizes. The consumer must drain at least one channel (Events or Text) to prevent deadlock. This matches the kernel's StreamResult pattern.

**Error wrapping:** Generate errors are wrapped with `fmt.Errorf("google generate: %w", err)` for clear provenance. The stream does not wrap errors since they come from the iterator and are already descriptive.

**No retry/caching:** Per the spec, retry logic belongs in `middleware.WithRetry`, not in the provider. The provider is a thin adapter only.
