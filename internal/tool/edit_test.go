package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

// captureSink records every event it sees for assertions.
type captureSink struct {
	events []agent.FileChangeEvent
}

func (s *captureSink) Record(_ context.Context, ev agent.FileChangeEvent) error {
	s.events = append(s.events, ev)
	return nil
}

// seedRead simulates a prior Read call by recording the file in FileState.
// Required before Edit/Write on an existing file.
func seedRead(t *testing.T, tc agent.ToolContext, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("seedRead stat: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("seedRead read: %v", err)
	}
	tc.FileState.Put(path, info.ModTime(), data)
}

func TestEditToolBasicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "fmt.Println(\"hello\")",
		NewString: "fmt.Println(\"world\")",
	})
	result, err := et.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "Successfully edited") {
		t.Errorf("result = %q", result.Content)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "\"world\"") {
		t.Errorf("file content = %q, expected replaced string", data)
	}
}

func TestEditToolRejectsWhenNotRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte("func main() {}\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "main",
		NewString: "start",
	})
	// No seedRead → ErrReadBeforeWrite.
	_, err := et.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for edit without prior read")
	}
	if !strings.Contains(err.Error(), "read the file first") {
		t.Errorf("error = %q, expected read-before-write guidance", err.Error())
	}
}

func TestEditToolNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	if err := os.WriteFile(path, []byte("func main() {}\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "does not exist",
		NewString: "replacement",
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for not-found string")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, expected 'not found'", err.Error())
	}
	if !strings.Contains(err.Error(), "read tool") {
		t.Errorf("error = %q, expected guidance to use read tool", err.Error())
	}
}

func TestEditToolMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	mustWrite(t, path, "foo\nfoo\nfoo\n")

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "foo",
		NewString: "bar",
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(err.Error(), "3 times") {
		t.Errorf("error = %q, expected count", err.Error())
	}
	if !strings.Contains(err.Error(), "more surrounding context") {
		t.Errorf("error = %q, expected guidance", err.Error())
	}
}

func TestEditToolEmptyOldString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	mustWrite(t, path, "content\n")

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "",
		NewString: "new",
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for empty old_string")
	}
	if !strings.Contains(err.Error(), "write tool") {
		t.Errorf("error = %q, expected redirect to write tool", err.Error())
	}
}

func TestEditToolDeleteText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	mustWrite(t, path, "keep this\ndelete this\nkeep this too\n")

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "delete this\n",
		NewString: "",
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "delete this") {
		t.Error("deleted text still present")
	}
	if !strings.Contains(string(data), "keep this") {
		t.Error("kept text missing")
	}
}

func TestEditToolPreservesPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows doesn't honor Unix permission bits on regular
		// files — os.Chmod only twiddles the read-only flag, so a
		// 0755 round-trip reads back as 0666. The behavior we want
		// to assert (edit doesn't drop the executable bit) only
		// has meaning on POSIX filesystems.
		t.Skip("permission-bit preservation is POSIX-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\necho hello\n"), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "echo hello",
		NewString: "echo world",
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0755 {
		t.Errorf("permissions = %o, want 0755", info.Mode().Perm())
	}
}

func TestEditToolMultilineReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	mustWrite(t, path, "func foo() {\n\ta := 1\n\tb := 2\n}\n")

	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "\ta := 1\n\tb := 2",
		NewString: "\tc := 3",
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "c := 3") {
		t.Error("multiline replacement failed")
	}
	if strings.Contains(string(data), "a := 1") {
		t.Error("old multiline text still present")
	}
}

func TestEditToolMissingFile(t *testing.T) {
	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  "/nonexistent/file.go",
		OldString: "foo",
		NewString: "bar",
	})
	_, err := et.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEditTool_EmitsFileChangeEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	mustWrite(t, path, "old body")

	sink := &captureSink{}
	tc := newTestCtx(t)
	tc.MessageID = "msg_1"
	tc.ToolCallID = "tc_1"
	tc.FileChanges = sink
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "old body",
		NewString: "new body",
	})
	if _, err := et.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Op != "modify" {
		t.Errorf("Op = %q, want modify", ev.Op)
	}
	if ev.MessageID != "msg_1" || ev.ToolCallID != "tc_1" {
		t.Errorf("attribution lost: %+v", ev)
	}
	if !bytes.Equal(ev.ContentBefore, []byte("old body")) {
		t.Errorf("ContentBefore = %q", ev.ContentBefore)
	}
	if !bytes.Equal(ev.ContentAfter, []byte("new body")) {
		t.Errorf("ContentAfter = %q", ev.ContentAfter)
	}
}

