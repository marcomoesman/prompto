package openai

import (
	"slices"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/sse"
)

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
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
		{Data: `{"id":"c1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}`},
		{Data: "[DONE]"},
	})

	var deltas []string
	var gotDone bool
	var gotUsage bool

	for _, e := range events {
		switch e.Type {
		case api.EventDelta:
			deltas = append(deltas, e.Delta)
		case api.EventDone:
			gotDone = true
		case api.EventUsage:
			gotUsage = true
			if e.Usage.InputTokens != 10 {
				t.Errorf("InputTokens = %d, want 10", e.Usage.InputTokens)
			}
			if e.Usage.OutputTokens != 5 {
				t.Errorf("OutputTokens = %d, want 5", e.Usage.OutputTokens)
			}
		}
	}

	if len(deltas) != 2 || deltas[0] != "Hello" || deltas[1] != " world" {
		t.Errorf("deltas = %v", deltas)
	}
	if !gotDone {
		t.Error("missing EventDone")
	}
	if !gotUsage {
		t.Error("missing EventUsage")
	}
}

func TestParseStreamToolCalls(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"/tmp\"}"}}]},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`},
		{Data: "[DONE]"},
	})

	var gotStart bool
	var args string

	for _, e := range events {
		switch e.Type {
		case api.EventToolCallStart:
			gotStart = true
			if e.ToolCallID != "call_1" {
				t.Errorf("ToolCallID = %q", e.ToolCallID)
			}
			if e.ToolCallName != "read" {
				t.Errorf("ToolCallName = %q", e.ToolCallName)
			}
		case api.EventToolCallDelta:
			args += e.ToolCallArgs
		}
	}

	if !gotStart {
		t.Error("missing EventToolCallStart")
	}
	if args != `{"path":"/tmp"}` {
		t.Errorf("args = %q", args)
	}
}

// TestParseStreamToolCallsBundledArgs covers servers (e.g. llama.cpp) that
// bundle the opening "{" of the arguments JSON with the start chunk instead
// of sending arguments:"" first and the "{" as a separate delta.
func TestParseStreamToolCallsBundledArgs(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{"}}]},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"path\":\"/tmp\"}"}}]},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`},
		{Data: "[DONE]"},
	})

	var gotStart bool
	var args string

	for _, e := range events {
		switch e.Type {
		case api.EventToolCallStart:
			gotStart = true
			if e.ToolCallID != "call_1" {
				t.Errorf("ToolCallID = %q", e.ToolCallID)
			}
			if e.ToolCallName != "read" {
				t.Errorf("ToolCallName = %q", e.ToolCallName)
			}
		case api.EventToolCallDelta:
			args += e.ToolCallArgs
		}
	}

	if !gotStart {
		t.Error("missing EventToolCallStart")
	}
	if args != `{"path":"/tmp"}` {
		t.Errorf("args = %q, want %q", args, `{"path":"/tmp"}`)
	}
}

// TestParseStreamToolCallsSingleChunk covers servers that emit a complete
// tool call (id + name + full arguments JSON) in a single delta with no
// further argument chunks. Common for short tool calls (e.g. list with
// only a `path` arg) on llama.cpp and some Ollama builds.
func TestParseStreamToolCallsSingleChunk(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list","arguments":"{\"path\":\"/tmp\"}"}}]},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`},
		{Data: "[DONE]"},
	})

	var gotStart bool
	var args string
	for _, e := range events {
		switch e.Type {
		case api.EventToolCallStart:
			gotStart = true
			if e.ToolCallID != "call_1" || e.ToolCallName != "list" {
				t.Errorf("Start id=%q name=%q", e.ToolCallID, e.ToolCallName)
			}
		case api.EventToolCallDelta:
			args += e.ToolCallArgs
		}
	}
	if !gotStart {
		t.Error("missing EventToolCallStart")
	}
	if args != `{"path":"/tmp"}` {
		t.Errorf("args = %q, want %q", args, `{"path":"/tmp"}`)
	}
}

func TestParseStreamDone(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: "[DONE]"},
	})
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != api.EventDone {
		t.Errorf("Type = %q, want %q", events[0].Type, api.EventDone)
	}
}

func TestParseStreamEmptyDelta(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":null}]}`},
	})
	// Empty delta with no finish_reason should produce no events
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

// TestParseStreamReasoningContent covers the chain-of-thought channel
// that llama.cpp (--reasoning-format deepseek) and vLLM
// (--enable-reasoning) emit alongside or before regular content.
// reasoning_content deltas must surface as EventThinkingDelta so the
// Ctrl+O overlay can render them; they must NOT bleed into the regular
// text delta stream that drives the chat body.
func TestParseStreamReasoningContent(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"Let me"},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":" think."},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"content":"The answer is 42."},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
		{Data: "[DONE]"},
	})

	var thinking, text []string
	for _, e := range events {
		switch e.Type {
		case api.EventThinkingDelta:
			thinking = append(thinking, e.Delta)
		case api.EventDelta:
			text = append(text, e.Delta)
		}
	}
	if len(thinking) != 2 || thinking[0] != "Let me" || thinking[1] != " think." {
		t.Errorf("thinking deltas = %v, want [Let me  think.]", thinking)
	}
	if len(text) != 1 || text[0] != "The answer is 42." {
		t.Errorf("text deltas = %v, want [The answer is 42.]", text)
	}
}

// TestParseStreamOpenRouterReasoning covers OpenRouter's unified
// reasoning channel — the `reasoning` key (not `reasoning_content`).
// Without this, OpenRouter's DeepSeek/Kimi/GLM thinking variants
// stream chain-of-thought that's silently dropped, leaving the Ctrl+O
// overlay empty.
func TestParseStreamOpenRouterReasoning(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"reasoning":"weighing"},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"reasoning":" options"},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`},
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
		{Data: "[DONE]"},
	})

	var thinking, text []string
	for _, e := range events {
		switch e.Type {
		case api.EventThinkingDelta:
			thinking = append(thinking, e.Delta)
		case api.EventDelta:
			text = append(text, e.Delta)
		}
	}
	if len(thinking) != 2 || thinking[0] != "weighing" || thinking[1] != " options" {
		t.Errorf("thinking deltas = %v, want [weighing  options]", thinking)
	}
	if len(text) != 1 || text[0] != "hi" {
		t.Errorf("text deltas = %v, want [hi]", text)
	}
}

// TestParseStreamReasoningContentInterleaved guards against servers
// that bundle reasoning_content and content in the same chunk. Both
// channels must surface independently — neither one should swallow the
// other.
func TestParseStreamReasoningContentInterleaved(t *testing.T) {
	events := collectStream([]sse.Event{
		{Data: `{"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"think","content":"speak"},"finish_reason":null}]}`},
	})
	saw := map[api.StreamEventType]string{}
	for _, e := range events {
		saw[e.Type] = e.Delta
	}
	if saw[api.EventThinkingDelta] != "think" {
		t.Errorf("thinking = %q, want %q", saw[api.EventThinkingDelta], "think")
	}
	if saw[api.EventDelta] != "speak" {
		t.Errorf("text = %q, want %q", saw[api.EventDelta], "speak")
	}
}
