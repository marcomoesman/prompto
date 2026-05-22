package permission

import "fmt"

// Mode governs how the Evaluator treats tool calls at the top level.
type Mode int

const (
	// ModeDefault applies the ruleset + protected-file guard. Unresolved
	// cases ask the user.
	ModeDefault Mode = iota

	// ModeAcceptEdits auto-allows Edit and Write (regardless of ruleset),
	// falls through to ruleset+prompt for everything else. Useful for
	// focused sessions where the user trusts the model to edit freely but
	// still wants to gate shell commands.
	ModeAcceptEdits

	// ModeBypass skips the ruleset entirely — every tool call is allowed.
	// Only entered via --yolo or --mode bypass; never via a keybind.
	ModeBypass
)

// String returns the lowercase canonical name.
func (m Mode) String() string {
	switch m {
	case ModeDefault:
		return "default"
	case ModeAcceptEdits:
		return "acceptEdits"
	case ModeBypass:
		return "bypass"
	default:
		return fmt.Sprintf("mode(%d)", m)
	}
}

// ParseMode converts a user-facing name to a Mode. Accepts the canonical
// names plus a couple of lowercase aliases.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "default":
		return ModeDefault, nil
	case "acceptEdits", "acceptedits", "accept-edits":
		return ModeAcceptEdits, nil
	case "bypass":
		return ModeBypass, nil
	default:
		return 0, fmt.Errorf("unknown permission mode %q (expected default | acceptEdits | bypass)", s)
	}
}

// Cycle advances the mode for interactive cycling. Returns default →
// acceptEdits → default. Bypass is sticky — once set, cycling is a no-op
// so users can't accidentally leave bypass by pressing the cycle key.
func Cycle(m Mode) Mode {
	switch m {
	case ModeDefault:
		return ModeAcceptEdits
	case ModeAcceptEdits:
		return ModeDefault
	default:
		return m
	}
}
