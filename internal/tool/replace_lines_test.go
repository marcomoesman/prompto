package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReplaceLinesSingleLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	mustWrite(t, path, "one\ntwo\nthree\n")
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	res, err := NewReplaceLinesTool().Execute(t.Context(), tc, replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   2,
		EndLine:     2,
		Replacement: "TWO\n",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "Successfully replaced lines 2-2") {
		t.Fatalf("result = %q", res.Content)
	}
	assertFile(t, path, "one\nTWO\nthree\n")
}

func TestReplaceLinesMultiLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	mustWrite(t, path, "one\ntwo\nthree\nfour\n")
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	_, err := NewReplaceLinesTool().Execute(t.Context(), tc, replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   2,
		EndLine:     3,
		Replacement: "TWO\nTHREE\n",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertFile(t, path, "one\nTWO\nTHREE\nfour\n")
}

func TestReplaceLinesFullFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	mustWrite(t, path, "one\ntwo\n")
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	_, err := NewReplaceLinesTool().Execute(t.Context(), tc, replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   1,
		EndLine:     2,
		Replacement: "all\nnew\n",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertFile(t, path, "all\nnew\n")
}

func TestReplaceLinesPreservesEOFShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	mustWrite(t, path, "one\ntwo")
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	_, err := NewReplaceLinesTool().Execute(t.Context(), tc, replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   2,
		EndLine:     2,
		Replacement: "TWO",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertFile(t, path, "one\nTWO")
}

func TestReplaceLinesRejectsUnreadStaleAndOutOfRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	mustWrite(t, path, "one\ntwo\n")
	tool := NewReplaceLinesTool()

	_, err := tool.Execute(t.Context(), newTestCtx(t), replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   1,
		EndLine:     1,
		Replacement: "ONE\n",
	}))
	if err == nil || !strings.Contains(err.Error(), "read the file first") {
		t.Fatalf("unread err = %v, want read-before-write guidance", err)
	}

	tc := newTestCtx(t)
	seedRead(t, tc, path)
	mustWrite(t, path, "changed\n")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	_, err = tool.Execute(t.Context(), tc, replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   1,
		EndLine:     1,
		Replacement: "ONE\n",
	}))
	if err == nil || !strings.Contains(err.Error(), "changed since last read") {
		t.Fatalf("stale err = %v, want stale file error", err)
	}

	tc = newTestCtx(t)
	seedRead(t, tc, path)
	_, err = tool.Execute(t.Context(), tc, replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   2,
		EndLine:     2,
		Replacement: "TWO\n",
	}))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "beyond eof") {
		t.Fatalf("out-of-range err = %v, want beyond EOF", err)
	}
}

func TestReplaceLinesRejectsInvalidRanges(t *testing.T) {
	tool := NewReplaceLinesTool()
	for _, input := range []ReplaceLinesInput{
		{FilePath: "x", StartLine: 0, EndLine: 1, Replacement: "x"},
		{FilePath: "x", StartLine: 2, EndLine: 1, Replacement: "x"},
	} {
		_, err := tool.Execute(t.Context(), newTestCtx(t), replaceLinesInput(t, input))
		if err == nil {
			t.Fatalf("Execute(%+v) returned nil, want range error", input)
		}
	}
}

func TestReplaceLinesPreservesPermissionsAndRecordsChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows doesn't honor Unix permission bits on regular
		// files — os.Chmod only twiddles the read-only flag, so a
		// 0755 round-trip reads back as 0666. The behavior we want
		// to assert (replace_lines doesn't drop the executable bit)
		// only has meaning on POSIX filesystems.
		t.Skip("permission-bit preservation is POSIX-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("echo one\necho two\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	sink := &captureSink{}
	tc := newTestCtx(t)
	tc.MessageID = "msg"
	tc.ToolCallID = "call"
	tc.FileChanges = sink
	seedRead(t, tc, path)

	_, err := NewReplaceLinesTool().Execute(t.Context(), tc, replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    path,
		StartLine:   2,
		EndLine:     2,
		Replacement: "echo TWO\n",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if sink.events[0].Path != resolvedPath || sink.events[0].Op != "modify" {
		t.Fatalf("event = %+v", sink.events[0])
	}
	if err := tc.FileState.Check(resolvedPath); err != nil {
		t.Fatalf("FileState not refreshed: %v", err)
	}
}

func TestReplaceLinesPermissionKeyAndDisplay(t *testing.T) {
	input := replaceLinesInput(t, ReplaceLinesInput{
		FilePath:    "foo.go",
		StartLine:   3,
		EndLine:     5,
		Replacement: "x\n",
	})
	tool := NewReplaceLinesTool()
	if got := tool.PermissionKey(input); got != "foo.go" {
		t.Fatalf("PermissionKey = %q, want foo.go", got)
	}
	if got := tool.FormatForDisplay(input); !strings.Contains(got, "ReplaceLines") || !strings.Contains(got, "foo.go") {
		t.Fatalf("FormatForDisplay = %q", got)
	}
}

func replaceLinesInput(t *testing.T, in ReplaceLinesInput) []byte {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
}
