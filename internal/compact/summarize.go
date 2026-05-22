package compact

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
)

// NoToolsPreamble forbids the summarization model from calling tools.
// Emphatic phrasing; Anthropic models respect it reliably. Weaker models
// may still emit tool_use blocks, which we ignore.
const NoToolsPreamble = `You are producing a summary of a prior conversation.
Do NOT call any tools. Do NOT output tool_use blocks. Produce only a single summary text block.`

// SummaryTemplate wraps the conversation and instructs the summarizer to
// produce a structured summary that the next turn (and any subsequent
// re-compaction) can navigate efficiently. `## Files Touched` is a
// prompto-specific addition — file paths are load-bearing for the
// coding-focused use case, and tracking them in their own section makes
// resume a straight lookup instead of a re-read.
//
// On re-compaction (when the head being summarized already contains a
// prior <compact_summary> block), the model is instructed to PRESERVE
// the prior Goal/Constraints, CARRY FORWARD Decisions and Files
// Touched (adding new entries), UPDATE Progress (move In Progress →
// Done), and REPLACE Next Steps. The prior summary appears verbatim
// in the rendered conversation (renderConversationAsText passes user-
// text-blocks through unchanged), so the model has the prior state to
// work from.
//
// Sessions resumed from disk after an old-template summary will produce
// a new-format summary on their first re-compaction; the "If the
// conversation already contains" clause is permissive enough to read
// the old format as best the model can.
const SummaryTemplate = `Summarize the conversation below into a single compact block using this schema:

## Goal
## Constraints & Preferences
## Progress
### Done
### In Progress
### Blocked
## Key Decisions
## Next Steps
## Files Touched
## Critical Context

Rules:
- Omit any section that has no items rather than writing "none" or leaving the heading empty.
- Files Touched: list every file path the conversation read, edited, or wrote, one per line, with file:line refs where load-bearing.
- Critical Context: include error messages verbatim, version numbers, library quirks, and any fact the next turn needs that doesn't fit elsewhere.

If the conversation already contains a <compact_summary>...</compact_summary> block (from an earlier compaction), this is a re-compaction: PRESERVE Goal and Constraints unchanged unless the user explicitly changed them; CARRY FORWARD Key Decisions and Files Touched, ADDING new entries; UPDATE Progress (move completed In Progress items to Done; add new In Progress items); REPLACE Next Steps with the current plan. Discard prior items only when they have been explicitly invalidated.

The reader is the assistant on the next turn, not the user. Be terse. Use file:line references.

<conversation>
%s
</conversation>`

// CompactSummaryOpen and CompactSummaryClose are the XML tags wrapped around
// the summary when it goes back into the conversation as a RoleUser message.
const (
	CompactSummaryOpen  = "<compact_summary>"
	CompactSummaryClose = "</compact_summary>"
)

// IsCompactSummary reports whether a message is a synthetic compaction
// summary. Used by the TUI to render it as a collapsed system line instead
// of the raw XML.
func IsCompactSummary(m api.Message) bool {
	if m.Role != api.RoleUser {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(m.Text()), CompactSummaryOpen)
}

// SummarizeInput bundles the inputs to Summarize.
type SummarizeInput struct {
	Ctx       context.Context
	Provider  api.Provider
	Model     string        // summarizer model; may differ from session model
	Messages  []api.Message // the "head" to compact
	MaxTokens int           // output cap; defaults to 4096 when 0
}

// Summarize runs a non-tool provider call to compact the given messages
// into a single synthetic RoleUser message wrapped in <compact_summary>.
// Errors bubble up; an empty response is treated as a failure.
func Summarize(in SummarizeInput) (api.Message, error) {
	if in.Provider == nil {
		return api.Message{}, errors.New("compact: Summarize requires a Provider")
	}
	if in.Model == "" {
		return api.Message{}, errors.New("compact: Summarize requires a Model")
	}
	if len(in.Messages) == 0 {
		return api.Message{}, errors.New("compact: Summarize requires at least one message to compact")
	}
	// 6144 default accommodates the structured template's slightly
	// heavier output, especially the monotonically-growing Done list
	// across re-compactions.
	maxTokens := cmp.Or(in.MaxTokens, 6144)

	body := renderConversationAsText(in.Messages)
	userPrompt := fmt.Sprintf(SummaryTemplate, body)

	params := api.CompleteParams{
		Model:     in.Model,
		System:    []api.SystemBlock{{Text: NoToolsPreamble}},
		Messages:  []api.Message{api.NewUserMessage(userPrompt)},
		MaxTokens: maxTokens,
		Tools:     nil,
	}

	var result strings.Builder
	var streamErr error
	for ev := range in.Provider.Complete(in.Ctx, params) {
		switch ev.Type {
		case api.EventDelta:
			result.WriteString(ev.Delta)
		case api.EventError:
			streamErr = ev.Error
		}
	}
	if streamErr != nil {
		return api.Message{}, fmt.Errorf("compact: summarize: %w", streamErr)
	}
	summary := strings.TrimSpace(result.String())
	if summary == "" {
		return api.Message{}, errors.New("compact: summarize returned empty response")
	}

	summary = trimDoneListIfOversize(summary)

	return api.NewUserMessage(CompactSummaryOpen + "\n" + summary + "\n" + CompactSummaryClose), nil
}

