package search

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/marcomoesman/prompto/internal/config"
)

// defaultTimeout caps any single search request. Search APIs are
// expected to respond in 1-3 seconds; 15 seconds is a generous ceiling
// before a stuck call blocks the agent loop.
const defaultTimeout = 15 * time.Second

// New constructs a Searcher from the merged search config. Validation
// of cfg fields is the caller's responsibility — config.Load already
// enforces Provider non-empty, BaseURL non-empty for searxng, and
// APIKey non-empty for paid providers. Returns an error only for
// unknown providers (defense in depth against a future config schema
// drift).
func New(cfg config.SearchConfig) (Searcher, error) {
	client := &http.Client{
		Timeout:   defaultTimeout,
		Transport: defaultTransport(),
	}
	switch cfg.Provider {
	case "tavily":
		return newTavily(client, cfg.APIKey, cfg.BaseURL), nil
	case "exa":
		return newExa(client, cfg.APIKey, cfg.BaseURL), nil
	case "firecrawl":
		return newFirecrawl(client, cfg.APIKey, cfg.BaseURL), nil
	case "searxng":
		return newSearxng(client, cfg.BaseURL), nil
	default:
		return nil, fmt.Errorf("unknown search provider %q", cfg.Provider)
	}
}

// defaultTransport mirrors the Anthropic provider's transport tuning
// (provider/anthropic/anthropic.go:38) — keeps connection pooling
// behaviour consistent across all of prompto's outbound HTTP.
func defaultTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   2,
	}
}
