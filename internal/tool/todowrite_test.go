package tool

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestTodoWrite_Definition(t *testing.T) {
	tw := NewTodoWriteTool()
	if tw.Name() != "todowrite" {
		t.Errorf("Name = %q, want todowrite", tw.Name())
	}
	if tw.IsReadOnly() {
		t.Error("IsReadOnly = true, want false (writes session sidecar)")
	}
	if tw.IsConcurrencySafe() {
		t.Error("IsConcurrencySafe = true, want false")
	}
	if tw.PermissionKey([]byte(`{"todos":[]}`)) != "" {
		t.Error("PermissionKey should be empty")
	}
}

func TestTodoWrite_AtomicReplaceCallsSaver(t *testing.T) {
	tw := NewTodoWriteTool()

	var savedSession string
	var savedTodos []agent.Todo
	var calls int
	saver := func(_ context.Context, sessionID string, todos []agent.Todo) error {
		calls++
		savedSession = sessionID
		savedTodos = todos
		return nil
	}

	tc := agent.ToolContext{
		SessionID: "sess-42",
		SaveTodos: saver,
	}

	input := []byte(`{"todos":[
		{"id":"a","content":"Do A","status":"pending","active_form":"Doing A"},
		{"id":"b","content":"Do B","status":"in_progress","active_form":"Doing B"}
	]}`)

	res, err := tw.Execute(context.Background(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if calls != 1 {
		t.Errorf("saver calls = %d, want 1", calls)
	}
	if savedSession != "sess-42" {
		t.Errorf("savedSession = %q, want sess-42", savedSession)
	}
	want := []agent.Todo{
		{ID: "a", Content: "Do A", Status: "pending", ActiveForm: "Doing A"},
		{ID: "b", Content: "Do B", Status: "in_progress", ActiveForm: "Doing B"},
	}
	if !reflect.DeepEqual(savedTodos, want) {
		t.Errorf("savedTodos = %+v, want %+v", savedTodos, want)
	}
	if !strings.Contains(res.Content, "1 pending, 1 in progress, 0 done") {
		t.Errorf("Content = %q, want counts in summary", res.Content)
	}
}

func TestTodoWrite_RejectsDuplicateIDs(t *testing.T) {
	tw := NewTodoWriteTool()
	tc := agent.ToolContext{SessionID: "s"}
	input := []byte(`{"todos":[
		{"id":"a","content":"x","status":"pending","active_form":"x"},
		{"id":"a","content":"y","status":"pending","active_form":"y"}
	]}`)
	if _, err := tw.Execute(context.Background(), tc, input); err == nil {
		t.Error("expected error for duplicate IDs")
	}
}

func TestTodoWrite_RejectsInvalidStatus(t *testing.T) {
	tw := NewTodoWriteTool()
	tc := agent.ToolContext{SessionID: "s"}
	input := []byte(`{"todos":[{"id":"a","content":"x","status":"halted","active_form":"x"}]}`)
	if _, err := tw.Execute(context.Background(), tc, input); err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestTodoWrite_AtMostOneInProgress(t *testing.T) {
	tw := NewTodoWriteTool()
	tc := agent.ToolContext{SessionID: "s"}
	input := []byte(`{"todos":[
		{"id":"a","content":"x","status":"in_progress","active_form":"x"},
		{"id":"b","content":"y","status":"in_progress","active_form":"y"}
	]}`)
	if _, err := tw.Execute(context.Background(), tc, input); err == nil {
		t.Error("expected error for multiple in_progress items")
	}
}

func TestTodoWrite_RequiresID(t *testing.T) {
	tw := NewTodoWriteTool()
	tc := agent.ToolContext{SessionID: "s"}
	input := []byte(`{"todos":[{"id":"","content":"x","status":"pending","active_form":"x"}]}`)
	if _, err := tw.Execute(context.Background(), tc, input); err == nil {
		t.Error("expected error for empty id")
	}
}

func TestTodoWrite_NilSaverIsNoOp(t *testing.T) {
	tw := NewTodoWriteTool()
	tc := agent.ToolContext{SessionID: "s"} // no saver
	input := []byte(`{"todos":[{"id":"a","content":"x","status":"pending","active_form":"x"}]}`)
	res, err := tw.Execute(context.Background(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "1 pending") {
		t.Errorf("Content = %q", res.Content)
	}
}

func TestTodoWrite_FormatForDisplay(t *testing.T) {
	tw := NewTodoWriteTool()
	input := []byte(`{"todos":[
		{"id":"1","content":"a","status":"pending","active_form":"a"},
		{"id":"2","content":"b","status":"pending","active_form":"b"},
		{"id":"3","content":"c","status":"pending","active_form":"c"},
		{"id":"4","content":"d","status":"in_progress","active_form":"d"},
		{"id":"5","content":"e","status":"completed","active_form":"e"},
		{"id":"6","content":"f","status":"completed","active_form":"f"}
	]}`)
	got := tw.FormatForDisplay(input)
	want := `TodoWrite(items: "6")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTodoWrite_FormatForDisplayMalformed(t *testing.T) {
	tw := NewTodoWriteTool()
	if got := tw.FormatForDisplay([]byte("not json")); got != "TodoWrite(?)" {
		t.Errorf("got %q, want TodoWrite(?)", got)
	}
}

func TestTodoWrite_PersistErrorBubbles(t *testing.T) {
	tw := NewTodoWriteTool()
	saver := func(_ context.Context, _ string, _ []agent.Todo) error {
		return errBoom
	}
	tc := agent.ToolContext{SessionID: "s", SaveTodos: saver}
	input := []byte(`{"todos":[{"id":"a","content":"x","status":"pending","active_form":"x"}]}`)
	if _, err := tw.Execute(context.Background(), tc, input); err == nil {
		t.Error("expected error to bubble from saver")
	}
}

type sentinelErr string

func (e sentinelErr) Error() string { return string(e) }

const errBoom sentinelErr = "boom"
