package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcomoesman/prompto/internal/api"
)

// jsonArgs encodes m as a JSON object string suitable for embedding in
// fake tool-call inputs. Tests previously concatenated paths into raw
// JSON via `{"path":"` + path + `"}`, which works on POSIX (no
// backslashes in paths) but breaks on Windows where `C:\Users\...`
// contains `\T`, `\U`, `\.` — all invalid JSON escapes that make
// Unmarshal error out and the fake tool silently no-op. Routing
// through json.Marshal escapes correctly on every platform.
func jsonArgs(m map[string]string) string {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- fakes ---------------------------------------------------------------

// fakeProvider yields a canned response per Complete call. On the N+1-th call
// it replays the last response, so loops that keep asking get deterministic
// behavior.
type fakeProvider struct {
	responses [][]api.StreamEvent
	calls     int
	params    []api.CompleteParams
}

func (p *fakeProvider) ContextLimit(string) int { return 0 }

func (p *fakeProvider) Complete(_ context.Context, params api.CompleteParams) iter.Seq[api.StreamEvent] {
	idx := p.calls
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	p.calls++
	p.params = append(p.params, params)
	events := p.responses[idx]
	return func(yield func(api.StreamEvent) bool) {
		for _, e := range events {
			if !yield(e) {
				return
			}
		}
	}
}

// panickingProvider's Complete iterator panics on first yield.
// Models a provider implementation bug — the runLoop must recover,
// emit an error event, surface it on Done, and close both channels
// so the TUI doesn't hang on an unclosed receiver.
type panickingProvider struct{ msg string }

func (p *panickingProvider) ContextLimit(string) int { return 0 }

func (p *panickingProvider) Complete(_ context.Context, _ api.CompleteParams) iter.Seq[api.StreamEvent] {
	return func(_ func(api.StreamEvent) bool) {
		panic(p.msg)
	}
}

// waitingProvider blocks until ctx is cancelled.
type waitingProvider struct{}

func (p *waitingProvider) ContextLimit(string) int { return 0 }

func (p *waitingProvider) Complete(ctx context.Context, _ api.CompleteParams) iter.Seq[api.StreamEvent] {
	return func(yield func(api.StreamEvent) bool) {
		<-ctx.Done()
	}
}

// fakeResolver implements ToolResolver with a map of name→Tool.
type fakeResolver struct {
	tools map[string]Tool
}

func newFakeResolver(tools ...Tool) *fakeResolver {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &fakeResolver{tools: m}
}

func (r *fakeResolver) Resolve(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *fakeResolver) Definitions() []api.ToolDefinition {
	var defs []api.ToolDefinition
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

// echoTool returns a canned string for any input.
type echoTool struct {
	name   string
	result string
	err    error
}

func (t *echoTool) Name() string                   { return t.name }
func (t *echoTool) Definition() api.ToolDefinition { return api.ToolDefinition{Name: t.name} }
func (t *echoTool) FormatForDisplay(_ []byte) string {
	return t.name + "()"
}
func (t *echoTool) MaxResultBytes() int           { return 0 }
func (t *echoTool) IsReadOnly() bool              { return true }
func (t *echoTool) IsConcurrencySafe() bool       { return true }
func (t *echoTool) PermissionKey(_ []byte) string { return t.name }
func (t *echoTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	if t.err != nil {
		return Result{}, t.err
	}
	return Result{Content: t.result, Bytes: len(t.result)}, nil
}

// fileReadTool is a minimal Read-equivalent that records in FileState.
type fileReadTool struct{}

func (t *fileReadTool) Name() string                     { return "read" }
func (t *fileReadTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "read"} }
func (t *fileReadTool) FormatForDisplay(_ []byte) string { return "read()" }
func (t *fileReadTool) MaxResultBytes() int              { return 0 }
func (t *fileReadTool) IsReadOnly() bool                 { return true }
func (t *fileReadTool) IsConcurrencySafe() bool          { return true }
func (t *fileReadTool) PermissionKey(_ []byte) string    { return "" }
func (t *fileReadTool) Execute(_ context.Context, tc ToolContext, input []byte) (Result, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{}, err
	}
	info, err := os.Stat(p.Path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return Result{}, err
	}
	tc.FileState.Put(p.Path, info.ModTime(), data)
	return Result{Content: string(data), Bytes: len(data)}, nil
}

// planExitStubTool mimics the plan_exit tool for run-loop tests
// without importing internal/tool (which would create a cycle).
// run.go gates on the literal name "plan_exit" + the
// agent's PlanMode flag, so this stub is sufficient.
type planExitStubTool struct{}

func (t *planExitStubTool) Name() string { return "plan_exit" }
func (t *planExitStubTool) Definition() api.ToolDefinition {
	return api.ToolDefinition{Name: "plan_exit"}
}
func (t *planExitStubTool) FormatForDisplay(_ []byte) string { return "PlanExit()" }
func (t *planExitStubTool) MaxResultBytes() int              { return 0 }
func (t *planExitStubTool) IsReadOnly() bool                 { return true }
func (t *planExitStubTool) IsConcurrencySafe() bool          { return false }
func (t *planExitStubTool) PermissionKey(_ []byte) string    { return "" }
func (t *planExitStubTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	return Result{Content: "ok", Bytes: 2}, nil
}

// writeStubTool is a minimal "write" placeholder for tests that
// drive the run loop's first-plan-write hook without touching the
// filesystem. PermissionKey extracts the path so the existing
// rule-evaluation path stays exercised; Execute is a no-op success.
type writeStubTool struct{}

func (t *writeStubTool) Name() string                     { return "write" }
func (t *writeStubTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "write"} }
func (t *writeStubTool) FormatForDisplay(_ []byte) string { return "write()" }
func (t *writeStubTool) MaxResultBytes() int              { return 0 }
func (t *writeStubTool) IsReadOnly() bool                 { return false }
func (t *writeStubTool) IsConcurrencySafe() bool          { return false }
func (t *writeStubTool) PermissionKey(input []byte) string {
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &p)
	return p.Path
}
func (t *writeStubTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	return Result{Content: "ok", Bytes: 2}, nil
}

// realWriteStubTool is the e2e companion to writeStubTool: instead
// of returning a no-op success, it actually writes content to disk.
// The end-to-end plan-mode test needs the plan file to exist
// after Turn 1 so the Turn-2 plan_exit pre-flight has something to
// validate. PermissionKey, IsReadOnly, IsConcurrencySafe match the
// real `write` tool's contract.
type realWriteStubTool struct{}

