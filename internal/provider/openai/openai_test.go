package openai

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

func TestChatCompletionsURL(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"https://api.openai.com", "https://api.openai.com/v1/chat/completions"},
		{"https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1/chat/completions"},
		{"http://localhost:1234", "http://localhost:1234/v1/chat/completions"},
		{"http://localhost:1234/v1", "http://localhost:1234/v1/chat/completions"},
	}
	for _, c := range cases {
		if got := chatCompletionsURL(c.base); got != c.want {
			t.Errorf("chatCompletionsURL(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

func TestProvider_RoutesOpenRouterStyleBaseURL(t *testing.T) {
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{Kind: "openai", BaseURL: server.URL + "/api/v1", APIKey: "k"})
	for range p.Complete(context.Background(), api.CompleteParams{
		Model:    "m",
		Messages: []api.Message{api.NewUserMessage("hi")},
	}) {
	}

	if capturedPath != "/api/v1/chat/completions" {
		t.Errorf("path = %q, want /api/v1/chat/completions (no doubled /v1)", capturedPath)
	}
}

// TestProvider_SendsAttributionHeaders confirms HTTP-Referer / X-Title /
// User-Agent are attached to every outbound chat-completions call. These
// headers identify prompto to OpenRouter (for app attribution and
// rate-limit benefits) and replace Go's default "Go-http-client/1.1"
// User-Agent with something diagnostic.
func TestProvider_SendsAttributionHeaders(t *testing.T) {
	var capturedReferer, capturedTitle, capturedUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReferer = r.Header.Get("HTTP-Referer")
		capturedTitle = r.Header.Get("X-Title")
		capturedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{Kind: "openai", BaseURL: server.URL, APIKey: "k"})
	for range p.Complete(context.Background(), api.CompleteParams{
		Model:     "gpt-4o",
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
	sseResponse := `data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}

data: {"id":"c1","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}

data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"c1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3}}

data: [DONE]

`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("Authorization = %q", auth)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseResponse))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{
		Kind:    "openai",
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	params := api.CompleteParams{
		Model:     "gpt-4o",
		System:    []api.SystemBlock{{Text: "test"}},
		MaxTokens: 100,
		Messages: []api.Message{
			api.NewUserMessage("hello"),
		},
	}

	events := slices.Collect(p.Complete(t.Context(), params))

	var text string
	var gotDone bool
	var gotUsage bool
	for _, e := range events {
		switch e.Type {
		case api.EventDelta:
			text += e.Delta
		case api.EventDone:
			gotDone = true
		case api.EventUsage:
			gotUsage = true
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
	if !gotUsage {
		t.Error("missing EventUsage")
	}
}

func TestProviderCompleteHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
	}))
	defer server.Close()

	p := New(api.ProviderConfig{
		Kind:    "openai",
		BaseURL: server.URL,
		APIKey:  "test",
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

	p := New(api.ProviderConfig{Kind: "openai", BaseURL: server.URL, APIKey: "test"})
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

	p := New(api.ProviderConfig{Kind: "openai", BaseURL: server.URL, APIKey: "test"})
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
		<-r.Context().Done()
	}))
	defer server.Close()

	p := New(api.ProviderConfig{
		Kind:    "openai",
		BaseURL: server.URL,
		APIKey:  "test",
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

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
