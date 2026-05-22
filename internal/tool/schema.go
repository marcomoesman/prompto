package tool

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// GenerateSchema produces a JSON Schema (as json.RawMessage) from a Go struct.
// The struct should use jsonschema struct tags for descriptions and requirements.
// Called once per tool at construction time.
func GenerateSchema(v any) json.RawMessage {
	r := &jsonschema.Reflector{
		DoNotReference: true, // inline all definitions, no $ref
	}
	schema := r.Reflect(v)
	schema.Version = ""
	schema.ID = ""
	data, err := json.Marshal(schema)
	if err != nil {
		panic("tool.GenerateSchema: " + err.Error())
	}
	return data
}
