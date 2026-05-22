package agent

import "github.com/marcomoesman/prompto/internal/api"

// Section is one labelled piece of the system prompt. The Name is for
// debug/logging; it is never sent to the model.
type Section struct {
	Name    string
	Content string
}

// Prompt builds a sectioned system prompt with exactly one implicit cache
// boundary between the stable prefix and the volatile suffix.
//
// AddStable appends a section to the cacheable prefix. AddVolatile appends a
// section after the cache boundary. Within each group, insertion order is
// preserved. The last stable section carries the cache marker when
// SystemBlocks() is called.
type Prompt struct {
	stable   []Section
	volatile []Section
}

// NewPrompt returns an empty Prompt.
func NewPrompt() *Prompt { return &Prompt{} }

// AddStable appends a cacheable section. Returns the receiver for chaining.
func (p *Prompt) AddStable(s Section) *Prompt {
	p.stable = append(p.stable, s)
	return p
}

// AddVolatile appends a post-boundary section. Returns the receiver for chaining.
func (p *Prompt) AddVolatile(s Section) *Prompt {
	p.volatile = append(p.volatile, s)
	return p
}

// SystemBlocks emits one api.SystemBlock per section, stable first then
// volatile. Exactly one of the emitted blocks has Cache: true — the last
// block originating from AddStable. If no stable sections were added, no
// block carries the cache marker.
func (p *Prompt) SystemBlocks() []api.SystemBlock {
	if len(p.stable) == 0 && len(p.volatile) == 0 {
		return nil
	}
	out := make([]api.SystemBlock, 0, len(p.stable)+len(p.volatile))
	for i, s := range p.stable {
		out = append(out, api.SystemBlock{
			Text:  s.Content,
			Cache: i == len(p.stable)-1,
		})
	}
	for _, s := range p.volatile {
		out = append(out, api.SystemBlock{Text: s.Content})
	}
	return out
}
