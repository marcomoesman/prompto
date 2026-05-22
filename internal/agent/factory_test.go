package agent

import (
	"context"
	"errors"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcomoesman/prompto/internal/api"
)

// fakeSpawnerStore extends fakeStore (defined in agent_test.go) with the
// child-session methods NewSpawner needs. Tracks every call so tests can
// assert ordering and arguments.
type fakeSpawnerStore struct {
	fakeStore

	mu             sync.Mutex
	createdSessID  string                    // returned by CreateChildSession
	createCalls    []CreateChildSessionInput // capture every CreateChildSession call
	statusCalls    []fakeStatusCall          // capture every SetSessionStatus call
	loadMessagesFn func(ctx context.Context, id string) ([]api.Message, error)
	createErr      error
}

type fakeStatusCall struct {
	sessionID string
	status    string
}

func (s *fakeSpawnerStore) CreateChildSession(_ context.Context, in CreateChildSessionInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return "", s.createErr
	}
	s.createCalls = append(s.createCalls, in)
	if s.createdSessID == "" {
		s.createdSessID = "child-1"
	}
	return s.createdSessID, nil
}

func (s *fakeSpawnerStore) SetSessionStatus(_ context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCalls = append(s.statusCalls, fakeStatusCall{id, status})
	return nil
}

func (s *fakeSpawnerStore) LoadMessages(ctx context.Context, id string) ([]api.Message, error) {
	if s.loadMessagesFn != nil {
		return s.loadMessagesFn(ctx, id)
	}
	return nil, nil
}

func newSpawnerAgent(prov api.Provider, tools ToolResolver, gate *ProviderGate) *Agent {
	return New(NewAgentInput{
		Provider: prov,
		Model:    "test-model",
		Tools:    tools,
		Gate:     gate,
	})
}

// registerSubagent adds a ModeSubagent definition with the given allowlist
// to the agent's registry. Tests use this to introduce a callable subagent
// without touching DefaultRegistry built-ins.
func registerSubagent(t *testing.T, a *Agent, name string, tools []string) {
	t.Helper()
	def := AgentDefinition{
		Name: name,
		Mode: ModeSubagent,
		// SystemPrompt nil → falls back to BuildSystemPrompt; fine for tests.
		Tools: tools,
	}
	if err := a.registry.Register(def); err != nil {
		t.Fatalf("register subagent %q: %v", name, err)
	}
}

func TestSpawner_RejectsUnknownSubagent(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	_, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "does-not-exist",
		Prompt:       "hi",
	})
	if err == nil {
		t.Fatal("expected error for unknown subagent")
	}
}

func TestSubagentApprovalGate_AllowsToolForChildLifetime(t *testing.T) {
	var calls int
	gate := newSubagentApprovalGate(func(context.Context, string, string, []byte) (Decision, error) {
		calls++
		return DecisionAllowForSubagent, nil
	})
	ctx := WithSubagentApprovalScope(t.Context(), "explore", "child-1")

	decision, err := gate.CanUseTool(ctx, "read", "/tmp/a.go", nil)
	if err != nil {
		t.Fatalf("first CanUseTool: %v", err)
	}
	if decision != DecisionAllow {
		t.Fatalf("first decision = %v, want DecisionAllow", decision)
	}

	decision, err = gate.CanUseTool(ctx, "read", "/tmp/b.go", nil)
	if err != nil {
		t.Fatalf("second CanUseTool: %v", err)
	}
	if decision != DecisionAllow {
		t.Fatalf("second decision = %v, want DecisionAllow", decision)
	}
	if calls != 1 {
		t.Fatalf("parent approval calls = %d, want 1", calls)
	}

	decision, err = gate.CanUseTool(ctx, "grep", "search:/tmp", nil)
	if err != nil {
		t.Fatalf("third CanUseTool: %v", err)
	}
	if decision != DecisionAllow {
		t.Fatalf("third decision = %v, want DecisionAllow", decision)
	}
	if calls != 2 {
		t.Fatalf("parent approval calls after different tool = %d, want 2", calls)
	}
}

func TestSpawner_RejectsPrimaryOnlyAgent(t *testing.T) {
	// "build" is registered by DefaultRegistry as ModePrimary — it should
	// not be invokable as a subagent.
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	_, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "build",
		Prompt:       "hi",
	})
	if err == nil {
		t.Fatal("expected error for primary-only agent invoked as subagent")
	}
}

