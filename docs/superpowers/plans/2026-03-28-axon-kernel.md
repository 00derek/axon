# Axon Kernel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Axon kernel package — the foundation of the agentic framework: LLM interface, Message types, Tool system with generics, AgentContext, Schema generation, and the Agent loop with hooks and streaming.

**Architecture:** Single Go package `kernel/` with zero external dependencies (stdlib only). Types are defined bottom-up: Message → Schema → Tool → LLM → AgentContext → Agent. The agent loop implements a ReAct pattern with configurable hooks and streaming.

**Tech Stack:** Go 1.25, stdlib only (no external deps in kernel)

**Source spec:** `docs/superpowers/specs/2026-03-28-axon-framework-design.md`, Sections 3.1–3.6

---

## File Structure

```
kernel/
├── message.go          # Role, Message, ContentPart, ToolCall, ToolResult, constructors
├── message_test.go
├── schema.go           # Schema type, SchemaFrom[T](), struct tag reflection
├── schema_test.go
├── tool.go             # Tool interface, NewTool[P,R](), Guided[T], Guide()
├── tool_test.go
├── llm.go              # LLM interface, GenerateParams, GenerateOptions, Response, Usage, ToolChoice
├── stream.go           # Stream interface, StreamEvent types, streamResult implementation
├── stream_test.go
├── context.go          # AgentContext, EnableTools, DisableTools, AddMessages, etc.
├── context_test.go
├── agent.go            # Agent, NewAgent, AgentOption, hooks, Run()
├── agent_test.go
├── agent_stream.go     # Agent.Stream(), StreamResult
├── agent_stream_test.go
└── go.mod              # module github.com/axonframework/axon/kernel
```

---

### Task 1: Initialize Go module and Message types

**Files:**
- Create: `kernel/go.mod`
- Create: `kernel/message.go`
- Create: `kernel/message_test.go`

- [ ] **Step 1: Write the test file**

```go
// kernel/message_test.go
package kernel

import (
	"testing"
)

func TestSystemMsg(t *testing.T) {
	msg := SystemMsg("You are helpful")
	if msg.Role != RoleSystem {
		t.Errorf("expected role %q, got %q", RoleSystem, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(msg.Content))
	}
	if msg.Content[0].Text == nil || *msg.Content[0].Text != "You are helpful" {
		t.Errorf("expected text %q, got %v", "You are helpful", msg.Content[0].Text)
	}
}

func TestUserMsg(t *testing.T) {
	msg := UserMsg("Hello")
	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text == nil || *msg.Content[0].Text != "Hello" {
		t.Errorf("unexpected content: %+v", msg.Content)
	}
}

func TestAssistantMsg(t *testing.T) {
	msg := AssistantMsg("Hi there")
	if msg.Role != RoleAssistant {
		t.Errorf("expected role %q, got %q", RoleAssistant, msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text == nil || *msg.Content[0].Text != "Hi there" {
		t.Errorf("unexpected content: %+v", msg.Content)
	}
}

func TestToolResultMsg(t *testing.T) {
	type result struct {
		Count int `json:"count"`
	}
	msg := ToolResultMsg("call-123", "search", result{Count: 3})
	if msg.Role != RoleTool {
		t.Errorf("expected role %q, got %q", RoleTool, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(msg.Content))
	}
	tr := msg.Content[0].ToolResult
	if tr == nil {
		t.Fatal("expected ToolResult content part")
	}
	if tr.ToolCallID != "call-123" {
		t.Errorf("expected ToolCallID %q, got %q", "call-123", tr.ToolCallID)
	}
	if tr.Name != "search" {
		t.Errorf("expected Name %q, got %q", "search", tr.Name)
	}
	if tr.Content != `{"count":3}` {
		t.Errorf("expected Content %q, got %q", `{"count":3}`, tr.Content)
	}
	if tr.IsError {
		t.Error("expected IsError false")
	}
}

func TestToolResultMsgError(t *testing.T) {
	msg := ToolResultMsg("call-456", "search", "something went wrong")
	tr := msg.Content[0].ToolResult
	if tr.Content != `"something went wrong"` {
		t.Errorf("expected Content %q, got %q", `"something went wrong"`, tr.Content)
	}
}

func TestMessageTextHelper(t *testing.T) {
	msg := SystemMsg("hello")
	if msg.TextContent() != "hello" {
		t.Errorf("expected %q, got %q", "hello", msg.TextContent())
	}

	// Message with no text
	msg2 := Message{Role: RoleAssistant, Content: []ContentPart{{ToolCall: &ToolCall{ID: "1", Name: "test"}}}}
	if msg2.TextContent() != "" {
		t.Errorf("expected empty string, got %q", msg2.TextContent())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v`
Expected: FAIL (package does not exist yet)

- [ ] **Step 3: Create go.mod**

```
cd /Users/derek/repo/ai-agent/kernel && go mod init github.com/axonframework/axon/kernel
```

- [ ] **Step 4: Write the implementation**

```go
// kernel/message.go
package kernel

import (
	"encoding/json"
)

// Role represents a message participant type.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message in a conversation.
type Message struct {
	Role     Role           `json:"role"`
	Content  []ContentPart  `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TextContent returns the concatenated text from all text content parts.
func (m Message) TextContent() string {
	var s string
	for _, c := range m.Content {
		if c.Text != nil {
			s += *c.Text
		}
	}
	return s
}

// ContentPart is a tagged union — exactly one field should be set.
type ContentPart struct {
	Text       *string       `json:"text,omitempty"`
	Image      *ImageContent `json:"image,omitempty"`
	ToolCall   *ToolCall     `json:"tool_call,omitempty"`
	ToolResult *ToolResult   `json:"tool_result,omitempty"`
}

// ImageContent holds image data for multimodal messages.
type ImageContent struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type,omitempty"`
}

// ToolCall represents an LLM's request to invoke a tool.
type ToolCall struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ToolResult represents the output of a tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

func textPtr(s string) *string {
	return &s
}

// SystemMsg creates a system message with the given text.
func SystemMsg(text string) Message {
	return Message{
		Role:    RoleSystem,
		Content: []ContentPart{{Text: textPtr(text)}},
	}
}

// UserMsg creates a user message with the given text.
func UserMsg(text string) Message {
	return Message{
		Role:    RoleUser,
		Content: []ContentPart{{Text: textPtr(text)}},
	}
}

// AssistantMsg creates an assistant message with the given text.
func AssistantMsg(text string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: []ContentPart{{Text: textPtr(text)}},
	}
}

