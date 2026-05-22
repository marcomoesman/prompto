package tool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

const (
	grepDefaultMax = 100
	grepMaxOutput  = 50 * 1024 // 50KB output truncation
	grepTimeout    = 30 * time.Second

	// grepScannerMax is the per-line cap for the bufio.Scanner used when
	// rg is unavailable. The default 64KB tripped on minified bundles and
	// any file with one very long line — Scan returned false, Err set
	// bufio.ErrTooLong, the rest of the file went unsearched, and matches
	// downstream were silently dropped. 1MB covers every realistic source
	// file; oversized lines surface as a scan error rather than vanishing.
	grepScannerMax = 1024 * 1024
)

// GrepInput defines the JSON parameters for the grep tool.
type GrepInput struct {
	Pattern    string `json:"pattern"              jsonschema:"required,description=Regular expression pattern to search for"`
	Include    string `json:"include,omitzero"     jsonschema:"description=Glob pattern to filter files (e.g. *.go or *.{ts,tsx})"`
	Path       string `json:"path,omitzero"        jsonschema:"description=Directory or file to search in. Defaults to the current working directory."`
	MaxResults int    `json:"max_results,omitzero" jsonschema:"description=Maximum number of matching lines to return. Defaults to 100."`
}

// GrepTool searches file contents using regular expressions.
type GrepTool struct {
	definition api.ToolDefinition
}

// NewGrepTool creates a GrepTool with its pre-computed schema.
func NewGrepTool() *GrepTool {
	return &GrepTool{
		definition: api.ToolDefinition{
			Name:        "grep",
			Description: "Search file contents using regular expressions. Uses ripgrep when available for fast searching, with a pure Go fallback. Respects .gitignore. Returns matching lines in file:line:content format.",
			InputSchema: GenerateSchema(GrepInput{}),
		},
	}
}

func (t *GrepTool) Name() string                   { return "grep" }
func (t *GrepTool) Definition() api.ToolDefinition { return t.definition }
func (t *GrepTool) MaxResultBytes() int            { return 0 }
func (t *GrepTool) IsReadOnly() bool               { return true }
func (t *GrepTool) IsConcurrencySafe() bool        { return true }

// PermissionKey returns "search:<path>" so rules can allow searching within
// certain subtrees without granting full read-by-path.
func (t *GrepTool) PermissionKey(input []byte) string {
	params, err := unmarshalInput[GrepInput](input)
	if err != nil {
		return ""
	}
	return "search:" + params.Path
}

