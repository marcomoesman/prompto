package agent

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// asstWithTool builds an assistant message that calls one tool.
func asstWithTool(name string) api.Message {
	return api.Message{
		Role: api.RoleAssistant,
		Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "ok"},
			{Type: api.BlockToolUse, ToolCall: &api.ToolCall{ID: "t", Name: name, Input: []byte(`{}`)}},
		},
	}
}

// userMsg builds a plain user message.
func userMsg(text string) api.Message {
	return api.Message{
		Role:    api.RoleUser,
		Content: []api.ContentBlock{{Type: api.BlockText, Text: text}},
	}
}

func TestPlanModeChecker(t *testing.T) {
	// Not in plan mode → no reminder.
	if got := (PlanModeChecker{}).Check(PreTurnContext{}); got != "" {
		t.Errorf("non-plan-mode → empty, got %q", got)
	}
	// InPlanMode disambiguates "not in plan
	// mode" (empty PlanFilePath, InPlanMode false) from "in plan
	// mode but no file written yet" (empty PlanFilePath, InPlanMode
	// true).
	if got := (PlanModeChecker{}).Check(PreTurnContext{PlanFilePath: "/p/x.md"}); got != "" {
		t.Errorf("PlanFilePath without InPlanMode should still return empty; got %q", got)
	}

	// Pre-write: in plan mode, no file yet → "pick a slug" message.
	preWrite := (PlanModeChecker{}).Check(PreTurnContext{InPlanMode: true})
	for _, want := range []string{"plan mode", "slug", "YYYY-MM-DD", ".prompto/plans"} {
		if !strings.Contains(preWrite, want) {
			t.Errorf("pre-write reminder missing %q; got %q", want, preWrite)
		}
	}

	// Post-write: in plan mode, file recorded → reminder cites the path.
	postWrite := (PlanModeChecker{}).Check(PreTurnContext{InPlanMode: true, PlanFilePath: "/p/x.md"})
	if !strings.Contains(postWrite, "/p/x.md") {
		t.Errorf("post-write reminder missing path: %q", postWrite)
	}
	if !strings.Contains(postWrite, "plan mode") {
		t.Errorf("post-write reminder missing plan mode keyword: %q", postWrite)
	}
}

func TestVerifyAfterEditChecker_FiresAfterEdit(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("change foo"))
	c.Append(asstWithTool("edit"))

	got := (VerifyAfterEditChecker{}).Check(PreTurnContext{Conversation: c})
	if got == "" {
		t.Fatal("expected reminder, got empty")
	}
	if !strings.Contains(got, "tests") && !strings.Contains(got, "go test") {
		t.Errorf("missing test guidance: %q", got)
	}
}

func TestVerifyAfterEditChecker_FiresAfterWrite(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("create foo"))
	c.Append(asstWithTool("write"))

	got := (VerifyAfterEditChecker{}).Check(PreTurnContext{Conversation: c})
	if got == "" {
		t.Error("expected reminder after write")
	}
}

func TestVerifyAfterEditChecker_UsesDetectedCommands(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("change frontend"))
	c.Append(asstWithTool("edit"))

	got := (VerifyAfterEditChecker{}).Check(PreTurnContext{
		Conversation: c,
		Verification: VerificationHint{Commands: []string{"npm test", "npm run build"}},
	})
	if !strings.Contains(got, "`npm test`") || !strings.Contains(got, "`npm run build`") {
		t.Fatalf("detected commands not used: %q", got)
	}
	if strings.Contains(got, "go test ./...") {
		t.Fatalf("fallback Go commands should not be used when detected commands exist: %q", got)
	}
}

func TestVerifyAfterEditChecker_ClearsAfterBash(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("change foo"))
	c.Append(asstWithTool("edit"))
	c.Append(asstWithTool("bash"))

	if got := (VerifyAfterEditChecker{}).Check(PreTurnContext{Conversation: c}); got != "" {
		t.Errorf("bash since edit should silence; got %q", got)
	}
}

