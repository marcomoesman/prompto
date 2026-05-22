package tui

import (
	"strings"
	"testing"
)

func TestRenderWelcome_VisibleAtNormalWidth(t *testing.T) {
	out := renderWelcome(80, WelcomeData{Version: "v0", Agent: "build", Model: "gpt-x"})
	if out == "" {
		t.Fatalf("expected banner content, got empty")
	}
	if !strings.Contains(out, "_") {
		t.Errorf("expected ASCII art block, got: %q", out)
	}
	if !strings.Contains(out, "build") {
		t.Errorf("expected agent name in banner, got: %q", out)
	}
	if !strings.Contains(out, "gpt-x") {
		t.Errorf("expected model name in banner, got: %q", out)
	}
}

func TestRenderWelcome_NarrowFallback(t *testing.T) {
	out := renderWelcome(40, WelcomeData{Version: "v0", Agent: "build", Model: "gpt-x"})
	if out == "" {
		t.Fatalf("expected fallback banner, got empty")
	}
	if strings.Contains(out, "____") {
		t.Errorf("expected no ASCII art at narrow width, got: %q", out)
	}
	if !strings.Contains(out, "prompto") {
		t.Errorf("expected compact banner header, got: %q", out)
	}
}

func TestRenderWelcome_ZeroWidthHidden(t *testing.T) {
	if got := renderWelcome(0, WelcomeData{Version: "v0"}); got != "" {
		t.Errorf("expected empty banner at width 0, got: %q", got)
	}
}

func TestWelcomeHeight_Thresholds(t *testing.T) {
	if got := welcomeHeight(0); got != 0 {
		t.Errorf("welcomeHeight(0) = %d, want 0", got)
	}
	if got := welcomeHeight(40); got != 5 {
		t.Errorf("welcomeHeight(40) = %d, want 5 (compact)", got)
	}
	if got := welcomeHeight(80); got != 11 {
		t.Errorf("welcomeHeight(80) = %d, want 11 (full)", got)
	}
}
