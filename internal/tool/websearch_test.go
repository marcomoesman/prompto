package tool

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/search"
)

type stubSearcher struct {
	name      string
	results   search.SearchResults
	err       error
	lastQuery search.SearchRequest
}

func (s *stubSearcher) ProviderName() string { return s.name }
func (s *stubSearcher) Search(_ context.Context, req search.SearchRequest) (search.SearchResults, error) {
	s.lastQuery = req
	return s.results, s.err
}

func TestWebSearch_Defaults(t *testing.T) {
	stub := &stubSearcher{name: "tavily"}
	tool := NewWebSearchTool(stub)
	_, err := tool.Execute(context.Background(), agent.ToolContext{}, []byte(`{"query":"go modules tutorial"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if stub.lastQuery.MaxResults != websearchDefaultMaxResults {
		t.Errorf("MaxResults = %d, want default %d", stub.lastQuery.MaxResults, websearchDefaultMaxResults)
	}
}

func TestWebSearch_ClampsMaxResults(t *testing.T) {
	stub := &stubSearcher{name: "tavily"}
	tool := NewWebSearchTool(stub)
	_, err := tool.Execute(context.Background(), agent.ToolContext{}, []byte(`{"query":"x","max_results":50}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if stub.lastQuery.MaxResults != websearchResultCeiling {
		t.Errorf("MaxResults = %d, want clamp to %d", stub.lastQuery.MaxResults, websearchResultCeiling)
	}
}

func TestWebSearch_RendersResults(t *testing.T) {
	stub := &stubSearcher{
		name: "tavily",
		results: search.SearchResults{Results: []search.SearchResult{
			{Title: "Go releases", URL: "https://go.dev/dl/", Snippet: "stable 1.25.0", PublishedAt: "2026-02-01"},
			{Title: "Go blog", URL: "https://go.dev/blog/", Snippet: "release notes"},
		}},
	}
	tool := NewWebSearchTool(stub)
	res, err := tool.Execute(context.Background(), agent.ToolContext{}, []byte(`{"query":"go latest"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{
		"## 1. Go releases",
		"https://go.dev/dl/",
		"2026-02-01",
		"stable 1.25.0",
		"## 2. Go blog",
		"https://go.dev/blog/",
		"release notes",
	}
	for _, sub := range want {
		if !strings.Contains(res.Content, sub) {
			t.Errorf("rendered output missing %q\n--- got ---\n%s", sub, res.Content)
		}
	}
	if !strings.Contains(res.DisplaySummary, "2 results from tavily") {
		t.Errorf("DisplaySummary = %q", res.DisplaySummary)
	}
}

func TestWebSearch_EmptyResults(t *testing.T) {
	stub := &stubSearcher{name: "exa"}
	tool := NewWebSearchTool(stub)
	res, err := tool.Execute(context.Background(), agent.ToolContext{}, []byte(`{"query":"x"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "No results") {
		t.Errorf("Content = %q, want 'No results.'", res.Content)
	}
}

func TestWebSearch_QueryRequired(t *testing.T) {
	tool := NewWebSearchTool(&stubSearcher{name: "tavily"})
	_, err := tool.Execute(context.Background(), agent.ToolContext{}, []byte(`{"query":"   "}`))
	if err == nil {
		t.Fatal("expected error for empty query")
	}
	if !strings.Contains(err.Error(), "query is required") {
		t.Errorf("error %q should mention query is required", err.Error())
	}
}

func TestWebSearch_PropagatesProviderError(t *testing.T) {
	stub := &stubSearcher{name: "tavily", err: errors.New("rate limited")}
	tool := NewWebSearchTool(stub)
	_, err := tool.Execute(context.Background(), agent.ToolContext{}, []byte(`{"query":"x"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error %q should wrap provider error", err.Error())
	}
}

func TestWebSearch_FormatForDisplay(t *testing.T) {
	tool := NewWebSearchTool(&stubSearcher{name: "tavily"})
	got := tool.FormatForDisplay([]byte(`{"query":"react useeffect cleanup"}`))
	if !strings.Contains(got, `WebSearch(query: "react useeffect cleanup")`) {
		t.Errorf("FormatForDisplay = %q", got)
	}
}

func TestWebSearch_PermissionKey(t *testing.T) {
	tool := NewWebSearchTool(&stubSearcher{name: "tavily"})
	if got := tool.PermissionKey([]byte(`{"query":"x"}`)); got != "websearch" {
		t.Errorf("PermissionKey = %q, want %q", got, "websearch")
	}
}

func TestWebSearch_DescriptionMentionsProvider(t *testing.T) {
	tool := NewWebSearchTool(&stubSearcher{name: "searxng"})
	if !strings.Contains(tool.Definition().Description, "searxng") {
		t.Error("tool description should mention the configured provider")
	}
}
