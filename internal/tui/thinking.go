package tui

import (
	"regexp"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
)

// thinkingBlankLineRun matches three or more consecutive "logical
// blank lines" — a newline followed by any amount of inline
// whitespace (ASCII space/tab/CR/FF/VT plus Unicode separators like
// NBSP / em-space, which reasoning models emit surprisingly often).
// Capped at one visible blank line, leaving normal paragraph
// spacing intact. Glamour does most of the work; this is a belt-
// and-braces step for the absurdly long runs that occasionally
// appear in tool_call-adjacent reasoning bursts.
var thinkingBlankLineRun = regexp.MustCompile(`(?:\n[\p{Z}\t\r\f\v]*){3,}`)

// thinkingDividerSentinel is the NUL-bracketed marker AppendDelta
// stamps into the content buffer in place of the styled divider.
// render() expands it back to "\n\n<styled divider>\n\n" AFTER the
// blank-line collapse runs, so the divider keeps its visual
// breathing room while every other blank line is removed.
const thinkingDividerSentinel = "\x00THINKING_DIVIDER\x00"

// thinkingToolCallBlock matches a complete <tool_call>…</tool_call>
// envelope (Hermes / GLM / Kimi convention) so the overlay can
// collapse the multi-line XML into a single one-line marker. The
// block is emitted by some open-weights models inside their
// reasoning channel even though the actual tool call goes through
// the structured API — left raw it dominates the overlay with
// </parameter>, </function>, </tool_call> noise.
var thinkingToolCallBlock = regexp.MustCompile(`(?s)<tool_call>(.*?)</tool_call>`)

// thinkingFunctionName extracts the name from `<function=NAME>`
// inside a tool_call block so the collapsed marker can identify
// which tool the model was reaching for.
var thinkingFunctionName = regexp.MustCompile(`<function=([^>\s]+)>`)

// thinkingPartialToolCall matches an UNCLOSED <tool_call> that runs
// to the end of the buffer — the streaming case. Without this, the
// XML preamble is fully visible until the closing tag arrives. Only
// fires when no </tool_call> has been seen after a <tool_call>.
var thinkingPartialToolCall = regexp.MustCompile(`(?s)<tool_call>(.*)$`)

// ThinkingModel renders the extended-thinking overlay. The chat region is
// replaced by this view while m.thinkingVisible is true; otherwise the
// model still receives deltas in the background so opening the overlay
// shows the full reasoning trail accumulated so far.
type ThinkingModel struct {
	viewport viewport.Model
	// content accumulates every thinking delta emitted in this session,
	// with turn separators inserted between bursts. Plain string (not
	// strings.Builder) because AppModel — and therefore this struct — is
	// copied each Update tick by Bubbletea's value-receiver pattern;
	// strings.Builder forbids copy after first write.
	content string
	// active is true when the last appended event was a thinking delta.
	// Used to decide whether the next delta starts a new turn block (and
	// therefore needs a separator above it).
	active bool
	// renderer applies markdown formatting to reasoning text. Reasoning
	// models emit dense markdown — paragraphs, code fences, lists — and
	// without a real markdown pass the overlay shows raw `\n\n`-padded
	// source which looks like one blank line per content row. Glamour
	// converts the markdown into block-aware terminal output: code
	// fences inline their contents, paragraphs get appropriate spacing,
	// lists tighten up. Recreated by SetSize / SetDark when the
	// terminal dimensions or theme change.
	renderer *glamour.TermRenderer
	dark     bool
	width    int
	height   int
}

// NewThinkingModel constructs an empty thinking overlay.
func NewThinkingModel() ThinkingModel {
	vp := viewport.New()
	vp.SoftWrap = true
	return ThinkingModel{viewport: vp, dark: true}
}

