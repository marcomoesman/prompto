package permission

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTraversal_RejectsNullByte(t *testing.T) {
	_, err := NormalizePath(NormalizePathInput{Input: "/tmp/foo\x00bar"})
	if !errors.Is(err, ErrNullByte) {
		t.Errorf("err = %v, want ErrNullByte", err)
	}
}

func TestTraversal_RejectsURLEncoded(t *testing.T) {
	cases := []string{
		"/home/%2e%2e/etc/passwd",
		"/home/%2E%2E/etc/passwd",
		"/home/user/%2fetc/passwd",
	}
	for _, in := range cases {
		_, err := NormalizePath(NormalizePathInput{Input: in})
		if !errors.Is(err, ErrURLEncoded) {
			t.Errorf("%s: err = %v, want ErrURLEncoded", in, err)
		}
	}
}

func TestTraversal_RejectsFullwidth(t *testing.T) {
	// U+FF0E fullwidth dot, U+FF0F fullwidth slash
	cases := []string{
		"/home/\uFF0E\uFF0E/etc/passwd",
		"/home\uFF0Fetc/passwd",
	}
	for _, in := range cases {
		_, err := NormalizePath(NormalizePathInput{Input: in})
		if !errors.Is(err, ErrFullwidth) {
			t.Errorf("%s: err = %v, want ErrFullwidth", in, err)
		}
	}
}

func TestTraversal_AcceptsWithinRoot(t *testing.T) {
	root := t.TempDir()
	// Resolve the root to its canonical form so we can compose an expected
	// value that survives macOS /var → /private/var symlinks.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "src", "main.go")
	expected := filepath.Join(resolvedRoot, "src", "main.go")

	got, err := NormalizePath(NormalizePathInput{
		Input:        sub,
		AllowedRoots: []string{root},
	})
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestTraversal_RejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := "/tmp/other-dir/evil"

	_, err := NormalizePath(NormalizePathInput{
		Input:        outside,
		AllowedRoots: []string{root},
	})
	if !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("err = %v, want ErrOutsideRoots", err)
	}
}

func TestTraversal_DoubleDotsCleanedAndRejectedWhenEscaping(t *testing.T) {
	root := t.TempDir()
	// A path that, after Clean, escapes the root.
	escape := filepath.Join(root, "..", "..", "elsewhere")

	_, err := NormalizePath(NormalizePathInput{
		Input:        escape,
		AllowedRoots: []string{root},
	})
	if !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("escape err = %v, want ErrOutsideRoots", err)
	}
}

func TestTraversal_NoAllowedRootsSkipsContainmentCheck(t *testing.T) {
	// With AllowedRoots empty, any absolute path normalizes through.
	// Use t.TempDir so the path actually exists; symlink resolution can then
	// produce a canonical form we can compare against.
	dir := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(dir)

	got, err := NormalizePath(NormalizePathInput{Input: dir})
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if got != resolved {
		t.Errorf("got %q, want %q", got, resolved)
	}
}

func TestTraversal_SymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()

	// Create a symlink INSIDE root that points OUTSIDE.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	_, err := NormalizePath(NormalizePathInput{
		Input:        filepath.Join(link, "file.txt"),
		AllowedRoots: []string{root},
	})
	if !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("symlink escape err = %v, want ErrOutsideRoots", err)
	}
}

func TestTraversal_NonExistentPathStillResolves(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	notYet := filepath.Join(root, "subdir", "not-created-yet.txt")
	expected := filepath.Join(resolvedRoot, "subdir", "not-created-yet.txt")

	got, err := NormalizePath(NormalizePathInput{
		Input:        notYet,
		AllowedRoots: []string{root},
	})
	if err != nil {
		t.Fatalf("NormalizePath: %v", err)
	}
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestTraversal_EmptyInputErrors(t *testing.T) {
	_, err := NormalizePath(NormalizePathInput{Input: ""})
	if !errors.Is(err, ErrRelativeEmpty) {
		t.Errorf("err = %v, want ErrRelativeEmpty", err)
	}
}
