package agent

import (
	"sync"

	"github.com/marcomoesman/prompto/internal/api"
)

// PreTurnContext is the per-turn snapshot a PreTurnChecker inspects to
// decide whether to fire. Built once by the run loop just before the
// outbound provider call. All fields are read-only — checkers must not
// mutate Conversation or its messages.
type PreTurnContext struct {
	Conversation *Conversation
	SessionID    string
	AgentName    string
	// InPlanMode reflects the running agent's AgentDefinition.PlanMode.
	// Empty PlanFilePath alone is ambiguous (could mean "not in plan
	// mode" or "in plan mode but no file written yet"), so the
	// PlanModeChecker uses this flag to disambiguate.
	InPlanMode   bool
	PlanFilePath string // empty when not in plan mode, OR in plan mode but the model hasn't written yet
	Todos        []Todo // current persisted list
	Verification VerificationHint
}

// PreTurnChecker is one stateless rule that may emit a reminder body per
// turn. Empty return means "decline." Implementations live alongside the
// notifier; the default registry seeds four built-ins (plan_mode,
// verify_after_edit, todowrite_stale, web_vs_local).
type PreTurnChecker interface {
	// Name is the checker identifier. Used for logging / dedup; no two
	// checkers should share a name within one notifier.
	Name() string
	// Check returns the reminder body (without the <system-reminder>
	// wrapper). Empty means "do not fire this turn."
	Check(rc PreTurnContext) string
}

// RemindNotifier funnels reminders into the run loop. Two distinct
// surfaces:
//   - PreTurn runs every registered PreTurnChecker against rc and returns
//     non-empty results in registration order.
//   - QueueOneShot / ConsumeOneShot hold a transient FIFO of reminders
//     fired exactly once. Used for events the run loop can't observe
//     directly (TUI agent switch, build-mode entry on existing plan).
//
// Implementations must be safe for concurrent QueueOneShot calls; PreTurn
// and ConsumeOneShot are only called from the run loop goroutine.
type RemindNotifier interface {
	PreTurn(rc PreTurnContext) []string
	ConsumeOneShot() []string
	QueueOneShot(text string)
}

// NewDefaultNotifier returns a notifier seeded with the four built-in
// PreTurnCheckers: plan_mode, verify_after_edit, todowrite_stale,
// web_vs_local. Pass extra checkers to append after the built-ins.
func NewDefaultNotifier(extra ...PreTurnChecker) RemindNotifier {
	checkers := []PreTurnChecker{
		PlanModeChecker{},
		VerifyAfterEditChecker{},
		TodoWriteStaleChecker{Threshold: defaultStaleToolCalls},
		WebVsLocalChecker{},
	}
	checkers = append(checkers, extra...)
	return &defaultNotifier{checkers: checkers}
}

// NewNotifier builds a notifier with only the supplied checkers (no
// built-ins). Tests use this; production paths reach for
// NewDefaultNotifier.
func NewNotifier(checkers ...PreTurnChecker) RemindNotifier {
	return &defaultNotifier{checkers: checkers}
}

// maxOneShots caps the in-memory one-shot reminder queue so a runaway
// caller (a buggy tool, a notifier wired into a tight loop) can't
// grow the slice unboundedly. When exceeded, the oldest entry is
// dropped — recent reminders are more relevant to the next turn.
const maxOneShots = 16

type defaultNotifier struct {
	checkers []PreTurnChecker

	mu       sync.Mutex
	oneShots []string
}

func (n *defaultNotifier) PreTurn(rc PreTurnContext) []string {
	if n == nil {
		return nil
	}
	out := make([]string, 0, len(n.checkers))
	for _, c := range n.checkers {
		if msg := c.Check(rc); msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

func (n *defaultNotifier) ConsumeOneShot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.oneShots) == 0 {
		return nil
	}
	out := n.oneShots
	n.oneShots = nil
	return out
}

func (n *defaultNotifier) QueueOneShot(text string) {
	if text == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.oneShots = append(n.oneShots, text)
	if len(n.oneShots) > maxOneShots {
		// Drop the oldest entries; copy the tail down so the slice
		// can't grow unbounded under a misbehaving caller.
		drop := len(n.oneShots) - maxOneShots
		n.oneShots = append(n.oneShots[:0], n.oneShots[drop:]...)
	}
}

// WrapReminder wraps a reminder body in the <system-reminder> tag the
// model recognizes. Centralized so the wire format stays consistent
// across checkers and one-shot callers.
func WrapReminder(body string) string {
	if body == "" {
		return ""
	}
	return "<system-reminder>\n" + body + "\n</system-reminder>"
}

// InjectReminders is the bulk version of InjectReminder: each non-empty
// body is wrapped and appended to the most recent user message as its
// own text block. Returns msgs unchanged when there is no user message
// to attach to. Order is preserved.
func InjectReminders(msgs []api.Message, bodies []string) []api.Message {
	if len(bodies) == 0 {
		return msgs
	}
	idx := lastUserIndex(msgs)
	if idx < 0 {
		return msgs
	}
	out := make([]api.Message, len(msgs))
	copy(out, msgs)
	target := out[idx]
	contents := make([]api.ContentBlock, len(target.Content), len(target.Content)+len(bodies))
	copy(contents, target.Content)
	for _, body := range bodies {
		wrapped := WrapReminder(body)
		if wrapped == "" {
			continue
		}
		contents = append(contents, api.ContentBlock{Type: api.BlockText, Text: wrapped})
	}
	target.Content = contents
	out[idx] = target
	return out
}
