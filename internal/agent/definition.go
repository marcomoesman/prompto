package agent

import "github.com/marcomoesman/prompto/internal/api"

// AgentMode classifies how an agent may be invoked. Primaries are launched
// by the user (CLI / Tab cycling); subagents are spawned only by the task
// tool from a primary. ModeBoth lets a definition wear both hats.
type AgentMode string

const (
	ModePrimary  AgentMode = "primary"
	ModeSubagent AgentMode = "subagent"
	ModeBoth     AgentMode = "both"
)

// SystemPromptBuilder returns the assembled system prompt for an agent. nil
// signals "use the default BuildSystemPrompt"; named agents (plan, explore)
// supply their own builders here so per-agent prompts can still leverage
// BuildSystemPromptInput (cwd, model, date, project instructions).
type SystemPromptBuilder func(in BuildSystemPromptInput) []api.SystemBlock

// AgentDefinition is the static description of one named agent. Definitions
// are immutable after construction; they are looked up by name in the
// AgentRegistry whenever a Run starts. Fields are deliberately lean: rule
// authoring (plan-mode permissions, etc.) lives outside the agent package
// so this struct stays free of the permission import.
type AgentDefinition struct {
	// Name is the canonical identifier. Must be unique within a registry.
	Name string
	// Mode controls who can invoke the agent.
	Mode AgentMode
	// Description is surfaced in the task tool's subagent_type parameter
	// description and in --sessions output.
	Description string
	// SystemPrompt builds the system prompt for this agent. nil falls back
	// to BuildSystemPrompt.
	SystemPrompt SystemPromptBuilder
	// Tools is the explicit tool allowlist for this agent. An empty slice
	// means "all tools that aren't in AllAgentDisallowedTools" — the build
	// agent uses this. AllAgentDisallowedTools is always subtracted.
	Tools []string
	// ReadOnly is a sticky note for the TUI/UX layer ("plan agent doesn't
	// edit"). Permission enforcement is the evaluator's job; this flag is
	// purely descriptive.
	ReadOnly bool
	// PlanMode enables the per-turn plan-mode reminder injection in the
	// run loop and signals the TUI to swap permission rules.
	PlanMode bool
	// Color is a lipgloss color string ("14", "11", "#5fafff"). Empty means
	// "use default". Kept as a plain string so the agent package doesn't
	// import lipgloss.
	Color string
	// MaxSteps overrides the default 25-step ceiling for this agent. 0
	// inherits.
	MaxSteps int
}

// EffectiveTools returns the agent's allowlist minus globally disallowed
// tools. Empty means "all non-disallowed tools, resolved at runtime."
func (d AgentDefinition) EffectiveTools() []string {
	if len(d.Tools) == 0 {
		return nil
	}
	out := make([]string, 0, len(d.Tools))
	for _, name := range d.Tools {
		if AllAgentDisallowedTools[name] {
			continue
		}
		out = append(out, name)
	}
	return out
}

// AllowsAllTools reports whether the agent's allowlist is empty — i.e. the
// run loop should expose every non-disallowed tool the resolver knows.
func (d AgentDefinition) AllowsAllTools() bool {
	return len(d.Tools) == 0
}
