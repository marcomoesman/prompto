package permission

import (
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestEvaluator_BypassAllowsEverything(t *testing.T) {
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "*", Pattern: "*", Action: agent.DecisionDeny}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeBypass, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "rm -rf /"})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("bypass: got %v, want Allow even with deny rule", res.Decision)
	}
}

func TestEvaluator_AcceptEditsForEditTool(t *testing.T) {
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeAcceptEdits, Ruleset: NewRuleset()})

	res := e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/project/src/main.go"})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("acceptEdits: edit = %v, want Allow", res.Decision)
	}
	res = e.Evaluate(agent.EvaluateInput{Tool: "write", Key: "/project/new.go"})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("acceptEdits: write = %v, want Allow", res.Decision)
	}
	res = e.Evaluate(agent.EvaluateInput{Tool: "replace_lines", Key: "/project/src/main.go"})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("acceptEdits: replace_lines = %v, want Allow", res.Decision)
	}
}

func TestEvaluator_TodoWriteAutoAllowed(t *testing.T) {
	// Even with a deny-everything ruleset, todowrite should bypass
	// the prompt because it has no security blast radius — it only
	// writes to prompto's own session sidecar.
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "*", Pattern: "*", Action: agent.DecisionDeny}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "todowrite", Key: "", IsReadOnly: false})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("todowrite under deny-all ruleset = %v, want Allow", res.Decision)
	}
	if res.Reason == "" {
		t.Error("auto-allow reason should be populated")
	}
}

func TestEvaluator_OtherToolsStillFallToAsk(t *testing.T) {
	// Make sure the auto-allow list didn't inadvertently widen to
	// other tools — bash with no rule must still go to Ask.
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "ls"})
	if res.Decision != agent.DecisionAsk {
		t.Errorf("bash with no rule = %v, want Ask", res.Decision)
	}
}

func TestIsAutoAllowedTool(t *testing.T) {
	if !isAutoAllowedTool("todowrite") {
		t.Error("todowrite should be auto-allowed")
	}
	for _, name := range []string{"bash", "edit", "write", "read", "webfetch", "task", ""} {
		if isAutoAllowedTool(name) {
			t.Errorf("%q must not be auto-allowed", name)
		}
	}
}

func TestEvaluator_AcceptEditsDoesNotAffectBash(t *testing.T) {
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeAcceptEdits, Ruleset: NewRuleset()})
	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "git status"})
	if res.Decision != agent.DecisionAsk {
		t.Errorf("acceptEdits: bash = %v, want Ask", res.Decision)
	}
}

func TestEvaluator_ProtectedWriteDenied(t *testing.T) {
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "edit", Pattern: "*", Action: agent.DecisionAllow}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/home/user/.env", IsReadOnly: false})
	if res.Decision != agent.DecisionDeny {
		t.Errorf("protected .env edit = %v, want Deny even with explicit allow rule", res.Decision)
	}
	if res.Reason == "" {
		t.Error("Reason should describe the protection")
	}
}

func TestEvaluator_ProtectedReadFallsThrough(t *testing.T) {
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "read", Pattern: "*", Action: agent.DecisionAllow}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "read", Key: "/home/user/.env", IsReadOnly: true})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("protected .env read (read-only tool) = %v, want Allow via ruleset", res.Decision)
	}
}

func TestEvaluator_RulesetDefaultAsk(t *testing.T) {
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "ls"})
	if res.Decision != agent.DecisionAsk {
		t.Errorf("empty ruleset = %v, want Ask", res.Decision)
	}
}

func TestEvaluator_ReasonPopulated(t *testing.T) {
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "bash", Pattern: "git *", Action: agent.DecisionAllow}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "git status"})
	if res.Decision != agent.DecisionAllow {
		t.Fatalf("decision = %v, want Allow", res.Decision)
	}
	if res.Reason == "" {
		t.Error("Reason should be populated for an allow-by-rule")
	}
}

func TestEvaluator_BashClassifier_PlanModeReadOnlyAllow(t *testing.T) {
	// In plan mode the classifier is installed and the agent
	// rules contain a `bash *: Ask` fallback. ReadOnly commands
	// should fast-path to Allow without the agent rule firing.
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetAgentRules(PlanRules("/proj"))
	e.SetBashClassifier(ClassifyBash)

	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "git status"})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("git status under classifier = %v, want Allow", res.Decision)
	}
	if res.Reason == "" {
		t.Error("classifier reason should be populated")
	}
}

func TestEvaluator_BashClassifier_PlanModeMutatingDeny(t *testing.T) {
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetAgentRules(PlanRules("/proj"))
	e.SetBashClassifier(ClassifyBash)

	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "git push"})
	if res.Decision != agent.DecisionDeny {
		t.Errorf("git push under classifier = %v, want Deny", res.Decision)
	}
}

func TestEvaluator_BashClassifier_PlanModeUnknownFallsThrough(t *testing.T) {
	// Unknown commands should not be short-circuited; they should
	// reach the existing `bash *: Ask` rule installed by PlanRules.
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetAgentRules(PlanRules("/proj"))
	e.SetBashClassifier(ClassifyBash)

	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "make build"})
	if res.Decision != agent.DecisionAsk {
		t.Errorf("make build under classifier = %v, want Ask", res.Decision)
	}
}

func TestEvaluator_BashClassifier_BuildModeUntouched(t *testing.T) {
	// No classifier installed (build mode). All bash commands —
	// even ones the classifier would auto-allow — must reach the
	// ruleset and produce the same Ask the user already sees.
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})

	for _, cmd := range []string{"git status", "git push", "rm foo", "ls"} {
		res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: cmd})
		if res.Decision != agent.DecisionAsk {
			t.Errorf("build-mode bash %q = %v, want Ask (no classifier)", cmd, res.Decision)
		}
	}
}

