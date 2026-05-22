package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/marcomoesman/prompto/internal/api"
)

// Wire format types (unexported) — match Anthropic API exactly.

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      any                `json:"system,omitzero"` // []anthropicSystemBlock when sectioned
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitzero"`
	Stream      bool               `json:"stream"`
	Temperature *float64           `json:"temperature,omitzero"`
	TopP        *float64           `json:"top_p,omitzero"`
	Stop        []string           `json:"stop_sequences,omitzero"`
	Thinking    *anthropicThinking `json:"thinking,omitzero"`
}

// anthropicThinking turns on extended thinking. BudgetTokens caps the
// reasoning budget per turn; max_tokens must exceed it. We default to
// 4096 in EncodeRequest — large enough to be useful, small enough to
// keep latency reasonable.
type anthropicThinking struct {
	Type         string `json:"type"` // "enabled"
	BudgetTokens int    `json:"budget_tokens"`
}

// thinkingBudgetTokens is the per-turn reasoning budget. Picked to be
// generous enough for non-trivial planning without unbounded latency on
// short turns. max_tokens (default 8192) must remain greater than this.
const thinkingBudgetTokens = 4096

// anthropicSystemBlock is a single text block within the Anthropic "system"
// array form. CacheControl on a block marks the cache boundary — that block
// and everything before it (including tools) is cached for ~5 minutes.
type anthropicSystemBlock struct {
	Type         string                 `json:"type"` // "text"
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitzero"`
}

type anthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicContent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitzero"`
	ID        string `json:"id,omitzero"`          // tool_use
	Name      string `json:"name,omitzero"`        // tool_use
	Input     any    `json:"input,omitzero"`       // tool_use (raw JSON object)
	ToolUseID string `json:"tool_use_id,omitzero"` // tool_result
	Content   any    `json:"content,omitzero"`     // tool_result
	IsError   bool   `json:"is_error,omitzero"`    // tool_result
	Thinking  string `json:"thinking,omitzero"`    // type=thinking
	Signature string `json:"signature,omitzero"`   // type=thinking
	Data      string `json:"data,omitzero"`        // type=redacted_thinking
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitzero"`
}

