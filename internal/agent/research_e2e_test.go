package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// stubWebSearchTool returns a canned markdown listing one URL,
// matching the shape of the real websearch tool. Used by the
// research-agent e2e test so the spawn flow doesn't require the
// internal/tool or internal/search packages (which would create
// import cycles in the agent package's tests).
type stubWebSearchTool struct{ result string }

func (t *stubWebSearchTool) Name() string                     { return "websearch" }
func (t *stubWebSearchTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "websearch"} }
func (t *stubWebSearchTool) FormatForDisplay(_ []byte) string { return "websearch()" }
func (t *stubWebSearchTool) MaxResultBytes() int              { return 0 }
func (t *stubWebSearchTool) IsReadOnly() bool                 { return true }
func (t *stubWebSearchTool) IsConcurrencySafe() bool          { return true }
func (t *stubWebSearchTool) PermissionKey(_ []byte) string    { return "websearch" }
func (t *stubWebSearchTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	return Result{Content: t.result, Bytes: len(t.result)}, nil
}

// stubWebFetchTool returns canned page content for any URL. The
// research agent's prompt instructs the model to webfetch the most
// authoritative search result; the canned content stands in for the
// real page body.
type stubWebFetchTool struct{ result string }

func (t *stubWebFetchTool) Name() string                     { return "webfetch" }
func (t *stubWebFetchTool) Definition() api.ToolDefinition   { return api.ToolDefinition{Name: "webfetch"} }
func (t *stubWebFetchTool) FormatForDisplay(_ []byte) string { return "webfetch()" }
func (t *stubWebFetchTool) MaxResultBytes() int              { return 0 }
func (t *stubWebFetchTool) IsReadOnly() bool                 { return true }
func (t *stubWebFetchTool) IsConcurrencySafe() bool          { return true }
func (t *stubWebFetchTool) PermissionKey(_ []byte) string    { return "" }
func (t *stubWebFetchTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	return Result{Content: t.result, Bytes: len(t.result)}, nil
}

// TestResearchSubagent_E2E spawns the built-in research subagent
// from a build-agent parent. The fake provider drives a 3-turn
// search → fetch → cite flow:
//
//  1. tool_use websearch — discover URLs.
//  2. tool_use webfetch — retrieve the top result.
//  3. text — final cited summary returned to parent.
//
// Asserts the parent receives a single final assistant message
// whose text contains the canned URL — i.e. the subagent honoured
// the "cite every claim" prompt instruction. No code change beyond
// the test exercises the research-subagent wiring; this is the integration
// proof.
func TestResearchSubagent_E2E(t *testing.T) {
	const docsURL = "https://pkg.go.dev/log/slog"

	searchResult := "## 1. log/slog package\n" + docsURL + "\nstructured logging in Go's standard library"
	fetchedPage := "log/slog provides structured logging. Key types: Logger, Handler, Record. " +
		"Use slog.NewJSONHandler for production. Available since Go 1.21."

	prov := &fakeProvider{responses: [][]api.StreamEvent{
		toolUseResponse("ws-1", "websearch", `{"query":"go slog tutorial"}`),
		toolUseResponse("wf-1", "webfetch", `{"url":"`+docsURL+`","query":"summarize"}`),
		textResponse("slog provides structured logging via Logger/Handler/Record. " +
			"Use NewJSONHandler in production. Available since Go 1.21. " +
			"[docs](" + docsURL + ")"),
	}}

	resolver := newFakeResolver(
		&stubWebSearchTool{result: searchResult},
		&stubWebFetchTool{result: fetchedPage},
	)

	a := New(NewAgentInput{
		Provider: prov,
		Model:    "test-model",
		Tools:    resolver,
	})
	// Use the real `research` definition from DefaultRegistry rather
	// than a synthetic test-only definition. This is what makes the
	// test a true integration check: any drift in the registry entry
	// (allowlist, ReadOnly, prompt builder) trips this test.
	defs := DefaultRegistry()
	if _, ok := defs.Resolve("research"); !ok {
		t.Fatal("DefaultRegistry missing the research subagent — wiring incomplete")
	}
	a.registry = defs

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	res, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType:    "research",
		Prompt:          "what is slog and when was it added?",
		Description:     "investigate slog",
		ParentAgentName: "build", // non-read-only parent; should pass the read-only-child guard trivially
	})
	if err != nil {
		t.Fatalf("spawn research: %v", err)
	}

	if !strings.Contains(res.Result, docsURL) {
		t.Errorf("research summary missing citation URL %q\n--- got ---\n%s", docsURL, res.Result)
	}
	if !strings.Contains(res.Result, "slog") {
		t.Errorf("research summary should mention the topic; got %q", res.Result)
	}
	if prov.calls != 3 {
		t.Errorf("provider Complete calls = %d, want 3 (search + fetch + summary)", prov.calls)
	}
}

// TestResearchSubagent_PlanCanSpawn confirms the plan→research path
// works under the read-only-parent guard. Plan agent has ReadOnly=true;
// research has ReadOnly=true; the guard should allow this combination.
// Catches regressions in the registry entry's ReadOnly bit.
func TestResearchSubagent_PlanCanSpawn(t *testing.T) {
	prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("done")}}
	a := New(NewAgentInput{
		Provider: prov,
		Model:    "test-model",
		Tools:    newFakeResolver(),
	})
	a.registry = DefaultRegistry()

	spawn := NewSpawner(SpawnerInput{Agent: a, CanUseTool: allowAll})
	_, err := spawn(t.Context(), TaskSpawnInput{
		SubagentType:    "research",
		Prompt:          "x",
		ParentAgentName: "plan", // read-only parent
	})
	if err != nil {
		t.Fatalf("plan should be allowed to spawn research (both read-only): %v", err)
	}
}
