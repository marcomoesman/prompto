package tui

import (
	"strings"
	"testing"
)

func TestPlanApprovalModel_RendersBody(t *testing.T) {
	body := "# Title\n\n## Context\nwhy we ship\n\n## Files\nfoo.go\n"
	m := NewPlanApprovalModel("/proj", "/proj/.prompto/plans/2026-04-30-x.md", body)
	m.SetSize(80, 24)

	out := m.View()
	if out == "" {
		t.Fatal("View returned empty for non-empty body + non-zero size")
	}
	for _, want := range []string{"review plan:", "approve & switch", "keep iterating", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("View missing %q; got %q", want, snip(out))
		}
	}
}

func TestPlanApprovalModel_EmptyOnZeroSize(t *testing.T) {
	m := NewPlanApprovalModel("/proj", "/proj/.prompto/plans/x.md", "## Context\n")
	if got := m.View(); got != "" {
		t.Errorf("View on zero-size model = %q, want empty", got)
	}
}

func TestPlanApprovalModel_RelativePathInHeader(t *testing.T) {
	m := NewPlanApprovalModel("/proj", "/proj/.prompto/plans/2026-04-30-foo.md", "## x\n")
	m.SetSize(80, 24)
	out := m.View()
	if !strings.Contains(out, ".prompto/plans/2026-04-30-foo.md") {
		t.Errorf("header should carry the path relative to cwd; got %q", snip(out))
	}
	// Absolute path must NOT appear when a clean relative form exists.
	if strings.Contains(out, "/proj/.prompto") {
		t.Errorf("header showed absolute path instead of relative; got %q", snip(out))
	}
}

func TestPlanApprovalModel_AbsolutePathFallback(t *testing.T) {
	m := NewPlanApprovalModel("/proj", "/elsewhere/plan.md", "## x\n")
	m.SetSize(80, 24)
	if !strings.Contains(m.View(), "/elsewhere/plan.md") {
		t.Errorf("path outside cwd should fall back to absolute; got %q", snip(m.View()))
	}
}

func TestPlanApprovalModel_PlanPathExposed(t *testing.T) {
	const path = "/proj/.prompto/plans/2026-04-30-foo.md"
	m := NewPlanApprovalModel("/proj", path, "## x\n")
	if got := m.PlanPath(); got != path {
		t.Errorf("PlanPath() = %q, want %q", got, path)
	}
}

// snip trims rendered output for error messages so test failures
// don't dump 24 lines of styled markdown.
func snip(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "..."
}
