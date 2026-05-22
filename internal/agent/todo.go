package agent

import (
	"context"
	"fmt"
	"strings"
)

// Todo is one entry in a session's todo list. Lives in the agent package
// because both the run loop (system-prompt rendering) and tools (atomic
// replace) work with this shape. Status values match Claude Code's
// precedent: "pending" | "in_progress" | "completed".
type Todo struct {
	ID         string
	Content    string
	Status     string
	ActiveForm string
}

// TodoStore is the narrow persistence interface the agent loop uses for
// todos. Lives here (not internal/store) so the agent package never
// imports the concrete store. The shapes use agent.Todo; main.go provides
// an adapter that translates to/from store.Todo.
type TodoStore interface {
	LoadTodos(ctx context.Context, sessionID string) ([]Todo, error)
	SaveTodos(ctx context.Context, sessionID string, todos []Todo) error
}

// TodoSaver is the closure stamped onto ToolContext.SaveTodos by the run
// loop. The TodoWrite tool calls it to persist the new list atomically.
// nil is a no-op so tests without a store still work.
type TodoSaver func(ctx context.Context, sessionID string, todos []Todo) error

// renderTodos produces the markdown block that lands in the volatile
// system-prompt section. Two glyphs match the on-disk shape:
//   - [ ] pending
//   - [~] in progress
//
// Completed todos are intentionally omitted to keep the volatile section
// short — they're informational only and rebill on every turn. A trailing
// "(N completed hidden)" line preserves visibility into history without
// the per-item cost.
//
// Returns "" when nothing actionable remains; callers must check for the
// empty string before emitting a section.
func renderTodos(todos []Todo) string {
	var b strings.Builder
	var actionable, completed int
	for _, t := range todos {
		if t.Status == "completed" {
			completed++
			continue
		}
		if actionable == 0 {
			b.WriteString("# Current todos")
		}
		actionable++
		b.WriteByte('\n')
		if t.Status == "in_progress" {
			active := t.ActiveForm
			if active == "" {
				active = t.Content
			}
			fmt.Fprintf(&b, "- [~] %s (in progress: %s)", t.Content, active)
			continue
		}
		fmt.Fprintf(&b, "- [ ] %s", t.Content)
	}
	if actionable == 0 {
		return ""
	}
	if completed > 0 {
		fmt.Fprintf(&b, "\n(%d completed hidden)", completed)
	}
	return b.String()
}

// CountTodos tallies the list by status. Used by the TUI status bar and
// the tool indicator.
func CountTodos(todos []Todo) (pending, inProgress, completed int) {
	for _, t := range todos {
		switch t.Status {
		case "in_progress":
			inProgress++
		case "completed":
			completed++
		default:
			pending++
		}
	}
	return
}
