package compact

import (
	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// ClearedPlaceholder replaces the Content of a tool_result when the
// compactor decides it's old enough to drop. Tool_use blocks are never
// touched — Anthropic requires tool_use/tool_result pairs to remain paired.
const ClearedPlaceholder = "[tool result cleared to save context]"

// ClearOpts controls ClearOldToolResults behavior.
type ClearOpts struct {
	// KeepRecent is the count of messages from the tail to leave untouched.
	// Default 8 (roughly 4 user↔assistant round-trips).
	KeepRecent int
	// Resolver is consulted to skip clearing results from non-read-only
	// tools (Edit/Write/Bash outputs may still matter). A nil resolver
	// conservatively clears every tool_result.
	Resolver agent.ToolResolver
}

// ClearOldToolResults walks conv.All() and mutates the Content of older
// tool_result blocks in place, replacing them with ClearedPlaceholder.
// Tool_use blocks are untouched, so the tool_use/tool_result structural
// pairing stays intact. Returns the count of tool_results that were
// cleared.
//
// "Older" means before the cutoff at len(messages) - KeepRecent. Only
// tool_results for read-only tools are cleared unless Resolver is nil, in
// which case all old tool_results are cleared.
func ClearOldToolResults(conv *agent.Conversation, opts ClearOpts) int {
	if conv == nil {
		return 0
	}
	if opts.KeepRecent < 0 {
		opts.KeepRecent = 0
	}

	msgs := conv.All()
	if len(msgs) == 0 {
		return 0
	}

	// Build toolCallID → toolName index from every tool_use block in the
	// conversation. Needed so we know which tool emitted each tool_result.
	// (tool_result only carries the tool_call_id, not the tool name.)
	toolNames := make(map[string]string, 16)
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == api.BlockToolUse && b.ToolCall != nil {
				toolNames[b.ToolCall.ID] = b.ToolCall.Name
			}
		}
	}

	cutoff := len(msgs) - opts.KeepRecent
	if cutoff <= 0 {
		return 0 // everything is recent; nothing to clear
	}

	var cleared int
	for i := 0; i < cutoff; i++ {
		for _, b := range msgs[i].Content {
			if b.Type != api.BlockToolResult || b.ToolResult == nil {
				continue
			}
			if b.ToolResult.Content == ClearedPlaceholder {
				continue // already cleared
			}
			if !shouldClearTool(toolNames[b.ToolResult.ToolCallID], opts.Resolver) {
				continue
			}
			b.ToolResult.Content = ClearedPlaceholder
			cleared++
		}
	}
	return cleared
}

// shouldClearTool returns true when the tool's output is safe to clear
// without losing state the model may still need. Read/Grep/Glob/List/
// WebFetch are read-only and safe. Edit/Write/Bash outputs are preserved.
// When the resolver is nil (tests, unknown sessions), return true — err
// on the side of aggressive clearing.
func shouldClearTool(toolName string, resolver agent.ToolResolver) bool {
	if resolver == nil {
		return true
	}
	if toolName == "" {
		return true // orphan tool_result — no tool_use found; safe to clear
	}
	tool, ok := resolver.Resolve(toolName)
	if !ok {
		return true // unknown tool — conservative: clear
	}
	return tool.IsReadOnly()
}
