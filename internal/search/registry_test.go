package search

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/config"
)

func TestNew_Dispatches(t *testing.T) {
	cases := []struct {
		provider string
		key      string
		baseURL  string
		wantName string
	}{
		{"tavily", "k", "", "tavily"},
		{"exa", "k", "", "exa"},
		{"firecrawl", "k", "", "firecrawl"},
		{"searxng", "", "http://localhost:8080", "searxng"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			s, err := New(config.SearchConfig{
				Provider: tc.provider,
				APIKey:   tc.key,
				BaseURL:  tc.baseURL,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := s.ProviderName(); got != tc.wantName {
				t.Errorf("ProviderName = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestNew_UnknownProvider(t *testing.T) {
	_, err := New(config.SearchConfig{Provider: "google"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown search provider") {
		t.Errorf("error %q should mention unknown search provider", err.Error())
	}
}
