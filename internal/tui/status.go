package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// StatusModel renders the three-segment bottom status bar:
//
//	[⏱ <elapsed>s · context: NN%]   /help for help menu    · MCP: 0
//
// Segments collapse from the right when the terminal is too narrow:
// below 80 cols the right segment drops; below 60 the center drops.
type StatusModel struct {
	modelName     string
	sessionPrefix string // empty when no session or store disabled
	agent         string // active agent name (build, plan, …)
	agentColor    string // lipgloss color spec from AgentDefinition.Color; "" hides the color tint
	mode          string // "" | "default" | "acceptEdits" | "bypass"
	inputTokens   int    // cumulative input tokens consumed
	outputTokens  int    // cumulative output tokens consumed
	contextTokens int    // latest context size (from provider usage)
	contextLimit  int    // operative ceiling for the active model
	elapsedSec    int    // seconds since current turn started; 0 when idle
	streaming     bool
	width         int
}

// View renders the status bar.
//
// Each segment is composed by concatenating individual Render calls
// rather than wrapping pre-styled inner content in an outer Render.
// Lipgloss v2 doesn't re-apply an outer style after a nested ANSI
// reset, so a `statusStyle.Render(" "+inner+" ")` where `inner`
// contains a colored span emits gray-bg until the inner span's
// `\x1b[0m`, then the rest of the segment renders on the terminal
// default. Composing piece-by-piece keeps every visible glyph inside
// its own self-contained `\x1b[BG;FG]…\x1b[0m` bubble.
func (m StatusModel) View() string {
	leftRendered := m.styledLeft()
	centerRendered := m.styledCenter()
	rightRendered := m.styledRight()

	leftW := lipgloss.Width(leftRendered)
	centerW := lipgloss.Width(centerRendered)
	rightW := lipgloss.Width(rightRendered)

	// Narrow-terminal collapse: below 80 cols drop the right segment;
	// below 60 also drop the center. Left always survives.
	if m.width > 0 {
		if m.width < 60 {
			centerRendered = ""
			rightRendered = ""
			centerW = 0
			rightW = 0
		} else if m.width < 80 {
			rightRendered = ""
			rightW = 0
		}
	}

	totalContent := leftW + centerW + rightW
	if m.width <= 0 || totalContent >= m.width {
		// Just stitch them together without padding when width is unset
		// or content already fills/overflows.
		return leftRendered + centerRendered + rightRendered
	}

	// Distribute the remaining space so center is centered and right is
	// flush-right.
	remaining := m.width - totalContent
	leftPad := remaining / 2
	if centerW == 0 {
		// No center segment — just push right to the far edge.
		return leftRendered + statusStyle.Render(strings.Repeat(" ", remaining)) + rightRendered
	}
	rightPad := remaining - leftPad
	return leftRendered +
		statusStyle.Render(strings.Repeat(" ", leftPad)) +
		centerRendered +
		statusStyle.Render(strings.Repeat(" ", rightPad)) +
		rightRendered
}

// styledLeft returns the left segment fully styled (one Render covers
// it because there are no inline accents).
func (m StatusModel) styledLeft() string {
	return statusStyle.Render(" " + m.renderLeft() + " ")
}

