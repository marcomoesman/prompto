package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/marcomoesman/prompto/internal/compact"
)

// ContextCommand renders an estimate of the token budget by category so the
// user can decide whether to /compact, prune AGENTS.md, or move on. The
// numbers are estimates from compact.EstimateMessage — close enough for a
// budgeting decision but never exact.
type ContextCommand struct{}

// NewContextCommand returns a /context command.
func NewContextCommand() Command { return ContextCommand{} }

// Name returns the canonical name.
func (ContextCommand) Name() string { return "context" }

// Aliases lists alternate names.
func (ContextCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (ContextCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (ContextCommand) Help() string { return "show token-budget breakdown by category" }

// Exec assembles the breakdown report and returns it as a system message.
func (ContextCommand) Exec(_ context.Context, _ []string, env Env) (Result, error) {
	conv := env.Conversation()
	systemPrompt := env.SystemPromptText()
	agentsMd := env.AGENTSMdText()

	systemTokens := approxTokens(systemPrompt)
	agentsTokens := approxTokens(agentsMd)
	convTokens := compact.EstimateConversation(conv)
	total := systemTokens + convTokens // AGENTS.md contributes via the system prompt; not double-counted

	var limit int
	if c := env.Compactor(); c != nil {
		limit = c.ContextLimit(env.Model())
	}

	var b strings.Builder
	b.WriteString("context budget\n")
	fmt.Fprintf(&b, "  system prompt:  ~%d tokens\n", systemTokens)
	fmt.Fprintf(&b, "  AGENTS.md:      ~%d tokens (included in system)\n", agentsTokens)
	fmt.Fprintf(&b, "  conversation:   ~%d tokens\n", convTokens)
	fmt.Fprintf(&b, "  total:          ~%d tokens", total)
	if limit > 0 {
		pct := total * 100 / limit
		fmt.Fprintf(&b, " of %d (%d%%)", limit, pct)
	}
	return Result{Message: b.String()}, nil
}

// approxTokens applies the same chars-per-token heuristic used by
// internal/compact so the breakdown is consistent with the status bar's
// `context: NN%` indicator.
func approxTokens(s string) int { return len(s) / 4 }
