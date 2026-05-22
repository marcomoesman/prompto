package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ClearCommand ends the current session, starts fresh, and purges the
// per-project tmp directory. /clear is the standard "wipe and continue"
// gesture.
type ClearCommand struct{}

// NewClearCommand returns a /clear command.
func NewClearCommand() Command { return ClearCommand{} }

// Name returns the canonical name.
func (ClearCommand) Name() string { return "clear" }

// Aliases lists alternate names.
func (ClearCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (ClearCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (ClearCommand) Help() string { return "end current session, start fresh, purge .prompto/tmp/" }

// Exec ends the current session, starts a new one, and best-effort purges
// .prompto/tmp/. Tmp purge failures are logged as a system message but do
// not fail the command — the user has already lost the previous session.
func (ClearCommand) Exec(ctx context.Context, _ []string, env Env) (Result, error) {
	if err := env.EndCurrentSession(ctx); err != nil {
		return Result{}, fmt.Errorf("end current session: %w", err)
	}
	if err := env.StartNewSession(ctx); err != nil {
		return Result{}, fmt.Errorf("start new session: %w", err)
	}

	tmpDir := filepath.Join(env.Cwd(), ".prompto", "tmp")
	if err := purgeTmpDir(tmpDir); err != nil {
		env.AppendSystemMessage(fmt.Sprintf("warning: could not purge %s: %v", tmpDir, err))
	}

	return Result{Message: "session cleared"}, nil
}

// purgeTmpDir removes every file directly inside dir. Subdirectories are
// also removed. Missing dir is treated as success (nothing to clean).
func purgeTmpDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