func TestEditToolDefinitionSchema(t *testing.T) {
	et := NewEditTool()
	def := et.Definition()
	if def.Name != "edit" {
		t.Errorf("Name = %q", def.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	props := schema["properties"].(map[string]any)
	for _, field := range []string{"file_path", "old_string", "new_string", "edits"} {
		if _, ok := props[field]; !ok {
			t.Errorf("schema missing %s property", field)
		}
	}
	// old_string and new_string must NOT be required at the
	// schema level — their required-ness is conditional on whether
	// edits[] is set, validated at the Go level.
	if required, ok := schema["required"].([]any); ok {
		for _, r := range required {
			name, _ := r.(string)
			if name == "old_string" || name == "new_string" {
				t.Errorf("schema marks %q as required; should be optional (validated at the Go level)", name)
			}
		}
	}
}

// --- Batch-form (edits[]) tests ------------------------------------------

func TestEditTool_BatchHappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	original := "import \"a\"\n\nfunc main() {\n\tx := 1\n}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath: path,
		Edits: []EditOp{
			{OldString: "\"a\"", NewString: "\"a\"\nimport \"b\""},
			{OldString: "x := 1", NewString: "x := 2"},
		},
	})
	res, err := et.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := os.ReadFile(path)
	want := "import \"a\"\nimport \"b\"\n\nfunc main() {\n\tx := 2\n}\n"
	if string(got) != want {
		t.Errorf("file content mismatch\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(res.DisplaySummary, "2 edits") {
		t.Errorf("DisplaySummary = %q, want it to mention '2 edits'", res.DisplaySummary)
	}
}

func TestEditTool_BatchOrderIndependence(t *testing.T) {
	// Same set of edits in two different input orders must produce
	// identical files (apply pass uses document order).
	original := "alpha\nbeta\ngamma\n"
	makeFile := func() string {
		dir := t.TempDir()
		path := filepath.Join(dir, "f.txt")
		if err := os.WriteFile(path, []byte(original), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}
	apply := func(ops []EditOp) string {
		t.Helper()
		path := makeFile()
		tc := newTestCtx(t)
		seedRead(t, tc, path)
		et := NewEditTool()
		input, _ := json.Marshal(EditInput{FilePath: path, Edits: ops})
		if _, err := et.Execute(t.Context(), tc, input); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		data, _ := os.ReadFile(path)
		return string(data)
	}

	a := apply([]EditOp{
		{OldString: "alpha", NewString: "ALPHA"},
		{OldString: "gamma", NewString: "GAMMA"},
	})
	b := apply([]EditOp{
		{OldString: "gamma", NewString: "GAMMA"},
		{OldString: "alpha", NewString: "ALPHA"},
	})
	if a != b {
		t.Errorf("order-dependence: got two different outputs\n a=%q\n b=%q", a, b)
	}
	if !strings.Contains(a, "ALPHA") || !strings.Contains(a, "GAMMA") {
		t.Errorf("expected both edits applied, got %q", a)
	}
}

func TestEditTool_BatchRejectsMissingMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath: path,
		Edits: []EditOp{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "does-not-exist", NewString: "x"},
		},
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for missing match")
	}
	if !strings.Contains(err.Error(), "edits[1]") {
		t.Errorf("error %q should cite the failing edit index", err.Error())
	}
	// File must be unchanged on failure (atomic rejection).
	got, _ := os.ReadFile(path)
	if string(got) != "alpha\n" {
		t.Errorf("file was modified on failed batch: %q", got)
	}
}

func TestEditTool_BatchRejectsDuplicateMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	// "foo" appears twice — ambiguous match.
	if err := os.WriteFile(path, []byte("foo bar foo\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath: path,
		Edits: []EditOp{
			{OldString: "foo", NewString: "X"},
		},
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for non-unique match")
	}
	if !strings.Contains(err.Error(), "appears 2 times") {
		t.Errorf("error %q should cite the count", err.Error())
	}
}

func TestEditTool_BatchRejectsOverlap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("the quick brown fox\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	// Two edits whose match regions overlap on "quick brown".
	input, _ := json.Marshal(EditInput{
		FilePath: path,
		Edits: []EditOp{
			{OldString: "quick brown", NewString: "fast tan"},
			{OldString: "brown fox", NewString: "tan dog"},
		},
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for overlapping edits")
	}
	if !strings.Contains(err.Error(), "overlap") {
		t.Errorf("error %q should mention overlap", err.Error())
	}
	got, _ := os.ReadFile(path)
	if string(got) != "the quick brown fox\n" {
		t.Errorf("file was modified on failed batch: %q", got)
	}
}

func TestEditTool_BatchRejectsIdenticalOldString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("foo\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	// Two edits with byte-identical old_string — ambiguous binding.
	input, _ := json.Marshal(EditInput{
		FilePath: path,
		Edits: []EditOp{
			{OldString: "foo", NewString: "X"},
			{OldString: "foo", NewString: "Y"},
		},
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for identical old_string in two edits")
	}
	if !strings.Contains(err.Error(), "byte-identical") {
		t.Errorf("error %q should mention byte-identical", err.Error())
	}
}

func TestEditTool_RejectsMixedForm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("foo bar\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	// Both single-form fields AND edits[] set.
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "foo",
		NewString: "X",
		Edits:     []EditOp{{OldString: "bar", NewString: "Y"}},
	})
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for mixed form")
	}
	if !strings.Contains(err.Error(), "either") || !strings.Contains(err.Error(), "edits[]") {
		t.Errorf("error %q should explain the mixed-form rejection", err.Error())
	}
}

