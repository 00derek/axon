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
