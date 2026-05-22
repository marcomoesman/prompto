package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
)

// PlanFileName is the relative path under cwd where plan-agent runs
// originally wrote their plan output: `.prompto/plans/<sessionID>.md`.
// The model picks a human-readable slug at first
// write and the chosen path is stored on the session row; this
// helper is now the FALLBACK shape, returned only when no slug has
// been recorded yet (legacy sessions, or new sessions before the
// model's first plan write).
func PlanFileName(sessionID string) string {
	return filepath.Join(".prompto", "plans", sessionID+".md")
}

// PlanFilePath returns the absolute legacy plan-file path for
// sessionID under cwd. An empty sessionID returns "". For most
// callers, ResolvePlanPath is the right entry point — this function
// is the fallback target it falls back to.
func PlanFilePath(cwd, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	return filepath.Join(cwd, PlanFileName(sessionID))
}

// ResolvePlanPathInput is the parameter struct for ResolvePlanPath.
// Declared before the function consuming it (per project style:
// input structs precede their consumers).
type ResolvePlanPathInput struct {
	// Cwd is the workspace root the plan file lives under. Required
	// for the legacy-fallback path.
	Cwd string
	// SessionID is the active session id, used as the legacy
	// filename when no persisted path is available.
	SessionID string
	// PersistedPath is the value loaded from sessions.plan_path
	// column. Empty means "not yet recorded" — resolver
	// falls back to the legacy filename.
	PersistedPath string
}

// ResolvePlanPath returns the canonical plan-file path for the
// given session. PersistedPath wins when set; otherwise the resolver
// falls back to the legacy `<cwd>/.prompto/plans/<sessionID>.md`
// shape so legacy sessions keep working
// untouched.
//
// Returns "" when both PersistedPath and SessionID are empty.
func ResolvePlanPath(in ResolvePlanPathInput) string {
	if in.PersistedPath != "" {
		return in.PersistedPath
	}
	return PlanFilePath(in.Cwd, in.SessionID)
}

// IsPlanFilePath reports whether path lives directly under
// `<cwd>/.prompto/plans/` and ends in `.md`. Used by run.go to
// detect the model's first plan-file write so the run loop can
// persist the chosen path on the session row.
//
// Excludes paths in the `.history/` subdirectory — those are the
// Backup-on-edit shadows, not user-facing plan files.
// Excludes paths outside cwd entirely (defends against the model
// writing somewhere unexpected even if a hypothetical permission
// rule allowed it).
func IsPlanFilePath(cwd, path string) bool {
	if cwd == "" || path == "" {
		return false
	}
	if !strings.HasSuffix(path, ".md") {
		return false
	}
	plansDir := filepath.Join(cwd, ".prompto", "plans")
	rel, err := filepath.Rel(plansDir, path)
	if err != nil {
		return false
	}
	// Reject anything that escapes plansDir (`..`) or descends into
	// a subdirectory (e.g. `.history/foo.md`). ContainsAny catches
	// both separators so a path that survived normalization with
	// forward slashes (e.g. an LLM-emitted relative path that's
	// already in slash form before filepath.Clean canonicalizes)
	// can't slip a subdirectory past the check on Windows.
	if rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	if strings.ContainsAny(rel, `/\`) {
		return false
	}
	return true
}

// BuildSwitchReminderBody returns the reminder body (no <system-reminder>
// wrapper) the TUI queues as a one-shot when the user cycles plan→build
// and a plan file already exists. The notifier wraps it via WrapReminder
// before injecting.
func BuildSwitchReminderBody(planFilePath string) string {
	return fmt.Sprintf("A plan file exists at `%s`. Read it first to orient. Execute the plan defined within it.", planFilePath)
}

func lastUserIndex(msgs []api.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == api.RoleUser {
			return i
		}
	}
	return -1
}
