package command

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SessionsCommand lists recent sessions inline so the user can pick one
// for /resume <prefix>. A full-screen picker overlay would be a nicer
// UX; this inline list is the minimum useful version until then.
type SessionsCommand struct{}

// NewSessionsCommand returns a /sessions command.
func NewSessionsCommand() Command { return SessionsCommand{} }

// Name returns the canonical name.
func (SessionsCommand) Name() string { return "sessions" }

// Aliases lists alternate names.
func (SessionsCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (SessionsCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (SessionsCommand) Help() string { return "list recent sessions in this project" }

// Exec lists up to 10 most-recent sessions and renders them as a system
// message. Children are skipped — only top-level primaries appear so the
// list mirrors the picker the polish task will eventually replace this
// with.
func (SessionsCommand) Exec(ctx context.Context, _ []string, env Env) (Result, error) {
	st := env.Store()
	if st == nil {
		return Result{Message: "persistence is disabled; no sessions to list"}, nil
	}
	sessions, err := st.ListSessions(ctx, 10)
	if err != nil {
		return Result{}, fmt.Errorf("list sessions: %w", err)
	}
	if len(sessions) == 0 {
		return Result{Message: "no sessions yet"}, nil
	}

	var b strings.Builder
	b.WriteString("recent sessions (use /resume <prefix> to switch)\n")
	currentID := env.SessionID()
	rendered := 0
	for _, sess := range sessions {
		if sess.ParentID != "" {
			continue
		}
		marker := "  "
		if sess.ID == currentID {
			marker = "* "
		}
		title := sess.Title
		if title == "" {
			title = "(untitled)"
		}
		agentName := sess.AgentName
		if agentName == "" {
			agentName = "build"
		}
		msgs, _ := st.CountMessages(ctx, sess.ID)
		fmt.Fprintf(&b, "%s%s  %-6s  %s  %d msgs  %s\n",
			marker,
			sess.ID[:8],
			agentName,
			sess.UpdatedAt.Format(time.RFC3339),
			msgs,
			title,
		)
		rendered++
	}
	if rendered == 0 {
		b.WriteString("  (no top-level sessions)\n")
	}
	return Result{Message: strings.TrimRight(b.String(), "\n")}, nil
}
