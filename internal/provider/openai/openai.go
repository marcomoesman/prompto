package openai

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"net/http"
	"sort"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/httpattr"
	"github.com/marcomoesman/prompto/internal/provider/internal/providerhttp"
	"github.com/marcomoesman/prompto/internal/sse"
)

const defaultBaseURL = "https://api.openai.com"

// Provider implements api.Provider for the OpenAI Chat Completions API.
type Provider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// New creates an OpenAI-compatible provider from the given config.
func New(cfg api.ProviderConfig) *Provider {
	return &Provider{
		client:  &http.Client{Transport: providerhttp.DefaultTransport()},
		baseURL: strings.TrimRight(cmp.Or(cfg.BaseURL, defaultBaseURL), "/"),
		apiKey:  cfg.APIKey,
	}
}

// contextLimits maps known OpenAI model families to their input context
// windows (in tokens). Custom / local-model users reach the unknown path
// and fall back to the configured default.
var contextLimits = map[string]int{
	"gpt-5":         400_000,
	"gpt-5-mini":    400_000,
	"gpt-4o":        128_000,
	"gpt-4o-mini":   128_000,
	"gpt-4-turbo":   128_000,
	"gpt-4":         8_192,
	"gpt-3.5-turbo": 16_385,
	"o1":            200_000,
	"o1-preview":    128_000,
	"o1-mini":       128_000,
	"o3-mini":       200_000,
}

// contextLimitPrefixes holds contextLimits' keys sorted by length (descending)
// so prefix matching on a versioned model name picks the most specific
// family. Without this, map iteration order is random and `gpt-4o-2024-11-20`
// can incorrectly match `gpt-4` (8192) before `gpt-4o` (128000).
var contextLimitPrefixes = sortedKeysByLengthDesc(contextLimits)

func sortedKeysByLengthDesc(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})
	return keys
}

// chatCompletionsURL builds the full chat-completions endpoint URL,
// tolerating both base-URL conventions: bare host (`https://api.openai.com`)
// and versioned base (`https://openrouter.ai/api/v1`, `http://localhost:1234/v1`).
// The `/v1`-suffixed form is what most OpenAI-compat clients (Python openai,
// go-openai, the OpenRouter / LM Studio docs) expect users to copy verbatim;
// without this normalization a user-supplied `…/v1` produced `…/v1/v1/chat/completions`,
// which OpenRouter answers with an HTML 404 at status 200 — the SSE parser
// finds no events and the turn ends silently.
func chatCompletionsURL(baseURL string) string {
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/v1/chat/completions"
}

// ContextLimit implements api.Provider. Tries exact match first, then
// prefix match so versioned names (gpt-4o-2024-11-20) resolve correctly.
// Prefix iteration walks longest→shortest so `gpt-4o-2024-11-20` matches
// `gpt-4o` rather than the also-prefix `gpt-4`.
func (p *Provider) ContextLimit(model string) int {
	if n, ok := contextLimits[model]; ok {
		return n
	}
	for _, prefix := range contextLimitPrefixes {
		if strings.HasPrefix(model, prefix) {
			return contextLimits[prefix]
		}
	}
	return 0
}

// wrapContextLimit detects OpenAI / OpenAI-compatible context-limit
// responses and wraps them with api.ErrContextLimit so isContextLimit
// can match via errors.Is. Non-context-limit errors are returned with
// their original text and no sentinel.
func wrapContextLimit(status int, body string) error {
	base := fmt.Errorf("openai API error (status %d): %s", status, body)
	if status == http.StatusBadRequest &&
		(strings.Contains(body, "context_length_exceeded") ||
			strings.Contains(body, "context window") ||
			strings.Contains(body, "maximum context length")) {
		return fmt.Errorf("%w: %s", api.ErrContextLimit, base.Error())
	}
	return base
}

// Complete sends a conversation to the OpenAI API and streams the response.
func (p *Provider) Complete(ctx context.Context, params api.CompleteParams) iter.Seq[api.StreamEvent] {
	return func(yield func(api.StreamEvent) bool) {
		body, err := EncodeRequest(params)
		if err != nil {
			yield(api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("encoding request: %w", err)})
			return
		}

		req, err := providerhttp.NewIdempotentPOST(ctx, chatCompletionsURL(p.baseURL), body)
		if err != nil {
			yield(api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("creating request: %w", err)})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
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
			// Wrap context-limit errors with the canonical sentinel so
			// the agent loop's isContextLimit / reactive-summarize path
			// triggers via errors.Is rather than substring scan. OpenAI
			// returns these as `context_length_exceeded`; OpenAI-compat
			// relays usually surface the same code or "context window".
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
			yield(api.StreamEvent{Type: api.EventError, Error: fmt.Errorf("openai stream error: %w", err)})
		}
	}
}
