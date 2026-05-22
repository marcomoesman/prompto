package tui

import (
	"regexp"
	"strings"
	"testing"
)

// TestChat_ParallelDispatchPairsByID is the regression for the
// "results stack at the bottom" rendering bug. The agent dispatches
// concurrency-safe tools in parallel: every EventToolCallStarted
// fires before any EventToolCallDone, and Done events arrive in
// completion order, not call order. Pairing-by-id must keep each
// summary under its owning call regardless.
func TestChat_ParallelDispatchPairsByID(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	// Step 1: planner emits four Started events back-to-back.
	c.AppendToolCall("id-A", "read", `{"file_path":"a.go"}`, `Read(file_path: "a.go")`)
	c.AppendToolCall("id-B", "read", `{"file_path":"b.go"}`, `Read(file_path: "b.go")`)
	c.AppendToolCall("id-C", "read", `{"file_path":"c.go"}`, `Read(file_path: "c.go")`)
	c.AppendToolCall("id-D", "read", `{"file_path":"d.go"}`, `Read(file_path: "d.go")`)

	// Step 2: tools execute concurrently. Done events arrive in
	// reverse / interleaved order to mimic real concurrent completion.
	c.AppendToolResult("id-D", "read", "...", false, "124 lines · 4.9KB")
	c.AppendToolResult("id-A", "read", "...", false, "lines 1–80 of 87 · 2.0KB")
	c.AppendToolResult("id-C", "read", "...", false, "lines 1–150 of 246 · 6.4KB")
	c.AppendToolResult("id-B", "read", "...", false, "lines 1–150 of 381 · 9.3KB")

	// Each call row should now own its summary in line order:
	//   read a.go → 80/87
	//   read b.go → 150/381
	//   read c.go → 150/246
	//   read d.go → 124 lines
	wantPairs := []struct {
		callContains    string
		summaryContains string
	}{
		{"a.go", "lines 1–80 of 87 · 2.0KB"},
		{"b.go", "lines 1–150 of 381 · 9.3KB"},
		{"c.go", "lines 1–150 of 246 · 6.4KB"},
		{"d.go", "124 lines · 4.9KB"},
	}

	// Walk the rendered viewport content and assert call/summary
	// adjacency rather than walking m.messages — this catches both
	// data-model bugs (wrong row mutated) and renderer bugs (summary
	// emitted in the wrong place).
	rendered := c.viewport.View()
	pos := 0
	for _, w := range wantPairs {
		callIdx := strings.Index(rendered[pos:], w.callContains)
		if callIdx < 0 {
			t.Fatalf("call %q not found after byte %d in:\n%s", w.callContains, pos, rendered)
		}
		callIdx += pos
		sumIdx := strings.Index(rendered[callIdx:], w.summaryContains)
		if sumIdx < 0 {
			t.Fatalf("summary %q not found after call %q in:\n%s", w.summaryContains, w.callContains, rendered)
		}
		sumIdx += callIdx
		// No other pair's call header should sit between this call and
		// its summary. (That's the precise bug the screenshot showed.)
		for _, other := range wantPairs {
			if other.callContains == w.callContains {
				continue
			}
			if intervening := strings.Index(rendered[callIdx+1:sumIdx], other.callContains); intervening >= 0 {
				t.Errorf("call %q and its summary are split by call %q in render:\n%s",
					w.callContains, other.callContains, rendered)
			}
		}
		pos = sumIdx
	}
}

// TestChat_ApprovalPathPromotesIDOnStarted covers the dual-render
// path: the approval prompt rendered the call row first (with no
// id), then the racing EventToolCallStarted arrived with the real
// id. Promotion must update the existing row instead of appending a
// duplicate, so the result still finds its owner by id.
func TestChat_ApprovalPathPromotesIDOnStarted(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	// Approval-path render — no id yet.
	c.AppendToolCall("", "bash", `{"command":"go test ./..."}`, `Bash(command: "go test ./...")`)
	if got := countToolCallRows(&c); got != 1 {
		t.Fatalf("after approval render: rows = %d, want 1", got)
	}

	// EventToolCallStarted arrives with the real id and matching
	// signature. Must promote, not duplicate.
	c.AppendToolCall("id-bash-1", "bash", `{"command":"go test ./..."}`, `Bash(command: "go test ./...")`)
	if got := countToolCallRows(&c); got != 1 {
		t.Errorf("after Started promotion: rows = %d, want 1 (no duplicate)", got)
	}

	c.AppendToolResult("id-bash-1", "bash", "PASS", false, "ran 12 tests in 0.4s")
	rendered := c.viewport.View()
	if !strings.Contains(rendered, "ran 12 tests in 0.4s") {
		t.Errorf("summary missing from render after promotion:\n%s", rendered)
	}
}

