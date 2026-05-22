package agent

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// recordingProvider captures every CompleteParams for inspection. Otherwise
// behaves like fakeProvider.
type recordingProvider struct {
	mu        sync.Mutex
	responses [][]api.StreamEvent
	calls     int
	captured  []api.CompleteParams
}

func (p *recordingProvider) ContextLimit(string) int { return 0 }

func (p *recordingProvider) Complete(_ context.Context, params api.CompleteParams) iter.Seq[api.StreamEvent] {
	p.mu.Lock()
	idx := p.calls
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}
	p.calls++
	// Capture a deep enough copy of messages so subsequent injections
	// don't mutate what we observed for the earlier call.
	captured := api.CompleteParams{
		Model:           params.Model,
		System:          params.System,
		MaxTokens:       params.MaxTokens,
		Temperature:     params.Temperature,
		PresencePenalty: params.PresencePenalty,
		Tools:           params.Tools,
	}
	captured.Messages = append(captured.Messages, params.Messages...)
	p.captured = append(p.captured, captured)
	events := p.responses[idx]
	p.mu.Unlock()
	return func(yield func(api.StreamEvent) bool) {
		for _, e := range events {
			if !yield(e) {
				return
			}
		}
	}
}

// memoryTodos is an in-memory TodoStore.
type memoryTodos struct {
	mu     sync.Mutex
	bySess map[string][]Todo
}

func newMemoryTodos() *memoryTodos {
	return &memoryTodos{bySess: make(map[string][]Todo)}
}

func (m *memoryTodos) LoadTodos(_ context.Context, sessionID string) ([]Todo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bySess[sessionID], nil
}

func (m *memoryTodos) SaveTodos(_ context.Context, sessionID string, todos []Todo) error {
	m.mu.Lock()
	m.bySess[sessionID] = append([]Todo(nil), todos...)
	m.mu.Unlock()
	return nil
}

func TestRun_NotifierPreTurnInjectsReminder(t *testing.T) {
	prov := &recordingProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	notifier := NewNotifier(stubChecker{name: "x", reply: "do the thing"})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver(), Notifier: notifier})

	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 2, CanUseTool: allowAll})
	if reason := drain(t, rr); !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v", reason)
	}

	if len(prov.captured) == 0 {
		t.Fatal("no captured calls")
	}
	last := prov.captured[0].Messages[len(prov.captured[0].Messages)-1]
	if last.Role != api.RoleUser {
		t.Fatalf("last role = %q, want user", last.Role)
	}
	var found bool
	for _, blk := range last.Content {
		if strings.Contains(blk.Text, "<system-reminder>") && strings.Contains(blk.Text, "do the thing") {
			found = true
		}
	}
	if !found {
		t.Errorf("reminder not injected; last user content = %+v", last.Content)
	}

	// Persisted conversation must NOT carry the reminder block.
	stored := conv.Messages()[0]
	if len(stored.Content) != 1 {
		t.Errorf("conversation mutated; len = %d, want 1", len(stored.Content))
	}
}

func TestRun_ForwardsConfiguredSamplingOnlyWhenSet(t *testing.T) {
	prov := &recordingProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})

	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 2, CanUseTool: allowAll})
	if reason := drain(t, rr); !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v", reason)
	}
	if len(prov.captured) != 1 {
		t.Fatalf("captured calls = %d, want 1", len(prov.captured))
	}
	if prov.captured[0].Temperature != nil {
		t.Fatalf("temperature = %v, want nil", *prov.captured[0].Temperature)
	}
	if prov.captured[0].PresencePenalty != nil {
		t.Fatalf("presence penalty = %v, want nil", *prov.captured[0].PresencePenalty)
	}

	temp := 0.4
	presence := -0.2
	prov = &recordingProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	agnt = New(NewAgentInput{
		Provider:        prov,
		Model:           "test",
		Tools:           newFakeResolver(),
		Temperature:     &temp,
		PresencePenalty: &presence,
	})

	conv = NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr = agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 2, CanUseTool: allowAll})
	if reason := drain(t, rr); !errors.Is(reason, ErrEndTurn) {
		t.Fatalf("reason = %v", reason)
	}
	if len(prov.captured) != 1 {
		t.Fatalf("captured calls = %d, want 1", len(prov.captured))
	}
	if prov.captured[0].Temperature == nil || *prov.captured[0].Temperature != temp {
		t.Fatalf("temperature = %v, want %v", prov.captured[0].Temperature, temp)
	}
	if prov.captured[0].PresencePenalty == nil || *prov.captured[0].PresencePenalty != presence {
		t.Fatalf("presence penalty = %v, want %v", prov.captured[0].PresencePenalty, presence)
	}
}