func (t *realWriteStubTool) Name() string                     { return "write" }
func (t *realWriteStubTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "write"} }
func (t *realWriteStubTool) FormatForDisplay(_ []byte) string { return "write()" }
func (t *realWriteStubTool) MaxResultBytes() int              { return 0 }
func (t *realWriteStubTool) IsReadOnly() bool                 { return false }
func (t *realWriteStubTool) IsConcurrencySafe() bool          { return false }
func (t *realWriteStubTool) PermissionKey(input []byte) string {
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &p)
	return p.Path
}
func (t *realWriteStubTool) Execute(_ context.Context, _ ToolContext, input []byte) (Result, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(p.Path, []byte(p.Content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Content: "ok", Bytes: 2}, nil
}

// fileEditTool enforces Check before writing.
type fileEditTool struct{}

func (t *fileEditTool) Name() string                     { return "edit" }
func (t *fileEditTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "edit"} }
func (t *fileEditTool) FormatForDisplay(_ []byte) string { return "edit()" }
func (t *fileEditTool) MaxResultBytes() int              { return 0 }
func (t *fileEditTool) IsReadOnly() bool                 { return false }
func (t *fileEditTool) IsConcurrencySafe() bool          { return false }
func (t *fileEditTool) PermissionKey(_ []byte) string    { return "" }
func (t *fileEditTool) Execute(_ context.Context, tc ToolContext, input []byte) (Result, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{}, err
	}
	if err := tc.FileState.Check(p.Path); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(p.Path, []byte(p.Content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Content: "ok", Bytes: 2}, nil
}

// --- helpers -------------------------------------------------------------

func allowAll(context.Context, string, string, []byte) (Decision, error) {
	return DecisionAllow, nil
}

func denyAll(context.Context, string, string, []byte) (Decision, error) {
	return DecisionDeny, nil
}

func drain(t *testing.T, rr RunResult) error {
	t.Helper()
	for range rr.Events {
	}
	return <-rr.Done
}

// toolUseResponse builds a canned stream that yields one tool call.
func toolUseResponse(id, name, argsJSON string) []api.StreamEvent {
	return []api.StreamEvent{
		{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: id, ToolCallName: name},
		{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: argsJSON},
		{Type: api.EventDone, StopReason: "tool_use"},
	}
}

// textResponse builds a canned stream that yields plain text.
func textResponse(text string) []api.StreamEvent {
	return []api.StreamEvent{
		{Type: api.EventDelta, Delta: text},
		{Type: api.EventDone, StopReason: "end_turn"},
	}
}

// emptyResponse — see internal/agent/run_recovery_test.go for the
// canonical helper. Tests in this file rely on it.

// --- tests ---------------------------------------------------------------

func TestRun_EndTurn(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("Hello world")}}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v, want ErrEndTurn", reason)
	}
	if len(conv.Messages()) != 2 {
		t.Fatalf("messages = %d, want 2 (user + assistant)", len(conv.Messages()))
	}
	if conv.Messages()[1].Text() != "Hello world" {
		t.Errorf("assistant text = %q", conv.Messages()[1].Text())
	}
}

// TestRun_EmptyTurnNoNotifier_DoesNotPersistEmptyAssistant covers
// the prefill-incompatibility regression. When a thinking model
// emits a turn with no visible text and no structured tool_calls,
// the run loop must NOT append the empty assistant message to the
// conversation — leaving it there would cause the next provider
// call (or any downstream consumer that re-encodes the
// conversation) to send messages ending with `assistant`, which
// llama.cpp's Qwen3-thinking template rejects as "Assistant
// response prefill is incompatible with enable_thinking."
//
// With no notifier configured (subagent / headless) the loop
// should exit cleanly via ErrEndTurn, and the conversation should
// be left exactly as it was before the call (just the seeded user
// message).
func TestRun_EmptyTurnNoNotifier_DoesNotPersistEmptyAssistant(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{emptyResponse()}}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v, want ErrEndTurn", reason)
	}
	if len(conv.Messages()) != 1 {
		t.Fatalf("messages = %d, want 1 (empty assistant must not be appended); got %v",
			len(conv.Messages()), conv.Messages())
	}
	if conv.Messages()[0].Role != api.RoleUser {
		t.Errorf("messages[0].Role = %q, want user", conv.Messages()[0].Role)
	}
}

// TestRun_EmptyTurnWithNotifier_RetriesAndExitsClean covers the
// nudge-and-retry path. With a notifier wired, the empty turn
// triggers a one-shot reminder and a re-call. The retry resolves
// (the second canned response is a normal text turn) and the run
// exits via ErrEndTurn. Crucially, only the SECOND turn's content
// reaches the conversation — the first (empty) one is suppressed.
func TestRun_EmptyTurnWithNotifier_RetriesAndExitsClean(t *testing.T) {
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			emptyResponse(),
			textResponse("recovered"),
		},
	}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(),
		Notifier: NewNotifier(),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v, want ErrEndTurn", reason)
	}
	if prov.calls != 2 {
		t.Errorf("provider calls = %d, want 2 (empty turn → nudge → retry)", prov.calls)
	}
	if len(conv.Messages()) != 2 {
		t.Fatalf("messages = %d, want 2 (user + recovered assistant); got %v",
			len(conv.Messages()), conv.Messages())
	}
	if conv.Messages()[1].Role != api.RoleAssistant {
		t.Errorf("messages[1].Role = %q, want assistant", conv.Messages()[1].Role)
	}
	if got := conv.Messages()[1].Text(); got != "recovered" {
		t.Errorf("messages[1].Text() = %q, want %q", got, "recovered")
	}
}

// TestRun_PlanMode_FirstWritePersistsPlanPath covers the Phase-20
// emission-phase hook. When the plan agent successfully writes a
// path under `<cwd>/.prompto/plans/`, the run loop persists the
// path on the session row so resume + the build-switch reminder +
// the per-turn plan-mode reminder all reference the right file.
//
// Subsequent writes to the same file must NOT overwrite the
// recorded path — the captured value is the canonical "first
// plan write," not "most recent plan write."
func TestRun_PlanMode_FirstWritePersistsPlanPath(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	planPath := filepath.Join(tmp, ".prompto", "plans", "2026-04-30-undo.md")
	argsJSON := fmt.Sprintf(`{"path":%q,"content":"# Plan"}`, planPath)

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "write"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: argsJSON},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		textResponse("plan written"),
	}}

	fs := &fakeStore{}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&writeStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("plan it"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "plan", // built-in plan agent has PlanMode=true
		Store:        fs,
		SessionID:    "sess-1",
	})
	_ = drain(t, rr)

	got := fs.planPaths["sess-1"]
	if got != planPath {
		t.Errorf("planPaths[sess-1] = %q, want %q", got, planPath)
	}
}

