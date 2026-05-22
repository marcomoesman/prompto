package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestMouseToggle_CtrlTFlipsCapture exercises the per-session mouse
// toggle. Default state is capture-on (so wheel scroll works); a
// Ctrl+T press flips it off, the next View() emits MouseModeNone,
// and a second press flips it back. Lets users select-and-copy text
// without recompiling or restarting.
func TestMouseToggle_CtrlTFlipsCapture(t *testing.T) {
	m := newTestAppModel(t)
	if !m.mouseCapture {
		t.Fatalf("default mouseCapture = false, want true (wheel scroll on by default)")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	got, ok := next.(AppModel)
	if !ok {
		t.Fatalf("Update returned %T, want AppModel", next)
	}
	if got.mouseCapture {
		t.Errorf("after Ctrl+T: mouseCapture still on; expected toggle off")
	}

	next, _ = got.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	got = next.(AppModel)
	if !got.mouseCapture {
		t.Errorf("after second Ctrl+T: mouseCapture off; expected toggle back on")
	}
}

// TestMouseToggle_ViewReflectsCaptureState verifies the View()
// MouseMode field follows the flag. With capture on, MouseMode must
// be CellMotion (so wheel events are reported). With capture off,
// MouseMode must be None so the terminal handles drag selection
// natively.
func TestMouseToggle_ViewReflectsCaptureState(t *testing.T) {
	m := newTestAppModel(t)
	resized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = resized.(AppModel)

	// Default: capture on.
	v := m.View()
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("default View.MouseMode = %v, want MouseModeCellMotion", v.MouseMode)
	}

	// Toggle off.
	m.mouseCapture = false
	v = m.View()
	if v.MouseMode != tea.MouseModeNone {
		t.Errorf("after toggle: View.MouseMode = %v, want MouseModeNone", v.MouseMode)
	}
}
