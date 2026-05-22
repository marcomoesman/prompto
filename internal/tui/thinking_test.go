package tui

import (
	"strings"
	"testing"
)

// renderedText returns the thinking viewport's content with ANSI
// escape sequences stripped. The thinking renderer pipes the body
// through glamour, which wraps every word in colour escapes; raw
// substring assertions on the styled output miss matches because
// "first paragraph" becomes "first\x1b[…m paragraph". All thinking
// substring tests funnel through this helper.
func renderedText(m ThinkingModel) string {
	return stripANSI(m.viewport.View())
}

// TestThinking_CollapsesLongBlankRuns reproduces the "thinking
// overlay scrolls forever" behaviour: reasoning models often emit
// runs of \n\n\n\n between paragraphs, and accumulating them across
// turns stretches a few paragraphs over a full screen of empty
// space. The renderer must cap any run at one visible blank line.
func TestThinking_CollapsesLongBlankRuns(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	// Simulate three deltas with excessive padding: the first delta
	// has lots of leading blanks (which should be trimmed at top),
	// the second mid-content blank run (which should be capped), and
	// trailing blanks (which should be trimmed at bottom).
	m.AppendDelta("\n\n\n\n\n\nfirst paragraph")
	m.AppendDelta("\n\n\n\n\nsecond paragraph")
	m.AppendDelta("\n\n\n\n\n")

	rendered := renderedText(m)

	// No five-or-more-newline runs should survive after collapse.
	if strings.Contains(rendered, "\n\n\n\n\n") {
		t.Errorf("rendered output still contains 5+ consecutive newlines:\n%q", rendered)
	}
	// Top should not start with a blank line.
	body := strings.TrimRight(rendered, " \n")
	if strings.HasPrefix(body, "\n") {
		t.Errorf("rendered body should not start with a blank line: %q", body[:50])
	}
	// Both paragraphs must still appear.
	if !strings.Contains(rendered, "first paragraph") {
		t.Error("first paragraph missing from render")
	}
	if !strings.Contains(rendered, "second paragraph") {
		t.Error("second paragraph missing from render")
	}
}

// TestThinking_CollapsesWhitespaceOnlyLines covers the case the
// pure `\n{3,}` regex missed: reasoning models occasionally emit
// blank lines that contain trailing spaces or tabs (e.g.,
// `\n   \n   \n`), so a strict newline-only collapse leaves the
// gap visually intact even though it should be one blank line.
func TestThinking_CollapsesWhitespaceOnlyLines(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	// Three "blank" lines, two of which carry spaces/tabs.
	m.AppendDelta("para 1\n   \n\t\nspaces\npara 2")

	rendered := renderedText(m)

	// After collapse, between "para 1" and "spaces" there must be at
	// most ONE blank line — not three. We assert there's no run of
	// 3 newlines (with optional whitespace between) anywhere in the
	// rendered body.
	body := strings.TrimSpace(rendered)
	if thinkingBlankLineRun.MatchString(body) {
		t.Errorf("rendered body still contains a 3+ blank-line run after collapse:\n%q", body)
	}
	if !strings.Contains(rendered, "para 1") || !strings.Contains(rendered, "para 2") {
		t.Errorf("paragraphs missing after collapse:\n%s", rendered)
	}
}

// TestThinking_CollapsesUnicodeBlankLines covers the case the
// previous `[ \t]*` regex missed: reasoning models occasionally
// emit Unicode separators (NBSP, em-space, ideographic space) on
// "blank" lines, so a strict ASCII-only collapse left the gap
// untouched. The new `\p{Z}`-aware regex must fold those runs to
// one blank line just like ASCII space/tab runs.
func TestThinking_CollapsesUnicodeBlankLines(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	// Three "blank" lines containing NBSP (U+00A0), em-space
	// (U+2003), and ideographic space (U+3000) — a sample of the
	// separator family models actually emit.
	const nbsp = " "
	const emsp = " "
	const idsp = "　"
	m.AppendDelta("para 1\n" + nbsp + "\n" + emsp + "\n" + idsp + "\n" + nbsp + "\npara 2")

	rendered := renderedText(m)

	// At most one blank line between paragraphs after collapse —
	// we shouldn't see the model's original 5-newline stretch.
	if thinkingBlankLineRun.MatchString(strings.TrimSpace(rendered)) {
		t.Errorf("rendered body still contains a 3+ blank-line run with Unicode separators:\n%q", rendered)
	}
	if !strings.Contains(rendered, "para 1") || !strings.Contains(rendered, "para 2") {
		t.Errorf("paragraphs missing after collapse:\n%s", rendered)
	}
}

