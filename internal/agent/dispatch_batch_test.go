package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// concurrentBatchEnd inspects only plan.tool (nil vs. non-nil) and
// plan.isConcurrent, plus plan.denied (which it must IGNORE per the
// fix). A minimal nil-method-body Tool is enough; tests never call
// Execute or Definition.
type batchTestTool struct{}

func (batchTestTool) Name() string                   { return "batch-test" }
func (batchTestTool) Definition() api.ToolDefinition { return api.ToolDefinition{} }
func (batchTestTool) PermissionKey([]byte) string    { return "" }
func (batchTestTool) IsReadOnly() bool               { return true }
func (batchTestTool) IsConcurrencySafe() bool        { return true }
func (batchTestTool) FormatForDisplay([]byte) string { return "" }
func (batchTestTool) MaxResultBytes() int            { return 0 }
func (batchTestTool) Execute(_ context.Context, _ ToolContext, _ []byte) (Result, error) {
	return Result{}, nil
}

type panickingBatchTool struct {
	batchTestTool
	concurrent bool
}

func (t panickingBatchTool) Name() string            { return "panic-tool" }
func (t panickingBatchTool) IsConcurrencySafe() bool { return t.concurrent }
func (t panickingBatchTool) Execute(context.Context, ToolContext, []byte) (Result, error) {
	panic("boom")
}

// TestConcurrentBatchEnd_DeniedDoesNotBreakRun regresses a behaviour
// where a denied plan in the middle of a concurrent run was treated
// as a batch boundary. Two safe plans on either side of a denial
// should still dispatch as a single parallel batch — denied plans
// don't execute, so they can't fragment.
func TestConcurrentBatchEnd_DeniedDoesNotBreakRun(t *testing.T) {
	live := &toolCallPlan{tool: batchTestTool{}, isConcurrent: true}
	denied := &toolCallPlan{tool: batchTestTool{}, isConcurrent: true, denied: "denied: by policy"}

	plans := []*toolCallPlan{live, denied, live, live}
	if got := concurrentBatchEnd(plans, 0); got != 4 {
		t.Errorf("denied in middle of concurrent run = batch end %d, want 4", got)
	}
}

func TestConcurrentBatchEnd_NonConcurrentBreaksRun(t *testing.T) {
	live := &toolCallPlan{tool: batchTestTool{}, isConcurrent: true}
	serial := &toolCallPlan{tool: batchTestTool{}, isConcurrent: false}

	plans := []*toolCallPlan{live, live, serial, live}
	if got := concurrentBatchEnd(plans, 0); got != 2 {
		t.Errorf("serial plan should end batch at 2, got %d", got)
	}
}

func TestConcurrentBatchEnd_UnknownToolBreaksRun(t *testing.T) {
	live := &toolCallPlan{tool: batchTestTool{}, isConcurrent: true}
	unknown := &toolCallPlan{tool: nil, isConcurrent: true}

	plans := []*toolCallPlan{live, unknown, live}
	if got := concurrentBatchEnd(plans, 0); got != 1 {
		t.Errorf("unknown-tool plan should end batch at 1, got %d", got)
	}
}

func TestConcurrentBatchEnd_AllDeniedStillRunsAsOneBatch(t *testing.T) {
	denied := &toolCallPlan{tool: batchTestTool{}, isConcurrent: true, denied: "x"}
	plans := []*toolCallPlan{denied, denied, denied}
	if got := concurrentBatchEnd(plans, 0); got != 3 {
		t.Errorf("all-denied run = %d, want 3", got)
	}
}

func TestDispatchPlans_InlineToolPanicBecomesToolError(t *testing.T) {
	plan := &toolCallPlan{
		acc:          &toolCallAccumulator{id: "tc_1", name: "panic-tool"},
		tool:         panickingBatchTool{},
		isConcurrent: false,
	}

	if err := dispatchPlans(t.Context(), ToolContext{}, []*toolCallPlan{plan}); err != nil {
		t.Fatalf("dispatchPlans: %v", err)
	}
	if !plan.resultIsError || !strings.Contains(plan.resultContent, "panicked") {
		t.Fatalf("result = (%v, %q), want panic tool error", plan.resultIsError, plan.resultContent)
	}
}

func TestDispatchPlans_ConcurrentToolPanicBecomesToolError(t *testing.T) {
	plan := &toolCallPlan{
		acc:          &toolCallAccumulator{id: "tc_1", name: "panic-tool"},
		tool:         panickingBatchTool{concurrent: true},
		isConcurrent: true,
	}

	if err := dispatchPlans(t.Context(), ToolContext{}, []*toolCallPlan{plan}); err != nil {
		t.Fatalf("dispatchPlans: %v", err)
	}
	if !plan.resultIsError || !strings.Contains(plan.resultContent, "panicked") {
		t.Fatalf("result = (%v, %q), want panic tool error", plan.resultIsError, plan.resultContent)
	}
}
