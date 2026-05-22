package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
)

// Wire format types (unexported) — match OpenAI API exactly.

type openaiRequest struct {
	Model           string          `json:"model"`
	Messages        []openaiMessage `json:"messages"`
	Tools           []openaiTool    `json:"tools,omitzero"`
	MaxTokens       int             `json:"max_tokens,omitzero"`
	Temperature     *float64        `json:"temperature,omitzero"`
	TopP            *float64        `json:"top_p,omitzero"`
	PresencePenalty *float64        `json:"presence_penalty,omitzero"`
	Stop            []string        `json:"stop,omitzero"`
	Stream          bool            `json:"stream"`
	StreamOptions   *streamOptions  `json:"stream_options,omitzero"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content"` // pointer: null when absent, string when present
	ToolCalls  []openaiToolCall `json:"tool_calls,omitzero"`
	ToolCallID string           `json:"tool_call_id,omitzero"`
}

type openaiToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiTool struct {
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Stream chunk types

type openaiChunk struct {
	ID      string         `json:"id"`
	Choices []openaiChoice `json:"choices"`
	Usage   *openaiUsage   `json:"usage,omitzero"`
}

type openaiChoice struct {
	Index        int         `json:"index"`
	Delta        openaiDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type openaiDelta struct {
	Role    string  `json:"role,omitzero"`
	Content *string `json:"content,omitzero"` // pointer to distinguish null from ""
	// ReasoningContent is the chain-of-thought channel emitted by
	// servers configured to surface reasoning separately from
	// `content`. llama.cpp does this with `--reasoning-format deepseek`;
	// vLLM does it with `--enable-reasoning`. Field name matches the
	// DeepSeek-R1 / Qwen reasoning convention used by both servers.
	// Pointer so a missing field round-trips as nil rather than "".
	ReasoningContent *string `json:"reasoning_content,omitzero"`
	// Reasoning is OpenRouter's unified reasoning channel — same role as
	// ReasoningContent, different key. OpenRouter normalises every
	// upstream provider's chain-of-thought into a top-level `reasoning`
	// string on the delta (DeepSeek-R1 / Kimi-thinking / GLM-thinking
	// variants emit it by default; OpenAI/Anthropic reasoning models
	// require an explicit `reasoning` request parameter we don't send
	// today, so they stay silent). Both fields are read so a single
	// codec serves direct llama.cpp/vLLM endpoints AND OpenRouter.
	Reasoning *string           `json:"reasoning,omitzero"`
	ToolCalls []openaiToolDelta `json:"tool_calls,omitzero"`
}

type openaiToolDelta struct {
	Index    int             `json:"index"`
	ID       string          `json:"id,omitzero"`
	Type     string          `json:"type,omitzero"`
	Function *openaiFunction `json:"function,omitzero"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// EncodeRequest converts CompleteParams to the OpenAI wire format JSON.
func EncodeRequest(params api.CompleteParams) ([]byte, error) {
	req := openaiRequest{
		Model:           params.Model,
		Messages:        encodeMessages(params.Messages, params.System),
		MaxTokens:       params.MaxTokens,
		Temperature:     params.Temperature,
		TopP:            params.TopP,
		PresencePenalty: params.PresencePenalty,
		Stop:            params.Stop,
		Stream:          true,
		StreamOptions: &streamOptions{
			IncludeUsage: true,
		},
	}

	for _, t := range params.Tools {
		req.Tools = append(req.Tools, openaiTool{
			Type: "function",
			Function: openaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return json.Marshal(req)
}

// encodeMessages converts api.Messages to OpenAI wire format.
// System blocks are flattened with "\n\n" and prepended as a single system
// message. OpenAI-compatible endpoints have no equivalent of Anthropic's
// cache_control, so SystemBlock.Cache is silently ignored.
func encodeMessages(msgs []api.Message, system []api.SystemBlock) []openaiMessage {
	var result []openaiMessage

	// Flatten system blocks to a single string; drop empties.
	var sysText string
	if len(system) > 0 {
		parts := make([]string, 0, len(system))
		for _, b := range system {
			if b.Text == "" {
				continue
			}
			parts = append(parts, b.Text)
		}
		sysText = strings.Join(parts, "\n\n")
	}
	if sysText != "" {
		result = append(result, openaiMessage{
			Role:    "system",
			Content: &sysText,
		})
	}

	for _, msg := range msgs {
		switch msg.Role {
		case api.RoleSystem:
			// Already handled above via the system parameter
			continue

		case api.RoleUser:
			text := msg.Text()
			result = append(result, openaiMessage{
				Role:    "user",
				Content: &text,
			})

		case api.RoleAssistant:
			om := openaiMessage{Role: "assistant"}
			// Content must be null (not absent) when there are only tool calls.
			// OpenAI-compatible APIs require this distinction.
			if text := msg.Text(); text != "" {
				om.Content = &text
			}
			// Add tool calls if present
			for _, tc := range msg.ToolCalls() {
				om.ToolCalls = append(om.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiFunction{
						Name:      tc.Name,
						Arguments: string(tc.Input),
					},
				})
			}
			result = append(result, om)

		case api.RoleTool:
			// Each tool result is a separate message with role "tool"
			for _, block := range msg.Content {
				if block.Type == api.BlockToolResult && block.ToolResult != nil {
					content := block.ToolResult.Content
					result = append(result, openaiMessage{
						Role:       "tool",
						Content:    &content,
						ToolCallID: block.ToolResult.ToolCallID,
					})
				}
			}
		}
	}

	// Defensive trim: drop a trailing assistant message that carries
	// neither content nor tool_calls. The agent loop's empty-turn
	// guard is the primary defense, but llama.cpp + Qwen3-thinking
	// rejects any request whose messages array ends with `assistant`
	// (treats it as a "prefill", incompatible with enable_thinking).
	// Dropping here costs nothing on requests that are already
	// well-formed and shields against any future caller path that
	// leaks an empty assistant through.
	for len(result) > 0 {
		last := result[len(result)-1]
		if last.Role != "assistant" {
			break
		}
		if last.Content != nil || len(last.ToolCalls) > 0 {
			break
		}
		result = result[:len(result)-1]
	}

	return result
}

// parseChunk parses a single SSE data payload into stream events.
// Returns events to yield and whether the stream is done.
func parseChunk(data string) ([]api.StreamEvent, bool) {
	if data == "[DONE]" {
		return []api.StreamEvent{{Type: api.EventDone}}, true
	}

	var chunk openaiChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return []api.StreamEvent{{
			Type:  api.EventError,
			Error: fmt.Errorf("parsing OpenAI chunk: %w", err),
		}}, false
	}

	var events []api.StreamEvent

	// Process choices
	for _, choice := range chunk.Choices {
		// Reasoning content (chain-of-thought) — separate from text
		// content. Emitted by llama.cpp with `--reasoning-format
		// deepseek` and vLLM with `--enable-reasoning` under the
		// `reasoning_content` key, and by OpenRouter under `reasoning`.
		// The agent loop defensively creates a thinking accumulator on
		// first delta when no Start event preceded (run.go:446), so we
		// don't need to synthesise EventThinkingStart from the
		// stateless chunk parser. ThinkingIndex stays at 0: OpenAI-
		// shaped servers don't emit multiple thinking blocks per turn.
		if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
			events = append(events, api.StreamEvent{
				Type:          api.EventThinkingDelta,
				Delta:         *choice.Delta.ReasoningContent,
				ThinkingIndex: 0,
			})
		}
		if choice.Delta.Reasoning != nil && *choice.Delta.Reasoning != "" {
			events = append(events, api.StreamEvent{
				Type:          api.EventThinkingDelta,
				Delta:         *choice.Delta.Reasoning,
				ThinkingIndex: 0,
			})
		}

		// Text content
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, api.StreamEvent{
				Type:  api.EventDelta,
				Delta: *choice.Delta.Content,
			})
		}

		// Tool calls. Start and Delta are independent: a chunk with `id`
		// set marks a new call, and a chunk with non-empty `arguments`
		// carries argument bytes. They can co-occur — llama.cpp bundles
		// the opening "{" of arguments with the start chunk. Treating
		// them as mutually exclusive drops the leading byte and produces
		// invalid JSON downstream.
		for _, tc := range choice.Delta.ToolCalls {
			if tc.Function == nil {
				continue
			}
			if tc.ID != "" {
				events = append(events, api.StreamEvent{
					Type:          api.EventToolCallStart,
					ToolCallID:    tc.ID,
					ToolCallName:  tc.Function.Name,
					ToolCallIndex: tc.Index,
				})
			}
			if tc.Function.Arguments != "" {
				events = append(events, api.StreamEvent{
					Type:          api.EventToolCallDelta,
					ToolCallArgs:  tc.Function.Arguments,
					ToolCallIndex: tc.Index,
				})
			}
		}

		// Finish reason
		if choice.FinishReason != nil {
			events = append(events, api.StreamEvent{
				Type:       api.EventDone,
				StopReason: *choice.FinishReason,
			})
		}
	}

	// Usage (final chunk with empty choices)
	if chunk.Usage != nil {
		events = append(events, api.StreamEvent{
			Type: api.EventUsage,
			Usage: &api.Usage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			},
		})
	}

	return events, false
}
