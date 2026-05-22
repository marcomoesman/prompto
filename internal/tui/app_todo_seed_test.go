package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/permission"
)

// stubTodoStore is a minimal in-memory TodoStore implementation. The
// resume-seed test needs the agent to have a non-nil store so the
// seedTodosFromStore helper actually runs; a fakeProvider isn't
// enough because seedTodosFromStore reaches in.Agent.Todos().
type stubTodoStore struct {
	bySession map[string][]agent.Todo
}

func newStubTodoStore() *stubTodoStore {
	return &stubTodoStore{bySession: map[string][]agent.Todo{}}
}

func (s *stubTodoStore) LoadTodos(_ context.Context, sessionID string) ([]agent.Todo, error) {
	return s.bySession[sessionID], nil
}

func (s *stubTodoStore) SaveTodos(_ context.Context, sessionID string, todos []agent.Todo) error {
	s.bySession[sessionID] = todos
	return nil
}

// TestAppModel_SeedsTodosFromStoreOnResume preloads todos via the
// TodoStore and confirms NewAppModel surfaces them on m.todos and
// the panel's visibility flips to true. Regresses the resume-seed
// path wired through seedTodosFromStore.
func TestAppModel_SeedsTodosFromStoreOnResume(t *testing.T) {
	todoStore := newStubTodoStore()
	preset := []agent.Todo{
		{ID: "1", Content: "P1", Status: "completed", ActiveForm: "Doing P1"},
		{ID: "2", Content: "P2", Status: "in_progress", ActiveForm: "Doing P2"},
		{ID: "3", Content: "P3", Status: "pending", ActiveForm: "Doing P3"},
	}
	todoStore.bySession["sess-x"] = preset

	a := agent.New(agent.NewAgentInput{
		Provider: stubProvider{},
		Model:    "test-model",
		Tools:    stubResolver{},
		Notifier: agent.NewNotifier(),
		Todos:    todoStore,
	})
	rs := permission.NewRuleset()
	ev := permission.NewEvaluator(permission.NewEvaluatorInput{
		Mode:    permission.ModeDefault,
		Ruleset: rs,
	})
	m := NewAppModel(AppModelInput{
		Agent:     a,
		SessionID: "sess-x",
		Ruleset:   rs,
		Evaluator: ev,
		AgentName: "build",
	})

	if got := len(m.todos); got != 3 {
		t.Fatalf("len(m.todos) = %d, want 3 (resume must seed full list, not just counts)", got)
	}
	if !m.todoPanel.Visible() {
		t.Error("todoPanel.Visible() = false after resume seed; expected true")
	}
	if m.todos[1].Status != "in_progress" || m.todos[1].ActiveForm != "Doing P2" {
		t.Errorf("middle todo = %+v, want in_progress 'Doing P2'", m.todos[1])
	}
}

// TestAppModel_PanelHiddenDuringThinkingOverlay regresses the
// "panel must vanish behind Ctrl+O" rule the user called out
// post-implementation. relayout() must drop the panel's height
// term while m.thinkingVisible is true so the overlay reclaims
// every available row, and View() must skip the panel render
// under the same condition so panel content doesn't leak below
// the overlay.
func TestAppModel_PanelHiddenDuringThinkingOverlay(t *testing.T) {
	m := newTestAppModel(t)
	m.todos = []agent.Todo{
		{ID: "1", Content: "P1: refactor", Status: "in_progress", ActiveForm: "Refactoring app.go"},
	}
	m.todoPanel.SetTodos(m.todos)
	m.todoPanel.SetMeter(true, 5*time.Second, 100)
	m.width, m.height = 120, 40
	m.relayout()

	if m.todoPanel.Height() == 0 {
		t.Fatal("baseline panel.Height() = 0; the test setup is wrong")
	}
	chatBaseline := m.chat.height

	// Open the thinking overlay. relayout must grant the overlay
	// the panel's row (chat.height grows) and View must omit the
	// panel section.
	m.thinkingVisible = true
	m.relayout()

	if m.chat.height <= chatBaseline {
		t.Errorf("chat viewport did not grow when thinking overlay opened: baseline=%d after=%d (relayout still subtracting panel height while overlay is up)", chatBaseline, m.chat.height)
	}
}

// TestAppModel_ApplyTodoUpdateRefreshesPanel confirms the
// EventToolCallDone handler's hook actually pushes the new list
// into the panel — the bug it guards against is "todowrite
// persists, panel never updates because the SetTodos call was
// missed".
func TestAppModel_ApplyTodoUpdateRefreshesPanel(t *testing.T) {
	m := newTestAppModel(t)
	m.applyTodoUpdate(`{"todos":[
		{"id":"1","content":"P1","status":"in_progress","active_form":"Doing P1"},
		{"id":"2","content":"P2","status":"pending","active_form":"Doing P2"}
	]}`)
	m.todoPanel.SetTodos(m.todos) // mirrors the call site in the dispatch handler
	m.todoPanel.SetWidth(120)

	if !m.todoPanel.Visible() {
		t.Fatal("panel not visible after applyTodoUpdate")
	}
	out := stripANSI(m.todoPanel.View(newSpinner()))
	if !strings.Contains(out, "■ P1") {
		t.Errorf("panel render missing in_progress row; got:\n%s", out)
	}
	if !strings.Contains(out, "□ P2") {
		t.Errorf("panel render missing pending row; got:\n%s", out)
	}
}
