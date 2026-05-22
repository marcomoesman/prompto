package api

// StreamEventType identifies the kind of streaming event.
type StreamEventType string

const (
	EventDelta         StreamEventType = "delta"
	EventToolCallStart StreamEventType = "tool_call_start"
	EventToolCallDelta StreamEventType = "tool_call_delta"
	EventToolCallDone  StreamEventType = "tool_call_done"
	EventThinkingStart StreamEventType = "thinking_start"
	EventThinkingDelta StreamEventType = "thinking_delta"
	EventDone          StreamEventType = "done"
	EventError         StreamEventType = "error"
	EventUsage         StreamEventType = "usage"
)

// StreamEvent represents one incremental event from a streaming LLM response.
// Flat struct — switch on Type to determine which fields are populated.
type StreamEvent struct {
	Type StreamEventType

	Delta         string // EventDelta: text chunk
	ToolCallID    string // EventToolCallStart: tool use ID
	ToolCallName  string // EventToolCallStart: tool name
	ToolCallArgs  string // EventToolCallDelta: partial JSON args
	ToolCallIndex int    // EventToolCallStart/Delta: index in content blocks
	Usage         *Usage // EventUsage
	Error         error  // EventError
	StopReason    string // EventDone: "end_turn", "tool_use", "stop", etc.

	// Thinking events. Index identifies the content block the chunk
	// belongs to (Anthropic emits one or more thinking blocks before
	// any text/tool_use blocks). On EventThinkingStart, ThinkingRedacted
	// indicates whether the block is encrypted; for redacted blocks the
	// full encrypted payload arrives once in ThinkingRedactedData and no
	// deltas follow. On EventThinkingDelta, exactly one of Delta (text
	// chunk) or ThinkingSignature (final per-block signature) is set.
	ThinkingIndex        int
	ThinkingRedacted     bool
	ThinkingRedactedData string
	ThinkingSignature    string
}