// TestSpawner_ReadOnlyParentRejectsNonReadOnlyChild covers the
// plan→build subagent guard: a read-only parent (plan) cannot spawn a
// child whose definition isn't also read-only. Without this rule the
// plan agent could bypass its own restrictions by delegating to a
// build subagent.
func TestSpawner_ReadOnlyParentRejectsNonReadOnlyChild(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	// Register a non-read-only subagent so we have a valid spawn target
	// that should still be rejected when the parent is read-only.
	if err := a.registry.Register(AgentDefinition{
		Name:     "writer-sub",
		Mode:     ModeSubagent,
		ReadOnly: false,
	}); err != nil {
		t.Fatalf("register writer-sub: %v", err)
	}

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	_, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType:    "writer-sub",
		Prompt:          "hi",
		ParentAgentName: "plan", // built-in plan agent has ReadOnly=true
	})
	if err == nil {
		t.Fatal("expected error: read-only parent cannot spawn non-read-only subagent")
	}
}

// TestSpawner_ReadOnlyParentAllowsReadOnlyChild asserts the guard
// doesn't over-fire — a read-only parent spawning a read-only child
// (the plan→explore happy path) succeeds.
func TestSpawner_ReadOnlyParentAllowsReadOnlyChild(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	res, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType:    "explore", // built-in, ReadOnly=true
		Prompt:          "hi",
		ParentAgentName: "plan",
	})
	if err != nil {
		t.Fatalf("read-only parent + read-only child should succeed: %v", err)
	}
	if res.Result != "ok" {
		t.Errorf("Result=%q, want %q", res.Result, "ok")
	}
}

// TestSpawner_NonReadOnlyParentSpawnsAnything asserts the guard only
// applies to read-only parents — a build agent can still spawn any
// subagent it pleases.
func TestSpawner_NonReadOnlyParentSpawnsAnything(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	if err := a.registry.Register(AgentDefinition{
		Name:     "writer-sub",
		Mode:     ModeSubagent,
		ReadOnly: false,
	}); err != nil {
		t.Fatalf("register writer-sub: %v", err)
	}

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	_, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType:    "writer-sub",
		Prompt:          "hi",
		ParentAgentName: "build", // built-in build agent has ReadOnly=false
	})
	if err != nil {
		t.Fatalf("non-read-only parent should spawn freely: %v", err)
	}
}

func TestSpawner_CreatesChildSessionAndReturnsText(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("found 3 files")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	registerSubagent(t, a, "echo-sub", nil)

	store := &fakeSpawnerStore{}
	spawn := NewSpawner(SpawnerInput{Agent: a, Store: store, CanUseTool: allowAll})

	ctx := WithParentSession(t.Context(), "parent-sess")
	res, err := spawn(ctx, TaskSpawnInput{
		SubagentType: "echo-sub",
		Prompt:       "look",
		Description:  "investigate auth",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.TaskID != "child-1" {
		t.Errorf("TaskID = %q, want child-1", res.TaskID)
	}
	if res.Result != "found 3 files" {
		t.Errorf("Result = %q, want %q", res.Result, "found 3 files")
	}

	// CreateChildSession should be called exactly once with parent-sess.
	if len(store.createCalls) != 1 {
		t.Fatalf("CreateChildSession calls = %d, want 1", len(store.createCalls))
	}
	c := store.createCalls[0]
	if c.ParentID != "parent-sess" {
		t.Errorf("ParentID = %q, want parent-sess", c.ParentID)
	}
	if c.AgentName != "echo-sub" {
		t.Errorf("AgentName = %q", c.AgentName)
	}
	if c.Title != "investigate auth" {
		t.Errorf("Title = %q", c.Title)
	}
	if c.Model != "test-model" {
		t.Errorf("Model = %q", c.Model)
	}

	// SetSessionStatus("ended") should be called for non-resume runs.
	if len(store.statusCalls) != 1 {
		t.Fatalf("SetSessionStatus calls = %d, want 1", len(store.statusCalls))
	}
	if store.statusCalls[0].sessionID != "child-1" || store.statusCalls[0].status != "ended" {
		t.Errorf("status call = %+v, want {child-1 ended}", store.statusCalls[0])
	}

	// The seed user message should have been persisted via AppendMessage so
	// LoadMessages can pick it up on resume.
	if len(store.calls) == 0 {
		t.Fatal("expected AppendMessage calls (at minimum the seed user message)")
	}
	if store.calls[0].sessionID != "child-1" {
		t.Errorf("first AppendMessage sessionID = %q, want child-1", store.calls[0].sessionID)
	}
	if store.calls[0].role != api.RoleUser {
		t.Errorf("first AppendMessage role = %q, want user (seed prompt)", store.calls[0].role)
	}
}

func TestSpawner_NoStoreSkipsPersistence(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	registerSubagent(t, a, "echo-sub", nil)

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	res, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "echo-sub",
		Prompt:       "go",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.TaskID != "" {
		t.Errorf("TaskID = %q, want empty (no store)", res.TaskID)
	}
	if res.Result != "ok" {
		t.Errorf("Result = %q, want ok", res.Result)
	}
}

func TestSpawner_ResumesByTaskID(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("resumed")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	registerSubagent(t, a, "echo-sub", nil)

	prior := []api.Message{
		api.NewUserMessage("first prompt"),
		{
			Role:    api.RoleAssistant,
			Content: []api.ContentBlock{{Type: api.BlockText, Text: "first answer"}},
		},
	}
	var loadCalled int
	store := &fakeSpawnerStore{
		loadMessagesFn: func(_ context.Context, id string) ([]api.Message, error) {
			loadCalled++
			if id != "existing-child" {
				t.Errorf("LoadMessages id = %q, want existing-child", id)
			}
			return prior, nil
		},
	}

	spawn := NewSpawner(SpawnerInput{Agent: a, Store: store, CanUseTool: allowAll})
	res, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "echo-sub",
		Prompt:       "more",
		TaskID:       "existing-child",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.TaskID != "existing-child" {
		t.Errorf("TaskID = %q, want existing-child (resume preserves id)", res.TaskID)
	}
	if loadCalled != 1 {
		t.Errorf("LoadMessages calls = %d, want 1", loadCalled)
	}
	// Resume must NOT create a new session.
	if len(store.createCalls) != 0 {
		t.Errorf("CreateChildSession calls = %d, want 0 on resume", len(store.createCalls))
	}
	// Resume must NOT mark the session ended — future resumes should keep going.
	if len(store.statusCalls) != 0 {
		t.Errorf("SetSessionStatus calls = %d, want 0 on resume", len(store.statusCalls))
	}
}

func TestSpawner_ResumeRequiresStore(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	registerSubagent(t, a, "echo-sub", nil)

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	_, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "echo-sub",
		Prompt:       "hi",
		TaskID:       "abc",
	})
	if err == nil {
		t.Fatal("expected error when resuming without a configured store")
	}
}