// ToolResultMsg creates a tool result message. The content value is JSON-serialized.
func ToolResultMsg(callID, name string, content any) Message {
	data, _ := json.Marshal(content)
	return Message{
		Role: RoleTool,
		Content: []ContentPart{{
			ToolResult: &ToolResult{
				ToolCallID: callID,
				Name:       name,
				Content:    string(data),
			},
		}},
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v`
Expected: PASS (all 6 tests)

- [ ] **Step 6: Commit**

```bash
git add kernel/
git commit -m "feat(kernel): add Message types and constructors"
```

---

### Task 2: Schema generation from struct tags

**Files:**
- Create: `kernel/schema.go`
- Create: `kernel/schema_test.go`

- [ ] **Step 1: Write the test file**

```go
// kernel/schema_test.go
package kernel

import (
	"encoding/json"
	"testing"
)

func TestSchemaFromSimpleStruct(t *testing.T) {
	type Params struct {
		Query    string `json:"query" description:"Search query"`
		Location string `json:"location" description:"Area to search"`
	}

	schema := SchemaFrom[Params]()

	if schema.Type != "object" {
		t.Errorf("expected type %q, got %q", "object", schema.Type)
	}
	if len(schema.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(schema.Properties))
	}

	q, ok := schema.Properties["query"]
	if !ok {
		t.Fatal("missing property 'query'")
	}
	if q.Type != "string" {
		t.Errorf("query: expected type %q, got %q", "string", q.Type)
	}
	if q.Description != "Search query" {
		t.Errorf("query: expected description %q, got %q", "Search query", q.Description)
	}

	// Both fields should be required by default
	if len(schema.Required) != 2 {
		t.Errorf("expected 2 required fields, got %d: %v", len(schema.Required), schema.Required)
	}
}

func TestSchemaFromOptionalField(t *testing.T) {
	type Params struct {
		Name  string `json:"name" description:"User name"`
		Email string `json:"email,omitempty" description:"Email" required:"false"`
	}

	schema := SchemaFrom[Params]()

	if len(schema.Required) != 1 {
		t.Fatalf("expected 1 required field, got %d: %v", len(schema.Required), schema.Required)
	}
	if schema.Required[0] != "name" {
		t.Errorf("expected required field %q, got %q", "name", schema.Required[0])
	}
}

func TestSchemaFromNumericConstraints(t *testing.T) {
	type Params struct {
		Count int `json:"count" description:"Number of items" minimum:"1" maximum:"100"`
	}

	schema := SchemaFrom[Params]()
	prop := schema.Properties["count"]

	if prop.Type != "integer" {
		t.Errorf("expected type %q, got %q", "integer", prop.Type)
	}
	if prop.Minimum == nil || *prop.Minimum != 1 {
		t.Errorf("expected minimum 1, got %v", prop.Minimum)
	}
	if prop.Maximum == nil || *prop.Maximum != 100 {
		t.Errorf("expected maximum 100, got %v", prop.Maximum)
	}
}

func TestSchemaFromEnum(t *testing.T) {
	type Params struct {
		Color string `json:"color" description:"Pick a color" enum:"red,green,blue"`
	}

	schema := SchemaFrom[Params]()
	prop := schema.Properties["color"]

	if len(prop.Enum) != 3 {
		t.Fatalf("expected 3 enum values, got %d", len(prop.Enum))
	}
	if prop.Enum[0] != "red" || prop.Enum[1] != "green" || prop.Enum[2] != "blue" {
		t.Errorf("unexpected enum values: %v", prop.Enum)
	}
}

func TestSchemaFromNestedStruct(t *testing.T) {
	type Address struct {
		Street string `json:"street" description:"Street address"`
		City   string `json:"city" description:"City name"`
	}
	type Params struct {
		Name    string  `json:"name" description:"User name"`
		Address Address `json:"address" description:"Home address"`
	}

	schema := SchemaFrom[Params]()
	addr, ok := schema.Properties["address"]
	if !ok {
		t.Fatal("missing property 'address'")
	}
	if addr.Type != "object" {
		t.Errorf("address: expected type %q, got %q", "object", addr.Type)
	}
	if len(addr.Properties) != 2 {
		t.Errorf("address: expected 2 properties, got %d", len(addr.Properties))
	}
	street, ok := addr.Properties["street"]
	if !ok {
		t.Fatal("missing nested property 'street'")
	}
	if street.Description != "Street address" {
		t.Errorf("street: expected description %q, got %q", "Street address", street.Description)
	}
}

func TestSchemaFromSlice(t *testing.T) {
	type Params struct {
		Tags []string `json:"tags" description:"Tag list"`
	}

	schema := SchemaFrom[Params]()
	prop := schema.Properties["tags"]

	if prop.Type != "array" {
		t.Errorf("expected type %q, got %q", "array", prop.Type)
	}
	if prop.Items == nil || prop.Items.Type != "string" {
		t.Errorf("expected items type %q, got %v", "string", prop.Items)
	}
}

func TestSchemaFromBool(t *testing.T) {
	type Params struct {
		Verbose bool `json:"verbose" description:"Enable verbose output"`
	}

	schema := SchemaFrom[Params]()
	prop := schema.Properties["verbose"]
	if prop.Type != "boolean" {
		t.Errorf("expected type %q, got %q", "boolean", prop.Type)
	}
}

func TestSchemaFromFloat(t *testing.T) {
	type Params struct {
		Score float64 `json:"score" description:"Quality score"`
	}

	schema := SchemaFrom[Params]()
	prop := schema.Properties["score"]
	if prop.Type != "number" {
		t.Errorf("expected type %q, got %q", "number", prop.Type)
	}
}

func TestSchemaJSON(t *testing.T) {
	type Params struct {
		Query string `json:"query" description:"Search query"`
	}

	schema := SchemaFrom[Params]()
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded Schema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.Properties["query"].Type != "string" {
		t.Error("round-trip failed")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestSchema`
Expected: FAIL (Schema type not defined)

- [ ] **Step 3: Write the implementation**

```go
// kernel/schema.go
package kernel

import (
	"reflect"
	"strconv"
	"strings"
)

// Schema represents a JSON Schema subset for tool parameter definitions.
type Schema struct {
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]Schema `json:"properties,omitempty"`
	Required    []string          `json:"required,omitempty"`
	Items       *Schema           `json:"items,omitempty"`
	Enum        []string          `json:"enum,omitempty"`
	Minimum     *float64          `json:"minimum,omitempty"`
	Maximum     *float64          `json:"maximum,omitempty"`
}

// SchemaFrom generates a Schema from a Go struct's type and tags.
func SchemaFrom[T any]() Schema {
	var zero T
	return schemaFromType(reflect.TypeOf(zero))
}

func schemaFromType(t reflect.Type) Schema {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return Schema{Type: goTypeToJSONType(t.Kind())}
	}

	schema := Schema{
		Type:       "object",
		Properties: make(map[string]Schema),
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		name := field.Name
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				name = parts[0]
			}
		}

		prop := buildPropertySchema(field)
		schema.Properties[name] = prop

		// Required by default unless required:"false"
		reqTag := field.Tag.Get("required")
		if reqTag != "false" {
			schema.Required = append(schema.Required, name)
		}
	}

	return schema
}

func buildPropertySchema(field reflect.StructField) Schema {
	ft := field.Type
	if ft.Kind() == reflect.Ptr {
		ft = ft.Elem()
	}

	var prop Schema

	switch ft.Kind() {
	case reflect.Struct:
		prop = schemaFromType(ft)
	case reflect.Slice:
		prop = Schema{
			Type:  "array",
			Items: ptrSchema(schemaFromType(ft.Elem())),
		}
	default:
		prop = Schema{Type: goTypeToJSONType(ft.Kind())}
	}

	if desc := field.Tag.Get("description"); desc != "" {
		prop.Description = desc
	}

	if enumTag := field.Tag.Get("enum"); enumTag != "" {
		prop.Enum = strings.Split(enumTag, ",")
	}

	if minTag := field.Tag.Get("minimum"); minTag != "" {
		if v, err := strconv.ParseFloat(minTag, 64); err == nil {
			prop.Minimum = &v
		}
	}

	if maxTag := field.Tag.Get("maximum"); maxTag != "" {
		if v, err := strconv.ParseFloat(maxTag, 64); err == nil {
			prop.Maximum = &v
		}
	}

	return prop
}

func goTypeToJSONType(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	default:
		return "string"
	}
}

