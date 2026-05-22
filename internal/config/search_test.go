package config

import (
	"strings"
	"testing"
)

func TestValidateSearch_Nil(t *testing.T) {
	if err := validateSearch(nil); err != nil {
		t.Errorf("nil search config should be valid; got %v", err)
	}
}

func TestValidateSearch_RequiresProvider(t *testing.T) {
	err := validateSearch(&SearchConfig{})
	if err == nil || !strings.Contains(err.Error(), "search.provider is required") {
		t.Errorf("err = %v, want 'search.provider is required'", err)
	}
}

func TestValidateSearch_SearxngRequiresBaseURL(t *testing.T) {
	err := validateSearch(&SearchConfig{Provider: "searxng"})
	if err == nil || !strings.Contains(err.Error(), "base_url is required") {
		t.Errorf("err = %v, want 'base_url is required'", err)
	}
}

func TestValidateSearch_SearxngBaseURLOK(t *testing.T) {
	if err := validateSearch(&SearchConfig{Provider: "searxng", BaseURL: "http://localhost:8080"}); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestValidateSearch_PaidRequiresAPIKey(t *testing.T) {
	for _, prov := range []string{"tavily", "exa", "firecrawl"} {
		t.Run(prov, func(t *testing.T) {
			err := validateSearch(&SearchConfig{Provider: prov})
			if err == nil || !strings.Contains(err.Error(), "api_key is required") {
				t.Errorf("%s: err = %v, want 'api_key is required'", prov, err)
			}
		})
	}
}

func TestValidateSearch_PaidWithKeyOK(t *testing.T) {
	for _, prov := range []string{"tavily", "exa", "firecrawl"} {
		t.Run(prov, func(t *testing.T) {
			if err := validateSearch(&SearchConfig{Provider: prov, APIKey: "k"}); err != nil {
				t.Errorf("%s: unexpected: %v", prov, err)
			}
		})
	}
}

func TestValidateSearch_UnknownProvider(t *testing.T) {
	err := validateSearch(&SearchConfig{Provider: "google", APIKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "unknown search provider") {
		t.Errorf("err = %v, want 'unknown search provider'", err)
	}
}

func TestExpandEnvVars_SearchAPIKey(t *testing.T) {
	t.Setenv("PROMPTO_TEST_SEARCH_KEY", "sk-from-env")
	cfg := &Config{
		Providers: map[string]ProviderEntry{},
		Search: &SearchConfig{
			Provider: "tavily",
			APIKey:   "$PROMPTO_TEST_SEARCH_KEY",
		},
	}
	expandEnvVars(cfg)
	if cfg.Search.APIKey != "sk-from-env" {
		t.Errorf("APIKey not expanded: got %q", cfg.Search.APIKey)
	}
}

func TestMerge_SearchReplacesWholeBlock(t *testing.T) {
	base := &Config{
		Providers: map[string]ProviderEntry{},
		Search: &SearchConfig{
			Provider: "tavily",
			APIKey:   "global-key",
		},
	}
	overlay := &Config{
		Search: &SearchConfig{
			Provider: "searxng",
			BaseURL:  "http://localhost:8080",
		},
	}
	merge(base, overlay, layerGlobal)
	if base.Search.Provider != "searxng" {
		t.Errorf("Provider = %q, want searxng", base.Search.Provider)
	}
	// APIKey should NOT have leaked from the global block — whole-block
	// replacement keeps stale credentials from a different provider out
	// of the merged config.
	if base.Search.APIKey != "" {
		t.Errorf("APIKey = %q, want empty (whole-block replacement)", base.Search.APIKey)
	}
	if base.Search.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q", base.Search.BaseURL)
	}
}
