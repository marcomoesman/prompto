package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAgentsMDForFile_FindsNested(t *testing.T) {
	root := t.TempDir()
	subA := filepath.Join(root, "sub")
	subB := filepath.Join(subA, "deep")
	if err := os.MkdirAll(subB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subA, "AGENTS.md"), []byte("middle-rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subB, "AGENTS.md"), []byte("deepest-rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	// AGENTS.md at the load root must NOT be returned — eager pass already
	// covered it.
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root-rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadAgentsMDForFile(filepath.Join(subB, "file.go"), root)
	if err != nil {
		t.Fatalf("LoadAgentsMDForFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (sub + deep): %+v", len(got), got)
	}
	// Deepest-last ordering.
	if !strings.HasSuffix(got[1].Path, filepath.Join("sub", "deep", "AGENTS.md")) {
		t.Errorf("got[1].Path = %q, want deepest entry last", got[1].Path)
	}
	if !strings.HasSuffix(got[0].Path, filepath.Join("sub", "AGENTS.md")) {
		t.Errorf("got[0].Path = %q, want middle entry first", got[0].Path)
	}
	// Root file must not appear.
	for _, e := range got {
		if filepath.Dir(e.Path) == root {
			t.Errorf("entry at load-root level should not be returned: %s", e.Path)
		}
	}
}

func TestLoadAgentsMDForFile_FileOutsideRootReturnsNothing(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "AGENTS.md"), []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAgentsMDForFile(filepath.Join(other, "file.go"), root)
	if err != nil {
		t.Fatalf("LoadAgentsMDForFile: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries from outside the load root, want 0", len(got))
	}
}

func TestLoadAgentsMDForFile_StopsAtGitBoundary(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	deep := filepath.Join(sub, "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// .git in sub: walking up from deep should include sub/AGENTS.md but
	// must not consider anything above sub.
	if err := os.MkdirAll(filepath.Join(sub, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("sub-rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	// File above the .git boundary (between root and sub). The walk should
	// not see it because .git in sub stops the walk.
	above := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(above, []byte("above-rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadAgentsMDForFile(filepath.Join(deep, "file.go"), root)
	if err != nil {
		t.Fatalf("LoadAgentsMDForFile: %v", err)
	}
	for _, e := range got {
		if e.Path == above {
			t.Errorf("walker crossed .git boundary: returned %s", e.Path)
		}
	}
	// We should still have the sub-rule entry.
	var sawSub bool
	for _, e := range got {
		if e.Content == "sub-rule" {
			sawSub = true
		}
	}
	if !sawSub {
		t.Errorf("expected sub-rule entry, got %+v", got)
	}
}

func TestAgentsMD_FindsInCwd(t *testing.T) {
	dir := t.TempDir()
	// Make dir a git root so the walker stops here.
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("prefer go test -race"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadProjectInstructions(LoadInstructionsInput{Cwd: dir, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "prefer go test -race") {
		t.Errorf("missing content, got:\n%s", got)
	}
}

func TestAgentsMD_WalksAncestors(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	subA := filepath.Join(dir, "a")
	subB := filepath.Join(subA, "b")
	if err := os.MkdirAll(subB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root-rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subA, "AGENTS.md"), []byte("middle-rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadProjectInstructions(LoadInstructionsInput{Cwd: subB, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "root-rule") {
		t.Errorf("missing root-rule, got:\n%s", got)
	}
	if !strings.Contains(got, "middle-rule") {
		t.Errorf("missing middle-rule, got:\n%s", got)
	}
}

func TestAgentsMD_StopsAtGitBoundary(t *testing.T) {
	outer := t.TempDir()
	// outer has its own AGENTS.md — should NOT be picked up because we stop at inner/.git.
	if err := os.WriteFile(filepath.Join(outer, "AGENTS.md"), []byte("outer-rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	// inner is a git root.
	if err := os.Mkdir(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "AGENTS.md"), []byte("inner-rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadProjectInstructions(LoadInstructionsInput{Cwd: inner, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "inner-rule") {
		t.Errorf("missing inner-rule, got:\n%s", got)
	}
	if strings.Contains(got, "outer-rule") {
		t.Errorf("outer-rule should be excluded (above git boundary), got:\n%s", got)
	}
}

func TestAgentsMD_EmptyWhenNoneFound(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	got, err := LoadProjectInstructions(LoadInstructionsInput{Cwd: dir, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestAgentsMD_GlobalFallbackAppended(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o755)

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".prompto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".prompto", "AGENTS.md"), []byte("global-rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadProjectInstructions(LoadInstructionsInput{Cwd: dir, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "global-rule") {
		t.Errorf("missing global-rule, got:\n%s", got)
	}
}

func TestAgentsMD_OrderingDeepestLast(t *testing.T) {
	outer := t.TempDir()
	_ = os.Mkdir(filepath.Join(outer, ".git"), 0o755)
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "AGENTS.md"), []byte("outer-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "AGENTS.md"), []byte("inner-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadProjectInstructions(LoadInstructionsInput{Cwd: inner, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	outerPos := strings.Index(got, "outer-content")
	innerPos := strings.Index(got, "inner-content")
	if outerPos < 0 || innerPos < 0 {
		t.Fatalf("missing content, got:\n%s", got)
	}
	if outerPos >= innerPos {
		t.Errorf("expected outer before inner (deepest last), outer=%d inner=%d", outerPos, innerPos)
	}
}

func TestAgentsMD_CustomFilenames(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude-rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadProjectInstructions(LoadInstructionsInput{
		Cwd:       dir,
		Filenames: []string{"AGENTS.md", "CLAUDE.md"},
		HomeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "claude-rule") {
		t.Errorf("missing claude-rule, got:\n%s", got)
	}
}
