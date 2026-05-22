package permission

import (
	"path/filepath"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestPlanRules_BlanketDenyPlanFileAllow(t *testing.T) {
	cwd := "/proj"
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetAgentRules(PlanRules(cwd))

	// Blanket deny on edits / writes outside the plans directory.
	got := e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/proj/main.go"})
	if got.Decision != agent.DecisionDeny {
		t.Errorf("non-plan edit = %v, want Deny", got.Decision)
	}
	got = e.Evaluate(agent.EvaluateInput{Tool: "write", Key: "/proj/main.go"})
	if got.Decision != agent.DecisionDeny {
		t.Errorf("non-plan write = %v, want Deny", got.Decision)
	}
	got = e.Evaluate(agent.EvaluateInput{Tool: "replace_lines", Key: "/proj/main.go"})
	if got.Decision != agent.DecisionDeny {
		t.Errorf("non-plan replace_lines = %v, want Deny", got.Decision)
	}

	// Plan files (legacy hex name) get late-allowed via the glob.
	legacy := filepath.Join(cwd, ".prompto", "plans", "abc12345.md")
	got = e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: legacy})
	if got.Decision != agent.DecisionAllow {
		t.Errorf("legacy plan-file edit = %v, want Allow", got.Decision)
	}
	got = e.Evaluate(agent.EvaluateInput{Tool: "replace_lines", Key: legacy})
	if got.Decision != agent.DecisionAllow {
		t.Errorf("legacy plan-file replace_lines = %v, want Allow", got.Decision)
	}

	// Phase-20 slug filenames also match the glob.
	slug := filepath.Join(cwd, ".prompto", "plans", "2026-04-30-undo-flag.md")
	got = e.Evaluate(agent.EvaluateInput{Tool: "write", Key: slug})
	if got.Decision != agent.DecisionAllow {
		t.Errorf("slug plan-file write = %v, want Allow", got.Decision)
	}

	// Bash always asks.
	got = e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "ls"})
	if got.Decision != agent.DecisionAsk {
		t.Errorf("bash = %v, want Ask", got.Decision)
	}
}

// TestPlanRules_HistorySubdirNotAllowed regresses the deliberately
// narrow `*.md` glob (not `**/*.md`): writes to the
// `.prompto/plans/.history/` directory — which the backup
// hook owns — must NOT be allowed for the model. Otherwise the
// model could overwrite revision history.
func TestPlanRules_HistorySubdirNotAllowed(t *testing.T) {
	cwd := "/proj"
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetAgentRules(PlanRules(cwd))

	historyPath := filepath.Join(cwd, ".prompto", "plans", ".history", "2026-04-30-foo.1234.md")
	got := e.Evaluate(agent.EvaluateInput{Tool: "write", Key: historyPath})
	if got.Decision != agent.DecisionDeny {
		t.Errorf(".history write = %v, want Deny", got.Decision)
	}
}

// TestPlanRules_EmptyCwdSkipsAllow keeps the existing defensive
// behavior under the new signature: with no cwd, only deny + ask
// rules are produced (no allow, since we'd otherwise fabricate a
// glob rooted at "/.prompto/plans/").
func TestPlanRules_EmptyCwdSkipsAllow(t *testing.T) {
	rules := PlanRules("")
	for _, r := range rules {
		if r.Action == agent.DecisionAllow {
			t.Errorf("empty cwd produced an allow rule: %+v", r)
		}
	}
}