func TestSpawner_PropagatesCreateError(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	registerSubagent(t, a, "echo-sub", nil)

	store := &fakeSpawnerStore{createErr: errors.New("db down")}
	spawn := NewSpawner(SpawnerInput{Agent: a, Store: store, CanUseTool: allowAll})

	_, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "echo-sub",
		Prompt:       "go",
	})
	if err == nil {
		t.Fatal("expected error when CreateChildSession fails")
	}
}

// gateBlockingProvider blocks until the test releases it. Combined with a
// 1-permit gate, this drives the saturation test.
type gateBlockingProvider struct {
	release chan struct{}
	started chan struct{}
}

func (p *gateBlockingProvider) ContextLimit(string) int { return 0 }

func (p *gateBlockingProvider) Complete(ctx context.Context, _ api.CompleteParams) iter.Seq[api.StreamEvent] {
	return func(yield func(api.StreamEvent) bool) {
		select {
		case p.started <- struct{}{}:
		default:
		}
		select {
		case <-p.release:
		case <-ctx.Done():
			return
		}
		yield(api.StreamEvent{Type: api.EventDelta, Delta: "done"})
		yield(api.StreamEvent{Type: api.EventDone, StopReason: "end_turn"})
	}
}

func TestSpawner_AcquiresProviderGate(t *testing.T) {
	// Cap at 1: one in-flight subagent should block the second until the
	// first releases the gate.
	gate := NewProviderGate(1)
	prov := &gateBlockingProvider{
		release: make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	a := newSpawnerAgent(prov, newFakeResolver(), gate)
	registerSubagent(t, a, "echo-sub", nil)

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})

	var firstDone, secondDone atomic.Bool

	go func() {
		_, _ = spawn(t.Context(), TaskSpawnInput{SubagentType: "echo-sub", Prompt: "p1"})
		firstDone.Store(true)
	}()

	// Wait for the first call to actually be in-flight (i.e. it has
	// acquired the gate and entered Complete).
	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("first call never entered provider")
	}

	go func() {
		_, _ = spawn(t.Context(), TaskSpawnInput{SubagentType: "echo-sub", Prompt: "p2"})
		secondDone.Store(true)
	}()

	// Give the second goroutine time to attempt Acquire. It should be
	// blocked because the gate has only 1 permit and the first holds it.
	time.Sleep(100 * time.Millisecond)
	if firstDone.Load() {
		t.Fatal("first call returned before release; can't test saturation")
	}
	if secondDone.Load() {
		t.Fatal("second call ran while gate was saturated — gate is not enforcing the cap")
	}

	// Release the first; both should now finish.
	close(prov.release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if firstDone.Load() && secondDone.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("calls did not complete after gate release: first=%v second=%v",
		firstDone.Load(), secondDone.Load())
}

