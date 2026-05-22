package agent

import (
	"context"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// stubTool is a minimal Tool satisfier for resolver tests. It carries a name
// and reports the rest of the interface with safe defaults.
type stubTool struct{ name string }

func (s stubTool) Name() string                                                 { return s.name }
func (s stubTool) Definition() api.ToolDefinition                               { return api.ToolDefinition{Name: s.name} }
func (s stubTool) FormatForDisplay([]byte) string                               { return "" }
func (s stubTool) MaxResultBytes() int                                          { return 0 }
func (s stubTool) IsReadOnly() bool                                             { return true }
func (s stubTool) IsConcurrencySafe() bool                                      { return true }
func (s stubTool) PermissionKey([]byte) string                                  { return "" }
func (s stubTool) Execute(context.Context, ToolContext, []byte) (Result, error) { return Result{}, nil }

// stubResolver is an in-memory ToolResolver for tests.
type stubResolver struct {
	tools map[string]Tool
}

func newStubResolver(names ...string) *stubResolver {
	tools := make(map[string]Tool, len(names))
	for _, n := range names {
		tools[n] = stubTool{name: n}
	}
	return &stubResolver{tools: tools}
}

func (s *stubResolver) Resolve(name string) (Tool, bool) {
	t, ok := s.tools[name]
	return t, ok
}

func (s *stubResolver) Definitions() []api.ToolDefinition {
	out := make([]api.ToolDefinition, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t.Definition())
	}
	return out
}

func TestFilteredResolver_AllowsOnlyAllowlisted(t *testing.T) {
	inner := newStubResolver("read", "edit", "bash", "task")
	r := NewFilteredResolver(inner, []string{"read"}, false)

	if _, ok := r.Resolve("read"); !ok {
		t.Error("read should resolve")
	}
	if _, ok := r.Resolve("edit"); ok {
		t.Error("edit not in allowlist; should not resolve")
	}
	if _, ok := r.Resolve("bash"); ok {
		t.Error("bash not in allowlist; should not resolve")
	}
}

func TestFilteredResolver_SubagentStripsDisallowed(t *testing.T) {
	inner := newStubResolver("read", "task", "todowrite")
	// Even when caller asks for task/todowrite, subagent resolver must strip.
	r := NewFilteredResolver(inner, []string{"read", "task", "todowrite"}, true)
	if _, ok := r.Resolve("task"); ok {
		t.Error("subagent: task is disallowed; resolver must drop it")
	}
	if _, ok := r.Resolve("todowrite"); ok {
		t.Error("subagent: todowrite is disallowed; resolver must drop it")
	}
	defs := r.Definitions()
	for _, d := range defs {
		if d.Name == "task" || d.Name == "todowrite" {
			t.Errorf("Definitions returned disallowed %q for subagent", d.Name)
		}
	}
}

func TestFilteredResolver_PrimaryKeepsDisallowed(t *testing.T) {
	inner := newStubResolver("read", "task", "todowrite")
	// Primary (isSubagent=false) keeps everything in its allowlist.
	r := NewFilteredResolver(inner, []string{"read", "task", "todowrite"}, false)
	if _, ok := r.Resolve("task"); !ok {
		t.Error("primary: task should resolve when in allowlist")
	}
	if _, ok := r.Resolve("todowrite"); !ok {
		t.Error("primary: todowrite should resolve when in allowlist")
	}
}

func TestFilteredResolver_SubagentEmptyAllowExposesAllNonDisallowed(t *testing.T) {
	inner := newStubResolver("read", "edit", "task", "todowrite")
	r := NewFilteredResolver(inner, nil, true)

	if _, ok := r.Resolve("read"); !ok {
		t.Error("read should resolve under empty allow")
	}
	if _, ok := r.Resolve("edit"); !ok {
		t.Error("edit should resolve under empty allow")
	}
	if _, ok := r.Resolve("task"); ok {
		t.Error("task is disallowed for subagents; should not resolve")
	}
	if _, ok := r.Resolve("todowrite"); ok {
		t.Error("todowrite is disallowed for subagents; should not resolve")
	}

	defs := r.Definitions()
	if len(defs) != 2 {
		t.Errorf("Definitions len = %d, want 2 (read+edit)", len(defs))
	}
}

func TestFilteredResolver_PrimaryEmptyAllowExposesEverything(t *testing.T) {
	inner := newStubResolver("read", "edit", "task", "todowrite")
	r := NewFilteredResolver(inner, nil, false)
	for _, name := range []string{"read", "edit", "task", "todowrite"} {
		if _, ok := r.Resolve(name); !ok {
			t.Errorf("primary empty-allow: %q should resolve", name)
		}
	}
	if got := len(r.Definitions()); got != 4 {
		t.Errorf("Definitions len = %d, want 4", got)
	}
}

func TestFilteredResolver_DefinitionsRespectAllowlist(t *testing.T) {
	inner := newStubResolver("read", "edit", "bash")
	r := NewFilteredResolver(inner, []string{"read", "edit"}, false)
	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("len = %d, want 2", len(defs))
	}
	have := map[string]bool{}
	for _, d := range defs {
		have[d.Name] = true
	}
	if !have["read"] || !have["edit"] {
		t.Errorf("missing allowed defs: %v", have)
	}
	if have["bash"] {
		t.Error("bash leaked through Definitions")
	}
}
