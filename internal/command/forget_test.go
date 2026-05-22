package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/store"
)

func mkdirAndTouch(dir, file string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	return f.Close()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// forgetTestEnv overlays stubEnv with the surfaces /forget actually
// touches: a real in-memory Store, the project Cwd, a captured
// startNewSessionCalls counter for the post-wipe new-session flow, and
// a sink for any AppendSystemMessage warnings.
type forgetTestEnv struct {
	stubEnv
	st                    *store.Store
	cwd                   string
	startNewSessionCalls  int
	startNewSessionErr    error
	appendedSystemMessage []string
}

func (e *forgetTestEnv) Store() *store.Store { return e.st }
func (e *forgetTestEnv) Cwd() string         { return e.cwd }
func (e *forgetTestEnv) StartNewSession(_ context.Context) error {
	e.startNewSessionCalls++
	return e.startNewSessionErr
}
func (e *forgetTestEnv) AppendSystemMessage(s string) {
	e.appendedSystemMessage = append(e.appendedSystemMessage, s)
}

func openForgetEnv(t *testing.T) *forgetTestEnv {
	t.Helper()
	s, err := store.Open(store.OpenInput{Path: ":memory:"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &forgetTestEnv{st: s, cwd: t.TempDir()}
}

// seedSessions creates n sessions in the store; each gets one message
// + one todo so the wipe touches every dependent table.
func seedSessions(t *testing.T, s *store.Store, n int) {
	t.Helper()
	ctx := t.Context()
	for i := 0; i < n; i++ {
		sess, err := s.CreateSession(ctx, store.CreateSessionInput{Model: "test-model"})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		if err := s.AppendMessage(ctx, sess.ID, api.NewUserMessage("hi"), nil); err != nil {
			t.Fatalf("append msg: %v", err)
		}
		if err := s.SaveTodos(ctx, store.SaveTodosInput{
			SessionID: sess.ID,
			Todos:     []store.Todo{{ID: "1", Content: "task", Status: "pending", ActiveForm: "doing task"}},
		}); err != nil {
			t.Fatalf("save todos: %v", err)
		}
	}
}

// TestForget_BareInvocationPreviewsNothingDeleted is the safety
// regression: a typo'd /forget without `yes` MUST NOT delete anything.
// Critical because the operation is irreversible.
func TestForget_BareInvocationPreviewsNothingDeleted(t *testing.T) {
	env := openForgetEnv(t)
	seedSessions(t, env.st, 3)

	res, err := (ForgetCommand{}).Exec(t.Context(), nil, env)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(res.Message, "/forget yes") {
		t.Errorf("preview message must include the confirm hint; got: %q", res.Message)
	}
	if !strings.Contains(res.Message, "3 session") {
		t.Errorf("preview must show the count of sessions about to be deleted; got: %q", res.Message)
	}

	all, err := env.st.ListSessions(t.Context(), 100)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("bare /forget deleted sessions! count = %d, want 3", len(all))
	}
	if env.startNewSessionCalls != 0 {
		t.Errorf("bare /forget triggered StartNewSession (%d calls); should be a pure preview", env.startNewSessionCalls)
	}
}

// TestForget_YesActuallyClears verifies the confirmed path: db is
// wiped, a fresh session is started, and the message reports the count.
func TestForget_YesActuallyClears(t *testing.T) {
	env := openForgetEnv(t)
	seedSessions(t, env.st, 4)

	res, err := (ForgetCommand{}).Exec(t.Context(), []string{"yes"}, env)
	if err != nil {
		t.Fatalf("Exec(yes): %v", err)
	}
	if !strings.Contains(res.Message, "cleared 4") {
		t.Errorf("message should report count cleared; got: %q", res.Message)
	}

	all, err := env.st.ListSessions(t.Context(), 100)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("sessions remaining after /forget yes: %d (want 0)", len(all))
	}
	if env.startNewSessionCalls != 1 {
		t.Errorf("StartNewSession calls = %d, want 1 (post-wipe must seed a fresh session)", env.startNewSessionCalls)
	}
}

// TestForget_EmptyStoreReturnsCleanly covers the no-op path: nothing
// to delete, and the user shouldn't be asked to confirm a wipe of zero
// sessions. Idempotent if invoked again.
func TestForget_EmptyStoreReturnsCleanly(t *testing.T) {
	env := openForgetEnv(t)

	res, err := (ForgetCommand{}).Exec(t.Context(), nil, env)
	if err != nil {
		t.Fatalf("Exec on empty store: %v", err)
	}
	if !strings.Contains(res.Message, "no sessions") {
		t.Errorf("empty-store message should say so; got: %q", res.Message)
	}
	if env.startNewSessionCalls != 0 {
		t.Errorf("empty store shouldn't trigger StartNewSession; got %d", env.startNewSessionCalls)
	}
}

// TestForget_UnknownArgumentDoesNotDelete guards against a fat-finger
// like `/forget nope` accidentally wiping when the second word isn't
// the literal yes-token. Must be a hint, not a delete.
func TestForget_UnknownArgumentDoesNotDelete(t *testing.T) {
	env := openForgetEnv(t)
	seedSessions(t, env.st, 2)

	res, err := (ForgetCommand{}).Exec(t.Context(), []string{"nope"}, env)
	if err != nil {
		t.Fatalf("Exec(nope): %v", err)
	}
	if !strings.Contains(res.Message, "unknown argument") {
		t.Errorf("expected unknown-argument hint; got: %q", res.Message)
	}

	all, _ := env.st.ListSessions(t.Context(), 100)
	if len(all) != 2 {
		t.Errorf("unknown-arg /forget deleted sessions! count = %d, want 2", len(all))
	}
}

// TestForget_RemovesArtefactDirs confirms .prompto/tmp and
// .prompto/plans both get wiped on the confirmed path.
func TestForget_RemovesArtefactDirs(t *testing.T) {
	env := openForgetEnv(t)
	seedSessions(t, env.st, 1)

	tmpDir := filepath.Join(env.cwd, ".prompto", "tmp")
	plansDir := filepath.Join(env.cwd, ".prompto", "plans")
	for _, d := range []string{tmpDir, plansDir} {
		if err := mkdirAndTouch(d, "leftover.txt"); err != nil {
			t.Fatalf("seed artefact %s: %v", d, err)
		}
	}

	if _, err := (ForgetCommand{}).Exec(t.Context(), []string{"yes"}, env); err != nil {
		t.Fatalf("Exec(yes): %v", err)
	}

	for _, d := range []string{tmpDir, plansDir} {
		if dirExists(d) {
			t.Errorf("artefact dir %s survived /forget yes", d)
		}
	}
}
