package openai

import "testing"

func TestContextLimit_KnownGpt4o(t *testing.T) {
	p := &Provider{}
	if got := p.ContextLimit("gpt-4o"); got != 128_000 {
		t.Errorf("gpt-4o = %d, want 128000", got)
	}
}

func TestContextLimit_PrefixMatchVersioned(t *testing.T) {
	p := &Provider{}
	if got := p.ContextLimit("gpt-4o-2024-11-20"); got != 128_000 {
		t.Errorf("gpt-4o versioned = %d, want 128000", got)
	}
}

func TestContextLimit_Gpt5(t *testing.T) {
	p := &Provider{}
	if got := p.ContextLimit("gpt-5"); got != 400_000 {
		t.Errorf("gpt-5 = %d, want 400000", got)
	}
}

func TestContextLimit_UnknownLocalModel(t *testing.T) {
	p := &Provider{}
	if got := p.ContextLimit("llama-3.2-8b"); got != 0 {
		t.Errorf("unknown local model = %d, want 0", got)
	}
}
