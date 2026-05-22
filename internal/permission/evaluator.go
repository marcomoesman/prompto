package permission

import (
	"sync"

	"github.com/marcomoesman/prompto/internal/agent"
)

// Evaluator is the single authoritative decision point combining mode,
// ruleset, and the protected-file guard. Construct once at startup and
// share across the agent loop (reads are concurrent-safe under RWMutex).
type Evaluator struct {
	mu      sync.RWMutex
	mode    Mode
	ruleset *Ruleset
	// agentRules layer on top of the ruleset. They are not persisted and
	// are replaced wholesale on agent switch via SetAgentRules. Last-
	// matching-wins ordering means agent rules can override an Allow from
	// a lower scope — that's intentional for plan mode.
	agentRules []Rule
	// bashClassifier, when non-nil, fast-paths bash decisions before
	// the ruleset runs: ReadOnly → Allow, Mutating → Deny, Unknown →
	// fall through. Installed by the TUI when entering plan mode and
	// cleared on the way out so build-mode bash semantics are
	// unchanged.
	bashClassifier func(string) BashClass
}

// NewEvaluatorInput bundles the constructor parameters.
type NewEvaluatorInput struct {
	Mode    Mode
	Ruleset *Ruleset
}

// NewEvaluator returns an Evaluator. A nil Ruleset is replaced with an
// empty one for safety; tools that evaluate against a nil ruleset would
// otherwise panic.
func NewEvaluator(in NewEvaluatorInput) *Evaluator {
	rs := in.Ruleset
	if rs == nil {
		rs = NewRuleset()
	}
	return &Evaluator{mode: in.Mode, ruleset: rs}
}

// Mode returns the currently-configured mode.
func (e *Evaluator) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// SetMode updates the mode. Safe for concurrent callers (e.g., Ctrl+Y from
// the TUI while the agent loop is reading).
func (e *Evaluator) SetMode(m Mode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = m
}

// Ruleset returns the underlying ruleset (live — mutations are visible
// here immediately).
func (e *Evaluator) Ruleset() *Ruleset { return e.ruleset }

// SetAgentRules replaces the agent-scoped rule slice atomically. Pass nil
// to clear (e.g., when switching back to a primary that imposes no extra
// constraints). The supplied rules are stamped with ScopeAgent so
// downstream introspection (audit logs, future TUI display) sees the
// correct scope regardless of caller hygiene.
func (e *Evaluator) SetAgentRules(rules []Rule) {
	cloned := make([]Rule, len(rules))
	for i, r := range rules {
		r.Scope = ScopeAgent
		cloned[i] = r
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agentRules = cloned
}

// AgentRules returns a snapshot of the currently-installed agent rules.
// Mainly useful for tests and for the TUI's permissions panel.
func (e *Evaluator) AgentRules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Rule, len(e.agentRules))
	copy(out, e.agentRules)
	return out
}

// SetBashClassifier installs (or, with nil, clears) the bash fast-path
// classifier. Plan-mode entry installs ClassifyBash; the build-side
// path clears it so build-mode prompts use the ruleset alone.
func (e *Evaluator) SetBashClassifier(f func(string) BashClass) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bashClassifier = f
}

