package tui

import (
	"testing"
	"time"
)

// TestThinkingTimer_PausesDuringApproval is the behavioral test for the
// thinking-time semantics: time that passes while the timer is paused
// must not be reflected in thinkingElapsed.
func TestThinkingTimer_PausesDuringApproval(t *testing.T) {
	var m AppModel
	t0 := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)

	m.startThinking(t0)

	// 5s of active thinking.
	if got := m.thinkingElapsed(t0.Add(5 * time.Second)); got != 5*time.Second {
		t.Fatalf("after 5s active, elapsed = %v, want 5s", got)
	}

	// Approval prompt arrives at t0+5s, user takes 60s to decide.
	m.pauseThinking(t0.Add(5 * time.Second))

	// Polled mid-pause — elapsed must remain frozen.
	if got := m.thinkingElapsed(t0.Add(30 * time.Second)); got != 5*time.Second {
		t.Fatalf("during pause, elapsed = %v, want 5s (frozen)", got)
	}
	if got := m.thinkingElapsed(t0.Add(65 * time.Second)); got != 5*time.Second {
		t.Fatalf("during long pause, elapsed = %v, want 5s (frozen)", got)
	}

	// User approves at t0+65s; agent resumes work.
	m.resumeThinking(t0.Add(65 * time.Second))

	// 3 more seconds of active thinking → total 8s, not 68s.
	if got := m.thinkingElapsed(t0.Add(68 * time.Second)); got != 8*time.Second {
		t.Fatalf("after resume + 3s, elapsed = %v, want 8s", got)
	}
}

// TestThinkingTimer_DoublePauseAndResumeAreNoOps protects against
// duplicate transitions inflating or deflating the count.
func TestThinkingTimer_DoublePauseAndResumeAreNoOps(t *testing.T) {
	var m AppModel
	t0 := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	m.startThinking(t0)

	m.pauseThinking(t0.Add(10 * time.Second))
	// Second pause must not bank again.
	m.pauseThinking(t0.Add(20 * time.Second))
	if got := m.thinkingElapsed(t0.Add(30 * time.Second)); got != 10*time.Second {
		t.Fatalf("after double pause, elapsed = %v, want 10s", got)
	}

	m.resumeThinking(t0.Add(30 * time.Second))
	// Second resume must not reset the active-interval start.
	m.resumeThinking(t0.Add(40 * time.Second))
	// 30s elapsed since first resume → 10s banked + 30s active = 40s.
	if got := m.thinkingElapsed(t0.Add(60 * time.Second)); got != 40*time.Second {
		t.Fatalf("after double resume, elapsed = %v, want 40s", got)
	}
}

// TestThinkingTimer_StartResetsAccum verifies a fresh turn discards
// any leftover state from a prior turn.
func TestThinkingTimer_StartResetsAccum(t *testing.T) {
	var m AppModel
	m.thinkingAccum = 99 * time.Second
	m.thinkingResumedAt = time.Time{}

	t0 := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	m.startThinking(t0)
	if got := m.thinkingElapsed(t0); got != 0 {
		t.Fatalf("startThinking did not zero accum: elapsed = %v, want 0", got)
	}
}

// TestThinkingPromptism_RotatesOnLongBurst asserts maybeRotatePromptism
// swaps in a fresh word once the current pick has been on screen
// past promptismRotateAfter. The spinner-tick handler calls this
// every frame, but only the post-window call should mutate state —
// otherwise the indicator would flicker on every tick.
func TestThinkingPromptism_RotatesOnLongBurst(t *testing.T) {
	m := newTestAppModel(t)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Transition into Thinking. setState picks word and stamps t0;
	// since setState uses time.Now() we pin the timestamp manually
	// to make the rotation window deterministic.
	m.setState(StateThinking, "")
	m.thinkingWordPickedAt = t0
	first := m.thinkingWord

	// Tick before the rotation window: word must NOT change.
	m.maybeRotatePromptism(t0.Add(promptismRotateAfter - time.Second))
	if m.thinkingWord != first {
		t.Errorf("rotated before the window expired: %q → %q", first, m.thinkingWord)
	}
	if !m.thinkingWordPickedAt.Equal(t0) {
		t.Errorf("pickedAt mutated before window expired: %v → %v", t0, m.thinkingWordPickedAt)
	}

	// Tick at the rotation window: must roll, must pick a different
	// word (pickPromptismExcluding contract), must reset pickedAt.
	rotatedAt := t0.Add(promptismRotateAfter)
	m.maybeRotatePromptism(rotatedAt)
	if m.thinkingWord == first {
		t.Errorf("rotation produced the same word %q (excluding-current contract violated)", first)
	}
	if !m.thinkingWordPickedAt.Equal(rotatedAt) {
		t.Errorf("pickedAt not reset to rotation time: got %v, want %v", m.thinkingWordPickedAt, rotatedAt)
	}

	// Outside StateThinking the rotator must be a no-op even past the
	// window — the indicator isn't visible there, and rolling would
	// just churn state for no user benefit.
	m.setState(StateToolRunning, "Read foo.go")
	stable := m.thinkingWord
	m.maybeRotatePromptism(rotatedAt.Add(10 * promptismRotateAfter))
	if m.thinkingWord != stable {
		t.Errorf("rotator mutated thinkingWord while in StateToolRunning: %q → %q", stable, m.thinkingWord)
	}
}

// TestThinkingPromptism_PickedOnTransitionAndSticky asserts setState
// rolls a fresh promptism on every transition INTO StateThinking and
// keeps the choice stable across re-entries from non-thinking states.
// The stickiness invariant is what prevents the indicator from
// flickering through the list on every spinner tick.
func TestThinkingPromptism_PickedOnTransitionAndSticky(t *testing.T) {
	allowed := make(map[string]struct{}, len(promptisms))
	for _, w := range promptisms {
		allowed[w] = struct{}{}
	}

	m := newTestAppModel(t)
	if m.thinkingWord != "" {
		t.Fatalf("fresh AppModel should have no thinkingWord; got %q", m.thinkingWord)
	}

	// First entry: idle → Thinking. Picks a word.
	m.setState(StateThinking, "")
	first := m.thinkingWord
	if _, ok := allowed[first]; !ok {
		t.Fatalf("first promptism = %q, not in promptisms list", first)
	}

	// While already in StateThinking, re-calling setState(StateThinking)
	// must NOT roll again — the indicator would flicker between renders.
	m.setState(StateThinking, "")
	if m.thinkingWord != first {
		t.Errorf("promptism changed on same-state setState: was %q, now %q", first, m.thinkingWord)
	}

	// Transition out → tool running. Word stays as last pick (it's not
	// rendered while non-thinking; clearing it would just complicate the
	// re-entry logic).
	m.setState(StateToolRunning, "Read foo.go")
	if m.thinkingWord != first {
		t.Errorf("promptism mutated on exit from StateThinking: %q → %q", first, m.thinkingWord)
	}

	// Transition back to Thinking. Must roll again — same value is
	// allowed (uniform random over 11 entries), but the path executed.
	m.setState(StateThinking, "")
	if _, ok := allowed[m.thinkingWord]; !ok {
		t.Fatalf("re-entry promptism = %q, not in promptisms list", m.thinkingWord)
	}
}
