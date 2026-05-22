package tool

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestBuildRipgrepArgs_IncludesMaxCount regresses a bug where the
// rg code path silently ignored params.MaxResults — only the Go
// fallback honored it. ripgrep would buffer the full unbounded
// match set before our 50KB post-process truncation kicked in.
func TestBuildRipgrepArgs_IncludesMaxCount(t *testing.T) {
	args := buildRipgrepArgs(GrepInput{
		Pattern:    "TODO",
		Path:       ".",
		MaxResults: 42,
	})

	wantPrefix := "--max-count="
	idx := slices.IndexFunc(args, func(a string) bool {
		return strings.HasPrefix(a, wantPrefix)
	})
	if idx < 0 {
		t.Fatalf("--max-count flag missing from rg args: %v", args)
	}
	got := strings.TrimPrefix(args[idx], wantPrefix)
	if n, err := strconv.Atoi(got); err != nil || n != 42 {
		t.Errorf("--max-count value = %q, want 42", got)
	}
}

func TestBuildRipgrepArgs_PassesPatternAndPathLast(t *testing.T) {
	args := buildRipgrepArgs(GrepInput{
		Pattern:    "TODO",
		Path:       "/some/path",
		Include:    "*.go",
		MaxResults: 100,
	})
	// Pattern + path are positional; rg requires them last (after flags).
	if len(args) < 2 {
		t.Fatalf("not enough args: %v", args)
	}
	if args[len(args)-2] != "TODO" || args[len(args)-1] != "/some/path" {
		t.Errorf("pattern/path should be last two args, got %v", args)
	}
	// --glob flag must precede pattern + path.
	globIdx := slices.Index(args, "--glob")
	if globIdx < 0 {
		t.Errorf("missing --glob flag for Include filter: %v", args)
	}
	if args[globIdx+1] != "*.go" {
		t.Errorf("--glob value = %q, want *.go", args[globIdx+1])
	}
}
