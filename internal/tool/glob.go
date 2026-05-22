package tool

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

const (
	globMaxResults = 200
	// globMaxBytes caps the rendered output regardless of result count.
	// Deeply nested repos can produce 200 paths whose combined length
	// exceeds the aggregator's per-tool cap; truncating here keeps the
	// "[truncated]" message readable instead of being chopped mid-line
	// by the central trimmer.
	globMaxBytes = 48 * 1024
)

// GlobInput defines the JSON parameters for the glob tool.
type GlobInput struct {
	Pattern string `json:"pattern" jsonschema:"required,description=Glob pattern to match (e.g. **/*.go). Supports ** for recursive matching."`
	Path    string `json:"path,omitzero" jsonschema:"description=Directory to search in. Defaults to the current working directory."`
}

// GlobTool finds files matching a glob pattern.
type GlobTool struct {
	definition api.ToolDefinition
}

// NewGlobTool creates a GlobTool with its pre-computed schema.
func NewGlobTool() *GlobTool {
	return &GlobTool{
		definition: api.ToolDefinition{
			Name:        "glob",
			Description: "Find LOCAL files matching a glob pattern. Only works on local filesystem — do not pass URLs. Use ** for recursive directory matching (e.g. '**/*.go'). Returns paths sorted by modification time (most recent first), respects .gitignore. To orient on a fresh repository, glob `**/*` once — the returned paths describe the project tree.",
			InputSchema: GenerateSchema(GlobInput{}),
		},
	}
}

func (t *GlobTool) Name() string                   { return "glob" }
func (t *GlobTool) Definition() api.ToolDefinition { return t.definition }
func (t *GlobTool) MaxResultBytes() int            { return globMaxBytes }
func (t *GlobTool) IsReadOnly() bool               { return true }
func (t *GlobTool) IsConcurrencySafe() bool        { return true }

// PermissionKey returns "search:<path>" so globbing scope mirrors grep.
func (t *GlobTool) PermissionKey(input []byte) string {
	params, err := unmarshalInput[GlobInput](input)
	if err != nil {
		return ""
	}
	return "search:" + params.Path
}

func (t *GlobTool) PermissionKeyWithContext(input []byte, tc agent.ToolContext) (string, error) {
	params, err := unmarshalInput[GlobInput](input)
	if err != nil {
		return "", err
	}
	path := params.Path
	if path == "" {
		path = tc.Cwd
	}
	resolved, err := resolveToolPath(path, tc, toolPathSearch)
	if err != nil {
		return "", err
	}
	return searchPermissionKey(resolved), nil
}

func (t *GlobTool) FormatForDisplay(input []byte) string {
	return t.formatForDisplay(input, "")
}

func (t *GlobTool) FormatForDisplayWithContext(input []byte, tc agent.ToolContext) string {
	return t.formatForDisplay(input, tc.Cwd)
}

func (t *GlobTool) formatForDisplay(input []byte, cwd string) string {
	params, err := unmarshalInput[GlobInput](input)
	if err != nil {
		return "Glob(?)"
	}
	kvs := []string{"pattern", params.Pattern}
	if params.Path != "" {
		kvs = append(kvs, "path", RelPathForDisplay(cwd, params.Path))
	}
	return FormatCall("Glob", kvs...)
}

type globEntry struct {
	path    string
	modTime time.Time
}

