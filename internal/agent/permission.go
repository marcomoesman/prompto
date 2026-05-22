package agent

// PermissionEvaluator is the narrow interface the agent loop uses to resolve
// tool authorizations before prompting the user. The concrete
// implementation lives in internal/permission; agent stays free of that
// import. A nil Evaluator means "everything asks" — the safe default
// when no permission policy has been configured.
type PermissionEvaluator interface {
	// Evaluate returns DecisionAllow / DecisionDeny / DecisionAsk plus a
	// short human-readable reason for the non-Ask decisions.
	Evaluate(in EvaluateInput) EvaluateResult
}

// EvaluateInput mirrors permission.EvaluateInput without depending on the
// permission package. Fields align by name; structural satisfaction does
// the rest.
type EvaluateInput struct {
	Tool       string
	Key        string
	IsReadOnly bool
}

// EvaluateResult mirrors permission.EvaluateResult.
type EvaluateResult struct {
	Decision Decision
	Reason   string
}
