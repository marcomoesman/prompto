package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// EditInput defines the JSON parameters for the edit tool. Two forms
// are accepted: single (top-level old_string + new_string) and batch
// (edits[]). Mixed input is rejected. The schema does not mark
// old_string/new_string as required because they're conditional —
// validation happens at the Go level in Execute.
type EditInput struct {
	FilePath  string   `json:"file_path"            jsonschema:"required,description=Path to the file to edit (absolute or relative to working directory)"`
	OldString string   `json:"old_string,omitempty" jsonschema:"description=Single-edit form: the exact string to find. MUST be a non-empty verbatim substring of the file's current content — copy bytes from a prior read tool call (do NOT paraphrase or reconstruct from memory). Include enough surrounding context to make the match unique. To insert at end-of-file, set old_string to the last existing line and new_string to that line plus the appended content. To create a new file, use the write tool instead — edit cannot create files. Required when edits[] is omitted; rejected when edits[] is set."`
	NewString string   `json:"new_string,omitempty" jsonschema:"description=Single-edit form: the replacement string. Empty deletes the matched text."`
	Edits     []EditOp `json:"edits,omitempty"      jsonschema:"description=Batch form: multiple replacements applied atomically. Each old_string is matched against the ORIGINAL file (not after earlier edits). The whole batch is rejected if any old_string fails to match uniquely or if two edits' match regions overlap or share an identical old_string."`
}

// EditOp is one replacement in the batch form.
type EditOp struct {
	OldString string `json:"old_string" jsonschema:"required,description=The exact string to find in the file. Must appear exactly once in the original file and must not overlap with another edit's region."`
	NewString string `json:"new_string" jsonschema:"description=The replacement string. Empty deletes the matched text."`
}

// EditTool performs exact string replacement in files. Single-form is
// the back-compat path; batch-form lets the model land multiple
// related changes in one call without round-trips.
type EditTool struct {
	definition api.ToolDefinition
}

// NewEditTool creates an EditTool with its pre-computed schema.
func NewEditTool() *EditTool {
	return &EditTool{
		definition: api.ToolDefinition{
			Name:        "edit",
			Description: "Edit a file by replacing exact text matches. Single-edit form: provide old_string + new_string. Batch form: provide edits[] with multiple {old_string, new_string} pairs. Each old_string must match exactly one non-overlapping region of the ORIGINAL file (matches are validated against the file as it exists now, not after earlier edits in the batch). If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Always read the file first.",
			InputSchema: GenerateSchema(EditInput{}),
		},
	}
}

func (t *EditTool) Name() string                   { return "edit" }
func (t *EditTool) Definition() api.ToolDefinition { return t.definition }
func (t *EditTool) MaxResultBytes() int            { return 0 }
func (t *EditTool) IsReadOnly() bool               { return false }
func (t *EditTool) IsConcurrencySafe() bool        { return false }

// PermissionKey returns the target file path. Single permission
// covers both single-form and batch-form — the batch is one
// model-initiated tool call against one file.
func (t *EditTool) PermissionKey(input []byte) string {
	params, err := unmarshalInput[EditInput](input)
	if err != nil {
		return ""
	}
	return params.FilePath
}

func (t *EditTool) PermissionKeyWithContext(input []byte, tc agent.ToolContext) (string, error) {
	params, err := unmarshalInput[EditInput](input)
	if err != nil {
		return "", err
	}
	return resolveToolPath(params.FilePath, tc, toolPathEdit)
}

func (t *EditTool) FormatForDisplay(input []byte) string {
	return t.formatForDisplay(input, "")
}

func (t *EditTool) FormatForDisplayWithContext(input []byte, tc agent.ToolContext) string {
	return t.formatForDisplay(input, tc.Cwd)
}

func (t *EditTool) formatForDisplay(input []byte, cwd string) string {
	params, err := unmarshalInput[EditInput](input)
	if err != nil {
		return "Edit(?)"
	}
	return FormatCall("Edit", "file_path", RelPathForDisplay(cwd, params.FilePath))
}

// editPlan is one validated edit ready to apply: the original-content
// match position and the replacement text. Computed during validation
// so the apply pass is straight string surgery.
type editPlan struct {
	start  int    // byte offset in the original content where oldString matched
	end    int    // start + len(oldString) — exclusive
	oldStr string
	newStr string
}

