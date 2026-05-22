package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/marcomoesman/prompto/internal/store"
)

// ResumeCommand swaps the active session for one identified by id prefix.
// `/resume` with no argument adopts the most recent prior session in this
// project. The current session is marked ended before the swap so its
// status reflects the user moved on.
type ResumeCommand struct{}

// NewResumeCommand returns a /resume command.
func NewResumeCommand() Command { return ResumeCommand{} }

// Name returns the canonical name.
func (ResumeCommand) Name() string { return "resume" }

// Aliases lists alternate names.
func (ResumeCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (ResumeCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (ResumeCommand) Help() string { return "resume a session by id prefix (no arg = most recent)" }

// Exec resolves the prefix, ends the current session, and adopts the
// resolved session.
func (ResumeCommand) Exec(ctx context.Context, args []string, env Env) (Result, error) {
	st := env.Store()
	if st == nil {
		return Result{Message: "persistence is disabled; cannot resume"}, nil
	}

	target, err := pickResumeTarget(ctx, st, env.SessionID(), args)
	if err != nil {
		return Result{Message: err.Error()}, nil
	}

	if err := env.EndCurrentSession(ctx); err != nil {
		return Result{}, fmt.Errorf("end current session: %w", err)
	}
	if err := env.AdoptSession(ctx, target.ID); err != nil {
		return Result{}, fmt.Errorf("adopt session: %w", err)
	}

	prefix := target.ID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	title := target.Title
	if title == "" {
		title = "(untitled)"
	}
	return Result{Message: fmt.Sprintf("resumed %s · %s", prefix, title)}, nil
}

// pickResumeTarget resolves args into a Session. Empty args picks the most
// recent session that isn't the active one. Otherwise the first arg is
// treated as an id prefix.
func pickResumeTarget(ctx context.Context, st *store.Store, currentID string, args []string) (store.Session, error) {
	if len(args) == 0 {
		sessions, err := st.ListSessions(ctx, 5)
		if err != nil {
			return store.Session{}, fmt.Errorf("listing sessions: %w", err)
		}
		for _, sess := range sessions {
			if sess.ID == currentID {
				continue
			}
			if sess.ParentID != "" {
				continue // skip subagent children
			}
			return sess, nil
		}
		return store.Session{}, errors.New("no other session to resume — use `prompto --new` for a fresh start")
	}

	prefix := args[0]
	sess, err := st.FindSessionByPrefix(ctx, prefix)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPrefixTooShort):
			return store.Session{}, fmt.Errorf("prefix too short (need %d chars)", store.MinSessionPrefix)
		case errors.Is(err, store.ErrSessionNotFound):
			return store.Session{}, fmt.Errorf("no session matches prefix %q", prefix)
		case errors.Is(err, store.ErrSessionAmbiguous):
			return store.Session{}, fmt.Errorf("prefix %q matches multiple sessions; lengthen it", prefix)
		default:
			return store.Session{}, err
		}
	}
	if sess.ID == currentID {
		return store.Session{}, fmt.Errorf("already on session %s", sess.ID[:8])
	}
	return sess, nil
}
