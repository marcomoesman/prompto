package command

import "fmt"

// RegisterBuiltins registers every built-in command on r in deterministic
// order. Returns the first registration error so startup fails loudly on
// duplicate names. Built-ins must register before any custom commands so
// their names + aliases reserve themselves first.
func RegisterBuiltins(r *Registry) error {
	for _, c := range builtins() {
		if err := r.Register(c); err != nil {
			return fmt.Errorf("command: register builtin %q: %w", c.Name(), err)
		}
	}
	return nil
}

// builtins returns the canonical set of built-in commands. Order is the
// help-list order (alphabetical inside each category isn't enforced here —
// /help renders Registry.All() which already sorts by name).
func builtins() []Command {
	return []Command{
		NewHelpCommand(),
		NewQuitCommand(),
		NewClearCommand(),
		NewForgetCommand(),
		NewNewSessionCommand(),
		NewCompactCommand(),
		NewContextCommand(),
		NewResumeCommand(),
		NewSessionsCommand(),
		NewModelCommand(),
		NewModeCommand(),
		NewAgentCommand(),
		NewPlanCommand(),
		NewBuildCommand(),
		NewTodoCommand(),
		NewUndoCommand(),
		NewLicenseCommand(),
		NewInitCommand(),
		NewReviewCommand(),
	}
}
