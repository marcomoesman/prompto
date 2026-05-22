package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

type countingReadTool struct {
	calls int
	err   error
}

func (t *countingReadTool) Name() string                     { return "read" }
func (t *countingReadTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "read"} }
func (t *countingReadTool) FormatForDisplay(_ []byte) string { return "read()" }
func (t *countingReadTool) MaxResultBytes() int              { return 0 }
func (t *countingReadTool) IsReadOnly() bool                 { return true }
func (t *countingReadTool) IsConcurrencySafe() bool          { return true }
func (t *countingReadTool) PermissionKey(_ []byte) string    { return "" }
func (t *countingReadTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	t.calls++
	if t.err != nil {
		return Result{}, t.err
	}
	return Result{Content: "read-result", Bytes: len("read-result")}, nil
}

func TestLoopGuard_DuplicateReadUsesCachedResult(t *testing.T) {
	read := &countingReadTool{}
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("tc_1", "read", `{"path":"x.go"}`),
		toolUseResponse("tc_2", "read", `{"path":"x.go"}`),
		textResponse("done"),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(read),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("read twice"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	var sawCached bool
	for evt := range rr.Events {
		if evt.Type == EventToolCallDone && strings.Contains(evt.ToolResult, "[cached duplicate result]") {
			sawCached = true
		}
	}
	if err := <-rr.Done; !errors.Is(err, ErrEndTurn) {
		t.Fatalf("run returned %v, want ErrEndTurn", err)
	}
	if read.calls != 1 {
		t.Fatalf("read executed %d times, want 1", read.calls)
	}
	if !sawCached {
		t.Fatal("expected cached duplicate result")
	}
}

func TestLoopGuard_DoesNotCacheErrors(t *testing.T) {
	read := &countingReadTool{err: errors.New("boom")}
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("tc_1", "read", `{"path":"x.go"}`),
		toolUseResponse("tc_2", "read", `{"path":"x.go"}`),
		textResponse("done"),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(read),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("read twice"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	_ = drain(t, rr)
	if read.calls != 2 {
		t.Fatalf("read executed %d times, want 2 because errors are not cached", read.calls)
	}
}

func TestLoopGuard_ReadLoopRemindersFireAndReset(t *testing.T) {
	g := NewLoopGuard()
	var queued []string
	queue := func(s string) { queued = append(queued, s) }
	for i := 0; i < 8; i++ {
		g.RecordPlanResult(successPlan("read"), queue)
	}
	if len(queued) != 2 {
		t.Fatalf("queued %d reminders, want 2: %#v", len(queued), queued)
	}
	if !strings.Contains(queued[0], "several read") || !strings.Contains(queued[1], "many read") {
		t.Fatalf("unexpected reminders: %#v", queued)
	}
	g.RecordPlanResult(successPlan("bash"), queue)
	queued = nil
	for i := 0; i < 4; i++ {
		g.RecordPlanResult(successPlan("read"), queue)
	}
	if len(queued) != 0 {
		t.Fatalf("read count should reset after bash, got %#v", queued)
	}
}

func TestLoopGuard_EditSpiralReminder(t *testing.T) {
	g := NewLoopGuard()
	var queued []string
	queue := func(s string) { queued = append(queued, s) }
	for i := 0; i < 3; i++ {
		p := successPlan("edit")
		p.argsStr = `{"path":"x.go"}`
		p.resultIsError = true
		g.RecordPlanResult(p, queue)
	}
	if len(queued) != 1 || !strings.Contains(queued[0], "Repeated edits") {
		t.Fatalf("queued = %#v, want edit spiral reminder", queued)
	}
}

func TestLoopGuard_MutatingToolsAreNeverDeduplicated(t *testing.T) {
	g := NewLoopGuard()
	for _, name := range []string{"write", "edit", "replace_lines"} {
		p := successPlan(name)
		p.isReadOnly = false
		p.isConcurrent = false
		g.RecordPlanResult(p, nil)
		if g.MaybeUseCached(p) {
			t.Fatalf("%s must not be deduplicated", name)
		}
	}
}

func TestLoopGuard_GreetingRegressionRetriesWithoutPersistingGreeting(t *testing.T) {
	read := &countingReadTool{}
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("tc_1", "read", `{"path":"x.go"}`),
		textResponse("How can I help you today?"),
		textResponse("done"),
	}}
	agnt := New(NewAgentInput{
		Provider: prov,
		Model:    "test",
		Tools:    newFakeResolver(read),
		Notifier: NewNotifier(),
	})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("read and continue"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	if err := drain(t, rr); !errors.Is(err, ErrEndTurn) {
		t.Fatalf("run returned %v, want ErrEndTurn", err)
	}
	if prov.calls != 3 {
		t.Fatalf("provider calls = %d, want 3", prov.calls)
	}
	for _, msg := range conv.Messages() {
		if msg.Text() == "How can I help you today?" {
			t.Fatal("greeting regression should not be persisted")
		}
	}
}

func successPlan(name string) *toolCallPlan {
	return &toolCallPlan{
		acc:           &toolCallAccumulator{id: "tc", name: name},
		argsStr:       `{}`,
		tool:          &echoTool{name: name, result: "ok"},
		isReadOnly:    true,
		isConcurrent:  true,
		resultContent: "ok",
	}
}
