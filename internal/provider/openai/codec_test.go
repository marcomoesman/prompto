package openai

import (
	"encoding/json"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestEncodeRequestSimpleChat(t *testing.T) {
	params := api.CompleteParams{
		Model:     "gpt-4o",
		System:    []api.SystemBlock{{Text: "You are helpful.", Cache: true}},
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "Hello"},
			}},
		},
	}

	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if req["model"] != "gpt-4o" {
		t.Errorf("model = %v", req["model"])
	}
	if req["stream"] != true {
		t.Errorf("stream = %v", req["stream"])
	}

	msgs := req["messages"].([]any)
	// Should have system + user = 2 messages
	if len(msgs) != 2 {
		t.Fatalf("messages count = %d, want 2", len(msgs))
	}
	// First message should be system
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Errorf("msgs[0].role = %v, want system", sys["role"])
	}
	if sys["content"] != "You are helpful." {
		t.Errorf("msgs[0].content = %v", sys["content"])
	}
	// Second should be user
	user := msgs[1].(map[string]any)
	if user["role"] != "user" {
		t.Errorf("msgs[1].role = %v, want user", user["role"])
	}

	// stream_options should include usage
	so := req["stream_options"].(map[string]any)
	if so["include_usage"] != true {
		t.Errorf("stream_options.include_usage = %v", so["include_usage"])
	}
}

func TestEncodeRequestSamplingSettings(t *testing.T) {
	temp := 0.7
	presence := 1.2
	data, err := EncodeRequest(api.CompleteParams{
		Model:           "qwen",
		MaxTokens:       1024,
		Temperature:     &temp,
		PresencePenalty: &presence,
		Messages: []api.Message{
			api.NewUserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if req["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", req["temperature"])
	}
	if req["presence_penalty"] != 1.2 {
		t.Errorf("presence_penalty = %v, want 1.2", req["presence_penalty"])
	}
}

func TestEncodeRequestOmitsUnsetSamplingSettings(t *testing.T) {
	data, err := EncodeRequest(api.CompleteParams{
		Model:     "qwen",
		MaxTokens: 1024,
		Messages:  []api.Message{api.NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := req["temperature"]; ok {
		t.Errorf("temperature should be omitted when unset")
	}
	if _, ok := req["presence_penalty"]; ok {
		t.Errorf("presence_penalty should be omitted when unset")
	}
}

// TestEncodeMessagesDropsTrailingEmptyAssistant covers the codec's
// defensive trim. llama.cpp's Qwen3-thinking template (and other
// prefill-aware servers) reject any request whose messages array
// ends with an `assistant` role entry that carries no content and no
// tool_calls — they treat it as an attempted "prefill", which is
// incompatible with enable_thinking. The agent loop's empty-turn
// guard is the primary defense; this is a belt-and-braces drop in
// the encoder so any future caller path that leaks an empty
// trailing assistant doesn't trigger the same 400.
func TestEncodeMessagesDropsTrailingEmptyAssistant(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "what's 2+2?"},
		}},
		// Empty assistant — no text content blocks, no tool_use.
		{Role: api.RoleAssistant, Content: nil},
	}

	encoded := encodeMessages(msgs, nil)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1 (empty trailing assistant should be dropped)", len(encoded))
	}
	if encoded[0].Role != "user" {
		t.Errorf("encoded[0].Role = %q, want user", encoded[0].Role)
	}
}

// TestEncodeMessagesPreservesTrailingTextOnlyAssistant asserts the
// trim doesn't over-fire — a final assistant message with visible
// text but no tool_calls is a normal end-of-turn response and must
// stay on the wire (e.g. when re-running the same conversation
// against a server that wants to score the response, or when the
// downstream provider supports prefill explicitly).
func TestEncodeMessagesPreservesTrailingTextOnlyAssistant(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "what's 2+2?"},
		}},
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "4."},
		}},
	}

	encoded := encodeMessages(msgs, nil)
	if len(encoded) != 2 {
		t.Fatalf("got %d messages, want 2 (text-only assistant must be preserved)", len(encoded))
	}
	if encoded[1].Role != "assistant" {
		t.Errorf("encoded[1].Role = %q, want assistant", encoded[1].Role)
	}
	if encoded[1].Content == nil || *encoded[1].Content != "4." {
		t.Errorf("encoded[1].Content = %v, want \"4.\"", encoded[1].Content)
	}
}

