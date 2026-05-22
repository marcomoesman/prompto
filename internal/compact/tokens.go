// Package compact implements two context-management layers for prompto's
// agent loop:
//
//   - Tool-result clearing blanks the content of older read-only tool
//     outputs (Read, Grep, Glob, List, WebFetch) while keeping
//     tool_use/tool_result pairing intact.
//   - Threshold-based summarization replaces the head of a long conversation
//     with a single synthetic user message wrapped in <compact_summary>.
//
// Both layers run before the next provider.Complete and are orchestrated
// by MaybeCompact. A reactive retry path in the agent loop invokes the
// summarizer directly when a provider returns a context-limit error
// despite the proactive check.
package compact

import (
	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

const (
	// charsPerToken: 4 conservatively overestimates real ratios (~3.5),
	// so compaction triggers a touch early — the safer side.
	charsPerToken = 4
	// overheadPerMessage approximates the role/id/block-type framing
	// a real tokenizer would count. Bumped from 5 to 15 to better
	// reflect JSON wire-format overhead on multi-block messages.
	overheadPerMessage = 15
	// thinkingBudgetEstimate is the worst-case token cost charged for a
	// redacted thinking block whose Text is unavailable. Mirrors the
	// Anthropic budget set in provider/anthropic/codec.go.
	thinkingBudgetEstimate = 4096
)

// EstimateMessage returns the approximate token count for one message. It
// sums text, tool_use input, tool_result content, and thinking blocks at
// charsPerToken. Zero-value messages return overheadPerMessage.
func EstimateMessage(m api.Message) int {
	n := overheadPerMessage
	for _, b := range m.Content {
		switch b.Type {
		case api.BlockText:
			n += len(b.Text) / charsPerToken
		case api.BlockToolUse:
			if b.ToolCall != nil {
				n += len(b.ToolCall.Name) / charsPerToken
				n += len(b.ToolCall.Input) / charsPerToken
			}
		case api.BlockToolResult:
			if b.ToolResult != nil {
				n += len(b.ToolResult.Content) / charsPerToken
			}
		case api.BlockThinking:
			if b.Thinking != nil {
				if b.Thinking.Redacted {
					// Opaque payload — assume the budget was used.
					n += thinkingBudgetEstimate
				} else {
					n += len(b.Thinking.Text) / charsPerToken
				}
			}
		}
	}
	return n
}

// EstimateConversation sums EstimateMessage across every message in conv.
// A nil conversation returns 0.
func EstimateConversation(conv *agent.Conversation) int {
	if conv == nil {
		return 0
	}
	var total int
	for _, m := range conv.All() {
		total += EstimateMessage(m)
	}
	return total
}