func TestEvaluator_BashClassifier_ClearedOnNil(t *testing.T) {
	// Setting the classifier to nil must restore build-mode behaviour
	// even if plan rules are still installed — paranoid, since we'd
	// expect the TUI to clear both, but the contract should hold.
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetBashClassifier(ClassifyBash)
	e.SetBashClassifier(nil)

	res := e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "git status"})
	if res.Decision != agent.DecisionAsk {
		t.Errorf("after clearing classifier: %v, want Ask", res.Decision)
	}
}

func TestEvaluator_BashClassifier_OnlyAffectsBash(t *testing.T) {
	// The fast-path is gated on Tool == "bash"; other tools should
	// not be affected even when the classifier is installed.
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})
	e.SetAgentRules(PlanRules("/proj"))
	e.SetBashClassifier(ClassifyBash)

	res := e.Evaluate(agent.EvaluateInput{Tool: "edit", Key: "/proj/main.go"})
	if res.Decision != agent.DecisionDeny {
		t.Errorf("edit under plan rules = %v, want Deny (classifier must not interfere)", res.Decision)
	}
}

func TestEvaluator_AcceptEditsRespectsDenyRule(t *testing.T) {
	// acceptEdits used to short-circuit to Allow before consulting
	// the ruleset, silently bypassing user-authored deny rules.
	// After the fix, an explicit deny still fires.
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "write", Pattern: "*.secret", Action: agent.DecisionDeny}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeAcceptEdits, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "write", Key: "notes.secret"})
	if res.Decision != agent.DecisionDeny {
		t.Errorf("acceptEdits with deny rule: write notes.secret = %v, want Deny", res.Decision)
	}
}

func TestEvaluator_AcceptEditsAutoAllowsWhenNoRuleMatches(t *testing.T) {
	// The no-rule path keeps the original UX: skip the prompt.
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "write", Pattern: "*.secret", Action: agent.DecisionDeny}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeAcceptEdits, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "write", Key: "main.go"})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("acceptEdits no-match: write main.go = %v, want Allow", res.Decision)
	}
}

func TestEvaluator_AcceptEditsRespectsExplicitAllow(t *testing.T) {
	// An explicit Allow rule wins and its reason is preserved (not
	// silently rebranded as "acceptEdits mode").
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "write", Pattern: "*.go", Action: agent.DecisionAllow}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeAcceptEdits, Ruleset: rs})

	res := e.Evaluate(agent.EvaluateInput{Tool: "write", Key: "main.go"})
	if res.Decision != agent.DecisionAllow {
		t.Errorf("acceptEdits with allow rule: write main.go = %v, want Allow", res.Decision)
	}
	if res.Reason == "acceptEdits mode (no rule matched)" {
		t.Errorf("explicit allow rule should not be rebranded as acceptEdits; got reason %q", res.Reason)
	}
}

func TestEvaluator_ReadProtectedDenied(t *testing.T) {
	// A permissive allow-read-everything rule must not unlock reads
	// of unambiguous credential files. The read-protected guard
	// fires before the ruleset.
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "read", Pattern: "*", Action: agent.DecisionAllow}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	cases := []string{
		"/home/u/.ssh/id_rsa",
		"/home/u/.ssh/authorized_keys",
		"/home/u/.aws/credentials",
		"/home/u/secrets/leaf.pem",
		"/home/u/certs/store.p12",
	}
	for _, key := range cases {
		res := e.Evaluate(agent.EvaluateInput{Tool: "read", Key: key, IsReadOnly: true})
		if res.Decision != agent.DecisionDeny {
			t.Errorf("read %q = %v, want Deny", key, res.Decision)
		}
	}
}

func TestEvaluator_ReadProtectedDoesNotBlockNonCreds(t *testing.T) {
	// Read-protected globs must be tight: legitimate read targets
	// (.env, .git/config) stay configurable via ruleset, not force-denied.
	rs := NewRuleset()
	_ = rs.Append(AppendInput{Rule: Rule{Tool: "read", Pattern: "*", Action: agent.DecisionAllow}})
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: rs})

	cases := []string{
		"/home/u/proj/.env",
		"/home/u/proj/.env.example",
		"/home/u/proj/.git/config",
		"/home/u/proj/main.go",
	}
	for _, key := range cases {
		res := e.Evaluate(agent.EvaluateInput{Tool: "read", Key: key, IsReadOnly: true})
		if res.Decision != agent.DecisionAllow {
			t.Errorf("read %q = %v, want Allow (not force-denied)", key, res.Decision)
		}
	}
}

func TestIsReadProtected(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/home/u/.ssh/id_rsa", true},
		{"/home/u/.ssh/known_hosts", true},
		{"/home/u/.aws/credentials", true},
		{"/home/u/server.pem", true},
		{"/home/u/cert.p12", true},
		{"/home/u/id_ed25519", true},
		{"/home/u/proj/.env", false},
		{"/home/u/proj/.env.example", false},
		{"/home/u/proj/.git/config", false},
		{"/home/u/proj/main.go", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := IsReadProtected(tc.path); got != tc.want {
			t.Errorf("IsReadProtected(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestEvaluator_SetModeSafeConcurrent(t *testing.T) {
	e := NewEvaluator(NewEvaluatorInput{Mode: ModeDefault, Ruleset: NewRuleset()})

	done := make(chan struct{})
	go func() {
		for range 1000 {
			_ = e.Evaluate(agent.EvaluateInput{Tool: "bash", Key: "x"})
		}
		close(done)
	}()

	for range 100 {
		e.SetMode(ModeAcceptEdits)
		e.SetMode(ModeDefault)
	}
	<-done
}