// TestRun_PlanMode_NonPlanWriteDoesNotPersist asserts the hook is
// gated on path: writes that target paths OUTSIDE
// `.prompto/plans/` (denied by permission rules in real runs but
// exercised here under allowAll) don't accidentally clobber
// plan_path with arbitrary file paths.
func TestRun_PlanMode_NonPlanWriteDoesNotPersist(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "write"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{"path":"main.go","content":"// not a plan"}`},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		textResponse("done"),
	}}

	fs := &fakeStore{}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&writeStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("write something"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "plan",
		Store:        fs,
		SessionID:    "sess-1",
	})
	_ = drain(t, rr)

	if got := fs.planPaths["sess-1"]; got != "" {
		t.Errorf("non-plan-path write recorded plan_path = %q, want empty", got)
	}
}

// TestRun_PlanMode_NonPlanAgentSkipsHook asserts the hook is gated
// on agentDef.PlanMode: a build-mode agent writing to a path that
// happens to live under .prompto/plans/ does NOT persist plan_path.
// Realistic scenario: a build agent revising a previously approved
// plan should not be silently re-recording it as the canonical
// path for this session.
func TestRun_PlanMode_NonPlanAgentSkipsHook(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	planPath := filepath.Join(tmp, ".prompto", "plans", "2026-04-30-undo.md")
	argsJSON := fmt.Sprintf(`{"path":%q,"content":"# Plan"}`, planPath)

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "write"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: argsJSON},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		textResponse("done"),
	}}

	fs := &fakeStore{}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&writeStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("revise"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "build", // NOT plan — hook should not fire
		Store:        fs,
		SessionID:    "sess-1",
	})
	_ = drain(t, rr)

	if got := fs.planPaths["sess-1"]; got != "" {
		t.Errorf("build agent write recorded plan_path = %q, want empty", got)
	}
}

// TestRun_PlanExit_ValidationFailureSurfacesAsToolError covers
// the pre-flight validator. The model calls plan_exit
// without writing a plan file → preflight fails → plan.denied is
// set → the existing tool-error path emits a Done event with
// ToolError=true and the run continues (no ErrUserDenied). The
// model can then write a plan and retry plan_exit on the next
// turn.
func TestRun_PlanExit_ValidationFailureSurfacesAsToolError(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		// Turn 1: plan_exit without any prior plan file.
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "plan_exit"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		// Turn 2: model gives up and emits text. Without this the
		// fakeProvider would replay the tool_use forever and hit
		// MaxSteps.
		textResponse("ok, I'll write the plan first"),
	}}

	fs := &fakeStore{}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&planExitStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("plan it"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "plan",
		Store:        fs,
		SessionID:    "sess-1",
	})

	var (
		sawDoneError bool
		sawApproved  bool
	)
	for ev := range rr.Events {
		switch ev.Type {
		case EventToolCallDone:
			if ev.ToolName == "plan_exit" && ev.ToolError {
				sawDoneError = true
				if !strings.Contains(ev.ToolResult, "no plan file") {
					t.Errorf("ToolResult should mention missing plan file; got %q", ev.ToolResult)
				}
			}
		case EventPlanApproved:
			sawApproved = true
		}
	}
	if err := <-rr.Done; !errors.Is(err, ErrEndTurn) {
		t.Errorf("Done = %v, want ErrEndTurn (run should continue past validation failure)", err)
	}
	if !sawDoneError {
		t.Error("expected a tool-error EventToolCallDone for plan_exit")
	}
	if sawApproved {
		t.Error("EventPlanApproved must NOT fire when validation failed")
	}
}

// TestRun_PlanExit_InvalidSchemaSurfacesAsToolError covers the
// other half of pre-flight: the plan file exists but is missing
// required `##` sections. The validator's MissingSectionsError
// reaches the model so it knows what to add.
func TestRun_PlanExit_InvalidSchemaSurfacesAsToolError(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	planPath := filepath.Join(tmp, ".prompto", "plans", "2026-04-30-bad.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Plan body with only one of five required sections.
	if err := os.WriteFile(planPath, []byte("# Plan\n\n## Context\nwhy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "plan_exit"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		textResponse("acknowledged, will revise"),
	}}

	// Pre-record the plan path so preflight finds it.
	fs := &fakeStore{planPaths: map[string]string{"sess-1": planPath}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&planExitStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("exit"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "plan",
		Store:        fs,
		SessionID:    "sess-1",
	})

	var sawSchemaError bool
	for ev := range rr.Events {
		if ev.Type == EventToolCallDone && ev.ToolName == "plan_exit" && ev.ToolError {
			sawSchemaError = true
			// MissingSectionsError lists the missing names.
			if !strings.Contains(ev.ToolResult, "Goal & acceptance criteria") {
				t.Errorf("expected missing-section names in error; got %q", ev.ToolResult)
			}
		}
	}
	<-rr.Done
	if !sawSchemaError {
		t.Error("expected schema-validation tool error")
	}
}

// TestRun_PlanExit_ApprovedEmitsEvent covers the happy path: a
// valid plan file exists, the model calls plan_exit, allowAll
// accepts the approval, post-execute hook fires
// EventPlanApproved with the plan path. The run terminates with
// ErrEndTurn so the TUI can flip to build cleanly.
func TestRun_PlanExit_ApprovedEmitsEvent(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	planPath := filepath.Join(tmp, ".prompto", "plans", "2026-04-30-good.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Plan\n\n## Context\na\n\n## Goal & acceptance criteria\nb\n\n## Files\nc\n\n## Verification\nd\n\n## Risks / out-of-scope\ne\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "plan_exit"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
	}}

	fs := &fakeStore{planPaths: map[string]string{"sess-1": planPath}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&planExitStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("exit"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "plan",
		Store:        fs,
		SessionID:    "sess-1",
	})

	var (
		approvedPath string
		sawError     bool
	)
	for ev := range rr.Events {
		switch ev.Type {
		case EventPlanApproved:
			approvedPath = ev.ToolDisp
		case EventToolCallDone:
			if ev.ToolError {
				sawError = true
			}
		}
	}
	if err := <-rr.Done; !errors.Is(err, ErrEndTurn) {
		t.Errorf("Done = %v, want ErrEndTurn (post-plan_exit terminal)", err)
	}
	if sawError {
		t.Error("plan_exit on a valid plan should not produce a tool error")
	}
	if approvedPath != planPath {
		t.Errorf("EventPlanApproved.ToolDisp = %q, want %q", approvedPath, planPath)
	}
}

// TestRun_PlanExit_BuildAgentDoesNotPreflight asserts the gate is
// gated on agentDef.PlanMode: a build agent calling plan_exit
// (which shouldn't happen in practice but might during tests or
// future custom agents) doesn't get the validator's "no plan
// file" treatment.
func TestRun_PlanExit_BuildAgentDoesNotPreflight(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "plan_exit"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		textResponse("done"),
	}}

	fs := &fakeStore{}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&planExitStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "build",
		Store:        fs,
		SessionID:    "sess-1",
	})

	var sawApproved bool
	for ev := range rr.Events {
		if ev.Type == EventPlanApproved {
			sawApproved = true
		}
	}
	<-rr.Done
	if sawApproved {
		t.Error("non-plan agents must not emit EventPlanApproved (post-execute hook is plan-mode-only)")
	}
}

