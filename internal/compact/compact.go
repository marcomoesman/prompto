package compact

import (
	"context"
	"fmt"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// lightTriggerPct is the estimated-usage percentage at which the lighter
// tool-result-clearing layer fires. Summarization fires at ThresholdPct
// (usually 80); below that but above lightTriggerPct we clear old
// read-only tool_results to buy more room before summarization is needed.
const lightTriggerPct = 60

// Compactor orchestrates pre-call compaction + reactive force-summarize.
// Construct via New; inject into agent.NewAgentInput.Compactor. All
// exported methods are safe for single-goroutine use from runLoop.
type Compactor struct {
	provider           api.Provider
	defaultLimit       int // fallback context ceiling when provider reports 0
	maxOverride        int // hard cap on any provider-reported limit
	thresholdPct       int // summarization trigger, % of effective limit
	keepRecentMessages int // messages preserved verbatim at the tail
	summarizerModel    string
	// modelContextLimit, when non-nil, returns the user-configured
	// per-model context limit in tokens (or 0 when none is set). It
	// takes precedence over the provider's reported value — the user
	// gets the final say. nil disables the lookup (subagent / test
	// paths that never threaded a config through).
	modelContextLimit func(model string) int
}

// NewInput bundles the inputs to New. All tunables have sensible defaults
// applied when zero.
type NewInput struct {
	Provider     api.Provider
	DefaultLimit int // default 200000
	MaxOverride  int // default 400000
	ThresholdPct int // default 80
	// KeepRecentMessages preserves the last N messages (not turns)
	// at the tail when summarizing the head. Default 8.
	KeepRecentMessages int
	SummarizerModel    string // optional override; falls back to session model per-call
	// ModelContextLimit, when non-nil, is consulted before
	// Provider.ContextLimit to resolve the operative ceiling. Wired
	// from config (per-model context_limit) by main.go; tests and
	// subagent paths that don't need it pass nil.
	ModelContextLimit func(model string) int
}

// New constructs a Compactor. A nil Provider panics — callers must wire
// one in. Other zero values get defaults.
func New(in NewInput) *Compactor {
	if in.Provider == nil {
		panic("compact.New: Provider is required")
	}
	c := &Compactor{
		provider:           in.Provider,
		defaultLimit:       in.DefaultLimit,
		maxOverride:        in.MaxOverride,
		thresholdPct:       in.ThresholdPct,
		keepRecentMessages: in.KeepRecentMessages,
		summarizerModel:    in.SummarizerModel,
		modelContextLimit:  in.ModelContextLimit,
	}
	if c.defaultLimit == 0 {
		c.defaultLimit = 200_000
	}
	if c.maxOverride == 0 {
		c.maxOverride = 400_000
	}
	if c.thresholdPct == 0 {
		c.thresholdPct = 80
	}
	if c.keepRecentMessages == 0 {
		c.keepRecentMessages = 8
	}
	return c
}

// MaybeCompact implements agent.Compactor.
func (c *Compactor) MaybeCompact(
	ctx context.Context,
	conv *agent.Conversation,
	model string,
	resolver agent.ToolResolver,
) agent.CompactResult {
	before := EstimateConversation(conv)
	limit := c.effectiveContextLimit(model)
	threshold := limit * c.thresholdPct / 100
	lightTrigger := limit * lightTriggerPct / 100

	if before < lightTrigger {
		return agent.CompactResult{
			Outcome:      agent.CompactOutcomeNoop,
			TokensBefore: before,
			TokensAfter:  before,
			Reason:       fmt.Sprintf("below light-trigger (%d < %d)", before, lightTrigger),
		}
	}

	if before < threshold {
		// Middle band: clear old read-only tool_results.
		cleared := ClearOldToolResults(conv, ClearOpts{
			KeepRecent: c.keepRecentMessages,
			Resolver:   resolver,
		})
		after := EstimateConversation(conv)
		return agent.CompactResult{
			Outcome:      agent.CompactOutcomeCleared,
			TokensBefore: before,
			TokensAfter:  after,
			Reason:       fmt.Sprintf("cleared %d old tool_results", cleared),
		}
	}

	// High band: summarize the head, keep the tail verbatim.
	msgs := conv.All()
	if len(msgs) <= c.keepRecentMessages+1 {
		// Nothing meaningful to compact — less than one round-trip of
		// history beyond the preserved tail. Fall back to clearing.
		cleared := ClearOldToolResults(conv, ClearOpts{
			KeepRecent: c.keepRecentMessages,
			Resolver:   resolver,
		})
		after := EstimateConversation(conv)
		return agent.CompactResult{
			Outcome:      agent.CompactOutcomeCleared,
			TokensBefore: before,
			TokensAfter:  after,
			Reason:       fmt.Sprintf("head too small to summarize; cleared %d tool_results", cleared),
		}
	}

	// Before summarizing, clear old tool_results in the head so the
	// summarizer reads a leaner conversation. This also reduces the
	// chance the summarizer itself overflows its context.
	_ = ClearOldToolResults(conv, ClearOpts{
		KeepRecent: c.keepRecentMessages,
		Resolver:   resolver,
	})

	head := msgs[:len(msgs)-c.keepRecentMessages]
	boundaryID := head[len(head)-1].ID
	summaryModel := c.pickSummarizerModel(model)
	summary, err := Summarize(SummarizeInput{
		Ctx:      ctx,
		Provider: c.provider,
		Model:    summaryModel,
		Messages: head,
	})
	if err != nil {
		// Summarization failed. Fall back to the clearing we already did.
		after := EstimateConversation(conv)
		return agent.CompactResult{
			Outcome:      agent.CompactOutcomeCleared,
			TokensBefore: before,
			TokensAfter:  after,
			Reason:       fmt.Sprintf("summarize failed (%v); kept cleared state", err),
		}
	}

	conv.ReplaceHead(c.keepRecentMessages, summary)
	after := EstimateConversation(conv)
	return agent.CompactResult{
		Outcome:                  agent.CompactOutcomeSummarized,
		TokensBefore:             before,
		TokensAfter:              after,
		Reason:                   fmt.Sprintf("summarized %d messages", len(head)),
		SummaryMessage:           &summary,
		ReplacedThroughMessageID: boundaryID,
	}
}

// ForceSummarize implements agent.Compactor. Called by the agent loop's
// reactive retry after a provider returns a context-limit error. Uses
// keepRecentMessages / 2 (minimum 2) to be more aggressive than the
// threshold path — when we're already over the limit, trim harder.
func (c *Compactor) ForceSummarize(
	ctx context.Context,
	conv *agent.Conversation,
	model string,
) (*api.Message, string, error) {
	keep := c.keepRecentMessages / 2
	if keep < 2 {
		keep = 2
	}
	msgs := conv.All()
	if len(msgs) <= keep+1 {
		return nil, "", fmt.Errorf("compact: nothing to summarize (have %d messages, keep %d)", len(msgs), keep)
	}
	head := msgs[:len(msgs)-keep]
	boundaryID := head[len(head)-1].ID
	summaryModel := c.pickSummarizerModel(model)
	summary, err := Summarize(SummarizeInput{
		Ctx:      ctx,
		Provider: c.provider,
		Model:    summaryModel,
		Messages: head,
	})
	if err != nil {
		return nil, "", err
	}
	conv.ReplaceHead(keep, summary)
	return &summary, boundaryID, nil
}

// ContextLimit returns the operative context ceiling for the given model,
// applying the same config→provider→default→max-override resolution used
// by MaybeCompact. The TUI consumes this to render the context% indicator
// in the status bar. Pure read; safe for concurrent calls.
func (c *Compactor) ContextLimit(model string) int {
	return c.effectiveContextLimit(model)
}

// effectiveContextLimit is the instance-method companion of the
// free function below. Resolution order:
//  1. modelContextLimit(model) > 0 — user-configured override wins.
//  2. provider.ContextLimit(model) > 0 — provider's hardcoded /
//     server-reported value.
//  3. defaultLimit.
//  4. Cap at maxOverride.
func (c *Compactor) effectiveContextLimit(model string) int {
	if c.modelContextLimit != nil {
		if n := c.modelContextLimit(model); n > 0 {
			if c.maxOverride > 0 && n > c.maxOverride {
				return c.maxOverride
			}
			return n
		}
	}
	return effectiveContextLimit(c.provider, model, c.defaultLimit, c.maxOverride)
}

func (c *Compactor) pickSummarizerModel(sessionModel string) string {
	if c.summarizerModel != "" {
		return c.summarizerModel
	}
	return sessionModel
}

// effectiveContextLimit resolves the operative ceiling for a model:
//   - If the provider reports a size > 0, use it.
//   - Otherwise fall back to defaultLimit.
//   - Cap the result at maxOverride.
func effectiveContextLimit(p api.Provider, model string, defaultLimit, maxOverride int) int {
	n := p.ContextLimit(model)
	if n == 0 {
		n = defaultLimit
	}
	if maxOverride > 0 && n > maxOverride {
		return maxOverride
	}
	return n
}
