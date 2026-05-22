package api

import "encoding/json"

// ToolDefinition is the JSON Schema description sent to the LLM.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}
