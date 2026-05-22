package agent

import (
	"strings"
	"testing"
)

// TestResearchModeSection_ContainsWorkflow asserts the research-mode
// prompt encodes the search → fetch → cite pattern derived from
// Factory's WebSearch usage data. Loud regression on lost steps.
func TestResearchModeSection_ContainsWorkflow(t *testing.T) {
	required := []string{
		"# Research mode",
		"websearch",
		"webfetch",
		"5–10 word query",
		"authoritative",
		"Cross-check",
		"Cite every claim",
		"[short label](url)",
		"Start with the answer",
	}
	for _, want := range required {
		if !strings.Contains(researchModeSection, want) {
			t.Errorf("researchModeSection missing %q", want)
		}
	}
}

// TestResearchSystemPrompt_SharesStablePrefix asserts that the
// research prompt and explore prompt produce byte-identical stable
// prefixes when given the same tool allowlist and provider profile,
// so the prompt cache survives agent switches between them.
//
// Subagents that share Tools and LocalProvider hash to the same
// stable prefix; build/plan diverge because their allowlists differ
// (which is correct — different prefix means cache invalidation on
// the next agent switch, but stable within one agent's lifetime).
func TestResearchSystemPrompt_SharesStablePrefix(t *testing.T) {
	// Use the same tool allowlist for both subagents so the stable
	// prefix is byte-identical. In production, explore and research
	// have different allowlists, so this test verifies the assembly
	// is deterministic in tools, not that the two real prompts match.
	tools := []string{"read", "grep", "glob", "webfetch"}
	in := BuildSystemPromptInput{
		Cwd: "/tmp", Platform: "linux", Model: "x", Date: "2026-05-01",
		Tools: tools,
	}
	research := ResearchSystemPrompt(in)
	explore := ExploreSystemPrompt(in)
	stable := stableBlocks(in)

	if len(research) < len(stable) {
		t.Fatalf("research blocks (%d) < stable prefix (%d)", len(research), len(stable))
	}
	for i, want := range stable {
		if research[i].Text != want.Text {
			t.Errorf("research stable block %d differs from stableBlocks(in); cache will miss on agent switch", i)
		}
		if explore[i].Text != want.Text {
			t.Errorf("explore stable block %d also differs (sanity check)", i)
		}
	}
}

// TestPlanModeSection_ContainsWorkflow asserts the plan-mode prompt
// covers the 5-phase workflow, the schema headings, and references
// the `task` tool. Phase numbers are checked literally so a regression
// (e.g. losing the Review phase) is loud.
func TestPlanModeSection_ContainsWorkflow(t *testing.T) {
	required := []string{
		"## Workflow",
		"1. **Initial Understanding**",
		"2. **Design**",
		"3. **Review**",
		"4. **Final Plan**",
		"5. **Exit**",
		"## Plan schema",
		"## Context",
		"## Goal & acceptance criteria",
		"## Files",
		"## Verification",
		"## Risks / out-of-scope",
		"## Rubric",
		"`task`",
		`subagent_type: "explore"`,
		"`plan_exit`",
	}
	for _, want := range required {
		if !strings.Contains(planModeSection, want) {
			t.Errorf("planModeSection missing %q", want)
		}
	}
}

func TestPrompt_StableThenVolatileOrder(t *testing.T) {
	p := NewPrompt().
		AddStable(Section{Name: "s1", Content: "a"}).
		AddStable(Section{Name: "s2", Content: "b"}).
		AddVolatile(Section{Name: "v1", Content: "c"})

	blocks := p.SystemBlocks()
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(blocks))
	}
	if blocks[0].Text != "a" || blocks[1].Text != "b" || blocks[2].Text != "c" {
		t.Errorf("order = %q %q %q", blocks[0].Text, blocks[1].Text, blocks[2].Text)
	}
}

func TestPrompt_CacheOnLastStableBlock(t *testing.T) {
	p := NewPrompt().
		AddStable(Section{Name: "s1", Content: "a"}).
		AddStable(Section{Name: "s2", Content: "b"}).
		AddVolatile(Section{Name: "v1", Content: "c"})

	blocks := p.SystemBlocks()
	cacheCount := 0
	var cacheIdx int
	for i, b := range blocks {
		if b.Cache {
			cacheCount++
			cacheIdx = i
		}
	}
	if cacheCount != 1 {
		t.Fatalf("cache markers = %d, want exactly 1", cacheCount)
	}
	if cacheIdx != 1 {
		t.Errorf("cache marker at index %d, want 1 (last stable block)", cacheIdx)
	}
}

func TestPrompt_NoStableNoCacheMarker(t *testing.T) {
	p := NewPrompt().AddVolatile(Section{Name: "v1", Content: "a"})
	blocks := p.SystemBlocks()
	for i, b := range blocks {
		if b.Cache {
			t.Errorf("block %d has cache marker but no stable sections", i)
		}
	}
}

