package agent

import (
	"context"

	"github.com/marcomoesman/prompto/internal/api"
)

// Compactor is the narrow interface the agent loop uses to decide whether
// to compact the conversation before each API call. The concrete impl
// lives in internal/compact; agent stays free of that import. A nil
// Compactor disables all compaction.
type Compactor interface {
	// MaybeCompact runs the pre-call decision (noop / clear / summarize).
	// Mutates conv in place when it acts. The returned result tells the
	// caller what happened; a non-nil SummaryMessage should be persisted
	// via Store so --resume knows about it.
	MaybeCompact(ctx context.Context, conv *Conversation, model string, resolver ToolResolver) CompactResult

	// ForceSummarize runs full summarization regardless of threshold.
	// Used by the reactive retry when a provider returns a context-limit
	// error. Mutates conv; returns the summary message and the id of the
	// last message in the replaced head so the caller can persist a
	// compaction marker via Store.AppendSummaryMessage.
	ForceSummarize(ctx context.Context, conv *Conversation, model string) (msg *api.Message, replacedThroughMessageID string, err error)
}

// CompactOutcome enumerates the possible results of MaybeCompact.
type CompactOutcome int

const (
	CompactOutcomeNoop       CompactOutcome = iota // conversation unchanged
	CompactOutcomeCleared                          // old tool_results blanked
	CompactOutcomeSummarized                       // head replaced with synthetic summary
)

// CompactResult carries what MaybeCompact did. Fields mirror the concrete
// compact.MaybeCompactResult; declared in agent so the interface contract
// doesn't leak compact-package types.
type CompactResult struct {
	Outcome        CompactOutcome
	TokensBefore   int
	TokensAfter    int
	Reason         string
	SummaryMessage *api.Message // populated when Outcome == CompactOutcomeSummarized
	// ReplacedThroughMessageID identifies the last message in the head slice
	// that the summary subsumes. The agent loop hands this to
	// Store.AppendSummaryMessage so resume can trim the conversation
	// correctly. Empty string means "no boundary persisted" — the caller
	// should fall back to plain AppendMessage.
	ReplacedThroughMessageID string
}