// Update forwards messages to the viewport (mouse wheel, key paging).
func (m ThinkingModel) Update(msg tea.Msg) (ThinkingModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// SetSize updates viewport dimensions and re-renders. Reserves one row
// for the header and one for the footer hint. Also rebuilds the
// glamour renderer so markdown wraps to the new width.
func (m *ThinkingModel) SetSize(width, height int) {
	m.width = width
	m.height = height
	bodyHeight := height - 2 // header + footer
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(bodyHeight)
	m.renderer = newThinkingRenderer(width, m.dark)
	m.render()
}

// SetDark toggles the theme used by the markdown renderer. Mirrors
// ChatModel.SetDark — the chat passes the terminal's reported
// background colour through both panels so light- and dark-themed
// terminals get the right glamour palette.
func (m *ThinkingModel) SetDark(dark bool) {
	if m.dark == dark && m.renderer != nil {
		return
	}
	m.dark = dark
	if m.width > 0 {
		m.renderer = newThinkingRenderer(m.width, dark)
	}
	m.render()
}

// newThinkingRenderer constructs a glamour renderer sized for the
// overlay. Errors are swallowed: a nil renderer falls back to plain
// text in render().
func newThinkingRenderer(width int, dark bool) *glamour.TermRenderer {
	style := "light"
	if dark {
		style = "dark"
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStylePath(style),
		glamour.WithWordWrap(width),
	)
	return r
}

// AppendDelta appends one streamed thinking chunk to the content buffer.
// On a transition from non-thinking → thinking we emit a faint divider
// so successive turns are visually separable. The buffer is right-
// trimmed before the divider is appended (and the leading whitespace
// of the next delta is dropped) so the divider hugs its surrounding
// content regardless of what trailing/leading whitespace the model
// happened to stream.
func (m *ThinkingModel) AppendDelta(delta string) {
	if delta == "" {
		return
	}
	if !m.active && m.content != "" {
		m.content = strings.TrimRightFunc(m.content, unicode.IsSpace)
		// Stamp a sentinel rather than the styled divider directly:
		// the blank-line collapse in render() would otherwise eat the
		// surrounding "\n\n" and slam the divider against neighbouring
		// content. The sentinel survives the collapse; render()
		// re-expands it with breathing room intact.
		m.content += thinkingDividerSentinel
		delta = strings.TrimLeftFunc(delta, unicode.IsSpace)
	}
	m.active = true
	m.content += delta
	m.render()
}

// MarkInactive flips the streaming state to "not currently thinking",
// so the next AppendDelta inserts a turn divider. Called by AppModel
// on tool-call/turn-complete events.
func (m *ThinkingModel) MarkInactive() {
	m.active = false
}

// HasContent reports whether any thinking has been recorded yet.
func (m *ThinkingModel) HasContent() bool {
	return m.content != ""
}

// View renders the overlay: header row, scrollable viewport, footer hint.
// Sized to fill width × height as set by SetSize.
func (m ThinkingModel) View() string {
	header := thinkingHeaderStyle.Render("Thinking")
	footer := thinkingFooterStyle.Render("Ctrl+O / ESC closes  ·  ↑↓ PgUp PgDn scroll")
	return lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View(), footer)
}

func (m *ThinkingModel) render() {
	body := m.content
	if body == "" {
		m.viewport.SetContent(thinkingEmptyStyle.Render("(no extended thinking yet — the assistant hasn't reasoned out loud this session)"))
		m.viewport.GotoBottom()
		return
	}

	// 1. Normalize line endings. Some providers emit \r\n on
	//    Windows-side servers; the markdown renderer chokes on
	//    \r if left in.
	body = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(body)
	// 2. Collapse complete tool_call envelopes (Hermes / GLM /
	//    Kimi) to a one-liner marker. Done before glamour so the
	//    pseudo-XML doesn't end up in a code fence and dominate
	//    the overlay.
	body = collapseThinkingToolCalls(body)
	// 3. Belt-and-braces collapse of any 3+ blank-line runs. The
	//    markdown renderer would normalise paragraph spacing on
	//    its own, but reasoning models occasionally emit absurdly
	//    long blank stretches (especially in tool_call gaps) that
	//    glamour preserves as multiple paragraph breaks.
	body = thinkingBlankLineRun.ReplaceAllString(body, "\n\n")
	// 4. Trim top/bottom whitespace so the first turn starts at
	//    the top.
	body = strings.TrimSpace(body)

	// 5. Render markdown via glamour when available. The reasoning
	//    text is markdown source; rendering it gives proper code
	//    fences, paragraph spacing, and list layout instead of the
	//    raw `\n\n`-padded source the user otherwise sees.
	//
	//    Split the body on the divider sentinel BEFORE glamour and
	//    render each segment as its own markdown document; glamour
	//    can't be trusted to round-trip the NUL bytes the sentinel
	//    uses, and we want the divider styled with our own glyph
	//    rather than as a markdown HR. The styled divider is
	//    re-injected between segments after rendering.
	segments := strings.Split(body, thinkingDividerSentinel)
	for i, seg := range segments {
		if m.renderer == nil {
			continue
		}
		if out, err := m.renderer.Render(seg); err == nil {
			segments[i] = strings.TrimRight(out, "\n")
		}
	}
	divider := thinkingDividerStyle.Render("─── new thinking ───")
	rendered := strings.Join(segments, "\n\n"+divider+"\n\n")

	m.viewport.SetContent(rendered)
	m.viewport.GotoBottom()
}

// collapseThinkingToolCalls replaces every <tool_call>…</tool_call>
// envelope (and a trailing unclosed one if present) with a single
// dim "↳ tool call: NAME" line. NAME is read from <function=…> when
// present; falls back to "tool" otherwise. Idempotent — re-running
// on already-collapsed content is a no-op.
func collapseThinkingToolCalls(s string) string {
	s = thinkingToolCallBlock.ReplaceAllStringFunc(s, func(match string) string {
		return formatCollapsedToolCall(match, false)
	})
	// After complete blocks are collapsed, anything matching the
	// partial-call regex is necessarily a streaming-in-flight call
	// (no closing tag yet). Render it tentatively so the user knows
	// a tool call is being formed.
	s = thinkingPartialToolCall.ReplaceAllStringFunc(s, func(match string) string {
		return formatCollapsedToolCall(match, true)
	})
	return s
}

// formatCollapsedToolCall renders the collapsed marker line. partial
// indicates the closing </tool_call> hasn't arrived yet — append a
// trailing ellipsis so the streaming state is obvious.
func formatCollapsedToolCall(block string, partial bool) string {
	name := "tool"
	if mm := thinkingFunctionName.FindStringSubmatch(block); mm != nil {
		name = mm[1]
	}
	suffix := ""
	if partial {
		suffix = "…"
	}
	return "↳ tool call: " + name + suffix + "\n"
}