func TestRun_NotifierOneShotConsumed(t *testing.T) {
	prov := &recordingProvider{responses: [][]api.StreamEvent{
		textResponse("turn1"),
		textResponse("turn2"),
	}}
	notifier := NewNotifier() // no checkers
	notifier.QueueOneShot("agent switched")
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver(), Notifier: notifier})

	conv := NewConversation()
	conv.Append(api.NewUserMessage("first"))
	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 2, CanUseTool: allowAll})
	_ = drain(t, rr)

	// Second turn with a new user message.
	conv.Append(api.NewUserMessage("second"))
	rr = agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 2, CanUseTool: allowAll})
	_ = drain(t, rr)

	if len(prov.captured) != 2 {
		t.Fatalf("captured calls = %d, want 2", len(prov.captured))
	}

	// Turn 1: one-shot must be present.
	contains := func(msg api.Message, needle string) bool {
		for _, b := range msg.Content {
			if strings.Contains(b.Text, needle) {
				return true
			}
		}
		return false
	}
	turn1Last := prov.captured[0].Messages[len(prov.captured[0].Messages)-1]
	if !contains(turn1Last, "agent switched") {
		t.Errorf("turn 1 missing one-shot reminder; got %+v", turn1Last.Content)
	}
	// Turn 2: queue drained, must be absent.
	turn2Last := prov.captured[1].Messages[len(prov.captured[1].Messages)-1]
	if contains(turn2Last, "agent switched") {
		t.Errorf("turn 2 still carries one-shot reminder; got %+v", turn2Last.Content)
	}
}

func TestRun_TodoStoreLoadsIntoSystemPrompt(t *testing.T) {
	prov := &recordingProvider{responses: [][]api.StreamEvent{textResponse("ok")}}
	todos := newMemoryTodos()
	_ = todos.SaveTodos(context.Background(), "s1", []Todo{
		{ID: "1", Content: "Do the thing", Status: "in_progress", ActiveForm: "Doing the thing"},
	})
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver(), Todos: todos})

	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))
	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv, MaxSteps: 2, CanUseTool: allowAll, SessionID: "s1",
	})
	_ = drain(t, rr)

	if len(prov.captured) == 0 {
		t.Fatal("no captured calls")
	}
	var sawTodos bool
	for _, blk := range prov.captured[0].System {
		if strings.Contains(blk.Text, "Doing the thing") {
			sawTodos = true
		}
	}
	if !sawTodos {
		t.Error("loaded todos did not surface in system prompt")
	}
}

// TestRun_SaveTodosClosureWiredForPrimary stamps a fake todo-using tool and
// verifies tc.SaveTodos was non-nil when invoked.
func TestRun_SaveTodosClosureWiredForPrimary(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("t1", "todocheck", `{}`),
		textResponse("done"),
	}}
	tool := &saveTodosCheckTool{}
	tools := newFakeResolver(tool)
	store := newMemoryTodos()
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: tools, Todos: store})

	conv := NewConversation()
	conv.Append(api.NewUserMessage("go"))
	rr := agnt.Run(t.Context(), RunInput{
		Conversation: conv, MaxSteps: 5, CanUseTool: allowAll, SessionID: "s1",
	})
	_ = drain(t, rr)

	if !tool.sawSaver {
		t.Error("primary run loop did not stamp SaveTodos closure")
	}
}

type saveTodosCheckTool struct {
	sawSaver bool
}

func (t *saveTodosCheckTool) Name() string { return "todocheck" }
func (t *saveTodosCheckTool) Definition() api.ToolDefinition {
	return api.ToolDefinition{Name: "todocheck"}
}
func (t *saveTodosCheckTool) FormatForDisplay(_ []byte) string { return "todocheck()" }
func (t *saveTodosCheckTool) MaxResultBytes() int              { return 0 }
func (t *saveTodosCheckTool) IsReadOnly() bool                 { return false }
func (t *saveTodosCheckTool) IsConcurrencySafe() bool          { return false }
func (t *saveTodosCheckTool) PermissionKey(_ []byte) string    { return "" }
func (t *saveTodosCheckTool) Execute(_ context.Context, tc ToolContext, _ []byte) (Result, error) {
	if tc.SaveTodos != nil {
		t.sawSaver = true
	}
	return Result{Content: "ok", Bytes: 2}, nil
}
