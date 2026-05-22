package main

import (
	"context"
	"iter"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/marcomoesman/prompto/internal/api"
)

// stubSummarizerProvider replays a fixed event sequence to the
// summarizer. Sufficient because newSummarizer only consumes
// Complete()'s iterator and ignores any other Provider behaviour.
type stubSummarizerProvider struct {
	events []api.StreamEvent
	// gotParams captures the CompleteParams the summarizer used so a
	// test can assert the right MaxTokens was forwarded.
	gotParams *api.CompleteParams
}

func (stubSummarizerProvider) ContextLimit(string) int { return 0 }
func (s *stubSummarizerProvider) Complete(_ context.Context, params api.CompleteParams) iter.Seq[api.StreamEvent] {
	if s.gotParams != nil {
		*s.gotParams = params
	}
	return func(yield func(api.StreamEvent) bool) {
		for _, e := range s.events {
			if !yield(e) {
				return
			}
		}
	}
}

func TestSummarizer_ForwardsMaxTokensToProvider(t *testing.T) {
	var captured api.CompleteParams
	prov := &stubSummarizerProvider{
		events: []api.StreamEvent{
			{Type: api.EventDelta, Delta: "ok"},
			{Type: api.EventDone, StopReason: "stop"},
		},
		gotParams: &captured,
	}
	if _, err := newSummarizer(prov, "model-x", 8192)(context.Background(), "raw", "q"); err != nil {
		t.Fatalf("summarizer error: %v", err)
	}
	if captured.MaxTokens != 8192 {
		t.Errorf("MaxTokens forwarded = %d, want 8192", captured.MaxTokens)
	}
	if captured.Model != "model-x" {
		t.Errorf("Model forwarded = %q, want model-x", captured.Model)
	}
}

func TestSummarizer_AppendsTruncationNoteOnLengthStop(t *testing.T) {
	cases := []struct {
		name       string
		stopReason string
		wantNote   bool
	}{
		{"openai_length", "length", true},
		{"anthropic_max_tokens", "max_tokens", true},
		{"clean_stop", "stop", false},
		{"anthropic_end_turn", "end_turn", false},
		{"empty_stop_reason", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prov := &stubSummarizerProvider{events: []api.StreamEvent{
				{Type: api.EventDelta, Delta: "partial answer"},
				{Type: api.EventDone, StopReason: c.stopReason},
			}}
			got, err := newSummarizer(prov, "model-x", 4096)(context.Background(), "raw page", "extract everything")
			if err != nil {
				t.Fatalf("summarizer returned error: %v", err)
			}
			hasNote := strings.HasSuffix(got, lengthTruncatedNote)
			if hasNote != c.wantNote {
				t.Errorf("stop=%q hasNote=%v want=%v\nresult: %q", c.stopReason, hasNote, c.wantNote, got)
			}
			if !strings.HasPrefix(got, "partial answer") {
				t.Errorf("expected output to begin with model answer, got: %q", got)
			}
		})
	}
}

// TestSummarizer_NoNoteOnEmptyOrErrorPaths protects the rule that the
// truncation note is only attached to a successful (non-empty) result.
// Empty / error responses already get truncateFallback's own preamble.
func TestSummarizer_NoNoteOnEmptyOrErrorPaths(t *testing.T) {
	prov := &stubSummarizerProvider{events: []api.StreamEvent{
		// No EventDelta — empty result. EventDone with "length" must
		// NOT cause the note to be appended; the empty-result fallback
		// owns the message in this branch.
		{Type: api.EventDone, StopReason: "length"},
	}}
	got, err := newSummarizer(prov, "model-x", 4096)(context.Background(), "raw page", "q")
	if err != nil {
		t.Fatalf("summarizer returned error: %v", err)
	}
	if strings.Contains(got, lengthTruncatedNote) {
		t.Errorf("empty-result path should not append truncation note, got: %q", got)
	}
}

// TestSummarizer_TruncatesOnRuneBoundary verifies that an oversized
// input cut at summarizeMaxInput doesn't split a multi-byte UTF-8 rune
// at the seam. Without rune-aware truncation, JSON encoding the request
// body silently substitutes U+FFFD for the orphaned half-rune.
func TestSummarizer_TruncatesOnRuneBoundary(t *testing.T) {
	// Build content where a 4-byte rune ("𝄞", U+1D11E) straddles the
	// summarizeMaxInput cut point. Pad with ASCII so the rune's first
	// byte lands at index summarizeMaxInput-1 and bytes 2-4 sit past
	// the cap. A naive `[:summarizeMaxInput]` slice would keep byte 1
	// of the rune and drop bytes 2-4, producing invalid UTF-8.
	prefix := strings.Repeat("a", summarizeMaxInput-1)
	content := prefix + "𝄞" + strings.Repeat("b", 100)

	prov := &stubSummarizerProvider{
		events: []api.StreamEvent{{Type: api.EventDone, StopReason: "stop"}},
	}
	var captured api.CompleteParams
	prov.gotParams = &captured

	if _, err := newSummarizer(prov, "model-x", 1024)(context.Background(), content, ""); err != nil {
		t.Fatalf("summarizer error: %v", err)
	}

	// The user message text is "<prompt>\n\n---\n\n<truncatedContent>".
	// Asserting valid UTF-8 over the whole string is the property that
	// matters; if the inner truncatedContent is invalid UTF-8, so is
	// the wrapper.
	sent := captured.Messages[0].Text()
	if !utf8.ValidString(sent) {
		t.Fatalf("truncated input is not valid UTF-8 — rune boundary split at the cut")
	}
	// And the orphaned tail-rune bytes must be gone (they sit past the
	// cap; truncation should drop them, not retain a half-rune).
	if strings.Contains(sent, "𝄞") {
		t.Errorf("expected the boundary rune to be dropped (truncated before it), but it survived")
	}
}

func TestIsLengthStop(t *testing.T) {
	cases := map[string]bool{
		"length":     true,
		"max_tokens": true,
		"stop":       false,
		"end_turn":   false,
		"tool_use":   false,
		"tool_calls": false,
		"":           false,
		"LENGTH":     false, // case-sensitive: providers emit lowercase
	}
	for in, want := range cases {
		if got := isLengthStop(in); got != want {
			t.Errorf("isLengthStop(%q) = %v, want %v", in, got, want)
		}
	}
}
