// Package search provides pluggable web-search backends for the
// websearch tool. Concrete implementations live alongside this file
// (tavily.go, exa.go, firecrawl.go, searxng.go) and are dispatched
// by New in registry.go.
package search

import "context"

// Searcher is the seam between the websearch tool and a concrete
// search-API client. Implementations must be safe to call from
// multiple goroutines concurrently — the dispatcher batches
// concurrency-safe tool calls.
type Searcher interface {
	// Search runs one query and returns ranked results. Callers must
	// pass MaxResults > 0; the tool layer clamps the model's input
	// before calling.
	Search(ctx context.Context, req SearchRequest) (SearchResults, error)
	// ProviderName returns a stable identifier ("tavily", "searxng",
	// etc.) used for logging and the tool's display summary.
	ProviderName() string
}

// SearchRequest is the input to Searcher.Search.
type SearchRequest struct {
	Query      string
	MaxResults int
}

// SearchResult is one ranked hit. PublishedAt may be empty when the
// provider doesn't surface it (SearXNG, Firecrawl) or when the
// underlying page has no publication date.
type SearchResult struct {
	Title       string
	URL         string
	Snippet     string
	PublishedAt string
}

// SearchResults wraps the ranked list. A struct (rather than a bare
// slice) leaves room for provider-level metadata later (latency,
// total hits, suggested queries) without breaking the seam.
type SearchResults struct {
	Results []SearchResult
}