func ptrSchema(s Schema) *Schema {
	return &s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestSchema`
Expected: PASS (all 9 tests)

- [ ] **Step 5: Commit**

```bash
git add kernel/schema.go kernel/schema_test.go
git commit -m "feat(kernel): add Schema type with struct tag reflection"
```

---

### Task 3: Tool interface, NewTool with generics, and Guided output

**Files:**
- Create: `kernel/tool.go`
- Create: `kernel/tool_test.go`

- [ ] **Step 1: Write the test file**

```go
// kernel/tool_test.go
package kernel

import (
	"context"
	"encoding/json"
	"testing"
)

type searchParams struct {
	Query    string `json:"query" description:"Search query"`
	Location string `json:"location" description:"Area to search"`
}

type searchResult struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func TestNewToolBasic(t *testing.T) {
	tool := NewTool("search", "Search for things",
		func(ctx context.Context, p searchParams) ([]searchResult, error) {
			return []searchResult{{Name: "Result 1", ID: "r1"}}, nil
		},
	)

	if tool.Name() != "search" {
		t.Errorf("expected name %q, got %q", "search", tool.Name())
	}
	if tool.Description() != "Search for things" {
		t.Errorf("expected description %q, got %q", "Search for things", tool.Description())
	}

	schema := tool.Schema()
	if schema.Type != "object" {
		t.Errorf("expected schema type %q, got %q", "object", schema.Type)
	}
	if _, ok := schema.Properties["query"]; !ok {
		t.Error("schema missing 'query' property")
	}
}

func TestNewToolExecute(t *testing.T) {
	tool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) ([]searchResult, error) {
			if p.Query != "thai" {
				t.Errorf("expected query %q, got %q", "thai", p.Query)
			}
			return []searchResult{{Name: "Thai Basil", ID: "r1"}}, nil
		},
	)

	params := json.RawMessage(`{"query":"thai","location":"SF"}`)
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	results, ok := result.([]searchResult)
	if !ok {
		t.Fatalf("expected []searchResult, got %T", result)
	}
	if len(results) != 1 || results[0].Name != "Thai Basil" {
		t.Errorf("unexpected results: %+v", results)
	}
}

func TestNewToolExecuteInvalidJSON(t *testing.T) {
	tool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) ([]searchResult, error) {
			return nil, nil
		},
	)

	params := json.RawMessage(`{invalid}`)
	_, err := tool.Execute(context.Background(), params)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestNewToolGuided(t *testing.T) {
	tool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (Guided[[]searchResult], error) {
			results := []searchResult{{Name: "Thai Basil", ID: "r1"}}
			return Guide(results, "Found %d restaurants. Ask user to pick.", len(results)), nil
		},
	)

	params := json.RawMessage(`{"query":"thai","location":"SF"}`)
	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	guided, ok := result.(Guided[[]searchResult])
	if !ok {
		t.Fatalf("expected Guided[[]searchResult], got %T", result)
	}
	if len(guided.Data) != 1 {
		t.Errorf("expected 1 result, got %d", len(guided.Data))
	}
	if guided.Guidance != "Found 1 restaurants. Ask user to pick." {
		t.Errorf("unexpected guidance: %q", guided.Guidance)
	}
}

func TestToolResultSerialization(t *testing.T) {
	result := []searchResult{{Name: "Thai Basil", ID: "r1"}}
	content := SerializeToolResult(result)
	if content == "" {
		t.Error("expected non-empty content")
	}

	var decoded []searchResult
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		t.Fatalf("content should be valid JSON: %v", err)
	}
}

func TestGuidedResultSerialization(t *testing.T) {
	guided := Guide([]searchResult{{Name: "Thai Basil"}}, "Pick one.")
	content := SerializeToolResult(guided)

	// Should contain both the data and the guidance
	if content == "" {
		t.Error("expected non-empty content")
	}

	// The content should contain "Pick one."
	if !contains(content, "Pick one.") {
		t.Errorf("expected guidance in content, got %q", content)
	}
	// The content should contain "Thai Basil"
	if !contains(content, "Thai Basil") {
		t.Errorf("expected data in content, got %q", content)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestNewTool`
Expected: FAIL (NewTool not defined)

- [ ] **Step 3: Write the implementation**

```go
// kernel/tool.go
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool represents an action that an LLM can invoke.
type Tool interface {
	Name() string
	Description() string
	Schema() Schema
	Execute(ctx context.Context, params json.RawMessage) (any, error)
}

// Guided wraps a tool result with guidance text for the LLM.
type Guided[T any] struct {
	Data     T
	Guidance string
}

// Guide creates a Guided result with formatted guidance.
func Guide[T any](data T, format string, args ...any) Guided[T] {
	return Guided[T]{
		Data:     data,
		Guidance: fmt.Sprintf(format, args...),
	}
}

// NewTool creates a Tool with typed parameters and return value.
// Parameters are auto-deserialized from JSON. Schema is auto-generated from P's struct tags.
func NewTool[P any, R any](
	name string,
	description string,
	fn func(ctx context.Context, params P) (R, error),
) Tool {
	return &typedTool[P, R]{
		name:        name,
		description: description,
		schema:      SchemaFrom[P](),
		fn:          fn,
	}
}

type typedTool[P any, R any] struct {
	name        string
	description string
	schema      Schema
	fn          func(ctx context.Context, params P) (R, error)
}

func (t *typedTool[P, R]) Name() string        { return t.name }
func (t *typedTool[P, R]) Description() string { return t.description }
func (t *typedTool[P, R]) Schema() Schema      { return t.schema }

func (t *typedTool[P, R]) Execute(ctx context.Context, params json.RawMessage) (any, error) {
	var p P
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid tool parameters: %w", err)
	}
	return t.fn(ctx, p)
}

// SerializeToolResult converts a tool result to a string for the LLM.
// If the result is Guided[T], it combines the data JSON and guidance text.
func SerializeToolResult(result any) string {
	// Check if it's a guided result by attempting to extract guidance
	type guidanceProvider interface {
		getGuidance() string
		getData() any
	}

	if gp, ok := result.(guidanceProvider); ok {
		data, _ := json.Marshal(gp.getData())
		return fmt.Sprintf("%s\n\n%s", string(data), gp.getGuidance())
	}

	data, _ := json.Marshal(result)
	return string(data)
}

// Implement guidanceProvider for Guided[T]
func (g Guided[T]) getGuidance() string { return g.Guidance }
func (g Guided[T]) getData() any        { return g.Data }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run "TestNewTool|TestTool|TestGuided"`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Commit**

```bash
git add kernel/tool.go kernel/tool_test.go
git commit -m "feat(kernel): add Tool interface, NewTool generics, and Guided output"
```

---

### Task 4: LLM interface and supporting types

**Files:**
- Create: `kernel/llm.go`

- [ ] **Step 1: Write the implementation**

This is an interface file with no logic — no tests needed yet. Tests come when we build the Agent that uses it.

```go
// kernel/llm.go
package kernel

import (
	"context"
	"encoding/json"
	"time"
)

// LLM is the interface that all LLM providers implement.
type LLM interface {
	Generate(ctx context.Context, params GenerateParams) (Response, error)
	GenerateStream(ctx context.Context, params GenerateParams) (Stream, error)
	Model() string
}

// GenerateParams holds everything needed for an LLM call.
type GenerateParams struct {
	Messages []Message
	Tools    []Tool
	Options  GenerateOptions
}

// GenerateOptions holds optional generation parameters.
type GenerateOptions struct {
	Temperature    *float32   `json:"temperature,omitempty"`
	MaxTokens      *int       `json:"max_tokens,omitempty"`
	StopSequences  []string   `json:"stop_sequences,omitempty"`
	ToolChoice     ToolChoice `json:"tool_choice,omitempty"`
	OutputSchema   *Schema    `json:"output_schema,omitempty"`
	ReasoningLevel *string    `json:"reasoning_level,omitempty"`
}

// ToolChoice controls how the LLM uses tools.
type ToolChoice struct {
	Type     string `json:"type"`               // "auto", "required", "none", "tool"
	ToolName string `json:"tool_name,omitempty"` // only when Type == "tool"
}

var (
	ToolChoiceAuto     = ToolChoice{Type: "auto"}
	ToolChoiceRequired = ToolChoice{Type: "required"}
	ToolChoiceNone     = ToolChoice{Type: "none"}
)

// ToolChoiceForce creates a ToolChoice that forces a specific tool.
func ToolChoiceForce(name string) ToolChoice {
	return ToolChoice{Type: "tool", ToolName: name}
}

// Response is the complete result of a non-streaming LLM call.
type Response struct {
	Text         string     `json:"text"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        Usage      `json:"usage"`
	FinishReason string     `json:"finish_reason"`
}

// Usage tracks token consumption and timing for an LLM call.
type Usage struct {
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	TotalTokens  int           `json:"total_tokens"`
	Latency      time.Duration `json:"latency"`
}

// Add adds another Usage to this one (for aggregation across rounds).
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		TotalTokens:  u.TotalTokens + other.TotalTokens,
		Latency:      u.Latency + other.Latency,
	}
}

// StreamEvent is a marker interface for events emitted during streaming.
type StreamEvent interface {
	streamEvent()
}

// TextDeltaEvent is emitted when the LLM produces text.
type TextDeltaEvent struct {
	Text string
}

func (TextDeltaEvent) streamEvent() {}

// ToolStartEvent is emitted when a tool begins execution.
type ToolStartEvent struct {
	ToolName string
	Params   json.RawMessage
}

func (ToolStartEvent) streamEvent() {}

// ToolEndEvent is emitted when a tool completes execution.
type ToolEndEvent struct {
	ToolName string
	Result   any
	Error    error
}

func (ToolEndEvent) streamEvent() {}

// Stream is the interface for consuming streaming LLM output.
type Stream interface {
	Events() <-chan StreamEvent
	Text() <-chan string
	Response() Response
	Err() error
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/derek/repo/ai-agent && go build ./kernel/`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add kernel/llm.go
git commit -m "feat(kernel): add LLM interface, GenerateParams, Response, Usage, Stream, and event types"
```

---

### Task 5: AgentContext

**Files:**
- Create: `kernel/context.go`
- Create: `kernel/context_test.go`

- [ ] **Step 1: Write the test file**

```go
// kernel/context_test.go
package kernel

import (
	"context"
	"encoding/json"
	"testing"
)

func makeDummyTool(name string) Tool {
	type empty struct{}
	return NewTool(name, "A "+name+" tool",
		func(ctx context.Context, p empty) (string, error) {
			return "ok", nil
		},
	)
}

func TestAgentContextAddMessages(t *testing.T) {
	ac := NewAgentContext(nil)
	ac.AddMessages(UserMsg("hello"), AssistantMsg("hi"))

	if len(ac.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ac.Messages))
	}
	if ac.Messages[0].Role != RoleUser {
		t.Errorf("expected first message role %q, got %q", RoleUser, ac.Messages[0].Role)
	}
}

