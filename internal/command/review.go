package command

import (
	"context"
	"strings"
)

// ReviewCommand asks the agent to review recent file changes — either the
// session's edits or, when args specify a target, a git ref. KindExpanding:
// synthesizes the review prompt and feeds it through agent.Run.
type ReviewCommand struct{}

// NewReviewCommand returns a /review command.
func NewReviewCommand() Command { return ReviewCommand{} }

// Name returns the canonical name.
func (ReviewCommand) Name() string { return "review" }

// Aliases lists alternate names.
func (ReviewCommand) Aliases() []string { return nil }

// Kind reports KindExpanding.
func (ReviewCommand) Kind() Kind { return KindExpanding }

// Help is the one-liner.
func (ReviewCommand) Help() string { return "review recent changes (optional: <ref> or path)" }

// Exec returns the review prompt, optionally targeted at args.
func (ReviewCommand) Exec(_ context.Context, args []string, _ Env) (Result, error) {
	target := strings.TrimSpace(strings.Join(args, " "))
	if target == "" {
		return Result{Prompt: reviewPromptCurrent}, nil
	}
	return Result{Prompt: reviewPromptHeader + " Target: " + target + "\n\n" + reviewPromptBody}, nil
}

const reviewPromptHeader = "Review the requested changes."

const reviewPromptCurrent = `Review the file changes made in this session so far.

Use git status / git diff (or read the files directly) to inspect what's changed. Look for: bugs, missed edge cases, style inconsistencies vs. the rest of the codebase, dead code, and any commented-out blocks that should be removed. If tests exist for the changed code, check whether they cover the new behavior.

Report findings as a short, actionable list. If everything looks good, say so plainly.`

const reviewPromptBody = `Use git diff (and direct file reads as needed) to inspect what's changed at the specified target. Look for: bugs, missed edge cases, style inconsistencies vs. the rest of the codebase, dead code, and any commented-out blocks that should be removed.

Report findings as a short, actionable list. If everything looks good, say so plainly.`
