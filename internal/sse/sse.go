package sse

import (
	"bufio"
	"io"
	"iter"
	"strings"
)

// Event represents one Server-Sent Event.
type Event struct {
	Type string // "event:" value (empty for OpenAI which omits it)
	Data string // "data:" value
}

// sseScannerMax bounds the per-line buffer for the SSE scanner. The default
// 64KB silently truncates streams whose single chunks legitimately exceed
// it — large tool_call.arguments blobs and big content deltas from cloud
// providers. 16MB is generous (every observed streaming chunk in the wild
// is well under 1MB) and keeps a malformed never-newline stream from
// growing the buffer unboundedly.
const sseScannerMax = 16 * 1024 * 1024

// Parse is the convenience entry point for tests and callers that
// don't need to inspect the trailing scanner error. Production code
// MUST use ParseWithError so transport-level failures (lines
// exceeding sseScannerMax, network truncation surfaced as a Read
// error) don't silently disappear.
func Parse(r io.Reader) iter.Seq[Event] {
	events, _ := ParseWithError(r)
	return events
}

// ParseWithError reads SSE events from r and records any scanner error after
// iteration reaches EOF. Call err after consuming the returned sequence.
func ParseWithError(r io.Reader) (iter.Seq[Event], func() error) {
	var parseErr error
	events := func(yield func(Event) bool) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), sseScannerMax)
		var current Event
		// dataBuilder accumulates multi-line `data:` segments. Earlier
		// versions did `current.Data += "\n" + data` per line — O(n²)
		// for events with many data lines (large tool_call.arguments
		// blobs from Anthropic, big content deltas from any provider).
		// The Builder is reset between events.
		var dataBuilder strings.Builder
		var hasData bool

		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case line == "":
				// Blank line = event boundary
				if hasData {
					current.Data = dataBuilder.String()
					if !yield(current) {
						return
					}
				}
				current = Event{}
				dataBuilder.Reset()
				hasData = false

			case strings.HasPrefix(line, "event:"):
				current.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))

			case strings.HasPrefix(line, "data:"):
				data := strings.TrimPrefix(line, "data:")
				// SSE spec: strip single leading space after "data:"
				data = strings.TrimPrefix(data, " ")
				if hasData {
					dataBuilder.WriteByte('\n')
				}
				dataBuilder.WriteString(data)
				hasData = true

			default:
				// Comment lines (":..."), keep-alives, and unknown fields
				// are all ignored per SSE spec.
			}
		}

		// Emit final event (if any) BEFORE recording the scanner error.
		// A truncated stream that ends mid-event without a trailing
		// blank line still yields a usable event — the receiver gets
		// the data first, then learns about the error via the err
		// callback after iteration completes.
		if hasData {
			current.Data = dataBuilder.String()
			yield(current)
		}
		parseErr = scanner.Err()
	}
	return events, func() error { return parseErr }
}
