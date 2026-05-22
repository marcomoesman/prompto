// Package tool implements the concrete tools the agent exposes to the model.
// The Tool interface itself lives in internal/agent next to its consumer;
// this file re-exports it as a type alias so existing imports continue to
// resolve by package name.
package tool

import (
	"encoding/json"
	"fmt"

	"github.com/marcomoesman/prompto/internal/agent"
)

// Tool is an alias for agent.Tool. The interface lives in the agent package;
// concrete implementations live here.
type Tool = agent.Tool

// unmarshalInput decodes a tool-call input blob into T, wrapping the JSON
// error in a uniform "invalid input" message so every tool surfaces
// malformed-args failures the same way. Used by every tool's Execute /
// PermissionKey / FormatForDisplay implementation.
func unmarshalInput[T any](input []byte) (T, error) {
	var p T
	if err := json.Unmarshal(input, &p); err != nil {
		return p, fmt.Errorf("invalid input: %w", err)
	}
	return p, nil
}
