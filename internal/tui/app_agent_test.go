package tui

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/permission"
)

// stubProvider is a minimal api.Provider that yields nothing. Sufficient
// for constructing an Agent — the TUI tests don't drive turns.
type stubProvider struct{}

func (stubProvider) ContextLimit(string) int { return 0 }
func (stubProvider) Complete(_ context.Context, _ api.CompleteParams) iter.Seq[api.StreamEvent] {
	return func(yield func(api.StreamEvent) bool) {}
}

// stubResolver is the empty ToolResolver. The TUI tests don't drive any
// tool calls, so this is enough.
type stubResolver struct{}

func (stubResolver) Resolve(string) (agent.Tool, bool) { return nil, false }
func (stubResolver) Definitions() []api.ToolDefinition { return nil }

func newTestAppModel(t *testing.T) AppModel {
	t.Helper()
	a := agent.New(agent.NewAgentInput{
		Provider: stubProvider{},
		Model:    "test-model",
		Tools:    stubResolver{},
		Notifier: agent.NewNotifier(),
	})
	rs := permission.NewRuleset()
	ev := permission.NewEvaluator(permission.NewEvaluatorInput{
		Mode:    permission.ModeDefault,
		Ruleset: rs,
	})
	return NewAppModel(AppModelInput{
		Agent:     a,
		Ruleset:   rs,
		Evaluator: ev,
		AgentName: "build",
	})
}

func TestCycleAgent_BuildToPlanToBuild(t *testing.T) {
	m := newTestAppModel(t)
	if m.agentName != "build" {
		t.Fatalf("initial agentName = %q, want build", m.agentName)
	}

	got := m.cycleAgent()
	if got != "plan" {
		t.Errorf("first cycle = %q, want plan", got)
	}
	if m.agentName != "plan" {
		t.Errorf("agentName after first cycle = %q, want plan", m.agentName)
	}
	if m.previousAgent != "build" {
		t.Errorf("previousAgent after first cycle = %q, want build", m.previousAgent)
	}
	if m.status.agent != "plan" {
		t.Errorf("status.agent = %q, want plan", m.status.agent)
	}
	if m.welcome.Agent != "plan" {
		t.Errorf("welcome.Agent = %q, want plan", m.welcome.Agent)
	}

	// In plan mode, the evaluator should carry agent rules.
	if rules := m.evaluator.AgentRules(); len(rules) == 0 {
		t.Error("expected plan-mode rules layered on the evaluator after switch into plan")
	}

	got2 := m.cycleAgent()
	if got2 != "build" {
		t.Errorf("second cycle = %q, want build", got2)
	}
	if m.previousAgent != "plan" {
		t.Errorf("previousAgent after second cycle = %q, want plan", m.previousAgent)
	}
	// Leaving plan should clear the agent rules.
	if rules := m.evaluator.AgentRules(); len(rules) != 0 {
		t.Errorf("expected agent rules cleared on leave, got %d", len(rules))
	}
}

func TestCycleAgent_BuildSwitchQueuedOnPlanToBuildWhenPlanFileExists(t *testing.T) {
	m := newTestAppModel(t)
	// Anchor cwd to a tempdir and a known sessionID so we can drop a plan
	// file where BuildSwitchReminderBody points.
	m.cwd = t.TempDir()
	m.sessionID = "abc123"

	notifier := m.agent.Notifier()
	if notifier == nil {
		t.Fatal("test agent missing notifier")
	}

	// Cycle into plan mode — should not enqueue anything.
	if got := m.cycleAgent(); got != "plan" {
		t.Fatalf("expected plan, got %q", got)
	}
	if got := notifier.ConsumeOneShot(); len(got) != 0 {
		t.Fatalf("entering plan must not queue a one-shot; got %v", got)
	}

	// Pretend the plan agent wrote a plan file.
	planFile := agent.PlanFilePath(m.cwd, m.sessionID)
	if err := writePlanFile(t, planFile); err != nil {
		t.Fatalf("seed plan file: %v", err)
	}

	// Cycle back to build — must queue exactly one reminder body that
	// references the plan file.
	if got := m.cycleAgent(); got != "build" {
		t.Fatalf("expected build, got %q", got)
	}
	queued := notifier.ConsumeOneShot()
	if len(queued) != 1 {
		t.Fatalf("expected 1 queued reminder, got %d (%v)", len(queued), queued)
	}
	if !strings.Contains(queued[0], planFile) {
		t.Errorf("queued reminder missing plan file path: %q", queued[0])
	}
}

