package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/privatefs"
	"github.com/marcomoesman/prompto/internal/tool"
)

const (
	summarizePrompt = `You are a web page summarizer. Given the raw markdown content of a web page, produce a clear and concise summary.

Rules:
- Focus on the MAIN content of the page, ignoring navigation, ads, footers, sign-in prompts, and other chrome.
- If the user's query provides context, focus the summary on information relevant to their query.
- Include key facts, descriptions, code examples, and important links.
- Preserve technical details that would be useful to a developer.
- Keep the summary under 2000 words.
- Use markdown formatting.
- Do NOT include navigation links, sidebar content, or repetitive UI text.`

	// Max content sent to the summarizer LLM. Sized to match
	// htmlMaxOutput in the tool package so the summarizer sees whatever
	// the fetch pipeline kept after readability extraction — head-
	// truncation here used to hide the real content of nav-heavy pages
	// (e.g. github.com/trending) behind login chrome.
	summarizeMaxInput = 200 * 1024 // 200KB
	// Max content returned when summarization fails.
	fallbackMaxOutput = 3 * 1024 // 3KB

	// Note appended to a successful-but-truncated summary so the calling
	// agent (and a human reading fetch logs) knows the answer is partial.
	// The summarizer's MaxTokens cap is shared between visible output and
	// any reasoning tokens the model emits, so this trips most often on
	// reasoning models with verbose output formats.
	lengthTruncatedNote = "\n\n[Note: summarization truncated by max_tokens — output is partial. Re-run with a more compact format or smaller scope to get the full result.]"
)

// newSummarizer creates a tool.Summarizer that uses the given provider and
// model to summarize web content in a separate LLM thread. It lives in the
// main package (wiring concern), not in internal/agent, to preserve the
// agent → no-tool-package invariant.
//
// maxTokens is the output cap for the summarizer call; it comes from the
// active model's config.ModelEntry.MaxTokens (validated > 0 in
// config.Load). Reasoning models that emit <think> tokens before the
// answer need this set generously — they share the budget with visible
// output.
func newSummarizer(provider api.Provider, model string, maxTokens int) tool.Summarizer {
	return func(ctx context.Context, content, query string) (string, error) {
		start := time.Now()

		// Truncate input to fit in the summarizer's context window.
		// Cut on a UTF-8 rune boundary so the downstream JSON encoder
		// doesn't replace half-runes at the seam with U+FFFD.
		truncatedContent := content
		if len(truncatedContent) > summarizeMaxInput {
			cut := summarizeMaxInput
			for cut > 0 && !utf8.RuneStart(truncatedContent[cut]) {
				cut--
			}
			truncatedContent = truncatedContent[:cut] + "\n\n[Page content truncated for summarization]"
		}

		prompt := "Summarize the following web page content."
		if query != "" {
			prompt = query
		}

		userMsg := api.NewUserMessage(fmt.Sprintf("%s\n\n---\n\n%s", prompt, truncatedContent))

		params := api.CompleteParams{
			Model:     model,
			System:    []api.SystemBlock{{Text: summarizePrompt, Cache: true}},
			Messages:  []api.Message{userMsg},
			MaxTokens: maxTokens,
		}

		var resultB strings.Builder
		var eventCount int
		var lastError error
		var stopReason string
		var usage api.Usage
		for event := range provider.Complete(ctx, params) {
			eventCount++
			switch event.Type {
			case api.EventDelta:
				resultB.WriteString(event.Delta)
			case api.EventDone:
				// Last event before the stream closes. StopReason carries
				// the provider's finish flag — "length"/"max_tokens" means
				// the answer was cut off by our configured output cap, not
				// by the model deciding it was finished.
				stopReason = event.StopReason
			case api.EventUsage:
				if event.Usage != nil {
					usage = *event.Usage
				}
			case api.EventError:
				lastError = event.Error
			}
		}

		truncatedByLength := isLengthStop(stopReason)
		result := resultB.String()

		var (
			agentSees string
			errStr    string
		)
		switch {
		case lastError != nil:
			agentSees = truncateFallback(content, fmt.Sprintf("summarization error: %v", lastError))
			errStr = lastError.Error()
		case result == "":
			agentSees = truncateFallback(content, fmt.Sprintf("summarization returned empty result (received %d stream events, content length: %d chars)", eventCount, len(truncatedContent)))
			errStr = "empty result"
		default:
			agentSees = result
			if truncatedByLength {
				agentSees += lengthTruncatedNote
			}
		}

		writeFetchDebug(fetchDebugRecord{
			Timestamp:     start,
			DurationMs:    time.Since(start).Milliseconds(),
			Query:         query,
			Model:         model,
			InputBytes:    len(truncatedContent),
			OutputBytes:   len(agentSees),
			RawLLMOutput:  result,
			InputMarkdown: truncatedContent,
			AgentSees:     agentSees,
			StopReason:    stopReason,
			InputTokens:   usage.InputTokens,
			OutputTokens:  usage.OutputTokens,
			CacheRead:     usage.CacheRead,
			CacheWrite:    usage.CacheWrite,
			Error:         errStr,
		})

		return agentSees, nil
	}
}

