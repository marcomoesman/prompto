package agent

import (
	"strings"
	"sync"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestDefaultNotifier_PreTurnRunsAllCheckers(t *testing.T) {
	a := stubChecker{name: "a", reply: "msg-a"}
	b := stubChecker{name: "b", reply: ""} // declines
	c := stubChecker{name: "c", reply: "msg-c"}
	n := NewNotifier(a, b, c)

	got := n.PreTurn(PreTurnContext{})
	if len(got) != 2 || got[0] != "msg-a" || got[1] != "msg-c" {
		t.Errorf("got %v, want [msg-a msg-c]", got)
	}
}

func TestDefaultNotifier_OneShotQueueAndDrain(t *testing.T) {
	n := NewNotifier()
	if got := n.ConsumeOneShot(); got != nil {
		t.Errorf("empty queue = %v, want nil", got)
	}

	n.QueueOneShot("first")
	n.QueueOneShot("second")
	n.QueueOneShot("") // empty bodies are ignored
	got := n.ConsumeOneShot()
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("got %v, want [first second]", got)
	}

	// Drained: subsequent consume returns nil.
	if again := n.ConsumeOneShot(); again != nil {
		t.Errorf("after drain = %v, want nil", again)
	}
}

func TestDefaultNotifier_QueueOneShotConcurrent(t *testing.T) {
	n := NewNotifier()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.QueueOneShot("x")
		}()
	}
	wg.Wait()
	got := n.ConsumeOneShot()
	// QueueOneShot caps the in-memory queue at maxOneShots; older
	// entries get dropped under pressure. The test guards concurrency
	// safety + the cap contract: survivors are "x" (no corruption),
	// total never exceeds the cap, queue is non-empty.
	if len(got) > maxOneShots {
		t.Errorf("len = %d, want at most %d", len(got), maxOneShots)
	}
	if len(got) == 0 {
		t.Errorf("len = 0, want at least 1 survivor")
	}
	for i, v := range got {
		if v != "x" {
			t.Errorf("got[%d] = %q, want %q", i, v, "x")
		}
	}
}

func TestNewDefaultNotifier_SeedsBuiltins(t *testing.T) {
	n := NewDefaultNotifier()
	dn, ok := n.(*defaultNotifier)
	if !ok {
		t.Fatalf("expected *defaultNotifier, got %T", n)
	}
	want := map[string]bool{"plan_mode": true, "verify_after_edit": true, "todowrite_stale": true, "web_vs_local": true}
	got := map[string]bool{}
	for _, c := range dn.checkers {
		got[c.Name()] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing built-in checker %q", name)
		}
	}
}

func TestWrapReminder(t *testing.T) {
	if WrapReminder("") != "" {
		t.Error("empty body should not be wrapped")
	}
	got := WrapReminder("body")
	if !strings.Contains(got, "<system-reminder>") || !strings.Contains(got, "</system-reminder>") {
		t.Errorf("missing tags: %q", got)
	}
	if !strings.Contains(got, "body") {
		t.Errorf("missing body: %q", got)
	}
}

func TestInjectReminders_AppendsAllToLastUser(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.ContentBlock{{Type: api.BlockText, Text: "hi"}}},
	}
	out := InjectReminders(msgs, []string{"one", "two"})
	if len(out) != 1 {
		t.Fatalf("len = %d", len(out))
	}
	last := out[0]
	if len(last.Content) != 3 {
		t.Fatalf("content blocks = %d, want 3 (orig + 2 reminders)", len(last.Content))
	}
	if !strings.Contains(last.Content[1].Text, "one") {
		t.Errorf("first reminder = %q", last.Content[1].Text)
	}
	if !strings.Contains(last.Content[2].Text, "two") {
		t.Errorf("second reminder = %q", last.Content[2].Text)
	}
	// Caller's slice unchanged.
	if len(msgs[0].Content) != 1 {
		t.Errorf("input mutated; len = %d", len(msgs[0].Content))
	}
}

func TestInjectReminders_EmptyBodiesSkipped(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.ContentBlock{{Type: api.BlockText, Text: "hi"}}},
	}
	out := InjectReminders(msgs, []string{"", "real", ""})
	last := out[0]
	if len(last.Content) != 2 {
		t.Fatalf("got %d blocks, want 2", len(last.Content))
	}
	if !strings.Contains(last.Content[1].Text, "real") {
		t.Errorf("missing real reminder: %q", last.Content[1].Text)
	}
}

func TestInjectReminders_NoUserNoOp(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleAssistant, Content: []api.ContentBlock{{Type: api.BlockText, Text: "hi"}}},
	}
	out := InjectReminders(msgs, []string{"x"})
	if len(out[0].Content) != 1 {
		t.Errorf("expected unchanged, got %+v", out)
	}
}

// --- helpers ---

type stubChecker struct {
	name  string
	reply string
}

func (s stubChecker) Name() string                  { return s.name }
func (s stubChecker) Check(_ PreTurnContext) string { return s.reply }
