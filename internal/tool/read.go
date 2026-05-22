package tool

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

const (
	// readSpillThreshold is the size above which Read writes full content to
	// .prompto/tmp/<sha>.txt and returns a head preview instead of inlining
	// everything. Below the threshold Read inlines the file as plain text.
	readSpillThreshold = 200 * 1024 // 200 KB
	// readHeadLines is the number of leading lines returned alongside a spill
	// reference.
	readHeadLines    = 500
	readDefaultLimit = 2000 // default max lines to return when no offset/limit
	// readMaxFileSize is the hard cap above which Read refuses to load
	// the file at all. The spill path still calls os.ReadFile, so without
	// this gate a multi-GB log dropped by mistake into the workspace would
	// OOM the agent before any size-aware code ran. 50MB covers normal
	// source / config / data files; oversized files should be sliced via
	// offset/limit, grep, or external tools.
	readMaxFileSize = 50 * 1024 * 1024
	readScannerMax  = 1024 * 1024
)

// ReadInput defines the JSON parameters for the read tool.
type ReadInput struct {
	FilePath string `json:"file_path" jsonschema:"required,description=The absolute path to the file to read"`
	Offset   int    `json:"offset,omitzero" jsonschema:"description=Line number to start reading from (0-based). Defaults to 0."`
	Limit    int    `json:"limit,omitzero" jsonschema:"description=Maximum number of lines to read. Defaults to 2000."`
}

// ReadTool reads files and returns their contents with line numbers.
type ReadTool struct {
	definition api.ToolDefinition
}

// NewReadTool creates a ReadTool with its pre-computed schema.
func NewReadTool() *ReadTool {
	return &ReadTool{
		definition: api.ToolDefinition{
			Name:        "read",
			Description: "Read the contents of a LOCAL file. Returns the file content with line numbers. Only works on local filesystem paths — for web URLs, use webfetch instead. For large files, use offset and limit to read specific sections.",
			InputSchema: GenerateSchema(ReadInput{}),
		},
	}
}

func (t *ReadTool) Name() string                   { return "read" }
func (t *ReadTool) Definition() api.ToolDefinition { return t.definition }

// MaxResultBytes is a defensive upper bound: the spill path at
// readSpillThreshold (200 KB) replaces oversized inlines with a head
// preview, so Read never legitimately produces output above ~256 KB. The
// 512 KB cap here exists only as a backstop for the central aggregator
// against a future bug in the spill path or an unusual head-only response
// that grows unexpectedly.
func (t *ReadTool) MaxResultBytes() int { return 512 * 1024 }

// IsReadOnly: Read never modifies state. Can be batched in parallel.
func (t *ReadTool) IsReadOnly() bool        { return true }
func (t *ReadTool) IsConcurrencySafe() bool { return true }

// PermissionKey returns the absolute file path (or the raw file_path input
// when it can't be parsed as JSON). Rules can then allow/deny reads by
// path glob.
func (t *ReadTool) PermissionKey(input []byte) string {
	params, err := unmarshalInput[ReadInput](input)
	if err != nil {
		return ""
	}
	return params.FilePath
}

func (t *ReadTool) PermissionKeyWithContext(input []byte, tc agent.ToolContext) (string, error) {
	params, err := unmarshalInput[ReadInput](input)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(params.FilePath, "http://") || strings.HasPrefix(params.FilePath, "https://") {
		return params.FilePath, nil
	}
	return resolveToolPath(params.FilePath, tc, toolPathRead)
}

func (t *ReadTool) FormatForDisplay(input []byte) string {
	return t.formatForDisplay(input, "")
}

// FormatForDisplayWithContext satisfies agent.ContextualDisplay so the
// dispatcher can pass cwd, letting the header render relative paths
// (`internal/tui/env.go`) instead of the escaped absolute form
// (`"G:\\Go Workspace\\prompto\\internal\\tui\\env.go"`).
func (t *ReadTool) FormatForDisplayWithContext(input []byte, tc agent.ToolContext) string {
	return t.formatForDisplay(input, tc.Cwd)
}

func (t *ReadTool) formatForDisplay(input []byte, cwd string) string {
	params, err := unmarshalInput[ReadInput](input)
	if err != nil {
		return "Read(?)"
	}
	return FormatCall("Read", "file_path", RelPathForDisplay(cwd, params.FilePath))
}

