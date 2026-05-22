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

const tavilyDefaultBaseURL = "https://api.tavily.com"

type tavilySearcher struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func newTavily(client *http.Client, apiKey, baseURL string) Searcher {
	if baseURL == "" {
		baseURL = tavilyDefaultBaseURL
	}
	return &tavilySearcher{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
	}
}

func (t *tavilySearcher) ProviderName() string { return "tavily" }

type tavilyRequest struct {
	APIKey     string `json:"api_key"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type tavilyResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Content       string `json:"content"`
		PublishedDate string `json:"published_date"`
	} `json:"results"`
}

func (t *tavilySearcher) Search(ctx context.Context, req SearchRequest) (SearchResults, error) {
	body, err := json.Marshal(tavilyRequest{
		APIKey:     t.apiKey,
		Query:      req.Query,
		MaxResults: req.MaxResults,
	})
	if err != nil {
		return SearchResults{}, fmt.Errorf("tavily: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return SearchResults{}, fmt.Errorf("tavily: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return SearchResults{}, fmt.Errorf("tavily: HTTP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return SearchResults{}, fmt.Errorf("tavily: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var out tavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SearchResults{}, fmt.Errorf("tavily: decode response: %w", err)
	}

	results := make([]SearchResult, 0, len(out.Results))
	for _, r := range out.Results {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Content,
			PublishedAt: r.PublishedDate,
		})
	}
	return SearchResults{Results: results}, nil
}
