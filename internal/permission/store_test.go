package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestStore_LoadMissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	r, err := LoadRuleset(LoadRulesetInput{ProjectPath: filepath.Join(dir, "permissions.json")})
	if err != nil {
		t.Fatalf("LoadRuleset: %v", err)
	}
	if len(r.Rules()) != 0 {
		t.Errorf("rules = %d, want 0 for missing file", len(r.Rules()))
	}
}

func TestStore_LoadCorruptedFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")
	_ = os.WriteFile(path, []byte("{not valid json"), 0o644)

	_, err := LoadRuleset(LoadRulesetInput{ProjectPath: path})
	if err == nil {
		t.Fatal("expected error for corrupted JSON")
	}
}

func TestStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")

	r1, err := LoadRuleset(LoadRulesetInput{ProjectPath: path})
	if err != nil {
		t.Fatal(err)
	}
	_ = r1.Append(AppendInput{Rule: Rule{
		Tool: "bash", Pattern: "git *",
		Action: agent.DecisionAllow, Scope: ScopeProject,
	}})
	_ = r1.Append(AppendInput{Rule: Rule{
		Tool: "read", Pattern: "**/*.go",
		Action: agent.DecisionAllow, Scope: ScopeProject,
	}})

	// Re-load from file.
	r2, err := LoadRuleset(LoadRulesetInput{ProjectPath: path})
	if err != nil {
		t.Fatal(err)
	}
	got := r2.Rules()
	if len(got) != 2 {
		t.Fatalf("reloaded rules = %d, want 2", len(got))
	}
	if got[0].Tool != "bash" || got[0].Pattern != "git *" || got[0].Action != agent.DecisionAllow {
		t.Errorf("rules[0] = %+v", got[0])
	}
	if got[0].Scope != ScopeProject {
		t.Errorf("rules[0].Scope = %v, want ScopeProject", got[0].Scope)
	}
}

func TestStore_SaveIsPrettyFormatted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")

	r, _ := LoadRuleset(LoadRulesetInput{ProjectPath: path})
	_ = r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "git *", Action: agent.DecisionAllow, Scope: ScopeProject}})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "\n") {
		t.Error("file content is not pretty-formatted (no newlines)")
	}
	if !strings.HasSuffix(content, "\n") {
		t.Error("file does not end with newline")
	}

	// Double check it's parseable as JSON.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("saved file doesn't parse: %v", err)
	}
}

func TestStore_SessionRulesNotPersisted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")

	r, _ := LoadRuleset(LoadRulesetInput{ProjectPath: path})
	_ = r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "x", Action: agent.DecisionAllow, Scope: ScopeSession}})

	// File should not exist because only a session rule was appended.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session-only append should not create permissions.json, got stat err = %v", err)
	}
}

func TestStore_EmptyFileTreatedAsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")
	_ = os.WriteFile(path, []byte(""), 0o644)

	r, err := LoadRuleset(LoadRulesetInput{ProjectPath: path})
	if err != nil {
		t.Fatalf("LoadRuleset: %v", err)
	}
	if len(r.Rules()) != 0 {
		t.Errorf("rules = %d, want 0 for empty file", len(r.Rules()))
	}
}
