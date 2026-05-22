package command

import "context"

// QuitCommand exits the TUI cleanly.
type QuitCommand struct{}

// NewQuitCommand returns a /quit command.
func NewQuitCommand() Command { return QuitCommand{} }

// Name returns the canonical name.
func (QuitCommand) Name() string { return "quit" }

// Aliases lists alternate names.
func (QuitCommand) Aliases() []string { return []string{"exit", "q"} }

// Kind reports KindLocal.
func (QuitCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (QuitCommand) Help() string { return "exit prompto" }

// Exec sets Result.Quit.
func (QuitCommand) Exec(_ context.Context, _ []string, _ Env) (Result, error) {
	return Result{Quit: true}, nil
}