// TestEncodeMessagesPreservesTrailingToolCallOnlyAssistant covers
// the other valid trailing-assistant shape: no visible text but at
// least one tool_call. That's how a model says "go run this tool
// and come back to me" — the tool_use is the entire payload, and
// the codec must keep it.
func TestEncodeMessagesPreservesTrailingToolCallOnlyAssistant(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "list /tmp"},
		}},
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: api.BlockToolUse, ToolCall: &api.ToolCall{
				ID:    "call_1",
				Name:  "list",
				Input: json.RawMessage(`{"path":"/tmp"}`),
			}},
		}},
	}

	encoded := encodeMessages(msgs, nil)
	if len(encoded) != 2 {
		t.Fatalf("got %d messages, want 2 (tool_call-only assistant must be preserved)", len(encoded))
	}
	if len(encoded[1].ToolCalls) != 1 {
		t.Errorf("encoded[1] should carry one tool_call, got %d", len(encoded[1].ToolCalls))
	}
}

// TestEncodeMessagesDropsMultipleTrailingEmptyAssistants — the loop
// is iterative; if pathology ever produces back-to-back empty
// assistants at the tail, drop them all.
func TestEncodeMessagesDropsMultipleTrailingEmptyAssistants(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "hello"},
		}},
		{Role: api.RoleAssistant, Content: nil},
		{Role: api.RoleAssistant, Content: nil},
	}
	encoded := encodeMessages(msgs, nil)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1 (both empty trailing assistants dropped)", len(encoded))
	}
}

func TestEncodeMessagesToolCallsInAssistant(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "Let me check."},
			{Type: api.BlockToolUse, ToolCall: &api.ToolCall{
				ID:    "call_1",
				Name:  "read",
				Input: json.RawMessage(`{"path":"/tmp"}`),
			}},
		}},
	}

	encoded := encodeMessages(msgs, nil)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1", len(encoded))
	}
	if encoded[0].Role != "assistant" {
		t.Errorf("role = %q", encoded[0].Role)
	}
	if len(encoded[0].ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(encoded[0].ToolCalls))
	}
	tc := encoded[0].ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("tc.ID = %q", tc.ID)
	}
	if tc.Type != "function" {
		t.Errorf("tc.Type = %q", tc.Type)
	}
	if tc.Function.Name != "read" {
		t.Errorf("tc.Function.Name = %q", tc.Function.Name)
	}
}

func TestEncodeMessagesToolResult(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleTool, Content: []api.ContentBlock{
			{Type: api.BlockToolResult, ToolResult: &api.ToolResult{
				ToolCallID: "call_1",
				Content:    "file contents",
			}},
		}},
	}

	encoded := encodeMessages(msgs, nil)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1", len(encoded))
	}
	if encoded[0].Role != "tool" {
		t.Errorf("role = %q, want %q", encoded[0].Role, "tool")
	}
	if encoded[0].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q", encoded[0].ToolCallID)
	}
	if encoded[0].Content == nil || *encoded[0].Content != "file contents" {
		t.Errorf("content = %v", encoded[0].Content)
	}
}

