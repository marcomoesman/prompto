package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearxng_Success(t *testing.T) {
	var capturedQuery, capturedFormat string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		capturedQuery = r.URL.Query().Get("q")
		capturedFormat = r.URL.Query().Get("format")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{"title":"Result 1","url":"https://a.example/","content":"first hit"},
				{"title":"Result 2","url":"https://b.example/","content":"second hit"},
				{"title":"Result 3","url":"https://c.example/","content":"third hit"}
			]
		}`))
	}))
	defer srv.Close()

	s := newSearxng(srv.Client(), srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "kubernetes ingress", MaxResults: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if capturedQuery != "kubernetes ingress" {
		t.Errorf("q = %q", capturedQuery)
	}
	if capturedFormat != "json" {
		t.Errorf("format = %q, want json", capturedFormat)
	}
	// Client-side slicing: instance returned 3, MaxResults asked for 2.
	if len(res.Results) != 2 {
		t.Fatalf("got %d results, want 2 (client-side slice of 3)", len(res.Results))
	}
	// Field-name gotcha: SearXNG returns "content", not "snippet".
	if res.Results[0].Snippet != "first hit" {
		t.Errorf("snippet[0] = %q", res.Results[0].Snippet)
	}
}

func TestSearxng_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`bot detected`))
	}))
	defer srv.Close()

	s := newSearxng(srv.Client(), srv.URL)
	_, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "bot detected") {
		t.Errorf("error %q should mention 403 and the body", err.Error())
	}
}

func TestSearxng_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": []}`))
	}))
	defer srv.Close()

	s := newSearxng(srv.Client(), srv.URL)
	res, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Results) != 0 {
		t.Errorf("got %d, want 0", len(res.Results))
	}
}

func TestSearxng_MissingBaseURL(t *testing.T) {
	s := newSearxng(http.DefaultClient, "")
	_, err := s.Search(context.Background(), SearchRequest{Query: "x", MaxResults: 5})
	if err == nil {
		t.Fatal("expected error when base_url is empty")
	}
	if !strings.Contains(err.Error(), "base_url is required") {
		t.Errorf("error %q should mention base_url is required", err.Error())
	}
}