func TestEditTool_RejectsEmptyInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("foo\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{FilePath: path}) // no old_string, no edits
	_, err := et.Execute(t.Context(), tc, input)
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "old_string") && !strings.Contains(err.Error(), "edits[]") {
		t.Errorf("error %q should mention the required forms", err.Error())
	}
}

func TestEditTool_BatchEmitsSingleFileChangeEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sink := &captureSink{}
	tc := newTestCtx(t)
	tc.MessageID = "m1"
	tc.ToolCallID = "tc1"
	tc.FileChanges = sink
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath: path,
		Edits: []EditOp{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "beta", NewString: "BETA"},
		},
	})
	if _, err := et.Execute(t.Context(), tc, input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1 (one event per batch)", len(sink.events))
	}
	ev := sink.events[0]
	if !bytes.Equal(ev.ContentBefore, []byte("alpha\nbeta\n")) {
		t.Errorf("ContentBefore = %q", ev.ContentBefore)
	}
	if !bytes.Equal(ev.ContentAfter, []byte("ALPHA\nBETA\n")) {
		t.Errorf("ContentAfter = %q", ev.ContentAfter)
	}
}

func TestEditTool_SingleFormDisplaySummaryUnchanged(t *testing.T) {
	// Single-form display output stays unchanged; only batch
	// (>1 edits) gets the "N edits" suffix.
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("foo\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tc := newTestCtx(t)
	seedRead(t, tc, path)

	et := NewEditTool()
	input, _ := json.Marshal(EditInput{
		FilePath:  path,
		OldString: "foo",
		NewString: "bar",
	})
	res, err := et.Execute(t.Context(), tc, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(res.DisplaySummary, "edits") {
		t.Errorf("single-form summary should not mention 'edits', got %q", res.DisplaySummary)
	}
}

// TestNfkcPrefixEqual_BasicMatch covers the simple case where haystack
// and target are byte-identical. The function should return the byte
// length of the matched prefix.
func TestNfkcPrefixEqual_BasicMatch(t *testing.T) {
	got, ok := nfkcPrefixEqual("hello world", "hello")
	if !ok || got != 5 {
		t.Errorf("nfkcPrefixEqual(\"hello world\", \"hello\") = (%d, %v), want (5, true)", got, ok)
	}
}

// TestNfkcPrefixEqual_NFKCFold covers the original motivation for the
// function: a Unicode-equivalent variant in haystack matches the
// normalized target. U+FF21 (FULLWIDTH LATIN CAPITAL LETTER A) folds
// to the ASCII "A" under NFKC.
func TestNfkcPrefixEqual_NFKCFold(t *testing.T) {
	haystack := "ＡBC123" // "ABC123" in fullwidth A
	got, ok := nfkcPrefixEqual(haystack, "ABC")
	if !ok {
		t.Fatalf("nfkcPrefixEqual fullwidth fold = (%d, false), want match", got)
	}
	// Consumed bytes in haystack: 3 (U+FF21) + 1 ('B') + 1 ('C') = 5.
	if got != 5 {
		t.Errorf("matched %d bytes, want 5 (U+FF21 is 3 bytes, B and C are 1 each)", got)
	}
}

// TestNfkcPrefixEqual_NoMatch verifies the negative path returns false.
func TestNfkcPrefixEqual_NoMatch(t *testing.T) {
	if _, ok := nfkcPrefixEqual("hello", "world"); ok {
		t.Error("expected false for disjoint strings")
	}
}

// TestNfkcPrefixEqual_HaystackTooShort handles the case where haystack
// runs out before the normalized buffer reaches len(target).
func TestNfkcPrefixEqual_HaystackTooShort(t *testing.T) {
	if _, ok := nfkcPrefixEqual("hi", "hello"); ok {
		t.Error("expected false when haystack shorter than target")
	}
}

// BenchmarkNfkcPrefixEqual measures the hot path the audit flagged:
// repeatedly running nfkcPrefixEqual against a long haystack with
// no allocations per call after the rewrite.
func BenchmarkNfkcPrefixEqual(b *testing.B) {
	// Worst-ish case: 4KB of haystack, ~32-byte target that doesn't match
	// at the first position so the function exhausts its threshold.
	haystack := strings.Repeat("the quick brown fox jumps over the lazy dog ", 100)
	target := "the quick brown fox jumps over"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = nfkcPrefixEqual(haystack, target)
	}
}
