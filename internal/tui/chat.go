package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/diff"
)

type chatMessage struct {
	role     api.Role // also "tool_call", "approval" as display-only roles
	content  string
	markdown bool

	// Tool-call-specific fields. Populated only when role == "tool_call".
	// The call row OWNS its result: when EventToolCallDone arrives,
	// AppendToolResult finds the matching row by toolID and mutates
	// summary / errBody in place. This keeps result lines beneath
	// their owning call regardless of the order in which Done events
	// arrive (parallel dispatch, retries, etc.).
	toolID   string // provider tool_use_id; "" until promoted by EventToolCallStarted
	toolSig  string // (name, args) fallback key used while toolID is empty
	subagent string // spawning subagent name (e.g. "explore"); empty for primary
	summary  string // raw success summary text (e.g. "lines 1–80 of 578 · 19.2KB");
	// stored unstyled so render() can both apply the dim style AND
	// parse it for collapsing runs of same-file Reads. Empty until
	// the matching success result arrives.
	errBody string // formatted error block; populated on isError result
	// collapseKey identifies same-target tool calls eligible for run
	// merging at render time. Populated at append time for Read calls
	// with a file_path arg ("read:<path>"); empty for any other tool
	// or when arg parsing fails — in which case the row renders as a
	// standalone entry.
	collapseKey string
}

// ChatModel displays the message history in a scrollable viewport.
type ChatModel struct {
	viewport viewport.Model
	renderer *glamour.TermRenderer
	messages []chatMessage
	// toolCallIdx maps a tool_use_id to its message index in
	// m.messages. AppendToolResult uses it to locate the call row
	// without scanning. Cleared/repopulated only when a row is
	// appended; results just mutate the row, no remap needed.
	toolCallIdx map[string]int
	streaming   string // current streaming text (incomplete assistant message)
	width       int
	height      int
	dark        bool // true = dark terminal background (default)
}

// NewChatModel creates a new chat viewport with markdown rendering.
func NewChatModel() ChatModel {
	vp := viewport.New()
	vp.SoftWrap = true
	return ChatModel{viewport: vp, dark: true, toolCallIdx: map[string]int{}}
}