func TestSpawner_CancelCascades(t *testing.T) {
	prov := &waitingProvider{} // blocks until ctx cancels
	a := newSpawnerAgent(prov, newFakeResolver(), nil)
	registerSubagent(t, a, "echo-sub", nil)

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})

	ctx, cancel := context.WithCancel(t.Context())
	doneCh := make(chan error, 1)
	go func() {
		_, err := spawn(ctx, TaskSpawnInput{SubagentType: "echo-sub", Prompt: "wait"})
		doneCh <- err
	}()

	time.AfterFunc(20*time.Millisecond, cancel)

	select {
	case err := <-doneCh:
		// We don't care exactly what the error is — only that the spawn
		// returns rather than hanging forever when the parent ctx is
		// cancelled.
		_ = err
	case <-time.After(time.Second):
		t.Fatal("spawn did not return after parent ctx cancel")
	}
}

// TestSpawner_ForwardsChildEventsToSink verifies the factory's drain loop
// passes the whitelisted child events (ToolCallStarted, ToolCallDone,
// SubagentStep) through args.EventSink while still aggregating
// EventTextDelta into the returned Result.
func TestSpawner_ForwardsChildEventsToSink(t *testing.T) {
	echo := &echoTool{name: "echo", result: "hi"}
	resolver := newFakeResolver(echo)
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("call-1", "echo", `{}`),
		textResponse("done"),
	}}
	a := newSpawnerAgent(prov, resolver, nil)
	registerSubagent(t, a, "echo-sub", []string{"echo"})

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})

	var (
		mu       sync.Mutex
		received []Event
	)
	sink := func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, ev)
	}

	res, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "echo-sub",
		Prompt:       "go",
		EventSink:    sink,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.Result != "done" {
		t.Errorf("Result = %q, want 'done' (text deltas should aggregate even when forwarding)", res.Result)
	}

	mu.Lock()
	defer mu.Unlock()

	got := map[EventType]int{}
	var lastSubagentName string
	for _, ev := range received {
		got[ev.Type]++
		if ev.Type == EventSubagentStep {
			lastSubagentName = ev.ToolSubagent
		}
	}
	if got[EventToolCallStarted] != 1 {
		t.Errorf("EventToolCallStarted forwarded %d times, want 1", got[EventToolCallStarted])
	}
	if got[EventToolCallDone] != 1 {
		t.Errorf("EventToolCallDone forwarded %d times, want 1", got[EventToolCallDone])
	}
	if got[EventSubagentStep] != 1 {
		t.Errorf("EventSubagentStep forwarded %d times, want 1", got[EventSubagentStep])
	}
	if got[EventTextDelta] != 0 {
		t.Errorf("EventTextDelta should NOT be forwarded, got %d", got[EventTextDelta])
	}
	if got[EventTurnComplete] != 0 {
		t.Errorf("EventTurnComplete should NOT be forwarded, got %d", got[EventTurnComplete])
	}
	if lastSubagentName != "echo-sub" {
		t.Errorf("EventSubagentStep ToolSubagent = %q, want 'echo-sub'", lastSubagentName)
	}
}

// TestSpawner_NoSinkSafeFallback verifies the drain loop tolerates a nil
// EventSink — the existing call sites that don't pass one (legacy tests,
// internal callers without a TUI) must still work.
func TestSpawner_NoSinkSafeFallback(t *testing.T) {
	echo := &echoTool{name: "echo", result: "hi"}
	resolver := newFakeResolver(echo)
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("call-1", "echo", `{}`),
		textResponse("done"),
	}}
	a := newSpawnerAgent(prov, resolver, nil)
	registerSubagent(t, a, "echo-sub", []string{"echo"})

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	res, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType: "echo-sub",
		Prompt:       "go",
		// EventSink intentionally nil
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.Result != "done" {
		t.Errorf("Result = %q, want 'done'", res.Result)
	}
}

func TestWithParentSession_RoundTrip(t *testing.T) {
	ctx := t.Context()
	if got := parentSessionFromCtx(ctx); got != "" {
		t.Errorf("parentSessionFromCtx on bare ctx = %q, want empty", got)
	}
	stamped := WithParentSession(ctx, "p-1")
	if got := parentSessionFromCtx(stamped); got != "p-1" {
		t.Errorf("parentSessionFromCtx after stamp = %q, want p-1", got)
	}
	// Empty session id is a no-op (don't pollute ctx with empty values).
	stamped2 := WithParentSession(ctx, "")
	if got := parentSessionFromCtx(stamped2); got != "" {
		t.Errorf("parentSessionFromCtx after empty stamp = %q, want empty", got)
	}
}
