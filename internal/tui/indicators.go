package tui

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
)

// promptisms is the rotating set of words that replaces the bare
// "Thinking" label while StateThinking is active. AppModel.setState
// picks one at random on every transition INTO StateThinking; the
// pick persists for the duration of that thinking burst so the
// indicator doesn't flicker between renders. Index 0 ("Thinking") is
// the canonical fallback used when no word has been picked yet
// (e.g. tests that exercise renderIndicator directly).
var promptisms = []string{
	"Thinking",
	"Thonking",
	"Considering",
	"Bamboozeling",
	"Pondering",
	"Analyzing",
	"Deliberating",
	"Judging",
	"Figuring",
	"Rattling",
	"Reckoning",
	"Outlining",
	"Evaluating",
	"Operating",
	"Perplexing",
	"Comprehending",
	"Slaying",
	"Beaming",
	"Noticing",
	"Observing",
	"Perceiving",
	"Distinguishing",
	"Discerning",
	"Appraising",
	"Assessing",
	"Realizing",
	"Grasping",
	"Fathoming",
	"Wrangling",
	"Wondering",
	"Working",
}

// pickPromptism returns a uniformly-random word from the promptisms
// list. Called once per transition into StateThinking — not per
// render tick — so the chosen word is sticky for the burst.
func pickPromptism() string {
	return promptisms[rand.IntN(len(promptisms))]
}

// pickPromptismExcluding picks a random promptism that is NOT the
// supplied current value, so a long thinking burst rotating through
// the list never appears to "stick" (re-picking the same word would
// look like the rotation didn't happen). Falls back to the bare pick
// when the list has a single entry.
func pickPromptismExcluding(current string) string {
	if len(promptisms) <= 1 {
		return promptisms[0]
	}
	for {
		w := promptisms[rand.IntN(len(promptisms))]
		if w != current {
			return w
		}
	}
}

// shimmerCycleGap is the number of frames between sweeps where no
// bright spot is on the label — gives the eye a brief rest before the
// next pass and stops the effect from feeling like a strobe.
const shimmerCycleGap = 6

// shimmerPalette maps "distance from the bright spot" to a 256-color
// foreground. Index 0 is the peak; later indices fade toward the base
// dim color. Distances past the table fall through to the last entry.
// The palette is intentionally narrow (4 hot bands) so the highlight
// reads as a moving point rather than a wash.
var shimmerPalette = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true), // peak: pure white
	lipgloss.NewStyle().Foreground(lipgloss.Color("253")).Bold(true),
	lipgloss.NewStyle().Foreground(lipgloss.Color("249")).Bold(true),
	lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true),
	lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Bold(true), // base dim
}

// renderShimmerLabel paints `text` with a moving highlight. The
// bright spot's position advances one rune per frame, so callers
// drive the animation by passing an incrementing frame counter (we
// use the spinner-tick counter so the cadence matches the spinner
// glyph rotation already on the same row). Each cycle covers the
// label width plus shimmerCycleGap idle frames.
//
// Implementation: for every rune, compute the absolute distance
// between its column and the current bright-spot position, then
// pick the palette entry. Distance ≥ len(palette) → base color.
// When the bright spot has swept past the last rune (gap phase),
// every distance exceeds the palette and the whole label renders
// in the base color, giving the eye a beat before the next pass.
func renderShimmerLabel(text string, frame int) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	cycle := len(runes) + shimmerCycleGap
	if frame < 0 {
		// Defensive: a wrapped negative frame would still index
		// correctly with Go's modulo semantics (which preserves
		// sign), but the bright spot would jump backward. Normalize.
		frame = -frame
	}
	pos := frame % cycle
	last := len(shimmerPalette) - 1
	var b strings.Builder
	for i, r := range runes {
		d := i - pos
		if d < 0 {
			d = -d
		}
		b.WriteString(shimmerPalette[min(d, last)].Render(string(r)))
	}
	return b.String()
}

// WorkingState describes what the agent is doing right now. The TUI renders
// a one-line indicator above the input that maps each state to a verbose,
// recognizable label (Droid / Claude Code style). StateIdle hides the
// indicator entirely; the row is still reserved so input doesn't jump when
// transitions arrive.
type WorkingState int

const (
	StateIdle WorkingState = iota
	StateThinking
	StateStreaming
	StateToolRunning      // Detail = e.g. "Read foo.go", "Bash go test ./...".
	StateCompacting       // Compactor.MaybeCompact / ForceSummarize is running.
	StateAwaitingApproval // Detail = pending tool description.
)

// IsActive returns true when the indicator should be shown. Idle returns
// false; the layout still reserves the row to avoid jitter.
func (s WorkingState) IsActive() bool {
	return s != StateIdle
}

