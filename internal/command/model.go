package command

import (
	"context"
	"fmt"
	"strings"
)

// ModelCommand reports or switches the active model. With no arg it
// renders the current model plus the list of every model configured
// across every provider, marking the active one. With one arg it
// hands the name to env.SetModel — which validates against the
// configured list and rebuilds the provider when the new model lives
// under a different provider entry.
type ModelCommand struct{}

// NewModelCommand returns a /model command.
func NewModelCommand() Command { return ModelCommand{} }

// Name returns the canonical name.
func (ModelCommand) Name() string { return "model" }

// Aliases lists alternate names.
func (ModelCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (ModelCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (ModelCommand) Help() string {
	return "list configured models, or /model <name> to switch"
}

// Exec opens the picker on no-args, or directly switches to args[0]
// when given. The picker is the primary path — direct-switch stays
// for muscle-memory and scriptable contexts (e.g., piping a name in).
//
// When the host hasn't loaded a config (Models() is empty), the
// no-args path falls back to a plain text listing so the legacy
// behaviour still works for tests that bypass NewAppModel's config
// plumbing.
func (ModelCommand) Exec(_ context.Context, args []string, env Env) (Result, error) {
	if len(args) == 0 {
		if len(env.Models()) == 0 {
			return Result{Message: renderModelList(env.Model(), env.Models())}, nil
		}
		return Result{OpenModelPicker: true}, nil
	}
	if err := env.SetModel(args[0]); err != nil {
		return Result{}, fmt.Errorf("set model: %w", err)
	}
	return Result{Message: "model: " + args[0]}, nil
}

// renderModelList formats the current-model line plus a one-per-line
// list of every configured model, marking the active one. Falls back
// to the bare current-model line when env.Models() returns nothing
// (e.g. tests that construct AppModel without a config).
func renderModelList(current string, models []ModelInfo) string {
	if len(models) == 0 {
		return "model: " + current
	}
	var b strings.Builder
	fmt.Fprintf(&b, "model: %s\n\navailable:\n", current)
	for _, m := range models {
		marker := "    "
		if m.Name == current {
			marker = "  ▸ "
		}
		fmt.Fprintf(&b, "%s%s  (%s, max_tokens %d)\n", marker, m.Name, m.Provider, m.MaxTokens)
	}
	b.WriteString("\nswitch with: /model <name>")
	return b.String()
}
