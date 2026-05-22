package permission

import (
	"sync"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestRuleset_DefaultAskWhenEmpty(t *testing.T) {
	r := NewRuleset()
	if got := r.Evaluate("bash", "git status"); got != agent.DecisionAsk {
		t.Errorf("empty ruleset returned %v, want DecisionAsk", got)
	}
}

func TestRuleset_LastMatchWins(t *testing.T) {
	r := NewRuleset()
	_ = r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "git *", Action: agent.DecisionDeny}})
	_ = r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "git *", Action: agent.DecisionAllow}})

	if got := r.Evaluate("bash", "git status"); got != agent.DecisionAllow {
		t.Errorf("got %v, want DecisionAllow (last match)", got)
	}
}

func TestRuleset_GlobPatterns(t *testing.T) {
	r := NewRuleset()
	_ = r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "git *", Action: agent.DecisionAllow}})

	cases := []struct {
		key  string
		want agent.Decision
	}{
		{"git status", agent.DecisionAllow},
		{"git log --oneline", agent.DecisionAllow},
		{"gitsomething", agent.DecisionAsk},
		{"git", agent.DecisionAsk},
		{"rm -rf /", agent.DecisionAsk},
	}
	for _, tc := range cases {
		if got := r.Evaluate("bash", tc.key); got != tc.want {
			t.Errorf("Evaluate(bash, %q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestRuleset_DoubleStarGlob(t *testing.T) {
	r := NewRuleset()
	_ = r.Append(AppendInput{Rule: Rule{Tool: "read", Pattern: "**/*.go", Action: agent.DecisionAllow}})

	if got := r.Evaluate("read", "internal/tool/edit.go"); got != agent.DecisionAllow {
		t.Errorf("nested path = %v, want DecisionAllow", got)
	}
	if got := r.Evaluate("read", "main.go"); got != agent.DecisionAllow {
		t.Errorf("top-level path = %v, want DecisionAllow", got)
	}
	if got := r.Evaluate("read", "README.md"); got != agent.DecisionAsk {
		t.Errorf("non-go = %v, want DecisionAsk", got)
	}
}

func TestRuleset_ToolWildcard(t *testing.T) {
	r := NewRuleset()
	_ = r.Append(AppendInput{Rule: Rule{Tool: "*", Pattern: "README.md", Action: agent.DecisionAllow}})

	if got := r.Evaluate("read", "README.md"); got != agent.DecisionAllow {
		t.Errorf("tool=read, key=README.md = %v, want DecisionAllow", got)
	}
	if got := r.Evaluate("edit", "README.md"); got != agent.DecisionAllow {
		t.Errorf("tool=edit, key=README.md = %v, want DecisionAllow", got)
	}
	if got := r.Evaluate("read", "other"); got != agent.DecisionAsk {
		t.Errorf("other key = %v, want DecisionAsk", got)
	}
}

func TestRuleset_AppendRaceClean(t *testing.T) {
	r := NewRuleset()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "x", Action: agent.DecisionAllow}})
		})
		wg.Go(func() {
			_ = r.Evaluate("bash", "x")
		})
	}
	wg.Wait()
}

func TestRuleset_SaverInvokedOnProjectAppend(t *testing.T) {
	r := NewRuleset()
	var saved [][]Rule
	r.SetSaver(func(rules []Rule) error {
		snap := make([]Rule, len(rules))
		copy(snap, rules)
		saved = append(saved, snap)
		return nil
	})

	_ = r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "git *", Action: agent.DecisionAllow, Scope: ScopeProject}})
	_ = r.Append(AppendInput{Rule: Rule{Tool: "read", Pattern: "x", Action: agent.DecisionAllow, Scope: ScopeSession}})
	_ = r.Append(AppendInput{Rule: Rule{Tool: "read", Pattern: "y", Action: agent.DecisionAllow, Scope: ScopeProject}})

	if len(saved) != 2 {
		t.Fatalf("saver called %d times, want 2 (only project appends)", len(saved))
	}
	if len(saved[1]) != 2 {
		t.Errorf("second save = %d rules, want 2 (only project-scoped)", len(saved[1]))
	}
}

func TestRuleset_SaverNotCalledWhenUnset(t *testing.T) {
	r := NewRuleset()
	if err := r.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "*", Action: agent.DecisionAllow, Scope: ScopeProject}}); err != nil {
		t.Fatalf("Append without saver = %v, want nil", err)
	}
}
