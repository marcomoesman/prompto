package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// WriteInput defines the JSON parameters for the write tool.
type WriteInput struct {
	FilePath string `json:"file_path" jsonschema:"required,description=Path to the file to write (absolute or relative to working directory)"`
	Content  string `json:"content"   jsonschema:"required,description=The content to write to the file"`
}

// WriteTool creates or overwrites a file with the given content.
type WriteTool struct {
	definition api.ToolDefinition
}

// NewWriteTool creates a WriteTool with its pre-computed schema.
func NewWriteTool() *WriteTool {
	return &WriteTool{
		definition: api.ToolDefinition{
			Name:        "write",
			Description: "Create or overwrite a file with the given content. Parent directories are created automatically. Use this tool to create new files. For modifying existing files, prefer the edit tool instead.",
			InputSchema: GenerateSchema(WriteInput{}),
		},
	}
}

func (t *WriteTool) Name() string                   { return "write" }
func (t *WriteTool) Definition() api.ToolDefinition { return t.definition }
func (t *WriteTool) MaxResultBytes() int            { return 0 }
func (t *WriteTool) IsReadOnly() bool               { return false }
func (t *WriteTool) IsConcurrencySafe() bool        { return false }

// PermissionKey returns the target file path.
func (t *WriteTool) PermissionKey(input []byte) string {
	params, err := unmarshalInput[WriteInput](input)
	if err != nil {
		return ""
	}
	return params.FilePath
}

func (t *WriteTool) PermissionKeyWithContext(input []byte, tc agent.ToolContext) (string, error) {
	params, err := unmarshalInput[WriteInput](input)
	if err != nil {
		return "", err
	}
	return resolveToolPath(params.FilePath, tc, toolPathWrite)
}

func (t *WriteTool) FormatForDisplay(input []byte) string {
	return t.formatForDisplay(input, "")
}

func (t *WriteTool) FormatForDisplayWithContext(input []byte, tc agent.ToolContext) string {
	return t.formatForDisplay(input, tc.Cwd)
}

func (t *WriteTool) formatForDisplay(input []byte, cwd string) string {
	params, err := unmarshalInput[WriteInput](input)
	if err != nil {
		return "Write(?)"
	}
	return FormatCall("Write", "file_path", RelPathForDisplay(cwd, params.FilePath))
}

func (t *WriteTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	params, err := unmarshalInput[WriteInput](input)
	if err != nil {
		return agent.Result{}, err
	}

	if params.FilePath == "" {
		return agent.Result{}, fmt.Errorf("file_path is required")
	}

	params.FilePath, err = resolveToolPath(params.FilePath, tc, toolPathWrite)
	if err != nil {
		return agent.Result{}, err
	}

	// Read-before-write applies when the file already exists. For a brand-new
	// file, there's nothing to read first. Capture the pre-write content
	// when the file exists so we can emit a file_change with before/after.
	var (
		op            = "create"
		contentBefore []byte
		modeBefore    uint32 // pre-write mode bits; 0 when file is new
	)
	if info, statErr := os.Stat(params.FilePath); statErr == nil {
		if err := tc.FileState.Check(params.FilePath); err != nil {
			if errors.Is(err, agent.ErrReadBeforeWrite) {
				return agent.Result{}, fmt.Errorf("write %s: %w — read the file first (or use edit for in-place changes)", params.FilePath, err)
			}
			return agent.Result{}, fmt.Errorf("write %s: %w", params.FilePath, err)
		}
		op = "modify"
		contentBefore, _ = os.ReadFile(params.FilePath) // best-effort
		modeBefore = uint32(info.Mode().Perm())
	}

	// Auto-created parent dirs use 0o700 (owner-only). On Unix the
	// agent shouldn't be silently widening directory exposure for the
	// user's account; if the model writes to /tmp/scratch/.env, the
	// containing directory should not become world-readable. The umask
	// will further restrict this; we never want the upper bound any
	// looser than owner-only.
	if err := os.MkdirAll(filepath.Dir(params.FilePath), 0o700); err != nil {
		return agent.Result{}, fmt.Errorf("creating parent directories for %s: %w", params.FilePath, err)
	}

	// Mode resolution:
	//   - existing file → reuse modeBefore, preserving the user's
	//     execute bits / restricted permissions / setuid bits exactly.
	//     A previously-0o600 secret stays 0o600 after the write.
	//   - new file → 0o600 (owner-only). Conservative default; the
	//     user can chmod later if a script needs to be group-readable
	//     or executable. Wrong-direction mistakes (over-permissive on
	//     create) leak information; under-permissive ones don't.
	writeMode := os.FileMode(0o600)
	if modeBefore != 0 {
		writeMode = os.FileMode(modeBefore)
	}
	if err := os.WriteFile(params.FilePath, []byte(params.Content), writeMode); err != nil {
		return agent.Result{}, fmt.Errorf("writing %s: %w", params.FilePath, err)
	}
	// os.WriteFile only applies its mode when creating; if the file
	// already existed, its permissions are unchanged. That's the
	// correct preservation behavior — no chmod call needed.

	// Refresh FileState with the new content.
	if info, err := os.Stat(params.FilePath); err == nil {
		tc.FileState.Put(params.FilePath, info.ModTime(), []byte(params.Content))
	}

	_ = tc.Sink().Record(ctx, agent.FileChangeEvent{
		MessageID:     tc.MessageID,
		ToolCallID:    tc.ToolCallID,
		Path:          params.FilePath,
		Op:            op,
		ContentBefore: contentBefore,
		ContentAfter:  []byte(params.Content),
		Mode:          modeBefore,
	})

	lineCount := strings.Count(params.Content, "\n")
	if !strings.HasSuffix(params.Content, "\n") && params.Content != "" {
		lineCount++
	}
	s := fmt.Sprintf("Successfully wrote %d bytes to %s", len(params.Content), params.FilePath)
	verb := "wrote"
	if op == "create" {
		verb = "created"
	}
	return agent.Result{
		Content:        s,
		Bytes:          len(s),
		DisplaySummary: fmt.Sprintf("%s %s · %d lines · %s", verb, filepath.Base(params.FilePath), lineCount, HumanizeBytes(len(params.Content))),
	}, nil
}
