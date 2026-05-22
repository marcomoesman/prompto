package agent

import (
	"fmt"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
)

// defaultStaleToolCalls is the tool-call window TodoWriteStaleChecker
// uses by default before nudging the model to update its todo list.
// Tuned to fire while the model is still mid-stride on a multi-step
// task.
const defaultStaleToolCalls = 5

// PlanModeChecker fires every turn while the agent is plan-mode.
// Two distinct messages reflect the two relevant states:
//
//   - Pre-write: the model is in plan mode but hasn't written a plan
//     file yet. The reminder asks it to pick a slug and write to
//     `.prompto/plans/YYYY-MM-DD-<slug>.md`. The run loop persists
//     the model-chosen path on
//     first plan-file write so subsequent turns transition to the
//     post-write state.
//   - Post-write: the plan file exists. The reminder cites the path
//     so the model has the exact file to update on this turn.
//
// `rc.InPlanMode == false` returns empty (no reminder for non-plan
// agents).
type PlanModeChecker struct{}

func (PlanModeChecker) Name() string { return "plan_mode" }

func (PlanModeChecker) Check(rc PreTurnContext) string {
	if !rc.InPlanMode {
		return ""
	}
	if rc.PlanFilePath == "" {
		return "You're in plan mode. Pick a short slug describing this work (2–5 hyphenated lowercase words) and write your plan to `.prompto/plans/YYYY-MM-DD-<slug>.md` using the `write` tool. Use today's date. Investigate first with read-only tools and `task` (subagent_type: \"explore\") for parallel research."
	}
	return fmt.Sprintf("You're in plan mode. The plan file is `%s`. Update it with `edit` as you iterate. Do not edit any other files.", rc.PlanFilePath)
}

// VerifyAfterEditChecker fires when the most recent assistant turn
// invoked edit/write but did not invoke bash, AND no bash call has
// occurred since. Stateless: walks the conversation each turn rather
// than carrying flags.
type VerifyAfterEditChecker struct{}

func (VerifyAfterEditChecker) Name() string { return "verify_after_edit" }

func (VerifyAfterEditChecker) Check(rc PreTurnContext) string {
	if rc.Conversation == nil {
		return ""
	}
	msgs := rc.Conversation.Messages()
	editIdx := lastAssistantWithToolUseAny(msgs, "edit", "write")
	if editIdx < 0 {
		return ""
	}
	// If a bash call occurred at or after the edit, no nudge.
	if assistantUsedToolSince(msgs, editIdx, "bash") {
		return ""
	}
	if rc.Verification.Present() {
		var quoted []string
		for _, cmd := range rc.Verification.Commands {
			quoted = append(quoted, "`"+cmd+"`")
		}
		return "You edited code without running tests. Per the workflow, run the project's verification command(s): " + strings.Join(quoted, ", ") + ". Report results before ending the turn."
	}
	return "You edited code without running tests. Per the workflow, run the project's tests and static checks (e.g. `go test ./...`, `go vet ./...`) and report results before ending the turn."
}

// TodoWriteStaleChecker fires when the conversation has accumulated
// Threshold or more tool calls since the last todowrite invocation
// (counted against the entire history when no todowrite has yet
// occurred). Subagents never see this — the tool itself isn't in their
// resolver and rc.AgentName won't be "build"/"plan", so the early-return
// keeps the nudge scoped to primaries.
type TodoWriteStaleChecker struct {
	// Threshold is the count past which the reminder fires. Zero falls
	// back to defaultStaleToolCalls.
	Threshold int
}

func (TodoWriteStaleChecker) Name() string { return "todowrite_stale" }

func (c TodoWriteStaleChecker) Check(rc PreTurnContext) string {
	if rc.Conversation == nil {
		return ""
	}
	threshold := c.Threshold
	if threshold <= 0 {
		threshold = defaultStaleToolCalls
	}
	count := toolCallsSinceLastNamed(rc.Conversation.Messages(), "todowrite")
	if count < threshold {
		return ""
	}
	return "Update your todo list with TodoWrite. You've made several tool calls without recording progress; the list is your scratchpad across compaction boundaries."
}