func newRenderer(width int, dark bool) *glamour.TermRenderer {
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

// Update passes messages to the viewport.
func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// View renders the viewport.
func (m ChatModel) View() string {
	return m.viewport.View()
}

// AppendUserMessage adds a finalized user message.
func (m *ChatModel) AppendUserMessage(text string) {
	m.messages = append(m.messages, chatMessage{role: api.RoleUser, content: text})
	m.render()
}

// AppendSystemMessage adds a system-level message (e.g. "Press Ctrl+C again to exit").
func (m *ChatModel) AppendSystemMessage(text string) {
	m.messages = append(m.messages, chatMessage{role: api.RoleSystem, content: text})
	m.render()
}

// AppendSystemMarkdown adds a system-level message rendered through the
// markdown renderer. Plain system messages remain unparsed so pre-styled
// errors and status text never leak ANSI escape sequences through glamour.
func (m *ChatModel) AppendSystemMarkdown(text string) {
	m.messages = append(m.messages, chatMessage{role: api.RoleSystem, content: text, markdown: true})
	m.render()
}

// AppendDelta appends streaming text to the current assistant response.
func (m *ChatModel) AppendDelta(delta string) {
	m.streaming += delta
	m.render()
}

// FinishStreaming finalizes the streaming buffer as a complete assistant message.
// Whitespace-only buffers are discarded: some open-weights models (Kimi, GLM via
// OpenRouter) emit a leading "\n" or " " content delta before a pure-tool-call
// turn. Persisting those would render as an empty "Assistant" header above
// each tool batch.
func (m *ChatModel) FinishStreaming() {
	if strings.TrimSpace(m.streaming) != "" {
		m.messages = append(m.messages, chatMessage{role: api.RoleAssistant, content: m.streaming})
	}
	if m.streaming != "" {
		m.streaming = ""
		m.render()
	}
}

// AppendToolCall adds the tool-call display row. Always rendered for every
// tool invocation (project-allowed, session-allowed, or asked) so the user
// sees what the model is doing regardless of permission resolution. The
// approval prompt is appended separately via AppendApprovalPrompt.
//
// toolID is the provider tool_use_id. When non-empty it becomes the
// primary correlation key — AppendToolResult locates the row by it.
// When empty (e.g. the ToolApprovalRequestMsg path which doesn't have
// the id yet, or resume-path messages with no id), the row is keyed
// only on its (name, args) signature; a later AppendToolCall with a
// real id and matching signature promotes the row instead of appending
// a duplicate.
//
// header is the pre-formatted call string (FormatForDisplay output). When
// empty the chat falls back to its legacy summarizer — kept for tests
// and any future callers that don't have the tool resolver handy.
func (m *ChatModel) AppendToolCall(toolID, name, argsJSON, header string) {
	m.AppendToolCallWithOrigin(toolID, name, argsJSON, header, "")
}

// AppendToolCallWithOrigin is AppendToolCall plus the spawning subagent's
// name (empty for primary). When non-empty the row is styled in the
// subagent color and prefixed with "<AgentName> → " so the user can tell
// which child issued the call.
func (m *ChatModel) AppendToolCallWithOrigin(toolID, name, argsJSON, header, subagent string) {
	// Finalize any in-progress streaming text first. Whitespace-only buffers
	// are dropped — see FinishStreaming for the rationale.
	if strings.TrimSpace(m.streaming) != "" {
		m.messages = append(m.messages, chatMessage{role: api.RoleAssistant, content: m.streaming})
	}
	m.streaming = ""

	sig := name + "\x00" + argsJSON

	// If a row with this id already exists (rare — duplicate Started
	// events), skip rather than duplicate.
	if toolID != "" {
		if _, ok := m.toolCallIdx[toolID]; ok {
			m.render()
			return
		}
	}

	// Promotion: an earlier AppendToolCall (typically the approval-path
	// render) created a row with this signature but no id yet. Stamp
	// the id onto it instead of appending a second row.
	if toolID != "" {
		for idx := len(m.messages) - 1; idx >= 0; idx-- {
			row := m.messages[idx]
			if row.role != "tool_call" {
				continue
			}
			if row.toolID == "" && row.toolSig == sig {
				m.messages[idx].toolID = toolID
				if m.messages[idx].subagent == "" {
					m.messages[idx].subagent = subagent
				}
				m.toolCallIdx[toolID] = idx
				m.render()
				return
			}
		}
	}

	display := formatToolCallRow(name, argsJSON, header, subagent, m.width)
	m.messages = append(m.messages, chatMessage{
		role:        "tool_call",
		content:     display,
		toolID:      toolID,
		toolSig:     sig,
		subagent:    subagent,
		collapseKey: computeCollapseKey(name, argsJSON),
	})
	if toolID != "" {
		m.toolCallIdx[toolID] = len(m.messages) - 1
	}
	m.render()
}

// AppendApprovalPrompt adds the inline approval prompt row beneath the most
// recent tool call. When isReadOnly is true the prompt offers an extra
// `[f] all files` option that installs a project-scope wildcard rule —
// only safe for tools that never modify state, AND only for primary-agent
// calls (subagent permissions are scoped to the child's lifetime, so a
// project-wide wildcard would outlive its useful blast radius and pollute
// the project ruleset). subagent is non-empty for a child run and swaps the
// optional `[s]` row in for an "allow all for this subagent" grant covering
// every remaining call this subagent makes.
func (m *ChatModel) AppendApprovalPrompt(isReadOnly bool, subagent string) {
	prompt := approvalStyle.Render("  Allow?") + " " +
		approvalStyle.Render("[y]") + " once  " +
		approvalStyle.Render("[a]") + " session  " +
		approvalStyle.Render("[p]") + " project  "
	if subagent != "" {
		prompt += approvalStyle.Render("[s]") + " allow all for this subagent  "
	} else if isReadOnly {
		prompt += approvalStyle.Render("[f]") + " all files  "
	}
	prompt += approvalStyle.Render("[n]") + " no"
	m.messages = append(m.messages, chatMessage{role: "approval", content: prompt})
	m.render()
}

// AppendSubagentHeartbeat renders the dim end-of-step line emitted by a
// subagent ("Research · 3 calls so far · last: WebFetch(...)"). The
// subagent name is normalised to its display form (Explore, Research)
// and styled in the subagent color so the line scans visually as
// belonging to the same child as its tool-call rows above. Empty body
// is rendered as a bare name line so the user still sees the child
// ticked over.
func (m *ChatModel) AppendSubagentHeartbeat(subagent, body string) {
	name := taskDisplayName(subagent)
	body = strings.TrimSpace(body)
	line := toolSubagentStyle.Render("    ↳ "+name) + " " + dimStyle.Render(body)
	if body == "" {
		line = toolSubagentStyle.Render("    ↳ " + name)
	}
	m.messages = append(m.messages, chatMessage{role: api.RoleSystem, content: line})
	m.render()
}

// ResolveApproval removes the approval prompt and shows whether it was approved.
func (m *ChatModel) ResolveApproval(approved bool) {
	// Remove the trailing "approval" message
	if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "approval" {
		m.messages = m.messages[:len(m.messages)-1]
	}
	if approved {
		m.messages = append(m.messages, chatMessage{role: "approval", content: dimStyle.Render("  ✓ approved")})
	} else {
		m.messages = append(m.messages, chatMessage{role: "approval", content: errorStyle.Render("  ✗ denied")})
	}
	m.render()
}

// AppendToolResult attaches a tool's outcome to its owning call row.
// On success, a dim `└ <summary>` line renders beneath the call (when
// summary is non-empty); on error, a red multi-line block does. The
// row is located by toolID when non-empty, falling back to "the most
// recent tool_call row whose result is still unfilled" — which keeps
// the resume-path and synthetic-event tests working without piping ids.
//
// Concurrent dispatch is the reason this is id-keyed: when the agent
// runs N read-only calls in parallel, all N Started events arrive
// before any Done event, so the previous "append result as a separate
// message" rule placed every summary at the bottom of the batch
// instead of next to its owner.
func (m *ChatModel) AppendToolResult(toolID, name, result string, isError bool, summary string) {
	idx := m.findToolCallRow(toolID)
	if idx < 0 {
		return
	}
	if isError {
		m.messages[idx].errBody = formatToolResult(name, result, isError)
	} else if summary != "" {
		// Store raw — render() applies dimStyle + "    └ " framing so
		// collapse logic can also inspect the unstyled text without
		// stripping ANSI off a pre-rendered string.
		m.messages[idx].summary = summary
	}
	m.render()
}

// findToolCallRow returns the index of the call row matching toolID,
// or — when toolID is empty — the index of the most recent tool_call
// row whose result is still unfilled. Returns -1 when no row matches.
//
// A non-empty toolID that misses the index returns -1 rather than
// scanning: a parallel-dispatch batch with multiple unfilled rows
// would misattribute via the scan, and a silent misattribution is
// worse than a dropped summary. The id-keyed path is the contract;
// any miss is a pairing bug to fix at the source.
func (m *ChatModel) findToolCallRow(toolID string) int {
	if toolID != "" {
		if idx, ok := m.toolCallIdx[toolID]; ok {
			return idx
		}
		return -1
	}
	for idx := len(m.messages) - 1; idx >= 0; idx-- {
		row := m.messages[idx]
		if row.role != "tool_call" {
			continue
		}
		if row.summary == "" && row.errBody == "" {
			return idx
		}
	}
	return -1
}

// SetSize updates viewport dimensions and recreates the markdown renderer
// with the correct word wrap width.
func (m *ChatModel) SetSize(width, height int) {
	if width != m.width {
		m.renderer = newRenderer(width, m.dark)
	}
	m.width = width
	m.height = height
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(height)
	m.render()
}

// SetDark updates the dark/light theme and recreates the renderer.
func (m *ChatModel) SetDark(dark bool) {
	if dark != m.dark {
		m.dark = dark
		if m.width > 0 {
			m.renderer = newRenderer(m.width, dark)
			m.render()
		}
	}
}

// renderMarkdown renders markdown content, falling back to plain text on error.
func (m *ChatModel) renderMarkdown(content string) string {
	if m.renderer == nil {
		return content
	}
	rendered, err := m.renderer.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimRight(rendered, "\n")
}

// blockOf classifies a chatMessage role into a render block. Consecutive
// messages of the same block share a single header (You / Assistant /
// Tools / System) and a blank line is inserted between blocks.
func blockOf(role api.Role) string {
	switch role {
	case api.RoleUser:
		return "user"
	case api.RoleAssistant:
		return "assistant"
	case api.RoleSystem:
		return "system"
	case "tool_call", "approval":
		return "tools"
	}
	return ""
}

func (m *ChatModel) render() {
	var b strings.Builder
	var prevBlock string

	writeHeader := func(block string) {
		switch block {
		case "user":
			b.WriteString(userStyle.Render("You") + "\n")
		case "assistant":
			b.WriteString(assistantStyle.Render("Assistant") + "\n")
		case "system":
			b.WriteString(systemHeaderStyle.Render("System") + "\n")
		}
	}

	for i := 0; i < len(m.messages); i++ {
		msg := m.messages[i]
		thisBlock := blockOf(msg.role)
		if thisBlock != prevBlock {
			if prevBlock != "" {
				b.WriteString("\n") // blank line separator between blocks
			}
			writeHeader(thisBlock)
		}
		switch msg.role {
		case api.RoleUser:
			b.WriteString(msg.content + "\n")
		case api.RoleAssistant:
			b.WriteString(m.renderMarkdown(msg.content) + "\n")
		case "tool_call":
			// Collapse run detection: when this row has a non-empty
			// collapseKey and no error, look ahead for consecutive
			// tool_call rows with the same key that also resolved
			// successfully and whose summaries all parse to the same
			// merge format. If a run ≥2 is found, emit one combined
			// header + one merged summary and skip past the rest.
			// Single rows fall through to the standard render below.
			if runEnd, merged, ok := m.collapseRun(i); ok && runEnd > i {
				b.WriteString(merged)
				prevBlock = thisBlock
				i = runEnd
				continue
			}
			b.WriteString(msg.content + "\n")
			// Result lines render INSIDE the call row so a parallel
			// dispatch batch lays out call/result/call/result/... in
			// the natural inline order, even when the underlying Done
			// events arrived out of order.
			if msg.errBody != "" {
				b.WriteString(msg.errBody + "\n")
			} else if msg.summary != "" {
				b.WriteString(formatToolSummary(msg.summary) + "\n")
			}
		case "approval":
			b.WriteString(msg.content + "\n")
		case api.RoleSystem:
			if msg.markdown {
				b.WriteString(m.renderMarkdown(msg.content) + "\n")
			} else {
				b.WriteString(systemStyle.Render(msg.content) + "\n")
			}
		}
		prevBlock = thisBlock
	}

	if strings.TrimSpace(m.streaming) != "" {
		// Streaming text always belongs to the assistant block. Emit a
		// header (with leading blank line) when the previous block isn't
		// already assistant, so post-tool continuations get a clean
		// "Assistant" label. Whitespace-only buffers are skipped so a
		// leading "\n" delta from Kimi/GLM doesn't flash an empty header.
		if prevBlock != "assistant" {
			if prevBlock != "" {
				b.WriteString("\n")
			}
			b.WriteString(assistantStyle.Render("Assistant") + "\n")
		}
		b.WriteString(m.renderMarkdown(m.streaming))
	}

	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

// formatToolCallRow renders the call header. When header is non-empty
// it is the canonical FormatForDisplay output (e.g.
// `WebFetch(url: "https://...")`) and the chat just decorates with the
// bullet + tool style. When header is empty the chat falls back to the
// legacy `name <summary>` form via summarizeArgs. A non-empty subagent
// adds an "Explore → " / "Research → " prefix so the user can tell
// which child issued the call.
//
// width is the chat viewport width — passed through so edit/write diff
// previews can render full-row colored bands sized to the terminal.
func formatToolCallRow(name, argsJSON, header, subagent string, width int) string {
	style := toolStyle
	prefix := ""
	if subagent != "" {
		style = toolSubagentStyle
		prefix = taskDisplayName(subagent) + " → "
	}
	var line string
	if header != "" {
		line = style.Render("  ⚡ " + prefix + header)
	} else {
		line = style.Render("  ⚡ "+prefix+name) + " " + summarizeArgs(name, argsJSON)
	}
	if diff := formatToolDiff(name, argsJSON, width); diff != "" {
		return line + "\n" + diff
	}
	return line
}

// formatToolSummary renders the dim `   └ <summary>` connector line
// shown beneath a successful tool-call row. The leading indent matches
// the call row's `  ⚡ ` prefix so the box-drawing character lines up.
func formatToolSummary(summary string) string {
	return dimStyle.Render("    └ " + summary)
}

// collapseRun inspects messages starting at index `start` and reports
// whether they form a mergeable run of same-collapseKey successful
// tool_call rows. Returns:
//
//   - runEnd: inclusive last index of the run (== start when no run).
//   - merged: the pre-rendered combined header + summary block when a
//     run ≥2 was found.
//   - ok: true iff a run ≥2 with all-parseable summaries was found.
//
// Run-break rules (Q1=b):
//   - Different role, different collapseKey, or empty collapseKey → break.
//   - An errored row → break (errors render individually with full body).
//   - A row whose summary isn't yet populated (in-flight) → break. The
//     run can't be merged until every result has arrived; partial runs
//     would jitter as Done events trickle in.
//   - Any summary that doesn't match the Read range format → break.
func (m *ChatModel) collapseRun(start int) (int, string, bool) {
	first := m.messages[start]
	if first.role != "tool_call" || first.collapseKey == "" || first.errBody != "" || first.summary == "" {
		return start, "", false
	}
	firstParsed, ok := parseReadRangeSummary(first.summary)
	if !ok {
		return start, "", false
	}

	end := start
	parsed := []readRangeSummary{firstParsed}
	for end+1 < len(m.messages) {
		next := m.messages[end+1]
		if next.role != "tool_call" || next.collapseKey != first.collapseKey || next.errBody != "" || next.summary == "" {
			break
		}
		np, ok := parseReadRangeSummary(next.summary)
		if !ok {
			break
		}
		end++
		parsed = append(parsed, np)
	}
	if end == start {
		return start, "", false
	}

	// Merge: every entry in `parsed` carries the same `total` and `size`
	// (they all describe the same file); pick min(start) and max(end)
	// across the run for the headline range.
	minStart := parsed[0].start
	maxEnd := parsed[0].end
	for _, p := range parsed[1:] {
		if p.start < minStart {
			minStart = p.start
		}
		if p.end > maxEnd {
			maxEnd = p.end
		}
	}
	count := end - start + 1
	header := first.content + " " + dimStyle.Render(fmt.Sprintf("× %d", count))
	summary := formatToolSummary(fmt.Sprintf("%d reads · lines %d–%d of %d · %s",
		count, minStart, maxEnd, firstParsed.total, firstParsed.size))
	return end, header + "\n" + summary + "\n", true
}

// readRangeSummary is the parsed shape of a Read-tool DisplaySummary
// of the form "lines <start>–<end> of <total> · <size>". Other Read
// summary formats (whole-file, spilled bytes) don't carry the of-N
// total and don't collapse.
type readRangeSummary struct {
	start, end, total int
	size              string
}

// parseReadRangeSummary returns (parsed, true) for summaries shaped
// "lines %d–%d of %d · %s" — the format Read emits for ranged reads
// at internal/tool/read.go:240. Other summary shapes return
// (zero, false) and disqualify the row from a collapse run. Note the
// en-dash (U+2013, –), not a hyphen — Read uses the en-dash form.
func parseReadRangeSummary(s string) (readRangeSummary, bool) {
	const enDash = "–"
	if !strings.HasPrefix(s, "lines ") {
		return readRangeSummary{}, false
	}
	rest := s[len("lines "):]
	dash := strings.Index(rest, enDash)
	if dash < 0 {
		return readRangeSummary{}, false
	}
	startN, err := strconv.Atoi(rest[:dash])
	if err != nil {
		return readRangeSummary{}, false
	}
	rest = rest[dash+len(enDash):]
	sp := strings.Index(rest, " ")
	if sp < 0 {
		return readRangeSummary{}, false
	}
	endN, err := strconv.Atoi(rest[:sp])
	if err != nil {
		return readRangeSummary{}, false
	}
	rest = rest[sp+1:]
	if !strings.HasPrefix(rest, "of ") {
		return readRangeSummary{}, false
	}
	rest = rest[len("of "):]
	sep := strings.Index(rest, " · ")
	if sep < 0 {
		return readRangeSummary{}, false
	}
	totalN, err := strconv.Atoi(rest[:sep])
	if err != nil {
		return readRangeSummary{}, false
	}
	return readRangeSummary{
		start: startN,
		end:   endN,
		total: totalN,
		size:  rest[sep+len(" · "):],
	}, true
}

// computeCollapseKey produces the merge-key for runs of similar tool
// calls eligible to render as one row. Currently scoped to Read calls
// keyed by file_path — sequential chunked reads of the same file (the
// model paging through a long source file) are the worst noise case.
// Other tools return "" so they always render standalone.
//
// Extension path: add other tools here when a per-tool merge target
// makes sense (e.g. Grep over the same path with a list of patterns).
func computeCollapseKey(name, argsJSON string) string {
	if name != "read" {
		return ""
	}
	var p struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &p); err != nil {
		return ""
	}
	if p.FilePath == "" {
		return ""
	}
	return "read:" + p.FilePath
}

// formatToolResult renders a tool error for inline display. Successful
// results are not rendered (see AppendToolResult), so this only ever runs
// for the error path. name and isError are unused; the signature is kept
// stable for callers and future re-introduction of success rendering.
func formatToolResult(_ string, result string, _ bool) string {
	const maxDisplay = 500
	display := result
	if len(display) > maxDisplay {
		display = display[:maxDisplay] + "\n  ... (truncated)"
	}
	return "  " + errorStyle.Render(display)
}

func summarizeArgs(name, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return truncate(argsJSON, 100)
	}

	switch name {
	case "read":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			// Never truncate shell commands — see the matching note in
			// indicators.go summarizeToolCall and bash.go FormatForDisplay.
			return cmd
		}
	case "edit":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "replace_lines":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "write":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "grep":
		if pat, ok := args["pattern"].(string); ok {
			return truncate(pat, 80)
		}
	case "glob":
		if pat, ok := args["pattern"].(string); ok {
			return pat
		}
	case "list":
		if p, ok := args["path"].(string); ok {
			return p
		}
		return "(cwd)"
	case "webfetch", "webfetch_headless":
		if u, ok := args["url"].(string); ok {
			summary := truncate(u, 80)
			if q, ok := args["query"].(string); ok && q != "" {
				summary += " — " + truncate(q, 50)
			}
			return summary
		}
	}

	data, _ := json.Marshal(args)
	return truncate(string(data), 100)
}