// styledCenter composes the center segment as a sequence of
// independent Render calls so the gray band stays continuous around
// the colored agent name. Each Render is its own self-contained
// bubble; nothing is wrapped twice.
func (m StatusModel) styledCenter() string {
	var b strings.Builder
	b.WriteString(statusStyle.Render(" " + m.modelName))
	if m.agent != "" {
		b.WriteString(statusStyle.Render(" · "))
		if m.agentColor != "" {
			// Derive from statusStyle so the bg matches automatically.
			b.WriteString(statusStyle.
				Foreground(lipgloss.Color(m.agentColor)).
				Bold(true).
				Render(m.agent))
		} else {
			b.WriteString(statusStyle.Render(m.agent))
		}
	}
	if m.sessionPrefix != "" {
		b.WriteString(statusStyle.Render(" · " + m.sessionPrefix))
	}
	// Mode badge. acceptEdits renders in yellow as a quiet reminder;
	// bypass renders in bold red caps so a user who launched with
	// --yolo can't miss that every tool call is going through
	// unprompted. Both derive from statusStyle to preserve the bar's
	// grey backdrop across the inline color span.
	switch m.mode {
	case "acceptEdits":
		b.WriteString(statusStyle.Render(" · "))
		b.WriteString(statusStyle.Foreground(lipgloss.Color("11")).Bold(true).Render("acceptEdits"))
	case "bypass":
		b.WriteString(statusStyle.Render(" · "))
		b.WriteString(statusStyle.Foreground(lipgloss.Color("9")).Bold(true).Render("BYPASS"))
	}
	b.WriteString(statusStyle.Render(" "))
	return b.String()
}

// styledRight composes the right segment piece-by-piece for the same
// reason styledCenter does: the `/help` accent uses helpStyle (which
// has the same bg as statusStyle), and the surrounding " for help
// menu" text is its own Render — neither nests inside the other.
func (m StatusModel) styledRight() string {
	var b strings.Builder
	b.WriteString(statusStyle.Render(" "))
	b.WriteString(helpStyle.Render("/help"))
	suffix := " for help menu"
	if m.streaming {
		suffix += " · streaming…"
	}
	b.WriteString(statusStyle.Render(suffix + " "))
	return b.String()
}

func (m StatusModel) renderLeft() string {
	parts := []string{}
	if m.elapsedSec > 0 {
		parts = append(parts, fmt.Sprintf("⏱ %s", formatElapsed(m.elapsedSec)))
	}
	pct := m.contextPct()
	if pct >= 0 {
		parts = append(parts, fmt.Sprintf("context: %d%%", pct))
	}
	// in/out are CUMULATIVE across every provider call this session,
	// not the current turn's size — the (Σ) sigma marks them so the
	// number isn't mistaken for "current context" (which is what the
	// `context: %` segment shows). On a local model with no prompt
	// caching, every turn re-sends the full conversation, so the
	// cumulative input grows by ~the full context each turn.
	parts = append(parts, fmt.Sprintf("in (Σ): %s", formatTokenCount(m.inputTokens)))
	parts = append(parts, fmt.Sprintf("out (Σ): %s", formatTokenCount(m.outputTokens)))
	return strings.Join(parts, " · ")
}

// statusBgColor is the bar's background tone. Pulled out as a named
// constant so every inline style nested inside the bar can match it
// exactly — drift in either direction shows up as a visible black
// gap where the inner span's ANSI reset wins.
var statusBgColor = lipgloss.Color("236")

// helpStyle paints the `/help` accent in the right segment. The
// Background must equal statusBgColor so the surrounding " for help menu"
// text doesn't lose the gray band when this style's ANSI reset
// fires.
var helpStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("75")).
	Background(statusBgColor).
	Bold(true)

// contextPct returns the percentage of the context window currently in
// use, or -1 when the data isn't available yet (no usage event received,
// or compactor not wired).
func (m StatusModel) contextPct() int {
	if m.contextTokens <= 0 || m.contextLimit <= 0 {
		return -1
	}
	pct := m.contextTokens * 100 / m.contextLimit
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// formatElapsed renders a turn duration in seconds using human time
// units. Under a minute it shows seconds ("47s"); under an hour it
// shows minutes and seconds ("1m23s"); above that it shows hours and
// minutes ("2h05m"). Past 24h it falls back to days+hours ("1d03h").
func formatElapsed(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm%02ds", sec/60, sec%60)
	}
	if sec < 86400 {
		return fmt.Sprintf("%dh%02dm", sec/3600, (sec%3600)/60)
	}
	return fmt.Sprintf("%dd%02dh", sec/86400, (sec%86400)/3600)
}

func formatTokenCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
