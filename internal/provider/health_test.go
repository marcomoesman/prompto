package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestCheckLocalOpenAIReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen"}]}`))
	}))
	defer srv.Close()

	err := CheckLocalOpenAI(t.Context(), api.ProviderConfig{
		Kind:    "openai",
		BaseURL: srv.URL,
		APIKey:  "k",
		Model:   "qwen",
	})
	if err != nil {
		t.Fatalf("CheckLocalOpenAI: %v", err)
	}
}

func TestCheckLocalOpenAIModelMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"other"}]}`))
	}))
	defer srv.Close()

	err := CheckLocalOpenAI(t.Context(), api.ProviderConfig{
		Kind:    "openai",
		BaseURL: srv.URL,
		Model:   "qwen",
	})
	if err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Fatalf("err = %v, want model-not-listed error", err)
	}
}

func TestCheckLocalOpenAIUnreachable(t *testing.T) {
	err := CheckLocalOpenAI(t.Context(), api.ProviderConfig{
		Kind:    "openai",
		BaseURL: "http://127.0.0.1:1",
		Model:   "qwen",
	})
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("err = %v, want unreachable error", err)
	}
}

func TestCheckLocalOpenAISkipsUnsupported(t *testing.T) {
	err := CheckLocalOpenAI(t.Context(), api.ProviderConfig{
		Kind:    "anthropic",
		BaseURL: "http://127.0.0.1:1",
		Model:   "qwen",
	})
	if err != nil {
		t.Fatalf("CheckLocalOpenAI unsupported kind = %v, want nil", err)
	}
}