func TestAgentContextSystemPrompt(t *testing.T) {
	ac := NewAgentContext(nil)

	// No system prompt initially
	if ac.SystemPrompt() != "" {
		t.Errorf("expected empty system prompt, got %q", ac.SystemPrompt())
	}

	// Set system prompt
	ac.SetSystemPrompt("You are helpful")
	if ac.SystemPrompt() != "You are helpful" {
		t.Errorf("expected %q, got %q", "You are helpful", ac.SystemPrompt())
	}

	// Overwrite system prompt
	ac.SetSystemPrompt("You are concise")
	if ac.SystemPrompt() != "You are concise" {
		t.Errorf("expected %q, got %q", "You are concise", ac.SystemPrompt())
	}

	// System prompt should be the first message
	if ac.Messages[0].Role != RoleSystem {
		t.Errorf("expected first message to be system, got %q", ac.Messages[0].Role)
	}

	// Should only have one system message
	systemCount := 0
	for _, m := range ac.Messages {
		if m.Role == RoleSystem {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Errorf("expected 1 system message, got %d", systemCount)
	}
}

func TestAgentContextLastUserMessage(t *testing.T) {
	ac := NewAgentContext(nil)
	ac.AddMessages(SystemMsg("system"), UserMsg("first"), AssistantMsg("response"), UserMsg("second"))

	last := ac.LastUserMessage()
	if last == nil {
		t.Fatal("expected non-nil last user message")
	}
	if last.TextContent() != "second" {
		t.Errorf("expected %q, got %q", "second", last.TextContent())
	}
}

func TestAgentContextLastUserMessageNone(t *testing.T) {
	ac := NewAgentContext(nil)
	ac.AddMessages(SystemMsg("system"))

	if ac.LastUserMessage() != nil {
		t.Error("expected nil when no user messages")
	}
}

func TestAgentContextToolManagement(t *testing.T) {
	tools := []Tool{
		makeDummyTool("search"),
		makeDummyTool("reserve"),
		makeDummyTool("music"),
	}
	ac := NewAgentContext(tools)

	// All tools active initially
	active := ac.ActiveTools()
	if len(active) != 3 {
		t.Fatalf("expected 3 active tools, got %d", len(active))
	}

	// Disable one
	ac.DisableTools("music")
	active = ac.ActiveTools()
	if len(active) != 2 {
		t.Fatalf("expected 2 active tools, got %d", len(active))
	}
	for _, tool := range active {
		if tool.Name() == "music" {
			t.Error("music should be disabled")
		}
	}

	// AllTools still returns all
	if len(ac.AllTools()) != 3 {
		t.Errorf("expected 3 total tools, got %d", len(ac.AllTools()))
	}
}

func TestAgentContextEnableTools(t *testing.T) {
	tools := []Tool{
		makeDummyTool("search"),
		makeDummyTool("reserve"),
		makeDummyTool("music"),
	}
	ac := NewAgentContext(tools)

	// Enable only search and reserve
	ac.EnableTools("search", "reserve")
	active := ac.ActiveTools()
	if len(active) != 2 {
		t.Fatalf("expected 2 active tools, got %d", len(active))
	}

	names := map[string]bool{}
	for _, tool := range active {
		names[tool.Name()] = true
	}
	if !names["search"] || !names["reserve"] {
		t.Errorf("expected search and reserve, got %v", names)
	}
}

func TestAgentContextAddTools(t *testing.T) {
	ac := NewAgentContext([]Tool{makeDummyTool("search")})
	ac.AddTools(makeDummyTool("weather"))

	if len(ac.AllTools()) != 2 {
		t.Errorf("expected 2 tools, got %d", len(ac.AllTools()))
	}
	if len(ac.ActiveTools()) != 2 {
		t.Errorf("expected 2 active tools, got %d", len(ac.ActiveTools()))
	}
}

func TestAgentContextDisableAllTools(t *testing.T) {
	tools := []Tool{makeDummyTool("a"), makeDummyTool("b")}
	ac := NewAgentContext(tools)
	ac.DisableTools("a", "b")

	if len(ac.ActiveTools()) != 0 {
		t.Errorf("expected 0 active tools, got %d", len(ac.ActiveTools()))
	}
}

func TestAgentContextGetTool(t *testing.T) {
	tools := []Tool{makeDummyTool("search"), makeDummyTool("reserve")}
	ac := NewAgentContext(tools)

	tool, ok := ac.GetTool("search")
	if !ok {
		t.Fatal("expected to find tool 'search'")
	}
	if tool.Name() != "search" {
		t.Errorf("expected tool name %q, got %q", "search", tool.Name())
	}

	_, ok = ac.GetTool("nonexistent")
	if ok {
		t.Error("expected not to find tool 'nonexistent'")
	}
}

// Ensure unused import doesn't break
var _ = json.Marshal
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestAgentContext`
Expected: FAIL (AgentContext not defined)

- [ ] **Step 3: Write the implementation**

```go
// kernel/context.go
package kernel

// AgentContext holds the state visible to the agent during a turn:
// conversation messages and available tools.
type AgentContext struct {
	Messages []Message
	tools    []Tool
	disabled map[string]bool
}

// NewAgentContext creates a new AgentContext with the given tools.
func NewAgentContext(tools []Tool) *AgentContext {
	if tools == nil {
		tools = []Tool{}
	}
	return &AgentContext{
		tools:    tools,
		disabled: make(map[string]bool),
	}
}

// AddMessages appends messages to the conversation.
func (c *AgentContext) AddMessages(msgs ...Message) {
	c.Messages = append(c.Messages, msgs...)
}

// SystemPrompt returns the text of the first system message, or empty string.
func (c *AgentContext) SystemPrompt() string {
	for _, m := range c.Messages {
		if m.Role == RoleSystem {
			return m.TextContent()
		}
	}
	return ""
}

// SetSystemPrompt replaces the first system message or inserts one at the beginning.
func (c *AgentContext) SetSystemPrompt(prompt string) {
	for i, m := range c.Messages {
		if m.Role == RoleSystem {
			c.Messages[i] = SystemMsg(prompt)
			return
		}
	}
	// No existing system message — prepend
	c.Messages = append([]Message{SystemMsg(prompt)}, c.Messages...)
}

// LastUserMessage returns the last message with RoleUser, or nil.
func (c *AgentContext) LastUserMessage() *Message {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == RoleUser {
			return &c.Messages[i]
		}
	}
	return nil
}