// Evaluate returns the authoritative decision for a tool call. Resolution
// order:
//  1. Mode = Bypass → Allow (no ruleset consultation).
//  2. Auto-allow tools (todowrite) → Allow.
//  3. Protected-path check: non-read-only tool touching a protected path
//     → Deny unconditionally, regardless of ruleset. Read-only tools fall
//     through.
//  4. Mode = AcceptEdits + Tool in {"edit", "replace_lines", "write"} → Allow.
//  5. Bash fast-path (plan-mode only): classifier ReadOnly → Allow,
//     Mutating → Deny, Unknown → fall through.
//  6. Ruleset evaluation, then agent-rule overlay (last-matching-wins).
//  7. Default: Ask.
//
// Inputs and outputs use agent.EvaluateInput / agent.EvaluateResult so the
// Evaluator structurally satisfies agent.PermissionEvaluator.
func (e *Evaluator) Evaluate(in agent.EvaluateInput) agent.EvaluateResult {
	e.mu.RLock()
	mode := e.mode
	e.mu.RUnlock()

	if mode == ModeBypass {
		return agent.EvaluateResult{Decision: agent.DecisionAllow, Reason: "bypass mode"}
	}

	// Auto-allow tools whose only side-effect is on prompto's own
	// session sidecar (todowrite). They have no security blast radius
	// — letting the model self-manage its task list without prompting
	// every turn matches what Claude Code, Cursor, and other agents
	// do with their planning tools.
	if isAutoAllowedTool(in.Tool) {
		return agent.EvaluateResult{Decision: agent.DecisionAllow, Reason: "auto-allowed (planning tool)"}
	}

	if !in.IsReadOnly && in.Key != "" && IsProtected(in.Key) {
		return agent.EvaluateResult{
			Decision: agent.DecisionDeny,
			Reason:   "protected path: " + in.Key,
		}
	}

	// Read-side hard-credential guard. The protected-globs check above
	// only fires on writes. A permissive `allow read **` rule would
	// otherwise let the model open ~/.ssh/id_rsa or *.pem. Force-deny
	// reads against the unambiguous credentials set even when a rule
	// would have allowed them.
	if in.IsReadOnly && in.Key != "" && IsReadProtected(in.Key) {
		return agent.EvaluateResult{
			Decision: agent.DecisionDeny,
			Reason:   "read-protected path: " + in.Key,
		}
	}

	if mode == ModeAcceptEdits {
		switch in.Tool {
		case "edit", "replace_lines", "write":
			// Consult the ruleset (and agent-rule overlay) BEFORE
			// short-circuiting to Allow. A user-authored deny rule
			// for these tools should still fire — acceptEdits is
			// "skip the prompt for benign edits", not "ignore my
			// deny rules". Only upgrade Ask → Allow; preserve Deny
			// and rule-driven Allow with their original reasons.
			decision, reason := e.evaluateRules(in)
			if decision == agent.DecisionAsk {
				return agent.EvaluateResult{Decision: agent.DecisionAllow, Reason: "acceptEdits mode (no rule matched)"}
			}
			return agent.EvaluateResult{Decision: decision, Reason: reason}
		}
	}

	// Bash fast-path. Installed only while plan-mode is active; nil in
	// build mode so the ruleset stays the sole authority. ReadOnly and
	// Mutating short-circuit; Unknown falls through to the ruleset
	// (where plan mode's `bash *: Ask` rule will fire).
	if in.Tool == "bash" {
		e.mu.RLock()
		classifier := e.bashClassifier
		e.mu.RUnlock()
		if classifier != nil {
			switch classifier(in.Key) {
			case BashClassReadOnly:
				return agent.EvaluateResult{
					Decision: agent.DecisionAllow,
					Reason:   "bash classified as read-only",
				}
			case BashClassMutating:
				return agent.EvaluateResult{
					Decision: agent.DecisionDeny,
					Reason:   "bash classified as mutating in plan mode",
				}
			}
		}
	}

	decision, reason := e.evaluateRules(in)
	return agent.EvaluateResult{Decision: decision, Reason: reason}
}

// evaluateRules runs the ruleset against the input, then layers the
// agent-rule overlay (last-matching-wins). Extracted from Evaluate so
// the acceptEdits branch can consult deny rules without duplicating
// the walk. The returned reason matches what the inlined version used
// to produce so existing tests / audit logs stay byte-compatible.
func (e *Evaluator) evaluateRules(in agent.EvaluateInput) (agent.Decision, string) {
	decision := e.ruleset.Evaluate(in.Tool, in.Key)
	reason := decisionReason(decision)

	e.mu.RLock()
	agentRules := e.agentRules
	e.mu.RUnlock()
	for _, rule := range agentRules {
		if !toolMatches(rule.Tool, in.Tool) {
			continue
		}
		if !patternMatches(rule.Pattern, in.Key) {
			continue
		}
		decision = rule.Action
		reason = "matched an agent rule"
		if rule.Reason != "" {
			reason = "agent rule: " + rule.Reason
		}
	}
	return decision, reason
}

// isAutoAllowedTool reports whether the tool name belongs to the set
// that bypasses approval prompts entirely. Currently:
//
//   - todowrite: writes prompto's session-scoped task sidecar. No
//     filesystem, network, or shell side-effect. The LLM uses it to
//     plan multi-step work, and prompting on every plan revision is
//     pure friction.
//
// Add new entries sparingly. The bar is "no possible blast radius
// outside prompto's own session state."
func isAutoAllowedTool(name string) bool {
	switch name {
	case "todowrite":
		return true
	}
	return false
}

func decisionReason(d agent.Decision) string {
	switch d {
	case agent.DecisionAllow:
		return "matched an allow rule"
	case agent.DecisionDeny:
		return "matched a deny rule"
	default:
		return "no rule matched"
	}
}