// TestBuildToolUseRules_FiltersByTools asserts the conditional sub-block
// assembly: Web Fetching only when webfetch is allowed, Shell Commands
// only when bash is allowed, Local Searching only when grep/glob is
// allowed. The intro + Local-vs-Web + File-Reading blocks are universal.
func TestBuildToolUseRules_FiltersByTools(t *testing.T) {
	cases := []struct {
		name        string
		tools       []string
		mustHave    []string
		mustNotHave []string
	}{
		{
			name:        "no tools restriction emits everything",
			tools:       nil,
			mustHave:    []string{"# Tool Use", "## Local tools vs Web tools", "## File Reading and Editing", "## Local Searching", "## Web Fetching", "## Shell Commands"},
			mustNotHave: nil,
		},
		{
			name:        "explore-style: read/grep/glob/webfetch only",
			tools:       []string{"read", "grep", "glob", "webfetch"},
			mustHave:    []string{"## Local Searching", "## Web Fetching"},
			mustNotHave: []string{"## Shell Commands"},
		},
		{
			name:        "no webfetch drops Web Fetching",
			tools:       []string{"read", "edit", "write", "grep", "glob", "bash"},
			mustHave:    []string{"## Local Searching", "## Shell Commands"},
			mustNotHave: []string{"## Web Fetching"},
		},
		{
			name:        "no bash drops Shell Commands",
			tools:       []string{"read", "edit", "write", "grep", "glob", "webfetch"},
			mustHave:    []string{"## Local Searching", "## Web Fetching"},
			mustNotHave: []string{"## Shell Commands"},
		},
		{
			name:        "no grep/glob drops Local Searching",
			tools:       []string{"read", "edit", "write", "bash", "webfetch"},
			mustHave:    []string{"## Shell Commands", "## Web Fetching"},
			mustNotHave: []string{"## Local Searching"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildToolUseRules(BuildSystemPromptInput{Tools: tc.tools})
			for _, want := range tc.mustHave {
				if !strings.Contains(got, want) {
					t.Errorf("expected sub-block %q in output:\n%s", want, got)
				}
			}
			for _, dropped := range tc.mustNotHave {
				if strings.Contains(got, dropped) {
					t.Errorf("sub-block %q should be filtered out for tools=%v:\n%s", dropped, tc.tools, got)
				}
			}
		})
	}
}

// TestBuildToolUseRules_LocalProviderGatesAntiInjection asserts the
// tool-call channel anti-injection block fires only when LocalProvider
// is true. Cloud sessions must not pay for that ~300 bytes.
func TestBuildToolUseRules_LocalProviderGatesAntiInjection(t *testing.T) {
	cloud := buildToolUseRules(BuildSystemPromptInput{LocalProvider: false})
	local := buildToolUseRules(BuildSystemPromptInput{LocalProvider: true})

	if strings.Contains(cloud, "CRITICAL: Tool-call channel") {
		t.Errorf("cloud variant must not include the anti-injection block:\n%s", cloud)
	}
	if !strings.Contains(local, "CRITICAL: Tool-call channel") {
		t.Errorf("local variant must include the anti-injection block:\n%s", local)
	}
	// Sanity: the local variant is strictly larger than the cloud variant
	// (one extra sub-block, no other differences).
	if len(local) <= len(cloud) {
		t.Errorf("local variant (%d) should be larger than cloud (%d)", len(local), len(cloud))
	}
}

// TestStableBlocks_DeterministicForFixedInput locks the byte-stability
// invariant: identical input produces identical stable prefix across
// calls. Critical for prompt-cache reuse.
func TestStableBlocks_DeterministicForFixedInput(t *testing.T) {
	in := BuildSystemPromptInput{
		Cwd: "/tmp", Platform: "linux", Model: "m", Date: "2026-05-01",
		Tools:         []string{"read", "edit", "bash"},
		LocalProvider: false,
	}
	a := stableBlocks(in)
	b := stableBlocks(in)
	if len(a) != len(b) {
		t.Fatalf("len mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Text != b[i].Text {
			t.Errorf("stable block %d differs across calls:\n  a=%q\n  b=%q", i, a[i].Text, b[i].Text)
		}
	}
}

func TestPrompt_NoVolatileStillWorks(t *testing.T) {
	p := NewPrompt().
		AddStable(Section{Name: "s1", Content: "a"}).
		AddStable(Section{Name: "s2", Content: "b"})

	blocks := p.SystemBlocks()
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if !blocks[1].Cache {
		t.Error("last block should have cache marker")
	}
	if blocks[0].Cache {
		t.Error("non-last block should not have cache marker")
	}
}

func TestPrompt_EmptyReturnsNil(t *testing.T) {
	p := NewPrompt()
	if blocks := p.SystemBlocks(); blocks != nil {
		t.Errorf("empty prompt blocks = %v, want nil", blocks)
	}
}

