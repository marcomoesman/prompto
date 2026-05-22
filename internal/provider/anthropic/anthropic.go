package anthropic

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/httpattr"
	"github.com/marcomoesman/prompto/internal/provider/internal/providerhttp"
	"github.com/marcomoesman/prompto/internal/sse"
)

const defaultBaseURL = "https://api.anthropic.com"

// Provider implements api.Provider for the Anthropic Messages API.
type Provider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// New creates an Anthropic provider from the given config.
func New(cfg api.ProviderConfig) *Provider {
	return &Provider{
		client:  &http.Client{Transport: providerhttp.DefaultTransport()},
		baseURL: strings.TrimRight(cmp.Or(cfg.BaseURL, defaultBaseURL), "/"),
		apiKey:  cfg.APIKey,
	}
}

// contextLimits is the hardcoded per-model lookup. Keys are the canonical
// names Anthropic ships; variants with date stamps (claude-sonnet-4-20250514)
// match via prefix. The [1m] suffix maps to the 1M-context beta variant.
var contextLimits = map[string]int{
	"claude-sonnet-4-6": 200_000,
	"claude-sonnet-4-5": 200_000,
	"claude-sonnet-4":   200_000,
	"claude-opus-4-6":   200_000,
	"claude-opus-4-5":   200_000,
	"claude-opus-4":     200_000,
	"claude-haiku-4-5":  200_000,
	"claude-haiku-4":    200_000,
}

// ContextLimit implements api.Provider.
func (p *Provider) ContextLimit(model string) int {
	// [1m] suffix indicates the 1M-context beta regardless of base model.
	if strings.HasSuffix(model, "[1m]") {
		return 1_000_000
	}
	if n, ok := contextLimits[model]; ok {
		return n
	}
	// Prefix match for date-stamped variants ("claude-sonnet-4-20250514").
	for prefix, n := range contextLimits {
		if strings.HasPrefix(model, prefix) {
			return n
		}
	}
	return 0
}

// wrapContextLimit detects Anthropic's "prompt is too long" responses
// and wraps the resulting error with api.ErrContextLimit so the agent
// loop can match via errors.Is. Non-context-limit errors are returned
// with their original text and no sentinel.
func wrapContextLimit(status int, body string) error {
	base := fmt.Errorf("anthropic API error (status %d): %s", status, body)
	if status == http.StatusBadRequest && strings.Contains(body, "prompt is too long") {
		return fmt.Errorf("%w: %s", api.ErrContextLimit, base.Error())
	}
	return base
}

// Complete sends a conversation to the Anthropic API and streams the response.
func (p *Provider) Complete(ctx context.Context, params api.CompleteParams) iter.Seq[api.StreamEvent] {
	return func(yield func(api.StreamEvent) bool) {
		body, err := EncodeRequest(params)
		if err != nil {
			yield(api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("encoding request: %w", err)})
			return
		}

		req, err := providerhttp.NewIdempotentPOST(ctx, p.baseURL+"/v1/messages", body)
		if err != nil {
			yield(api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("creating request: %w", err)})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", p.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		httpattr.Apply(req)

		resp, err := p.client.Do(req)
		if err != nil {
			yield(api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("executing request: %w", err)})
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			body, truncated := providerhttp.ReadErrorBody(resp.Body)
			if truncated {
				body += "\n[Response body truncated at 64KB]"
			}
			// Wrap the context-limit error with the canonical sentinel
			// so the agent loop's isContextLimit / reactive-summarize
			// path triggers via errors.Is rather than substring scan.
			// Anthropic returns this as `invalid_request_error` with the
			// text "prompt is too long: …".
			yield(api.StreamEvent{
				Type:  api.EventError,
				Error: wrapContextLimit(resp.StatusCode, body),
			})
			return
		}

		sseEvents, sseErr := sse.ParseWithError(resp.Body)
		for event := range ParseStream(sseEvents) {
			if !yield(event) {
				return
			}
		}
		if err := sseErr(); err != nil {
			yield(api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("anthropic stream error: %w", err)})
		}
	}
}