// TestThinking_CollapsesCRLFLineEndings covers \r\n (CRLF). Some
// providers terminate lines with CRLF on the wire; the renderer
// normalizes to \n before collapse so the blank-line regex works
// regardless of upstream endings.
func TestThinking_CollapsesCRLFLineEndings(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	m.AppendDelta("para 1\r\n\r\n\r\n\r\n\r\npara 2")
	rendered := renderedText(m)

	if strings.Count(rendered, "\r") != 0 {
		t.Errorf("expected \\r to be normalized away in render: %q", rendered)
	}
	if thinkingBlankLineRun.MatchString(strings.TrimSpace(rendered)) {
		t.Errorf("CRLF run survived collapse:\n%q", rendered)
	}
}

// TestThinking_CollapsesCompleteToolCallBlock covers the case where
// some open-weights models (Hermes / GLM / Kimi) emit pseudo-XML
// tool calls inside their reasoning channel even though the
// real tool call goes through the structured API. The overlay must
// collapse the noisy XML into a single marker line that still
// names the tool the model was reaching for.
func TestThinking_CollapsesCompleteToolCallBlock(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	body := "Let me search.\n" +
		"<tool_call>\n" +
		"<function=grep>\n" +
		"<parameter=pattern>\n" +
		"func dispatchSlash\n" +
		"</parameter>\n" +
		"<parameter=path>\n" +
		"/some/path\n" +
		"</parameter>\n" +
		"</function>\n" +
		"</tool_call>\n" +
		"That's the call I need."

	m.AppendDelta(body)
	rendered := renderedText(m)

	// XML markup must not be visible.
	for _, marker := range []string{"<tool_call>", "</tool_call>", "<function=", "<parameter=", "</parameter>"} {
		if strings.Contains(rendered, marker) {
			t.Errorf("XML marker %q still visible after collapse:\n%s", marker, rendered)
		}
	}
	// Collapsed marker must name the tool.
	if !strings.Contains(rendered, "tool call: grep") {
		t.Errorf("collapsed marker missing tool name:\n%s", rendered)
	}
	// Surrounding prose must survive.
	if !strings.Contains(rendered, "Let me search.") || !strings.Contains(rendered, "That's the call I need.") {
		t.Errorf("surrounding prose lost during collapse:\n%s", rendered)
	}
}