// truncateFallback returns a small chunk of content with an error note.
// Cuts on a rune boundary so the snippet stays valid UTF-8.
func truncateFallback(content, reason string) string {
	if len(content) <= fallbackMaxOutput {
		return fmt.Sprintf("[Note: %s. Showing raw content below.]\n\n%s", reason, content)
	}
	cut := fallbackMaxOutput
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	return fmt.Sprintf("[Note: %s. Showing first %dKB of raw content.]\n\n%s", reason, fallbackMaxOutput/1024, content[:cut])
}

// fetchDebugRecord is one JSONL line in fetch-YYYY-MM-DD.jsonl. Captures
// everything needed to diagnose summarizer feedback loops without re-running:
// the exact bytes the LLM saw, what it produced, and what the agent ultimately
// got back. Sister of agent.RequestLogEntry but separately gated so users can
// debug fetch behaviour without flooding their disk with API request logs.
type fetchDebugRecord struct {
	Timestamp     time.Time `json:"timestamp"`
	DurationMs    int64     `json:"duration_ms"`
	Query         string    `json:"query"`
	Model         string    `json:"model"`
	InputBytes    int       `json:"input_bytes"`
	OutputBytes   int       `json:"output_bytes"`
	InputMarkdown string    `json:"input_markdown"`
	RawLLMOutput  string    `json:"raw_llm_output"`
	AgentSees     string    `json:"agent_sees"`
	// StopReason is the provider's finish flag for the LAST event of the
	// stream — typically "stop"/"end_turn" for normal completion,
	// "length"/"max_tokens" when the output cap was hit, "tool_use" when
	// a tool was requested. Empty when no EventDone arrived (e.g. error
	// path or some non-conforming providers).
	StopReason   string `json:"stop_reason,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	CacheRead    int    `json:"cache_read,omitempty"`
	CacheWrite   int    `json:"cache_write,omitempty"`
	Error        string `json:"error,omitempty"`
}

// isLengthStop reports whether the provider's stop reason indicates the
// response was cut off by the output token cap. OpenAI emits "length";
// Anthropic emits "max_tokens"; some local OpenAI-compatible servers
// pass either through. Anything else (including "stop"/"end_turn") is
// a clean finish.
func isLengthStop(stopReason string) bool {
	switch stopReason {
	case "length", "max_tokens":
		return true
	}
	return false
}

// writeFetchDebug appends one JSONL record to .prompto/logs/fetch-<date>.jsonl
// when PROMPTO_DEBUG_FETCH=1. All errors are swallowed: a broken debug log
// must never break a real fetch.
func writeFetchDebug(rec fetchDebugRecord) {
	if os.Getenv("PROMPTO_DEBUG_FETCH") != "1" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	dir := filepath.Join(cwd, ".prompto", "logs")
	if err := privatefs.EnsureDir(dir); err != nil {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("fetch-%s.jsonl", rec.Timestamp.Format("2006-01-02")))
	// 0o600: fetch debug payloads include rendered page content, raw LLM
	// output, and any query the user passed — readable to the owner only.
	f, err := privatefs.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}
