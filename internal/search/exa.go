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

const exaDefaultBaseURL = "https://api.exa.ai"

type exaSearcher struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func newExa(client *http.Client, apiKey, baseURL string) Searcher {
	if baseURL == "" {
		baseURL = exaDefaultBaseURL
	}
	return &exaSearcher{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
	}
}

func (e *exaSearcher) ProviderName() string { return "exa" }

type exaRequest struct {
	Query      string      `json:"query"`
	NumResults int         `json:"numResults"`
	Type       string      `json:"type"`
	Contents   exaContents `json:"contents"`
}

type exaContents struct {
	Text bool `json:"text"`
}

type exaResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Text          string `json:"text"`
		PublishedDate string `json:"publishedDate"`
	} `json:"results"`
}

func (e *exaSearcher) Search(ctx context.Context, req SearchRequest) (SearchResults, error) {
	body, err := json.Marshal(exaRequest{
		Query:      req.Query,
		NumResults: req.MaxResults,
		Type:       "auto",
		Contents:   exaContents{Text: true},
	})
	if err != nil {
		return SearchResults{}, fmt.Errorf("exa: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return SearchResults{}, fmt.Errorf("exa: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("x-api-key", e.apiKey)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return SearchResults{}, fmt.Errorf("exa: HTTP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return SearchResults{}, fmt.Errorf("exa: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var out exaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return SearchResults{}, fmt.Errorf("exa: decode response: %w", err)
	}

	results := make([]SearchResult, 0, len(out.Results))
	for _, r := range out.Results {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Text,
			PublishedAt: r.PublishedDate,
		})
	}
	return SearchResults{Results: results}, nil
}
