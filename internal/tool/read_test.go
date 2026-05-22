package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTool_FormatForDisplay_AbsoluteFallback(t *testing.T) {
	// Without context (no cwd), FormatForDisplay must preserve the raw
	// absolute path. This is the form persisted in tests / golden files.
	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: "/abs/path/main.go"})
	got := rt.FormatForDisplay(input)
	if !strings.Contains(got, "/abs/path/main.go") {
		t.Errorf("absolute path lost: %q", got)
	}
}

func TestReadTool_FormatForDisplayWithContext_Relativises(t *testing.T) {
	rt := NewReadTool()
	tc := newTestCtx(t)
	abs := filepath.Join(tc.Cwd, "internal", "tui", "env.go")
	input, _ := json.Marshal(ReadInput{FilePath: abs})

	got := rt.FormatForDisplayWithContext(input, tc)
	if !strings.Contains(got, "internal/tui/env.go") {
		t.Errorf("relative form missing: %q", got)
	}
	if strings.Contains(got, tc.Cwd) {
		t.Errorf("cwd prefix leaked into display: %q", got)
	}
}

func TestReadToolBasicFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	mustWrite(t, path, "line one\nline two\nline three\n")

	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: path})
	result, err := rt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "1\tline one") {
		t.Errorf("missing line 1, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "3\tline three") {
		t.Errorf("missing line 3, got:\n%s", result.Content)
	}
}

func TestReadToolOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	var lines []string
	for i := range 10 {
		lines = append(lines, strings.Repeat("x", i+1))
	}
	mustWrite(t, path, strings.Join(lines, "\n")+"\n")

	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: path, Offset: 5, Limit: 3})
	result, err := rt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "6\t") {
		t.Errorf("missing line 6")
	}
	if !strings.Contains(result.Content, "8\t") {
		t.Errorf("missing line 8")
	}
	if strings.Contains(result.Content, "9\t") {
		t.Error("should not contain line 9")
	}
}

func TestReadToolBinaryDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	mustWrite(t, path, "\x00\x01\x02\xFF")

	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: path})
	_, err := rt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for binary file")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("error = %q, expected mention of binary", err.Error())
	}
}

func TestReadToolMissingFile(t *testing.T) {
	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: "/nonexistent/file.txt"})
	_, err := rt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadToolSpillsLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	// Build a file that exceeds the 200 KB spill threshold with lots of lines
	// so the head preview is meaningful.
	var lines []string
	for i := range 5000 {
		lines = append(lines, fmt.Sprintf("line %d %s", i, strings.Repeat("x", 50)))
	}
	data := strings.Join(lines, "\n")
	if len(data) < 200*1024 {
		t.Fatalf("test setup: file too small (%d bytes)", len(data))
	}
	mustWrite(t, path, data)

	tc := newTestCtx(t)
	tc.Cwd = dir // spill writes to <cwd>/.prompto/tmp
	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: path})
	result, err := rt.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "Full file saved to") {
		t.Errorf("expected spill reference in result, got:\n%s", result.Content[:min(200, len(result.Content))])
	}
	// Spill file must exist.
	spillDir := filepath.Join(dir, ".prompto", "tmp")
	entries, err := os.ReadDir(spillDir)
	if err != nil {
		t.Fatalf("spill dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected spill file in .prompto/tmp")
	}
	// Reported bytes == full on-disk size.
	if result.Bytes < 200*1024 {
		t.Errorf("Result.Bytes = %d, expected >= 200KB", result.Bytes)
	}
}

func TestReadToolSmallFileNoSpill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.txt")
	mustWrite(t, path, "one\ntwo\nthree\n")

	tc := newTestCtx(t)
	tc.Cwd = dir
	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: path})
	result, err := rt.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(result.Content, "Full file saved to") {
		t.Error("small file should not spill")
	}
	if _, err := os.Stat(filepath.Join(dir, ".prompto", "tmp")); err == nil {
		t.Error("small file should not create .prompto/tmp directory")
	}
}

func TestReadToolEmptyPath(t *testing.T) {
	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{})
	_, err := rt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// TestReadToolQueuesNestedAgentsMD verifies the Phase-11 hook: reading a
