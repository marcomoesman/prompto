package anthropic

import (
	"iter"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/sse"
)

// ParseStream converts raw SSE events into api.StreamEvent values.
// It interprets Anthropic-specific event types from the SSE event field.
func ParseStream(events iter.Seq[sse.Event]) iter.Seq[api.StreamEvent] {
	return func(yield func(api.StreamEvent) bool) {
		var stopReason string

		for sseEvt := range events {
			evt, ok := parseStreamEvent(sseEvt.Type, sseEvt.Data)
			if !ok {
				continue
			}

			// Track stop reason from message_delta for the final EventDone
			if evt.StopReason != "" {
				stopReason = evt.StopReason
			}

			// Set the accumulated stop reason on EventDone
			if evt.Type == api.EventDone {
				evt.StopReason = stopReason
			}

			if !yield(evt) {
				return
			}
		}
	}
}
