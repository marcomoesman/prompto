package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"

	"github.com/marcomoesman/prompto/internal/agent"
)

// todoPanelPendingSoftCap bounds how many pending items the panel
// renders before rolling the rest into a "+M more pending" line.
// Keeps the panel from claiming more than ~half a typical screen
// when the model produces a very long todo list.
const todoPanelPendingSoftCap = 12

// TodoPanel is the sticky panel pinned above the input. It's a
// passive view-only component — AppModel pushes state in via
// SetTodos / SetMeter; the panel stores what it needs and renders
// on demand. Keeping it stateless beyond the inputs makes the
// rendering logic exhaustively testable without a tea loop.
type TodoPanel struct {
	todos     []agent.Todo
	width     int
	streaming bool
	elapsed   time.Duration
	tokens    int
}

// NewTodoPanel returns an empty panel. Visible() returns false until
// SetTodos is called with a non-empty slice.
func NewTodoPanel() TodoPanel { return TodoPanel{} }

// SetTodos replaces the cached list. nil or empty hides the panel.
func (p *TodoPanel) SetTodos(todos []agent.Todo) { p.todos = todos }

// SetWidth pushes the panel's render width. Lines are rune-truncated
// to width-1 so the panel never wraps and ANSI-styled cells don't
// leak into the input area on a narrow terminal.
func (p *TodoPanel) SetWidth(w int) { p.width = w }

// SetMeter updates the header chip (timer + token total) and the
// streaming gate that drives whether the header line is rendered.
// Called from the elapsedTickMsg handler and the EventUsageReport
// handler in app.go.
func (p *TodoPanel) SetMeter(streaming bool, elapsed time.Duration, tokens int) {
	p.streaming = streaming
	p.elapsed = elapsed
	p.tokens = tokens
}

// Visible reports whether the panel should render anything. relayout
// uses this to decide whether to reserve any rows for the panel.
//
// The panel exists to surface outstanding work — the active todo and
// what's still pending. A list whose items have all flipped to
// `completed` carries no live signal, so it collapses; otherwise the
// most-recent ✓ row and the "+N completed" rollup would linger
// indefinitely (until /clear) eating viewport rows for nothing.
func (p TodoPanel) Visible() bool {
	for _, t := range p.todos {
		if t.Status == "in_progress" || t.Status == "pending" || t.Status == "" {
			return true
		}
	}
	return false
}

// Height returns the row count the panel will occupy on the next
// paint. Zero when not visible. The caller subtracts this from the
// chat-viewport height in relayout().
func (p TodoPanel) Height() int {
	if !p.Visible() {
		return 0
	}
	completed, inProgress, pending := splitTodos(p.todos)
	rows := 0
	if p.streaming {
		rows++ // header line
	}
	if len(completed) > 0 {
		rows++ // most-recent completed
	}
	if len(inProgress) > 0 {
		rows++
	}
	visiblePending := len(pending)
	if visiblePending > todoPanelPendingSoftCap {
		visiblePending = todoPanelPendingSoftCap
	}
	rows += visiblePending
	if len(completed) > 1 {
		rows++ // "+N completed" rollup
	}
	if len(pending) > todoPanelPendingSoftCap {
		rows++ // "+M more pending" rollup
	}
	return rows
}

// View renders the panel. spin is the shared spinner from AppModel —
// passed by value so the panel doesn't have to drive ticks itself.
func (p TodoPanel) View(spin spinner.Model) string {
	if !p.Visible() {
		return ""
	}
	completed, inProgress, pending := splitTodos(p.todos)

	var b strings.Builder
	width := p.width
	if width <= 0 {
		width = 80
	}

	// Header line. Only when the agent is mid-turn — idle sessions
	// just show the list. activeForm wins; falls back to "Working…"
	// when the model has produced no in_progress todo this turn.
	if p.streaming {
		header := "Working…"
		if len(inProgress) > 0 {
			active := inProgress[0].ActiveForm
			if active == "" {
				active = inProgress[0].Content
			}
			header = active
		}
		meter := formatTodoMeter(p.elapsed, p.tokens)
		line := fmt.Sprintf("%s %s  %s",
			indicatorStyle.Render(spin.View()),
			todoActiveStyle.Render(header),
			dimStyle.Render(meter),
		)
		b.WriteString(truncatePanelLine(line, width))
		b.WriteByte('\n')
	}

	// Most-recent completed (single line, with the tree-bullet on the
	// first body row).
	bodyFirst := true
	bullet := func() string {
		if bodyFirst {
			bodyFirst = false
			return "  └ "
		}
		return "    "
	}

	if len(completed) > 0 {
		t := completed[len(completed)-1]
		line := bullet() + dimStyle.Render("✓ "+t.Content)
		b.WriteString(truncatePanelLine(line, width))
		b.WriteByte('\n')
	}

	if len(inProgress) > 0 {
		t := inProgress[0]
		line := bullet() + todoActiveStyle.Render("■ "+t.Content)
		b.WriteString(truncatePanelLine(line, width))
		b.WriteByte('\n')
	}

	visiblePending := pending
	overflow := 0
	if len(visiblePending) > todoPanelPendingSoftCap {
		overflow = len(visiblePending) - todoPanelPendingSoftCap
		visiblePending = visiblePending[:todoPanelPendingSoftCap]
	}
	for _, t := range visiblePending {
		line := bullet() + todoPendingStyle.Render("□ "+t.Content)
		b.WriteString(truncatePanelLine(line, width))
		b.WriteByte('\n')
	}

	if len(completed) > 1 {
		line := dimStyle.Render(fmt.Sprintf("       … +%d completed", len(completed)-1))
		b.WriteString(truncatePanelLine(line, width))
		b.WriteByte('\n')
	}
	if overflow > 0 {
		line := dimStyle.Render(fmt.Sprintf("       … +%d more pending", overflow))
		b.WriteString(truncatePanelLine(line, width))
		b.WriteByte('\n')
	}

	return strings.TrimRight(b.String(), "\n")
}

// splitTodos groups a list by status, preserving relative order
// within each group. completed/inProgress/pending are returned in
// the order the model wrote them — the renderer picks the most
// recent completed, the (single, by tool contract) in_progress, and
// every pending in the same order.
func splitTodos(todos []agent.Todo) (completed, inProgress, pending []agent.Todo) {
	for _, t := range todos {
		switch t.Status {
		case "completed":
			completed = append(completed, t)
		case "in_progress":
			inProgress = append(inProgress, t)
		default:
			pending = append(pending, t)
		}
	}
	return
}

// formatTodoMeter renders the "(12m 48s · ↑ 51.9k)" header chip.
// Empty string when the agent isn't streaming AND has produced no
// usage yet (avoids a stale "0s · ↑ 0" on a freshly-resumed session).
func formatTodoMeter(elapsed time.Duration, tokens int) string {
	parts := make([]string, 0, 2)
	if s := int(elapsed.Seconds()); s > 0 {
		parts = append(parts, formatElapsed(s))
	}
	if tokens > 0 {
		parts = append(parts, "↑ "+formatTokenCount(tokens))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " · ") + ")"
}

// truncatePanelLine cuts a styled line to fit a panel of width w.
// lipgloss.Width respects ANSI escapes, so the cut accounts for
// rendered width rather than byte length. A simple byte-slice would
// chop SGR codes mid-sequence and corrupt the next line.
func truncatePanelLine(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	// Walk runes from the end while ANSI-aware width still exceeds w.
	// Rare path; the inner loop's O(n) is fine for the small panel.
	for lipgloss.Width(s) > w && len(s) > 0 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}
