package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTavily_Success(t *testing.T) {
	var captured tavilyRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"title":"Go releases","url":"https://go.dev/dl/","content":"Latest stable 1.25.0","published_date":"2026-02-01"},
				{"title":"Go blog","url":"https://go.dev/blog/","content":"Release notes"}
			]
		}`))
	}))
	defer srv.Close()

	s := newTavily(srv.Client(), "test-key", srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "go latest version", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if captured.APIKey != "test-key" || captured.Query != "go latest version" || captured.MaxResults != 5 {
		t.Errorf("captured request = %+v", captured)
	}
	if len(res.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(res.Results))
	}
	if res.Results[0].Title != "Go releases" || res.Results[0].URL != "https://go.dev/dl/" {
		t.Errorf("result[0] = %+v", res.Results[0])
	}
	if res.Results[0].Snippet != "Latest stable 1.25.0" {
		t.Errorf("snippet = %q", res.Results[0].Snippet)
	}
	if res.Results[0].PublishedAt != "2026-02-01" {
		t.Errorf("published_at = %q", res.Results[0].PublishedAt)
	}
}

func TestTavily_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	s := newTavily(srv.Client(), "bad-key", srv.URL)
	_, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("error %q should mention 401 and the response body", err.Error())
	}
}

func TestTavily_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": []}`))
	}))
	defer srv.Close()

	s := newTavily(srv.Client(), "k", srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Results == nil {
		t.Fatal("Results should be non-nil even when empty")
	}
	if len(res.Results) != 0 {
		t.Errorf("got %d results, want 0", len(res.Results))
	}
}