// WebVsLocalChecker fires the turn after the agent ran webfetch.
// Reinforces the local-vs-web tool boundary so the model doesn't
// conflate URLs with filesystem paths on the next turn.
type WebVsLocalChecker struct{}

func (WebVsLocalChecker) Name() string { return "web_vs_local" }

func (WebVsLocalChecker) Check(rc PreTurnContext) string {
	if rc.Conversation == nil {
		return ""
	}
	msgs := rc.Conversation.Messages()
	idx := lastAssistantWithToolUseAny(msgs, "webfetch")
	if idx < 0 {
		return ""
	}
	// Only fire when webfetch was the most recent tool-bearing assistant
	// turn (no later tool call has happened yet).
	if hasLaterAssistantToolUse(msgs, idx) {
		return ""
	}
	return "You just used a web tool. Reminder: `read`/`grep`/`glob`/`list` are LOCAL filesystem only — never pass URLs to them. Use `webfetch` for any remote content."
}

// lastAssistantWithToolUseAny returns the index of the most recent
// assistant message that contains a tool_use block whose Name matches
// any entry in names. Returns -1 when none.
func lastAssistantWithToolUseAny(msgs []api.Message, names ...string) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != api.RoleAssistant {
			continue
		}
		if assistantMessageUsesAny(m, names...) {
			return i
		}
	}
	return -1
}

// assistantUsedToolSince walks msgs from sinceIdx forward and returns
// true if any assistant message after sinceIdx uses one of names.
// Excludes sinceIdx itself.
func assistantUsedToolSince(msgs []api.Message, sinceIdx int, names ...string) bool {
	for i := sinceIdx + 1; i < len(msgs); i++ {
		if msgs[i].Role != api.RoleAssistant {
			continue
		}
		if assistantMessageUsesAny(msgs[i], names...) {
			return true
		}
	}
	return false
}

// hasLaterAssistantToolUse reports whether any assistant message after
// sinceIdx uses any tool at all. Used by the web-vs-local checker to
// scope the reminder to "previous-turn webfetch only."
func hasLaterAssistantToolUse(msgs []api.Message, sinceIdx int) bool {
	for i := sinceIdx + 1; i < len(msgs); i++ {
		if msgs[i].Role != api.RoleAssistant {
			continue
		}
		for _, blk := range msgs[i].Content {
			if blk.Type == api.BlockToolUse {
				return true
			}
		}
	}
	return false
}

// assistantMessageUsesAny returns true when m carries a tool_use block
// whose name matches any entry in names.
func assistantMessageUsesAny(m api.Message, names ...string) bool {
	for _, blk := range m.Content {
		if blk.Type != api.BlockToolUse || blk.ToolCall == nil {
			continue
		}
		for _, n := range names {
			if strings.EqualFold(blk.ToolCall.Name, n) {
				return true
			}
		}
	}
	return false
}

// toolCallsSinceLastNamed counts tool_use blocks that appear after the
// most recent occurrence of name. When name has never been called, the
// total tool_use count across msgs is returned.
func toolCallsSinceLastNamed(msgs []api.Message, name string) int {
	lastIdx := -1
	for i, m := range msgs {
		if m.Role != api.RoleAssistant {
			continue
		}
		for _, blk := range m.Content {
			if blk.Type != api.BlockToolUse || blk.ToolCall == nil {
				continue
			}
			if strings.EqualFold(blk.ToolCall.Name, name) {
				lastIdx = i
			}
		}
	}
	count := 0
	for i := lastIdx + 1; i < len(msgs); i++ {
		if msgs[i].Role != api.RoleAssistant {
			continue
		}
		for _, blk := range msgs[i].Content {
			if blk.Type == api.BlockToolUse && blk.ToolCall != nil {
				// Don't count a fresh todowrite call against itself.
				if strings.EqualFold(blk.ToolCall.Name, name) {
					continue
				}
				count++
			}
		}
	}
	return count
}
