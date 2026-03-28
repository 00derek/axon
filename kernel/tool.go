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
