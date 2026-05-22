package compact

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// scriptedProvider replays canned events for a single Complete call. It
// also records the last CompleteParams it saw so tests can assert what
// was sent.
type scriptedProvider struct {
	events  []api.StreamEvent
	lastReq api.CompleteParams
}

func (p *scriptedProvider) ContextLimit(string) int { return 0 }

func (p *scriptedProvider) Complete(_ context.Context, params api.CompleteParams) iter.Seq[api.StreamEvent] {
	p.lastReq = params
	return func(yield func(api.StreamEvent) bool) {
		for _, e := range p.events {
			if !yield(e) {
				return
			}
		}
	}
}

func TestSummarize_RoundTrip(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventDelta, Delta: "goal: fix bug. done."},
		{Type: api.EventDone},
	}}
	out, err := Summarize(SummarizeInput{
		Ctx:      t.Context(),
		Provider: prov,
		Model:    "test",
		Messages: []api.Message{api.NewUserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if out.Role != api.RoleUser {
		t.Errorf("Role = %v, want RoleUser", out.Role)
	}
	if !strings.HasPrefix(out.Text(), CompactSummaryOpen) {
		t.Errorf("summary missing open tag: %q", out.Text())
	}
	if !strings.HasSuffix(out.Text(), CompactSummaryClose) {
		t.Errorf("summary missing close tag: %q", out.Text())
	}
	if !strings.Contains(out.Text(), "goal: fix bug. done.") {
		t.Errorf("summary missing content: %q", out.Text())
	}
}

func TestSummarize_PassesNoToolsInParams(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventDelta, Delta: "x"},
		{Type: api.EventDone},
	}}
	_, err := Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: prov, Model: "test",
		Messages: []api.Message{api.NewUserMessage("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prov.lastReq.Tools != nil {
		t.Errorf("summarize should pass Tools=nil, got %v", prov.lastReq.Tools)
	}
	if len(prov.lastReq.System) != 1 || !strings.Contains(prov.lastReq.System[0].Text, "Do NOT call any tools") {
		t.Errorf("system prompt missing NoToolsPreamble, got %v", prov.lastReq.System)
	}
}

func TestSummarize_EmptyResponseErrors(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{{Type: api.EventDone}}}
	_, err := Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: prov, Model: "test",
		Messages: []api.Message{api.NewUserMessage("hi")},
	})
	if err == nil {
		t.Error("expected error on empty response")
	}
}

func TestSummarize_ProviderErrorPropagates(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventError, Error: errors.New("boom")},
	}}
	_, err := Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: prov, Model: "test",
		Messages: []api.Message{api.NewUserMessage("hi")},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("provider error not surfaced: %v", err)
	}
}

func TestSummarize_RejectsMissingInputs(t *testing.T) {
	cases := []struct {
		name string
		in   SummarizeInput
	}{
		{"nil provider", SummarizeInput{Ctx: context.Background(), Model: "m", Messages: []api.Message{api.NewUserMessage("x")}}},
		{"empty model", SummarizeInput{Ctx: context.Background(), Provider: &scriptedProvider{}, Messages: []api.Message{api.NewUserMessage("x")}}},
		{"empty messages", SummarizeInput{Ctx: context.Background(), Provider: &scriptedProvider{}, Model: "m"}},
	}
	for _, tc := range cases {
		if _, err := Summarize(tc.in); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

// TestSummaryTemplate_StructuredSchema locks the template
// shape: every required heading is present, the re-compaction
// instruction is present, and the omit-empty-sections rule is
// present. A snapshot test on structural properties (not the literal
// string) so future formatting tweaks don't break the test, but
// dropping a required heading does.
func TestSummaryTemplate_StructuredSchema(t *testing.T) {
	required := []string{
		"## Goal",
		"## Constraints & Preferences",
		"## Progress",
		"### Done",
		"### In Progress",
		"### Blocked",
		"## Key Decisions",
		"## Next Steps",
		"## Files Touched",
		"## Critical Context",
		// Re-compaction instruction must be present:
		"<compact_summary>",
		"PRESERVE",
		"CARRY FORWARD",
		"REPLACE Next Steps",
		// Omit-empty rule:
		"Omit any section that has no items",
		// Conversation slot:
		"<conversation>",
	}
	for _, want := range required {
		if !strings.Contains(SummaryTemplate, want) {
			t.Errorf("SummaryTemplate missing %q", want)
		}
	}
}

// TestSummarize_DefaultMaxTokensRaised confirms the default
// MaxTokens of 6144. Captured in the params the provider sees.
func TestSummarize_DefaultMaxTokensRaised(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventDelta, Delta: "x"},
		{Type: api.EventDone},
	}}
	_, err := Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: prov, Model: "test",
		Messages: []api.Message{api.NewUserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if prov.lastReq.MaxTokens != 6144 {
		t.Errorf("default MaxTokens = %d, want 6144", prov.lastReq.MaxTokens)
	}
}

