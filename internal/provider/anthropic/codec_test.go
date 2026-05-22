package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestEncodeRequestSimpleChat(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
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

	if req["model"] != "claude-sonnet-4-20250514" {
		t.Errorf("model = %v", req["model"])
	}
	sysArr, ok := req["system"].([]any)
	if !ok || len(sysArr) != 1 {
		t.Fatalf("system should be a 1-element array, got %T %v", req["system"], req["system"])
	}
	sysBlock := sysArr[0].(map[string]any)
	if sysBlock["type"] != "text" || sysBlock["text"] != "You are helpful." {
		t.Errorf("system block = %v", sysBlock)
	}
	cc, ok := sysBlock["cache_control"].(map[string]any)
	if !ok || cc["type"] != "ephemeral" {
		t.Errorf("cache_control = %v", sysBlock["cache_control"])
	}
	if req["stream"] != true {
		t.Errorf("stream = %v", req["stream"])
	}
	if req["max_tokens"] != float64(1024) {
		t.Errorf("max_tokens = %v", req["max_tokens"])
	}

	msgs := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages count = %d, want 1", len(msgs))
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v", msg["role"])
	}
}

func TestEncodeRequest_OmitsSystemWhenEmpty(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "hi"},
			}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	if _, present := req["system"]; present {
		t.Errorf("system should be omitted when no blocks, got %v", req["system"])
	}
}

func TestEncodeRequest_NoCacheControlWhenNoneMarked(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
		System:    []api.SystemBlock{{Text: "a"}, {Text: "b"}},
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "hi"},
			}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	arr := req["system"].([]any)
	if len(arr) != 2 {
		t.Fatalf("system blocks = %d, want 2", len(arr))
	}
	for i, item := range arr {
		block := item.(map[string]any)
		if _, ok := block["cache_control"]; ok {
			t.Errorf("block %d has cache_control, should not", i)
		}
	}
}

func TestEncodeRequest_EmitsSystemAsArrayWithCacheControl(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
		System:    []api.SystemBlock{{Text: "a", Cache: true}, {Text: "b"}},
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "hi"},
			}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	arr := req["system"].([]any)
	if len(arr) != 2 {
		t.Fatalf("system blocks = %d, want 2", len(arr))
	}
	first := arr[0].(map[string]any)
	second := arr[1].(map[string]any)
	if first["text"] != "a" {
		t.Errorf("first.text = %v", first["text"])
	}
	if cc, ok := first["cache_control"].(map[string]any); !ok || cc["type"] != "ephemeral" {
		t.Errorf("first.cache_control = %v", first["cache_control"])
	}
	if second["text"] != "b" {
		t.Errorf("second.text = %v", second["text"])
	}
	if _, ok := second["cache_control"]; ok {
		t.Error("second block should not have cache_control")
	}
}

func TestParseStreamEvent_PopulatesCacheTokens(t *testing.T) {
	data := `{"message":{"id":"msg_1","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":50,"cache_creation_input_tokens":25}}}`
	ev, ok := parseStreamEvent("message_start", data)
	if !ok {
		t.Fatal("parseStreamEvent returned false")
	}
	if ev.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if ev.Usage.CacheRead != 50 {
		t.Errorf("CacheRead = %d, want 50", ev.Usage.CacheRead)
	}
	if ev.Usage.CacheWrite != 25 {
		t.Errorf("CacheWrite = %d, want 25", ev.Usage.CacheWrite)
	}
}

