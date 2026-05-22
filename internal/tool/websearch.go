package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/search"
)

// websearchDefaultMaxResults is what a model gets when it omits
// max_results. Five matches the median of provider defaults
// (Tavily: 5, Exa: 10, Firecrawl: 5, SearXNG: 10) and keeps the
// rendered output well under the 50KB result cap.
const websearchDefaultMaxResults = 5

// websearchResultCeiling clamps the model's max_results upward.
// Search APIs return 50–100 hits at most; agents rarely need
// more than 10 to triangulate an answer.
const websearchResultCeiling = 10

// WebSearchInput defines the JSON parameters for the websearch tool.
type WebSearchInput struct {
	Query      string `json:"query"                 jsonschema:"required,description=Search query, 5-10 words. Name the framework, library, and version when relevant (e.g. 'React useEffect cleanup' beats 'useEffect')."`
	MaxResults int    `json:"max_results,omitzero"  jsonschema:"description=Number of results to return (1-10). Defaults to 5."`
}

// WebSearchTool runs queries through a configured search backend
// (Tavily / Exa / Firecrawl / SearXNG) and returns ranked results
// formatted as numbered markdown.
type WebSearchTool struct {
	definition api.ToolDefinition
	searcher   search.Searcher
}

// NewWebSearchTool wires a Searcher into a registered tool. The
// caller is expected to construct the searcher from cfg.Search and
// only register the tool when cfg.Search != nil — agents that list
// "websearch" in their allowlist simply don't see it otherwise
// (see internal/agent/tool.go:198 for the resolver skip behaviour).
func NewWebSearchTool(s search.Searcher) *WebSearchTool {
	provider := s.ProviderName()
	return &WebSearchTool{
		searcher: s,
		definition: api.ToolDefinition{
			Name:        "websearch",
			Description: "Search the web for ranked URLs and snippets. Use this to discover sources before reading them with webfetch — search broadly first, then fetch the most authoritative result. For multi-step research that needs both search and reading several pages, prefer spawning the `research` subagent via `task`. Backed by " + provider + ".",
			InputSchema: GenerateSchema(WebSearchInput{}),
		},
	}
}

func (t *WebSearchTool) Name() string                   { return "websearch" }
func (t *WebSearchTool) Definition() api.ToolDefinition { return t.definition }
func (t *WebSearchTool) MaxResultBytes() int            { return 50 * 1024 }
func (t *WebSearchTool) IsReadOnly() bool               { return true }
func (t *WebSearchTool) IsConcurrencySafe() bool        { return true }

// PermissionKey returns the constant string "websearch". The provider
// is fixed by config — there is no per-call host distinction worth
// surfacing here. A single rule like `{tool:websearch, action:allow}`
// covers all calls.
func (t *WebSearchTool) PermissionKey(_ []byte) string { return "websearch" }

func (t *WebSearchTool) FormatForDisplay(input []byte) string {
	var params WebSearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "WebSearch(?)"
	}
	return FormatCall("WebSearch", "query", params.Query)
}

func (t *WebSearchTool) Execute(ctx context.Context, _ agent.ToolContext, input []byte) (agent.Result, error) {
	var params WebSearchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return agent.Result{}, fmt.Errorf("websearch: invalid input: %w", err)
	}
	if strings.TrimSpace(params.Query) == "" {
		return agent.Result{}, fmt.Errorf("websearch: query is required")
	}

	maxResults := params.MaxResults
	switch {
	case maxResults <= 0:
		maxResults = websearchDefaultMaxResults
	case maxResults > websearchResultCeiling:
		maxResults = websearchResultCeiling
	}

	results, err := t.searcher.Search(ctx, search.SearchRequest{
		Query:      params.Query,
		MaxResults: maxResults,
	})
	if err != nil {
		return agent.Result{}, fmt.Errorf("websearch: %w", err)
	}

	rendered := renderSearchResults(results)
	return agent.Result{
		Content:        rendered,
		Bytes:          len(rendered),
		DisplaySummary: fmt.Sprintf("%d results from %s", len(results.Results), t.searcher.ProviderName()),
	}, nil
}

// renderSearchResults formats SearchResults as the numbered markdown
// the model sees. Mirrors the webfetch output shape so the model
// reads search and fetch results in the same idiom.
func renderSearchResults(r SearchResults) string {
	if len(r.Results) == 0 {
		return "No results."
	}
	var b strings.Builder
	for i, hit := range r.Results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## %d. %s\n", i+1, strings.TrimSpace(hit.Title))
		b.WriteString(hit.URL)
		if hit.PublishedAt != "" {
			b.WriteString(" · ")
			b.WriteString(hit.PublishedAt)
		}
		if snippet := strings.TrimSpace(hit.Snippet); snippet != "" {
			b.WriteString("\n")
			b.WriteString(snippet)
		}
	}
	return b.String()
}

// SearchResults is re-exported so renderSearchResults' signature
// reads cleanly without forcing every caller of this file to import
// internal/search. Internal use only.
type SearchResults = search.SearchResults