// missingOldStringMsg is the error returned when the model calls
// edit with new_string set but old_string empty. The most common
// causes are: (a) treating edit like write (i.e. trying to create a
// file or replace its full contents — that's the write tool's job),
// (b) trying to "append" by leaving old_string empty (the API has
// no append idiom — match the last existing line and emit it +
// appended content as new_string), and (c) hallucinating an empty
// string match. The message names each case so the model has a
// concrete next move instead of retrying the same shape.
const missingOldStringMsg = "old_string is required: copy the EXACT bytes of the region you want to replace from the file. " +
	"If you don't have them, call the read tool first and copy from its output verbatim — do NOT paraphrase or reconstruct from memory. " +
	"To create a new file, use the write tool instead (edit cannot create files). " +
	"To append to an existing file, set old_string to the last line currently in the file and new_string to that same line followed by your appended content"

// resolveEdits collapses the two input forms into one []EditOp. Mixed
// input (both old_string and edits[] set) is rejected. Empty input
// (neither set) is rejected. Single-form is hoisted into a one-element
// slice so the apply path is shared.
func resolveEdits(p EditInput) ([]EditOp, error) {
	hasSingle := p.OldString != "" || p.NewString != ""
	hasBatch := len(p.Edits) > 0
	switch {
	case hasSingle && hasBatch:
		return nil, fmt.Errorf("provide either old_string/new_string OR edits[], not both")
	case !hasSingle && !hasBatch:
		return nil, fmt.Errorf("missing arguments: provide old_string + new_string (single-edit form) or edits[] (batch form). To create a new file use the write tool — edit only modifies existing files")
	case hasSingle:
		if p.OldString == "" {
			return nil, errors.New(missingOldStringMsg)
		}
		return []EditOp{{OldString: p.OldString, NewString: p.NewString}}, nil
	default:
		// Batch form: reject zero-length oldStrings up front so the
		// "appears N times" count below doesn't divide by zero.
		for i, op := range p.Edits {
			if op.OldString == "" {
				return nil, fmt.Errorf("edits[%d]: %s", i, missingOldStringMsg)
			}
		}
		return p.Edits, nil
	}
}

// validateAndPlan computes the match position for each EditOp against
// the original content. Returns an error if any edit fails to match
// uniquely, if two edits share an identical oldString, or if two
// edits' match regions overlap. The returned slice is sorted in
// document order so the apply pass is deterministic regardless of
// input order.
func validateAndPlan(content string, ops []EditOp) ([]editPlan, error) {
	// First pass: per-edit uniqueness and identical-oldString detection.
	seen := make(map[string]int, len(ops)) // oldString -> first index
	plans := make([]editPlan, 0, len(ops))
	for i, op := range ops {
		if prev, dup := seen[op.OldString]; dup {
			return nil, fmt.Errorf("edits[%d]: old_string is byte-identical to edits[%d] — merge them into one edit (or change one to disambiguate)", i, prev)
		}
		seen[op.OldString] = i

		count := strings.Count(content, op.OldString)
		switch {
		case count == 0:
			// Diagnostic 1: edit already applied. The model loops on
			// "old_string not found" when the file already contains the
			// substitution it's proposing — the read after the failed
			// edit shows the new state, the model re-proposes the same
			// edit, and the cycle repeats. A specific message breaks
			// the loop.
			if op.NewString != "" && strings.Contains(content, op.NewString) {
				return nil, fmt.Errorf("edits[%d]: old_string not found, but new_string is already present — this edit may have been applied previously; check current file contents before trying again", i)
			}
			// Diagnostic 2: NFKC fallback. LLM tokenizers often emit
			// visually-equivalent Unicode variants (e.g. U+2022 BULLET
			// instead of the file's U+00B7 MIDDLE DOT, or U+22C5 DOT
			// OPERATOR for the same dot). The displayed glyph is
			// identical; the bytes differ; exact-match fails. Retry
			// with NFKC normalization on both sides — if there's
			// exactly one match in normalized form, splice using the
			// file's original bytes so the file's encoding survives
			// the edit unchanged.
			if start, end, ok := findNFKCMatch(content, op.OldString); ok {
				plans = append(plans, editPlan{
					start:  start,
					end:    end,
					oldStr: content[start:end],
					newStr: op.NewString,
				})
				continue
			}
			return nil, fmt.Errorf("edits[%d]: old_string not found — the file may have changed, use the read tool to see current contents", i)
		case count > 1:
			return nil, fmt.Errorf("edits[%d]: old_string appears %d times — provide more surrounding context to make the match unique", i, count)
		}
		start := strings.Index(content, op.OldString)
		plans = append(plans, editPlan{
			start:  start,
			end:    start + len(op.OldString),
			oldStr: op.OldString,
			newStr: op.NewString,
		})
	}

	// Second pass: overlap check after sorting by start position.
	sort.Slice(plans, func(i, j int) bool { return plans[i].start < plans[j].start })
	for i := 1; i < len(plans); i++ {
		if plans[i].start < plans[i-1].end {
			return nil, fmt.Errorf("edits overlap: regions [%d,%d) and [%d,%d) intersect — merge into one edit",
				plans[i-1].start, plans[i-1].end, plans[i].start, plans[i].end)
		}
	}
	return plans, nil
}

