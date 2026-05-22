package compact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

func TestEstimateMessage_Text(t *testing.T) {
	text := strings.Repeat("x", 1000) // 1000 chars → ~250 tokens + overhead
	msg := api.NewUserMessage(text)
	got := EstimateMessage(msg)
	// 250 (text) + overheadPerMessage (15) = 265.
	if got < 250 || got > 270 {
		t.Errorf("EstimateMessage(1000 chars) = %d; want 250-270", got)
	}
}

func TestEstimateMessage_Empty(t *testing.T) {
	msg := api.Message{Role: api.RoleUser}
	if got := EstimateMessage(msg); got != overheadPerMessage {
		t.Errorf("empty msg = %d, want overheadPerMessage=%d", got, overheadPerMessage)
	}
}

func TestEstimateMessage_ToolUse(t *testing.T) {
	input := strings.Repeat("a", 200) // 200 chars → 50 tokens
	msg := api.Message{
		Role: api.RoleAssistant,
		Content: []api.ContentBlock{{
			Type: api.BlockToolUse,
			ToolCall: &api.ToolCall{
				Name:  "grep",
				Input: json.RawMessage(input),
			},
		}},
	}
	// 50 (input) + 1 (name) + 15 (overhead) = 66
	got := EstimateMessage(msg)
	if got < 64 || got > 70 {
		t.Errorf("tool_use estimate = %d, want ~66", got)
	}
}

func TestEstimateMessage_ToolResult(t *testing.T) {
	content := strings.Repeat("y", 400) // 400 chars → 100 tokens
	msg := api.Message{
		Role: api.RoleTool,
		Content: []api.ContentBlock{{
			Type: api.BlockToolResult,
			ToolResult: &api.ToolResult{
				ToolCallID: "tc_1",
				Content:    content,
			},
		}},
	}
	got := EstimateMessage(msg)
	// 100 (content) + 15 (overhead) = 115.
	if got < 110 || got > 120 {
		t.Errorf("tool_result estimate = %d, want 110-120", got)
	}
}

func TestEstimateConversation_Empty(t *testing.T) {
	conv := agent.NewConversation()
	if got := EstimateConversation(conv); got != 0 {
		t.Errorf("empty conv = %d, want 0", got)
	}
}

func TestEstimateConversation_NilSafe(t *testing.T) {
	if got := EstimateConversation(nil); got != 0 {
		t.Errorf("nil conv = %d, want 0", got)
	}
}

func TestEstimateConversation_Sum(t *testing.T) {
	conv := agent.NewConversation()
	conv.Append(api.NewUserMessage(strings.Repeat("x", 400)))  // ~105
	conv.Append(api.NewUserMessage(strings.Repeat("y", 800)))  // ~205
	conv.Append(api.NewUserMessage(strings.Repeat("z", 1200))) // ~305

	got := EstimateConversation(conv)
	// 400/4+15 + 800/4+15 + 1200/4+15 = 115 + 215 + 315 = 645
	if got != 645 {
		t.Errorf("sum = %d, want 645", got)
	}
}
