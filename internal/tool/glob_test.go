package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGlobToolBasicPattern(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "main.go"), "package main")
	mustWrite(t, filepath.Join(dir, "readme.md"), "# readme")

	gt := NewGlobTool()
	input, _ := json.Marshal(GlobInput{Pattern: "*.go", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "main.go") {
		t.Errorf("result = %q, expected main.go", result.Content)
	}
	if strings.Contains(result.Content, "readme.md") {
		t.Error("should not contain readme.md")
	}
}

func TestGlobToolRecursive(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustMkdirAll(t, filepath.Join(dir, "internal", "tool"))
	mustWrite(t, filepath.Join(dir, "main.go"), "package main")
	mustWrite(t, filepath.Join(dir, "internal", "tool", "edit.go"), "package tool")

	gt := NewGlobTool()
	input, _ := json.Marshal(GlobInput{Pattern: "**/*.go", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "main.go") {
		t.Errorf("result missing main.go")
	}
	if !strings.Contains(result.Content, filepath.Join("internal", "tool", "edit.go")) {
		t.Errorf("result missing internal/tool/edit.go, got:\n%s", result.Content)
	}
}

func TestGlobToolSortedByModTime(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))

	// Create files with different mod times.
	old := filepath.Join(dir, "old.go")
	new := filepath.Join(dir, "new.go")
	mustWrite(t, old, "old")
	if err := os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	mustWrite(t, new, "new")

	gt := NewGlobTool()
	input, _ := json.Marshal(GlobInput{Pattern: "*.go", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(result.Content), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), result.Content)
	}
	if lines[0] != "new.go" {
		t.Errorf("first result = %q, want new.go (most recent)", lines[0])
	}
}

func TestGlobToolRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package main")
	mustWrite(t, filepath.Join(dir, "debug.log"), "log data")

	gt := NewGlobTool()
	input, _ := json.Marshal(GlobInput{Pattern: "*", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.Contains(result.Content, "debug.log") {
		t.Error("gitignored file debug.log should not appear")
	}
	if !strings.Contains(result.Content, "main.go") {
		t.Error("main.go should appear")
	}
}

func TestGlobToolNoMatches(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "readme.md"), "# hi")

	gt := NewGlobTool()
	input, _ := json.Marshal(GlobInput{Pattern: "*.go", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "No files found") {
		t.Errorf("result = %q, expected no-match message", result.Content)
	}
}

func TestGlobToolEmptyPattern(t *testing.T) {
	gt := NewGlobTool()
	input, _ := json.Marshal(GlobInput{Pattern: ""})
	_, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestGlobToolNonexistentPath(t *testing.T) {
	gt := NewGlobTool()
	input, _ := json.Marshal(GlobInput{Pattern: "*.go", Path: "/nonexistent/path"})
	_, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestGlobToolDefinitionSchema(t *testing.T) {
	gt := NewGlobTool()
	def := gt.Definition()
	if def.Name != "glob" {
		t.Errorf("Name = %q", def.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["pattern"]; !ok {
		t.Error("schema missing pattern property")
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "readme.md", false},
		{"**/*.go", "main.go", true},
		{"**/*.go", "internal/tool/edit.go", true},
		{"internal/**/*.go", "internal/tool/edit.go", true},
		{"internal/**/*.go", "cmd/main.go", false},
		{"internal/**", "internal/tool/edit.go", true},
		{"**/*.go", "readme.md", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}
