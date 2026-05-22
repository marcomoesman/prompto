package tool

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepToolBasicMatch(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	gt := NewGrepTool()
	input, _ := json.Marshal(GrepInput{Pattern: "Println", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "Println") {
		t.Errorf("result = %q, expected match", result.Content)
	}
	if !strings.Contains(result.Content, "main.go") {
		t.Errorf("result = %q, expected file name", result.Content)
	}
}

func TestGrepToolRegex(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "test.go"), "func TestFoo(t *testing.T) {\n}\nfunc TestBar(t *testing.T) {\n}\n")

	gt := NewGrepTool()
	input, _ := json.Marshal(GrepInput{Pattern: `func\s+Test\w+`, Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "TestFoo") {
		t.Errorf("result missing TestFoo")
	}
	if !strings.Contains(result.Content, "TestBar") {
		t.Errorf("result missing TestBar")
	}
}

func TestGrepToolNoMatch(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n")

	gt := NewGrepTool()
	input, _ := json.Marshal(GrepInput{Pattern: "nonexistent_string_xyz", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Content, "No matches found") {
		t.Errorf("result = %q, expected no-match message", result.Content)
	}
}

func TestGrepToolIncludeFilter(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "main.go"), "hello world\n")
	mustWrite(t, filepath.Join(dir, "readme.md"), "hello world\n")

	gt := NewGrepTool()
	input, _ := json.Marshal(GrepInput{Pattern: "hello", Include: "*.go", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "main.go") {
		t.Errorf("result missing main.go match")
	}
	if strings.Contains(result.Content, "readme.md") {
		t.Error("readme.md should be excluded by include filter")
	}
}

func TestGrepToolMaxResults(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))

	var content strings.Builder
	for i := range 50 {
		fmt.Fprintf(&content, "match line %d\n", i)
	}
	mustWrite(t, filepath.Join(dir, "data.txt"), content.String())

	gt := NewGrepTool()
	input, _ := json.Marshal(GrepInput{Pattern: "match", Path: dir, MaxResults: 5})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(result.Content), "\n")
	matchLines := 0
	for _, line := range lines {
		if strings.Contains(line, "match line") {
			matchLines++
		}
	}

	if matchLines > 5 {
		t.Errorf("got %d match lines, expected at most 5", matchLines)
	}
}

func TestGrepToolEmptyPattern(t *testing.T) {
	gt := NewGrepTool()
	input, _ := json.Marshal(GrepInput{Pattern: ""})
	_, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestGrepToolDefinitionSchema(t *testing.T) {
	gt := NewGrepTool()
	def := gt.Definition()
	if def.Name != "grep" {
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

// TestGrepToolGoFallback tests the pure Go fallback path directly.
func TestGrepToolGoFallback(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nfunc hello() {\n}\n")
	mustWrite(t, filepath.Join(dir, "readme.md"), "# hello\n")

	params := GrepInput{
		Pattern:    "hello",
		Path:       dir,
		MaxResults: 100,
	}

	result, err := grepWithGo(t.Context(), params)
	if err != nil {
		t.Fatalf("grepWithGo: %v", err)
	}

	if !strings.Contains(result.Content, "main.go") {
		t.Errorf("result missing main.go")
	}
	if !strings.Contains(result.Content, "readme.md") {
		t.Errorf("result missing readme.md")
	}
}

func TestGrepToolGoFallbackInvalidRegex(t *testing.T) {
	params := GrepInput{
		Pattern:    "[invalid",
		Path:       t.TempDir(),
		MaxResults: 100,
	}

	_, err := grepWithGo(t.Context(), params)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
	if !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("error = %q, expected invalid regex mention", err.Error())
	}
}

func TestGrepToolGoFallbackSkipsBinary(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))
	mustWrite(t, filepath.Join(dir, "binary.bin"), "\x00\x01hello")
	mustWrite(t, filepath.Join(dir, "text.txt"), "hello world\n")

	params := GrepInput{
		Pattern:    "hello",
		Path:       dir,
		MaxResults: 100,
	}

	result, err := grepWithGo(t.Context(), params)
	if err != nil {
		t.Fatalf("grepWithGo: %v", err)
	}

	if strings.Contains(result.Content, "binary.bin") {
		t.Error("binary file should be skipped")
	}
	if !strings.Contains(result.Content, "text.txt") {
		t.Errorf("text file should be found, got: %s", result.Content)
	}
}

// TestGrepToolHandlesLongLine guards against the bufio.Scanner default
// 64KB-per-line cap that used to silently drop matches in the rest of a
// file once a single long line was hit (minified bundles, JSON dumps).
// With the larger Buffer cap the match must still surface.
func TestGrepToolHandlesLongLine(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, ".git"))

	// 200KB filler + sentinel + newline + a normal line that should also
	// match. If the scanner buffer regresses to 64KB, Scan returns false
	// after the long line and the second match is dropped.
	long := strings.Repeat("x", 200*1024)
	body := long + "NEEDLE_HEAD\n" + "second line NEEDLE_TAIL\n"
	mustWrite(t, filepath.Join(dir, "long.txt"), body)

	gt := NewGrepTool()
	input, _ := json.Marshal(GrepInput{Pattern: "NEEDLE_TAIL", Path: dir})
	result, err := gt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "NEEDLE_TAIL") {
		t.Errorf("post-long-line match was dropped — scanner buffer regressed.\nresult = %q", result.Content)
	}
}