func (t *ReadTool) Execute(_ context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	params, err := unmarshalInput[ReadInput](input)
	if err != nil {
		return agent.Result{}, err
	}

	if params.FilePath == "" {
		return agent.Result{}, fmt.Errorf("file_path is required")
	}

	if strings.HasPrefix(params.FilePath, "http://") || strings.HasPrefix(params.FilePath, "https://") {
		return agent.Result{}, fmt.Errorf("read only works on local files — use the webfetch tool to fetch content from URLs")
	}
	params.FilePath, err = resolveToolPath(params.FilePath, tc, toolPathRead)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", params.FilePath, err)
	}

	info, err := os.Stat(params.FilePath)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", params.FilePath, err)
	}
	if info.Size() > readMaxFileSize {
		if params.Limit > 0 {
			res, err := readPagedFile(params.FilePath, info, params.Offset, params.Limit)
			if err != nil {
				return agent.Result{}, err
			}
			maybeSurfaceNestedAgentsMD(tc, params.FilePath)
			return res, nil
		}
		return agent.Result{}, fmt.Errorf(
			"reading %s: file is %s, larger than the %s read limit; use grep/glob to scan, or read with offset+limit to slice",
			params.FilePath, HumanizeBytes(int(info.Size())), HumanizeBytes(readMaxFileSize),
		)
	}

	data, err := os.ReadFile(params.FilePath)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", params.FilePath, err)
	}

	if !utf8.Valid(data) || containsNull(data) {
		return agent.Result{}, fmt.Errorf("%s appears to be a binary file", params.FilePath)
	}

	// Record full content in FileState before any truncation for the model view.
	// Edit/Write see the real on-disk state, not the truncated one.
	tc.FileState.Put(params.FilePath, info.ModTime(), data)

	// Surface any nested AGENTS.md the eager startup pass missed. Best-
	// effort: failures are silent — the read result is the user-facing
	// product, not the bonus reminder.
	maybeSurfaceNestedAgentsMD(tc, params.FilePath)

	content := strings.TrimRight(string(data), "\n")
	lines := strings.Split(content, "\n")

	offset := params.Offset
	limit := params.Limit
	if limit <= 0 {
		limit = readDefaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(lines) {
		s := fmt.Sprintf("(file has %d lines, offset %d is past end)", len(lines), offset)
		return agent.Result{Content: s, Bytes: len(s)}, nil
	}

	// Oversized file and no paging requested → spill full content and return
	// a head preview + reference. Pagination (non-default offset/limit)
	// short-circuits spill: the caller wants a specific window.
	paging := params.Offset != 0 || (params.Limit > 0 && params.Limit != readDefaultLimit)
	if !paging && len(data) > readSpillThreshold {
		spillPath, err := agent.Spill(agent.SpillInput{Cwd: tc.Cwd, Content: string(data)})
		if err != nil {
			// Fall through to the non-spill path on spill failure; it's a
			// degraded mode, not fatal.
			spillPath = ""
		}
		head := readHeadLines
		if head > len(lines) {
			head = len(lines)
		}
		var b strings.Builder
		for i, line := range lines[:head] {
			fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
		}
		if spillPath != "" {
			fmt.Fprintf(&b,
				"\n[Full file saved to %s (%d lines, %d bytes).\n"+
					"Use read with offset/limit to scan specific ranges, or grep/glob for targeted lookups.]\n",
				spillPath, len(lines), len(data))
		} else {
			fmt.Fprintf(&b,
				"\n[File has %d total lines (%d bytes). Use offset/limit to read remaining content.]\n",
				len(lines), len(data))
		}
		s := b.String()
		return agent.Result{
			Content:        s,
			Bytes:          len(data),
			DisplaySummary: fmt.Sprintf("%d lines · %s", len(lines), HumanizeBytes(len(data))),
		}, nil
	}

	// Standard paged read.
	end := offset + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i, line := range lines[offset:end] {
		fmt.Fprintf(&b, "%6d\t%s\n", offset+i+1, line)
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "\n[Showing lines %d-%d of %d total. Use offset to read more.]\n",
			offset+1, end, len(lines))
	}

	s := b.String()
	summary := fmt.Sprintf("%d lines · %s", len(lines), HumanizeBytes(len(data)))
	if end < len(lines) || offset > 0 {
		summary = fmt.Sprintf("lines %d–%d of %d · %s", offset+1, end, len(lines), HumanizeBytes(len(data)))
	}
	return agent.Result{Content: s, Bytes: len(data), DisplaySummary: summary}, nil
}

func readPagedFile(path string, info os.FileInfo, offset, limit int) (agent.Result, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		return agent.Result{}, fmt.Errorf("reading %s: limit is required when slicing files larger than %s", path, HumanizeBytes(readMaxFileSize))
	}

	f, err := os.Open(path)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	header := make([]byte, 4096)
	n, _ := f.Read(header)
	if !utf8.Valid(header[:n]) || containsNull(header[:n]) {
		return agent.Result{}, fmt.Errorf("%s appears to be a binary file", path)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), readScannerMax)

	var b strings.Builder
	lineNum := 0
	written := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= offset {
			continue
		}
		if written >= limit {
			break
		}
		fmt.Fprintf(&b, "%6d\t%s\n", lineNum, scanner.Text())
		written++
		if written >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", path, err)
	}

	if written == 0 {
		s := fmt.Sprintf("(offset %d is past the inspected range)", offset)
		return agent.Result{Content: s, Bytes: int(info.Size()), DisplaySummary: fmt.Sprintf("0 lines · %s", HumanizeBytes(int(info.Size())))}, nil
	}

	end := offset + written
	fmt.Fprintf(&b,
		"\n[Showing lines %d-%d from a %s file. Partial large-file reads are for inspection only; edit/write safety still requires loading the full target.]\n",
		offset+1, end, HumanizeBytes(int(info.Size())))
	s := b.String()
	return agent.Result{
		Content:        s,
		Bytes:          int(info.Size()),
		DisplaySummary: fmt.Sprintf("lines %d–%d · %s", offset+1, end, HumanizeBytes(int(info.Size()))),
	}, nil
}

func containsNull(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// maybeSurfaceNestedAgentsMD walks up from filePath and queues a
// path-only reminder for each nested AGENTS.md not yet seen this run.
// The full content is intentionally omitted — injecting it permanently
// into the conversation costs tokens forever, while a path-only nudge
// lets the model spend one cheap read tool call to fetch it on demand.
// No-op when the wiring closures are nil (subagents, tests).
func maybeSurfaceNestedAgentsMD(tc agent.ToolContext, filePath string) {
	if tc.QueueReminder == nil || tc.HasSeenAgentsMD == nil || tc.MarkSeenAgentsMD == nil {
		return
	}
	if tc.AgentsMDLoadRoot == "" {
		return
	}
	entries, err := agent.LoadAgentsMDForFile(filePath, tc.AgentsMDLoadRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if tc.HasSeenAgentsMD(e.Path) {
			continue
		}
		tc.MarkSeenAgentsMD(e.Path)
		tc.QueueReminder("Project instructions exist at " + e.Path + ". Read that file before editing files in its directory.")
	}
}
