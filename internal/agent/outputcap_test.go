package agent

import (
	"strings"
	"testing"
)

func TestTurnAggregator_PerToolTruncates(t *testing.T) {
	a := NewTurnAggregator(0)
	content := strings.Repeat("x", 60*1024)
	out := a.Apply(content, 50*1024)
	if len(out) <= 50*1024 {
		t.Errorf("output truncated to <=%d bytes? got %d", 50*1024, len(out))
	}
	if !strings.Contains(out, "bytes truncated") {
		t.Error("missing truncation marker")
	}
	if !strings.HasPrefix(out, strings.Repeat("x", 50*1024)) {
		t.Error("prefix should be 50KB of x")
	}
}

func TestTurnAggregator_PerToolCapZeroUsesDefault(t *testing.T) {
	a := NewTurnAggregator(0)
	content := strings.Repeat("x", 60*1024) // > default 50KB
	out := a.Apply(content, 0)
	if !strings.Contains(out, "bytes truncated") {
		t.Error("expected truncation at default cap")
	}
}

func TestTurnAggregator_PerTurnAccumulates(t *testing.T) {
	a := NewTurnAggregator(100 * 1024)

	first := a.Apply(strings.Repeat("a", 80*1024), 100*1024) // under 100KB cap entirely
	if strings.Contains(first, "bytes truncated") {
		t.Errorf("first call should be untruncated, got marker")
	}

	second := a.Apply(strings.Repeat("b", 80*1024), 100*1024)
	if !strings.Contains(second, "bytes truncated") {
		t.Errorf("second call should hit aggregate cap, missing marker; len=%d", len(second))
	}
}

func TestTurnAggregator_SuppressesOnceExhausted(t *testing.T) {
	a := NewTurnAggregator(50 * 1024)
	_ = a.Apply(strings.Repeat("a", 60*1024), 100*1024) // fills the budget
	next := a.Apply("something", 100*1024)
	if next != suppressedStub {
		t.Errorf("expected suppressed stub, got %q", next)
	}
}

func TestTurnAggregator_ResetClearsUsed(t *testing.T) {
	a := NewTurnAggregator(50 * 1024)
	_ = a.Apply(strings.Repeat("a", 60*1024), 100*1024)
	a.Reset()
	out := a.Apply("tiny", 100*1024)
	if out != "tiny" {
		t.Errorf("after Reset expected 'tiny', got %q", out)
	}
}

func TestTurnAggregator_ExactFitNoTruncation(t *testing.T) {
	a := NewTurnAggregator(100)
	out := a.Apply(strings.Repeat("x", 100), 200)
	if out != strings.Repeat("x", 100) {
		t.Errorf("exact-fit content was modified: got len=%d", len(out))
	}
	if strings.Contains(out, "bytes truncated") {
		t.Error("exact-fit shouldn't carry truncation marker")
	}
}

func TestTurnAggregator_PerToolAppliedBeforeAggregate(t *testing.T) {
	a := NewTurnAggregator(1000)
	// per-tool cap of 100 applies first; then 100 bytes go through aggregate.
	out := a.Apply(strings.Repeat("x", 500), 100)
	if !strings.Contains(out, "bytes truncated") {
		t.Error("expected per-tool truncation marker")
	}
}
