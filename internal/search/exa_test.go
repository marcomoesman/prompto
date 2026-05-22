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

func TestExa_Success(t *testing.T) {
	var captured exaRequest
	var capturedKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		capturedKey = r.Header.Get("x-api-key")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"title":"net/http","url":"https://pkg.go.dev/net/http","text":"HTTP client and server","publishedDate":"2025-12-01"}
			]
		}`))
	}))
	defer srv.Close()

	s := newExa(srv.Client(), "exa-key", srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "net/http docs", MaxResults: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if capturedKey != "exa-key" {
		t.Errorf("x-api-key header = %q", capturedKey)
	}
	if captured.Query != "net/http docs" || captured.NumResults != 3 || captured.Type != "auto" {
		t.Errorf("captured request = %+v", captured)
	}
	if !captured.Contents.Text {
		t.Error("contents.text should be true so Exa returns snippets")
	}
	if len(res.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(res.Results))
	}
	got := res.Results[0]
	if got.Title != "net/http" || got.URL != "https://pkg.go.dev/net/http" || got.Snippet != "HTTP client and server" || got.PublishedAt != "2025-12-01" {
		t.Errorf("result = %+v", got)
	}
}

func TestExa_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()

	s := newExa(srv.Client(), "k", srv.URL)
	_, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error %q should mention 429 and the response body", err.Error())
	}
}

func TestExa_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": []}`))
	}))
	defer srv.Close()

	s := newExa(srv.Client(), "k", srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 0 {
		t.Errorf("got %d, want 0", len(res.Results))
	}
}