// EnableTools sets only the named tools as active. All others are disabled.
func (c *AgentContext) EnableTools(names ...string) {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	c.disabled = make(map[string]bool)
	for _, t := range c.tools {
		if !nameSet[t.Name()] {
			c.disabled[t.Name()] = true
		}
	}
}

// DisableTools marks the named tools as inactive.
func (c *AgentContext) DisableTools(names ...string) {
	for _, n := range names {
		c.disabled[n] = true
	}
}

// AddTools registers new tools (active by default).
func (c *AgentContext) AddTools(tools ...Tool) {
	c.tools = append(c.tools, tools...)
}

// ActiveTools returns only the currently enabled tools.
func (c *AgentContext) ActiveTools() []Tool {
	var active []Tool
	for _, t := range c.tools {
		if !c.disabled[t.Name()] {
			active = append(active, t)
		}
	}
	return active
}

// AllTools returns all registered tools regardless of enabled state.
func (c *AgentContext) AllTools() []Tool {
	return c.tools
}

// GetTool returns a tool by name from all registered tools.
func (c *AgentContext) GetTool(name string) (Tool, bool) {
	for _, t := range c.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestAgentContext`
Expected: PASS (all 9 tests)

- [ ] **Step 5: Commit**

```bash
git add kernel/context.go kernel/context_test.go
git commit -m "feat(kernel): add AgentContext with tool enable/disable and message management"
```

---

### Task 6: Agent with hooks and Run()

**Files:**
- Create: `kernel/agent.go`
- Create: `kernel/agent_test.go`

- [ ] **Step 1: Write the test file**

```go
// kernel/agent_test.go
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// fakeLLM implements LLM for testing. Returns scripted responses per round.
type fakeLLM struct {
	responses []Response
	callCount int
	mu        sync.Mutex
}

func newFakeLLM(responses ...Response) *fakeLLM {
	return &fakeLLM{responses: responses}
}

func (f *fakeLLM) Generate(ctx context.Context, params GenerateParams) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.callCount >= len(f.responses) {
		return Response{Text: "no more responses", FinishReason: "stop"}, nil
	}
	resp := f.responses[f.callCount]
	f.callCount++
	return resp, nil
}

func (f *fakeLLM) GenerateStream(ctx context.Context, params GenerateParams) (Stream, error) {
	return nil, fmt.Errorf("streaming not implemented in fakeLLM")
}

func (f *fakeLLM) Model() string { return "fake" }

func TestAgentRunTextOnly(t *testing.T) {
	llm := newFakeLLM(Response{
		Text:         "Hello, how can I help?",
		FinishReason: "stop",
		Usage:        Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})

	agent := NewAgent(
		WithModel(llm),
		WithSystemPrompt("You are helpful"),
	)

	result, err := agent.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello, how can I help?" {
		t.Errorf("expected text %q, got %q", "Hello, how can I help?", result.Text)
	}
	if len(result.Rounds) != 1 {
		t.Errorf("expected 1 round, got %d", len(result.Rounds))
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestAgentRunWithToolCall(t *testing.T) {
	searchTool := NewTool("search", "Search things",
		func(ctx context.Context, p searchParams) ([]searchResult, error) {
			return []searchResult{{Name: "Thai Basil", ID: "r1"}}, nil
		},
	)

	llm := newFakeLLM(
		// Round 0: tool call
		Response{
			ToolCalls: []ToolCall{{
				ID:     "call-1",
				Name:   "search",
				Params: json.RawMessage(`{"query":"thai","location":"SF"}`),
			}},
			FinishReason: "tool_calls",
			Usage:        Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
		},
		// Round 1: text response
		Response{
			Text:         "I found Thai Basil for you!",
			FinishReason: "stop",
			Usage:        Usage{InputTokens: 30, OutputTokens: 8, TotalTokens: 38},
		},
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		WithSystemPrompt("You are a restaurant assistant"),
	)

	result, err := agent.Run(context.Background(), "Find thai food")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "I found Thai Basil for you!" {
		t.Errorf("expected final text, got %q", result.Text)
	}
	if len(result.Rounds) != 2 {
		t.Errorf("expected 2 rounds, got %d", len(result.Rounds))
	}
	if result.Usage.TotalTokens != 68 {
		t.Errorf("expected 68 total tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestAgentRunMaxRounds(t *testing.T) {
	// LLM always returns tool calls — agent should stop at max rounds
	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
		Response{ToolCalls: []ToolCall{{ID: "c2", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
		Response{ToolCalls: []ToolCall{{ID: "c3", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
	)

	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) {
			return "result", nil
		},
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		WithMaxRounds(3),
	)

	result, err := agent.Run(context.Background(), "search forever")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rounds) != 3 {
		t.Errorf("expected 3 rounds (max), got %d", len(result.Rounds))
	}
}

func TestAgentHooksOnStartOnFinish(t *testing.T) {
	var startCalled, finishCalled bool

	llm := newFakeLLM(Response{Text: "hi", FinishReason: "stop"})

	agent := NewAgent(
		WithModel(llm),
		OnStart(func(ctx *TurnContext) {
			startCalled = true
			if ctx.Input != "hello" {
				t.Errorf("OnStart: expected input %q, got %q", "hello", ctx.Input)
			}
		}),
		OnFinish(func(ctx *TurnContext) {
			finishCalled = true
			if ctx.Result == nil {
				t.Error("OnFinish: expected non-nil result")
			}
		}),
	)

	agent.Run(context.Background(), "hello")

	if !startCalled {
		t.Error("OnStart was not called")
	}
	if !finishCalled {
		t.Error("OnFinish was not called")
	}
}

func TestAgentHookPrepareRound(t *testing.T) {
	var roundNumbers []int

	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"a","location":"b"}`)}}, FinishReason: "tool_calls"},
		Response{Text: "done", FinishReason: "stop"},
	)

	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) { return "ok", nil },
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		PrepareRound(func(ctx *RoundContext) {
			roundNumbers = append(roundNumbers, ctx.RoundNumber)
		}),
	)

	agent.Run(context.Background(), "test")

	if len(roundNumbers) != 2 {
		t.Fatalf("expected PrepareRound called 2 times, got %d", len(roundNumbers))
	}
	if roundNumbers[0] != 0 || roundNumbers[1] != 1 {
		t.Errorf("expected round numbers [0, 1], got %v", roundNumbers)
	}
}

func TestAgentHookToolStartEnd(t *testing.T) {
	var toolStartName, toolEndName string
	var toolEndResult any

	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"test","location":"here"}`)}}, FinishReason: "tool_calls"},
		Response{Text: "done", FinishReason: "stop"},
	)

	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) { return "found it", nil },
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool),
		OnToolStart(func(ctx *ToolContext) {
			toolStartName = ctx.ToolName
		}),
		OnToolEnd(func(ctx *ToolContext) {
			toolEndName = ctx.ToolName
			toolEndResult = ctx.Result
		}),
	)

	agent.Run(context.Background(), "test")

	if toolStartName != "search" {
		t.Errorf("OnToolStart: expected tool %q, got %q", "search", toolStartName)
	}
	if toolEndName != "search" {
		t.Errorf("OnToolEnd: expected tool %q, got %q", "search", toolEndName)
	}
	if toolEndResult != "found it" {
		t.Errorf("OnToolEnd: expected result %q, got %v", "found it", toolEndResult)
	}
}

