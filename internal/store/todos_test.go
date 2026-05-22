package store

import (
	"reflect"
	"testing"
)

func TestStore_SaveAndLoadTodos(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	sess, err := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	in := []Todo{
		{ID: "a", Content: "Do A", Status: "pending", ActiveForm: "Doing A"},
		{ID: "b", Content: "Do B", Status: "in_progress", ActiveForm: "Doing B"},
	}
	if err := s.SaveTodos(ctx, SaveTodosInput{SessionID: sess.ID, Todos: in}); err != nil {
		t.Fatalf("SaveTodos: %v", err)
	}

	got, err := s.LoadTodos(ctx, sess.ID)
	if err != nil {
		t.Fatalf("LoadTodos: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, in)
	}
}

func TestStore_SaveTodos_AtomicReplace(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	sess, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})

	first := []Todo{
		{ID: "a", Content: "Do A", Status: "pending", ActiveForm: "Doing A"},
		{ID: "b", Content: "Do B", Status: "pending", ActiveForm: "Doing B"},
	}
	if err := s.SaveTodos(ctx, SaveTodosInput{SessionID: sess.ID, Todos: first}); err != nil {
		t.Fatalf("save 1: %v", err)
	}

	// Overwrite with a smaller list — atomic replace, not merge.
	second := []Todo{
		{ID: "c", Content: "Do C", Status: "completed", ActiveForm: "Doing C"},
	}
	if err := s.SaveTodos(ctx, SaveTodosInput{SessionID: sess.ID, Todos: second}); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	got, _ := s.LoadTodos(ctx, sess.ID)
	if !reflect.DeepEqual(got, second) {
		t.Errorf("after atomic replace got %+v, want %+v", got, second)
	}
}

func TestStore_LoadTodos_NoneForUnknownSession(t *testing.T) {
	s := openMem(t)
	got, err := s.LoadTodos(t.Context(), "not-a-session")
	if err != nil {
		t.Fatalf("LoadTodos: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestStore_LoadTodos_EmptySessionIDErrors(t *testing.T) {
	s := openMem(t)
	got, err := s.LoadTodos(t.Context(), "")
	if err == nil {
		t.Error("expected error for empty sessionID")
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestStore_SaveTodos_EmptySessionIDErrors(t *testing.T) {
	s := openMem(t)
	err := s.SaveTodos(t.Context(), SaveTodosInput{Todos: []Todo{{ID: "a"}}})
	if err == nil {
		t.Error("expected error for empty SessionID")
	}
}

func TestStore_SaveTodos_EmptyListPersists(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sess, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})

	if err := s.SaveTodos(ctx, SaveTodosInput{SessionID: sess.ID, Todos: nil}); err != nil {
		t.Fatalf("SaveTodos nil: %v", err)
	}
	got, err := s.LoadTodos(ctx, sess.ID)
	if err != nil {
		t.Fatalf("LoadTodos: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("after nil save got %+v, want empty", got)
	}
}

func TestStore_TodosCascadeOnSessionDelete(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()
	sess, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})

	if err := s.SaveTodos(ctx, SaveTodosInput{
		SessionID: sess.ID,
		Todos:     []Todo{{ID: "a", Content: "x", Status: "pending", ActiveForm: "x"}},
	}); err != nil {
		t.Fatalf("SaveTodos: %v", err)
	}

	// Manually delete the session row; the FK CASCADE should clear todos.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM todos WHERE session_id = ?`, sess.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count todos: %v", err)
	}
	if count != 0 {
		t.Errorf("after session delete, todos rows = %d, want 0 (cascade failed)", count)
	}
}
