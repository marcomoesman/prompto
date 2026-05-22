package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/marcomoesman/prompto/internal/command"
)

// ModelPickerModel renders an arrow-key-driven model selection panel
// in the chat region while AppModel.modelPickerVisible is true. Same
// overlay idiom as the /help panel; differs in that it captures
// keystrokes (j/k/up/down/enter/esc) and writes the user's choice
// back through the host's SetModel hook.
//
// The model is constructed lazily from env.Models() each time the
// picker opens, so additions to config show up without needing a
// restart. Cursor position resets to the active model on open.
type ModelPickerModel struct {
	models        []command.ModelInfo
	cursor        int
	settingCursor int
	settings      bool
	current       string // active model name when the picker opened
	width         int
	height        int
}

// NewModelPickerModel builds a fresh picker. current marks which row
// gets the active-model glyph; the cursor starts there so Enter
// without movement is a no-op (handy for "what model am I on?"
// glances that don't intend to switch).
func NewModelPickerModel(models []command.ModelInfo, current string) ModelPickerModel {
	cursor := 0
	for i, m := range models {
		if m.Name == current {
			cursor = i
			break
		}
	}
	return ModelPickerModel{models: models, cursor: cursor, current: current}
}

// SetSize updates layout dimensions. The panel auto-sizes to its
// content; SetSize controls the centred placement bounds.
func (m *ModelPickerModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Move adjusts the cursor by delta, clamping to the bounds. Used by
// the host's KeyPressMsg dispatch.
func (m *ModelPickerModel) Move(delta int) {
	if m.settings {
		m.settingCursor += delta
		if m.settingCursor < 0 {
			m.settingCursor = 0
		}
		if m.settingCursor > 1 {
			m.settingCursor = 1
		}
		return
	}
	if len(m.models) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.models) {
		m.cursor = len(m.models) - 1
	}
}

func (m *ModelPickerModel) ToggleSettings() {
	m.settings = !m.settings
}

func (m *ModelPickerModel) AdjustSetting(delta float64) (command.ModelInfo, bool) {
	if len(m.models) == 0 || !m.settings {
		return command.ModelInfo{}, false
	}
	mi := m.models[m.cursor]
	if mi.ProviderKind != "openai" {
		return mi, false
	}
	switch m.settingCursor {
	case 0:
		mi.Temperature = clampRound1(mi.Temperature+delta, 0, 2)
		mi.TemperatureConfigured = true
	case 1:
		mi.PresencePenalty = clampRound1(mi.PresencePenalty+delta, -2, 2)
		mi.PresencePenaltyConfigured = true
	}
	m.models[m.cursor] = mi
	return mi, true
}

func (m *ModelPickerModel) ResetSettings() (command.ModelInfo, bool) {
	if len(m.models) == 0 || !m.settings {
		return command.ModelInfo{}, false
	}
	mi := m.models[m.cursor]
	mi.Temperature = 1.0
	mi.PresencePenalty = 0.0
	mi.TemperatureConfigured = false
	mi.PresencePenaltyConfigured = false
	m.models[m.cursor] = mi
	return mi, true
}

// Selected returns the currently highlighted model, or zero value
// when the picker has no rows.
func (m *ModelPickerModel) Selected() command.ModelInfo {
	if len(m.models) == 0 {
		return command.ModelInfo{}
	}
	return m.models[m.cursor]
}

// View renders the centred panel.
func (m ModelPickerModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	var b strings.Builder
	title := "select a model"
	if m.settings {
		title = "model settings"
	}
	b.WriteString(pickerHeaderStyle.Render(title))
	b.WriteString("\n\n")

	if len(m.models) == 0 {
		b.WriteString(pickerMetaStyle.Render("(no models configured — see ~/.config/prompto/config.json)"))
	} else {
		if m.settings {
			m.renderSettings(&b)
		} else {
			m.renderModelList(&b)
		}
	}

	b.WriteByte('\n')
	if m.settings {
		b.WriteString(pickerHintStyle.Render("↑↓ / j k  knob    ←→ / h l  adjust    r  reset    tab  models    esc  close"))
	} else {
		b.WriteString(pickerHintStyle.Render("↑↓ / j k  navigate    enter  switch    tab / s  settings    esc  cancel"))
	}

	panel := pickerPanelStyle.Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

func (m ModelPickerModel) renderModelList(b *strings.Builder) {
	nameW := 0
	for _, mi := range m.models {
		if n := lipgloss.Width(mi.Name); n > nameW {
			nameW = n
		}
	}

	for i, mi := range m.models {
		active := mi.Name == m.current
		selected := i == m.cursor

		marker := "  "
		if active {
			marker = pickerActiveMarkStyle.Render("● ")
		}
		pointer := "  "
		if selected {
			pointer = pickerSelectedStyle.Render("▸ ")
		}

		name := padRight(mi.Name, nameW)
		meta := fmt.Sprintf("  %s · max_tokens %d", mi.Provider, mi.MaxTokens)

		var nameRendered, metaRendered string
		if selected {
			nameRendered = pickerSelectedStyle.Render(name)
			metaRendered = pickerMetaStyle.Render(meta)
		} else {
			nameRendered = pickerNameStyle.Render(name)
			metaRendered = pickerMetaStyle.Render(meta)
		}

		row := pointer + marker + nameRendered + metaRendered
		if selected {
			row = pickerSelectedBg.Render(row)
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
}

func (m ModelPickerModel) renderSettings(b *strings.Builder) {
	mi := m.Selected()
	b.WriteString(pickerNameStyle.Render(mi.Name))
	b.WriteString(pickerMetaStyle.Render(fmt.Sprintf("  %s", mi.Provider)))
	b.WriteString("\n\n")
	rows := []struct {
		label string
		value string
	}{
		{"temperature", settingValue(mi.Temperature, mi.TemperatureConfigured)},
		{"presence penalty", settingValue(mi.PresencePenalty, mi.PresencePenaltyConfigured)},
	}
	if mi.ProviderKind != "openai" {
		b.WriteString(pickerMetaStyle.Render("sampling controls are not sent for this provider"))
		b.WriteString("\n")
	}
	for i, row := range rows {
		pointer := "  "
		if i == m.settingCursor {
			pointer = pickerSelectedStyle.Render("▸ ")
		}
		line := fmt.Sprintf("%s%-18s %5s", pointer, row.label, row.value)
		if i == m.settingCursor {
			line = pickerSelectedBg.Render(pickerSelectedStyle.Render(line))
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func clampRound1(v, min, max float64) float64 {
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return math.Round(v*10) / 10
}

func settingValue(v float64, configured bool) string {
	if configured {
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.1f default", v)
}
