package command

import (
	"context"
	"fmt"

	"github.com/marcomoesman/prompto/internal/permission"
)

// ModeCommand sets or cycles the permission mode. With no arg it cycles
// (mirrors Ctrl+Y) between default and acceptEdits. Bypass is sticky —
// once entered it can't be left via this command (matches Ctrl+Y).
type ModeCommand struct{}

// NewModeCommand returns a /mode command.
func NewModeCommand() Command { return ModeCommand{} }

// Name returns the canonical name.
func (ModeCommand) Name() string { return "mode" }

// Aliases lists alternate names.
func (ModeCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (ModeCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (ModeCommand) Help() string {
	return "show or set permission mode (default | acceptEdits | bypass)"
}

// Exec resolves the requested mode (or cycles when no arg) and applies it.
func (ModeCommand) Exec(_ context.Context, args []string, env Env) (Result, error) {
	ev := env.Evaluator()
	if ev == nil {
		return Result{Message: "no permission evaluator wired"}, nil
	}
	if len(args) == 0 {
		next := permission.Cycle(ev.Mode())
		ev.SetMode(next)
		return Result{Message: "mode: " + next.String()}, nil
	}
	mode, err := permission.ParseMode(args[0])
	if err != nil {
		return Result{}, fmt.Errorf("parse mode: %w", err)
	}
	ev.SetMode(mode)
	return Result{Message: "mode: " + mode.String()}, nil
}
