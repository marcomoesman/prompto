package agent

import (
	"fmt"
	"sort"
)

// AgentRegistry stores AgentDefinitions keyed by name. Construct via
// DefaultRegistry for the built-ins; tests build empty registries with
// NewAgentRegistry and Register manually. Reads are safe for concurrent
// callers; writes (Register) must be sequenced before any reads.
type AgentRegistry struct {
	defs map[string]AgentDefinition
}

// NewAgentRegistry returns an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{defs: make(map[string]AgentDefinition)}
}

// Register adds a definition. Duplicate names return an error rather than
// overwriting, so misconfigured init paths fail loudly.
func (r *AgentRegistry) Register(def AgentDefinition) error {
	if def.Name == "" {
		return fmt.Errorf("agent registry: definition has empty Name")
	}
	if _, exists := r.defs[def.Name]; exists {
		return fmt.Errorf("agent registry: duplicate name %q", def.Name)
	}
	r.defs[def.Name] = def
	return nil
}

// Resolve returns the definition for name. The bool is false when no agent
// of that name is registered.
func (r *AgentRegistry) Resolve(name string) (AgentDefinition, bool) {
	if r == nil {
		return AgentDefinition{}, false
	}
	def, ok := r.defs[name]
	return def, ok
}

// Primaries returns every definition with Mode == ModePrimary or ModeBoth,
// sorted by Name. Used by the TUI to build the Tab cycle.
func (r *AgentRegistry) Primaries() []AgentDefinition {
	return r.filter(func(def AgentDefinition) bool {
		return def.Mode == ModePrimary || def.Mode == ModeBoth
	})
}

// Subagents returns every definition spawnable from the task tool. Used by
// the task tool to validate the subagent_type parameter.
func (r *AgentRegistry) Subagents() []AgentDefinition {
	return r.filter(func(def AgentDefinition) bool {
		return def.Mode == ModeSubagent || def.Mode == ModeBoth
	})
}

// Names returns every registered agent name in deterministic order.
func (r *AgentRegistry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.defs))
	for name := range r.defs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *AgentRegistry) filter(keep func(AgentDefinition) bool) []AgentDefinition {
	if r == nil {
		return nil
	}
	var out []AgentDefinition
	for _, def := range r.defs {
		if keep(def) {
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DefaultRegistry returns the registry seeded with the built-in agents:
// build (primary, all tools), plan (primary, read-only with plan-mode),
// explore (subagent, read-only investigator).
func DefaultRegistry() *AgentRegistry {
	r := NewAgentRegistry()
	for _, def := range builtinAgents() {
		if err := r.Register(def); err != nil {
			// Built-in registration must not fail; Names are unique by
			// construction. Surface as a panic so the bug is loud during
			// development.
			panic(fmt.Sprintf("agent: built-in registration failed: %v", err))
		}
	}
	return r
}

// builtinAgents returns the seed AgentDefinitions for the prompto built-ins.
// Kept private so callers always reach for DefaultRegistry; tests that need
// just the seed slice can use the registry's accessors instead.
func builtinAgents() []AgentDefinition {
	return []AgentDefinition{
		{
			Name:        "build",
			Mode:        ModePrimary,
			Description: "default builder: read, edit, write, run shell, all tools",
			Color:       "14", // bright cyan
		},
		{
			Name:        "plan",
			Mode:        ModePrimary,
			Description: "investigate and write a plan; cannot edit non-plan files",
			Tools: []string{
				"read", "grep", "glob", "list", "webfetch", "webfetch_headless", "websearch",
				"edit", "replace_lines", "write", "bash", "todowrite", "task", "plan_exit",
			},
			ReadOnly:     true,
			PlanMode:     true,
			Color:        "11", // yellow
			SystemPrompt: PlanSystemPrompt,
		},
		{
			Name:        "explore",
			Mode:        ModeSubagent,
			Description: "concise read-only investigator; returns a tight summary",
			Tools: []string{
				"read", "grep", "glob", "list", "webfetch", "webfetch_headless",
			},
			ReadOnly:     true,
			Color:        "13", // magenta
			SystemPrompt: ExploreSystemPrompt,
		},
		{
			Name:        "research",
			Mode:        ModeSubagent,
			Description: "online research: search docs, fetch pages, return a cited summary",
			Tools: []string{
				"read", "grep", "glob",
				"webfetch", "webfetch_headless", "websearch",
			},
			ReadOnly:     true,
			Color:        "12", // bright blue
			SystemPrompt: ResearchSystemPrompt,
		},
	}
}
