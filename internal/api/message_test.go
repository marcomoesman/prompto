package api

import (
	"encoding/json"
	"testing"
)

func TestMessageText(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: BlockText, Text: "Hello "},
			{Type: BlockText, Text: "world"},
		},
	}
	if got := msg.Text(); got != "Hello world" {
		t.Errorf("Text() = %q, want %q", got, "Hello world")
	}
}

func TestMessageTextEmpty(t *testing.T) {
	msg := Message{Role: RoleAssistant}
	if got := msg.Text(); got != "" {
		t.Errorf("Text() = %q, want empty", got)
	}
}

func TestMessageTextSkipsNonTextBlocks(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: BlockText, Text: "before "},
			{Type: BlockToolUse, ToolCall: &ToolCall{ID: "1", Name: "read"}},
			{Type: BlockText, Text: "after"},
		},
	}
	if got := msg.Text(); got != "before after" {
		t.Errorf("Text() = %q, want %q", got, "before after")
	}
}

func TestMessageToolCalls(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: BlockText, Text: "Let me read that."},
			{Type: BlockToolUse, ToolCall: &ToolCall{
				ID:    "tc_1",
				Name:  "read",
				Input: json.RawMessage(`{"file_path":"/tmp/foo"}`),
			}},
			{Type: BlockToolUse, ToolCall: &ToolCall{
				ID:    "tc_2",
				Name:  "bash",
				Input: json.RawMessage(`{"command":"ls"}`),
			}},
		},
	}
	calls := msg.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("ToolCalls() returned %d calls, want 2", len(calls))
	}
	if calls[0].Name != "read" {
		t.Errorf("calls[0].Name = %q, want %q", calls[0].Name, "read")
	}
	if calls[1].Name != "bash" {
		t.Errorf("calls[1].Name = %q, want %q", calls[1].Name, "bash")
	}
}

func TestMessageToolCallsEmpty(t *testing.T) {
	msg := Message{
		Role:    RoleAssistant,
		Content: []ContentBlock{{Type: BlockText, Text: "hi"}},
	}
	if calls := msg.ToolCalls(); len(calls) != 0 {
		t.Errorf("ToolCalls() returned %d calls, want 0", len(calls))
	}
}

func TestNewUserMessage(t *testing.T) {
	msg := NewUserMessage("hello")
	if msg.Role != RoleUser {
		t.Errorf("Role = %q, want %q", msg.Role, RoleUser)
	}
	if msg.ID == "" {
		t.Error("ID is empty")
	}
	if msg.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "hello" {
		t.Errorf("Content = %+v, want single text block with 'hello'", msg.Content)
	}
}

func TestNewAssistantMessage(t *testing.T) {
	msg := NewAssistantMessage()
	if msg.Role != RoleAssistant {
		t.Errorf("Role = %q, want %q", msg.Role, RoleAssistant)
	}
	if msg.ID == "" {
		t.Error("ID is empty")
	}
	if len(msg.Content) != 0 {
		t.Errorf("Content should be empty, got %d blocks", len(msg.Content))
	}
}

func TestMessageJSONRoundTrip(t *testing.T) {
	original := Message{
		ID:   "test-id",
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: BlockText, Text: "Hello"},
			{Type: BlockToolUse, ToolCall: &ToolCall{
				ID:    "tc_1",
				Name:  "read",
				Input: json.RawMessage(`{"path":"/tmp"}`),
			}},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Role != original.Role {
		t.Errorf("Role = %q, want %q", decoded.Role, original.Role)
	}
	if len(decoded.Content) != 2 {
		t.Fatalf("Content length = %d, want 2", len(decoded.Content))
	}
	if decoded.Content[0].Text != "Hello" {
		t.Errorf("Content[0].Text = %q, want %q", decoded.Content[0].Text, "Hello")
	}
	if decoded.Content[1].ToolCall == nil {
		t.Fatal("Content[1].ToolCall is nil")
	}
	if decoded.Content[1].ToolCall.Name != "read" {
		t.Errorf("ToolCall.Name = %q, want %q", decoded.Content[1].ToolCall.Name, "read")
	}
}

func TestToolResultJSONRoundTrip(t *testing.T) {
	original := ContentBlock{
		Type: BlockToolResult,
		ToolResult: &ToolResult{
			ToolCallID: "tc_1",
			Content:    "file contents here",
			IsError:    false,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ContentBlock
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Type != BlockToolResult {
		t.Errorf("Type = %q, want %q", decoded.Type, BlockToolResult)
	}
	if decoded.ToolResult == nil {
		t.Fatal("ToolResult is nil")
	}
	if decoded.ToolResult.ToolCallID != "tc_1" {
		t.Errorf("ToolCallID = %q, want %q", decoded.ToolResult.ToolCallID, "tc_1")
	}
	if decoded.ToolResult.Content != "file contents here" {
		t.Errorf("Content = %q, want %q", decoded.ToolResult.Content, "file contents here")
	}
}
