package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/compact"
	"github.com/marcomoesman/prompto/internal/permission"
	"github.com/marcomoesman/prompto/internal/store"
)

// planEnv is a focused stub for the /plan subcommand tests. It captures
// the bits the commands actually touch (agent name, switches, cwd,
// session id, notifier reminders) and is intentionally separate from
// model_test.go's stubEnv so the two test files don't fight over field
// shapes.
type planEnv struct {
	cwd        string
	sessionID  string
	agentName  string
	switchedTo string
	switchErr  error
	reminders  []string
	systemMsgs []string
	mu         sync.Mutex
}

func (e *planEnv) AppendSystemMessage(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.systemMsgs = append(e.systemMsgs, msg)
}
func (e *planEnv) EndCurrentSession(context.Context) error    { return nil }
func (e *planEnv) StartNewSession(context.Context) error      { return nil }
func (e *planEnv) AdoptSession(context.Context, string) error { return nil }
func (e *planEnv) SessionID() string                          { return e.sessionID }
func (e *planEnv) AgentName() string                          { return e.agentName }
func (e *planEnv) Model() string                              { return "" }
func (e *planEnv) Cwd() string                                { return e.cwd }
func (e *planEnv) Version() string                            { return "" }
func (e *planEnv) Conversation() *agent.Conversation          { return nil }
func (e *planEnv) SystemPromptText() string                   { return "" }
func (e *planEnv) AGENTSMdText() string                       { return "" }
func (e *planEnv) Store() *store.Store                        { return nil }
func (e *planEnv) Compactor() *compact.Compactor              { return nil }
func (e *planEnv) Evaluator() *permission.Evaluator           { return nil }
func (e *planEnv) Agent() *agent.Agent                        { return nil }
func (e *planEnv) Registry() *agent.AgentRegistry             { return nil }
func (e *planEnv) Notifier() agent.RemindNotifier             { return e }
func (e *planEnv) ToolDefinitions() []api.ToolDefinition      { return nil }
func (e *planEnv) SwitchAgent(name string) error {
	if e.switchErr != nil {
		return e.switchErr
	}
	e.switchedTo = name
	e.agentName = name
	return nil
}
func (e *planEnv) SetModel(string) error { return nil }
func (e *planEnv) Models() []ModelInfo   { return nil }

// RemindNotifier implementation. PreTurn is unused by these tests.
func (e *planEnv) PreTurn(agent.PreTurnContext) []string { return nil }
func (e *planEnv) ConsumeOneShot() []string              { return nil }
func (e *planEnv) QueueOneShot(text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reminders = append(e.reminders, text)
}

// seedLegacyPlan writes a plan file at the legacy
// `<cwd>/.prompto/plans/<sessionID>.md` location and returns the
// absolute path. Tests use this to populate the path that
// resolvePlanPathFromEnv falls back to when no `sessions.plan_path`
// is recorded.
func seedLegacyPlan(t *testing.T, env *planEnv, body string) string {
	t.Helper()
	plansDir := filepath.Join(env.cwd, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	planPath := filepath.Join(plansDir, env.sessionID+".md")
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	return planPath
}

func TestPlanCommand_NoArgsSwitchesToPlan(t *testing.T) {
	env := &planEnv{agentName: "build"}
	if _, err := (PlanCommand{}).Exec(context.Background(), nil, env); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if env.switchedTo != "plan" {
		t.Errorf("switchedTo = %q, want \"plan\"", env.switchedTo)
	}
}

func TestPlanCommand_UnknownSubcommandErrors(t *testing.T) {
	env := &planEnv{agentName: "plan"}
	_, err := (PlanCommand{}).Exec(context.Background(), []string{"oops"}, env)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unknown subcommand error = %v, want contains 'unknown subcommand'", err)
	}
}

func TestPlanRevise_EmptyFeedbackErrors(t *testing.T) {
	env := &planEnv{agentName: "plan"}
	_, err := runPlanRevise(t.Context(), nil, env)
	if err == nil {
		t.Fatal("empty feedback = nil err, want error")
	}
	if !strings.Contains(err.Error(), "feedback") {
		t.Errorf("error should mention feedback: %v", err)
	}
}