// summaryMaxChars is the soft size cap above which the Done list gets
// trimmed. Picked to roughly correspond to ~4096 tokens at the
// charsPerToken=4 ratio used elsewhere in this package.
const summaryMaxChars = 16 * 1024

// doneSectionRetainEntries is the number of trailing Done entries kept
// when a re-compacted summary exceeds summaryMaxChars. Most recent
// completions are the ones the next turn is likely to reference; older
// completions are the load-bearing case for trimming since the Done list
// grows monotonically across re-compactions.
const doneSectionRetainEntries = 12

// trimDoneListIfOversize bounds the rendered summary's char count by
// trimming the oldest entries from the ### Done subsection. Other
// sections (Goal, Constraints, Files Touched, etc.) pass through. If the
// summary is below summaryMaxChars or has no recognizable Done section,
// it returns unchanged.
func trimDoneListIfOversize(summary string) string {
	if len(summary) <= summaryMaxChars {
		return summary
	}
	doneStart := strings.Index(summary, "### Done")
	if doneStart < 0 {
		return summary
	}
	// Find the next "###" or "##" header that ends the Done section.
	// Scan from the line after the Done heading.
	tail := summary[doneStart:]
	headerEnd := strings.Index(tail, "\n")
	if headerEnd < 0 {
		return summary
	}
	body := tail[headerEnd+1:]
	endRel := -1
	for i := 0; i < len(body); {
		nl := strings.Index(body[i:], "\n")
		if nl < 0 {
			break
		}
		next := i + nl + 1
		if next < len(body) && (strings.HasPrefix(body[next:], "## ") || strings.HasPrefix(body[next:], "### ")) {
			endRel = next
			break
		}
		i = next
	}
	if endRel < 0 {
		endRel = len(body)
	}
	doneBody := body[:endRel]
	rest := body[endRel:]

	lines := strings.Split(doneBody, "\n")
	// Keep only entries that look like list items (start with "-" or "*").
	// Trim oldest, keep newest doneSectionRetainEntries.
	var items []string
	var nonItems []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") {
			items = append(items, ln)
		} else {
			nonItems = append(nonItems, ln)
		}
	}
	if len(items) <= doneSectionRetainEntries {
		return summary
	}
	dropped := len(items) - doneSectionRetainEntries
	items = items[dropped:]

	var rebuilt strings.Builder
	for _, ln := range nonItems {
		rebuilt.WriteString(ln)
		rebuilt.WriteByte('\n')
	}
	fmt.Fprintf(&rebuilt, "- (%d earlier completions elided to bound summary growth)\n", dropped)
	for _, ln := range items {
		rebuilt.WriteString(ln)
		rebuilt.WriteByte('\n')
	}

	return summary[:doneStart] + tail[:headerEnd+1] + rebuilt.String() + rest
}

// renderConversationAsText produces a plain-text serialization of the
// message list for inclusion in the summarization prompt. Tool_use blocks
// render as `[tool: <name>] <input>`; tool_results render as the content
// text; plain text passes through. We deliberately don't recreate the JSON
// wire format because the summarizer reads it as prose, not as API calls.
func renderConversationAsText(msgs []api.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		for _, block := range m.Content {
			switch block.Type {
			case api.BlockText:
				b.WriteString(block.Text)
			case api.BlockToolUse:
				if block.ToolCall != nil {
					b.WriteString("[tool: ")
					b.WriteString(block.ToolCall.Name)
					b.WriteString("] ")
					b.Write(block.ToolCall.Input)
				}
			case api.BlockToolResult:
				if block.ToolResult != nil {
					if block.ToolResult.IsError {
						b.WriteString("[tool_error] ")
					} else {
						b.WriteString("[tool_result] ")
					}
					b.WriteString(block.ToolResult.Content)
				}
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return b.String()
}
