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
