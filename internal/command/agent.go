package command

import (
	"context"
	"fmt"
	"strings"
)

// AgentCommand switches the active primary agent. With no arg it lists
// the registered primaries and the current selection. Mirrors Tab cycling
// in the TUI.
type AgentCommand struct{}

// NewAgentCommand returns a /agent command.
func NewAgentCommand() Command { return AgentCommand{} }

// Name returns the canonical name.
func (AgentCommand) Name() string { return "agent" }

// Aliases lists alternate names.
func (AgentCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (AgentCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (AgentCommand) Help() string { return "show or switch the active primary agent" }

// Exec resolves the named agent or lists primaries when no arg.
func (AgentCommand) Exec(_ context.Context, args []string, env Env) (Result, error) {
	reg := env.Registry()
	if reg == nil {
		return Result{Message: "no agent registry wired"}, nil
	}
	if len(args) == 0 {
		primaries := reg.Primaries()
		var b strings.Builder
		b.WriteString("agents (use /agent <name> to switch)\n")
		for _, def := range primaries {
			marker := "  "
			if def.Name == env.AgentName() {
				marker = "* "
			}
			fmt.Fprintf(&b, "%s%s\n", marker, def.Name)
		}
		return Result{Message: strings.TrimRight(b.String(), "\n")}, nil
	}
	if err := env.SwitchAgent(args[0]); err != nil {
		return Result{}, fmt.Errorf("switch agent: %w", err)
	}
	return Result{}, nil
}
