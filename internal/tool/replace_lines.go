package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// ReplaceLinesInput defines the JSON parameters for replace_lines.
type ReplaceLinesInput struct {
	FilePath    string `json:"file_path"   jsonschema:"required,description=Path to the file to edit (absolute or relative to working directory)"`
	StartLine   int    `json:"start_line"  jsonschema:"required,description=First line to replace, 1-based and inclusive"`
	EndLine     int    `json:"end_line"    jsonschema:"required,description=Last line to replace, 1-based and inclusive"`
	Replacement string `json:"replacement" jsonschema:"required,description=The full replacement text for the selected line range"`
}

// ReplaceLinesTool replaces a 1-based inclusive line range in a file.
type ReplaceLinesTool struct {
	definition api.ToolDefinition
}

// NewReplaceLinesTool creates a ReplaceLinesTool with its pre-computed schema.
func NewReplaceLinesTool() *ReplaceLinesTool {
	return &ReplaceLinesTool{
		definition: api.ToolDefinition{
			Name:        "replace_lines",
			Description: "Replace a 1-based inclusive line range in a file with the provided replacement text. Use this when exact-string edit matching is fragile. Always read the file first and use line numbers from the latest read output.",
			InputSchema: GenerateSchema(ReplaceLinesInput{}),
		},
	}
}

func (t *ReplaceLinesTool) Name() string                   { return "replace_lines" }
func (t *ReplaceLinesTool) Definition() api.ToolDefinition { return t.definition }
func (t *ReplaceLinesTool) MaxResultBytes() int            { return 0 }
func (t *ReplaceLinesTool) IsReadOnly() bool               { return false }
func (t *ReplaceLinesTool) IsConcurrencySafe() bool        { return false }

func (t *ReplaceLinesTool) PermissionKey(input []byte) string {
	params, err := unmarshalInput[ReplaceLinesInput](input)
	if err != nil {
		return ""
	}
	return params.FilePath
}

func (t *ReplaceLinesTool) PermissionKeyWithContext(input []byte, tc agent.ToolContext) (string, error) {
	params, err := unmarshalInput[ReplaceLinesInput](input)
	if err != nil {
		return "", err
	}
	return resolveToolPath(params.FilePath, tc, toolPathEdit)
}

func (t *ReplaceLinesTool) FormatForDisplay(input []byte) string {
	return t.formatForDisplay(input, "")
}

func (t *ReplaceLinesTool) FormatForDisplayWithContext(input []byte, tc agent.ToolContext) string {
	return t.formatForDisplay(input, tc.Cwd)
}

func (t *ReplaceLinesTool) formatForDisplay(input []byte, cwd string) string {
	params, err := unmarshalInput[ReplaceLinesInput](input)
	if err != nil {
		return "ReplaceLines(?)"
	}
	return FormatCall("ReplaceLines",
		"file_path", RelPathForDisplay(cwd, params.FilePath),
		"start_line", fmt.Sprintf("%d", params.StartLine),
		"end_line", fmt.Sprintf("%d", params.EndLine),
	)
}

func (t *ReplaceLinesTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	params, err := unmarshalInput[ReplaceLinesInput](input)
	if err != nil {
		return agent.Result{}, err
	}
	if params.FilePath == "" {
		return agent.Result{}, fmt.Errorf("file_path is required")
	}
	if params.StartLine < 1 {
		return agent.Result{}, fmt.Errorf("start_line must be >= 1")
	}
	if params.EndLine < params.StartLine {
		return agent.Result{}, fmt.Errorf("end_line must be >= start_line")
	}

	params.FilePath, err = resolveToolPath(params.FilePath, tc, toolPathEdit)
	if err != nil {
		return agent.Result{}, err
	}
	if err := tc.FileState.Check(params.FilePath); err != nil {
		return agent.Result{}, fmt.Errorf("replace_lines %s: %w — read the file first to see current line numbers", params.FilePath, err)
	}

	info, err := os.Stat(params.FilePath)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", params.FilePath, err)
	}
	data, err := os.ReadFile(params.FilePath)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", params.FilePath, err)
	}

	spans := lineSpans(data)
	if len(spans) == 0 {
		return agent.Result{}, fmt.Errorf("replace_lines %s: file has 0 lines; range %d-%d is beyond EOF", params.FilePath, params.StartLine, params.EndLine)
	}
	if params.EndLine > len(spans) {
		return agent.Result{}, fmt.Errorf("replace_lines %s: range %d-%d is beyond EOF; file has %d lines", params.FilePath, params.StartLine, params.EndLine, len(spans))
	}

	start := spans[params.StartLine-1].start
	end := spans[params.EndLine-1].end
	newContent := make([]byte, 0, len(data)-(end-start)+len(params.Replacement))
	newContent = append(newContent, data[:start]...)
	newContent = append(newContent, params.Replacement...)
	newContent = append(newContent, data[end:]...)

	if err := os.WriteFile(params.FilePath, newContent, info.Mode()); err != nil {
		return agent.Result{}, fmt.Errorf("writing %s: %w", params.FilePath, err)
	}
	if newInfo, err := os.Stat(params.FilePath); err == nil {
		tc.FileState.Put(params.FilePath, newInfo.ModTime(), newContent)
	}

	_ = tc.Sink().Record(ctx, agent.FileChangeEvent{
		MessageID:     tc.MessageID,
		ToolCallID:    tc.ToolCallID,
		Path:          params.FilePath,
		Op:            "modify",
		ContentBefore: data,
		ContentAfter:  newContent,
	})

	s := fmt.Sprintf("Successfully replaced lines %d-%d in %s", params.StartLine, params.EndLine, params.FilePath)
	return agent.Result{
		Content: s,
		Bytes:   len(s),
		DisplaySummary: fmt.Sprintf("+%d −%d lines · %s",
			newlineDelta(params.Replacement),
			params.EndLine-params.StartLine+1,
			filepath.Base(params.FilePath),
		),
	}, nil
}

type byteLineSpan struct {
	start int
	end   int
}

func lineSpans(data []byte) []byteLineSpan {
	if len(data) == 0 {
		return nil
	}
	var spans []byteLineSpan
	start := 0
	for i, b := range data {
		if b == '\n' {
			spans = append(spans, byteLineSpan{start: start, end: i + 1})
			start = i + 1
		}
	}
	if start < len(data) {
		spans = append(spans, byteLineSpan{start: start, end: len(data)})
	}
	return spans
}