func TestBuildSystemPrompt_ShapeAndCacheBoundary(t *testing.T) {
	blocks := BuildSystemPrompt(BuildSystemPromptInput{
		Cwd:                 "/tmp/proj",
		Platform:            "darwin/arm64",
		Model:               "test-model",
		Date:                "2026-04-21",
		ProjectInstructions: "be concise",
	})
	if len(blocks) < 4 {
		t.Fatalf("blocks = %d, want at least 4 stable + project instructions", len(blocks))
	}

	// Exactly one Cache: true block.
	cacheCount := 0
	var cacheIdx int
	for i, b := range blocks {
		if b.Cache {
			cacheCount++
			cacheIdx = i
		}
	}
	if cacheCount != 1 {
		t.Errorf("cache markers = %d, want 1", cacheCount)
	}

	// With project instructions present, the cache boundary is not the
	// final block — volatile content follows it.
	if cacheIdx == len(blocks)-1 {
		t.Error("cache boundary should not be the final block when volatile content is present")
	}
}

func TestBuildSystemPrompt_StableIsByteIdenticalAcrossCalls(t *testing.T) {
	// The stable prefix must not drift between consecutive calls within a
	// session. Cwd/Platform/Model/Date are session-stable (folded into
	// the cacheable prefix), so we hold them constant and vary only the
	// truly volatile inputs (ProjectInstructions, Todos).
	common := BuildSystemPromptInput{
		Cwd: "/proj", Platform: "linux/amd64", Model: "m1", Date: "2026-01-01",
	}
	aIn := common
	aIn.ProjectInstructions = "first"
	bIn := common
	bIn.ProjectInstructions = "second"
	bIn.Todos = []Todo{{ID: "1", Content: "x", Status: "pending", ActiveForm: "X"}}
	a := BuildSystemPrompt(aIn)
	b := BuildSystemPrompt(bIn)
	if len(a) == 0 || len(b) == 0 {
		t.Fatal("empty blocks")
	}
	// Find the cache-marked blocks.
	var ai, bi int
	for i, x := range a {
		if x.Cache {
			ai = i
		}
	}
	for i, x := range b {
		if x.Cache {
			bi = i
		}
	}
	if ai != bi {
		t.Fatalf("cache index drift: %d vs %d", ai, bi)
	}
	// All blocks up to and including the cache boundary must match exactly.
	for i := 0; i <= ai; i++ {
		if a[i].Text != b[i].Text {
			t.Errorf("stable block %d differs across calls:\n  a=%q\n  b=%q", i, a[i].Text, b[i].Text)
		}
	}
}

func TestBuildSystemPrompt_TodosRenderedVolatile(t *testing.T) {
	blocks := BuildSystemPrompt(BuildSystemPromptInput{
		Cwd:      "/tmp",
		Platform: "linux/amd64",
		Model:    "m",
		Date:     "2026-04-21",
		Todos: []Todo{
			{ID: "1", Content: "Add notifier", Status: "pending", ActiveForm: "Adding notifier"},
			{ID: "2", Content: "Wire run loop", Status: "in_progress", ActiveForm: "Wiring run loop"},
			{ID: "3", Content: "Document phase", Status: "completed", ActiveForm: "Documenting phase"},
		},
	})

	cacheIdx := -1
	for i, b := range blocks {
		if b.Cache {
			cacheIdx = i
		}
	}
	if cacheIdx < 0 {
		t.Fatal("missing cache boundary")
	}

	foundAfterCache := false
	for i := cacheIdx + 1; i < len(blocks); i++ {
		text := blocks[i].Text
		if !contains(text, "# Current todos") {
			continue
		}
		foundAfterCache = true
		// Pending and in-progress glyphs render; completed items are
		// elided to keep the volatile section short, with a trailing
		// "(N completed hidden)" line preserving visibility.
		if !contains(text, "[ ] Add notifier") {
			t.Errorf("pending glyph missing in: %q", text)
		}
		if !contains(text, "[~] Wire run loop (in progress: Wiring run loop)") {
			t.Errorf("in-progress glyph missing in: %q", text)
		}
		if contains(text, "[x] Document phase") {
			t.Errorf("completed todo must not render verbatim, got: %q", text)
		}
		if !contains(text, "(1 completed hidden)") {
			t.Errorf("hidden-completed footer missing in: %q", text)
		}
	}
	if !foundAfterCache {
		t.Error("todos section must appear after the cache boundary")
	}
}

func TestBuildSystemPrompt_EmptyTodosOmitsSection(t *testing.T) {
	blocks := BuildSystemPrompt(BuildSystemPromptInput{
		Cwd:      "/tmp",
		Platform: "linux/amd64",
		Model:    "m",
		Date:     "2026-04-21",
	})
	for _, b := range blocks {
		if contains(b.Text, "# Current todos") {
			t.Errorf("empty todo list must not render section, got: %q", b.Text)
		}
	}
}

func TestBuildSystemPrompt_ProjectInstructionsVolatile(t *testing.T) {
	blocks := BuildSystemPrompt(BuildSystemPromptInput{
		Cwd:                 "/tmp",
		Platform:            "linux/amd64",
		Model:               "m",
		Date:                "2026-04-21",
		ProjectInstructions: "prefer go test -race",
	})
	// Find it in the volatile (post-cache) region.
	found := false
	for i, b := range blocks {
		if contains(b.Text, "prefer go test -race") {
			// Must be after the cache boundary.
			for j := 0; j < i; j++ {
				if blocks[j].Cache {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("ProjectInstructions content must appear after the cache boundary")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
