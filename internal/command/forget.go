package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ForgetCommand is the in-TUI counterpart to the --clear-history CLI
// flag: it deletes every session in the project's store along with
// the on-disk session-scoped artefacts (.prompto/tmp/ spills,
// .prompto/plans/ files), then starts a brand-new empty session so
// the user can keep typing.
//
// Two-step invocation: a bare `/forget` previews what would be
// deleted and asks for explicit confirmation; only `/forget yes`
// (case-insensitive) actually performs the wipe. Statelessness is
// preserved by encoding the confirmation in the argument rather than
// stamping a timer on the command struct.
type ForgetCommand struct{}

// NewForgetCommand returns a /forget command.
func NewForgetCommand() Command { return ForgetCommand{} }

// Name returns the canonical name.
func (ForgetCommand) Name() string { return "forget" }

// Aliases lists alternate names. None — `/clear` already exists with a
// different meaning (end current, start new) and `/forget` is the
// well-known idiom for "wipe history" in chat tools.
func (ForgetCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (ForgetCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (ForgetCommand) Help() string {
	return "delete every session in this project (run `/forget yes` to confirm)"
}

// Exec previews the wipe on bare invocation, performs it on `/forget yes`.
// Anything else surfaces a short hint so a typo doesn't silently abort.
func (ForgetCommand) Exec(ctx context.Context, args []string, env Env) (Result, error) {
	st := env.Store()
	if st == nil {
		return Result{Message: "/forget: persistence is disabled in this session — nothing to clear"}, nil
	}

	// Preview: count sessions so the user knows what they're about to lose.
	all, err := st.ListSessions(ctx, 1000)
	if err != nil {
		return Result{}, fmt.Errorf("count sessions: %w", err)
	}
	if len(all) == 0 {
		return Result{Message: "/forget: no sessions to clear"}, nil
	}

	confirmed := len(args) > 0 && (args[0] == "yes" || args[0] == "YES" || args[0] == "Yes")
	if !confirmed {
		if len(args) > 0 {
			return Result{Message: fmt.Sprintf(
				"/forget: unknown argument %q. Type `/forget yes` to confirm, or `/forget` alone for the warning.",
				args[0])}, nil
		}
		msg := fmt.Sprintf(
			"/forget will permanently delete %d session(s) in this project — every message, file change, todo, plan file, and tmp spill.\n"+
				"This action cannot be undone.\n\n"+
				"Type `/forget yes` to confirm.",
			len(all))
		return Result{Message: msg}, nil
	}

	// Confirmed. Wipe the DB first so the live session's row also gets
	// removed, then start a fresh session so the user has somewhere to
	// keep typing. StartNewSession resets the in-memory conversation,
	// chat, todos, and panel state.
	n, err := st.DeleteAllSessions(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("clear history: %w", err)
	}
	cwd := env.Cwd()
	if err := os.RemoveAll(filepath.Join(cwd, ".prompto", "tmp")); err != nil {
		env.AppendSystemMessage(fmt.Sprintf("warning: removing .prompto/tmp: %v", err))
	}
	if err := os.RemoveAll(filepath.Join(cwd, ".prompto", "plans")); err != nil {
		env.AppendSystemMessage(fmt.Sprintf("warning: removing .prompto/plans: %v", err))
	}
	if err := env.StartNewSession(ctx); err != nil {
		return Result{}, fmt.Errorf("start new session after wipe: %w", err)
	}
	return Result{Message: fmt.Sprintf("cleared %d session(s); started a fresh one", n)}, nil
}
