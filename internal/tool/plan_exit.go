package tool

import (
	"context"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

// PlanExitInput is the parameter struct for the plan_exit tool.
// Empty by design — the model has nothing to pass; the plan body
// lives on disk under the path recorded in `sessions.plan_path`,
// and validation + approval are handled outside Execute.
type PlanExitInput struct{}

// PlanExitTool is the explicit "I'm done planning, please switch
// to build" gate the plan agent calls when its plan markdown is
// complete. Three layers cooperate:
//
//  1. Pre-permission validation in run.go reads the recorded plan
//     path and runs ValidatePlanMarkdown. If sections are missing,
//     the tool call is short-circuited to a tool-error result so
//     the model can fix and retry without bothering the user.
//  2. The TUI's CanUseTool path renders a full-screen plan-approval
//     overlay showing the rendered plan body. The user picks Y/N.
//  3. On Allow, Execute runs (this no-op success), and the
//     emission-phase hook in run.go emits EventPlanApproved so
//     the TUI can flip the agent to build, queue the BUILD_SWITCH
//     reminder, and synthesise an "execute it" user message.
//
// Execute itself is intentionally a no-op success: every gate
// happens outside it. Keeping Execute trivial means the tool stays
// pure / stateless and tests can drive the
// run.go pre-flight + post-execute hooks without provider-side
// surprises.
type PlanExitTool struct {
	definition api.ToolDefinition
}

// NewPlanExitTool builds a PlanExitTool with a frozen schema.
func NewPlanExitTool() *PlanExitTool {
	return &PlanExitTool{
		definition: api.ToolDefinition{
			Name:        "plan_exit",
			Description: "Signal that your plan is complete and ready for execution. Call this only after the plan markdown satisfies the schema (## Context, ## Goal & acceptance criteria, ## Files, ## Verification, ## Risks / out-of-scope). Prompto will validate the plan, show it to the user for approval, and on approval auto-switch to the build agent which will execute the plan. On rejection or validation failure, you'll see a tool result explaining what to fix; revise the plan and call plan_exit again.",
			InputSchema: GenerateSchema(PlanExitInput{}),
		},
	}
}

// Name returns the canonical tool name.
func (t *PlanExitTool) Name() string { return "plan_exit" }

// Definition returns the API tool definition.
func (t *PlanExitTool) Definition() api.ToolDefinition { return t.definition }

// MaxResultBytes — the result is a fixed short string; the
// per-turn aggregator clip ceiling is irrelevant. Default is fine.
func (t *PlanExitTool) MaxResultBytes() int { return 0 }

// IsReadOnly reports true — plan_exit reads no files itself; any
// write/edit on the plan markdown is performed by the model
// through the `write` and `edit` tools before plan_exit is
// called. Marking it read-only also keeps the `[f] all files`
// approval shortcut behaviour consistent across read-only
// tools (though the TUI routes plan_exit to the dedicated
// approval overlay rather than the standard one-line prompt).
func (t *PlanExitTool) IsReadOnly() bool { return true }

// IsConcurrencySafe reports false — plan_exit must run alone in
// its turn. There's no real race on Execute, but mixing it with
// other tool calls would muddle the approval UX.
func (t *PlanExitTool) IsConcurrencySafe() bool { return false }

// PermissionKey returns "" — plan_exit has no per-call key worth
// matching against rule patterns.
func (t *PlanExitTool) PermissionKey(_ []byte) string { return "" }

// FormatForDisplay renders the tool-call header for chat.
func (t *PlanExitTool) FormatForDisplay(_ []byte) string {
	return "PlanExit()"
}

// Execute is a no-op success. The post-execute hook in run.go
// converts this success into an EventPlanApproved event for the
// TUI.
func (t *PlanExitTool) Execute(_ context.Context, _ agent.ToolContext, _ []byte) (agent.Result, error) {
	const msg = "plan approved; switching to the build agent."
	return agent.Result{
		Content:        msg,
		Bytes:          len(msg),
		DisplaySummary: "approved",
	}, nil
}
