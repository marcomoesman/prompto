package permission

import (
	"path/filepath"

	"github.com/marcomoesman/prompto/internal/agent"
)

// PlanRules returns the agent-scoped rule set installed when the
// user switches into the plan agent. Layout:
//
//  1. Blanket Deny on edit + replace_lines + write (any path).
//  2. Late Allow on edit + replace_lines + write of `.prompto/plans/*.md` under
//     cwd (overrides 1 via last-matching-wins). The glob accepts
//     both the legacy `<sessionID>.md` filename AND the
//     model-chosen `YYYY-MM-DD-<slug>.md` shape, so the agent can
//     pick its own slug at first write without us pre-computing
//     the path. The single-segment glob (`*.md`, not `**/*.md`)
//     keeps writes to the `.history/` subdirectory denied — that
//     directory is owned by the backup-on-edit hook.
//  3. Bash → Ask. Plan agent may shell out, but always with
//     confirmation.
//
// An empty cwd skips the plan-file allow rules entirely (the
// agent then can't write anywhere — useful for tests asserting
// the deny-baseline). Real callers always pass a populated cwd.
func PlanRules(cwd string) []Rule {
	rules := []Rule{
		{Tool: "edit", Pattern: "*", Action: agent.DecisionDeny, Reason: "plan agent: edits restricted to plan files"},
		{Tool: "replace_lines", Pattern: "*", Action: agent.DecisionDeny, Reason: "plan agent: edits restricted to plan files"},
		{Tool: "write", Pattern: "*", Action: agent.DecisionDeny, Reason: "plan agent: writes restricted to plan files"},
	}
	if cwd != "" {
		plansGlob := filepath.Join(cwd, ".prompto", "plans", "*.md")
		rules = append(rules,
			Rule{Tool: "edit", Pattern: plansGlob, Action: agent.DecisionAllow, Reason: "plan file"},
			Rule{Tool: "replace_lines", Pattern: plansGlob, Action: agent.DecisionAllow, Reason: "plan file"},
			Rule{Tool: "write", Pattern: plansGlob, Action: agent.DecisionAllow, Reason: "plan file"},
		)
	}
	rules = append(rules,
		Rule{Tool: "bash", Pattern: "*", Action: agent.DecisionAsk, Reason: "plan agent: confirm before shelling out"},
	)
	return rules
}
