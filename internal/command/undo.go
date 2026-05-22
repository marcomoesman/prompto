package command

import (
	"context"
	"fmt"
	"os"

	"github.com/marcomoesman/prompto/internal/store"
)

// UndoCommand reverts the most recent file change recorded for the
// current session. Edits/Writes/creates/deletes all reverse cleanly when
// the row's content snapshots are intact. Truncated rows (>1 MB) cannot
// be reversed — the user is told and the row is left in place.
//
// One /undo undoes one change. Run repeatedly to step further back. The
// conversation history is left intact: the model sees the reverted file
// the next time it Reads it, and can react.
type UndoCommand struct{}

// NewUndoCommand returns a /undo command.
func NewUndoCommand() Command { return UndoCommand{} }

// Name returns the canonical name.
func (UndoCommand) Name() string { return "undo" }

// Aliases lists alternate names.
func (UndoCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (UndoCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (UndoCommand) Help() string { return "revert the most recent file change in this session" }

// Exec finds the most recent file_changes row for the active session,
// applies the inverse of its op to disk, and removes the row from the
// store.
func (UndoCommand) Exec(ctx context.Context, _ []string, env Env) (Result, error) {
	st := env.Store()
	if st == nil {
		return Result{Message: "persistence is disabled; nothing to undo"}, nil
	}
	if env.SessionID() == "" {
		return Result{Message: "no active session"}, nil
	}

	changes, err := st.ListFileChangesBySession(ctx, env.SessionID())
	if err != nil {
		return Result{}, fmt.Errorf("list file changes: %w", err)
	}
	if len(changes) == 0 {
		return Result{Message: "nothing to undo (no file changes recorded this session)"}, nil
	}

	target := changes[0] // ListFileChangesBySession is DESC by created_at, id
	if target.Truncated {
		return Result{Message: fmt.Sprintf("can't undo %s: snapshot was too large to record", target.Path)}, nil
	}

	if err := revertFileChange(target); err != nil {
		return Result{}, fmt.Errorf("revert %s: %w", target.Path, err)
	}
	if err := st.DeleteFileChange(ctx, target.ID); err != nil {
		return Result{}, fmt.Errorf("clear file_changes row: %w", err)
	}
	return Result{Message: fmt.Sprintf("undone: %s (%s)", target.Path, target.Op)}, nil
}

// revertFileChange applies the inverse of fc.Op to disk:
//
//   - "create": delete the file. Missing → success (already gone).
//   - "modify": restore ContentBefore. Empty before → empty file.
//   - "delete": recreate with ContentBefore.
func revertFileChange(fc store.FileChange) error {
	switch fc.Op {
	case "create":
		if err := os.Remove(fc.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case "modify", "delete":
		return os.WriteFile(fc.Path, fc.ContentBefore, 0o644)
	default:
		return fmt.Errorf("unknown op %q", fc.Op)
	}
}
