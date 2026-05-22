package permission

import (
	"sync"

	"github.com/marcomoesman/prompto/internal/agent"
)

// Scope identifies the durability of a rule.
type Scope int

const (
	// ScopeCLI is for rules supplied at startup (e.g., future --allow flag).
	ScopeCLI Scope = iota
	// ScopeProject persists to .prompto/permissions.json and survives restarts.
	ScopeProject
	// ScopeSession lives in memory for the current prompto process only.
	ScopeSession
	// ScopeAgent layers on top of CLI/Project/Session. These rules are
	// supplied at agent-switch time and replace wholesale via
	// Evaluator.SetAgentRules. Last-matching-wins still applies, so a
	// project-scoped Allow can be overridden by an agent-scoped Deny —
	// that's the intent for plan mode (project Allow doesn't grant edits
	// while planning).
	ScopeAgent
)

// String returns the JSON-friendly name of the scope.
func (s Scope) String() string {
	switch s {
	case ScopeCLI:
		return "cli"
	case ScopeProject:
		return "project"
	case ScopeSession:
		return "session"
	case ScopeAgent:
		return "agent"
	default:
		return "unknown"
	}
}

// Rule is one permission entry. Pattern is matched via MatchGlob against the
// tool's PermissionKey output; Tool is matched literally or "*" for any.
type Rule struct {
	Tool    string         `json:"tool"`
	Pattern string         `json:"pattern"`
	Action  agent.Decision `json:"action"` // Allow or Deny; Ask is not persisted
	Scope   Scope          `json:"scope,omitempty"`
	Reason  string         `json:"reason,omitempty"` // user-facing note
}

// Ruleset holds permission rules in evaluation order. Last-matching-rule
// wins. Construct via NewRuleset; mutate via Append (safe for concurrent
// callers).
type Ruleset struct {
	mu    sync.RWMutex
	rules []Rule
	// saver is invoked whenever a ScopeProject rule is appended. When nil,
	// project rules are kept in memory only. Set by LoadRuleset.
	saver func(rules []Rule) error
}

// NewRuleset returns an empty ruleset with no persistence configured.
func NewRuleset() *Ruleset {
	return &Ruleset{}
}

// AppendInput bundles Append's inputs.
type AppendInput struct {
	Rule Rule
}

// Append adds a rule. If the rule has ScopeProject and a saver is configured,
// the project-persisted subset of rules is saved after the append. Returns
// the saver's error (if any); the in-memory ruleset is updated regardless.
func (r *Ruleset) Append(in AppendInput) error {
	r.mu.Lock()
	r.rules = append(r.rules, in.Rule)
	saver := r.saver
	projectRules := filterByScope(r.rules, ScopeProject)
	r.mu.Unlock()

	if in.Rule.Scope == ScopeProject && saver != nil {
		return saver(projectRules)
	}
	return nil
}

// Evaluate walks the rules in order; returns the Action of the last rule
// whose Tool matches (literal or "*") and whose Pattern matches the key via
// MatchGlob. Returns DecisionAsk when no rule matches.
func (r *Ruleset) Evaluate(tool, key string) agent.Decision {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := agent.DecisionAsk
	for _, rule := range r.rules {
		if !toolMatches(rule.Tool, tool) {
			continue
		}
		if !patternMatches(rule.Pattern, key) {
			continue
		}
		result = rule.Action
	}
	return result
}

// Rules returns a snapshot of all rules. Safe to mutate the returned slice.
func (r *Ruleset) Rules() []Rule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Rule, len(r.rules))
	copy(out, r.rules)
	return out
}

// SetSaver installs a persistence hook invoked on ScopeProject appends.
// Used by LoadRuleset to wire the JSON store. Not exported in the usual
// construction path; tests pass their own saver when exercising persistence.
func (r *Ruleset) SetSaver(fn func(rules []Rule) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saver = fn
}

func toolMatches(rulePattern, tool string) bool {
	if rulePattern == "*" || rulePattern == "" {
		return true
	}
	return rulePattern == tool
}

func patternMatches(pattern, key string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	return MatchGlob(pattern, key)
}

func filterByScope(rules []Rule, scope Scope) []Rule {
	var out []Rule
	for _, rule := range rules {
		if rule.Scope == scope {
			out = append(out, rule)
		}
	}
	return out
}
