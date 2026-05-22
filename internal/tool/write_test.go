package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteToolNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "hello world"})
	result, err := wt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "Successfully wrote") {
		t.Errorf("result = %q, expected success message", result.Content)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", data, "hello world")
	}
}

func TestWriteToolCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "file.txt")

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "nested"})
	_, err := wt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "nested" {
		t.Errorf("file content = %q", data)
	}
}

func TestWriteToolOverwriteRequiresRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	mustWrite(t, path, "original")

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "replaced"})
	// No prior read → must reject overwrite.
	_, err := wt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error overwriting an existing file without prior read")
	}
	if !strings.Contains(err.Error(), "read the file first") {
		t.Errorf("error = %q, expected read-before-write guidance", err.Error())
	}

	// File content must be unchanged.
	data, _ := os.ReadFile(path)
	if string(data) != "original" {
		t.Errorf("file content = %q, want %q (unchanged)", data, "original")
	}
}

func TestWriteToolOverwriteAfterRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	mustWrite(t, path, "original")

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "replaced"})
	_, err := wt.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "replaced" {
		t.Errorf("file content = %q, want %q", data, "replaced")
	}
}

func TestWriteToolEmptyPath(t *testing.T) {
	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{Content: "hello"})
	_, err := wt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestWriteToolRelativePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: "relative/path.txt", Content: "hello"})
	tc := newTestCtx(t)
	tc.Cwd = dir // align ToolContext cwd with the chdir'd dir
	result, err := wt.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "Successfully wrote") {
		t.Errorf("result = %q, expected success message", result.Content)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "relative", "path.txt"))
	if string(data) != "hello" {
		t.Errorf("file content = %q, want %q", data, "hello")
	}
}

func TestWriteToolAllowsBenignDotDotInName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foo..bar.txt")
	tc := newTestCtx(t)
	tc.AllowedRoots = []string{dir}

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "ok"})
	if _, err := wt.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestWriteToolDeniesOutsideAllowedRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	tc := newTestCtx(t)
	tc.Cwd = root
	tc.AllowedRoots = []string{root}

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: filepath.Join(outside, "x.txt"), Content: "no"})
	if _, err := wt.Execute(t.Context(), tc, input); err == nil {
		t.Fatal("expected outside-root write to be denied")
	}
}

func TestWriteToolDeniesSymlinkToSensitiveFile(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, ".env")
	mustWrite(t, target, "SECRET=1\n")
	link := filepath.Join(root, "link.env")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tc := newTestCtx(t)
	tc.Cwd = root
	tc.AllowedRoots = []string{root}

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: link, Content: "SECRET=2\n"})
	if _, err := wt.Execute(t.Context(), tc, input); err == nil {
		t.Fatal("expected symlink to sensitive file to be denied")
	}
}

func TestWriteToolEmptyContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: ""})
	_, err := wt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v, empty content should be allowed", err)
	}

	data, _ := os.ReadFile(path)
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}
}

func TestWriteToolByteCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "12345"})
	result, err := wt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "5 bytes") {
		t.Errorf("result = %q, expected byte count", result.Content)
	}
}

func TestWriteTool_EmitsCreateEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.txt")

	sink := &captureSink{}
	tc := newTestCtx(t)
	tc.MessageID = "msg_create"
	tc.ToolCallID = "tc_create"
	tc.FileChanges = sink

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "hello"})
	if _, err := wt.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Op != "create" {
		t.Errorf("Op = %q, want create", ev.Op)
	}
	if ev.ContentBefore != nil {
		t.Errorf("ContentBefore = %q, want nil on create", ev.ContentBefore)
	}
	if string(ev.ContentAfter) != "hello" {
		t.Errorf("ContentAfter = %q", ev.ContentAfter)
	}
}

func TestWriteTool_EmitsModifyEventOnOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	_ = writeSeed(t, path, "original")

	sink := &captureSink{}
	tc := newTestCtx(t)
	tc.MessageID = "msg_mod"
	tc.ToolCallID = "tc_mod"
	tc.FileChanges = sink
	seedRead(t, tc, path)

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "replaced"})
	if _, err := wt.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Op != "modify" {
		t.Errorf("Op = %q, want modify", ev.Op)
	}
	if string(ev.ContentBefore) != "original" {
		t.Errorf("ContentBefore = %q", ev.ContentBefore)
	}
	if string(ev.ContentAfter) != "replaced" {
		t.Errorf("ContentAfter = %q", ev.ContentAfter)
	}
}

// TestWriteTool_PreservesModeOnOverwrite verifies that when an existing
// file is rewritten, its permission bits survive — including a tightly
// restricted 0o600 secret. Without this behaviour the tool would silently
// widen permissions on every overwrite, leaking previously-private files.
// Skipped on Windows where Unix mode bits are not enforced.
func TestWriteTool_PreservesModeOnOverwrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are advisory on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "replaced"})
	if _, err := wt.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0o600 (overwrite must preserve restrictive perms)", got)
	}
}

// TestWriteTool_NewFileUsesRestrictiveMode verifies that brand-new files
// land at 0o600 rather than the older 0o644 default — minimizing the
// surface where the agent silently creates world-readable files.
func TestWriteTool_NewFileUsesRestrictiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are advisory on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	wt := NewWriteTool()
	input, _ := json.Marshal(WriteInput{FilePath: path, Content: "hello"})
	if _, err := wt.Execute(t.Context(), newTestCtx(t), input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// umask may further restrict but never widen; assert the upper bound.
	if got := info.Mode().Perm(); got&^0o600 != 0 {
		t.Errorf("mode = %o, want bits no wider than 0o600", got)
	}
}

// writeSeed writes content to path and returns it, failing the test on error.
func writeSeed(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return content
}

func TestWriteToolDefinitionSchema(t *testing.T) {
	wt := NewWriteTool()
	def := wt.Definition()
	if def.Name != "write" {
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
	if _, ok := props["content"]; !ok {
		t.Error("schema missing content property")
	}
}
