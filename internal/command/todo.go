package command

import (
	"context"
	"fmt"
	"strings"
)

// TodoCommand renders the persisted todo list for the active session.
// Read-only — the model writes via the todowrite tool; this command
// lets the user inspect state without scrolling history.
type TodoCommand struct{}

// NewTodoCommand returns a /todo command.
func NewTodoCommand() Command { return TodoCommand{} }

// Name returns the canonical name.
func (TodoCommand) Name() string { return "todo" }

// Aliases lists alternate names.
func (TodoCommand) Aliases() []string { return []string{"todos"} }

// Kind reports KindLocal.
func (TodoCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (TodoCommand) Help() string { return "show the persisted todo list (read-only)" }

// Exec loads the session's todo list and prints it. Empty list / no store
// yields a friendly message rather than a blank line.
func (TodoCommand) Exec(ctx context.Context, _ []string, env Env) (Result, error) {
	store := env.Agent().Todos()
	if store == nil {
		return Result{Message: "todos disabled (no TodoStore wired)"}, nil
	}
	if env.SessionID() == "" {
		return Result{Message: "no active session"}, nil
	}
	todos, err := store.LoadTodos(ctx, env.SessionID())
	if err != nil {
		return Result{}, fmt.Errorf("load todos: %w", err)
	}
	if len(todos) == 0 {
		return Result{Message: "no todos yet"}, nil
	}

	var b strings.Builder
	b.WriteString("todos\n")
	for _, t := range todos {
		switch t.Status {
		case "in_progress":
			active := t.ActiveForm
			if active == "" {
				active = t.Content
			}
			fmt.Fprintf(&b, "  [~] %s — %s\n", t.Content, active)
		case "completed":
			fmt.Fprintf(&b, "  [x] %s\n", t.Content)
		default:
			fmt.Fprintf(&b, "  [ ] %s\n", t.Content)
		}
	}
	return Result{Message: strings.TrimRight(b.String(), "\n")}, nil
}