func TestEncodeMessagesToolCallRoundTrip(t *testing.T) {
	// Simulate a full tool call round-trip: user → assistant(tool_use) → tool(result)
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "Fetch example.com"},
		}},
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			// Assistant has ONLY a tool call, no text
			{Type: api.BlockToolUse, ToolCall: &api.ToolCall{
				ID:    "call_abc",
				Name:  "webfetch",
				Input: json.RawMessage(`{"url":"https://example.com"}`),
			}},
		}},
		{Role: api.RoleTool, Content: []api.ContentBlock{
			{Type: api.BlockToolResult, ToolResult: &api.ToolResult{
				ToolCallID: "call_abc",
				Content:    "# Example Domain\nThis is a test page.",
			}},
		}},
	}

	data, err := EncodeRequest(api.CompleteParams{
		Model:     "gpt-4o",
		System:    []api.SystemBlock{{Text: "You are helpful.", Cache: true}},
		MaxTokens: 1024,
		Messages:  msgs,
	})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	// Parse the raw JSON to verify exact wire format.
	var req map[string]any
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	rawMsgs := req["messages"].([]any)
	// system + user + assistant + tool = 4
	if len(rawMsgs) != 4 {
		t.Fatalf("messages count = %d, want 4", len(rawMsgs))
	}

	// Assistant message (index 2) must have content: null, not missing
	assistantRaw := rawMsgs[2].(map[string]any)
	if assistantRaw["role"] != "assistant" {
		t.Fatalf("msg[2].role = %v, want assistant", assistantRaw["role"])
	}
	// content key MUST exist and be null (not absent)
	contentVal, contentExists := assistantRaw["content"]
	if !contentExists {
		t.Fatal("assistant message with tool calls must have 'content' key (even if null)")
	}
	if contentVal != nil {
		t.Errorf("assistant message content = %v, want null", contentVal)
	}
	// tool_calls must be present
	toolCalls, ok := assistantRaw["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %v", assistantRaw["tool_calls"])
	}

	// Tool result message (index 3) must have content and tool_call_id
	toolRaw := rawMsgs[3].(map[string]any)
	if toolRaw["role"] != "tool" {
		t.Fatalf("msg[3].role = %v, want tool", toolRaw["role"])
	}
	if toolRaw["tool_call_id"] != "call_abc" {
		t.Errorf("tool_call_id = %v", toolRaw["tool_call_id"])
	}
	if toolRaw["content"] != "# Example Domain\nThis is a test page." {
		t.Errorf("tool content = %v", toolRaw["content"])
	}
}

func TestEncodeRequest_FlattensSystemBlocks(t *testing.T) {
	params := api.CompleteParams{
		Model: "gpt-4o",
		System: []api.SystemBlock{
			{Text: "A"},
			{Text: "B", Cache: true}, // cache flag silently ignored for OpenAI
		},
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{{Type: api.BlockText, Text: "hi"}}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	msgs := req["messages"].([]any)
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Fatalf("first msg role = %v", sys["role"])
	}
	if sys["content"] != "A\n\nB" {
		t.Errorf("system content = %q, want %q", sys["content"], "A\n\nB")
	}
	if _, ok := sys["cache_control"]; ok {
		t.Error("OpenAI system message must not carry cache_control")
	}
}

func TestEncodeRequest_EmptySystemProducesNoSystemMessage(t *testing.T) {
	params := api.CompleteParams{
		Model:     "gpt-4o",
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{{Type: api.BlockText, Text: "hi"}}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	msgs := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (no system)", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("first msg role = %v", msgs[0].(map[string]any)["role"])
	}
}

func TestEncodeMessagesSystemExcluded(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleSystem, Content: []api.ContentBlock{{Type: api.BlockText, Text: "ignored"}}},
		{Role: api.RoleUser, Content: []api.ContentBlock{{Type: api.BlockText, Text: "hello"}}},
	}

	encoded := encodeMessages(msgs, []api.SystemBlock{{Text: "system prompt"}})
	// Should have: system (from param) + user = 2 (api.RoleSystem message is skipped)
	if len(encoded) != 2 {
		t.Fatalf("got %d messages, want 2", len(encoded))
	}
	if encoded[0].Role != "system" {
		t.Errorf("first role = %q, want system", encoded[0].Role)
	}
	if encoded[0].Content == nil || *encoded[0].Content != "system prompt" {
		t.Errorf("system content = %v", encoded[0].Content)
	}
}
