package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSpill_WritesContentAddressedFile(t *testing.T) {
	dir := t.TempDir()
	path, err := Spill(SpillInput{Cwd: dir, Content: "hello world"})
	if err != nil {
		t.Fatalf("Spill: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("spill content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("spill file stat: %v", err)
	}
	// Windows doesn't honor Unix permission bits — os.Chmod only
	// flips the read-only flag, so files always read back as 0666.
	// The hardening guarantee on Windows is "private to the user
	// profile via NTFS ACLs," not "0600 mode bits." On POSIX the
	// 0600 contract still holds.
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("spill file mode = %o, want 0600", got)
		}
	}
	if filepath.Dir(path) != filepath.Join(dir, spillDirName) {
		t.Errorf("spill dir = %q", filepath.Dir(path))
	}
}

func TestSpill_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	// .prompto/tmp does not exist yet.
	_, err := Spill(SpillInput{Cwd: dir, Content: "x"})
	if err != nil {
		t.Fatalf("Spill: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, spillDirName))
	if err != nil {
		t.Fatalf("spill dir stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("spill target is not a directory")
	}
	// See note above — directory mode bits aren't enforced on Windows.
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("spill dir mode = %o, want 0700", got)
		}
	}
}

func TestSpill_IdempotentForIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	content := "abc"
	p1, err := Spill(SpillInput{Cwd: dir, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Spill(SpillInput{Cwd: dir, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Errorf("paths differ: %q vs %q", p1, p2)
	}
}

func TestSpill_UniquePathForDifferentContent(t *testing.T) {
	dir := t.TempDir()
	p1, err := Spill(SpillInput{Cwd: dir, Content: "one"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Spill(SpillInput{Cwd: dir, Content: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Errorf("distinct content should map to distinct paths; got %q twice", p1)
	}
}

func TestSpill_RequiresCwd(t *testing.T) {
	_, err := Spill(SpillInput{Cwd: "", Content: "x"})
	if err == nil {
		t.Error("expected error when Cwd is empty")
	}
}