// label returns the verbose label for a state. Detail is appended for
// states that carry per-call context (tool name, approval target).
// thinkingWord, when non-empty, replaces the default "Thinking" label
// with one of the rotating promptisms — picked by setState on each
// transition into StateThinking so the choice is sticky for the burst.
func (s WorkingState) label(detail, thinkingWord string) string {
	switch s {
	case StateThinking:
		if thinkingWord != "" {
			return thinkingWord + "…"
		}
		return "Thinking…"
	case StateStreaming:
		return "Streaming response…"
	case StateToolRunning:
		if detail != "" {
			return detail + "…"
		}
		return "Running tool…"
	case StateCompacting:
		return "Compacting context…"
	case StateAwaitingApproval:
		if detail != "" {
			return "Approval needed: " + detail
		}
		return "Approval needed"
	default:
		return ""
	}
}

// renderIndicator returns the one-line indicator for the current state.
// Returns an empty string for StateIdle so the layout can keep the row
// reserved (caller pads to one line).
//
// For StateAwaitingApproval, a warning glyph is used instead of the
// spinner — the call is paused waiting on human input, not progressing.
//
// elapsed, when non-empty, is appended after the label as a dim
// "(<elapsed>)" suffix. Used by StateToolRunning to surface per-tool
// dwell time so a stalled tool is visually distinct from a slow one.
func renderIndicator(state WorkingState, detail, elapsed, spinnerFrame, thinkingWord string, shimmerFrame int) string {
	if !state.IsActive() {
		return ""
	}
	if state == StateAwaitingApproval {
		return indicatorAlertStyle.Render("⚠ " + state.label(detail, thinkingWord))
	}
	glyph := spinnerFrame
	if glyph == "" {
		glyph = "·"
	}
	label := state.label(detail, thinkingWord)
	var labelRendered string
	if state == StateThinking && thinkingWord != "" {
		// Shimmer the promptism so a long thinking burst feels alive
		// rather than statically pinned. Other states use the flat
		// indicatorTextStyle — only the thinking burst needs the
		// "look at me, I'm working" affordance.
		labelRendered = renderShimmerLabel(label, shimmerFrame)
	} else {
		labelRendered = indicatorTextStyle.Render(label)
	}
	out := indicatorStyle.Render(glyph+" ") + labelRendered
	if elapsed != "" {
		out += " " + dimStyle.Render("("+elapsed+")")
	}
	return out
}

