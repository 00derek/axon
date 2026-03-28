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