// TestRun_PlanMode_FirstWriteIsIdempotent asserts the hook only
// records the first plan-file write. A second `write` to a
// different plan filename in the same session must not overwrite
// the original recording.
func TestRun_PlanMode_FirstWriteIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	first := filepath.Join(tmp, ".prompto", "plans", "2026-04-30-foo.md")
	second := filepath.Join(tmp, ".prompto", "plans", "2026-05-01-bar.md")

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "write"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: fmt.Sprintf(`{"path":%q,"content":"a"}`, first)},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_2", ToolCallName: "write"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: fmt.Sprintf(`{"path":%q,"content":"b"}`, second)},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		textResponse("done"),
	}}

	fs := &fakeStore{}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&writeStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("plan"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "plan",
		Store:        fs,
		SessionID:    "sess-1",
	})
	_ = drain(t, rr)

	if got := fs.planPaths["sess-1"]; got != first {
		t.Errorf("planPaths[sess-1] = %q, want %q (first write must win)", got, first)
	}
}

func TestRun_MaxSteps(t *testing.T) {
	// Provider always wants a tool call; maxSteps hit.
	prov := &fakeProvider{responses: [][]api.StreamEvent{toolUseResponse("t1", "echo", `{}`)}}
	tools := newFakeResolver(&echoTool{name: "echo", result: "x"})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("loop"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 3, CanUseTool: allowAll})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrMaxSteps) {
		t.Fatalf("reason = %v, want ErrMaxSteps", reason)
	}
	if prov.calls != 3 {
		t.Errorf("provider calls = %d, want 3", prov.calls)
	}
}

func TestRun_UserDenies(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{toolUseResponse("t1", "echo", `{}`)}}
	executed := false
	tool := &echoTool{name: "echo", result: "should-not-run"}
	resolver := newFakeResolver(tool)
	_ = executed

	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: resolver})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("please"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: denyAll})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrUserDenied) {
		t.Fatalf("reason = %v, want ErrUserDenied", reason)
	}
	if prov.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (should stop before retry)", prov.calls)
	}
}

// TestRun_ProviderPanicRecovers is the regression for the indefinite-
// hang bug. Without the runLoop's deferred recover, a panicking provider
// kills the goroutine before events/done are closed and the TUI's drain
// loop blocks forever. After the fix, the panic surfaces as an EventError
// + Done error, and both channels close so the consumer unblocks.
func TestRun_ProviderPanicRecovers(t *testing.T) {
	prov := &panickingProvider{msg: "kaboom"}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})

	// Drain on a timer so a regression manifests as a test timeout, not a hang.
	type result struct {
		err        error
		sawError   bool
		sawTurnEnd bool
	}
	res := make(chan result, 1)
	go func() {
		var r result
		for ev := range rr.Events {
			switch ev.Type {
			case EventError:
				r.sawError = true
			case EventTurnComplete:
				r.sawTurnEnd = true
			}
		}
		r.err = <-rr.Done
		res <- r
	}()

	select {
	case r := <-res:
		if !r.sawError {
			t.Error("expected EventError surfacing the panic")
		}
		if !r.sawTurnEnd {
			t.Error("expected EventTurnComplete after recover")
		}
		if r.err == nil || !strings.Contains(r.err.Error(), "kaboom") {
			t.Errorf("Done err = %v, want one mentioning the panic message", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent.Run did not close channels after provider panic — recover is missing or buggy")
	}
}

func TestRun_ProviderPanicReleasesGate(t *testing.T) {
	gate := NewProviderGate(1)
	prov := &panickingProvider{msg: "kaboom"}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver(), Gate: gate})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	_ = drain(t, rr)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := gate.Acquire(ctx); err != nil {
		t.Fatalf("gate was not released after provider panic: %v", err)
	}
	gate.Release()
}

func TestRun_ContextCancel(t *testing.T) {
	prov := &waitingProvider{}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	ctx, cancel := context.WithCancel(t.Context())
	rr := agnt.Run(ctx, RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})

	// Cancel shortly after starting the run.
	time.AfterFunc(20*time.Millisecond, cancel)

	doneCh := make(chan error, 1)
	go func() { doneCh <- drain(t, rr) }()

	select {
	case reason := <-doneCh:
		if !errors.Is(reason, context.Canceled) {
			t.Fatalf("reason = %v, want context.Canceled", reason)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not terminate after context cancel")
	}
}

func TestRun_ToolErrorContinues(t *testing.T) {
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			toolUseResponse("t1", "echo", `{}`),
			textResponse("recovered"),
		},
	}
	tools := newFakeResolver(&echoTool{name: "echo", err: errors.New("boom")})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v, want ErrEndTurn", reason)
	}
	if prov.calls != 2 {
		t.Errorf("provider calls = %d, want 2 (one with tool_result, one finish)", prov.calls)
	}
}

func TestRun_EditWithoutReadFailsAsToolError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			toolUseResponse("t1", "edit", jsonArgs(map[string]string{"path": path, "content": "changed"})),
			textResponse("done"),
		},
	}
	tools := newFakeResolver(&fileEditTool{})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("edit"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})

	var gotErrorToolResult bool
	for e := range rr.Events {
		if e.Type == EventToolCallDone && e.ToolError {
			gotErrorToolResult = true
		}
	}
	<-rr.Done

	if !gotErrorToolResult {
		t.Fatal("expected tool_result with is_error=true for edit without prior read")
	}
	// File must be unchanged.
	data, _ := os.ReadFile(path)
	if string(data) != "initial" {
		t.Errorf("file content = %q, want %q", string(data), "initial")
	}
}

func TestRun_EditAfterReadSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			toolUseResponse("r1", "read", jsonArgs(map[string]string{"path": path})),
			toolUseResponse("e1", "edit", jsonArgs(map[string]string{"path": path, "content": "changed"})),
			textResponse("done"),
		},
	}
	tools := newFakeResolver(&fileReadTool{}, &fileEditTool{})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("edit"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v, want ErrEndTurn", reason)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "changed" {
		t.Errorf("file content = %q, want %q", string(data), "changed")
	}
}

