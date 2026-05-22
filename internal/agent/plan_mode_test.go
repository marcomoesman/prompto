package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanFilePath(t *testing.T) {
	got := PlanFilePath("/proj", "abc12345")
	want := filepath.Join("/proj", ".prompto", "plans", "abc12345.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPlanFilePath_EmptyID(t *testing.T) {
	if got := PlanFilePath("/proj", ""); got != "" {
		t.Errorf("empty session = %q, want empty", got)
	}
}

// TestResolvePlanPath_Persisted asserts the Phase-20 priority
// order: when the session row carries a recorded plan path, the
// resolver returns it verbatim, ignoring cwd / sessionID.
func TestResolvePlanPath_Persisted(t *testing.T) {
	got := ResolvePlanPath(ResolvePlanPathInput{
		Cwd:           "/proj",
		SessionID:     "abc12345",
		PersistedPath: "/proj/.prompto/plans/2026-04-30-undo-flag.md",
	})
	want := "/proj/.prompto/plans/2026-04-30-undo-flag.md"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolvePlanPath_FallbackToLegacy covers backward compat: a
// session without a persisted path falls back to the legacy
// `<sessionID>.md` shape so older sessions continue to find their
// plan file.
func TestResolvePlanPath_FallbackToLegacy(t *testing.T) {
	got := ResolvePlanPath(ResolvePlanPathInput{
		Cwd:       "/proj",
		SessionID: "abc12345",
	})
	want := filepath.Join("/proj", ".prompto", "plans", "abc12345.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolvePlanPath_EmptyAll asserts the resolver doesn't
// fabricate a path when both inputs are missing.
func TestResolvePlanPath_EmptyAll(t *testing.T) {
	if got := ResolvePlanPath(ResolvePlanPathInput{}); got != "" {
		t.Errorf("empty inputs → empty, got %q", got)
	}
}

func TestIsPlanFilePath_Match(t *testing.T) {
	cwd := "/proj"
	for _, tc := range []struct {
		name string
		path string
	}{
		{"legacy hex name", filepath.Join(cwd, ".prompto", "plans", "abc12345.md")},
		{"slug name", filepath.Join(cwd, ".prompto", "plans", "2026-04-30-undo.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !IsPlanFilePath(cwd, tc.path) {
				t.Errorf("IsPlanFilePath(%q) = false, want true", tc.path)
			}
		})
	}
}

func TestIsPlanFilePath_Mismatch(t *testing.T) {
	cwd := "/proj"
	for _, tc := range []struct {
		name string
		path string
	}{
		{"empty path", ""},
		{"non-md extension", filepath.Join(cwd, ".prompto", "plans", "foo.txt")},
		{"history subdirectory", filepath.Join(cwd, ".prompto", "plans", ".history", "x.md")},
		{"outside plans dir", filepath.Join(cwd, "main.go")},
		{"outside cwd entirely", "/elsewhere/.prompto/plans/x.md"},
		{"plans dir itself", filepath.Join(cwd, ".prompto", "plans")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if IsPlanFilePath(cwd, tc.path) {
				t.Errorf("IsPlanFilePath(%q) = true, want false", tc.path)
			}
		})
	}
}

func TestIsPlanFilePath_EmptyCwd(t *testing.T) {
	if IsPlanFilePath("", "/anywhere/.prompto/plans/x.md") {
		t.Error("empty cwd should always reject")
	}
}

func TestBuildSwitchReminderBody_ReferencesPlanFile(t *testing.T) {
	planPath := "/proj/.prompto/plans/x.md"
	r := BuildSwitchReminderBody(planPath)
	if !strings.Contains(r, planPath) {
		t.Errorf("body missing plan path: %q", r)
	}
	if !strings.Contains(r, "Read it first") {
		t.Errorf("body missing instruction: %q", r)
	}
	if strings.Contains(r, "<system-reminder>") {
		t.Errorf("body should not be pre-wrapped: %q", r)
	}
}
