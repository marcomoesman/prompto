package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const firecrawlDefaultBaseURL = "https://api.firecrawl.dev"

type firecrawlSearcher struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func newFirecrawl(client *http.Client, apiKey, baseURL string) Searcher {
	if baseURL == "" {
		baseURL = firecrawlDefaultBaseURL
	}
	return &firecrawlSearcher{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
	}
}

func (f *firecrawlSearcher) ProviderName() string { return "firecrawl" }

type firecrawlRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type firecrawlResponse struct {
	Data []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	} `json:"data"`
}

func (f *firecrawlSearcher) Search(ctx context.Context, req SearchRequest) (SearchResults, error) {
	body, err := json.Marshal(firecrawlRequest{
		Query: req.Query,
		Limit: req.MaxResults,
	})
	if err != nil {
		return SearchResults{}, fmt.Errorf("firecrawl: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+"/v1/search", bytes.NewReader(body))
	if err != nil {
		return SearchResults{}, fmt.Errorf("firecrawl: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+f.apiKey)

	resp, err := f.client.Do(httpReq)
	if err != nil {
		return SearchResults{}, fmt.Errorf("firecrawl: HTTP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return SearchResults{}, fmt.Errorf("firecrawl: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var out firecrawlResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SearchResults{}, fmt.Errorf("firecrawl: decode response: %w", err)
	}

	results := make([]SearchResult, 0, len(out.Data))
	for _, r := range out.Data {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}
	return SearchResults{Results: results}, nil
}
