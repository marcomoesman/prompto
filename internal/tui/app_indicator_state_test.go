package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcomoesman/prompto/internal/agent"
)

// helper: wrap an agent.Event into the tea.Msg the TUI consumes.
func toolCallStarted(name, args string) agentEventMsg {
	return agentEventMsg{event: agent.Event{
		Type:     agent.EventToolCallStarted,
		ToolName: name,
		ToolArgs: args,
	}}
}

func toolCallDone(name, result string) agentEventMsg {
	return agentEventMsg{event: agent.Event{
		Type:       agent.EventToolCallDone,
		ToolName:   name,
		ToolResult: result,
	}}
}

func approvalRequest(name, key, args string) ToolApprovalRequestMsg {
	return ToolApprovalRequestMsg{Req: &PendingApproval{
		Name:       name,
		Key:        key,
		Input:      []byte(args),
		IsReadOnly: true,
		Done:       make(chan agent.Decision, 1),
	}}
}

func subagentApprovalRequest(name, key, args, subagent string) ToolApprovalRequestMsg {
	msg := approvalRequest(name, key, args)
	msg.Req.Subagent = subagent
	return msg
}

func keyPress(s string) tea.KeyPressMsg {
	if len(s) != 1 {
		panic("keyPress helper expects a single-rune key for these tests")
	}
	r := rune(s[0])
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// updateModel feeds one tea.Msg into AppModel.Update and returns the new model.
func updateModel(t *testing.T, m AppModel, msg tea.Msg) AppModel {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(AppModel)
	if !ok {
		t.Fatalf("Update returned %T, want AppModel", next)
	}
	return got
}

// TestIndicator_ApprovalThenStartedKeepsApprovalState reproduces the race
// where ToolApprovalRequestMsg lands first and EventToolCallStarted lands
// second. The fix is that the second event must NOT override the
// approval indicator — the user has not yet approved the call.
func TestIndicator_ApprovalThenStartedKeepsApprovalState(t *testing.T) {
	m := newTestAppModel(t)
	args := `{"url":"https://example.com/p"}`

	m = updateModel(t, m, approvalRequest("webfetch", "domain:example.com", args))
	if m.workingState != StateAwaitingApproval {
		t.Fatalf("after approval msg: workingState = %v, want StateAwaitingApproval", m.workingState)
	}

	// Now the racing EventToolCallStarted lands. The indicator must
	// stay on StateAwaitingApproval — the tool isn't running yet.
	m = updateModel(t, m, toolCallStarted("webfetch", args))
	if m.workingState != StateAwaitingApproval {
		t.Errorf("EventToolCallStarted overrode approval state: got %v, want StateAwaitingApproval", m.workingState)
	}
	if m.pending == nil {
		t.Errorf("approval should still be pending; got m.pending == nil")
	}

	// User approves. Indicator should transition into StateToolRunning
	// with the gerund describer ("Reading page content from …"), so
	// the indicator reflects the actual work the agent is doing.
	m = updateModel(t, m, keyPress("y"))
	if m.workingState != StateToolRunning {
		t.Fatalf("after approval grant: workingState = %v, want StateToolRunning", m.workingState)
	}
	if !strings.HasPrefix(m.workingDetail, "Reading page content") {
		t.Errorf("after approval grant: workingDetail = %q, want gerund describer prefix", m.workingDetail)
	}

	// Tool finishes. Indicator returns to Thinking.
	m = updateModel(t, m, toolCallDone("webfetch", "ok"))
	if m.workingState != StateThinking {
		t.Errorf("after tool done: workingState = %v, want StateThinking", m.workingState)
	}
}

func TestApproval_SubagentOptionReturnsSubagentDecision(t *testing.T) {
	m := newTestAppModel(t)
	msg := subagentApprovalRequest("read", "/tmp/a.go", `{"file_path":"/tmp/a.go"}`, "explore")
	done := msg.Req.Done

	m = updateModel(t, m, msg)
	if m.pending == nil {
		t.Fatal("expected pending approval")
	}

	m = updateModel(t, m, keyPress("s"))
	select {
	case got := <-done:
		if got != agent.DecisionAllowForSubagent {
			t.Fatalf("decision = %v, want DecisionAllowForSubagent", got)
		}
	default:
		t.Fatal("expected decision on approval channel")
	}
	if m.pending != nil {
		t.Fatal("pending approval was not cleared")
	}
}

// TestIndicator_StartedThenApprovalShowsApprovalState covers the other
// race ordering: EventToolCallStarted arrives first (briefly showing
// the running indicator), then ToolApprovalRequestMsg overrides it
// with the approval prompt.
func TestIndicator_StartedThenApprovalShowsApprovalState(t *testing.T) {
	m := newTestAppModel(t)
	args := `{"url":"https://example.com/p"}`

	m = updateModel(t, m, toolCallStarted("webfetch", args))
	if m.workingState != StateToolRunning {
		t.Fatalf("after started: workingState = %v, want StateToolRunning", m.workingState)
	}
	if !strings.HasPrefix(m.workingDetail, "Reading page content") {
		t.Errorf("after started: detail = %q, want gerund describer prefix", m.workingDetail)
	}

	m = updateModel(t, m, approvalRequest("webfetch", "domain:example.com", args))
	if m.workingState != StateAwaitingApproval {
		t.Errorf("after approval msg: workingState = %v, want StateAwaitingApproval", m.workingState)
	}

	m = updateModel(t, m, keyPress("y"))
	if m.workingState != StateToolRunning {
		t.Errorf("after grant: workingState = %v, want StateToolRunning", m.workingState)
	}
}

// TestIndicator_PreApprovedToolEntersRunningDirectly ensures the gating
// in EventToolCallStarted ("only enter ToolRunning when m.pending is
// nil") doesn't break tools that were granted by a prior session/project
// rule and therefore never trigger ToolApprovalRequestMsg.
func TestIndicator_PreApprovedToolEntersRunningDirectly(t *testing.T) {
	m := newTestAppModel(t)
	args := `{"file_path":"main.go"}`

	m = updateModel(t, m, toolCallStarted("read", args))
	if m.workingState != StateToolRunning {
		t.Fatalf("pre-approved start: workingState = %v, want StateToolRunning", m.workingState)
	}
	if !strings.HasPrefix(m.workingDetail, "Reading main.go") {
		t.Errorf("pre-approved detail = %q, want \"Reading main.go\" prefix", m.workingDetail)
	}

	m = updateModel(t, m, toolCallDone("read", "ok"))
	if m.workingState != StateThinking {
		t.Errorf("pre-approved done: workingState = %v, want StateThinking", m.workingState)
	}
}

// TestIndicator_DenialReturnsToThinking ensures [n]/[esc] still routes
// back to StateThinking — only the allow paths (y/a/p/f) transition
// into StateToolRunning, since on denial nothing actually runs.
func TestIndicator_DenialReturnsToThinking(t *testing.T) {
	m := newTestAppModel(t)
	args := `{"url":"https://example.com/p"}`

	m = updateModel(t, m, approvalRequest("webfetch", "domain:example.com", args))
	if m.workingState != StateAwaitingApproval {
		t.Fatalf("after approval msg: workingState = %v, want StateAwaitingApproval", m.workingState)
	}
	m = updateModel(t, m, keyPress("n"))
	if m.workingState != StateThinking {
		t.Errorf("after denial: workingState = %v, want StateThinking", m.workingState)
	}
	if m.workingDetail != "" {
		t.Errorf("after denial: workingDetail = %q, want empty", m.workingDetail)
	}
}
