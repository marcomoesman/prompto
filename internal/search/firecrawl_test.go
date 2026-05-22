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

func TestFirecrawl_Success(t *testing.T) {
	var captured firecrawlRequest
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		capturedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"title":"slog docs","url":"https://pkg.go.dev/log/slog","description":"structured logging"}
			]
		}`))
	}))
	defer srv.Close()

	s := newFirecrawl(srv.Client(), "fc-key", srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "slog tutorial", MaxResults: 4})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if capturedAuth != "Bearer fc-key" {
		t.Errorf("Authorization header = %q", capturedAuth)
	}
	if captured.Query != "slog tutorial" || captured.Limit != 4 {
		t.Errorf("captured request = %+v", captured)
	}
	if len(res.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(res.Results))
	}
	got := res.Results[0]
	// Field-name gotcha: Firecrawl returns "description", not "snippet".
	if got.Title != "slog docs" || got.URL != "https://pkg.go.dev/log/slog" || got.Snippet != "structured logging" {
		t.Errorf("result = %+v", got)
	}
}

func TestFirecrawl_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`upstream error`))
	}))
	defer srv.Close()

	s := newFirecrawl(srv.Client(), "k", srv.URL)
	_, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "upstream error") {
		t.Errorf("error %q should mention 500 and the response body", err.Error())
	}
}

func TestFirecrawl_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer srv.Close()

	s := newFirecrawl(srv.Client(), "k", srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 0 {
		t.Errorf("got %d, want 0", len(res.Results))
	}
}
