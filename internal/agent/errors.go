package agent

import "errors"

// Termination sentinels. Run sends exactly one of these (or a wrapped
// transport error) on its Done channel before closing. Callers match with
// errors.Is.
var (
	ErrEndTurn      = errors.New("agent: end_turn")
	ErrMaxSteps     = errors.New("agent: max_steps reached")
	ErrUserDenied   = errors.New("agent: user denied tool call")
	ErrContextLimit = errors.New("agent: context limit exceeded")
)