// EncodeRequest converts CompleteParams to the Anthropic wire format JSON.
// Extended thinking is always on. Anthropic forbids non-default temperature
// and top_p whenever thinking is enabled, so both fields are dropped — the
// model runs at its native sampling profile.
func EncodeRequest(params api.CompleteParams) ([]byte, error) {
	req := anthropicRequest{
		Model:     params.Model,
		MaxTokens: params.MaxTokens,
		Messages:  encodeMessages(params.Messages),
		Stream:    true,
		Stop:      params.Stop,
		Thinking:  &anthropicThinking{Type: "enabled", BudgetTokens: thinkingBudgetTokens},
	}
	if len(params.System) > 0 {
		req.System = encodeSystemBlocks(params.System)
	}

	for _, t := range params.Tools {
		req.Tools = append(req.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	// Mark the last tool cacheable. Anthropic's cache lookup walks
	// tools → system → messages, and a cache_control entry caches that
	// component and everything before it. Tools are byte-stable across
	// turns within a session (the agent's allowlist doesn't shift mid-
	// conversation), so attaching the breakpoint here lets the full tool
	// schema array hit the prompt cache instead of being re-billed each
	// turn. The existing breakpoint on the stable system block remains —
	// Anthropic permits up to 4.
	if n := len(req.Tools); n > 0 {
		req.Tools[n-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	return json.Marshal(req)
}

// encodeSystemBlocks emits the array form of Anthropic's system field. Each
// api.SystemBlock maps to one anthropicSystemBlock of type "text"; Cache:true
// attaches cache_control: {type: "ephemeral"} to that block.
func encodeSystemBlocks(blocks []api.SystemBlock) []anthropicSystemBlock {
	out := make([]anthropicSystemBlock, 0, len(blocks))
	for _, b := range blocks {
		ab := anthropicSystemBlock{Type: "text", Text: b.Text}
		if b.Cache {
			ab.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
		}
		out = append(out, ab)
	}
	return out
}

// encodeMessages converts api.Messages to Anthropic wire format.
// System messages are excluded (handled via the top-level system field).
func encodeMessages(msgs []api.Message) []anthropicMessage {
	var result []anthropicMessage

	for _, msg := range msgs {
		switch msg.Role {
		case api.RoleSystem:
			// Skip — system prompt is a top-level field in Anthropic API
			continue

		case api.RoleUser:
			am := anthropicMessage{Role: "user"}
			for _, block := range msg.Content {
				switch block.Type {
				case api.BlockText:
					am.Content = append(am.Content, anthropicContent{
						Type: "text",
						Text: block.Text,
					})
				case api.BlockToolResult:
					if block.ToolResult != nil {
						am.Content = append(am.Content, anthropicContent{
							Type:      "tool_result",
							ToolUseID: block.ToolResult.ToolCallID,
							Content:   block.ToolResult.Content,
							IsError:   block.ToolResult.IsError,
						})
					}
				}
			}
			result = append(result, am)

		case api.RoleAssistant:
			am := anthropicMessage{Role: "assistant"}
			for _, block := range msg.Content {
				switch block.Type {
				case api.BlockThinking:
					if block.Thinking == nil {
						continue
					}
					if block.Thinking.Redacted {
						am.Content = append(am.Content, anthropicContent{
							Type: "redacted_thinking",
							Data: block.Thinking.Data,
						})
						continue
					}
					// Drop thinking blocks lacking a signature — Anthropic
					// rejects unsigned thinking on inbound messages. This
					// path covers older sessions persisted before signature
					// capture, and the rare case where the stream cut off
					// before the signature delta arrived.
					if block.Thinking.Signature == "" {
						continue
					}
					am.Content = append(am.Content, anthropicContent{
						Type:      "thinking",
						Thinking:  block.Thinking.Text,
						Signature: block.Thinking.Signature,
					})
				case api.BlockText:
					am.Content = append(am.Content, anthropicContent{
						Type: "text",
						Text: block.Text,
					})
				case api.BlockToolUse:
					if block.ToolCall != nil {
						var input any
						if len(block.ToolCall.Input) > 0 {
							_ = json.Unmarshal(block.ToolCall.Input, &input)
						}
						am.Content = append(am.Content, anthropicContent{
							Type:  "tool_use",
							ID:    block.ToolCall.ID,
							Name:  block.ToolCall.Name,
							Input: input,
						})
					}
				}
			}
			result = append(result, am)

		case api.RoleTool:
			// Tool results are sent as user messages with tool_result content blocks
			am := anthropicMessage{Role: "user"}
			for _, block := range msg.Content {
				if block.Type == api.BlockToolResult && block.ToolResult != nil {
					am.Content = append(am.Content, anthropicContent{
						Type:      "tool_result",
						ToolUseID: block.ToolResult.ToolCallID,
						Content:   block.ToolResult.Content,
						IsError:   block.ToolResult.IsError,
					})
				}
			}
			result = append(result, am)

		default:
			// Unknown role, skip
		}
	}

	return result
}

// Stream event parsing types

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitzero"`
	PartialJSON string `json:"partial_json,omitzero"`
	StopReason  string `json:"stop_reason,omitzero"`
	Thinking    string `json:"thinking,omitzero"`
	Signature   string `json:"signature,omitzero"`
}

type anthropicBlock struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitzero"`
	Name     string `json:"name,omitzero"`
	Thinking string `json:"thinking,omitzero"`
	Data     string `json:"data,omitzero"`
}

type anthropicMessageBody struct {
	ID    string         `json:"id"`
	Usage anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitzero"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitzero"`
}

type anthropicMessageDelta struct {
	Delta anthropicDelta `json:"delta"`
	Usage anthropicUsage `json:"usage"`
}

// parseStreamEvent converts a raw SSE data payload into an api.StreamEvent.
// Returns the event and true if it should be yielded, or false to skip.
func parseStreamEvent(eventType, data string) (api.StreamEvent, bool) {
	switch eventType {
	case "message_start":
		var envelope struct {
			Message anthropicMessageBody `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("parsing message_start: %w", err)}, true
		}
		return api.StreamEvent{
			Type: api.EventUsage,
			Usage: &api.Usage{
				InputTokens:  envelope.Message.Usage.InputTokens,
				OutputTokens: envelope.Message.Usage.OutputTokens,
				CacheRead:    envelope.Message.Usage.CacheReadInputTokens,
				CacheWrite:   envelope.Message.Usage.CacheCreationInputTokens,
			},
		}, true

	case "content_block_start":
		var evt struct {
			Index        int            `json:"index"`
			ContentBlock anthropicBlock `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("parsing content_block_start: %w", err)}, true
		}
		switch evt.ContentBlock.Type {
		case "tool_use":
			return api.StreamEvent{
				Type:          api.EventToolCallStart,
				ToolCallID:    evt.ContentBlock.ID,
				ToolCallName:  evt.ContentBlock.Name,
				ToolCallIndex: evt.Index,
			}, true
		case "thinking":
			return api.StreamEvent{
				Type:          api.EventThinkingStart,
				ThinkingIndex: evt.Index,
			}, true
		case "redacted_thinking":
			// Redacted blocks carry their full payload up-front and never
			// emit deltas. Emit a single Start event with the data; the
			// agent loop treats it as a complete thinking block.
			return api.StreamEvent{
				Type:                 api.EventThinkingStart,
				ThinkingIndex:        evt.Index,
				ThinkingRedacted:     true,
				ThinkingRedactedData: evt.ContentBlock.Data,
			}, true
		}
		// Text block start — no event to yield
		return api.StreamEvent{}, false

	case "content_block_delta":
		var evt struct {
			Index int            `json:"index"`
			Delta anthropicDelta `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("parsing content_block_delta: %w", err)}, true
		}
		switch evt.Delta.Type {
		case "text_delta":
			return api.StreamEvent{
				Type:  api.EventDelta,
				Delta: evt.Delta.Text,
			}, true
		case "input_json_delta":
			return api.StreamEvent{
				Type:          api.EventToolCallDelta,
				ToolCallArgs:  evt.Delta.PartialJSON,
				ToolCallIndex: evt.Index,
			}, true
		case "thinking_delta":
			return api.StreamEvent{
				Type:          api.EventThinkingDelta,
				Delta:         evt.Delta.Thinking,
				ThinkingIndex: evt.Index,
			}, true
		case "signature_delta":
			return api.StreamEvent{
				Type:              api.EventThinkingDelta,
				ThinkingIndex:     evt.Index,
				ThinkingSignature: evt.Delta.Signature,
			}, true
		}
		return api.StreamEvent{}, false

	case "content_block_stop":
		// Could emit EventToolCallDone, but we defer that to message_stop
		return api.StreamEvent{}, false

	case "message_delta":
		var evt anthropicMessageDelta
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("parsing message_delta: %w", err)}, true
		}
		// Emit usage update with output tokens and any cache tokens Anthropic
		// reports at message_delta (some models report cache numbers here
		// instead of on message_start).
		return api.StreamEvent{
			Type: api.EventUsage,
			Usage: &api.Usage{
				OutputTokens: evt.Usage.OutputTokens,
				CacheRead:    evt.Usage.CacheReadInputTokens,
				CacheWrite:   evt.Usage.CacheCreationInputTokens,
			},
			StopReason: evt.Delta.StopReason,
		}, true

	case "message_stop":
		return api.StreamEvent{Type: api.EventDone}, true

	case "ping":
		return api.StreamEvent{}, false

	case "error":
		return api.StreamEvent{
			Type:  api.EventError,
			Error: fmt.Errorf("anthropic stream error: %s", data),
		}, true

	default:
		// Unknown event type, skip
		return api.StreamEvent{}, false
	}
}
