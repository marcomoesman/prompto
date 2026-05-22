package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Todo is one row from the todos JSON blob. The shape is a JSON-shaped twin
// of internal/tool.Todo so neither package has to import the other; the
// tool package converts at the seam.
type Todo struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Status     string `json:"status"` // "pending" | "in_progress" | "completed"
	ActiveForm string `json:"active_form"`
}

// SaveTodosInput bundles the inputs to SaveTodos. Declared before the
// function per CLAUDE.md.
type SaveTodosInput struct {
	SessionID string
	Todos     []Todo
}

// SaveTodos overwrites the todo list for sessionID atomically. An empty
// Todos slice persists an empty JSON array (so LoadTodos returns a non-nil
// empty slice on next read; callers can distinguish "explicitly cleared"
// from "never written" via the absence of a row, but neither case is
// surfaced today — both render as no-todos).
func (s *Store) SaveTodos(ctx context.Context, in SaveTodosInput) error {
	if in.SessionID == "" {
		return fmt.Errorf("store: SaveTodos: SessionID is required")
	}
	todos := in.Todos
	if todos == nil {
		todos = []Todo{}
	}
	data, err := json.Marshal(todos)
	if err != nil {
		return fmt.Errorf("store: marshal todos: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO todos (session_id, todos_json, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET
		   todos_json = excluded.todos_json,
		   updated_at = excluded.updated_at`,
		in.SessionID, string(data), time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("store: upsert todos: %w", err)
	}
	return nil
}

// LoadTodos returns the persisted list for sessionID. Returns (nil, nil)
// when no row exists for that session — callers treat empty as "no todos."
// An empty sessionID is rejected with an error to match SaveTodos and
// the rest of the Store surface; silently returning nil masked
// uninitialized-state bugs in upstream callers.
func (s *Store) LoadTodos(ctx context.Context, sessionID string) ([]Todo, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("store: LoadTodos: sessionID is required")
	}
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT todos_json FROM todos WHERE session_id = ?`, sessionID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: load todos: %w", err)
	}
	var todos []Todo
	if err := json.Unmarshal([]byte(raw), &todos); err != nil {
		return nil, fmt.Errorf("store: parse todos: %w", err)
	}
	return todos, nil
}
