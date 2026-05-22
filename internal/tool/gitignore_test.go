package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGitignoreFindsFile(t *testing.T) {
	dir := t.TempDir()

	// Create a fake git repo.
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(dir, ".gitignore"), "*.log\nbuild/\n")

	matcher := LoadGitignore(dir)
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}

	if !IsGitIgnored(matcher, "app.log") {
		t.Error("expected app.log to be ignored")
	}
	if !IsGitIgnored(matcher, "build/output") {
		t.Error("expected build/output to be ignored")
	}
	if IsGitIgnored(matcher, "main.go") {
		t.Error("main.go should not be ignored")
	}
}

func TestLoadGitignoreNoFile(t *testing.T) {
	dir := t.TempDir()
	// No .git, no .gitignore.
	matcher := LoadGitignore(dir)
	// Should still get a matcher (with .git hardcoded), but not nil.
	// .git is always ignored.
	if matcher == nil {
		t.Fatal("expected non-nil matcher even without .gitignore")
	}
	if !IsGitIgnored(matcher, ".git") {
		t.Error("expected .git to always be ignored")
	}
}

func TestIsGitIgnoredNilMatcher(t *testing.T) {
	if IsGitIgnored(nil, "anything") {
		t.Error("nil matcher should return false")
	}
}

func TestLoadGitignoreNestedGitignore(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Root gitignore.
	mustWrite(t, filepath.Join(dir, ".gitignore"), "*.log\n")

	// Nested directory with its own .gitignore.
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(sub, ".gitignore"), "*.tmp\n")

	matcher := LoadGitignore(sub)
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}

	// Root pattern should apply.
	if !IsGitIgnored(matcher, "foo.log") {
		t.Error("expected foo.log to be ignored by root pattern")
	}

	// Nested pattern should apply within subdir.
	if !IsGitIgnored(matcher, "subdir/test.tmp") {
		t.Error("expected subdir/test.tmp to be ignored by nested pattern")
	}

	// Non-matching file should not be ignored.
	if IsGitIgnored(matcher, "subdir/test.go") {
		t.Error("subdir/test.go should not be ignored")
	}
}

func TestLoadGitignoreNegationPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(dir, ".gitignore"), "*.log\n!important.log\n")

	matcher := LoadGitignore(dir)
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}

	if !IsGitIgnored(matcher, "debug.log") {
		t.Error("expected debug.log to be ignored")
	}
	if IsGitIgnored(matcher, "important.log") {
		t.Error("important.log should NOT be ignored (negation pattern)")
	}
}

func TestLoadGitignoreGitDirAlwaysIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// No .gitignore file at all — but .git should still be ignored.

	matcher := LoadGitignore(dir)
	if matcher == nil {
		t.Fatal("expected non-nil matcher")
	}
	if !IsGitIgnored(matcher, ".git") {
		t.Error("expected .git to be ignored even without .gitignore")
	}
	if !IsGitIgnored(matcher, ".git/config") {
		t.Error("expected .git/config to be ignored")
	}
}
