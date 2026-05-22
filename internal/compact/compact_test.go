package compact

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// limitedProvider reports a fixed ContextLimit regardless of model.
type limitedProvider struct {
	*scriptedProvider
	limit int
}

func newLimitedProvider(limit int, deltas ...string) *limitedProvider {
	events := make([]api.StreamEvent, 0, len(deltas)+1)
	for _, d := range deltas {
		events = append(events, api.StreamEvent{Type: api.EventDelta, Delta: d})
	}
	events = append(events, api.StreamEvent{Type: api.EventDone})
	return &limitedProvider{
		scriptedProvider: &scriptedProvider{events: events},
		limit:            limit,
	}
}

func (p *limitedProvider) ContextLimit(string) int { return p.limit }

func TestMaybeCompact_NoopBelowLightTrigger(t *testing.T) {
	prov := newLimitedProvider(1000, "summary")
	c := New(NewInput{Provider: prov, DefaultLimit: 1000, MaxOverride: 1000, ThresholdPct: 80, KeepRecentMessages: 2})

	conv := agent.NewConversation()
	// Small convo: well under the 60% light-trigger of 1000 tokens.
	conv.Append(api.NewUserMessage(strings.Repeat("x", 400))) // ~105 tokens
	conv.Append(api.NewUserMessage(strings.Repeat("y", 400)))

	res := c.MaybeCompact(t.Context(), conv, "any-model", nil)
	if res.Outcome != agent.CompactOutcomeNoop {
		t.Errorf("Outcome = %v, want Noop", res.Outcome)
	}
}

func TestMaybeCompact_ClearsInMiddleBand(t *testing.T) {
	// Build a conversation whose estimate lands in the [60%, 80%) band.
	// Limit 1000 → light trigger 600, threshold 800. Target ~700.
	prov := newLimitedProvider(1000, "summary")
	c := New(NewInput{Provider: prov, DefaultLimit: 1000, MaxOverride: 1000, ThresholdPct: 80, KeepRecentMessages: 2})

	conv := agent.NewConversation()
	conv.Append(api.NewUserMessage("start"))
	// Add several tool_use/tool_result pairs to produce ~700 tokens total.
	for i := 0; i < 3; i++ {
		assistant := api.NewAssistantMessage()
		assistant.Content = []api.ContentBlock{{
			Type:     api.BlockToolUse,
			ToolCall: &api.ToolCall{ID: "tc_" + itoa(i), Name: "read", Input: []byte("{}")},
		}}
		conv.Append(assistant)
		conv.Append(api.Message{
			Role: api.RoleTool,
			Content: []api.ContentBlock{{
				Type: api.BlockToolResult,
				ToolResult: &api.ToolResult{
					ToolCallID: "tc_" + itoa(i),
					Content:    strings.Repeat("y", 800),
				},
			}},
		})
	}

	resolver := newFakeResolver(&fakeTool{name: "read", readOnly: true})

	before := EstimateConversation(conv)
	if before < 600 || before >= 800 {
		t.Fatalf("test setup: estimate %d not in [600, 800) band", before)
	}

	res := c.MaybeCompact(t.Context(), conv, "model", resolver)
	if res.Outcome != agent.CompactOutcomeCleared {
		t.Errorf("Outcome = %v (reason=%s), want Cleared", res.Outcome, res.Reason)
	}
	if res.TokensAfter >= res.TokensBefore {
		t.Errorf("TokensAfter %d should be < TokensBefore %d", res.TokensAfter, res.TokensBefore)
	}
}

func TestMaybeCompact_SummarizesAboveThreshold(t *testing.T) {
	prov := newLimitedProvider(1000, "summarized!")
	c := New(NewInput{Provider: prov, DefaultLimit: 1000, MaxOverride: 1000, ThresholdPct: 80, KeepRecentMessages: 2})

	conv := agent.NewConversation()
	// Build a conv with ~900 tokens, above the 80% threshold.
	for i := 0; i < 9; i++ {
		conv.Append(api.NewUserMessage(strings.Repeat("x", 400))) // ~105 each → ~945
	}

	before := EstimateConversation(conv)
	if before < 800 {
		t.Fatalf("test setup: estimate %d below threshold", before)
	}

	res := c.MaybeCompact(t.Context(), conv, "model", nil)
	if res.Outcome != agent.CompactOutcomeSummarized {
		t.Fatalf("Outcome = %v (reason=%s), want Summarized", res.Outcome, res.Reason)
	}
	if res.SummaryMessage == nil {
		t.Fatal("SummaryMessage should be populated")
	}
	if !strings.Contains(res.SummaryMessage.Text(), "summarized!") {
		t.Errorf("summary missing provider content: %q", res.SummaryMessage.Text())
	}
	// After summarization, token count should be drastically lower.
	if res.TokensAfter >= res.TokensBefore {
		t.Errorf("TokensAfter %d not reduced from %d", res.TokensAfter, res.TokensBefore)
	}
}