func TestChat_SubagentToolCallUsesLightBlue(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	c.AppendToolCallWithOrigin("", "read", `{"file_path":"a.go"}`, `Read(file_path: "a.go")`, "explore")
	if len(c.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(c.messages))
	}
	if !strings.Contains(c.messages[0].content, "[1;38;5;75m") {
		t.Fatalf("subagent tool call should use bold light-blue (256-color 75) ANSI color, got %q", c.messages[0].content)
	}
	if strings.Contains(c.messages[0].content, "[1;93m") {
		t.Fatalf("subagent tool call used primary yellow color, got %q", c.messages[0].content)
	}
	if !strings.Contains(c.messages[0].content, "Explore → ") {
		t.Fatalf("subagent tool call missing %q prefix, got %q", "Explore → ", c.messages[0].content)
	}
}

// TestChat_ResultErrorAttachesToOwningCall ensures the error path
// also pairs by id — error blocks must not leak across rows.
func TestChat_ResultErrorAttachesToOwningCall(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)
	c.AppendToolCall("id-1", "read", `{"file_path":"ok.go"}`, `Read(file_path: "ok.go")`)
	c.AppendToolCall("id-2", "read", `{"file_path":"missing.go"}`, `Read(file_path: "missing.go")`)

	c.AppendToolResult("id-2", "read", "no such file: missing.go", true, "")
	c.AppendToolResult("id-1", "read", "...", false, "12 lines · 0.3KB")

	rendered := c.viewport.View()
	// Expected layout in render order:
	//   ⚡ Read ok.go
	//     └ 12 lines · 0.3KB    ← id-1's success summary
	//   ⚡ Read missing.go
	//     no such file: ...     ← id-2's error block
	id1Pos := strings.Index(rendered, "ok.go")
	id2Pos := strings.Index(rendered, "missing.go")
	errPos := strings.Index(rendered, "no such file")
	sumPos := strings.Index(rendered, "12 lines")
	if id1Pos < 0 || id2Pos < 0 || errPos < 0 || sumPos < 0 {
		t.Fatalf("missing rendered fragments in:\n%s", rendered)
	}
	// id-1's summary must sit between id-1 and id-2 (not pushed
	// below the second call as it would have been with the old
	// "append-only" logic).
	if id1Pos >= sumPos || sumPos >= id2Pos {
		t.Errorf("id-1 summary not adjacent to its call (id1=%d sum=%d id2=%d):\n%s",
			id1Pos, sumPos, id2Pos, rendered)
	}
	// id-2's error must follow id-2 (and therefore the id-1 summary).
	if id2Pos >= errPos {
		t.Errorf("id-2 error block not after its owning call (id2=%d err=%d):\n%s",
			id2Pos, errPos, rendered)
	}
}

// TestChat_SystemHeaderOnSystemBlock verifies that consecutive
// system messages render under a single bold "System" header,
// matching the You / Assistant / Tools convention. /context,
// /model, error notices, and any other AppendSystemMessage caller
// land in this block.
func TestChat_SystemHeaderOnSystemBlock(t *testing.T) {
	c := NewChatModel()
	c.SetSize(120, 40)

	c.AppendUserMessage("show me /context")
	c.AppendSystemMessage("conversation: 12 messages, ~3.4k tokens")
	c.AppendSystemMessage("system prompt: 1.2k tokens")

	rendered := stripANSI(c.viewport.View())

	if !strings.Contains(rendered, "System") {
		t.Errorf("expected bold System header before system messages:\n%s", rendered)
	}
	// Consecutive system messages share a header — "System" should
	// appear exactly once before the run.
	if n := countHeaders(rendered, "System"); n != 1 {
		t.Errorf("expected exactly 1 System header for consecutive system messages, got %d:\n%s", n, rendered)
	}
	for _, want := range []string{"conversation: 12", "system prompt: 1.2k"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing system message %q in render:\n%s", want, rendered)
		}
	}
}