func TestCycleAgent_BuildSwitchSilentWhenNoPlanFile(t *testing.T) {
	m := newTestAppModel(t)
	m.cwd = t.TempDir()
	m.sessionID = "no-plan-here"

	notifier := m.agent.Notifier()
	if notifier == nil {
		t.Fatal("test agent missing notifier")
	}

	_ = m.cycleAgent() // → plan
	_ = m.cycleAgent() // → build (without plan file existing)

	if got := notifier.ConsumeOneShot(); len(got) != 0 {
		t.Errorf("expected no queued reminder when plan file absent, got %v", got)
	}
}

func TestStatusBar_RendersAgentName(t *testing.T) {
	m := StatusModel{
		modelName: "gpt-x",
		agent:     "plan",
		width:     120,
	}
	out := m.View()
	if !strings.Contains(out, "plan") {
		t.Errorf("expected agent in status bar, got: %q", out)
	}
	if !strings.Contains(out, "gpt-x") {
		t.Errorf("expected model in status bar, got: %q", out)
	}
}

func TestApplyTodoUpdate_PopulatesList(t *testing.T) {
	m := newTestAppModel(t)
	args := `{"todos":[
		{"id":"1","content":"a","status":"pending","active_form":"Doing a"},
		{"id":"2","content":"b","status":"pending","active_form":"Doing b"},
		{"id":"3","content":"c","status":"in_progress","active_form":"Doing c"},
		{"id":"4","content":"d","status":"completed","active_form":"Doing d"}
	]}`
	m.applyTodoUpdate(args)
	if len(m.todos) != 4 {
		t.Fatalf("len(m.todos) = %d, want 4", len(m.todos))
	}
	if m.todos[2].Status != "in_progress" || m.todos[2].ActiveForm != "Doing c" {
		t.Errorf("third todo = %+v, want in_progress 'Doing c'", m.todos[2])
	}
}

func TestApplyTodoUpdate_EmptyArgsLeavesPriorState(t *testing.T) {
	m := newTestAppModel(t)
	prior := []agent.Todo{{ID: "1", Content: "x", Status: "pending"}}
	m.todos = prior
	m.applyTodoUpdate("")
	if len(m.todos) != 1 || m.todos[0].ID != "1" {
		t.Errorf("empty args should not touch list; got %+v", m.todos)
	}
}

func TestApplyTodoUpdate_BadJSONLeavesPriorState(t *testing.T) {
	m := newTestAppModel(t)
	prior := []agent.Todo{{ID: "1", Content: "x", Status: "pending"}}
	m.todos = prior
	m.applyTodoUpdate("{not json")
	if len(m.todos) != 1 || m.todos[0].ID != "1" {
		t.Errorf("malformed args clobbered list; got %+v", m.todos)
	}
}

func TestSummarizeToolCall_TaskTool(t *testing.T) {
	got := summarizeToolCall("task", `{"subagent_type":"explore","description":"investigate auth flow"}`)
	if got != `Explore("investigate auth flow")` {
		t.Errorf("task summary = %q, want %q", got, `Explore("investigate auth flow")`)
	}
}

func TestSummarizeToolCall_TaskToolMissingDescription(t *testing.T) {
	got := summarizeToolCall("task", `{"subagent_type":"explore"}`)
	if got != "Explore" {
		t.Errorf("task summary = %q, want %q", got, "Explore")
	}
}

// writePlanFile creates the plan file (and parent dirs) so cycleAgent can
// see it via os.Stat.
func writePlanFile(t *testing.T, path string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("# plan\n"), 0o644)
}
