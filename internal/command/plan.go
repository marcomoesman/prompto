package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/diff"
)

// PlanCommand is the entry point for the `/plan` family. With no
// arguments it acts as a shortcut for `/agent plan` (the historical
// behaviour). With a subcommand it routes to:
//
//   - `revise <text>` — switch to plan if needed, queue a one-shot
//     reminder framing the user's feedback, and feed the feedback
//     through agent.Run as a fresh user message.
//   - `diff` — compare the current plan to the most recent
//     `.history/` snapshot and render a unified diff in chat.
//   - `approve` — validate the plan against the schema and, on pass,
//     open the same approval overlay the model-driven `plan_exit`
//     triggers.
type PlanCommand struct{}

// NewPlanCommand returns a /plan command.
func NewPlanCommand() Command { return PlanCommand{} }

// Name returns the canonical name.
func (PlanCommand) Name() string { return "plan" }

// Aliases lists alternate names.
func (PlanCommand) Aliases() []string { return nil }

// Kind reports KindLocal. The `revise` subcommand returns a Prompt
// that the TUI feeds through agent.Run — Result.Prompt is honoured
// regardless of the parent command's Kind, so a single command can
// host both local and prompt-emitting subcommands without the
// surface getting messy.
func (PlanCommand) Kind() Kind { return KindLocal }

// Help is the one-liner shown by /help. Subcommand help is rendered
// inline if a user runs `/plan` with an unknown subcommand.
func (PlanCommand) Help() string {
	return "switch to plan agent (or: revise <text> | diff | approve)"
}

// Exec dispatches based on args[0].
func (PlanCommand) Exec(ctx context.Context, args []string, env Env) (Result, error) {
	if len(args) == 0 {
		if err := env.SwitchAgent("plan"); err != nil {
			return Result{}, fmt.Errorf("switch to plan: %w", err)
		}
		return Result{}, nil
	}
	switch args[0] {
	case "revise":
		return runPlanRevise(ctx, args[1:], env)
	case "diff":
		return runPlanDiff(ctx, env)
	case "approve":
		return runPlanApprove(ctx, env)
	}
	return Result{}, fmt.Errorf("unknown subcommand %q (try: revise <text> | diff | approve)", args[0])
}

// BuildCommand is a shortcut for `/agent build`. Symmetric with /plan
// so the user can return to build without going through /agent.
type BuildCommand struct{}

// NewBuildCommand returns a /build command.
func NewBuildCommand() Command { return BuildCommand{} }

// Name returns the canonical name.
func (BuildCommand) Name() string { return "build" }

// Aliases lists alternate names.
func (BuildCommand) Aliases() []string { return nil }

// Kind reports KindLocal.
func (BuildCommand) Kind() Kind { return KindLocal }

// Help is the one-liner.
func (BuildCommand) Help() string { return "switch to the build agent" }

// Exec switches to the build primary.
func (BuildCommand) Exec(_ context.Context, _ []string, env Env) (Result, error) {
	if err := env.SwitchAgent("build"); err != nil {
		return Result{}, fmt.Errorf("switch to build: %w", err)
	}
	return Result{}, nil
}

// runPlanRevise handles `/plan revise <text>`. Switches to plan if
// the user is on a different agent, queues a system-reminder one-
// shot describing the feedback (so the model orients on it before
// the next assistant turn), and returns the user's text as a Prompt
// so the TUI feeds it as a normal user message.
func runPlanRevise(ctx context.Context, args []string, env Env) (Result, error) {
	feedback := strings.TrimSpace(strings.Join(args, " "))
	if feedback == "" {
		return Result{}, errors.New("/plan revise needs feedback text — try `/plan revise <what to change>`")
	}

	if env.AgentName() != "plan" {
		if err := env.SwitchAgent("plan"); err != nil {
			return Result{}, fmt.Errorf("switch to plan: %w", err)
		}
	}

	planPath, err := resolvePlanPathFromEnv(ctx, env)
	if err != nil {
		return Result{}, err
	}
	rel := planPath
	if cwd := env.Cwd(); cwd != "" {
		if r, err := filepath.Rel(cwd, planPath); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}

	if n := env.Notifier(); n != nil {
		body := fmt.Sprintf(
			"The user revised the plan request. Update the plan file at `%s` to reflect this feedback before continuing.\nFeedback: %s",
			rel, feedback,
		)
		// QueueOneShot takes a raw body — InjectReminders wraps every
		// body in <system-reminder> at injection time.
		n.QueueOneShot(body)
	}

	return Result{Prompt: feedback}, nil
}

// runPlanDiff handles `/plan diff`. Compares the live plan to the
// most recent `.history/` snapshot. No backup yet → friendly
// "no prior version" message; otherwise a unified diff rendered in
// a fenced code block.
func runPlanDiff(ctx context.Context, env Env) (Result, error) {
	planPath, err := resolvePlanPathFromEnv(ctx, env)
	if err != nil {
		return Result{}, err
	}
	current, err := os.ReadFile(planPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{Message: "no plan file yet — start one in plan mode"}, nil
		}
		return Result{}, fmt.Errorf("read plan: %w", err)
	}

	backupPath, err := agent.LatestPlanBackup(planPath)
	if err != nil {
		return Result{}, fmt.Errorf("locate prior version: %w", err)
	}
	if backupPath == "" {
		return Result{Message: "no prior version of this plan exists yet"}, nil
	}
	prior, err := os.ReadFile(backupPath)
	if err != nil {
		return Result{}, fmt.Errorf("read prior version: %w", err)
	}

	body := diff.Unified(string(prior), string(current))
	if body == "" {
		return Result{Message: "plan unchanged since the last revision"}, nil
	}
	return Result{Message: "```diff\n" + body + "```"}, nil
}

// runPlanApprove handles `/plan approve`. Validates the live plan
// markdown against the schema; on pass, signals the TUI to open the
// approval overlay in user-driven mode. Validation failures surface
// as an error so the chat shows the missing-section message and the
// user knows why approval is blocked.
func runPlanApprove(ctx context.Context, env Env) (Result, error) {
	planPath, err := resolvePlanPathFromEnv(ctx, env)
	if err != nil {
		return Result{}, err
	}
	body, err := os.ReadFile(planPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, errors.New("no plan file yet — start one in plan mode")
		}
		return Result{}, fmt.Errorf("read plan: %w", err)
	}
	if err := agent.ValidatePlanMarkdown(body); err != nil {
		return Result{}, fmt.Errorf("plan not ready for approval: %w", err)
	}
	return Result{OpenPlanApproval: true}, nil
}

// resolvePlanPathFromEnv resolves the active session's plan-file path
// using the same precedence the run loop and TUI use:
//
//  1. `sessions.plan_path` column if set.
//  2. Legacy `<cwd>/.prompto/plans/<sessionID>.md` fallback.
//
// Returns an error when neither is computable (no session, no cwd) so
// callers can surface a clear "no plan in this session yet" message
// rather than a silent empty path.
func resolvePlanPathFromEnv(ctx context.Context, env Env) (string, error) {
	cwd := env.Cwd()
	sessionID := env.SessionID()
	persisted := ""
	if s := env.Store(); s != nil && sessionID != "" {
		if p, err := s.LoadPlanPath(ctx, sessionID); err == nil {
			persisted = p
		}
	}
	planPath := agent.ResolvePlanPath(agent.ResolvePlanPathInput{
		Cwd:           cwd,
		SessionID:     sessionID,
		PersistedPath: persisted,
	})
	if planPath == "" {
		return "", errors.New("no plan in this session yet — open plan mode and write one first")
	}
	return planPath, nil
}