// summarizeToolCall produces the short verbose detail used in
// `StateToolRunning` (e.g. "Read foo.go", "Edit cmd/prompto/main.go",
// "Bash go test ./..."). Falls back to the bare tool name when the
// argument shape is unfamiliar. Truncates to ~40 chars to keep the
// single-row indicator readable.
func summarizeToolCall(name, argsJSON string) string {
	if name == "" {
		return "Tool"
	}
	display := strings.ToUpper(name[:1]) + name[1:]
	args := map[string]any{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err == nil {
		switch name {
		case "read", "edit", "replace_lines", "write":
			if fp, ok := args["file_path"].(string); ok {
				return summarizeTrunc(fmt.Sprintf("%s %s", display, fp), 40)
			}
		case "bash":
			if cmd, ok := args["command"].(string); ok {
				// Bash is shell-execution. The approval indicator is the
				// last thing the user reads before pressing [y], so the
				// full command must always be visible — even if it wraps
				// the indicator over several rows.
				return fmt.Sprintf("%s %s", display, cmd)
			}
		case "grep":
			if pat, ok := args["pattern"].(string); ok {
				return summarizeTrunc(fmt.Sprintf("%s %s", display, pat), 40)
			}
		case "glob":
			if pat, ok := args["pattern"].(string); ok {
				return summarizeTrunc(fmt.Sprintf("%s %s", display, pat), 40)
			}
		case "list":
			if p, ok := args["path"].(string); ok {
				return summarizeTrunc(fmt.Sprintf("%s %s", display, p), 40)
			}
		case "webfetch", "webfetch_headless":
			if u, ok := args["url"].(string); ok {
				return summarizeTrunc(fmt.Sprintf("%s %s", display, u), 40)
			}
		case "task":
			// task spawns a named subagent. Mirror the chat row's
			// Explore("...") / Research("...") shape so the approval and
			// running displays do not expose raw JSON field names.
			sub, _ := args["subagent_type"].(string)
			desc, _ := args["description"].(string)
			sub = taskDisplayName(sub)
			desc = strings.TrimSpace(desc)
			switch {
			case sub != "Task" && desc != "":
				return summarizeTrunc(fmt.Sprintf("%s(%q)", sub, desc), 40)
			case sub != "Task":
				return sub
			}
		case "todowrite":
			// todowrite swaps the entire list. Show the new tally so the
			// indicator mirrors the post-call status-bar segment.
			p, ip, d := tallyTodoArgs(args)
			return summarizeTrunc(fmt.Sprintf("TodoWrite ☐%d ▶%d ✓%d", p, ip, d), 40)
		}
	}
	return display
}

func taskDisplayName(subagentType string) string {
	subagentType = strings.TrimSpace(subagentType)
	if subagentType == "" {
		return "Task"
	}
	return strings.ToUpper(subagentType[:1]) + subagentType[1:]
}

// describeRunningTool produces the gerund-style detail used while a
// tool is executing — "Reading page content from example.com",
// "Running go test ./...", "Editing main.go". The phrase reads as the
// agent's current action, in contrast to summarizeToolCall which uses
// the noun form expected by approval prompts ("Read foo.go"). When the
// tool name or argument shape is unknown, falls back to a verbose
// "Running <name>" so the indicator never collapses back to a generic
// "Thinking…" while a tool is mid-flight.
func describeRunningTool(name, argsJSON string) string {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	switch name {
	case "read":
		if fp, ok := args["file_path"].(string); ok {
			return summarizeTrunc("Reading "+fp, 40)
		}
		return "Reading file"
	case "edit":
		if fp, ok := args["file_path"].(string); ok {
			return summarizeTrunc("Editing "+fp, 40)
		}
		return "Editing file"
	case "replace_lines":
		if fp, ok := args["file_path"].(string); ok {
			return summarizeTrunc("Editing "+fp, 40)
		}
		return "Editing file"
	case "write":
		if fp, ok := args["file_path"].(string); ok {
			return summarizeTrunc("Writing "+fp, 40)
		}
		return "Writing file"
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			return summarizeTrunc("Running "+cmd, 40)
		}
		return "Running shell command"
	case "grep":
		if pat, ok := args["pattern"].(string); ok {
			return summarizeTrunc(fmt.Sprintf("Searching for %q", pat), 40)
		}
		return "Searching"
	case "glob":
		if pat, ok := args["pattern"].(string); ok {
			return summarizeTrunc(fmt.Sprintf("Finding files %q", pat), 40)
		}
		return "Finding files"
	case "list":
		if p, ok := args["path"].(string); ok {
			return summarizeTrunc("Listing "+p, 40)
		}
		return "Listing directory"
	case "webfetch", "webfetch_headless":
		if u, ok := args["url"].(string); ok {
			if host := webfetchHost(u); host != "" {
				return summarizeTrunc("Reading page content from "+host, 40)
			}
		}
		return "Reading page content"
	case "task":
		// The dispatched subagent's own tool calls already render with
		// the "Explore →" / "Research →" prefix in the chat, so the
		// running indicator just needs to convey "we're spawning a
		// child" — the description and parens were noise.
		sub, _ := args["subagent_type"].(string)
		sub = strings.ToLower(strings.TrimSpace(sub))
		if sub == "" {
			return "Dispatching agent"
		}
		return summarizeTrunc("Dispatching "+sub+" agent", 40)
	case "todowrite":
		p, ip, d := tallyTodoArgs(args)
		return summarizeTrunc(fmt.Sprintf("Updating todos ☐%d ▶%d ✓%d", p, ip, d), 40)
	}
	if name == "" {
		return "Running tool"
	}
	return summarizeTrunc("Running "+name, 40)
}

// webfetchHost extracts the host portion of a URL for the running
// indicator. Returns "" when the URL is unparseable so the caller can
// fall back to a generic phrase.
func webfetchHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// tallyTodoArgs counts pending/in-progress/done from the loosely-typed
// `{"todos":[{"status":"…"},…]}` shape produced by json.Unmarshal into
// map[string]any. Unknown / missing statuses fall through to "pending"
// so the indicator never reports zero on a malformed payload.
func tallyTodoArgs(args map[string]any) (pending, inProgress, done int) {
	raw, ok := args["todos"].([]any)
	if !ok {
		return
	}
	for _, item := range raw {
		td, ok := item.(map[string]any)
		if !ok {
			continue
		}
		status, _ := td["status"].(string)
		switch status {
		case "in_progress":
			inProgress++
		case "completed":
			done++
		default:
			pending++
		}
	}
	return
}

// summarizeTrunc caps s at max RUNES so multibyte tool arguments
// (paths, bash commands, search patterns with non-ASCII) don't get
// chopped mid-codepoint into invalid UTF-8 — lipgloss renders that as
// U+FFFD in the indicator row.
func summarizeTrunc(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

// newSpinner returns the configured indicator spinner (MiniDot braille).
func newSpinner() spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	return s
}
