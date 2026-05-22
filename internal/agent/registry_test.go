package agent

import "testing"

func TestDefaultRegistry_BuildHasAllTools(t *testing.T) {
	r := DefaultRegistry()
	build, ok := r.Resolve("build")
	if !ok {
		t.Fatal("build agent missing from default registry")
	}
	if !build.AllowsAllTools() {
		t.Errorf("build agent should expose all tools, got allowlist=%v", build.Tools)
	}
	if build.Mode != ModePrimary {
		t.Errorf("build.Mode = %q, want %q", build.Mode, ModePrimary)
	}
}

func TestDefaultRegistry_PlanIsPrimaryReadOnly(t *testing.T) {
	r := DefaultRegistry()
	plan, ok := r.Resolve("plan")
	if !ok {
		t.Fatal("plan agent missing")
	}
	if plan.Mode != ModePrimary {
		t.Errorf("plan.Mode = %q, want primary", plan.Mode)
	}
	if !plan.PlanMode {
		t.Error("plan.PlanMode should be true")
	}
	if !plan.ReadOnly {
		t.Error("plan.ReadOnly should be true")
	}
	if plan.SystemPrompt == nil {
		t.Error("plan.SystemPrompt should be set")
	}
	// Plan agent must include `task` so it can fan out parallel
	// investigations (the spawner restricts the subagent_type to
	// read-only children).
	hasTask := false
	hasPlanExit := false
	for _, tool := range plan.Tools {
		switch tool {
		case "task":
			hasTask = true
		case "plan_exit":
			hasPlanExit = true
		}
	}
	if !hasTask {
		t.Error("plan.Tools must include \"task\" for parallel investigation")
	}
	if !hasPlanExit {
		t.Error("plan.Tools must include \"plan_exit\" for the approval gate")
	}
}

func TestDefaultRegistry_ExploreIsSubagentReadOnly(t *testing.T) {
	r := DefaultRegistry()
	exp, ok := r.Resolve("explore")
	if !ok {
		t.Fatal("explore agent missing")
	}
	if exp.Mode != ModeSubagent {
		t.Errorf("explore.Mode = %q, want subagent", exp.Mode)
	}
	if !exp.ReadOnly {
		t.Error("explore.ReadOnly should be true")
	}
	if exp.SystemPrompt == nil {
		t.Error("explore.SystemPrompt should be set")
	}
	// Explore must NOT include edit/write/bash/task in its allowlist.
	for _, tool := range exp.Tools {
		switch tool {
		case "edit", "write", "bash", "task":
			t.Errorf("explore allowlist must not include %q", tool)
		}
	}
}

func TestDefaultRegistry_ResearchIsSubagentReadOnly(t *testing.T) {
	r := DefaultRegistry()
	res, ok := r.Resolve("research")
	if !ok {
		t.Fatal("research agent missing")
	}
	if res.Mode != ModeSubagent {
		t.Errorf("research.Mode = %q, want subagent", res.Mode)
	}
	if !res.ReadOnly {
		t.Error("research.ReadOnly should be true")
	}
	if res.SystemPrompt == nil {
		t.Error("research.SystemPrompt should be set")
	}
	if res.Color == "" {
		t.Error("research.Color should be set for TUI distinction")
	}
	// Research must include websearch (its primary tool) plus the
	// retrieve + ground combo. Must NOT include edit/write/bash/task.
	hasWebsearch := false
	hasWebfetch := false
	for _, tool := range res.Tools {
		switch tool {
		case "websearch":
			hasWebsearch = true
		case "webfetch":
			hasWebfetch = true
		case "edit", "write", "bash", "task":
			t.Errorf("research allowlist must not include %q", tool)
		}
	}
	if !hasWebsearch {
		t.Error("research.Tools must include \"websearch\"")
	}
	if !hasWebfetch {
		t.Error("research.Tools must include \"webfetch\" for retrieval after discovery")
	}
}

func TestDefaultRegistry_PrimariesPartition(t *testing.T) {
	r := DefaultRegistry()
	prim := r.Primaries()
	sub := r.Subagents()
	if len(prim) < 2 {
		t.Errorf("Primaries len = %d, want >= 2", len(prim))
	}
	if len(sub) < 1 {
		t.Errorf("Subagents len = %d, want >= 1", len(sub))
	}
	for _, p := range prim {
		if p.Mode == ModeSubagent {
			t.Errorf("Primaries returned subagent-only %q", p.Name)
		}
	}
	for _, s := range sub {
		if s.Mode == ModePrimary {
			t.Errorf("Subagents returned primary-only %q", s.Name)
		}
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewAgentRegistry()
	if err := r.Register(AgentDefinition{Name: "a", Mode: ModePrimary}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(AgentDefinition{Name: "a", Mode: ModePrimary}); err == nil {
		t.Fatal("expected error on duplicate name")
	}
}

func TestRegistry_EmptyName(t *testing.T) {
	r := NewAgentRegistry()
	if err := r.Register(AgentDefinition{Mode: ModePrimary}); err == nil {
		t.Fatal("expected error on empty Name")
	}
}

func TestAgentDefinition_EffectiveToolsStripsDisallowed(t *testing.T) {
	def := AgentDefinition{
		Name:  "explore",
		Tools: []string{"read", "grep", "task", "edit"},
	}
	got := def.EffectiveTools()
	for _, name := range got {
		if AllAgentDisallowedTools[name] {
			t.Errorf("EffectiveTools returned disallowed %q", name)
		}
	}
	if len(got) != 3 {
		t.Errorf("EffectiveTools len = %d (%v), want 3 (read/grep/edit)", len(got), got)
	}
}

func TestIsAgentDisallowed(t *testing.T) {
	if !IsAgentDisallowed("task") {
		t.Error("task must be globally disallowed")
	}
	if IsAgentDisallowed("read") {
		t.Error("read should NOT be in the global disallow list")
	}
}