func TestPlanRevise_SwitchesToPlanWhenNeeded(t *testing.T) {
	env := &planEnv{
		agentName: "build",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	seedLegacyPlan(t, env, "## Context\nbody\n")

	res, err := runPlanRevise(t.Context(), []string{"drop", "the", "bash", "tool"}, env)
	if err != nil {
		t.Fatalf("runPlanRevise: %v", err)
	}
	if env.switchedTo != "plan" {
		t.Errorf("did not switch to plan; switchedTo = %q", env.switchedTo)
	}
	if res.Prompt != "drop the bash tool" {
		t.Errorf("Prompt = %q, want feedback text", res.Prompt)
	}
	if len(env.reminders) != 1 {
		t.Fatalf("reminders queued = %d, want 1", len(env.reminders))
	}
	if !strings.Contains(env.reminders[0], "drop the bash tool") {
		t.Errorf("reminder body should include feedback text: %q", env.reminders[0])
	}
	// QueueOneShot now takes raw bodies; InjectReminders wraps at injection
	// time. Wrapping here would double-wrap and waste tokens.
	if strings.Contains(env.reminders[0], "<system-reminder>") {
		t.Errorf("reminder body should not be pre-wrapped: %q", env.reminders[0])
	}
}

func TestPlanRevise_AlreadyOnPlanDoesNotSwitch(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	seedLegacyPlan(t, env, "body")

	if _, err := runPlanRevise(t.Context(), []string{"feedback"}, env); err != nil {
		t.Fatalf("runPlanRevise: %v", err)
	}
	if env.switchedTo != "" {
		t.Errorf("should not switch when already on plan; got %q", env.switchedTo)
	}
}

func TestPlanDiff_NoPlanFile(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	res, err := runPlanDiff(t.Context(), env)
	if err != nil {
		t.Fatalf("runPlanDiff: %v", err)
	}
	if !strings.Contains(res.Message, "no plan file yet") {
		t.Errorf("Message = %q, want friendly no-plan message", res.Message)
	}
}

func TestPlanDiff_NoBackupYet(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	seedLegacyPlan(t, env, "## Context\nbody\n")
	res, err := runPlanDiff(t.Context(), env)
	if err != nil {
		t.Fatalf("runPlanDiff: %v", err)
	}
	if !strings.Contains(res.Message, "no prior version") {
		t.Errorf("Message = %q, want 'no prior version'", res.Message)
	}
}

func TestPlanDiff_RendersUnifiedDiff(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	planPath := seedLegacyPlan(t, env, "old line\nshared\n")
	// Snapshot the current ("old") body, then overwrite with new
	// content. agent.BackupPlan handles the timestamping so we don't
	// have to fight clock resolution in tests.
	if err := agent.BackupPlan(planPath); err != nil {
		t.Fatalf("BackupPlan: %v", err)
	}
	if err := os.WriteFile(planPath, []byte("new line\nshared\n"), 0o644); err != nil {
		t.Fatalf("rewrite plan: %v", err)
	}

	res, err := runPlanDiff(t.Context(), env)
	if err != nil {
		t.Fatalf("runPlanDiff: %v", err)
	}
	if !strings.HasPrefix(res.Message, "```diff\n") || !strings.HasSuffix(res.Message, "```") {
		t.Errorf("expected fenced ```diff block, got: %q", res.Message)
	}
	if !strings.Contains(res.Message, "-old line") {
		t.Errorf("missing `-old line` in diff: %q", res.Message)
	}
	if !strings.Contains(res.Message, "+new line") {
		t.Errorf("missing `+new line` in diff: %q", res.Message)
	}
}

func TestPlanDiff_UnchangedSinceBackup(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	planPath := seedLegacyPlan(t, env, "## Context\nstable\n")
	if err := agent.BackupPlan(planPath); err != nil {
		t.Fatalf("BackupPlan: %v", err)
	}
	// Don't modify the plan body; current and backup are byte-identical.
	res, err := runPlanDiff(t.Context(), env)
	if err != nil {
		t.Fatalf("runPlanDiff: %v", err)
	}
	if !strings.Contains(res.Message, "unchanged") {
		t.Errorf("Message = %q, want 'unchanged' note", res.Message)
	}
}

func TestPlanApprove_NoPlanFile(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	_, err := runPlanApprove(t.Context(), env)
	if err == nil {
		t.Fatal("expected error when no plan file exists")
	}
}

func TestPlanApprove_InvalidPlanRejected(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	// Missing every required section — validator should reject.
	seedLegacyPlan(t, env, "# title only, no required `##` headings\n")
	_, err := runPlanApprove(t.Context(), env)
	if err == nil {
		t.Fatal("expected error when plan is missing required sections")
	}
	if !strings.Contains(err.Error(), "plan not ready") {
		t.Errorf("error should be wrapped with 'plan not ready': %v", err)
	}
}

func TestPlanApprove_ValidPlanOpensOverlay(t *testing.T) {
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	body := "## Context\nbecause\n## Goal & acceptance criteria\nx\n## Files\ny\n## Verification\nz\n## Risks / out-of-scope\nw\n"
	seedLegacyPlan(t, env, body)
	res, err := runPlanApprove(t.Context(), env)
	if err != nil {
		t.Fatalf("runPlanApprove: %v", err)
	}
	if !res.OpenPlanApproval {
		t.Errorf("OpenPlanApproval = false, want true")
	}
}

func TestPlanCommand_DispatchesSubcommands(t *testing.T) {
	// Sanity check: PlanCommand.Exec routes through the helpers.
	env := &planEnv{
		agentName: "plan",
		cwd:       t.TempDir(),
		sessionID: "abc123",
	}
	seedLegacyPlan(t, env, "## Context\nbecause\n## Goal & acceptance criteria\nx\n## Files\ny\n## Verification\nz\n## Risks / out-of-scope\nw\n")

	res, err := (PlanCommand{}).Exec(context.Background(), []string{"approve"}, env)
	if err != nil {
		t.Fatalf("Exec(approve): %v", err)
	}
	if !res.OpenPlanApproval {
		t.Error("dispatch to approve did not flag OpenPlanApproval")
	}

	res, err = (PlanCommand{}).Exec(context.Background(), []string{"diff"}, env)
	if err != nil {
		t.Fatalf("Exec(diff): %v", err)
	}
	if res.Message == "" {
		t.Error("dispatch to diff produced empty message")
	}

	if _, err := (PlanCommand{}).Exec(context.Background(), []string{"revise", "drop sqlite"}, env); err != nil {
		t.Fatalf("Exec(revise): %v", err)
	}
}
