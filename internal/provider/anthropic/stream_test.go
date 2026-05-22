package anthropic

import (
	"slices"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/sse"
)

func sseFromString(input string) []sse.Event {
	return slices.Collect(sse.Parse(strings.NewReader(input)))
}

func sseSeq(events []sse.Event) func(func(sse.Event) bool) {
	return func(yield func(sse.Event) bool) {
		for _, e := range events {
			if !yield(e) {
				return
			}
		}
	}
}

func collectStream(events []sse.Event) []api.StreamEvent {
	return slices.Collect(ParseStream(sseSeq(events)))
}

func TestParseStreamTextOnly(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":25,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
	sseEvents := sseFromString(input)
	events := collectStream(sseEvents)

	// Expect: Usage (message_start), Delta "Hello", Delta " world", Usage (message_delta), Done
	var deltas []string
	var usageCount int
	var gotDone bool

	for _, e := range events {
		switch e.Type {
		case api.EventDelta:
			deltas = append(deltas, e.Delta)
		case api.EventUsage:
			usageCount++
		case api.EventDone:
			gotDone = true
			if e.StopReason != "end_turn" {
				t.Errorf("StopReason = %q, want %q", e.StopReason, "end_turn")
			}
		}
	}

	if len(deltas) != 2 || deltas[0] != "Hello" || deltas[1] != " world" {
		t.Errorf("deltas = %v, want [Hello, world]", deltas)
	}
	if usageCount != 2 {
		t.Errorf("usage events = %d, want 2", usageCount)
	}
	if !gotDone {
		t.Error("missing EventDone")
	}
}

func TestParseStreamToolUse(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me read that."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"/tmp/foo\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}

`
	sseEvents := sseFromString(input)
	events := collectStream(sseEvents)

	var gotToolStart bool
	var toolArgs string
	var gotDone bool

	for _, e := range events {
		switch e.Type {
		case api.EventToolCallStart:
			gotToolStart = true
			if e.ToolCallID != "toolu_1" {
				t.Errorf("ToolCallID = %q", e.ToolCallID)
			}
			if e.ToolCallName != "read" {
				t.Errorf("ToolCallName = %q", e.ToolCallName)
			}
			if e.ToolCallIndex != 1 {
				t.Errorf("ToolCallIndex = %d, want 1", e.ToolCallIndex)
			}
		case api.EventToolCallDelta:
			toolArgs += e.ToolCallArgs
		case api.EventDone:
			gotDone = true
			if e.StopReason != "tool_use" {
				t.Errorf("StopReason = %q, want %q", e.StopReason, "tool_use")
			}
		}
	}

	if !gotToolStart {
		t.Error("missing EventToolCallStart")
	}
	if toolArgs != `{"path":"/tmp/foo"}` {
		t.Errorf("toolArgs = %q", toolArgs)
	}
	if !gotDone {
		t.Error("missing EventDone")
	}
}

func TestParseStreamPingSkipped(t *testing.T) {
	events := collectStream([]sse.Event{
		{Type: "ping", Data: `{"type":"ping"}`},
	})
	if len(events) != 0 {
		t.Errorf("got %d events from ping, want 0", len(events))
	}
}

func TestParseStreamThinking(t *testing.T) {
	input := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" Step two."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`
	sseEvents := sseFromString(input)
	events := collectStream(sseEvents)

	var thinkingStart bool
	var thinkingText string
	var thinkingSig string
	var textDeltas []string

	for _, e := range events {
		switch e.Type {
		case api.EventThinkingStart:
			thinkingStart = true
			if e.ThinkingIndex != 0 {
				t.Errorf("ThinkingIndex = %d, want 0", e.ThinkingIndex)
			}
			if e.ThinkingRedacted {
				t.Error("ThinkingRedacted should be false")
			}
		case api.EventThinkingDelta:
			thinkingText += e.Delta
			if e.ThinkingSignature != "" {
				thinkingSig = e.ThinkingSignature
			}
		case api.EventDelta:
			textDeltas = append(textDeltas, e.Delta)
		}
	}

	if !thinkingStart {
		t.Error("missing EventThinkingStart")
	}
	if thinkingText != "Let me think. Step two." {
		t.Errorf("thinkingText = %q", thinkingText)
	}
	if thinkingSig != "sig-abc" {
		t.Errorf("thinkingSig = %q, want %q", thinkingSig, "sig-abc")
	}
	if len(textDeltas) != 1 || textDeltas[0] != "Hello" {
		t.Errorf("textDeltas = %v", textDeltas)
	}
}

func TestParseStreamRedactedThinking(t *testing.T) {
	input := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"encrypted-blob"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}

`
	sseEvents := sseFromString(input)
	events := collectStream(sseEvents)

	var found bool
	for _, e := range events {
		if e.Type != api.EventThinkingStart {
			continue
		}
		found = true
		if !e.ThinkingRedacted {
			t.Error("ThinkingRedacted should be true")
		}
		if e.ThinkingRedactedData != "encrypted-blob" {
			t.Errorf("ThinkingRedactedData = %q", e.ThinkingRedactedData)
		}
	}
	if !found {
		t.Error("missing EventThinkingStart for redacted block")
	}
}

func TestParseStreamUsageFromMessageStart(t *testing.T) {
	events := collectStream([]sse.Event{
		{Type: "message_start", Data: `{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":42,"output_tokens":0}}}`},
	})
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != api.EventUsage {
		t.Errorf("Type = %q, want %q", events[0].Type, api.EventUsage)
	}
	if events[0].Usage.InputTokens != 42 {
		t.Errorf("InputTokens = %d, want 42", events[0].Usage.InputTokens)
	}
}
