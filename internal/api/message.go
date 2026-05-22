package api

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Role represents who authored a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// BlockType identifies the kind of content in a ContentBlock.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockThinking   BlockType = "thinking"
)

// ContentBlock is one piece of a message. A single message can contain
// multiple blocks (e.g., text + tool_use, or text + tool_result).
type ContentBlock struct {
	Type       BlockType      `json:"type"`
	Text       string         `json:"text,omitzero"`
	ToolCall   *ToolCall      `json:"tool_call,omitzero"`
	ToolResult *ToolResult    `json:"tool_result,omitzero"`
	Thinking   *ThinkingBlock `json:"thinking,omitzero"`
}

// ThinkingBlock is the assistant's chain-of-thought for a turn. The
// signature is server-issued and MUST be round-tripped unmodified on
// subsequent turns whenever the same assistant message also contains
// tool_use — Anthropic verifies it before accepting the next tool result.
// Redacted blocks carry an opaque encrypted payload in Data instead of
// human-readable text.
type ThinkingBlock struct {
	Text      string `json:"text,omitzero"`
	Signature string `json:"signature,omitzero"`
	Redacted  bool   `json:"redacted,omitzero"`
	Data      string `json:"data,omitzero"`
}

// ToolCall represents an LLM request to invoke a tool.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is the outcome of executing a tool.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitzero"`
}

// Message is the universal message type carried through the entire system.
type Message struct {
	ID        string         `json:"id"`
	Role      Role           `json:"role"`
	Content   []ContentBlock `json:"content"`
	CreatedAt time.Time      `json:"created_at"`
}

// Usage tracks token consumption for a single LLM response.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read,omitzero"`
	CacheWrite   int `json:"cache_write,omitzero"`
}

// SystemBlock is one section of the system prompt. Providers that support
// prompt caching (currently Anthropic) emit a cache marker on the last block
// where Cache is true; providers without caching concatenate the Text fields.
type SystemBlock struct {
	Text  string
	Cache bool
}

// Text returns the concatenated text of all text blocks in the message.
func (m Message) Text() string {
	var s string
	for _, b := range m.Content {
		if b.Type == BlockText {
			s += b.Text
		}
	}
	return s
}

// ToolCalls returns all tool call blocks in the message.
func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, b := range m.Content {
		if b.Type == BlockToolUse && b.ToolCall != nil {
			calls = append(calls, *b.ToolCall)
		}
	}
	return calls
}

// NewUserMessage creates a user message with a single text block.
func NewUserMessage(text string) Message {
	return Message{
		ID:   uuid.New().String(),
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: BlockText, Text: text},
		},
		CreatedAt: time.Now(),
	}
}

// NewAssistantMessage creates an empty assistant message for streaming accumulation.
func NewAssistantMessage() Message {
	return Message{
		ID:        uuid.New().String(),
		Role:      RoleAssistant,
		CreatedAt: time.Now(),
	}
}
