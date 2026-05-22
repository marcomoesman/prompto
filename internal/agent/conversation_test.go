package agent

import (
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestReplaceHead_KeepsTail(t *testing.T) {
	conv := NewConversation()
	for i := range 6 {
		_ = i
		conv.Append(api.NewUserMessage("x"))
	}
	summary := api.NewUserMessage("summary")

	conv.ReplaceHead(2, summary)

	if got := len(conv.All()); got != 3 { // [summary, msg4, msg5]
		t.Fatalf("len = %d, want 3", got)
	}
	if conv.All()[0].Text() != "summary" {
		t.Errorf("head = %q, want summary", conv.All()[0].Text())
	}
}

func TestReplaceHead_PrependsReplacement(t *testing.T) {
	conv := NewConversation()
	conv.Append(api.NewUserMessage("a"))
	conv.Append(api.NewUserMessage("b"))

	summary := api.NewUserMessage("s")
	conv.ReplaceHead(2, summary)

	msgs := conv.All()
	if len(msgs) != 3 || msgs[0].Text() != "s" || msgs[1].Text() != "a" || msgs[2].Text() != "b" {
		t.Errorf("unexpected: %v", textsOf(msgs))
	}
}

func TestReplaceHead_KeepTailLargerThanConv(t *testing.T) {
	conv := NewConversation()
	conv.Append(api.NewUserMessage("a"))
	conv.Append(api.NewUserMessage("b"))

	summary := api.NewUserMessage("s")
	conv.ReplaceHead(10, summary)

	msgs := conv.All()
	if len(msgs) != 3 || msgs[0].Text() != "s" {
		t.Errorf("expected [s, a, b], got %v", textsOf(msgs))
	}
}

func TestReplaceHead_EmptyReplacementSkipped(t *testing.T) {
	conv := NewConversation()
	conv.Append(api.NewUserMessage("a"))
	conv.Append(api.NewUserMessage("b"))
	conv.Append(api.NewUserMessage("c"))

	conv.ReplaceHead(2, api.Message{}) // zero-value — skip insertion

	msgs := conv.All()
	if len(msgs) != 2 || msgs[0].Text() != "b" || msgs[1].Text() != "c" {
		t.Errorf("expected [b, c], got %v", textsOf(msgs))
	}
}

func TestReplaceHead_NegativeKeepTailTreatedAsZero(t *testing.T) {
	conv := NewConversation()
	conv.Append(api.NewUserMessage("a"))
	conv.Append(api.NewUserMessage("b"))

	conv.ReplaceHead(-5, api.NewUserMessage("s"))

	msgs := conv.All()
	if len(msgs) != 1 || msgs[0].Text() != "s" {
		t.Errorf("expected [s] only, got %v", textsOf(msgs))
	}
}

func textsOf(msgs []api.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Text())
	}
	return out
}