// formatToolDiff generates a diff preview for edit and write tool calls.
// width is the chat viewport width — band rows fill to this so the
// background colors render as full-row labels.
func formatToolDiff(name, argsJSON string, width int) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	switch name {
	case "edit":
		return formatEditDiff(args, width)
	case "write":
		return formatWriteDiff(args, width)
	default:
		return ""
	}
}

// diffViewportFallback caps the rendered band width when the chat
// model hasn't received a SetSize yet (resize before first paint, or
// tests). 80 cols matches the legacy default and keeps backgrounds
// from spilling on terminals reporting a 0 width.
const diffViewportFallback = 80

// renderDiffBand emits the unified-diff layout for a slice of
// diff.Op: a 5-wide line-number gutter, a 1-wide +/- gutter, and a
// background-filled content row. Lines wider than the viewport are
// soft-truncated with an ellipsis — full-row backgrounds + viewport
// soft-wrap don't compose cleanly, and a wrapped band would leak the
// background past the next row's gutter. Trailing newline trimmed.
func renderDiffBand(ops []diff.Op, width int) string {
	if width <= 0 {
		width = diffViewportFallback
	}
	// Compute available content width: 5 gutter + 1 marker + 1 space
	// = 7 reserved cells. Floor at a sane minimum so very narrow
	// terminals still render something readable.
	contentWidth := width - 7
	if contentWidth < 20 {
		contentWidth = 20
	}

	var b strings.Builder
	for _, op := range ops {
		switch op.Kind {
		case diff.OpRemove:
			b.WriteString(diffLineNumStyle.Render(gutterInt(op.OldLine)))
			b.WriteString(diffGutterRemBG.Render("-"))
			b.WriteString(diffRemRowBG.Width(contentWidth).Render(" " + clipForBand(op.Line, contentWidth-1)))
			b.WriteByte('\n')
		case diff.OpAdd:
			b.WriteString(diffLineNumStyle.Render(gutterInt(op.NewLine)))
			b.WriteString(diffGutterAddBG.Render("+"))
			b.WriteString(diffAddRowBG.Width(contentWidth).Render(" " + clipForBand(op.Line, contentWidth-1)))
			b.WriteByte('\n')
		case diff.OpEqual:
			b.WriteString(diffLineNumStyle.Render(gutterInt(op.NewLine)))
			b.WriteString(diffGutterCtxBG.Render(" "))
			b.WriteString(diffCtxRowBG.Render(" " + clipForBand(op.Line, contentWidth-1)))
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// clipForBand caps s at max RUNES with an ellipsis when overflow.
// Used inside background-filled bands where soft-wrap would bleed
// into the next row's gutter — controlled clip + a single trailing
// "…" stays inside the colored cell.
func clipForBand(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}

// gutterInt renders an int for the diff line-number gutter. 0 is a
// sentinel for "this line doesn't exist on this side" — render as a
// blank cell so adds/removes have an aligned column without a
// misleading "0".
func gutterInt(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func formatEditDiff(args map[string]any, width int) string {
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	if oldStr == "" {
		return ""
	}
	ops := diff.Diff(oldStr, newStr)
	if len(ops) == 0 {
		return ""
	}
	return renderDiffBand(ops, width)
}

func formatWriteDiff(args map[string]any, width int) string {
	content, _ := args["content"].(string)
	if content == "" {
		return dimStyle.Render("  (empty file)")
	}

	// A `write` is "create from nothing", so every line is an add.
	// Build the op slice directly — no diff needed — and cap the
	// preview at maxPreview lines to keep huge files from pushing the
	// chat scroll halfway across the screen.
	const maxPreview = 15
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	truncated := false
	if len(lines) > maxPreview {
		lines = lines[:maxPreview]
		truncated = true
	}
	ops := make([]diff.Op, 0, len(lines))
	for i, line := range lines {
		ops = append(ops, diff.Op{Kind: diff.OpAdd, NewLine: i + 1, Line: line})
	}
	out := renderDiffBand(ops, width)
	if truncated {
		out += "\n" + dimStyle.Render(fmt.Sprintf("  ... (+%d more lines)", strings.Count(content, "\n")+1-maxPreview))
	}
	return out
}

// truncate caps s at max RUNES (not bytes) so multibyte characters
// stay intact. The TUI feeds this string to lipgloss/bubbletea, which
// renders U+FFFD on a mid-codepoint cut.
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
