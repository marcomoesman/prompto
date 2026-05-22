package tui

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

// toolCallStartedID is the id-carrying variant of toolCallStarted: the
// approval-promotion path under test only kicks in when the Started
// event carries a non-empty ToolCallID.
func toolCallStartedID(id, name, args string) agentEventMsg {
	return agentEventMsg{event: agent.Event{
		Type:       agent.EventToolCallStarted,
		ToolCallID: id,
		ToolName:   name,
		ToolArgs:   args,
		ToolDisp:   name + "(" + args + ")",
	}}
}

// toolCallDoneID is the id-carrying variant of toolCallDone with a
// success summary attached, mirroring what the agent emits for a real
// completed call.
func toolCallDoneID(id, name, summary string) agentEventMsg {
	return agentEventMsg{event: agent.Event{
		Type:       agent.EventToolCallDone,
		ToolCallID: id,
		ToolName:   name,
		ToolResult: "...",
		ToolDisp:   summary,
	}}
}

// TestAppPairing_ApprovalThenStartedPromotesIDForParallelDispatch is the
// regression for the "summaries swap between parallel tool calls" bug.
//
// The race: ToolApprovalRequestMsg lands first (rendering an empty-id
// row keyed only by signature), then the matching EventToolCallStarted
// lands with the real id. The fix in EventToolCallStarted is to ALWAYS
// forward the event to chat.AppendToolCallWithOrigin so the chat's
// promotion path stamps the id onto the existing row. Skipping the
// forward when the signature already matched lastToolCallSig left the
// row unkeyed, and findToolCallRow's id miss caused parallel-dispatch
// completions to pair with the wrong call via the unfilled-row scan.
//
// This test drives both calls through approval then completes them in
// reverse order — which is the ordering that produced the visible swap.
func TestAppPairing_ApprovalThenStartedPromotesIDForParallelDispatch(t *testing.T) {
	m := newTestAppModel(t)
	m.chat.SetSize(200, 100)

	globArgs := `{"pattern":"**/*.go","path":"G:\\Go Workspace\\prompto"}`
	readArgs := `{"file_path":"G:\\Go Workspace\\prompto\\go.mod"}`

	// Glob: approval lands first, then the racing Started arrives with
	// the real id. The chat row must be promoted from empty-id to id-A.
	m = updateModel(t, m, approvalRequest("glob", "glob:**/*.go", globArgs))
	m = updateModel(t, m, toolCallStartedID("id-A", "glob", globArgs))
	m = updateModel(t, m, keyPress("y"))

	// Read: same race, different call. Promoted to id-B.
	m = updateModel(t, m, approvalRequest("read", "read:go.mod", readArgs))
	m = updateModel(t, m, toolCallStartedID("id-B", "read", readArgs))
	m = updateModel(t, m, keyPress("y"))

	// Both rows must now be id-keyed. If the bug returns, lastToolCallSig
	// blocks the promotion and the row keeps toolID == "".
	if _, ok := m.chat.toolCallIdx["id-A"]; !ok {
		t.Fatal("Glob row not promoted: id-A missing from toolCallIdx (promotion path was skipped)")
	}
	if _, ok := m.chat.toolCallIdx["id-B"]; !ok {
		t.Fatal("Read row not promoted: id-B missing from toolCallIdx (promotion path was skipped)")
	}

	// Completion arrives in reverse order — the agent dispatches read-only
	// calls in parallel after sequential approval, so Done events come
	// back in completion order, not call order. With the old findToolCallRow
	// fallback this is exactly when summaries swap.
	m = updateModel(t, m, toolCallDoneID("id-A", "glob", "260 files (showing 200)"))
	m = updateModel(t, m, toolCallDoneID("id-B", "read", "lines 1–30 of 87 · 4.0KB"))

	rendered := stripANSI(m.chat.viewport.View())

	// Assert each call header is followed by ITS OWN summary BEFORE the
	// next call header — i.e. summaries did not jump rows. The approval
	// path renders without a pre-formatted header, so summarizeArgs
	// produces the `glob **/*.go` / `read go.mod` form.
	globPos := strings.Index(rendered, "⚡ glob")
	readPos := strings.Index(rendered, "⚡ read")
	globSumPos := strings.Index(rendered, "260 files (showing 200)")
	readSumPos := strings.Index(rendered, "lines 1–30 of 87 · 4.0KB")
	if globPos < 0 || readPos < 0 || globSumPos < 0 || readSumPos < 0 {
		t.Fatalf("missing rendered fragments: glob=%d read=%d globSum=%d readSum=%d in:\n%s",
			globPos, readPos, globSumPos, readSumPos, rendered)
	}
	if globPos >= globSumPos || globSumPos >= readPos {
		t.Errorf("Glob summary not adjacent to Glob call (glob=%d sum=%d read=%d) — summaries swapped:\n%s",
			globPos, globSumPos, readPos, rendered)
	}
	if readPos >= readSumPos {
		t.Errorf("Read summary not after Read call (read=%d sum=%d) — summaries swapped:\n%s",
			readPos, readSumPos, rendered)
	}
}

// TestAppPairing_UnknownIDDoesNotMisattribute is the belt-and-braces
// regression for findToolCallRow: a Done event with a non-empty id
// that misses the index must drop on the floor rather than scan for
// "the most recent unfilled row" — the scan is what made the original
// bug silent. Loud (missing summary) beats wrong (mispaired summary).
func TestAppPairing_UnknownIDDoesNotMisattribute(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	c.AppendToolCall("id-1", "read", `{"file_path":"a.go"}`, `Read(file_path: "a.go")`)
	c.AppendToolCall("id-2", "read", `{"file_path":"b.go"}`, `Read(file_path: "b.go")`)

	// Result for an id that was never registered. Must NOT scan-fallback
	// onto either of the unfilled rows above.
	c.AppendToolResult("id-ghost", "read", "...", false, "PHANTOM SUMMARY")

	rendered := c.viewport.View()
	if strings.Contains(rendered, "PHANTOM SUMMARY") {
		t.Errorf("unknown-id summary leaked onto an unrelated row:\n%s", rendered)
	}
}
