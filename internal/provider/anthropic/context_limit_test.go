package anthropic

import "testing"

func TestContextLimit_KnownSonnet(t *testing.T) {
	p := &Provider{}
	if got := p.ContextLimit("claude-sonnet-4-6"); got != 200_000 {
		t.Errorf("sonnet-4-6 = %d, want 200000", got)
	}
}

func TestContextLimit_DateStampedVariant(t *testing.T) {
	p := &Provider{}
	// Prefix match against "claude-sonnet-4".
	if got := p.ContextLimit("claude-sonnet-4-20250514"); got != 200_000 {
		t.Errorf("dated sonnet = %d, want 200000", got)
	}
}

func TestContextLimit_1MSuffix(t *testing.T) {
	p := &Provider{}
	if got := p.ContextLimit("claude-sonnet-4-6[1m]"); got != 1_000_000 {
		t.Errorf("[1m] = %d, want 1M", got)
	}
}

func TestContextLimit_UnknownModel(t *testing.T) {
	p := &Provider{}
	if got := p.ContextLimit("made-up-model-x"); got != 0 {
		t.Errorf("unknown = %d, want 0", got)
	}
}
