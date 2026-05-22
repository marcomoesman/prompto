package store

import (
	"errors"
	"testing"
	"time"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestSessions_CreateAndGetRoundTrip(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	sess, err := s.CreateSession(ctx, CreateSessionInput{
		Model: "sonnet-4-6", Title: "demo",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID == "" {
		t.Error("ID is empty")
	}
	if sess.Status != "active" {
		t.Errorf("Status = %q, want active", sess.Status)
	}
	if sess.AgentName != "build" {
		t.Errorf("AgentName = %q, want build", sess.AgentName)
	}

	got, err := s.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Model != "sonnet-4-6" || got.Title != "demo" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestSessions_CreateRequiresModel(t *testing.T) {
	s := openMem(t)
	_, err := s.CreateSession(t.Context(), CreateSessionInput{})
	if err == nil {
		t.Error("expected error when Model is empty")
	}
}

func TestSessions_GetNotFound(t *testing.T) {
	s := openMem(t)
	_, err := s.GetSession(t.Context(), "nope")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestSessions_ListByUpdatedAtDesc(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	a, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
	time.Sleep(2 * time.Millisecond)
	b, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
	time.Sleep(2 * time.Millisecond)
	c, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})

	got, err := s.ListSessions(ctx, 0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3", len(got))
	}
	if got[0].ID != c.ID || got[1].ID != b.ID || got[2].ID != a.ID {
		t.Errorf("order wrong: %s %s %s (want %s %s %s)",
			got[0].ID[:8], got[1].ID[:8], got[2].ID[:8],
			c.ID[:8], b.ID[:8], a.ID[:8])
	}
}

func TestSessions_FindByPrefixTooShort(t *testing.T) {
	s := openMem(t)
	_, err := s.FindSessionByPrefix(t.Context(), "abc")
	if !errors.Is(err, ErrPrefixTooShort) {
		t.Errorf("err = %v, want ErrPrefixTooShort", err)
	}
}

func TestSessions_FindByPrefixNotFound(t *testing.T) {
	s := openMem(t)
	_, err := s.FindSessionByPrefix(t.Context(), "deadbeef")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestSessions_FindByPrefixUnique(t *testing.T) {
	s := openMem(t)
	sess, _ := s.CreateSession(t.Context(), CreateSessionInput{Model: "m"})
	prefix := sess.ID[:MinSessionPrefix]
	got, err := s.FindSessionByPrefix(t.Context(), prefix)
	if err != nil {
		t.Fatalf("FindSessionByPrefix: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("got %s, want %s", got.ID, sess.ID)
	}
}

func TestSessions_FindByPrefixAmbiguous(t *testing.T) {
	// Manually insert two sessions with IDs sharing an 8-char prefix so we
	// can exercise the ambiguity path deterministically. UUID collisions are
	// impossible in practice, but we still want the code path covered.
	s := openMem(t)
	ctx := t.Context()

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, model, agent_name, status, created_at, updated_at)
		 VALUES ('abcdef1200000001', 'm', 'build', 'active', 1, 1),
		        ('abcdef1200000002', 'm', 'build', 'active', 2, 2)`,
	)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err = s.FindSessionByPrefix(ctx, "abcdef12")
	if !errors.Is(err, ErrSessionAmbiguous) {
		t.Errorf("err = %v, want ErrSessionAmbiguous", err)
	}
}

func TestSessions_SetStatusAndTitle(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	sess, _ := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
	if err := s.SetSessionStatus(ctx, sess.ID, "ended"); err != nil {
		t.Fatalf("SetSessionStatus: %v", err)
	}
	if err := s.SetSessionTitle(ctx, sess.ID, "new title"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}

	got, _ := s.GetSession(ctx, sess.ID)
	if got.Status != "ended" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Title != "new title" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestSessions_SetModel(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	sess, _ := s.CreateSession(ctx, CreateSessionInput{Model: "old"})
	if err := s.SetModel(ctx, sess.ID, "new"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	got, _ := s.GetSession(ctx, sess.ID)
	if got.Model != "new" {
		t.Errorf("Model = %q, want %q", got.Model, "new")
	}
	if err := s.SetModel(ctx, sess.ID, ""); err == nil {
		t.Error("SetModel empty: expected error, got nil")
	}
	if err := s.SetModel(ctx, "missing-id", "x"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("SetModel unknown id: err = %v, want ErrSessionNotFound", err)
	}
}

// TestSessions_PlanPathRoundTrip covers the plan_path
// column. Newly created sessions report an empty plan_path; once
// SetPlanPath records a value, LoadPlanPath returns it; subsequent
// calls overwrite. Empty-string clears the column back to NULL.
func TestSessions_PlanPathRoundTrip(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	sess, err := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.LoadPlanPath(ctx, sess.ID)
	if err != nil {
		t.Fatalf("LoadPlanPath (initial): %v", err)
	}
	if got != "" {
		t.Errorf("initial plan_path = %q, want empty", got)
	}

	planPath := "/proj/.prompto/plans/2026-04-30-undo.md"
	if err := s.SetPlanPath(ctx, sess.ID, planPath); err != nil {
		t.Fatalf("SetPlanPath: %v", err)
	}
	got, err = s.LoadPlanPath(ctx, sess.ID)
	if err != nil {
		t.Fatalf("LoadPlanPath (after set): %v", err)
	}
	if got != planPath {
		t.Errorf("plan_path = %q, want %q", got, planPath)
	}

	// Overwrite.
	updated := "/proj/.prompto/plans/2026-05-01-revised.md"
	if err := s.SetPlanPath(ctx, sess.ID, updated); err != nil {
		t.Fatalf("SetPlanPath (overwrite): %v", err)
	}
	got, _ = s.LoadPlanPath(ctx, sess.ID)
	if got != updated {
		t.Errorf("after overwrite = %q, want %q", got, updated)
	}

	// Clear.
	if err := s.SetPlanPath(ctx, sess.ID, ""); err != nil {
		t.Fatalf("SetPlanPath (clear): %v", err)
	}
	got, _ = s.LoadPlanPath(ctx, sess.ID)
	if got != "" {
		t.Errorf("after clear = %q, want empty", got)
	}

	// Unknown session ids surface ErrSessionNotFound.
	if err := s.SetPlanPath(ctx, "missing", "/x.md"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("SetPlanPath unknown: err = %v, want ErrSessionNotFound", err)
	}
	if _, err := s.LoadPlanPath(ctx, "missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("LoadPlanPath unknown: err = %v, want ErrSessionNotFound", err)
	}
}

func TestSessions_DeleteAllSessions(t *testing.T) {
	s := openMem(t)
	ctx := t.Context()

	// Seed three sessions, each with a message + a todo.
	for i := 0; i < 3; i++ {
		sess, err := s.CreateSession(ctx, CreateSessionInput{Model: "m"})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if err := s.AppendMessage(ctx, sess.ID, api.NewUserMessage("hi"), nil); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		if err := s.SaveTodos(ctx, SaveTodosInput{
			SessionID: sess.ID,
			Todos:     []Todo{{ID: "1", Content: "task", Status: "pending", ActiveForm: "doing task"}},
		}); err != nil {
			t.Fatalf("SaveTodos: %v", err)
		}
	}

	n, err := s.DeleteAllSessions(ctx)
	if err != nil {
		t.Fatalf("DeleteAllSessions: %v", err)
	}
	if n != 3 {
		t.Errorf("deleted = %d, want 3", n)
	}

	all, err := s.ListSessions(ctx, 100)
	if err != nil {
		t.Fatalf("ListSessions after wipe: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("sessions remaining after wipe: %d (want 0)", len(all))
	}

	// Idempotency: a second wipe on an empty store returns 0 with no error.
	n2, err := s.DeleteAllSessions(ctx)
	if err != nil {
		t.Fatalf("DeleteAllSessions on empty store: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second wipe deleted = %d, want 0", n2)
	}

	// The DB is still usable after a wipe — schema + connection survive.
	if _, err := s.CreateSession(ctx, CreateSessionInput{Model: "m"}); err != nil {
		t.Errorf("CreateSession after wipe failed: %v", err)
	}
}

func TestSessions_SetStatusUnknownID(t *testing.T) {
	s := openMem(t)
	err := s.SetSessionStatus(t.Context(), "missing", "ended")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}
