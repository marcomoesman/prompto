// Package command defines the slash-command surface for prompto. The TUI
// detects a leading `/` on user input, looks the name up in the Registry,
// and dispatches the resolved Command. Commands fall into two kinds:
//
//   - Local: executed synchronously inside the TUI; the model never sees
//     the input.
//   - Expanding: synthesize a prompt that the TUI feeds through agent.Run
//     as if the user had typed it directly.
//
// Commands depend on internal/agent, internal/api, internal/compact,
// internal/permission, and internal/store. They must not import
// internal/tui — the dependency direction is one-way.
package command

import "context"

// Kind separates commands that run in the TUI from commands that synthesize
// a prompt for the model.
type Kind int

const (
	// KindLocal commands execute in the TUI; the model never sees the input.
	KindLocal Kind = iota
	// KindExpanding commands return Result.Prompt; the TUI feeds it through
	// agent.Run as a normal user message.
	KindExpanding
)

// String returns the canonical lowercase name for a Kind.
func (k Kind) String() string {
	switch k {
	case KindLocal:
		return "local"
	case KindExpanding:
		return "expanding"
	default:
		return "unknown"
	}
}

// Command is a registered slash command. Implementations are stateless —
// per-invocation context flows through Env and args.
type Command interface {
	// Name is the canonical command name without the leading slash.
	Name() string
	// Aliases are alternative names that resolve to this command. Optional.
	Aliases() []string
	// Kind reports whether this command is Local or Expanding.
	Kind() Kind
	// Help is the one-line description shown by /help.
	Help() string
	// Exec runs the command. Local commands act on env directly and return
	// a Result with chat output / control flags; Expanding commands return
	// Result.Prompt for the TUI to feed through agent.Run.
	Exec(ctx context.Context, args []string, env Env) (Result, error)
}

// Result is what a Command returns. Different fields apply to different
// kinds; see Command.Kind for which.
type Result struct {
	// Message is rendered as a system message in chat. Empty for silent
	// commands. Both kinds may set this.
	Message string
	// MessageMarkdown marks Message as GitHub-flavored markdown. The TUI
	// renders it through the chat markdown renderer instead of the plain
	// system-message style. Use only for command-owned formatted reports;
	// errors and status notices should stay plain text.
	MessageMarkdown bool

	// Prompt is the user-message text fed through agent.Run. Only
	// Expanding commands set this; the TUI ignores it for KindLocal.
	Prompt string

	// Quit causes the TUI to exit cleanly after handling the result.
	Quit bool

	// OpenPicker opens the full-screen sessions picker.
	OpenPicker bool

	// OpenHelp toggles the floating help overlay.
	OpenHelp bool

	// OpenModelPicker opens the arrow-key-driven model selection
	// overlay. Set by /model when invoked with no arguments. The TUI
	// builds its own picker model from env.Models() and handles the
	// keystrokes; commands don't drive the UI directly.
	OpenModelPicker bool

	// OpenPlanApproval opens the plan-approval overlay in
	// user-driven mode (no pending tool). Set by `/plan approve`
	// after the validator passes. The TUI builds the overlay from
	// the active session's plan path; on `y` the build flip + synth
	// message path that the model-driven `plan_exit` triggers runs
	// the same way.
	OpenPlanApproval bool
}
