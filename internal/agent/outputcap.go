package agent

import "fmt"

// DefaultMaxResultBytes is the per-tool output ceiling when a Tool reports
// MaxResultBytes() == 0. Chosen to match Claude Code's default.
const DefaultMaxResultBytes = 50 * 1024

// DefaultMaxTurnBytes is the per-turn aggregate ceiling. Sum of all
// tool_result content within one loop iteration.
const DefaultMaxTurnBytes = 200 * 1024

// suppressedStub replaces tool output once the per-turn aggregate cap has
// been hit entirely. The tool still executed (side effects happened); the
// model just can't see any more output this turn.
const suppressedStub = "[tool output suppressed: per-turn cap reached]"

// TurnAggregator enforces per-tool and per-turn byte caps on tool_result
// content over the course of one loop iteration. Construct a new one
// per turn, or call Reset between turns. Apply is called from the
// emission phase only (single-goroutine), so the underlying counter
// doesn't need a mutex; if a future caller dispatches Apply across
// goroutines, add one.
type TurnAggregator struct {
	limit int
	used  int
}

// NewTurnAggregator returns an aggregator with the given per-turn cap. A cap
// of 0 uses DefaultMaxTurnBytes.
func NewTurnAggregator(limit int) *TurnAggregator {
	if limit <= 0 {
		limit = DefaultMaxTurnBytes
	}
	return &TurnAggregator{limit: limit}
}

// Reset clears the used-byte counter so the next turn starts fresh.
func (a *TurnAggregator) Reset() { a.used = 0 }

// Apply enforces per-tool truncation first, then per-turn aggregate
// truncation. Returns the content that should be passed to the model as
// tool_result. perToolMax == 0 uses DefaultMaxResultBytes.
//
// Ordering:
//  1. If content > perToolMax, truncate to perToolMax + marker.
//  2. If aggregate cap is already exhausted, return the suppressed stub.
//  3. If the remaining budget is smaller than the (post-per-tool) content,
//     further truncate to fit with a marker.
//  4. Bump used by the length of the returned content.
func (a *TurnAggregator) Apply(content string, perToolMax int) string {
	if perToolMax <= 0 {
		perToolMax = DefaultMaxResultBytes
	}

	// Per-tool cap.
	if len(content) > perToolMax {
		dropped := len(content) - perToolMax
		content = content[:perToolMax] + fmt.Sprintf("\n[... %d bytes truncated ...]", dropped)
	}

	// Per-turn aggregate.
	if a.used >= a.limit {
		a.used += len(suppressedStub)
		return suppressedStub
	}

	remaining := a.limit - a.used
	if len(content) > remaining {
		dropped := len(content) - remaining
		content = content[:remaining] + fmt.Sprintf("\n[... %d bytes truncated ...]", dropped)
	}

	a.used += len(content)
	return content
}
