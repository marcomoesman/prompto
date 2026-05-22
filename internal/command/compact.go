package command

import (
	"context"
	"fmt"
)

// CompactCommand forces summarization of the active conversation. The
// reactive compactor already runs on a context-limit error, but /compact
// lets the user trigger it explicitly when they're about to feed a long
// follow-up turn.
type CompactCommand struct{}

// NewCompactCommand returns a /compact command.
func NewCompactCommand() Command { return CompactCommand{} }

// Name returns the canonical name.
func (CompactCommand) Name() string { return "compact" }

// Aliases lists alternate names.
func (CompactCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (CompactCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (CompactCommand) Help() string { return "summarize older turns to free context" }

// Exec invokes Compactor.ForceSummarize on the live conversation. Reports
// the before/after token estimate. No-op when no compactor is wired.
func (CompactCommand) Exec(ctx context.Context, _ []string, env Env) (Result, error) {
	c := env.Compactor()
	if c == nil {
		return Result{Message: "compaction is disabled (no Compactor wired)"}, nil
	}
	conv := env.Conversation()
	if conv == nil {
		return Result{Message: "no conversation to compact"}, nil
	}
	summary, boundaryID, err := c.ForceSummarize(ctx, conv, env.Model())
	if err != nil {
		return Result{}, fmt.Errorf("compact: %w", err)
	}
	if summary == nil {
		return Result{Message: "nothing to compact"}, nil
	}
	if st := env.Store(); st != nil && env.SessionID() != "" && boundaryID != "" {
		if err := st.AppendSummaryMessage(ctx, env.SessionID(), *summary, boundaryID); err != nil {
			return Result{}, fmt.Errorf("compact: persist summary: %w", err)
		}
	}
	return Result{Message: "conversation summarized"}, nil
}