func (t *GrepTool) PermissionKeyWithContext(input []byte, tc agent.ToolContext) (string, error) {
	params, err := unmarshalInput[GrepInput](input)
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

func (t *GrepTool) FormatForDisplay(input []byte) string {
	return t.formatForDisplay(input, "")
}

func (t *GrepTool) FormatForDisplayWithContext(input []byte, tc agent.ToolContext) string {
	return t.formatForDisplay(input, tc.Cwd)
}

func (t *GrepTool) formatForDisplay(input []byte, cwd string) string {
	params, err := unmarshalInput[GrepInput](input)
	if err != nil {
		return "Grep(?)"
	}
	kvs := []string{"pattern", params.Pattern}
	if params.Include != "" {
		kvs = append(kvs, "include", params.Include)
	}
	if params.Path != "" {
		kvs = append(kvs, "path", RelPathForDisplay(cwd, params.Path))
	}
	return FormatCall("Grep", kvs...)
}

func (t *GrepTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	params, err := unmarshalInput[GrepInput](input)
	if err != nil {
		return agent.Result{}, err
	}

	if params.Pattern == "" {
		return agent.Result{}, fmt.Errorf("pattern is required")
	}

	if params.Path == "" {
		if tc.Cwd != "" {
			params.Path = tc.Cwd
		} else {
			var err error
			params.Path, err = os.Getwd()
			if err != nil {
				return agent.Result{}, fmt.Errorf("getting working directory: %w", err)
			}
		}
	}
	params.Path, err = resolveToolPath(params.Path, tc, toolPathSearch)
	if err != nil {
		return agent.Result{}, fmt.Errorf("searching %s: %w", params.Path, err)
	}

	if params.MaxResults <= 0 {
		params.MaxResults = grepDefaultMax
	}

	if err := validateGrepPattern(params.Pattern); err != nil {
		return agent.Result{}, err
	}

	// Try ripgrep first.
	if rgPath, err := exec.LookPath("rg"); err == nil {
		return grepWithRipgrep(ctx, rgPath, params)
	}

	return grepWithGo(ctx, params)
}

// buildRipgrepArgs is the pure arg-builder for grepWithRipgrep,
// extracted so the args can be unit-tested without shelling out.
// --max-count caps matches PER FILE; the Go post-process at
// grepMaxOutput / params.MaxResults still applies a global cap.
func buildRipgrepArgs(params GrepInput) []string {
	args := []string{
		"--line-number",
		"--no-heading",
		"--color=never",
		// Per-file match cap. Without it, the rg path silently
		// buffered the full unbounded match set even when the Go
		// fallback path honored MaxResults — a pathological file
		// could stream megabytes before the post-process trim.
		fmt.Sprintf("--max-count=%d", params.MaxResults),
	}
	if params.Include != "" {
		args = append(args, "--glob", params.Include)
	}
	args = append(args, params.Pattern, params.Path)
	return args
}

func grepWithRipgrep(ctx context.Context, rgPath string, params GrepInput) (agent.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, grepTimeout)
	defer cancel()

	args := buildRipgrepArgs(params)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{}, fmt.Errorf("starting ripgrep stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.Result{}, fmt.Errorf("starting ripgrep stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.Result{}, fmt.Errorf("starting ripgrep: %w", err)
	}

	output := newLimitedOutputBuffer(grepMaxOutput)
	stderrOutput := newLimitedOutputBuffer(grepMaxOutput)
	stderrDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(stderrOutput, stderr)
		stderrDone <- err
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), grepScannerMax)
	matchCount := 0
	stoppedByLimit := false
	for scanner.Scan() {
		output.WriteString(scanner.Text())
		output.WriteString("\n")
		matchCount++
		if matchCount >= params.MaxResults || output.Len() >= grepMaxOutput {
			stoppedByLimit = true
			cancel()
			break
		}
	}
	scanErr := scanner.Err()
	// Wait under a 2s ceiling. exec.CommandContext kills the process
	// on cancel, but on Windows TerminateProcess is async and pipe
	// teardown can stall — without the bound, a stoppedByLimit hit
	// occasionally hangs Wait until rg's own buffer pressure
	// resolves. Force-kill and re-Wait if it doesn't exit promptly.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case err = <-waitDone:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		err = <-waitDone
	}
	if stderrErr := <-stderrDone; stderrErr != nil && err == nil && !stoppedByLimit {
		err = stderrErr
	}
	result := output.String()

	// rg exit code 1 = no matches (not an error), exit code 2 = actual error.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return agent.Result{Content: "No matches found.", Bytes: 18}, nil
			}
			if !stoppedByLimit {
				errText := strings.TrimSpace(stderrOutput.String())
				if errText == "" {
					errText = strings.TrimSpace(result)
				}
				return agent.Result{}, fmt.Errorf("ripgrep error: %s", errText)
			}
		} else if !stoppedByLimit {
			if ctx.Err() == context.DeadlineExceeded {
				s := fmt.Sprintf("%s\n[Search timed out after %s]", result, grepTimeout)
				return agent.Result{Content: s, Bytes: output.Observed()}, nil
			}
			return agent.Result{}, fmt.Errorf("running ripgrep: %w", err)
		}
	}
	if scanErr != nil && !stoppedByLimit {
		return agent.Result{}, fmt.Errorf("reading ripgrep output: %w", scanErr)
	}
	if matchCount == 0 {
		return agent.Result{Content: "No matches found.", Bytes: 18}, nil
	}

	fullBytes := output.Observed()

	lines := strings.Split(result, "\n")
	matches, files := countGrepMatches(lines)
	if matchCount >= params.MaxResults {
		result = strings.TrimRight(result, "\n") + fmt.Sprintf("\n\n[Results limited to %d matches]", params.MaxResults)
	}
	if output.Truncated() {
		result = strings.TrimRight(result, "\n") + "\n[Output truncated at 50KB]"
	}

	return agent.Result{
		Content:        result,
		Bytes:          fullBytes,
		DisplaySummary: grepSummary(matches, files),
	}, nil
}