// findNFKCMatch returns the byte offsets of a unique NFKC-normalized
// match of target inside content, or false when zero or multiple
// such matches exist. Used by validateAndPlan as a fallback when an
// exact byte match fails — the most common cause is the model
// emitting a visually-equivalent Unicode variant (e.g. U+2022 BULLET
// instead of the file's U+00B7 MIDDLE DOT). NFKC compatibility-folds
// those variants together so the search succeeds.
//
// The returned offsets index into the ORIGINAL content (not the
// normalized form), so callers can splice without disturbing the
// file's existing encoding. For each candidate start position we
// walk runes forward, normalize incrementally, and accept the first
// length whose normalized form equals the normalized target.
func findNFKCMatch(content, target string) (start, end int, ok bool) {
	if target == "" {
		return 0, 0, false
	}
	nTarget := norm.NFKC.String(target)
	if nTarget == "" {
		return 0, 0, false
	}

	var matches []int // candidate start offsets in content
	var endOffsets []int
	for i := 0; i < len(content); {
		if matchEnd, hit := nfkcPrefixEqual(content[i:], nTarget); hit {
			matches = append(matches, i)
			endOffsets = append(endOffsets, i+matchEnd)
			i += matchEnd
			continue
		}
		_, size := utf8.DecodeRuneInString(content[i:])
		if size == 0 {
			break
		}
		i += size
	}
	if len(matches) != 1 {
		return 0, 0, false
	}
	return matches[0], endOffsets[0], true
}

// nfkcPrefixEqual reports whether some prefix of haystack has the
// same NFKC form as nTarget; on a match it returns the byte length
// of that prefix. nTarget must already be NFKC-normalized.
//
// The hot path here is findNFKCMatch, which calls this function once
// per starting byte position in the file content. The previous
// implementation built a strings.Builder and, per loop iteration,
// allocated `string(r)` for the single-rune slice and another string
// from `norm.NFKC.String(...)`. On a file with many candidate offsets
// (matching the first rune of target) this produced O(content_len ×
// target_len) allocations and visible latency for large files. The
// rewrite below keeps the same termination logic but:
//   - uses a byte slice instead of strings.Builder (no extra wrapper);
//   - feeds the rune's existing UTF-8 bytes from haystack to
//     norm.NFKC.AppendString, eliminating the `string(r)` copy;
//   - relies on Go's compiler optimization that turns
//     `string(byteSlice) == stringConst` into a memcmp without a heap
//     allocation, so the final compare is also alloc-free.
//
// AppendString does cumulative normalization (it returns
// f(append(out, src))) — semantically more accurate than the previous
// per-rune isolated normalization, and matches the contract implied by
// the function's name ("the NFKC form of the prefix").
func nfkcPrefixEqual(haystack, nTarget string) (int, bool) {
	nb := make([]byte, 0, len(nTarget)+utf8.UTFMax)
	for i := 0; i < len(haystack); {
		r, size := utf8.DecodeRuneInString(haystack[i:])
		if r == utf8.RuneError && size <= 1 {
			return 0, false
		}
		nb = norm.NFKC.AppendString(nb, haystack[i:i+size])
		i += size
		if len(nb) >= len(nTarget) {
			if string(nb) == nTarget {
				return i, true
			}
			return 0, false
		}
	}
	return 0, false
}

