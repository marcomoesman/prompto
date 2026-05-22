package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
)

// PlanApprovalModel renders the full-screen overlay shown when the
// plan agent calls plan_exit. The plan markdown is rendered through
// glamour for visual parity with chat; long plans scroll under
// PgUp/PgDn/j/k. The user picks Y/N at the footer to drive the
// underlying CanUseTool Done channel.
//
// Construction takes the absolute plan path and the raw markdown
// body. The relative path (vs cwd) is computed lazily for the
// header. Width/height arrive via SetSize from AppModel.relayout.
type PlanApprovalModel struct {
	planPath string
	relPath  string
	body     string
	rendered string
	viewport viewport.Model
	width    int
	height   int
	dark     bool
}

// NewPlanApprovalModel builds the overlay for the given plan file.
// cwd is used to compute a short relative path for the header;
// when the plan path lies outside cwd the absolute path is used
// instead. The body is the raw markdown that the validator already
// approved (plan_exit pre-flight).
//
// The displayed path is normalized to forward slashes regardless of
// host OS — `.prompto/plans/...` is a project-shape convention, and
// rendering `\` on Windows looks alien while breaking string
// assertions in tests.
func NewPlanApprovalModel(cwd, planPath, body string) PlanApprovalModel {
	rel := planPath
	if cwd != "" {
		if r, err := filepath.Rel(cwd, planPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	rel = filepath.ToSlash(rel)
	vp := viewport.New()
	vp.SoftWrap = true
	return PlanApprovalModel{
		planPath: planPath,
		relPath:  rel,
		body:     body,
		viewport: vp,
		dark:     true,
	}
}

// SetDark mirrors the chat's dark/light background toggle so the
// markdown renderer picks a matching glamour style.
func (m *PlanApprovalModel) SetDark(dark bool) {
	if m.dark == dark {
		return
	}
	m.dark = dark
	m.render()
}

// SetSize updates layout dimensions and re-renders the body so
// glamour can re-wrap to the new width.
func (m *PlanApprovalModel) SetSize(width, height int) {
	if width == m.width && height == m.height {
		return
	}
	m.width = width
	m.height = height
	// Reserve rows for the header (1), divider (1), footer (1),
	// and panel border + padding (4). The viewport gets whatever
	// remains.
	const chrome = 7
	innerW := width - 4 // border (2) + padding (2)
	if innerW < 1 {
		innerW = 1
	}
	innerH := height - chrome
	if innerH < 1 {
		innerH = 1
	}
	m.viewport.SetWidth(innerW)
	m.viewport.SetHeight(innerH)
	m.render()
}

// Update routes scroll keys (PgUp/PgDn/j/k/up/down) to the
// viewport. Y/N/Esc are handled by the AppModel keymap, not here.
func (m PlanApprovalModel) Update(msg tea.Msg) (PlanApprovalModel, tea.Cmd) {
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// PlanPath exposes the absolute path of the plan being approved.
// AppModel reads it after the user clicks Y to flip the agent.
func (m *PlanApprovalModel) PlanPath() string { return m.planPath }

// View renders the centred panel.
func (m PlanApprovalModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	header := planApprovalHeaderStyle.Render("review plan: ") +
		planApprovalPathStyle.Render(m.relPath)
	footer := planApprovalFooterStyle.Render(
		"[y] approve & switch to build  ·  [n] keep iterating  ·  esc cancel  ·  ↑↓/PgUp/PgDn scroll",
	)

	body := m.viewport.View()

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		body,
		"",
		footer,
	)

	panel := planApprovalPanelStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// render re-wraps body via glamour for the current width and pushes
// it into the viewport. Falls back to the raw body on render
// failure so the overlay never goes blank.
func (m *PlanApprovalModel) render() {
	if m.body == "" || m.viewport.Width() <= 0 {
		return
	}
	style := "light"
	if m.dark {
		style = "dark"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(style),
		glamour.WithWordWrap(m.viewport.Width()),
	)
	if err != nil {
		m.rendered = m.body
		m.viewport.SetContent(m.rendered)
		return
	}
	out, err := r.Render(m.body)
	if err != nil {
		m.rendered = m.body
	} else {
		m.rendered = strings.TrimRight(out, "\n")
	}
	m.viewport.SetContent(m.rendered)
}

// describe returns a one-line summary for chat / system message
// when the user accepts. Used by AppModel after the agent flip.
func (m *PlanApprovalModel) describe() string {
	return fmt.Sprintf("plan approved: %s", m.relPath)
}