func grepWithGo(ctx context.Context, params GrepInput) (agent.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, grepTimeout)
	defer cancel()

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return agent.Result{}, fmt.Errorf("invalid regex pattern: %w", err)
	}

	root := params.Path

	info, err := os.Stat(root)
	if err != nil {
		return agent.Result{}, fmt.Errorf("path %s: %w", root, err)
	}

	var b strings.Builder
	matchCount := 0

	if !info.IsDir() {
		// Single file search.
		return grepFile(re, root, root, params.MaxResults)
	}

	matcher := LoadGitignore(root)

	// Track WalkDir callback errors so the caller learns when EACCES
	// (or another I/O fault) silently truncated the search. Without
	// surfacing this, a permission-denied subdirectory looks the same
	// as "no matches there".
	var skippedErrors int
	var firstSkippedPath string

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skippedErrors++
			if firstSkippedPath == "" {
				firstSkippedPath = path
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if matchCount >= params.MaxResults {
			return nil
		}

		// Check context cancellation.
		if ctx.Err() != nil {
			return fs.SkipAll
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		if rel == "." {
			return nil
		}

		// Check gitignore.
		if IsGitIgnored(matcher, rel) || (d.IsDir() && IsGitIgnored(matcher, rel+"/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		// Apply include filter.
		if params.Include != "" {
			matched, _ := filepath.Match(params.Include, filepath.Base(path))
			if !matched {
				return nil
			}
		}

		// Skip binary files (check first 512 bytes for null).
		f, err := os.Open(path)
		if err != nil {
			return nil
		}

		header := make([]byte, 512)
		n, _ := f.Read(header)
		if containsNullByte(header[:n]) {
			_ = f.Close()
			return nil
		}

		// Reset to beginning.
		if _, err := f.Seek(0, 0); err != nil {
			_ = f.Close()
			return nil
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), grepScannerMax)
		lineNum := 0
		for scanner.Scan() {
			if ctx.Err() != nil {
				break
			}
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				fmt.Fprintf(&b, "%s:%d:%s\n", rel, lineNum, line)
				matchCount++
				if matchCount >= params.MaxResults {
					break
				}
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(&b, "%s: scan error: %v\n", rel, err)
		}
		_ = f.Close()

		return nil
	})

	if ctx.Err() == context.DeadlineExceeded {
		s := fmt.Sprintf("%s\n[Search timed out after %s]", b.String(), grepTimeout)
		return agent.Result{Content: s, Bytes: len(s)}, nil
	}

	if matchCount == 0 {
		body := "No matches found."
		if skippedErrors > 0 {
			body += fmt.Sprintf("\n[Skipped %d path(s) due to I/O errors; first: %s]", skippedErrors, firstSkippedPath)
		}
		return agent.Result{Content: body, Bytes: len(body)}, nil
	}

	result := b.String()
	fullBytes := len(result)
	if len(result) > grepMaxOutput {
		result = truncateOnRune(result, grepMaxOutput) + "\n[Output truncated at 50KB]"
	}

	if matchCount >= params.MaxResults {
		result += fmt.Sprintf("\n[Results limited to %d matches]", params.MaxResults)
	}
	if skippedErrors > 0 {
		result += fmt.Sprintf("\n[Skipped %d path(s) due to I/O errors; first: %s]", skippedErrors, firstSkippedPath)
	}

	_, files := countGrepMatches(strings.Split(b.String(), "\n"))
	return agent.Result{
		Content:        result,
		Bytes:          fullBytes,
		DisplaySummary: grepSummary(matchCount, files),
	}, nil
}

// countGrepMatches counts non-empty lines and unique file-path prefixes
// (everything before the first `:`) so the summary can report
// "N matches in M files".
func countGrepMatches(lines []string) (matches, files int) {
	seen := make(map[string]struct{})
	for _, line := range lines {
		if line == "" {
			continue
		}
		matches++
		if i := strings.IndexByte(line, ':'); i > 0 {
			seen[line[:i]] = struct{}{}
		}
	}
	return matches, len(seen)
}

func grepSummary(matches, files int) string {
	if matches == 0 {
		return "no matches"
	}
	noun := "matches"
	if matches == 1 {
		noun = "match"
	}
	fnoun := "files"
	if files == 1 {
		fnoun = "file"
	}
	if files == 0 {
		return fmt.Sprintf("%d %s", matches, noun)
	}
	return fmt.Sprintf("%d %s in %d %s", matches, noun, files, fnoun)
}

// grepFile searches a single file and returns matches.
func grepFile(re *regexp.Regexp, path, displayPath string, maxResults int) (agent.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var b strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), grepScannerMax)
	lineNum := 0
	matchCount := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			fmt.Fprintf(&b, "%s:%d:%s\n", displayPath, lineNum, line)
			matchCount++
			if matchCount >= maxResults {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(&b, "%s: scan error: %v\n", displayPath, err)
	}

	if matchCount == 0 {
		return agent.Result{Content: "No matches found.", Bytes: 18}, nil
	}

	s := b.String()
	return agent.Result{
		Content:        s,
		Bytes:          len(s),
		DisplaySummary: grepSummary(matchCount, 1),
	}, nil
}

// truncateOnRune cuts s to at most n bytes, backtracking to a UTF-8
// rune boundary so the seam doesn't split a multi-byte codepoint.
// Used by tool output truncators where the raw byte slice would
// otherwise leak invalid UTF-8 into the JSON-encoded response.
func truncateOnRune(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func containsNullByte(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}