func TestRun_EditFailsWhenFileChangedExternally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	// After Read, provider swaps in an external modification, then proceeds
	// with Edit. We simulate the external change by using a special tool
	// that writes the file outside FileState's knowledge.
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			toolUseResponse("r1", "read", jsonArgs(map[string]string{"path": path})),
			toolUseResponse("x1", "extwrite", jsonArgs(map[string]string{"path": path, "content": "externally-changed"})),
			toolUseResponse("e1", "edit", jsonArgs(map[string]string{"path": path, "content": "changed"})),
			textResponse("done"),
		},
	}
	extwrite := &echoTool{name: "extwrite", result: "external"}
	// extwrite actually needs to modify the file behind FileState's back.
	// Wrap it with a custom Execute.
	extwriter := &extWriterTool{}
	tools := newFakeResolver(&fileReadTool{}, &fileEditTool{}, extwriter)
	_ = extwrite

	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("edit"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 6, CanUseTool: allowAll})

	var editWasError bool
	for e := range rr.Events {
		if e.Type == EventToolCallDone && e.ToolName == "edit" && e.ToolError {
			editWasError = true
		}
	}
	<-rr.Done

	if !editWasError {
		t.Fatal("expected edit tool to fail because file changed externally")
	}
}

type extWriterTool struct{}

func (t *extWriterTool) Name() string                     { return "extwrite" }
func (t *extWriterTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "extwrite"} }
func (t *extWriterTool) FormatForDisplay(_ []byte) string { return "extwrite()" }
func (t *extWriterTool) MaxResultBytes() int              { return 0 }
func (t *extWriterTool) IsReadOnly() bool                 { return false }
func (t *extWriterTool) IsConcurrencySafe() bool          { return false }
func (t *extWriterTool) PermissionKey(_ []byte) string    { return "" }
func (t *extWriterTool) Execute(_ context.Context, _ ToolContext, input []byte) (Result, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{}, err
	}
	// Sleep briefly so mtime definitely differs.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(p.Path, []byte(p.Content), 0o644); err != nil {
		return Result{}, err
	}
	// Bump mtime explicitly to force sha256 recheck path in Check.
	future := time.Now().Add(time.Second)
	_ = os.Chtimes(p.Path, future, future)
	return Result{Content: "external wrote", Bytes: 13}, nil
}

// largePayloadTool returns a fixed-size string of 'x' bytes; used for
// exercising the per-turn aggregator.
type largePayloadTool struct {
	name string
	size int
}

func (t *largePayloadTool) Name() string                     { return t.name }
func (t *largePayloadTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: t.name} }
func (t *largePayloadTool) FormatForDisplay(_ []byte) string { return t.name + "()" }

// MaxResultBytes is large so the per-tool cap doesn't bite; these tests
// specifically exercise the per-turn aggregate.
func (t *largePayloadTool) MaxResultBytes() int           { return 10 * 1024 * 1024 }
func (t *largePayloadTool) IsReadOnly() bool              { return true }
func (t *largePayloadTool) IsConcurrencySafe() bool       { return true }
func (t *largePayloadTool) PermissionKey(_ []byte) string { return t.name }
func (t *largePayloadTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	content := strings.Repeat("x", t.size)
	return Result{Content: content, Bytes: t.size}, nil
}

func TestRun_ContextLimitSurfacesSentinel(t *testing.T) {
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{{
			{Type: api.EventError, Error: errors.New("prompt is too long")},
		}},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 3, CanUseTool: allowAll})
	reason := drain(t, rr)
	if !errors.Is(reason, ErrContextLimit) {
		t.Fatalf("reason = %v, want ErrContextLimit", reason)
	}
}

// TestRun_ContextLimitDetectsViaSentinel verifies that providers which
// wrap their error with api.ErrContextLimit get exact, allocation-free
// detection via errors.Is — independent of the substring fallback.
// The wrapped error message is intentionally rephrased ("token budget
// exceeded") to prove the substring path is NOT what's matching.
func TestRun_ContextLimitDetectsViaSentinel(t *testing.T) {
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{{
			{Type: api.EventError, Error: fmt.Errorf("%w: token budget exceeded", api.ErrContextLimit)},
		}},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 3, CanUseTool: allowAll})
	reason := drain(t, rr)
	if !errors.Is(reason, ErrContextLimit) {
		t.Fatalf("reason = %v, want ErrContextLimit (sentinel path)", reason)
	}
}

func TestRun_PerTurnCapSuppressesLaterTools(t *testing.T) {
	// Two tool calls in one turn: first returns 190 KB, second returns 50 KB.
	// Per-turn cap is 200 KB → second must be suppressed.
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			// Both tool calls in the same assistant turn.
			{
				{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "t1", ToolCallName: "big"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
				{Type: api.EventToolCallStart, ToolCallIndex: 1, ToolCallID: "t2", ToolCallName: "medium"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 1, ToolCallArgs: `{}`},
				{Type: api.EventDone, StopReason: "tool_use"},
			},
			textResponse("done"),
		},
	}
	tools := newFakeResolver(
		&largePayloadTool{name: "big", size: 190 * 1024},
		&largePayloadTool{name: "medium", size: 50 * 1024},
	)
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	_ = drain(t, rr)

	// The tool_result message is the last user-role message appended to the
	// conversation (agent appends it before the second provider call). Walk
	// back until we find it.
	var toolResults []api.ContentBlock
	for _, msg := range conv.All() {
		if msg.Role == api.RoleTool {
			toolResults = append(toolResults, msg.Content...)
		}
	}
	if len(toolResults) < 2 {
		t.Fatalf("expected 2 tool_result blocks, got %d", len(toolResults))
	}
	// The second result must carry either the truncation marker or the
	// suppressed stub (depending on exact budget math).
	secondContent := ""
	if toolResults[1].ToolResult != nil {
		secondContent = toolResults[1].ToolResult.Content
	}
	if !strings.Contains(secondContent, "truncated") && !strings.Contains(secondContent, "suppressed") {
		t.Errorf("second tool_result not capped; got %q", secondContent[:min(80, len(secondContent))])
	}
}

func TestRun_TurnAggregatorResetsBetweenTurns(t *testing.T) {
	// Two back-to-back turns each hit a 150 KB tool. Both must succeed
	// because the aggregator resets per iteration.
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			{
				{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "t1", ToolCallName: "big"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
				{Type: api.EventDone, StopReason: "tool_use"},
			},
			{
				{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "t2", ToolCallName: "big"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
				{Type: api.EventDone, StopReason: "tool_use"},
			},
			textResponse("done"),
		},
	}
	tools := newFakeResolver(&largePayloadTool{name: "big", size: 150 * 1024})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	_ = drain(t, rr)

	// Each of the two turns produced one tool_result. Both must be >= 50 KB
	// (tool's own cap is 50 KB; default per-tool applies).
	var sizes []int
	for _, msg := range conv.All() {
		if msg.Role == api.RoleTool {
			for _, b := range msg.Content {
				if b.ToolResult != nil {
					sizes = append(sizes, len(b.ToolResult.Content))
				}
			}
		}
	}
	if len(sizes) != 2 {
		t.Fatalf("expected 2 tool_result blocks, got %d (%v)", len(sizes), sizes)
	}
	for i, s := range sizes {
		// DefaultMaxResultBytes is 50 KB; each result is capped at that.
		if s < DefaultMaxResultBytes-1024 {
			t.Errorf("tool_result[%d] size = %d, expected ~50 KB (aggregator reset should allow both)", i, s)
		}
	}
}