func TestEncodeRequestWithTools(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "Read the file"},
			}},
		},
		Tools: []api.ToolDefinition{
			{
				Name:        "read",
				Description: "Read a file",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
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

	tools := req["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "read" {
		t.Errorf("tool name = %v", tool["name"])
	}
	if tool["input_schema"] == nil {
		t.Error("tool input_schema is nil")
	}
}

// TestEncodeRequest_LastToolCacheable verifies the prompt-caching breakpoint
// on the final tool entry. Tools are byte-stable across turns within a
// session, so this lets the entire tool-schema array hit the prompt cache
// instead of being re-billed every request.
func TestEncodeRequest_LastToolCacheable(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "hi"},
			}},
		},
		Tools: []api.ToolDefinition{
			{Name: "read", Description: "r", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "grep", Description: "g", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "bash", Description: "b", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	tools := req["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("tools count = %d, want 3", len(tools))
	}
	// Only the last tool should carry cache_control.
	for i, item := range tools {
		tool := item.(map[string]any)
		_, has := tool["cache_control"]
		want := i == len(tools)-1
		if has != want {
			t.Errorf("tool[%d] cache_control present=%v, want %v", i, has, want)
		}
	}
	last := tools[2].(map[string]any)
	cc, ok := last["cache_control"].(map[string]any)
	if !ok || cc["type"] != "ephemeral" {
		t.Errorf("last tool cache_control = %v, want ephemeral", last["cache_control"])
	}
}

// TestEncodeRequest_NoToolsNoCacheBreakpoint ensures we don't crash or
// inject anything when the request carries no tools at all.
func TestEncodeRequest_NoToolsNoCacheBreakpoint(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "hi"},
			}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	if _, ok := req["tools"]; ok {
		t.Errorf("tools field should be omitted when none provided, got %v", req["tools"])
	}
}

func TestEncodeMessagesSystemExcluded(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleSystem, Content: []api.ContentBlock{{Type: api.BlockText, Text: "system"}}},
		{Role: api.RoleUser, Content: []api.ContentBlock{{Type: api.BlockText, Text: "hello"}}},
	}

	encoded := encodeMessages(msgs)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1 (system excluded)", len(encoded))
	}
	if encoded[0].Role != "user" {
		t.Errorf("role = %q, want %q", encoded[0].Role, "user")
	}
}

func TestEncodeMessagesToolResult(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleTool, Content: []api.ContentBlock{
			{Type: api.BlockToolResult, ToolResult: &api.ToolResult{
				ToolCallID: "tc_1",
				Content:    "file contents",
				IsError:    false,
			}},
		}},
	}

	encoded := encodeMessages(msgs)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1", len(encoded))
	}
	// Tool results are sent as user role in Anthropic
	if encoded[0].Role != "user" {
		t.Errorf("role = %q, want %q", encoded[0].Role, "user")
	}
	if len(encoded[0].Content) != 1 {
		t.Fatalf("content count = %d, want 1", len(encoded[0].Content))
	}
	c := encoded[0].Content[0]
	if c.Type != "tool_result" {
		t.Errorf("type = %q, want %q", c.Type, "tool_result")
	}
	if c.ToolUseID != "tc_1" {
		t.Errorf("tool_use_id = %q, want %q", c.ToolUseID, "tc_1")
	}
}

func TestEncodeRequest_EnablesThinking(t *testing.T) {
	params := api.CompleteParams{
		Model:     "claude-sonnet-4-5",
		MaxTokens: 8192,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "hi"},
			}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	thinking, ok := req["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking missing, req = %v", req)
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking.type = %v, want enabled", thinking["type"])
	}
	if budget, _ := thinking["budget_tokens"].(float64); budget <= 0 {
		t.Errorf("thinking.budget_tokens = %v, want >0", thinking["budget_tokens"])
	}
}

func TestEncodeRequest_DropsTemperatureAndTopP(t *testing.T) {
	temp := 0.5
	topP := 0.9
	params := api.CompleteParams{
		Model:       "claude-sonnet-4-5",
		MaxTokens:   8192,
		Temperature: &temp,
		TopP:        &topP,
		Messages: []api.Message{
			{Role: api.RoleUser, Content: []api.ContentBlock{
				{Type: api.BlockText, Text: "hi"},
			}},
		},
	}
	data, err := EncodeRequest(params)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req map[string]any
	_ = json.Unmarshal(data, &req)
	if _, ok := req["temperature"]; ok {
		t.Errorf("temperature must be omitted when thinking is on, got %v", req["temperature"])
	}
	if _, ok := req["top_p"]; ok {
		t.Errorf("top_p must be omitted when thinking is on, got %v", req["top_p"])
	}
}

