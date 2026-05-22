package sse

import (
	"slices"
	"strings"
	"testing"
)

func collect(seq func(func(Event) bool)) []Event {
	return slices.Collect(seq)
}

func TestParseBasicEvent(t *testing.T) {
	input := "data: hello\n\n"
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Data != "hello" {
		t.Errorf("Data = %q, want %q", events[0].Data, "hello")
	}
	if events[0].Type != "" {
		t.Errorf("Type = %q, want empty", events[0].Type)
	}
}

func TestParseEventWithType(t *testing.T) {
	input := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n"
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != "message_start" {
		t.Errorf("Type = %q, want %q", events[0].Type, "message_start")
	}
	if events[0].Data != `{"type":"message_start"}` {
		t.Errorf("Data = %q", events[0].Data)
	}
}

func TestParseMultiLineData(t *testing.T) {
	input := "data: line1\ndata: line2\n\n"
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Data != "line1\nline2" {
		t.Errorf("Data = %q, want %q", events[0].Data, "line1\nline2")
	}
}

func TestParseCommentSkipped(t *testing.T) {
	input := ": keep-alive\n\n"
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 0 {
		t.Errorf("got %d events, want 0 (comment only)", len(events))
	}
}

func TestParseMultipleEvents(t *testing.T) {
	input := "data: first\n\ndata: second\n\n"
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Data != "first" {
		t.Errorf("events[0].Data = %q", events[0].Data)
	}
	if events[1].Data != "second" {
		t.Errorf("events[1].Data = %q", events[1].Data)
	}
}

func TestParseOpenAIFormat(t *testing.T) {
	input := `data: {"choices":[{"delta":{"content":"Hi"}}]}

data: [DONE]

`
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if !strings.Contains(events[0].Data, "choices") {
		t.Errorf("events[0].Data = %q, want JSON with choices", events[0].Data)
	}
	if events[1].Data != "[DONE]" {
		t.Errorf("events[1].Data = %q, want %q", events[1].Data, "[DONE]")
	}
}

func TestParseEarlyBreak(t *testing.T) {
	input := "data: first\n\ndata: second\n\ndata: third\n\n"
	var got []Event
	for event := range Parse(strings.NewReader(input)) {
		got = append(got, event)
		if len(got) == 1 {
			break
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Data != "first" {
		t.Errorf("Data = %q, want %q", got[0].Data, "first")
	}
}

func TestParseNoTrailingBlankLine(t *testing.T) {
	input := "data: no-newline"
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Data != "no-newline" {
		t.Errorf("Data = %q, want %q", events[0].Data, "no-newline")
	}
}

func TestParseEmptyStream(t *testing.T) {
	events := collect(Parse(strings.NewReader("")))
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestParseAnthropicSequence(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":25}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
	events := collect(Parse(strings.NewReader(input)))
	if len(events) != 7 {
		t.Fatalf("got %d events, want 7", len(events))
	}

	expectedTypes := []string{
		"message_start", "content_block_start", "ping",
		"content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}
	for i, et := range expectedTypes {
		if events[i].Type != et {
			t.Errorf("events[%d].Type = %q, want %q", i, events[i].Type, et)
		}
	}
}

// TestParseLargeLineBeyondDefaultScannerLimit covers the >64KB-per-line
// case where bufio.Scanner default buffer used to silently truncate the
// stream. The grown buffer (sseScannerMax) must accept the line and yield
// the event; if the buffer cap regresses, this test fails by length.
func TestParseLargeLineBeyondDefaultScannerLimit(t *testing.T) {
	const payloadSize = 200 * 1024 // 200KB single delta line, comfortably > the old 64KB cap
	payload := strings.Repeat("a", payloadSize)
	events := collect(Parse(strings.NewReader("data: " + payload + "\n\n")))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if len(events[0].Data) != payloadSize {
		t.Errorf("Data length = %d, want %d (scanner buffer truncated)", len(events[0].Data), payloadSize)
	}
}

func TestParseWithErrorReportsScannerError(t *testing.T) {
	payload := strings.Repeat("x", sseScannerMax+1)
	events, parseErr := ParseWithError(strings.NewReader("data: " + payload))

	if got := collect(events); len(got) != 0 {
		t.Fatalf("events = %d, want none on oversized line", len(got))
	}
	if err := parseErr(); err == nil {
		t.Fatal("expected scanner error for oversized SSE line")
	}
}