// slowConcurrentTool sleeps for Delay then returns a fixed string. Used to
// exercise parallel dispatch: if N instances run in series, the total time
// is N*Delay; parallel brings it down to ~Delay.
type slowConcurrentTool struct {
	name  string
	delay time.Duration
}

func (t *slowConcurrentTool) Name() string                     { return t.name }
func (t *slowConcurrentTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: t.name} }
func (t *slowConcurrentTool) FormatForDisplay(_ []byte) string { return t.name + "()" }
func (t *slowConcurrentTool) MaxResultBytes() int              { return 0 }
func (t *slowConcurrentTool) IsReadOnly() bool                 { return true }
func (t *slowConcurrentTool) IsConcurrencySafe() bool          { return true }
func (t *slowConcurrentTool) PermissionKey(_ []byte) string    { return "" }
func (t *slowConcurrentTool) Execute(ctx context.Context, _ ToolContext, _ []byte) (Result, error) {
	select {
	case <-time.After(t.delay):
		return Result{Content: "ok", Bytes: 2}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func TestRun_ParallelConcurrencySafeTools(t *testing.T) {
	// Three slow tool calls should finish in ~1 × delay, not 3 × delay.
	delay := 80 * time.Millisecond
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			{
				{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "a", ToolCallName: "slow"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
				{Type: api.EventToolCallStart, ToolCallIndex: 1, ToolCallID: "b", ToolCallName: "slow"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 1, ToolCallArgs: `{}`},
				{Type: api.EventToolCallStart, ToolCallIndex: 2, ToolCallID: "c", ToolCallName: "slow"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 2, ToolCallArgs: `{}`},
				{Type: api.EventDone, StopReason: "tool_use"},
			},
			textResponse("done"),
		},
	}
	tools := newFakeResolver(&slowConcurrentTool{name: "slow", delay: delay})
	eval := &fakeEvaluator{
		decide: func(_ EvaluateInput) EvaluateResult {
			return EvaluateResult{Decision: DecisionAllow}
		},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools, Evaluator: eval})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))

	start := time.Now()
	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
	})
	reason := drain(t, rr)
	elapsed := time.Since(start)

	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v", reason)
	}

	// Sequential would be ~3*delay = 240ms. Parallel should be well under
	// 2*delay. Leave headroom for CI noise.
	if elapsed >= 3*delay {
		t.Errorf("elapsed %v ≥ %v — tools did not run in parallel", elapsed, 3*delay)
	}

	// All three tool_results must be present in original order.
	toolIDs := []string{}
	for _, m := range conv.All() {
		if m.Role == api.RoleTool {
			for _, b := range m.Content {
				if b.ToolResult != nil {
					toolIDs = append(toolIDs, b.ToolResult.ToolCallID)
				}
			}
		}
	}
	want := []string{"a", "b", "c"}
	if !slicesEqual(toolIDs, want) {
		t.Errorf("tool IDs = %v, want %v (original order preserved)", toolIDs, want)
	}
}

func TestRun_ParallelStopsAtNonSafeTool(t *testing.T) {
	// Batch: [slow, slow, echo-nonsafe, slow]. First two parallel; echo
	// sequential; fourth starts a new (single-item) parallel batch. We
	// primarily verify correctness here — all four results appear in order.
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			{
				{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "a", ToolCallName: "slow"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
				{Type: api.EventToolCallStart, ToolCallIndex: 1, ToolCallID: "b", ToolCallName: "slow"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 1, ToolCallArgs: `{}`},
				{Type: api.EventToolCallStart, ToolCallIndex: 2, ToolCallID: "c", ToolCallName: "blocker"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 2, ToolCallArgs: `{}`},
				{Type: api.EventToolCallStart, ToolCallIndex: 3, ToolCallID: "d", ToolCallName: "slow"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 3, ToolCallArgs: `{}`},
				{Type: api.EventDone, StopReason: "tool_use"},
			},
			textResponse("done"),
		},
	}
	tools := newFakeResolver(
		&slowConcurrentTool{name: "slow", delay: 20 * time.Millisecond},
		&nonSafeTool{name: "blocker", result: "blocked"},
	)

	eval := &fakeEvaluator{
		decide: func(_ EvaluateInput) EvaluateResult {
			return EvaluateResult{Decision: DecisionAllow}
		},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools, Evaluator: eval})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
	})
	reason := drain(t, rr)
	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v", reason)
	}

	var ids []string
	for _, m := range conv.All() {
		if m.Role == api.RoleTool {
			for _, b := range m.Content {
				if b.ToolResult != nil {
					ids = append(ids, b.ToolResult.ToolCallID)
				}
			}
		}
	}
	if !slicesEqual(ids, []string{"a", "b", "c", "d"}) {
		t.Errorf("tool IDs = %v, want a,b,c,d preserved", ids)
	}
}

// nonSafeTool is a concurrency-NOT-safe test tool.
type nonSafeTool struct {
	name, result string
}

func (t *nonSafeTool) Name() string                     { return t.name }
func (t *nonSafeTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: t.name} }
func (t *nonSafeTool) FormatForDisplay(_ []byte) string { return t.name + "()" }
func (t *nonSafeTool) MaxResultBytes() int              { return 0 }
func (t *nonSafeTool) IsReadOnly() bool                 { return false }
func (t *nonSafeTool) IsConcurrencySafe() bool          { return false }
func (t *nonSafeTool) PermissionKey(_ []byte) string    { return "" }
func (t *nonSafeTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	return Result{Content: t.result, Bytes: len(t.result)}, nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeEvaluator is a test-only PermissionEvaluator. Resolves via a function.
type fakeEvaluator struct {
	decide func(EvaluateInput) EvaluateResult
}

func (f *fakeEvaluator) Evaluate(in EvaluateInput) EvaluateResult {
	return f.decide(in)
}

func TestRun_EvaluatorAllowSkipsPrompt(t *testing.T) {
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			toolUseResponse("t1", "echo", `{}`),
			textResponse("done"),
		},
	}
	tools := newFakeResolver(&echoTool{name: "echo", result: "ok"})
	eval := &fakeEvaluator{
		decide: func(_ EvaluateInput) EvaluateResult {
			return EvaluateResult{Decision: DecisionAllow, Reason: "test allow"}
		},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools, Evaluator: eval})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))

	called := 0
	canUseTool := func(context.Context, string, string, []byte) (Decision, error) {
		called++
		return DecisionAllow, nil
	}

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   canUseTool,
	})
	reason := drain(t, rr)
	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v", reason)
	}
	if called != 0 {
		t.Errorf("CanUseTool called %d times when evaluator pre-allowed; want 0", called)
	}
}