// TestChat_SystemHeaderRepeatsAfterDifferentBlock checks the
// grouping invariant: a system message that follows a non-system
// block (a user reply, for instance) gets its own fresh "System"
// header, not piggybacking on a previous one.
func TestChat_SystemHeaderRepeatsAfterDifferentBlock(t *testing.T) {
	c := NewChatModel()
	c.SetSize(120, 40)

	c.AppendSystemMessage("model: qwopus-36-27b")
	c.AppendUserMessage("now use claude")
	c.AppendSystemMessage("model: anthropic/claude-sonnet-4.5")

	rendered := stripANSI(c.viewport.View())

	if n := countHeaders(rendered, "System"); n != 2 {
		t.Errorf("expected 2 System headers (one before each contiguous run), got %d:\n%s", n, rendered)
	}
}

func TestChat_SystemMarkdownRendersFormatting(t *testing.T) {
	c := NewChatModel()
	c.SetSize(120, 40)

	c.AppendSystemMarkdown("**MIT**\n\n- github.com/example/lib v1.0.0")

	rendered := c.viewport.View()
	plain := stripANSI(rendered)
	if strings.Contains(plain, "**MIT**") {
		t.Fatalf("raw markdown header survived; system markdown was not rendered:\n%s", plain)
	}
	if !strings.Contains(plain, "MIT") || !strings.Contains(plain, "github.com/example/lib") {
		t.Fatalf("rendered markdown missing expected content:\n%s", plain)
	}
}

// stripANSI removes terminal escape sequences so plain-text
// assertions don't have to dance around lipgloss's styling.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// TestChat_WhitespaceOnlyDeltaSuppressesAssistantHeader is the regression
// for OpenRouter Kimi/GLM turns that begin with a "\n" or " " content
// delta before pure-tool-call output. Persisting the whitespace as an
// assistant message rendered an empty "Assistant" header above each tool
// batch.
func TestChat_WhitespaceOnlyDeltaSuppressesAssistantHeader(t *testing.T) {
	c := NewChatModel()
	c.SetSize(200, 100)

	c.AppendUserMessage("hello")

	// Turn 1: leading "\n" delta, then tool calls. The whitespace
	// must NOT seed an assistant message.
	c.AppendDelta("\n")
	c.AppendToolCall("id-1", "glob", `{"pattern":"**/*"}`, `Glob(pattern: "**/*")`)
	c.AppendToolResult("id-1", "glob", "files...", false, "270 files")

	// Turn 2: another whitespace-only delta, more tool calls.
	c.AppendDelta(" ")
	c.AppendToolCall("id-2", "read", `{"file_path":"x"}`, `Read(file_path: "x")`)
	c.AppendToolResult("id-2", "read", "...", false, "10 lines")

	// Turn 3: real text — should still produce ONE Assistant header.
	c.AppendDelta("done.")
	c.FinishStreaming()

	rendered := stripANSI(c.viewport.View())
	if got := countHeaders(rendered, "Assistant"); got != 1 {
		t.Errorf("expected 1 Assistant header (only the final text turn), got %d:\n%s", got, rendered)
	}
}

// countHeaders counts how many lines start with `name` followed by
// either end-of-line or trailing whitespace (lipgloss pads viewport
// header lines to the panel width). Matches block headers like
// "System", "Tools", "You", "Assistant".
func countHeaders(rendered, name string) int {
	n := 0
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == name {
			n++
		}
	}
	return n
}

// countToolCallRows is a test helper.
func countToolCallRows(c *ChatModel) int {
	n := 0
	for _, m := range c.messages {
		if m.role == "tool_call" {
			n++
		}
	}
	return n
}
