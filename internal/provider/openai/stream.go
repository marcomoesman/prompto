package openai

import (
	"iter"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/sse"
)

// ParseStream converts raw SSE events into api.StreamEvent values.
// It interprets OpenAI's streaming format (data-only lines, [DONE] terminator).
func ParseStream(events iter.Seq[sse.Event]) iter.Seq[api.StreamEvent] {
	return func(yield func(api.StreamEvent) bool) {
		for sseEvt := range events {
			if sseEvt.Data == "" {
				continue
			}

			parsed, done := parseChunk(sseEvt.Data)
			for _, evt := range parsed {
				if !yield(evt) {
					return
				}
			}
			if done {
				return
			}
		}
	}
}
