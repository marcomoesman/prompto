package command

import "context"

// InitCommand asks the agent to inspect the project and generate an
// AGENTS.md file. KindExpanding: the synthesized prompt is fed through
// agent.Run as if the user typed it. The model decides what to write —
// this command only frames the request.
type InitCommand struct{}

// NewInitCommand returns a /init command.
func NewInitCommand() Command { return InitCommand{} }

// Name returns the canonical name.
func (InitCommand) Name() string { return "init" }

// Aliases lists alternate names.
func (InitCommand) Aliases() []string { return nil }

// Kind reports KindExpanding.
func (InitCommand) Kind() Kind { return KindExpanding }

// Help is the one-liner.
func (InitCommand) Help() string { return "generate AGENTS.md for this project" }

// Exec returns the canned init prompt.
func (InitCommand) Exec(_ context.Context, _ []string, _ Env) (Result, error) {
	return Result{Prompt: initPrompt}, nil
}

const initPrompt = `Inspect this project's tech stack, layout, and conventions, then create an AGENTS.md file at the repository root.

Include sections for: tech stack and runtime, how to test/build/lint, project structure (top-level dirs and their purpose), and any non-obvious style or workflow rules a new contributor should know.

Keep it focused and skimmable. If an AGENTS.md already exists, read it first and only update sections that are stale or missing.`