// TestThinking_CollapsesPartialStreamedToolCall covers the streaming
// case: deltas arrive incrementally, so a tool_call may be open
// (no closing tag yet) when render fires. The partial collapse
// should still hide the in-flight XML and show a "…" suffix so the
// user sees something is forming.
func TestThinking_CollapsesPartialStreamedToolCall(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	// Streaming chunks: opening tag arrives, then function name,
	// then partial parameters — but no closing </tool_call> yet.
	m.AppendDelta("Searching now.\n<tool_call>\n<function=grep>\n<parameter=pattern>\nfunc disp")
	rendered := renderedText(m)

	if strings.Contains(rendered, "<tool_call>") || strings.Contains(rendered, "<function=") {
		t.Errorf("partial-call XML still visible:\n%s", rendered)
	}
	if !strings.Contains(rendered, "tool call: grep…") {
		t.Errorf("partial-call marker missing or unmarked as in-flight:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Searching now.") {
		t.Errorf("preamble prose lost:\n%s", rendered)
	}
	if strings.Contains(m.viewport.View(), "[38;5;240m↳") {
		t.Errorf("raw ANSI style sequence leaked through markdown renderer:\n%s", m.viewport.View())
	}
}

// TestThinking_CollapsesMultipleToolCalls confirms the renderer
// handles back-to-back tool calls in a single reasoning pass.
func TestThinking_CollapsesMultipleToolCalls(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	body := "<tool_call><function=grep></function></tool_call>\n" +
		"intermediate thought\n" +
		"<tool_call><function=read></function></tool_call>"
	m.AppendDelta(body)
	rendered := renderedText(m)

	if !strings.Contains(rendered, "tool call: grep") {
		t.Errorf("first call marker missing:\n%s", rendered)
	}
	if !strings.Contains(rendered, "tool call: read") {
		t.Errorf("second call marker missing:\n%s", rendered)
	}
	if !strings.Contains(rendered, "intermediate thought") {
		t.Errorf("intermediate prose lost:\n%s", rendered)
	}
}

// TestThinking_RendersMarkdownNotRawSource is the regression for the
// "every block separated by a blank line" complaint. When the
// reasoning body is markdown-shaped (paragraph + fenced code +
// paragraph), the rendered overlay must NOT contain the raw `\n\n`
// runs that wedge a blank line between every block. Glamour renders
// the markdown into terminal-formatted output instead.
func TestThinking_RendersMarkdownNotRawSource(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	// Reasoning-shaped markdown: paragraph, code fence, paragraph.
	// In raw form this is 9 newlines; with glamour the rendered
	// output should have a tight code block and at most one blank
	// line between paragraphs.
	m.AppendDelta("First paragraph.\n\n```go\ncase agentEventMsg:\n    return cmd\n```\n\nSecond paragraph.")

	rendered := renderedText(m)

	// Code text must survive somewhere in the output.
	if !strings.Contains(rendered, "case agentEventMsg") {
		t.Fatalf("code block content missing — markdown render dropped fenced code:\n%s", rendered)
	}
	if !strings.Contains(rendered, "First paragraph") || !strings.Contains(rendered, "Second paragraph") {
		t.Fatalf("paragraph text missing from render:\n%s", rendered)
	}
	// The raw-markdown fence delimiter must NOT appear — glamour
	// converts ``` into a styled block, so seeing literal backticks
	// would mean the renderer was bypassed.
	if strings.Contains(rendered, "```") {
		t.Errorf("raw markdown fence delimiter `\\x60\\x60\\x60` survived; markdown was not rendered:\n%s", rendered)
	}
}

// TestThinking_DividerPaddingIsBounded covers the cross-turn case:
// turn 1 ends, MarkInactive fires, turn 2 starts. The divider
// between them must add at most one blank line on each side, even
// when the surrounding deltas already brought their own blank lines.
func TestThinking_DividerPaddingIsBounded(t *testing.T) {
	m := NewThinkingModel()
	m.SetSize(120, 40)

	m.AppendDelta("turn one body\n\n\n")
	m.MarkInactive()
	m.AppendDelta("\n\n\nturn two body")

	rendered := renderedText(m)

	// The divider text identifies turn boundary. Around it should be
	// at most one blank line on each side, never the four-plus runs
	// the user reported.
	dividerIdx := strings.Index(rendered, "new thinking")
	if dividerIdx < 0 {
		t.Fatalf("divider missing from render:\n%s", rendered)
	}
	// Look at the 30 chars before and after the divider — count
	// consecutive newlines next to it.
	prefix := rendered[:dividerIdx]
	suffix := rendered[dividerIdx:]

	// Trailing newlines on prefix
	leading := 0
	for i := len(prefix) - 1; i >= 0 && prefix[i] == '\n'; i-- {
		leading++
	}
	if leading > 2 {
		t.Errorf("divider has %d leading newlines, want ≤ 2 (one blank line)", leading)
	}
	// Find first \n after divider line, then count consecutive
	if nl := strings.Index(suffix, "\n"); nl >= 0 {
		trailing := 0
		for i := nl; i < len(suffix) && suffix[i] == '\n'; i++ {
			trailing++
		}
		if trailing > 2 {
			t.Errorf("divider has %d trailing newlines, want ≤ 2 (one blank line)", trailing)
		}
	}
}