func TestVerifyAfterEditChecker_QuietWhenNoEdit(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("read foo"))
	c.Append(asstWithTool("read"))

	if got := (VerifyAfterEditChecker{}).Check(PreTurnContext{Conversation: c}); got != "" {
		t.Errorf("expected silent, got %q", got)
	}
}

func TestVerifyAfterEditChecker_NilConversation(t *testing.T) {
	if got := (VerifyAfterEditChecker{}).Check(PreTurnContext{}); got != "" {
		t.Errorf("nil conv must be silent, got %q", got)
	}
}

func TestTodoWriteStaleChecker_FiresAfterThreshold(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("implement feature"))
	for i := 0; i < 8; i++ {
		c.Append(asstWithTool("read"))
	}

	got := TodoWriteStaleChecker{Threshold: 8}.Check(PreTurnContext{Conversation: c})
	if got == "" {
		t.Fatal("expected stale reminder at threshold")
	}
	if !strings.Contains(got, "TodoWrite") {
		t.Errorf("missing TodoWrite mention: %q", got)
	}
}

func TestTodoWriteStaleChecker_QuietBelowThreshold(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("implement feature"))
	for i := 0; i < 3; i++ {
		c.Append(asstWithTool("read"))
	}
	if got := (TodoWriteStaleChecker{Threshold: 8}).Check(PreTurnContext{Conversation: c}); got != "" {
		t.Errorf("under threshold should be silent, got %q", got)
	}
}

func TestTodoWriteStaleChecker_ResetsAfterTodoWrite(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("implement feature"))
	for i := 0; i < 10; i++ {
		c.Append(asstWithTool("read"))
	}
	c.Append(asstWithTool("todowrite"))
	// One read since the todowrite — well below threshold.
	c.Append(asstWithTool("read"))

	if got := (TodoWriteStaleChecker{Threshold: 5}).Check(PreTurnContext{Conversation: c}); got != "" {
		t.Errorf("post-todowrite count must reset; got %q", got)
	}
}

func TestTodoWriteStaleChecker_ZeroThresholdFallsBack(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("x"))
	for i := 0; i < defaultStaleToolCalls; i++ {
		c.Append(asstWithTool("read"))
	}
	if got := (TodoWriteStaleChecker{}).Check(PreTurnContext{Conversation: c}); got == "" {
		t.Error("zero threshold should use default and fire here")
	}
}

func TestWebVsLocalChecker_FiresAfterWebfetch(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("look up X"))
	c.Append(asstWithTool("webfetch"))

	got := (WebVsLocalChecker{}).Check(PreTurnContext{Conversation: c})
	if got == "" {
		t.Fatal("expected reminder")
	}
	if !strings.Contains(got, "LOCAL") || !strings.Contains(got, "webfetch") {
		t.Errorf("missing boundary text: %q", got)
	}
}

func TestWebVsLocalChecker_QuietAfterUnrelatedTool(t *testing.T) {
	// The merged webfetch tool is the only matched name; a non-web tool
	// must not trigger the boundary reminder.
	c := NewConversation()
	c.Append(userMsg("read foo"))
	c.Append(asstWithTool("read"))

	if got := (WebVsLocalChecker{}).Check(PreTurnContext{Conversation: c}); got != "" {
		t.Errorf("unrelated tool should not fire; got %q", got)
	}
}

func TestWebVsLocalChecker_QuietAfterLaterToolCall(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("look up X"))
	c.Append(asstWithTool("webfetch"))
	c.Append(asstWithTool("read"))

	if got := (WebVsLocalChecker{}).Check(PreTurnContext{Conversation: c}); got != "" {
		t.Errorf("later tool call should silence; got %q", got)
	}
}

func TestWebVsLocalChecker_QuietWhenNoWebFetch(t *testing.T) {
	c := NewConversation()
	c.Append(userMsg("read foo"))
	c.Append(asstWithTool("read"))
	if got := (WebVsLocalChecker{}).Check(PreTurnContext{Conversation: c}); got != "" {
		t.Errorf("expected silent, got %q", got)
	}
}
