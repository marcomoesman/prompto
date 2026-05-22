package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestTodoPanel_HiddenWhenEmpty(t *testing.T) {
	p := NewTodoPanel()
	p.SetWidth(80)
	if p.Visible() {
		t.Error("Visible() = true with no todos, want false")
	}
	if p.Height() != 0 {
		t.Errorf("Height() = %d, want 0", p.Height())
	}
	if got := p.View(newSpinner()); got != "" {
		t.Errorf("View() = %q, want empty", got)
	}
}

func TestTodoPanel_HiddenWhenAllCompleted(t *testing.T) {
	p := NewTodoPanel()
	p.SetWidth(120)
	p.SetTodos([]agent.Todo{
		{ID: "1", Content: "P1", Status: "completed"},
		{ID: "2", Content: "P2", Status: "completed"},
		{ID: "3", Content: "P3", Status: "completed"},
	})
	p.SetMeter(false, 0, 0)

	if p.Visible() {
		t.Error("Visible() = true when every todo is completed; panel should collapse")
	}
	if got := p.Height(); got != 0 {
		t.Errorf("Height() = %d when every todo is completed, want 0", got)
	}
	if got := p.View(newSpinner()); got != "" {
		t.Errorf("View() = %q when every todo is completed, want empty", got)
	}
}

func TestTodoPanel_RendersHeaderWhenStreaming(t *testing.T) {
	p := NewTodoPanel()
	p.SetWidth(120)
	p.SetTodos([]agent.Todo{
		{ID: "1", Content: "P1: refactor", Status: "in_progress", ActiveForm: "Refactoring app.go"},
		{ID: "2", Content: "P2: tests", Status: "pending"},
	})
	p.SetMeter(true, 47*time.Second, 1234)

	out := stripANSI(p.View(newSpinner()))
	if !strings.Contains(out, "Refactoring app.go") {
		t.Errorf("expected activeForm in header, got:\n%s", out)
	}
	if !strings.Contains(out, "47s") {
		t.Errorf("expected elapsed in header, got:\n%s", out)
	}
	if !strings.Contains(out, "↑ 1.2k") {
		t.Errorf("expected token count in header, got:\n%s", out)
	}
}

func TestTodoPanel_HidesHeaderWhenIdle(t *testing.T) {
	p := NewTodoPanel()
	p.SetWidth(120)
	p.SetTodos([]agent.Todo{
		{ID: "1", Content: "P1: refactor", Status: "in_progress", ActiveForm: "Refactoring"},
	})
	p.SetMeter(false, 0, 1234)

	out := stripANSI(p.View(newSpinner()))
	if strings.Contains(out, "Refactoring") {
		t.Errorf("idle header line should be hidden; got:\n%s", out)
	}
	if !strings.Contains(out, "■ P1: refactor") {
		t.Errorf("in-progress body row should still render when idle; got:\n%s", out)
	}
}

func TestTodoPanel_ShowsMostRecentCompletedAndAllPending(t *testing.T) {
	p := NewTodoPanel()
	p.SetWidth(120)
	p.SetTodos([]agent.Todo{
		{ID: "1", Content: "P1", Status: "completed"},
		{ID: "2", Content: "P2", Status: "completed"},
		{ID: "3", Content: "P3", Status: "completed"},
		{ID: "4", Content: "P4 current", Status: "in_progress", ActiveForm: "Doing P4"},
		{ID: "5", Content: "P5", Status: "pending"},
		{ID: "6", Content: "P6", Status: "pending"},
		{ID: "7", Content: "P7", Status: "pending"},
	})
	p.SetMeter(false, 0, 0)

	out := stripANSI(p.View(newSpinner()))

	// Most recent completed = the LAST completed in the list (P3).
	if !strings.Contains(out, "✓ P3") {
		t.Errorf("expected most-recent completed (✓ P3) in render; got:\n%s", out)
	}
	// Earlier completed must be rolled up, not rendered inline.
	if strings.Contains(out, "✓ P1") || strings.Contains(out, "✓ P2") {
		t.Errorf("earlier completed should be rolled up, not inlined; got:\n%s", out)
	}
	if !strings.Contains(out, "+2 completed") {
		t.Errorf("expected '+2 completed' rollup; got:\n%s", out)
	}
	if !strings.Contains(out, "■ P4 current") {
		t.Errorf("expected in-progress body row; got:\n%s", out)
	}
	for _, p := range []string{"□ P5", "□ P6", "□ P7"} {
		if !strings.Contains(out, p) {
			t.Errorf("expected pending row %q; got:\n%s", p, out)
		}
	}
}

func TestTodoPanel_PendingTruncationWithRollup(t *testing.T) {
	todos := make([]agent.Todo, 0, 30)
	todos = append(todos, agent.Todo{ID: "x", Content: "current", Status: "in_progress", ActiveForm: "Working"})
	for i := 0; i < 30; i++ {
		todos = append(todos, agent.Todo{ID: "p", Content: "pending item", Status: "pending"})
	}

	p := NewTodoPanel()
	p.SetWidth(120)
	p.SetTodos(todos)

	if got := p.Height(); got > todoPanelPendingSoftCap+3 {
		t.Errorf("Height() = %d, exceeds soft cap; oversized panel will eat the screen", got)
	}
	out := stripANSI(p.View(newSpinner()))
	overflow := 30 - todoPanelPendingSoftCap
	if !strings.Contains(out, "more pending") {
		t.Errorf("expected pending overflow rollup; got:\n%s", out)
	}
	wantStr := "+" + itoa(overflow) + " more pending"
	if !strings.Contains(out, wantStr) {
		t.Errorf("expected %q in overflow line; got:\n%s", wantStr, out)
	}
}

func TestTodoPanel_WidthTruncationDoesNotWrap(t *testing.T) {
	long := strings.Repeat("X", 500)
	p := NewTodoPanel()
	p.SetWidth(40)
	p.SetTodos([]agent.Todo{
		{ID: "1", Content: long, Status: "in_progress", ActiveForm: long},
	})
	p.SetMeter(true, 5*time.Second, 100)

	out := p.View(newSpinner())
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := visibleWidth(line); w > 40 {
			t.Errorf("line wider than panel width 40: w=%d line=%q", w, line)
		}
	}
}

func TestTodoPanel_HeaderFallbackWhenNoInProgress(t *testing.T) {
	p := NewTodoPanel()
	p.SetWidth(120)
	p.SetTodos([]agent.Todo{
		{ID: "1", Content: "P1", Status: "pending"},
	})
	p.SetMeter(true, 5*time.Second, 200)

	out := stripANSI(p.View(newSpinner()))
	if !strings.Contains(out, "Working…") {
		t.Errorf("expected 'Working…' fallback header when streaming with no in_progress; got:\n%s", out)
	}
}

// visibleWidth returns the rendered cell width of s after ANSI escapes
// are stripped. lipgloss.Width handles this directly but the helper
// keeps the test independent of that import.
func visibleWidth(s string) int {
	stripped := stripANSI(s)
	// Approximate: most monospace cells are 1 rune. The panel doesn't
	// render wide CJK or emoji glyphs in its frame, so this is fine
	// for the truncation test; if that changes, swap to
	// lipgloss.Width.
	return len([]rune(stripped))
}

// itoa is a tiny helper to keep imports minimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 4)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
