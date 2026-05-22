package command

import "context"

// HelpCommand opens the floating help overlay. The overlay contents are
// rendered by the TUI from Registry.All() plus a static keymap; the
// command itself is a one-line trigger.
type HelpCommand struct{}

// NewHelpCommand returns a /help command. Stateless.
func NewHelpCommand() Command { return HelpCommand{} }

// Name returns the canonical name.
func (HelpCommand) Name() string { return "help" }

// Aliases lists alternate names.
func (HelpCommand) Aliases() []string { return []string{"?"} }

// Kind reports KindLocal.
func (HelpCommand) Kind() Kind { return KindLocal }

// Help is the one-liner shown by /help itself.
func (HelpCommand) Help() string { return "show available commands and keybindings" }

// Exec sets Result.OpenHelp; the TUI mounts the overlay.
func (HelpCommand) Exec(_ context.Context, _ []string, _ Env) (Result, error) {
	return Result{OpenHelp: true}, nil
}