func TestAgentStopWhen(t *testing.T) {
	llm := newFakeLLM(
		Response{Text: "round 0", FinishReason: "stop", Usage: Usage{TotalTokens: 5000}},
		Response{Text: "round 1", FinishReason: "stop", Usage: Usage{TotalTokens: 6000}},
	)

	agent := NewAgent(
		WithModel(llm),
		StopWhen(func(ctx *RoundContext) bool {
			// Stop after first round regardless
			return ctx.RoundNumber > 0
		}),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should only have 1 round because StopWhen fires before round 1
	if len(result.Rounds) != 1 {
		t.Errorf("expected 1 round, got %d", len(result.Rounds))
	}
}

func TestAgentToolNotFound(t *testing.T) {
	llm := newFakeLLM(
		Response{ToolCalls: []ToolCall{{ID: "c1", Name: "nonexistent", Params: json.RawMessage(`{}`)}}, FinishReason: "tool_calls"},
	)

	agent := NewAgent(WithModel(llm))

	_, err := agent.Run(context.Background(), "test")
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestAgentPrepareRoundModifiesTools(t *testing.T) {
	llm := newFakeLLM(Response{Text: "done", FinishReason: "stop"})

	searchTool := makeDummyTool("search")
	musicTool := makeDummyTool("music")

	var activeToolCount int

	agent := NewAgent(
		WithModel(llm),
		WithTools(searchTool, musicTool),
		PrepareRound(func(ctx *RoundContext) {
			ctx.AgentCtx.EnableTools("search")
			activeToolCount = len(ctx.AgentCtx.ActiveTools())
		}),
	)

	agent.Run(context.Background(), "test")

	if activeToolCount != 1 {
		t.Errorf("expected 1 active tool after PrepareRound, got %d", activeToolCount)
	}
}

func TestAgentMultipleHooksSameType(t *testing.T) {
	var order []string

	llm := newFakeLLM(Response{Text: "ok", FinishReason: "stop"})

	agent := NewAgent(
		WithModel(llm),
		OnStart(func(ctx *TurnContext) { order = append(order, "first") }),
		OnStart(func(ctx *TurnContext) { order = append(order, "second") }),
	)

	agent.Run(context.Background(), "test")

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("expected hooks in declaration order, got %v", order)
	}
}

func TestAgentParallelToolExecution(t *testing.T) {
	var mu sync.Mutex
	var executionOrder []string

	tool1 := NewTool("tool_a", "Tool A",
		func(ctx context.Context, p struct{}) (string, error) {
			mu.Lock()
			executionOrder = append(executionOrder, "a")
			mu.Unlock()
			return "result_a", nil
		},
	)
	tool2 := NewTool("tool_b", "Tool B",
		func(ctx context.Context, p struct{}) (string, error) {
			mu.Lock()
			executionOrder = append(executionOrder, "b")
			mu.Unlock()
			return "result_b", nil
		},
	)

	llm := newFakeLLM(
		Response{
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "tool_a", Params: json.RawMessage(`{}`)},
				{ID: "c2", Name: "tool_b", Params: json.RawMessage(`{}`)},
			},
			FinishReason: "tool_calls",
		},
		Response{Text: "both done", FinishReason: "stop"},
	)

	agent := NewAgent(
		WithModel(llm),
		WithTools(tool1, tool2),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(executionOrder) != 2 {
		t.Errorf("expected both tools executed, got %v", executionOrder)
	}
	if result.Text != "both done" {
		t.Errorf("expected %q, got %q", "both done", result.Text)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestAgent`
Expected: FAIL (Agent not defined)

- [ ] **Step 3: Write the implementation**

```go
// kernel/agent.go
package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

const defaultMaxRounds = 20

// Agent is the core agentic loop: generates LLM responses, executes tool calls,
// and repeats until a text response or stop condition.
type Agent struct {
	model        LLM
	tools        []Tool
	systemPrompt string
	hooks        agentHooks
	stopConds    []StopCondition
	maxRounds    int
}

// StopCondition is evaluated before each round. If any returns true, the loop stops.
type StopCondition func(ctx *RoundContext) bool

// TurnContext is available to OnStart and OnFinish hooks.
type TurnContext struct {
	AgentCtx *AgentContext
	Input    string
	Result   *Result
}

// RoundContext is available to PrepareRound, OnRoundFinish, and StopWhen.
type RoundContext struct {
	AgentCtx     *AgentContext
	RoundNumber  int
	LastResponse *Response
}

// ToolContext is available to OnToolStart and OnToolEnd hooks.
type ToolContext struct {
	ToolName string
	Params   json.RawMessage
	Result   any
	Error    error
}

// Result is the output of an Agent.Run() call.
type Result struct {
	Text   string
	Rounds []RoundResult
	Usage  Usage
}

// RoundResult captures what happened in a single round.
type RoundResult struct {
	Response  Response
	ToolCalls []ToolCallResult
}

// ToolCallResult captures a single tool execution.
type ToolCallResult struct {
	Name   string
	Params json.RawMessage
	Result any
	Error  error
}

// agentHooks holds all registered hook functions.
type agentHooks struct {
	onStart      []func(*TurnContext)
	onFinish     []func(*TurnContext)
	prepareRound []func(*RoundContext)
	onRoundFinish []func(*RoundContext)
	onToolStart  []func(*ToolContext)
	onToolEnd    []func(*ToolContext)
}

// AgentOption configures an Agent.
type AgentOption func(*Agent)

func WithModel(llm LLM) AgentOption           { return func(a *Agent) { a.model = llm } }
func WithTools(tools ...Tool) AgentOption      { return func(a *Agent) { a.tools = tools } }
func WithSystemPrompt(prompt string) AgentOption { return func(a *Agent) { a.systemPrompt = prompt } }
func WithMaxRounds(n int) AgentOption          { return func(a *Agent) { a.maxRounds = n } }

func OnStart(fn func(*TurnContext)) AgentOption {
	return func(a *Agent) { a.hooks.onStart = append(a.hooks.onStart, fn) }
}

func OnFinish(fn func(*TurnContext)) AgentOption {
	return func(a *Agent) { a.hooks.onFinish = append(a.hooks.onFinish, fn) }
}

func PrepareRound(fn func(*RoundContext)) AgentOption {
	return func(a *Agent) { a.hooks.prepareRound = append(a.hooks.prepareRound, fn) }
}

func OnRoundFinish(fn func(*RoundContext)) AgentOption {
	return func(a *Agent) { a.hooks.onRoundFinish = append(a.hooks.onRoundFinish, fn) }
}

func OnToolStart(fn func(*ToolContext)) AgentOption {
	return func(a *Agent) { a.hooks.onToolStart = append(a.hooks.onToolStart, fn) }
}

func OnToolEnd(fn func(*ToolContext)) AgentOption {
	return func(a *Agent) { a.hooks.onToolEnd = append(a.hooks.onToolEnd, fn) }
}

func StopWhen(fn StopCondition) AgentOption {
	return func(a *Agent) { a.stopConds = append(a.stopConds, fn) }
}

// NewAgent creates a new Agent with the given options.
func NewAgent(opts ...AgentOption) *Agent {
	a := &Agent{maxRounds: defaultMaxRounds}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Run executes the agent loop synchronously and returns the result.
func (a *Agent) Run(ctx context.Context, input string) (*Result, error) {
	// Build AgentContext
	agentCtx := NewAgentContext(a.tools)
	if a.systemPrompt != "" {
		agentCtx.SetSystemPrompt(a.systemPrompt)
	}
	agentCtx.AddMessages(UserMsg(input))

	turnCtx := &TurnContext{AgentCtx: agentCtx, Input: input}

	// Fire OnStart hooks
	for _, fn := range a.hooks.onStart {
		fn(turnCtx)
	}

	var rounds []RoundResult
	var totalUsage Usage
	var lastResponse *Response
	var finalText string

	// Agent loop
	for round := 0; round < a.maxRounds; round++ {
		roundCtx := &RoundContext{
			AgentCtx:     agentCtx,
			RoundNumber:  round,
			LastResponse: lastResponse,
		}

		// Fire PrepareRound hooks
		for _, fn := range a.hooks.prepareRound {
			fn(roundCtx)
		}

		// Check stop conditions
		shouldStop := false
		for _, cond := range a.stopConds {
			if cond(roundCtx) {
				shouldStop = true
				break
			}
		}
		if shouldStop {
			break
		}

		// Build generate params
		params := GenerateParams{
			Messages: agentCtx.Messages,
			Tools:    agentCtx.ActiveTools(),
		}

		// Generate
		resp, err := a.model.Generate(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("llm generate round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)
		lastResponse = &resp

		roundResult := RoundResult{Response: resp}

		if len(resp.ToolCalls) > 0 {
			// Execute tool calls
			toolResults, err := a.executeToolCalls(ctx, agentCtx, resp.ToolCalls)
			if err != nil {
				return nil, err
			}
			roundResult.ToolCalls = toolResults

			// Add tool call and result messages to context
			for i, tc := range resp.ToolCalls {
				// Add the assistant's tool call message
				agentCtx.AddMessages(Message{
					Role:    RoleAssistant,
					Content: []ContentPart{{ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Params: tc.Params}}},
				})

				// Add the tool result message
				content := SerializeToolResult(toolResults[i].Result)
				isError := toolResults[i].Error != nil
				if isError {
					content = toolResults[i].Error.Error()
				}
				agentCtx.AddMessages(Message{
					Role: RoleTool,
					Content: []ContentPart{{ToolResult: &ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    content,
						IsError:    isError,
					}}},
				})
			}
		} else {
			finalText = resp.Text
		}

		rounds = append(rounds, roundResult)

		// Fire OnRoundFinish hooks
		roundCtx.LastResponse = &resp
		for _, fn := range a.hooks.onRoundFinish {
			fn(roundCtx)
		}

		// If no tool calls, we have the final text response — exit loop
		if len(resp.ToolCalls) == 0 {
			break
		}
	}

	result := &Result{
		Text:   finalText,
		Rounds: rounds,
		Usage:  totalUsage,
	}

	// Fire OnFinish hooks
	turnCtx.Result = result
	for _, fn := range a.hooks.onFinish {
		fn(turnCtx)
	}

	return result, nil
}

// executeToolCalls runs tool calls in parallel and returns results.
func (a *Agent) executeToolCalls(ctx context.Context, agentCtx *AgentContext, calls []ToolCall) ([]ToolCallResult, error) {
	results := make([]ToolCallResult, len(calls))

	if len(calls) == 1 {
		// Single tool call — no goroutine overhead
		result, err := a.executeSingleTool(ctx, agentCtx, calls[0])
		if err != nil {
			return nil, err
		}
		results[0] = result
		return results, nil
	}

	// Multiple tool calls — execute in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, tc ToolCall) {
			defer wg.Done()
			result, err := a.executeSingleTool(ctx, agentCtx, tc)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			results[idx] = result
		}(i, call)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// executeSingleTool finds and executes a single tool, firing hooks.
func (a *Agent) executeSingleTool(ctx context.Context, agentCtx *AgentContext, call ToolCall) (ToolCallResult, error) {
	tool, ok := agentCtx.GetTool(call.Name)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("tool %q not found", call.Name)
	}

	toolCtx := &ToolContext{
		ToolName: call.Name,
		Params:   call.Params,
	}

	// Fire OnToolStart hooks
	for _, fn := range a.hooks.onToolStart {
		fn(toolCtx)
	}

	// Execute
	result, err := tool.Execute(ctx, call.Params)

	toolCtx.Result = result
	toolCtx.Error = err

	// Fire OnToolEnd hooks
	for _, fn := range a.hooks.onToolEnd {
		fn(toolCtx)
	}

	return ToolCallResult{
		Name:   call.Name,
		Params: call.Params,
		Result: result,
		Error:  err,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestAgent`
Expected: PASS (all 11 tests)

- [ ] **Step 5: Run all kernel tests**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v`
Expected: PASS (all tests across all files)

- [ ] **Step 6: Commit**

```bash
git add kernel/agent.go kernel/agent_test.go
git commit -m "feat(kernel): add Agent with ReAct loop, hooks, parallel tool execution"
```

---

### Task 7: Agent.Stream() and StreamResult

**Files:**
- Create: `kernel/agent_stream.go`
- Create: `kernel/agent_stream_test.go`

- [ ] **Step 1: Write the test file**

```go
// kernel/agent_stream_test.go
package kernel

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAgentStreamTextOnly(t *testing.T) {
	llm := newFakeLLM(Response{
		Text:         "Hello there!",
		FinishReason: "stop",
		Usage:        Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})

	agent := NewAgent(WithModel(llm), WithSystemPrompt("Be helpful"))

	sr, err := agent.Stream(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect all text
	var text string
	for chunk := range sr.Text() {
		text += chunk
	}

	if text != "Hello there!" {
		t.Errorf("expected %q, got %q", "Hello there!", text)
	}

	result := sr.Result()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 tokens, got %d", result.Usage.TotalTokens)
	}
}

func TestAgentStreamWithToolCalls(t *testing.T) {
	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) {
			return "found it", nil
		},
	)

	llm := newFakeLLM(
		Response{
			ToolCalls:    []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"test","location":"here"}`)}},
			FinishReason: "tool_calls",
		},
		Response{
			Text:         "Here are your results",
			FinishReason: "stop",
		},
	)

	agent := NewAgent(WithModel(llm), WithTools(searchTool))

	sr, err := agent.Stream(context.Background(), "search for something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect text (should only be final response)
	var text string
	for chunk := range sr.Text() {
		text += chunk
	}

	if text != "Here are your results" {
		t.Errorf("expected %q, got %q", "Here are your results", text)
	}
}

