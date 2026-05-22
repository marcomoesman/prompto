package tool

import (
	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// Registry holds all registered tools and provides lookup by name.
type Registry struct {
	tools map[string]agent.Tool
	order []string // preserves registration order for deterministic schema output
}

// NewRegistry creates a registry from the given tools.
// Panics on duplicate names (a programming error, not a runtime condition).
func NewRegistry(tools ...agent.Tool) *Registry {
	r := &Registry{tools: make(map[string]agent.Tool, len(tools))}
	for _, t := range tools {
		name := t.Name()
		if _, exists := r.tools[name]; exists {
			panic("duplicate tool name: " + name)
		}
		r.tools[name] = t
		r.order = append(r.order, name)
	}
	return r
}

// Get returns the tool with the given name, or nil if not found.
func (r *Registry) Get(name string) agent.Tool {
	return r.tools[name]
}

// Resolve implements agent.ToolResolver.
func (r *Registry) Resolve(name string) (agent.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns all tool definitions in registration order.
func (r *Registry) Definitions() []api.ToolDefinition {
	defs := make([]api.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}