func (t *GlobTool) Execute(_ context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	params, err := unmarshalInput[GlobInput](input)
	if err != nil {
		return agent.Result{}, err
	}

	if params.Pattern == "" {
		return agent.Result{}, fmt.Errorf("pattern is required")
	}

	root := params.Path
	if strings.HasPrefix(root, "http://") || strings.HasPrefix(root, "https://") {
		return agent.Result{}, fmt.Errorf("glob only works on local directories — use the webfetch tool to fetch content from URLs")
	}
	if root == "" {
		if tc.Cwd != "" {
			root = tc.Cwd
		} else {
			var err error
			root, err = os.Getwd()
			if err != nil {
				return agent.Result{}, fmt.Errorf("getting working directory: %w", err)
			}
		}
	}
	root, err = resolveToolPath(root, tc, toolPathSearch)
	if err != nil {
		return agent.Result{}, fmt.Errorf("path %s: %w", root, err)
	}

	info, err := os.Stat(root)
	if err != nil {
		return agent.Result{}, fmt.Errorf("path %s: %w", root, err)
	}
	if !info.IsDir() {
		return agent.Result{}, fmt.Errorf("path %s is not a directory", root)
	}

	matcher := LoadGitignore(root)

	var entries []globEntry
	// Track WalkDir callback errors so the caller learns when EACCES
	// (or another I/O fault) silently truncates the search. Without
	// this, a permission-denied subdirectory results in incomplete
	// output indistinguishable from "no matches there".
	var skippedErrors int
	var firstSkippedPath string

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skippedErrors++
			if firstSkippedPath == "" {
				firstSkippedPath = path
			}
			// Bail on the whole subtree when the entry was a directory
			// — descending further is pointless once we've lost access.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		if rel == "." {
			return nil
		}

		// Check gitignore — skip ignored directories entirely.
		if IsGitIgnored(matcher, rel) || (d.IsDir() && IsGitIgnored(matcher, rel+"/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		if matchGlob(params.Pattern, rel) {
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, globEntry{path: rel, modTime: fi.ModTime()})
		}

		return nil
	})

	if len(entries) == 0 {
		return agent.Result{Content: "No files found matching pattern.", Bytes: 29}, nil
	}

	totalEntries := len(entries)

	// Sort by modification time, most recent first.
	slices.SortFunc(entries, func(a, b globEntry) int {
		return cmp.Compare(b.modTime.UnixNano(), a.modTime.UnixNano())
	})

	truncated := false
	if len(entries) > globMaxResults {
		entries = entries[:globMaxResults]
		truncated = true
	}

	var b strings.Builder
	bytesTruncated := false
	emitted := 0
	for _, e := range entries {
		// Stop emitting if the next path would push us past globMaxBytes.
		// Long paths in deeply nested repos can blow the aggregate cap
		// even at 200 entries; a soft byte cutoff keeps the output
		// self-describing instead of being trimmed mid-line by the
		// central limiter.
		if b.Len()+len(e.path)+1 > globMaxBytes {
			bytesTruncated = true
			break
		}
		b.WriteString(e.path)
		b.WriteByte('\n')
		emitted++
	}

	if truncated {
		fmt.Fprintf(&b, "\n[Results limited to %d files]", globMaxResults)
	}
	if bytesTruncated {
		fmt.Fprintf(&b, "\n[Output truncated at %d bytes; showing %d of %d entries]", globMaxBytes, emitted, len(entries))
	}
	if skippedErrors > 0 {
		// One trailing line so the model (and user) see the search
		// wasn't exhaustive. First-skipped path is enough to
		// orient — we don't enumerate every inaccessible subtree.
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "\n[Skipped %d path(s) due to I/O errors; first: %s]", skippedErrors, firstSkippedPath)
	}

	s := b.String()
	noun := "files"
	if totalEntries == 1 {
		noun = "file"
	}
	summary := fmt.Sprintf("%d %s", totalEntries, noun)
	if truncated {
		summary = fmt.Sprintf("%d %s (showing %d)", totalEntries, noun, globMaxResults)
	}
	return agent.Result{Content: s, Bytes: len(s), DisplaySummary: summary}, nil
}

// matchGlob matches a file's relative path against a glob pattern with **
// support. Both inputs are normalized to forward slashes so the same
// pattern matches on Windows and POSIX (mirrors permission.MatchGlob).
func matchGlob(pattern, relPath string) bool {
	pattern = filepath.ToSlash(pattern)
	relPath = filepath.ToSlash(relPath)

	// Handle patterns starting with **/
	if strings.HasPrefix(pattern, "**/") {
		suffix := pattern[3:]

		parts := strings.Split(relPath, "/")
		for i := range parts {
			tail := strings.Join(parts[i:], "/")
			if matched, _ := path.Match(suffix, tail); matched {
				return true
			}
		}
		return false
	}

	// Handle patterns with /**/ in the middle.
	if idx := strings.Index(pattern, "/**/"); idx >= 0 {
		prefix := pattern[:idx]
		suffix := pattern[idx+4:]

		parts := strings.Split(relPath, "/")
		for i := 1; i < len(parts); i++ {
			left := strings.Join(parts[:i], "/")
			right := strings.Join(parts[i:], "/")

			prefixMatch, _ := path.Match(prefix, left)
			if !prefixMatch {
				continue
			}
			if matchGlob("**/"+suffix, right) {
				return true
			}
		}
		return false
	}

	// Handle trailing /** (match everything under a directory).
	if strings.HasSuffix(pattern, "/**") {
		prefix := pattern[:len(pattern)-3]
		matched, _ := path.Match(prefix, strings.Split(relPath, "/")[0])
		if matched {
			return true
		}
		dir := path.Dir(relPath)
		for dir != "." && dir != "/" {
			if matched, _ := path.Match(prefix, dir); matched {
				return true
			}
			dir = path.Dir(dir)
		}
		return false
	}

	// No ** — use path.Match directly (slash-separated semantics).
	matched, _ := path.Match(pattern, relPath)
	return matched
}
