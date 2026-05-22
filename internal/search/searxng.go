package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type searxngSearcher struct {
	client  *http.Client
	baseURL string
}

func newSearxng(client *http.Client, baseURL string) Searcher {
	return &searxngSearcher{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

func (s *searxngSearcher) ProviderName() string { return "searxng" }

type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

func (s *searxngSearcher) Search(ctx context.Context, req SearchRequest) (SearchResults, error) {
	if s.baseURL == "" {
		return SearchResults{}, fmt.Errorf("searxng: base_url is required")
	}

	q := url.Values{}
	q.Set("q", req.Query)
	q.Set("format", "json")
	// SearXNG paginates server-side; the JSON response always returns
	// the configured page size (often 10). We slice client-side after
	// decode rather than negotiate page size per-instance.
	target := s.baseURL + "/search?" + q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return SearchResults{}, fmt.Errorf("searxng: build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return SearchResults{}, fmt.Errorf("searxng: HTTP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return SearchResults{}, fmt.Errorf("searxng: HTTP %d: %s (is %s a SearXNG instance with the json format enabled? see settings.yml `search.formats`)",
			resp.StatusCode, strings.TrimSpace(string(errBody)), strconv.Quote(s.baseURL))
	}

	var out searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SearchResults{}, fmt.Errorf("searxng: decode response: %w", err)
	}

	limit := req.MaxResults
	if limit <= 0 || limit > len(out.Results) {
		limit = len(out.Results)
	}
	results := make([]SearchResult, 0, limit)
	for i := 0; i < limit; i++ {
		r := out.Results[i]
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return SearchResults{Results: results}, nil
}
