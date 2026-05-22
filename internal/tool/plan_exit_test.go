package tool

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
)

func TestPlanExitTool_Definition(t *testing.T) {
	tool := NewPlanExitTool()
	if tool.Name() != "plan_exit" {
		t.Errorf("Name() = %q, want plan_exit", tool.Name())
	}
	def := tool.Definition()
	if def.Name != "plan_exit" {
		t.Errorf("Definition.Name = %q", def.Name)
	}
	if !strings.Contains(def.Description, "schema") {
		t.Errorf("description should mention the schema; got %q", def.Description)
	}
	if !strings.Contains(def.Description, "build agent") {
		t.Errorf("description should mention the build-agent handoff; got %q", def.Description)
	}
}

// TestPlanExitTool_IsReadOnly asserts the tool reports read-only:
// the actual write/edit on the plan markdown happened earlier via
// the `write` and `edit` tools, and the plan-approval overlay is
// the user-visible side effect — nothing on the filesystem changes
// inside Execute.
func TestPlanExitTool_IsReadOnly(t *testing.T) {
	if !NewPlanExitTool().IsReadOnly() {
		t.Error("plan_exit should report IsReadOnly() = true")
	}
}

// TestPlanExitTool_NotConcurrencySafe — plan_exit must run alone in
// its turn so the run loop's pre-permission validation + post-
// execute event ordering stay clean.
func TestPlanExitTool_NotConcurrencySafe(t *testing.T) {
	if NewPlanExitTool().IsConcurrencySafe() {
		t.Error("plan_exit should not be concurrency-safe")
	}
}

// TestPlanExitTool_Execute returns a fixed success result. The
// content is arbitrary but the bytes value must match its length
// for the per-turn aggregator's accounting.
func TestPlanExitTool_Execute(t *testing.T) {
	tool := NewPlanExitTool()
	res, err := tool.Execute(t.Context(), agent.ToolContext{}, []byte(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Content == "" {
		t.Error("Execute returned empty Content")
	}
	if res.Bytes != len(res.Content) {
		t.Errorf("Bytes = %d, want %d (len of Content)", res.Bytes, len(res.Content))
	}
	if res.DisplaySummary == "" {
		t.Error("DisplaySummary should be non-empty so the chat row gets a tail line")
	}
}

func TestPlanExitTool_FormatForDisplay(t *testing.T) {
	tool := NewPlanExitTool()
	got := tool.FormatForDisplay(nil)
	if !strings.Contains(got, "PlanExit") {
		t.Errorf("FormatForDisplay = %q, want contains PlanExit", got)
	}
}

func TestPlanExitTool_PermissionKey(t *testing.T) {
	if got := NewPlanExitTool().PermissionKey([]byte(`{}`)); got != "" {
		t.Errorf("PermissionKey = %q, want empty", got)
	}
}
