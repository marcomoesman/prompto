package api

import (
	"context"
	"errors"
	"iter"
)

// ErrContextLimit is the sentinel a Provider implementation should wrap
// (via fmt.Errorf("…: %w", api.ErrContextLimit)) when the API rejects a
// request because the prompt exceeded the model's input-context window.
// The agent loop checks errors.Is(streamErr, api.ErrContextLimit) before
// any substring fallback, so providers that opt in get exact, allocation-
// free detection that survives error-message rewording on the server side.
//
// Providers that don't wrap with this sentinel still work — the agent
// loop's isContextLimit falls back to substring matching against well-
// known phrasings ("prompt is too long", "context_length_exceeded",
// "context window") for backwards compatibility with custom / OpenAI-
// compatible servers.
var ErrContextLimit = errors.New("provider: context limit exceeded")

// Provider is the interface every LLM backend must implement.
type Provider interface {
	// Complete sends a conversation to the LLM and returns a stream of events.
	// The iterator is single-use. Cancel ctx to abort.
	Complete(ctx context.Context, params CompleteParams) iter.Seq[StreamEvent]

	// ContextLimit returns the model's input-context ceiling in tokens.
	// A return of 0 means "unknown" — callers fall back to a configured
	// default. Providers back this with a per-model lookup table; neither
	// Anthropic nor OpenAI expose window sizes in streaming responses.
	ContextLimit(model string) int
}

// CompleteParams bundles all inputs to a completion request.
type CompleteParams struct {
	Model           string
	System          []SystemBlock    // sectioned system prompt; last Cache:true marks cacheable boundary
	Messages        []Message        // conversation history
	Tools           []ToolDefinition // tool schemas (empty when no tools)
	MaxTokens       int
	Temperature     *float64
	TopP            *float64
	PresencePenalty *float64
	Stop            []string
}

// ProviderConfig holds the connection details for a provider instance.
type ProviderConfig struct {
	Kind    string // "anthropic" or "openai"
	BaseURL string // API base URL (custom for Ollama, LM Studio, etc.)
	APIKey  string // resolved API key
	Model   string // default model
}
