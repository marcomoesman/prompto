package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/httpattr"
)

// TestProvider_SendsAttributionHeaders confirms the prompto attribution
// headers are attached alongside the Anthropic-required auth/version
// headers. Anthropic's /v1/messages ignores the extra headers; the
// invariant matters because the same provider is reused for any
// OpenAI-compatible endpoint reached through anthropic-shape config in
// the future, and consistency across providers is a tested guarantee.
func TestProvider_SendsAttributionHeaders(t *testing.T) {
	var capturedReferer, capturedTitle, capturedUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReferer = r.Header.Get("HTTP-Referer")
		capturedTitle = r.Header.Get("X-Title")
		capturedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{Kind: "anthropic", BaseURL: server.URL, APIKey: "k"})
	for range p.Complete(context.Background(), api.CompleteParams{
		Model:     "claude-sonnet-4",
		MaxTokens: 100,
		Messages:  []api.Message{api.NewUserMessage("hi")},
	}) {
		// drain
	}

	if capturedReferer != httpattr.RefererURL {
		t.Errorf("HTTP-Referer = %q, want %q", capturedReferer, httpattr.RefererURL)
	}
	if !strings.HasPrefix(capturedTitle, "prompto/v") {
		t.Errorf("X-Title = %q, want prompto/v… prefix", capturedTitle)
	}
	if !strings.HasPrefix(capturedUA, "prompto/") {
		t.Errorf("User-Agent = %q, want prompto/… prefix", capturedUA)
	}
}

func TestProviderCompleteStreaming(t *testing.T) {
	sseResponse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing or wrong x-api-key: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("missing anthropic-version header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseResponse))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{
		Kind:    "anthropic",
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	params := api.CompleteParams{
		Model:     "claude-sonnet-4-20250514",
		System:    []api.SystemBlock{{Text: "test"}},
		MaxTokens: 100,
		Messages: []api.Message{
			api.NewUserMessage("hello"),
		},
	}

	events := slices.Collect(p.Complete(t.Context(), params))

	var text string
	var gotDone bool
	for _, e := range events {
		switch e.Type {
		case api.EventDelta:
			text += e.Delta
		case api.EventDone:
			gotDone = true
		case api.EventError:
			t.Fatalf("unexpected error: %v", e.Error)
		}
	}

	if text != "Hi there" {
		t.Errorf("text = %q, want %q", text, "Hi there")
	}
	if !gotDone {
		t.Error("missing EventDone")
	}
}

func TestProviderCompleteHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key"}}`))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{
		Kind:    "anthropic",
		BaseURL: server.URL,
		APIKey:  "bad-key",
	})

	params := api.CompleteParams{
		Model:     "test",
		MaxTokens: 100,
		Messages:  []api.Message{api.NewUserMessage("hello")},
	}

	events := slices.Collect(p.Complete(t.Context(), params))
	if len(events) != 1 || events[0].Type != api.EventError {
		t.Fatalf("expected single error event, got %d events", len(events))
	}
}

func TestProviderCompleteHTTPErrorBodyIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", 80*1024)))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{Kind: "anthropic", BaseURL: server.URL, APIKey: "bad-key"})
	events := slices.Collect(p.Complete(t.Context(), api.CompleteParams{
		Model:     "test",
		MaxTokens: 100,
		Messages:  []api.Message{api.NewUserMessage("hello")},
	}))
	if len(events) != 1 || events[0].Type != api.EventError {
		t.Fatalf("expected single error event, got %d events", len(events))
	}
	msg := events[0].Error.Error()
	if !strings.Contains(msg, "truncated at 64KB") {
		t.Fatalf("error missing truncation marker")
	}
	if len(msg) > 70*1024 {
		t.Fatalf("error length = %d, want bounded", len(msg))
	}
}

func TestProviderCompleteSSEScannerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: " + strings.Repeat("x", 17*1024*1024)))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{Kind: "anthropic", BaseURL: server.URL, APIKey: "test"})
	events := slices.Collect(p.Complete(t.Context(), api.CompleteParams{
		Model:     "test",
		MaxTokens: 100,
		Messages:  []api.Message{api.NewUserMessage("hello")},
	}))
	if len(events) != 1 || events[0].Type != api.EventError {
		t.Fatalf("expected single stream error, got %#v", events)
	}
	if !strings.Contains(events[0].Error.Error(), "stream error") {
		t.Fatalf("error = %v, want stream error", events[0].Error)
	}
}

func TestProviderCompleteContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block forever — the client should cancel
		<-r.Context().Done()
	}))
	defer server.Close()

	p := New(api.ProviderConfig{
		Kind:    "anthropic",
		BaseURL: server.URL,
		APIKey:  "test",
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	params := api.CompleteParams{
		Model:     "test",
		MaxTokens: 100,
		Messages:  []api.Message{api.NewUserMessage("hello")},
	}

	events := slices.Collect(p.Complete(ctx, params))
	if len(events) != 1 || events[0].Type != api.EventError {
		t.Fatalf("expected error from cancelled context, got %d events", len(events))
	}
}