func TestRun_EvaluatorDenyProducesToolError(t *testing.T) {
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			toolUseResponse("t1", "echo", `{}`),
			textResponse("recovered"),
		},
	}
	tools := newFakeResolver(&echoTool{name: "echo", result: "should-not-run"})
	eval := &fakeEvaluator{
		decide: func(_ EvaluateInput) EvaluateResult {
			return EvaluateResult{Decision: DecisionDeny, Reason: "test deny"}
		},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools, Evaluator: eval})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("try"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll, // shouldn't be consulted
	})

	var sawDenyEvent bool
	for e := range rr.Events {
		if e.Type == EventToolCallDone && e.ToolError && strings.Contains(e.ToolResult, "test deny") {
			sawDenyEvent = true
		}
	}
	reason := <-rr.Done
	if !errors.Is(reason, ErrEndTurn) {
		t.Errorf("reason = %v, want ErrEndTurn (loop continued after deny)", reason)
	}
	if !sawDenyEvent {
		t.Error("expected tool_result error carrying the evaluator reason")
	}
}

// fakeCompactor is a test-only implementation of Compactor.
type fakeCompactor struct {
	onMaybe          func(*Conversation, string, ToolResolver) CompactResult
	onForceSummarize func(*Conversation, string) (*api.Message, string, error)
	maybeCalls       int
	forceCalls       int
}

func (f *fakeCompactor) MaybeCompact(_ context.Context, conv *Conversation, model string, resolver ToolResolver) CompactResult {
	f.maybeCalls++
	if f.onMaybe != nil {
		return f.onMaybe(conv, model, resolver)
	}
	return CompactResult{Outcome: CompactOutcomeNoop}
}

func (f *fakeCompactor) ForceSummarize(_ context.Context, conv *Conversation, model string) (*api.Message, string, error) {
	f.forceCalls++
	if f.onForceSummarize != nil {
		return f.onForceSummarize(conv, model)
	}
	return nil, "", errors.New("not implemented")
}

func TestRun_PreCallCompactionFires(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	fs := &fakeStore{}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	summary := api.NewUserMessage("<compact_summary>x</compact_summary>")
	agnt.compactor = &fakeCompactor{
		onMaybe: func(conv *Conversation, _ string, _ ToolResolver) CompactResult {
			return CompactResult{
				Outcome:        CompactOutcomeSummarized,
				Reason:         "test",
				SummaryMessage: &summary,
			}
		},
	}

	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv, MaxSteps: 2, CanUseTool: allowAll,
		SessionID: "s1", Store: fs,
	})

	var sawCompactionEvent bool
	for e := range rr.Events {
		if e.Type == EventCompactionApplied {
			sawCompactionEvent = true
		}
	}
	<-rr.Done

	if !sawCompactionEvent {
		t.Error("expected EventCompactionApplied")
	}
	// Summary message should be persisted.
	var summaryPersisted bool
	for _, c := range fs.calls {
		if c.msgID == summary.ID {
			summaryPersisted = true
		}
	}
	if !summaryPersisted {
		t.Error("summary message not persisted via Store")
	}
}

func TestRun_ReactiveRetryOnContextLimit(t *testing.T) {
	// First Complete call errors with "prompt is too long"; second succeeds.
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			{{Type: api.EventError, Error: errors.New("prompt is too long")}},
			textResponse("recovered"),
		},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	summary := api.NewUserMessage("<compact_summary>forced</compact_summary>")
	fc := &fakeCompactor{
		onForceSummarize: func(_ *Conversation, _ string) (*api.Message, string, error) {
			return &summary, "", nil
		},
	}
	agnt.compactor = fc

	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv, MaxSteps: 3, CanUseTool: allowAll,
	})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v, want ErrEndTurn (reactive retry should have recovered)", reason)
	}
	if fc.forceCalls != 1 {
		t.Errorf("ForceSummarize calls = %d, want 1", fc.forceCalls)
	}
	if prov.calls < 2 {
		t.Errorf("provider.Complete called %d times, want ≥ 2 (retry)", prov.calls)
	}
}

func TestRun_ReactiveRetryGivesUpAfterSecondFailure(t *testing.T) {
	// Both calls error → surfaces ErrContextLimit.
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			{{Type: api.EventError, Error: errors.New("prompt is too long")}},
			{{Type: api.EventError, Error: errors.New("prompt is too long")}},
		},
	}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	summary := api.NewUserMessage("<compact_summary>x</compact_summary>")
	agnt.compactor = &fakeCompactor{
		onForceSummarize: func(_ *Conversation, _ string) (*api.Message, string, error) {
			return &summary, "", nil
		},
	}

	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv, MaxSteps: 3, CanUseTool: allowAll,
	})
	reason := drain(t, rr)

	if !errors.Is(reason, ErrContextLimit) {
		t.Errorf("reason = %v, want ErrContextLimit", reason)
	}
}

// fakeStore captures every AppendMessage call. Implements agent.Store.
type fakeStore struct {
	calls        []fakeStoreCall
	summaryCalls []fakeSummaryCall
	// In-memory plan_path persistence per session id.
	// Tests for the first-plan-write hook assert against this map.
	planPaths map[string]string
}

type fakeStoreCall struct {
	sessionID string
	msgID     string
	role      api.Role
	usage     *api.Usage
}

type fakeSummaryCall struct {
	sessionID                string
	msgID                    string
	replacedThroughMessageID string
}

func (s *fakeStore) AppendMessage(_ context.Context, sessionID string, msg api.Message, usage *api.Usage) error {
	s.calls = append(s.calls, fakeStoreCall{
		sessionID: sessionID,
		msgID:     msg.ID,
		role:      msg.Role,
		usage:     usage,
	})
	return nil
}

func (s *fakeStore) AppendSummaryMessage(_ context.Context, sessionID string, msg api.Message, replacedThroughMessageID string) error {
	s.summaryCalls = append(s.summaryCalls, fakeSummaryCall{
		sessionID:                sessionID,
		msgID:                    msg.ID,
		replacedThroughMessageID: replacedThroughMessageID,
	})
	return nil
}

// LoadPlanPath returns the persisted plan_path for sessionID, or ""
// when none. Errors are never returned in the fake — tests that
// want error injection should wrap or replace this.
func (s *fakeStore) LoadPlanPath(_ context.Context, sessionID string) (string, error) {
	if s.planPaths == nil {
		return "", nil
	}
	return s.planPaths[sessionID], nil
}