// file under a subdirectory containing AGENTS.md should queue exactly one
// reminder per discovered file, even when the same path is read twice.
func TestReadToolQueuesNestedAgentsMD(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	mustMkdir(t, sub)

	agentsPath := filepath.Join(sub, "AGENTS.md")
	mustWrite(t, agentsPath, "use snake_case here")

	target := filepath.Join(sub, "file.go")
	mustWrite(t, target, "package main\n")

	var (
		queued []string
		seen   = make(map[string]struct{})
	)
	tc := newTestCtx(t)
	tc.AgentsMDLoadRoot = root
	tc.QueueReminder = func(text string) { queued = append(queued, text) }
	tc.HasSeenAgentsMD = func(p string) bool { _, ok := seen[p]; return ok }
	tc.MarkSeenAgentsMD = func(p string) { seen[p] = struct{}{} }

	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: target})

	if _, err := rt.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute first call: %v", err)
	}
	if len(queued) != 1 {
		t.Fatalf("first call queued %d reminders, want 1: %v", len(queued), queued)
	}
	// Path-only nudge: full content is intentionally NOT inlined to avoid
	// permanently inflating the conversation. The model can read the file
	// on demand if it needs the rules.
	if !strings.Contains(queued[0], "AGENTS.md") {
		t.Errorf("queued reminder missing AGENTS.md path: %q", queued[0])
	}
	if strings.Contains(queued[0], "use snake_case here") {
		t.Errorf("queued reminder unexpectedly inlines AGENTS.md content: %q", queued[0])
	}

	// Second read of the same target — must NOT re-queue.
	if _, err := rt.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute second call: %v", err)
	}
	if len(queued) != 1 {
		t.Errorf("second call re-queued reminder; total now %d", len(queued))
	}
}

// TestReadToolNoQueueWhenClosuresMissing covers subagents / tests: when
// any of the three closures is nil, the read tool must not panic and
// must not call into the agent helper.
func TestReadToolNoQueueWhenClosuresMissing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "x")
	target := filepath.Join(root, "file.go")
	mustWrite(t, target, "package main\n")

	tc := newTestCtx(t)
	tc.AgentsMDLoadRoot = root
	// closures left nil

	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: target})
	if _, err := rt.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestReadToolDefinitionSchema(t *testing.T) {
	rt := NewReadTool()
	def := rt.Definition()
	if def.Name != "read" {
		t.Errorf("Name = %q", def.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["file_path"]; !ok {
		t.Error("schema missing file_path property")
	}
}

// TestReadToolRefusesOversizedFile guards against the OOM path: without a
// pre-stat size gate, os.ReadFile would happily slurp a multi-GB file into
// memory before any size-aware code ran. The Stat-based check refuses with
// a clear error well below the cap.
func TestReadToolRefusesOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.bin")

	// Sparse file at readMaxFileSize+1 bytes — Truncate doesn't allocate
	// disk for it on common filesystems, so the test stays fast.
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.Truncate(int64(readMaxFileSize) + 1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	_ = f.Close()

	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: path})
	_, err = rt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected an error for oversized file, got nil — Read would have OOM'd on the real path")
	}
	if !strings.Contains(err.Error(), "read limit") {
		t.Errorf("error = %q, want one mentioning the read limit", err.Error())
	}
}

func TestReadToolPagedReadAllowsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.log")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.WriteString(strings.Repeat("a", 4096) + "\nsecond\n"); err != nil {
		t.Fatalf("write prefix: %v", err)
	}
	if err := f.Truncate(int64(readMaxFileSize) + 1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	_ = f.Close()

	rt := NewReadTool()
	input, _ := json.Marshal(ReadInput{FilePath: path, Offset: 1, Limit: 1})
	result, err := rt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "second") {
		t.Fatalf("content = %q, want requested line", result.Content)
	}
	if !strings.Contains(result.Content, "Partial large-file reads are for inspection only") {
		t.Fatalf("content missing large-file safety note: %q", result.Content)
	}
}
