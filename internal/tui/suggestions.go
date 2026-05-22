package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/marcomoesman/prompto/internal/command"
)

// suggestionsMaxVisible caps the rendered rows so the panel never
// dominates the screen. Excess matches are reachable by ↑/↓ scrolling
// the cursor through the list.
const suggestionsMaxVisible = 5

// SuggestionsModel renders the slash-command autocomplete popup. It
// activates when the input begins with `/` and a single token (no
// whitespace yet); typing further narrows the prefix-matched set, and
// `Tab` / `Enter` accept the highlighted command.
//
// The model is driven by AppModel through:
//
//   - Update(text string)  — re-evaluate visibility/matches
//   - Move(delta int)      — cursor navigation while visible
//   - Selected()           — read the highlighted command
//   - Hide()               — force-hide (overlay open, streaming, etc.)
//   - SetWidth(w int)      — match the input's width
//
// All state is kept here; AppModel only owns the value type.
type SuggestionsModel struct {
	reg *command.Registry

	visible bool
	prefix  string
	matches []command.Command
	cursor  int
	scroll  int
	width   int
}

// NewSuggestionsModel builds a hidden model bound to reg. A nil
// registry yields a permanently inert model — Visible() always returns
// false — so callers without slash dispatch don't crash.
func NewSuggestionsModel(reg *command.Registry) SuggestionsModel {
	return SuggestionsModel{reg: reg}
}

// Visible reports whether the panel should render.
func (m *SuggestionsModel) Visible() bool { return m.visible && len(m.matches) > 0 }

// Hide forces the panel off without altering the cursor. Update will
// re-enable visibility on the next matching input.
func (m *SuggestionsModel) Hide() { m.visible = false }

// SetWidth records the panel width — typically the input's width — so
// rows can be padded/truncated against it.
func (m *SuggestionsModel) SetWidth(w int) { m.width = w }

// Selected returns the highlighted command, or nil when the panel is
// hidden / has no matches.
func (m *SuggestionsModel) Selected() command.Command {
	if !m.Visible() {
		return nil
	}
	return m.matches[m.cursor]
}

// Update re-evaluates the popup from raw input text. It activates only
// when text starts with `/` and contains no whitespace after the slash;
// otherwise the panel hides. The matches list is rebuilt from the
// registry on every call (cheap — there are ~20 commands).
func (m *SuggestionsModel) Update(text string) {
	if m.reg == nil {
		m.visible = false
		return
	}
	prefix, ok := slashPrefix(text)
	if !ok {
		m.visible = false
		m.matches = nil
		return
	}
	matches := filterByPrefix(m.reg.All(), prefix)
	if len(matches) == 0 {
		m.visible = false
		m.matches = nil
		return
	}
	if prefix != m.prefix {
		// New prefix → reset selection to the top so users always
		// land on the most-likely match (alphabetical leader).
		m.cursor = 0
		m.scroll = 0
	} else if m.cursor >= len(matches) {
		m.cursor = len(matches) - 1
	}
	m.prefix = prefix
	m.matches = matches
	m.visible = true
	m.clampScroll()
}

// Move shifts the cursor by delta and slides the scroll window so the
// cursor stays within the visible band. Cursor clamps; no wraparound.
func (m *SuggestionsModel) Move(delta int) {
	if !m.Visible() {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.matches) {
		m.cursor = len(m.matches) - 1
	}
	m.clampScroll()
}

// clampScroll keeps the scroll window aligned with the cursor and the
// list bounds. Called after every cursor or matches mutation.
func (m *SuggestionsModel) clampScroll() {
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+suggestionsMaxVisible {
		m.scroll = m.cursor - suggestionsMaxVisible + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	maxScroll := len(m.matches) - suggestionsMaxVisible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
}

// Height returns the row count the panel occupies, including the top
// and bottom border rows. Returns 0 when hidden so layout math can add
// it unconditionally.
func (m *SuggestionsModel) Height() int {
	if !m.Visible() {
		return 0
	}
	rows := len(m.matches)
	if rows > suggestionsMaxVisible {
		rows = suggestionsMaxVisible
	}
	// rows + 2 border rows + 1 hint row (rendered inside the border).
	return rows + 3
}

// View renders the popup, or "" when hidden. The panel matches the
// /help overlay's style idiom: rounded cyan border, two-column rows
// with the command name in the picker-selected style and the help
// text dimmed. The currently highlighted row gets the bold variant +
// a left chevron for orientation when the user is navigating.
func (m *SuggestionsModel) View() string {
	if !m.Visible() {
		return ""
	}
	end := m.scroll + suggestionsMaxVisible
	if end > len(m.matches) {
		end = len(m.matches)
	}
	visible := m.matches[m.scroll:end]

	nameW := 0
	for _, c := range visible {
		if w := lipgloss.Width("/" + c.Name()); w > nameW {
			nameW = w
		}
	}

	// innerWidth caps row content so the panel never exceeds the
	// input's width. Panel chrome (border + padding) accounts for 4
	// columns. When width hasn't been set yet (early frame), fall
	// back to a generous default so the panel still renders.
	innerWidth := m.width - 4
	if m.width <= 0 {
		innerWidth = 80
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	helpWidth := innerWidth - 2 - nameW - 2 // pointer(2) + name + gap(2)
	if helpWidth < 0 {
		helpWidth = 0
	}

	var b strings.Builder
	for i, c := range visible {
		idx := m.scroll + i
		selected := idx == m.cursor

		pointer := "  "
		nameStyle := helpKeyStyle
		if selected {
			pointer = suggestionsSelectedStyle.Render("▸ ")
			nameStyle = suggestionsSelectedStyle
		}
		name := padRight("/"+c.Name(), nameW)
		help := truncateToWidth(c.Help(), helpWidth)
		row := pointer + nameStyle.Render(name) + "  " + helpDescStyle.Render(help)
		b.WriteString(row)
		b.WriteByte('\n')
	}

	hint := suggestionsHintStyle.Render(truncateToWidth("↑↓ select · tab/enter accept · esc close", innerWidth))
	b.WriteString(hint)

	return suggestionsPanelStyle.Render(b.String())
}

// slashPrefix returns the part after the leading `/` when the input
// is in single-token slash form. ok is false for plain text, multiple
// tokens, or empty input.
func slashPrefix(text string) (string, bool) {
	s := strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(s, "/") {
		return "", false
	}
	s = strings.TrimPrefix(s, "/")
	if strings.ContainsAny(s, " \t\n") {
		return "", false
	}
	return s, true
}

// filterByPrefix narrows cmds to those whose canonical name begins
// with prefix (case-insensitive). The registry already returns
// alphabetically sorted commands, so the result preserves that order.
func filterByPrefix(cmds []command.Command, prefix string) []command.Command {
	if prefix == "" {
		out := make([]command.Command, len(cmds))
		copy(out, cmds)
		return out
	}
	lower := strings.ToLower(prefix)
	out := make([]command.Command, 0, len(cmds))
	for _, c := range cmds {
		if strings.HasPrefix(strings.ToLower(c.Name()), lower) {
			out = append(out, c)
		}
	}
	return out
}

// truncateToWidth shortens s so its rendered width fits within max,
// appending an ellipsis when truncation occurs. Cheap byte-slice walk
// — the inputs are small command names + help strings, never multibyte
// in practice for the strings we control.
func truncateToWidth(s string, max int) string {
	if max <= 1 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	// Walk runes until adding one more would exceed max-1 (room for `…`).
	out := make([]rune, 0, max)
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > max-1 {
			break
		}
		out = append(out, r)
		w += rw
	}
	return string(out) + "…"
}