// SetPlanPath stores the path in the in-memory map. Tests assert
// against `s.planPaths` after triggering the first-write hook.
func (s *fakeStore) SetPlanPath(_ context.Context, sessionID, path string) error {
	if s.planPaths == nil {
		s.planPaths = make(map[string]string)
	}
	s.planPaths[sessionID] = path
	return nil
}

func TestRun_PersistsMessagesViaStore(t *testing.T) {
	prov := &fakeProvider{
		responses: [][]api.StreamEvent{
			// turn 1: tool use
			{
				{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "echo"},
				{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
				{Type: api.EventDone, StopReason: "tool_use"},
			},
			// turn 2: text response
			textResponse("done"),
		},
	}
	tools := newFakeResolver(&echoTool{name: "echo", result: "ok"})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))

	fs := &fakeStore{}
	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		SessionID:    "sess_1",
		Store:        fs,
	})
	reason := drain(t, rr)
	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v, want ErrEndTurn", reason)
	}

	// Expected persistence calls:
	//   1. assistant (turn 1, has tool_use)
	//   2. tool_result (turn 1)
	//   3. assistant (turn 2, text only)
	if len(fs.calls) != 3 {
		t.Fatalf("persist calls = %d, want 3", len(fs.calls))
	}
	if fs.calls[0].role != api.RoleAssistant {
		t.Errorf("call[0].role = %q, want assistant", fs.calls[0].role)
	}
	if fs.calls[1].role != api.RoleTool {
		t.Errorf("call[1].role = %q, want tool", fs.calls[1].role)
	}
	if fs.calls[2].role != api.RoleAssistant {
		t.Errorf("call[2].role = %q, want assistant", fs.calls[2].role)
	}
	for i, c := range fs.calls {
		if c.sessionID != "sess_1" {
			t.Errorf("call[%d].sessionID = %q", i, c.sessionID)
		}
		if c.msgID == "" {
			t.Errorf("call[%d].msgID is empty", i)
		}
	}
}

func TestRun_NoPersistenceWhenStoreNil(t *testing.T) {
	// Baseline regression: zero-valued SessionID/Store/FileChanges must not
	// break the loop.
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("hi")}}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv, MaxSteps: 2, CanUseTool: allowAll,
	})
	reason := drain(t, rr)
	if !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v", reason)
	}
}

func TestRun_EventsChannelClosesOnCompletion(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})

	for range rr.Events {
	}
	// If the channel didn't close, the range would block forever and the
	// test harness would time out. Reaching here = channel closed.
	select {
	case _, ok := <-rr.Events:
		if ok {
			t.Fatal("Events channel not closed")
		}
	default:
	}
	<-rr.Done
}

// TestRun_PlanMode_EndToEnd_WritesPlanThenApproves is the Phase-24
// arc closer. It drives the whole plan-mode flow in a single
// agent.Run call: the model writes a schema-valid plan markdown
// body to a slug path, then calls plan_exit. Both calls auto-allow
// (no real user). The test asserts:
//
//   - The first-write hook persisted plan_path on the session row.
//   - plan_exit's pre-flight read the file, ValidatePlanMarkdown
//     passed, and EventPlanApproved fired with the chosen path.
//   - No tool errors slipped through.
//   - Done returned ErrEndTurn so the TUI's done-handler would
//     proceed to flip to build.
//
// The test exercises Phases 19 (validator), 20 (path persistence),
// 21 (plan_exit + event), and 23 (backup hook is a no-op on first
// write but mustn't error). Phases 18 and 22 don't need run-loop
// integration coverage — 18 is prompt content and 22 is permission
// fast-path, both fully exercised by their own unit tests.
func TestRun_PlanMode_EndToEnd_WritesPlanThenApproves(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	planPath := filepath.Join(tmp, ".prompto", "plans", "2026-04-30-undo.md")
	body := "# Plan\n\n## Context\nbecause\n\n## Goal & acceptance criteria\nx\n\n## Files\ny\n\n## Verification\nz\n\n## Risks / out-of-scope\nw\n"
	writeArgs, err := json.Marshal(map[string]string{"path": planPath, "content": body})
	if err != nil {
		t.Fatal(err)
	}

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		// Turn 1: write the plan to a slug path.
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_1", ToolCallName: "write"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: string(writeArgs)},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
		// Turn 2: ratify via plan_exit.
		{
			{Type: api.EventToolCallStart, ToolCallIndex: 0, ToolCallID: "tc_2", ToolCallName: "plan_exit"},
			{Type: api.EventToolCallDelta, ToolCallIndex: 0, ToolCallArgs: `{}`},
			{Type: api.EventDone, StopReason: "tool_use"},
		},
	}}

	fs := &fakeStore{}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(&realWriteStubTool{}, &planExitStubTool{}),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("write a plan and exit"))

	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv,
		MaxSteps:     5,
		CanUseTool:   allowAll,
		AgentName:    "plan",
		Store:        fs,
		SessionID:    "sess-1",
	})

	var (
		approvedPath string
		toolErrors   []string
	)
	for ev := range rr.Events {
		switch ev.Type {
		case EventPlanApproved:
			approvedPath = ev.ToolDisp
		case EventToolCallDone:
			if ev.ToolError {
				toolErrors = append(toolErrors, ev.ToolName+": "+ev.ToolResult)
			}
		}
	}
	doneErr := <-rr.Done

	if !errors.Is(doneErr, ErrEndTurn) {
		t.Errorf("Done = %v, want ErrEndTurn", doneErr)
	}
	if len(toolErrors) > 0 {
		t.Errorf("unexpected tool errors: %v", toolErrors)
	}
	if got := fs.planPaths["sess-1"]; got != planPath {
		t.Errorf("planPaths[sess-1] = %q, want %q", got, planPath)
	}
	if approvedPath != planPath {
		t.Errorf("EventPlanApproved.ToolDisp = %q, want %q", approvedPath, planPath)
	}
	if _, err := os.Stat(planPath); err != nil {
		t.Errorf("plan file not created on disk: %v", err)
	}
}

func TestConversation_MessagesFiltersSystem(t *testing.T) {
	conv := NewConversation()
	conv.Append(api.Message{Role: api.RoleSystem, Content: []api.ContentBlock{{Type: api.BlockText, Text: "system"}}})
	conv.Append(api.NewUserMessage("hello"))

	if len(conv.Messages()) != 1 {
		t.Fatalf("Messages = %d, want 1", len(conv.Messages()))
	}
	if len(conv.All()) != 2 {
		t.Fatalf("All = %d, want 2", len(conv.All()))
	}
}
