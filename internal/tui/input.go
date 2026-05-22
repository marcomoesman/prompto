package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/marcomoesman/prompto/internal/permission"
)

// inputBorderRows is the number of rows the manually-rendered border
// occupies (top + bottom).
const inputBorderRows = 2

// InputModel wraps a textarea and renders it inside a mode-aware border
// with the active permission mode embedded in the top edge.
type InputModel struct {
	textarea textarea.Model
	width    int
	mode     permission.Mode
}

// NewInputModel creates a configured input area.
func NewInputModel() InputModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send · ESC to cancel)"
	ta.SetHeight(1)
	ta.Focus()
	// Unbind Enter from inserting newline — we handle submission in app.go
	ta.KeyMap.InsertNewline.Unbind()
	// Strip the textarea's own border so our wrapper owns the visual frame.
	styles := ta.Styles()
	styles.Focused.Base = lipgloss.NewStyle()
	styles.Blurred.Base = lipgloss.NewStyle()
	ta.SetStyles(styles)
	return InputModel{textarea: ta}
}

// Update passes messages to the textarea.
func (m InputModel) Update(msg tea.Msg) (InputModel, tea.Cmd) {
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// View renders the bordered input.
func (m InputModel) View() string {
	if m.width <= 0 {
		// No size yet — bubbletea calls View before the first
		// WindowSizeMsg. Return empty so we don't try to draw a frame.
		return ""
	}
	border, label := borderForMode(m.mode)
	inner := m.textarea.View()

	// Constrain the inner width so we never overflow the border.
	contentWidth := m.width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}

	// The textarea may render a single line; pad it to contentWidth so
	// the right border aligns.
	innerLines := strings.Split(inner, "\n")
	for i, line := range innerLines {
		w := lipgloss.Width(line)
		if w < contentWidth {
			innerLines[i] = line + strings.Repeat(" ", contentWidth-w)
		}
	}

	top := renderInputTop(m.width, border, label, m.mode)
	bottom := renderInputBottom(m.width, border)

	var sb strings.Builder
	sb.WriteString(top)
	sb.WriteString("\n")
	for _, line := range innerLines {
		sb.WriteString(border.Render("│"))
		sb.WriteString(line)
		sb.WriteString(border.Render("│"))
		sb.WriteString("\n")
	}
	sb.WriteString(bottom)
	return sb.String()
}

// renderInputTop builds `┌─[ mode ]────...┐` using the mode-colored
// border characters. The label itself uses a brighter style so it pops
// against the dimmer border.
func renderInputTop(width int, border lipgloss.Style, label string, mode permission.Mode) string {
	if width <= 0 {
		return ""
	}
	if width < 4 {
		// Too narrow for the labelled form. Emit corners + as much fill
		// as we have room for; never go negative.
		fill := width - 2
		if fill < 0 {
			fill = 0
		}
		return border.Render("┌" + strings.Repeat("─", fill) + "┐")
	}
	tag := " " + label + " "
	tagStyle := labelStyleForMode(mode)
	rendered := border.Render("┌─[") + tagStyle.Render(tag) + border.Render("]")
	visible := 4 + lipgloss.Width(tag) // ┌─[ + tag + ]
	pad := width - visible - 1         // -1 for trailing ┐
	if pad < 0 {
		pad = 0
	}
	rendered += border.Render(strings.Repeat("─", pad) + "┐")
	return rendered
}

func renderInputBottom(width int, border lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	if width < 2 {
		return border.Render("└┘")
	}
	return border.Render("└" + strings.Repeat("─", width-2) + "┘")
}

// borderForMode returns the border style + label for the given permission
// mode. Bypass and acceptEdits get colored, attention-grabbing borders;
// default mode uses neutral grey.
func borderForMode(m permission.Mode) (style lipgloss.Style, label string) {
	switch m {
	case permission.ModeAcceptEdits:
		return modeAcceptBorderStyle, "acceptEdits"
	case permission.ModeBypass:
		return modeBypassBorderStyle, "bypass"
	default:
		return modeDefaultBorderStyle, "default"
	}
}

func labelStyleForMode(m permission.Mode) lipgloss.Style {
	switch m {
	case permission.ModeAcceptEdits:
		return modeAcceptLabelStyle
	case permission.ModeBypass:
		return modeBypassLabelStyle
	default:
		return modeDefaultLabelStyle
	}
}

// Value returns the current input text.
func (m *InputModel) Value() string { return m.textarea.Value() }

// Reset clears the input.
func (m *InputModel) Reset() { m.textarea.Reset() }

// SetValue overwrites the current input text. Used by the slash-command
// suggestion popup to insert a chosen command after the user accepts it.
func (m *InputModel) SetValue(s string) { m.textarea.SetValue(s) }

// MoveToEnd places the cursor after the last character. Pair with
// SetValue so a programmatic insert leaves the caret in a usable spot.
func (m *InputModel) MoveToEnd() { m.textarea.MoveToEnd() }

// SetWidth updates the textarea width. The textarea itself receives the
// inner width (window minus border columns).
func (m *InputModel) SetWidth(w int) {
	m.width = w
	inner := w - 2
	if inner < 1 {
		inner = 1
	}
	m.textarea.SetWidth(inner)
}

// SetDisabled enables or disables input.
func (m *InputModel) SetDisabled(d bool) {
	if d {
		m.textarea.Blur()
	} else {
		m.textarea.Focus()
	}
}

// SetMode updates the displayed mode. Caller should invoke whenever the
// permission mode changes (Ctrl+Y cycle, --mode boot flag, etc.).
func (m *InputModel) SetMode(mode permission.Mode) {
	m.mode = mode
}

// Focus returns the textarea's focus command.
func (m *InputModel) Focus() tea.Cmd {
	return m.textarea.Focus()
}