func TestMaybeCompact_FallsBackToDefaultWhenProviderUnknown(t *testing.T) {
	// Provider reports 0 → defaultLimit kicks in.
	prov := &scriptedProvider{events: []api.StreamEvent{{Type: api.EventDone}}}
	c := New(NewInput{Provider: prov, DefaultLimit: 1000, MaxOverride: 1000, ThresholdPct: 80, KeepRecentMessages: 2})

	conv := agent.NewConversation()
	conv.Append(api.NewUserMessage(strings.Repeat("x", 400))) // ~105 tokens — below light trigger 600

	res := c.MaybeCompact(t.Context(), conv, "unknown-model", nil)
	if res.Outcome != agent.CompactOutcomeNoop {
		t.Errorf("Outcome = %v, want Noop (default limit 1000 kicks in)", res.Outcome)
	}
}

func TestMaybeCompact_MaxOverrideCapsProviderLimit(t *testing.T) {
	// Provider says 1M, MaxOverride caps at 1000 → 800 threshold. Conv at 900
	// should trigger summarize, not noop.
	prov := newLimitedProvider(1_000_000, "summary")
	c := New(NewInput{
		Provider: prov, DefaultLimit: 1000, MaxOverride: 1000,
		ThresholdPct: 80, KeepRecentMessages: 2,
	})

	conv := agent.NewConversation()
	for i := 0; i < 9; i++ {
		conv.Append(api.NewUserMessage(strings.Repeat("x", 400)))
	}

	res := c.MaybeCompact(t.Context(), conv, "any-model", nil)
	if res.Outcome != agent.CompactOutcomeSummarized {
		t.Errorf("Outcome = %v, want Summarized (MaxOverride should cap provider's 1M at 1000)", res.Outcome)
	}
}

func TestForceSummarize_Succeeds(t *testing.T) {
	prov := newLimitedProvider(200_000, "forced summary")
	c := New(NewInput{Provider: prov, DefaultLimit: 200_000, MaxOverride: 200_000, ThresholdPct: 80, KeepRecentMessages: 8})

	conv := agent.NewConversation()
	for i := 0; i < 10; i++ {
		conv.Append(api.NewUserMessage("hi " + itoa(i)))
	}

	msg, boundaryID, err := c.ForceSummarize(t.Context(), conv, "model")
	if err != nil {
		t.Fatalf("ForceSummarize: %v", err)
	}
	if msg == nil || !strings.Contains(msg.Text(), "forced summary") {
		t.Errorf("ForceSummarize result missing content: %v", msg)
	}
	if boundaryID == "" {
		t.Error("ForceSummarize returned empty boundary id; expected the last replaced message id")
	}
	// Tail should have been preserved; head replaced.
	if len(conv.All()) > 10 {
		t.Errorf("expected conv smaller after force-summarize, got %d msgs", len(conv.All()))
	}
}

func TestForceSummarize_RejectsShortConv(t *testing.T) {
	prov := newLimitedProvider(200_000, "x")
	c := New(NewInput{Provider: prov, DefaultLimit: 200_000, MaxOverride: 200_000, ThresholdPct: 80, KeepRecentMessages: 8})

	conv := agent.NewConversation()
	conv.Append(api.NewUserMessage("only one"))
	_, _, err := c.ForceSummarize(t.Context(), conv, "model")
	if err == nil {
		t.Error("expected error on too-short conversation")
	}
}

func TestPickSummarizerModel(t *testing.T) {
	prov := newLimitedProvider(1000, "x")
	c := New(NewInput{Provider: prov, SummarizerModel: "haiku"})
	if got := c.pickSummarizerModel("sonnet"); got != "haiku" {
		t.Errorf("pickSummarizerModel = %q, want haiku (override)", got)
	}

	c2 := New(NewInput{Provider: prov}) // no override
	if got := c2.pickSummarizerModel("sonnet"); got != "sonnet" {
		t.Errorf("pickSummarizerModel = %q, want sonnet (fallback)", got)
	}
}

func TestNew_PanicsWithoutProvider(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil provider")
		}
	}()
	_ = New(NewInput{})
}

func TestContextLimit_UsesProviderReportedLimit(t *testing.T) {
	prov := newLimitedProvider(123_456, "x")
	c := New(NewInput{Provider: prov, DefaultLimit: 1000, MaxOverride: 200_000})
	if got := c.ContextLimit("any"); got != 123_456 {
		t.Errorf("ContextLimit = %d, want 123456 (provider reported)", got)
	}
}

func TestContextLimit_FallsBackToDefault(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{{Type: api.EventDone}}}
	c := New(NewInput{Provider: prov, DefaultLimit: 50_000, MaxOverride: 200_000})
	if got := c.ContextLimit("unknown"); got != 50_000 {
		t.Errorf("ContextLimit = %d, want 50000 (defaultLimit fallback)", got)
	}
}

func TestContextLimit_CappedByMaxOverride(t *testing.T) {
	prov := newLimitedProvider(1_000_000, "x")
	c := New(NewInput{Provider: prov, DefaultLimit: 1000, MaxOverride: 250_000})
	if got := c.ContextLimit("any"); got != 250_000 {
		t.Errorf("ContextLimit = %d, want 250000 (capped by MaxOverride)", got)
	}
}
