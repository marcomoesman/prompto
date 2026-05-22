package permission

import (
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestEvaluator_SetAgentRules_AppendedAfterRuleset(t *testing.T) {
	rs := NewRuleset()
	// Project allow on edit:**.
	_ = rs.Append(AppendInput{Rule: Rule{
		Tool:    "edit",
		Pattern: "*",
		Action:  agent.DecisionAllow,
		Scope:   ScopeProject,
	}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	// Without agent rules, edit is allowed.
	got := e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/proj/main.go", IsReadOnly: false})
	if got.Decision != agent.DecisionAllow {
		t.Fatalf("baseline: got %v, want Allow", got.Decision)
	}

	// Agent rule denies the same tool — should override the project allow.
	e.SetAgentRules([]Rule{{Tool: "edit", Pattern: "*", Action: agent.DecisionDeny}})
	got = e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/proj/main.go", IsReadOnly: false})
	if got.Decision != agent.DecisionDeny {
		t.Errorf("with agent deny: got %v, want Deny (agent rule must override)", got.Decision)
	}

	// Clearing agent rules restores the project allow.
	e.SetAgentRules(nil)
	got = e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/proj/main.go", IsReadOnly: false})
	if got.Decision != agent.DecisionAllow {
		t.Errorf("after clear: got %v, want Allow", got.Decision)
	}
}

func TestEvaluator_AgentRules_LastMatchWins(t *testing.T) {
	rs := NewRuleset()
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	planFile := "/proj/.prompto/plans/abc.md"
	e.SetAgentRules([]Rule{
		{Tool: "edit", Pattern: "*", Action: agent.DecisionDeny},
		{Tool: "write", Pattern: "*", Action: agent.DecisionDeny},
		{Tool: "edit", Pattern: planFile, Action: agent.DecisionAllow},
		{Tool: "write", Pattern: planFile, Action: agent.DecisionAllow},
	})

	// Editing some other file: blanket deny applies.
	got := e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/proj/foo.go"})
	if got.Decision != agent.DecisionDeny {
		t.Errorf("non-plan edit = %v, want Deny", got.Decision)
	}
	// Editing the plan file: late-matching allow wins.
	got = e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: planFile})
	if got.Decision != agent.DecisionAllow {
		t.Errorf("plan-file edit = %v, want Allow (last match wins)", got.Decision)
	}
}

func TestEvaluator_AgentRules_ScopeStamped(t *testing.T) {
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetAgentRules([]Rule{{Tool: "edit", Pattern: "*", Action: agent.DecisionDeny, Scope: ScopeProject}})
	rules := e.AgentRules()
	if len(rules) != 1 {
		t.Fatalf("len = %d, want 1", len(rules))
	}
	if rules[0].Scope != ScopeAgent {
		t.Errorf("Scope = %v, want ScopeAgent (caller's value should be overridden)", rules[0].Scope)
	}
}

func TestScope_AgentString(t *testing.T) {
	if got := ScopeAgent.String(); got != "agent" {
		t.Errorf("String() = %q, want %q", got, "agent")
	}
}
