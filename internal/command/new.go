package command

import (
	"context"
	"fmt"
)

// NewSessionCommand starts a fresh session without quitting. The current
// session is marked ended; a new one is created with the same model and
// agent.
type NewSessionCommand struct{}

// NewNewSessionCommand returns a /new command.
func NewNewSessionCommand() Command { return NewSessionCommand{} }

// Name returns the canonical name.
func (NewSessionCommand) Name() string { return "new" }

// Aliases lists alternate names.
func (NewSessionCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (NewSessionCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (NewSessionCommand) Help() string { return "start a fresh session (current session is ended)" }

// Exec ends the current session and starts a new one.
func (NewSessionCommand) Exec(ctx context.Context, _ []string, env Env) (Result, error) {
	if err := env.EndCurrentSession(ctx); err != nil {
		return Result{}, fmt.Errorf("end current session: %w", err)
	}
	if err := env.StartNewSession(ctx); err != nil {
		return Result{}, fmt.Errorf("start new session: %w", err)
	}
	return Result{Message: "started a fresh session"}, nil
}
