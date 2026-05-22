package agent

import (
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// thinkingResponse builds a canned stream that includes one extended-
// thinking content block, then a single text block. Mirrors what the
// Anthropic stream parser emits so the run loop sees realistic event
// ordering.
func thinkingResponse(thinkingText, signature, finalText string) []api.StreamEvent {
	return []api.StreamEvent{
		{Type: api.EventThinkingStart, ThinkingIndex: 0},
		{Type: api.EventThinkingDelta, ThinkingIndex: 0, Delta: thinkingText},
		{Type: api.EventThinkingDelta, ThinkingIndex: 0, ThinkingSignature: signature},
		{Type: api.EventDelta, Delta: finalText},
		{Type: api.EventDone, StopReason: "end_turn"},
	}
}

func TestRun_ThinkingBlockPrependedAndStreamed(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{
		thinkingResponse("Step 1. Step 2.", "sig-xyz", "answer"),
	}}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})

	var thinkingText string
	for ev := range rr.Events {
		if ev.Type == EventThinkingDelta {
			thinkingText += ev.Delta
		}
	}
	<-rr.Done

	if thinkingText != "Step 1. Step 2." {
		t.Errorf("streamed thinking = %q, want %q", thinkingText, "Step 1. Step 2.")
	}

	msgs := conv.Messages()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	assistant := msgs[1]
	// First block must be the thinking block (preserves Anthropic's ordering
	// requirement on signature-bearing turns); second is the text answer.
	if len(assistant.Content) != 2 {
		t.Fatalf("assistant content blocks = %d, want 2", len(assistant.Content))
	}
	if assistant.Content[0].Type != api.BlockThinking {
		t.Errorf("content[0].Type = %q, want thinking", assistant.Content[0].Type)
	}
	th := assistant.Content[0].Thinking
	if th == nil {
		t.Fatal("thinking block payload is nil")
	}
	if th.Text != "Step 1. Step 2." {
		t.Errorf("thinking text = %q", th.Text)
	}
	if th.Signature != "sig-xyz" {
		t.Errorf("thinking signature = %q, want sig-xyz", th.Signature)
	}
	if assistant.Content[1].Type != api.BlockText || assistant.Content[1].Text != "answer" {
		t.Errorf("content[1] = %+v, want text:answer", assistant.Content[1])
	}
}

func TestRun_RedactedThinkingPreserved(t *testing.T) {
	events := []api.StreamEvent{
		{
			Type:                 api.EventThinkingStart,
			ThinkingIndex:        0,
			ThinkingRedacted:     true,
			ThinkingRedactedData: "blob",
		},
		{Type: api.EventDelta, Delta: "ok"},
		{Type: api.EventDone, StopReason: "end_turn"},
	}
	prov := &fakeProvider{responses: [][]api.StreamEvent{events}}
	agnt := New(NewAgentInput{Provider: prov, Model: "test", Tools: newFakeResolver()})
	conv := NewConversation()
	conv.Append(api.NewUserMessage("hi"))

	rr := agnt.Run(t.Context(), RunInput{Conversation: conv, MaxSteps: 5, CanUseTool: allowAll})
	for range rr.Events {
	}
	<-rr.Done

	assistant := conv.Messages()[1]
	if assistant.Content[0].Type != api.BlockThinking {
		t.Fatalf("first block type = %q, want thinking", assistant.Content[0].Type)
	}
	th := assistant.Content[0].Thinking
	if th == nil || !th.Redacted || th.Data != "blob" {
		t.Errorf("redacted thinking block = %+v, want Redacted=true Data=blob", th)
	}
}