// applyPlans returns the new file content with all plans applied.
// Walks the original content once, splicing in newStr at each
// planned position. Plans must already be sorted by start ascending
// (validateAndPlan guarantees this).
func applyPlans(content string, plans []editPlan) string {
	var b strings.Builder
	b.Grow(len(content)) // close-enough hint; grows naturally if larger
	cursor := 0
	for _, p := range plans {
		b.WriteString(content[cursor:p.start])
		b.WriteString(p.newStr)
		cursor = p.end
	}
	b.WriteString(content[cursor:])
	return b.String()
}

func (t *EditTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	params, err := unmarshalInput[EditInput](input)
	if err != nil {
		return agent.Result{}, err
	}

	if params.FilePath == "" {
		return agent.Result{}, fmt.Errorf("file_path is required")
	}

	params.FilePath, err = resolveToolPath(params.FilePath, tc, toolPathEdit)
	if err != nil {
		return agent.Result{}, err
	}

	ops, err := resolveEdits(params)
	if err != nil {
		return agent.Result{}, err
	}

	// Read-before-write: the file must have been Read via the tool earlier
	// in this session and its content unchanged since then. One check
	// covers the whole batch.
	if err := tc.FileState.Check(params.FilePath); err != nil {
		return agent.Result{}, fmt.Errorf("edit %s: %w — read the file first to see current contents", params.FilePath, err)
	}

	info, err := os.Stat(params.FilePath)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", params.FilePath, err)
	}

	data, err := os.ReadFile(params.FilePath)
	if err != nil {
		return agent.Result{}, fmt.Errorf("reading %s: %w", params.FilePath, err)
	}

	content := string(data)
	plans, err := validateAndPlan(content, ops)
	if err != nil {
		return agent.Result{}, fmt.Errorf("edit %s: %w", params.FilePath, err)
	}

	newContent := applyPlans(content, plans)
	if err := os.WriteFile(params.FilePath, []byte(newContent), info.Mode()); err != nil {
		return agent.Result{}, fmt.Errorf("writing %s: %w", params.FilePath, err)
	}

	// Refresh FileState so a subsequent Edit on the same file still passes Check.
	newInfo, err := os.Stat(params.FilePath)
	if err == nil {
		tc.FileState.Put(params.FilePath, newInfo.ModTime(), []byte(newContent))
	}

	// One file-change event per batch — the natural unit. /undo wants
	// atomic revert; per-edit events would all share the same MessageID/
	// ToolCallID anyway. Failure to persist is not fatal.
	_ = tc.Sink().Record(ctx, agent.FileChangeEvent{
		MessageID:     tc.MessageID,
		ToolCallID:    tc.ToolCallID,
		Path:          params.FilePath,
		Op:            "modify",
		ContentBefore: data,
		ContentAfter:  []byte(newContent),
		Mode:          uint32(info.Mode().Perm()),
	})

	s := fmt.Sprintf("Successfully edited %s", params.FilePath)
	return agent.Result{
		Content:        s,
		Bytes:          len(s),
		DisplaySummary: editDisplaySummary(plans, params.FilePath),
	}, nil
}

// editDisplaySummary renders the per-call line shown beneath the tool
// row in the TUI: total +/- lines and an edit count when the batch is
// >1. Single-form edits read the same as before this phase.
func editDisplaySummary(plans []editPlan, filePath string) string {
	added, removed := 0, 0
	for _, p := range plans {
		added += newlineDelta(p.newStr)
		removed += newlineDelta(p.oldStr)
	}
	base := filepath.Base(filePath)
	if len(plans) > 1 {
		return fmt.Sprintf("+%d −%d lines · %d edits · %s", added, removed, len(plans), base)
	}
	return fmt.Sprintf("+%d −%d lines · %s", added, removed, base)
}

// newlineDelta counts the line-impact of a string. Mirrors the per-edit
// math the previous implementation used: every \n contributes one line,
// plus one extra when the string is non-empty and doesn't end in \n
// (the trailing partial line that the diff would still display).
func newlineDelta(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
