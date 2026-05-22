package tui

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/permission"
)

func TestInputBorder_LabelPerMode(t *testing.T) {
	cases := []struct {
		mode  permission.Mode
		label string
	}{
		{permission.ModeDefault, "default"},
		{permission.ModeAcceptEdits, "acceptEdits"},
		{permission.ModeBypass, "bypass"},
	}
	for _, c := range cases {
		input := NewInputModel()
		input.SetMode(c.mode)
		input.SetWidth(60)
		out := input.View()
		if !strings.Contains(out, c.label) {
			t.Errorf("mode %s: expected label %q in border, got: %q", c.mode, c.label, out)
		}
	}
}

func TestInputBorder_ColorDiffersPerMode(t *testing.T) {
	def := NewInputModel()
	def.SetMode(permission.ModeDefault)
	def.SetWidth(60)
	defOut := def.View()

	bypass := NewInputModel()
	bypass.SetMode(permission.ModeBypass)
	bypass.SetWidth(60)
	bypassOut := bypass.View()

	if defOut == bypassOut {
		t.Errorf("expected different ANSI output between default and bypass borders")
	}
}

func TestInputView_ZeroWidthDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero-width View panicked: %v", r)
		}
	}()
	input := NewInputModel()
	input.SetMode(permission.ModeDefault)
	// no SetWidth — width stays 0 (initial state before WindowSizeMsg)
	if got := input.View(); got != "" {
		t.Errorf("expected empty render at width 0, got: %q", got)
	}
}

func TestInputView_TinyWidthDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tiny-width View panicked: %v", r)
		}
	}()
	for _, w := range []int{1, 2, 3, 4} {
		input := NewInputModel()
		input.SetMode(permission.ModeDefault)
		input.SetWidth(w)
		_ = input.View()
	}
}

func TestInputBorder_TopAndBottomFrame(t *testing.T) {
	input := NewInputModel()
	input.SetMode(permission.ModeDefault)
	input.SetWidth(60)
	out := input.View()
	if !strings.Contains(out, "┌") || !strings.Contains(out, "┐") {
		t.Errorf("expected top corners ┌ ┐, got: %q", out)
	}
	if !strings.Contains(out, "└") || !strings.Contains(out, "┘") {
		t.Errorf("expected bottom corners └ ┘, got: %q", out)
	}
}