func TestEncodeMessagesAssistantWithThinking(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: api.BlockThinking, Thinking: &api.ThinkingBlock{
				Text:      "Let me reason.",
				Signature: "sig-1",
			}},
			{Type: api.BlockText, Text: "Here's the answer."},
			{Type: api.BlockToolUse, ToolCall: &api.ToolCall{
				ID: "tc_1", Name: "read",
				Input: json.RawMessage(`{"path":"/foo"}`),
			}},
		}},
	}
	encoded := encodeMessages(msgs)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1", len(encoded))
	}
	if len(encoded[0].Content) != 3 {
		t.Fatalf("content count = %d, want 3", len(encoded[0].Content))
	}
	if encoded[0].Content[0].Type != "thinking" {
		t.Errorf("content[0].type = %q", encoded[0].Content[0].Type)
	}
	if encoded[0].Content[0].Thinking != "Let me reason." {
		t.Errorf("content[0].thinking = %q", encoded[0].Content[0].Thinking)
	}
	if encoded[0].Content[0].Signature != "sig-1" {
		t.Errorf("content[0].signature = %q", encoded[0].Content[0].Signature)
	}
}

func TestEncodeMessagesAssistantDropsUnsignedThinking(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: api.BlockThinking, Thinking: &api.ThinkingBlock{
				Text: "no signature here",
			}},
			{Type: api.BlockText, Text: "ok"},
		}},
	}
	encoded := encodeMessages(msgs)
	if len(encoded[0].Content) != 1 {
		t.Fatalf("content count = %d, want 1 (unsigned thinking dropped)", len(encoded[0].Content))
	}
	if encoded[0].Content[0].Type != "text" {
		t.Errorf("content[0].type = %q, want text", encoded[0].Content[0].Type)
	}
}

func TestEncodeMessagesAssistantRedactedThinking(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: api.BlockThinking, Thinking: &api.ThinkingBlock{
				Redacted: true,
				Data:     "blob",
			}},
		}},
	}
	encoded := encodeMessages(msgs)
	if len(encoded[0].Content) != 1 {
		t.Fatalf("content count = %d, want 1", len(encoded[0].Content))
	}
	if encoded[0].Content[0].Type != "redacted_thinking" {
		t.Errorf("type = %q", encoded[0].Content[0].Type)
	}
	if encoded[0].Content[0].Data != "blob" {
		t.Errorf("data = %q", encoded[0].Content[0].Data)
	}
}

func TestEncodeMessagesAssistantWithToolUse(t *testing.T) {
	msgs := []api.Message{
		{Role: api.RoleAssistant, Content: []api.ContentBlock{
			{Type: api.BlockText, Text: "Let me read that."},
			{Type: api.BlockToolUse, ToolCall: &api.ToolCall{
				ID:    "tc_1",
				Name:  "read",
				Input: json.RawMessage(`{"path":"/tmp/foo"}`),
			}},
		}},
	}

	encoded := encodeMessages(msgs)
	if len(encoded) != 1 {
		t.Fatalf("got %d messages, want 1", len(encoded))
	}
	if len(encoded[0].Content) != 2 {
		t.Fatalf("content count = %d, want 2", len(encoded[0].Content))
	}
	if encoded[0].Content[0].Type != "text" {
		t.Errorf("content[0].type = %q", encoded[0].Content[0].Type)
	}
	if encoded[0].Content[1].Type != "tool_use" {
		t.Errorf("content[1].type = %q", encoded[0].Content[1].Type)
	}
	if encoded[0].Content[1].Name != "read" {
		t.Errorf("content[1].name = %q", encoded[0].Content[1].Name)
	}
}