func TestAgentStreamEvents(t *testing.T) {
	searchTool := NewTool("search", "Search",
		func(ctx context.Context, p searchParams) (string, error) {
			return "found", nil
		},
	)

	llm := newFakeLLM(
		Response{
			ToolCalls:    []ToolCall{{ID: "c1", Name: "search", Params: json.RawMessage(`{"query":"test","location":"here"}`)}},
			FinishReason: "tool_calls",
		},
		Response{
			Text:         "Done!",
			FinishReason: "stop",
		},
	)

	agent := NewAgent(WithModel(llm), WithTools(searchTool))

	sr, err := agent.Stream(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []StreamEvent
	for event := range sr.Events() {
		events = append(events, event)
	}

	// Should have: ToolStartEvent, ToolEndEvent, TextDeltaEvent
	hasToolStart := false
	hasToolEnd := false
	hasTextDelta := false
	for _, e := range events {
		switch e.(type) {
		case ToolStartEvent:
			hasToolStart = true
		case ToolEndEvent:
			hasToolEnd = true
		case TextDeltaEvent:
			hasTextDelta = true
		}
	}

	if !hasToolStart {
		t.Error("expected ToolStartEvent")
	}
	if !hasToolEnd {
		t.Error("expected ToolEndEvent")
	}
	if !hasTextDelta {
		t.Error("expected TextDeltaEvent")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestAgentStream`
Expected: FAIL (Stream method not defined)

- [ ] **Step 3: Write the implementation**

```go
// kernel/agent_stream.go
package kernel

import (
	"context"
	"fmt"
	"sync"
)

// StreamResult provides both streaming access and final result.
type StreamResult struct {
	textCh   chan string
	eventCh  chan StreamEvent
	result   *Result
	err      error
	done     chan struct{}
	mu       sync.Mutex
}

// Text returns a channel of text deltas from the final response only.
func (sr *StreamResult) Text() <-chan string {
	return sr.textCh
}

// Events returns a channel of all events (tool starts, tool ends, text deltas).
func (sr *StreamResult) Events() <-chan StreamEvent {
	return sr.eventCh
}

// Result returns the final result after the stream completes. Blocks until done.
func (sr *StreamResult) Result() *Result {
	<-sr.done
	return sr.result
}

// Err returns any error that occurred during streaming. Blocks until done.
func (sr *StreamResult) Err() error {
	<-sr.done
	return sr.err
}

// Stream executes the agent loop in a goroutine, emitting events and text as they happen.
func (a *Agent) Stream(ctx context.Context, input string) (*StreamResult, error) {
	sr := &StreamResult{
		textCh:  make(chan string, 64),
		eventCh: make(chan StreamEvent, 64),
		done:    make(chan struct{}),
	}

	go func() {
		defer close(sr.textCh)
		defer close(sr.eventCh)
		defer close(sr.done)

		result, err := a.runWithEvents(ctx, input, sr)
		sr.mu.Lock()
		sr.result = result
		sr.err = err
		sr.mu.Unlock()
	}()

	return sr, nil
}

// runWithEvents is the same as Run but emits events to a StreamResult.
func (a *Agent) runWithEvents(ctx context.Context, input string, sr *StreamResult) (*Result, error) {
	agentCtx := NewAgentContext(a.tools)
	if a.systemPrompt != "" {
		agentCtx.SetSystemPrompt(a.systemPrompt)
	}
	agentCtx.AddMessages(UserMsg(input))

	turnCtx := &TurnContext{AgentCtx: agentCtx, Input: input}

	for _, fn := range a.hooks.onStart {
		fn(turnCtx)
	}

	var rounds []RoundResult
	var totalUsage Usage
	var lastResponse *Response
	var finalText string

	for round := 0; round < a.maxRounds; round++ {
		roundCtx := &RoundContext{
			AgentCtx:     agentCtx,
			RoundNumber:  round,
			LastResponse: lastResponse,
		}

		for _, fn := range a.hooks.prepareRound {
			fn(roundCtx)
		}

		shouldStop := false
		for _, cond := range a.stopConds {
			if cond(roundCtx) {
				shouldStop = true
				break
			}
		}
		if shouldStop {
			break
		}

		params := GenerateParams{
			Messages: agentCtx.Messages,
			Tools:    agentCtx.ActiveTools(),
		}

		resp, err := a.model.Generate(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("llm generate round %d: %w", round, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)
		lastResponse = &resp

		roundResult := RoundResult{Response: resp}

		if len(resp.ToolCalls) > 0 {
			toolResults, err := a.executeToolCallsWithEvents(ctx, agentCtx, resp.ToolCalls, sr)
			if err != nil {
				return nil, err
			}
			roundResult.ToolCalls = toolResults

			for i, tc := range resp.ToolCalls {
				agentCtx.AddMessages(Message{
					Role:    RoleAssistant,
					Content: []ContentPart{{ToolCall: &ToolCall{ID: tc.ID, Name: tc.Name, Params: tc.Params}}},
				})

				content := SerializeToolResult(toolResults[i].Result)
				isError := toolResults[i].Error != nil
				if isError {
					content = toolResults[i].Error.Error()
				}
				agentCtx.AddMessages(Message{
					Role: RoleTool,
					Content: []ContentPart{{ToolResult: &ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    content,
						IsError:    isError,
					}}},
				})
			}
		} else {
			finalText = resp.Text
			// Emit text as a single delta (since we're using Generate, not GenerateStream)
			sr.textCh <- resp.Text
			sr.eventCh <- TextDeltaEvent{Text: resp.Text}
		}

		rounds = append(rounds, roundResult)

		roundCtx.LastResponse = &resp
		for _, fn := range a.hooks.onRoundFinish {
			fn(roundCtx)
		}

		if len(resp.ToolCalls) == 0 {
			break
		}
	}

	result := &Result{
		Text:   finalText,
		Rounds: rounds,
		Usage:  totalUsage,
	}

	turnCtx.Result = result
	for _, fn := range a.hooks.onFinish {
		fn(turnCtx)
	}

	return result, nil
}

// executeToolCallsWithEvents runs tools and emits events to the stream.
func (a *Agent) executeToolCallsWithEvents(ctx context.Context, agentCtx *AgentContext, calls []ToolCall, sr *StreamResult) ([]ToolCallResult, error) {
	results := make([]ToolCallResult, len(calls))

	if len(calls) == 1 {
		result, err := a.executeSingleToolWithEvents(ctx, agentCtx, calls[0], sr)
		if err != nil {
			return nil, err
		}
		results[0] = result
		return results, nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, call := range calls {
		wg.Add(1)
		go func(idx int, tc ToolCall) {
			defer wg.Done()
			result, err := a.executeSingleToolWithEvents(ctx, agentCtx, tc, sr)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			results[idx] = result
		}(i, call)
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func (a *Agent) executeSingleToolWithEvents(ctx context.Context, agentCtx *AgentContext, call ToolCall, sr *StreamResult) (ToolCallResult, error) {
	tool, ok := agentCtx.GetTool(call.Name)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("tool %q not found", call.Name)
	}

	toolCtx := &ToolContext{ToolName: call.Name, Params: call.Params}

	// Emit event + fire hooks
	sr.eventCh <- ToolStartEvent{ToolName: call.Name, Params: call.Params}
	for _, fn := range a.hooks.onToolStart {
		fn(toolCtx)
	}

	result, err := tool.Execute(ctx, call.Params)
	toolCtx.Result = result
	toolCtx.Error = err

	// Emit event + fire hooks
	sr.eventCh <- ToolEndEvent{ToolName: call.Name, Result: result, Error: err}
	for _, fn := range a.hooks.onToolEnd {
		fn(toolCtx)
	}

	return ToolCallResult{
		Name:   call.Name,
		Params: call.Params,
		Result: result,
		Error:  err,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -run TestAgentStream`
Expected: PASS (all 3 streaming tests)

- [ ] **Step 5: Run all kernel tests**

Run: `cd /Users/derek/repo/ai-agent && go test ./kernel/ -v -count=1`
Expected: PASS (all tests across all files — message, schema, tool, context, agent, agent_stream)

- [ ] **Step 6: Commit**

```bash
git add kernel/agent_stream.go kernel/agent_stream_test.go
git commit -m "feat(kernel): add Agent.Stream() with event emission and StreamResult"
```

---

## Self-Review

**Spec coverage:**
- 3.1 LLM Interface → Task 4 (llm.go)
- 3.2 Stream → Task 4 (llm.go event types) + Task 7 (agent_stream.go StreamResult)
- 3.3 Message → Task 1 (message.go)
- 3.4 Tool → Task 3 (tool.go) with NewTool generics, Guided, SerializeToolResult
- 3.5 AgentContext → Task 5 (context.go)
- 3.6 Agent → Task 6 (agent.go) + Task 7 (agent_stream.go)
- Schema generation → Task 2 (schema.go)
- All 7 hooks → Task 6 (OnStart, OnFinish, PrepareRound, OnRoundFinish, OnToolStart, OnToolEnd, StopWhen)
- Parallel tool execution → Task 6 (executeToolCalls with WaitGroup)
- Streaming with events → Task 7

**Placeholder scan:** No TBDs, TODOs, or "implement later" found. All code is complete.

**Type consistency:** Verified across all tasks — `Response`, `ToolCall`, `ToolContext`, `RoundContext`, `TurnContext`, `AgentContext`, `Result`, `RoundResult`, `ToolCallResult`, `StreamResult` are consistent throughout.
