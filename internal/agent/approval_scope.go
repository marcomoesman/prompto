package agent

import "context"

type subagentApprovalScopeKey struct{}

// SubagentApprovalScope identifies the child run currently asking for a
// permission decision. It is carried on the approval context only; it is not
// persisted or sent to the model.
type SubagentApprovalScope struct {
	AgentName string
	SessionID string
}

// WithSubagentApprovalScope marks ctx as belonging to a subagent run so the
// approval UI can offer a child-lifetime allow option.
func WithSubagentApprovalScope(ctx context.Context, agentName, sessionID string) context.Context {
	return context.WithValue(ctx, subagentApprovalScopeKey{}, SubagentApprovalScope{
		AgentName: agentName,
		SessionID: sessionID,
	})
}

// SubagentApprovalScopeFromContext returns the child approval scope when ctx
// belongs to a spawned subagent run.
func SubagentApprovalScopeFromContext(ctx context.Context) (SubagentApprovalScope, bool) {
	scope, ok := ctx.Value(subagentApprovalScopeKey{}).(SubagentApprovalScope)
	if !ok || scope.AgentName == "" {
		return SubagentApprovalScope{}, false
	}
	return scope, true
}