// TestSummarize_ExplicitMaxTokensRespected confirms the caller
// override still works alongside the default.
func TestSummarize_ExplicitMaxTokensRespected(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventDelta, Delta: "x"},
		{Type: api.EventDone},
	}}
	_, err := Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: prov, Model: "test",
		Messages:  []api.Message{api.NewUserMessage("hi")},
		MaxTokens: 2000,
	})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if prov.lastReq.MaxTokens != 2000 {
		t.Errorf("explicit MaxTokens = %d, want 2000", prov.lastReq.MaxTokens)
	}
}

// TestSummarize_RecompactionFlow asserts the two-pass scenario:
// a first summarization produces a <compact_summary>, and a second
// summarization pass receives that summary verbatim in its input
// (renderConversationAsText passes user-text-blocks through
// unchanged). The summarizer prompt's "If the conversation already
// contains <compact_summary>" clause then handles incremental
// updates. Asserts the prior summary appears in the second-pass
// rendered prompt — the load-bearing precondition.
func TestSummarize_RecompactionFlow(t *testing.T) {
	// First pass: produces a summary.
	first := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventDelta, Delta: "## Goal\nFix issue #123\n"},
		{Type: api.EventDone},
	}}
	summary, err := Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: first, Model: "test",
		Messages: []api.Message{
			api.NewUserMessage("fix bug in foo.go"),
		},
	})
	if err != nil {
		t.Fatalf("first Summarize: %v", err)
	}

	// Second pass: convey the prior summary as part of the input
	// (this is what compact.go does via conv.ReplaceHead).
	second := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventDelta, Delta: "## Goal\nFix issue #123\n## Progress\n### Done\n- foo.go updated\n"},
		{Type: api.EventDone},
	}}
	_, err = Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: second, Model: "test",
		Messages: []api.Message{
			summary,
			api.NewUserMessage("now also update bar.go"),
		},
	})
	if err != nil {
		t.Fatalf("second Summarize: %v", err)
	}

	// The second summarizer must have seen the prior <compact_summary>
	// block in its input — that's how the re-compaction "If the
	// conversation already contains" clause activates.
	rendered := second.lastReq.Messages[0].Text()
	if !strings.Contains(rendered, "<compact_summary>") {
		t.Errorf("re-compaction input missing prior <compact_summary> block:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Fix issue #123") {
		t.Errorf("re-compaction input missing prior Goal content:\n%s", rendered)
	}
}

// TestSummarize_RenderIncludesToolCalls asserts the summarizer
// receives tool_use blocks in its rendered conversation, so the
// model can extract file paths for the Files Touched section.
// The summary template relies on this — without it, Files Touched
// would always be empty.
func TestSummarize_RenderIncludesToolCalls(t *testing.T) {
	prov := &scriptedProvider{events: []api.StreamEvent{
		{Type: api.EventDelta, Delta: "ok"},
		{Type: api.EventDone},
	}}
	msgs := []api.Message{
		api.NewUserMessage("look at the registry"),
		{
			Role: api.RoleAssistant,
			Content: []api.ContentBlock{{
				Type: api.BlockToolUse,
				ToolCall: &api.ToolCall{
					ID:    "tc1",
					Name:  "read",
					Input: []byte(`{"file_path":"internal/agent/registry.go"}`),
				},
			}},
		},
	}
	if _, err := Summarize(SummarizeInput{
		Ctx: t.Context(), Provider: prov, Model: "test", Messages: msgs,
	}); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	rendered := prov.lastReq.Messages[0].Text()
	if !strings.Contains(rendered, "[tool: read]") {
		t.Errorf("rendered conversation missing tool_use marker:\n%s", rendered)
	}
	if !strings.Contains(rendered, "internal/agent/registry.go") {
		t.Errorf("rendered conversation missing file path the model needs to extract:\n%s", rendered)
	}
}

func TestIsCompactSummary(t *testing.T) {
	sum := api.NewUserMessage(CompactSummaryOpen + "\nsome summary\n" + CompactSummaryClose)
	if !IsCompactSummary(sum) {
		t.Error("summary not detected")
	}
	regular := api.NewUserMessage("plain user message")
	if IsCompactSummary(regular) {
		t.Error("regular user message falsely detected as summary")
	}
	assistant := api.Message{Role: api.RoleAssistant, Content: []api.ContentBlock{{Type: api.BlockText, Text: CompactSummaryOpen}}}
	if IsCompactSummary(assistant) {
		t.Error("assistant-role message falsely detected as summary")
	}
}
