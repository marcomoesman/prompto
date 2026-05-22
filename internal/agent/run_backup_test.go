package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMaybeBackupPlanFiles_HappyPath verifies that a plan-mode write
// targeting an existing plan file gets snapshotted to `.history/`
// before dispatch. The file content on disk is not changed by the
// hook (the actual write happens later, in the dispatch phase) — we
// just verify the snapshot was taken from the current contents.
func TestMaybeBackupPlanFiles_HappyPath(t *testing.T) {
	cwd := t.TempDir()
	plansDir := filepath.Join(cwd, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	planPath := filepath.Join(plansDir, "2026-04-30-foo.md")
	original := "## Context\noriginal body\n"
	if err := os.WriteFile(planPath, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	plan := &toolCallPlan{
		acc:     &toolCallAccumulator{id: "c1", name: "write"},
		argsStr: jsonArgs(map[string]string{"path": planPath, "content": "new body"}),
	}
	events := make(chan Event, 4)
	maybeBackupPlanFiles([]*toolCallPlan{plan}, cwd, events)
	close(events)

	for ev := range events {
		if ev.Type == EventError {
			t.Errorf("unexpected EventError: %v", ev.Error)
		}
	}

	entries, err := os.ReadDir(filepath.Join(plansDir, ".history"))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("history entries = %d, want 1", len(entries))
	}
	body, err := os.ReadFile(filepath.Join(plansDir, ".history", entries[0].Name()))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(body) != original {
		t.Errorf("backup body = %q, want %q", body, original)
	}
}

// TestMaybeBackupPlanFiles_DeniedSkipped guards against the hook
// firing for plans that won't actually execute. A denied plan must
// not produce a backup — the file isn't getting overwritten.
func TestMaybeBackupPlanFiles_DeniedSkipped(t *testing.T) {
	cwd := t.TempDir()
	plansDir := filepath.Join(cwd, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	planPath := filepath.Join(plansDir, "2026-04-30-foo.md")
	if err := os.WriteFile(planPath, []byte("body"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	plan := &toolCallPlan{
		acc:     &toolCallAccumulator{id: "c1", name: "write"},
		argsStr: jsonArgs(map[string]string{"path": planPath}),
		denied:  "denied: not allowed",
	}
	events := make(chan Event, 1)
	maybeBackupPlanFiles([]*toolCallPlan{plan}, cwd, events)

	if _, err := os.Stat(filepath.Join(plansDir, ".history")); !os.IsNotExist(err) {
		t.Errorf(".history must not exist for denied plan: stat err = %v", err)
	}
}

// TestMaybeBackupPlanFiles_NonPlanFileSkipped guards the cwd-based
// IsPlanFilePath gate. A `write` to a path outside `.prompto/plans/`
// must not trigger a backup.
func TestMaybeBackupPlanFiles_NonPlanFileSkipped(t *testing.T) {
	cwd := t.TempDir()
	other := filepath.Join(cwd, "main.go")
	if err := os.WriteFile(other, []byte("package main"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	plan := &toolCallPlan{
		acc:     &toolCallAccumulator{id: "c1", name: "write"},
		argsStr: jsonArgs(map[string]string{"path": other}),
	}
	events := make(chan Event, 1)
	maybeBackupPlanFiles([]*toolCallPlan{plan}, cwd, events)

	plansDir := filepath.Join(cwd, ".prompto", "plans")
	if _, err := os.Stat(plansDir); !os.IsNotExist(err) {
		t.Errorf("plans dir must not exist for non-plan write: stat err = %v", err)
	}
}

// TestMaybeBackupPlanFiles_NonWriteToolSkipped: a `read` (or any
// other tool) targeting a plan file shouldn't snapshot. Only the
// tools that mutate the plan trigger the hook.
func TestMaybeBackupPlanFiles_NonWriteToolSkipped(t *testing.T) {
	cwd := t.TempDir()
	plansDir := filepath.Join(cwd, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	planPath := filepath.Join(plansDir, "2026-04-30-foo.md")
	if err := os.WriteFile(planPath, []byte("body"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	plan := &toolCallPlan{
		acc:     &toolCallAccumulator{id: "c1", name: "read"},
		argsStr: jsonArgs(map[string]string{"path": planPath}),
	}
	events := make(chan Event, 1)
	maybeBackupPlanFiles([]*toolCallPlan{plan}, cwd, events)

	if _, err := os.Stat(filepath.Join(plansDir, ".history")); !os.IsNotExist(err) {
		t.Errorf(".history must not exist for read tool: stat err = %v", err)
	}
}

// TestMaybeBackupPlanFiles_FirstWriteIsNoop covers the common case
// where the model is creating a plan for the first time. There's
// nothing to back up; the hook must not error.
func TestMaybeBackupPlanFiles_FirstWriteIsNoop(t *testing.T) {
	cwd := t.TempDir()
	plansDir := filepath.Join(cwd, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	planPath := filepath.Join(plansDir, "2026-04-30-foo.md")

	plan := &toolCallPlan{
		acc:     &toolCallAccumulator{id: "c1", name: "write"},
		argsStr: jsonArgs(map[string]string{"path": planPath}),
	}
	events := make(chan Event, 1)
	maybeBackupPlanFiles([]*toolCallPlan{plan}, cwd, events)
	close(events)

	for ev := range events {
		if ev.Type == EventError {
			t.Errorf("first-write should not produce EventError: %v", ev.Error)
		}
	}

	entries, _ := os.ReadDir(filepath.Join(plansDir, ".history"))
	if len(entries) != 0 {
		t.Errorf("first-write produced %d history entries, want 0", len(entries))
	}
}

// TestMaybeBackupPlanFiles_EditTool: edit calls trigger the hook
// the same way write calls do.
func TestMaybeBackupPlanFiles_EditTool(t *testing.T) {
	cwd := t.TempDir()
	plansDir := filepath.Join(cwd, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	planPath := filepath.Join(plansDir, "2026-04-30-foo.md")
	if err := os.WriteFile(planPath, []byte("body"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	plan := &toolCallPlan{
		acc:     &toolCallAccumulator{id: "c1", name: "edit"},
		argsStr: jsonArgs(map[string]string{"path": planPath, "old_string": "a", "new_string": "b"}),
	}
	events := make(chan Event, 1)
	maybeBackupPlanFiles([]*toolCallPlan{plan}, cwd, events)

	entries, err := os.ReadDir(filepath.Join(plansDir, ".history"))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("edit should snapshot once, got %d", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "2026-04-30-foo.") {
		t.Errorf("snapshot name %q lacks expected stem prefix", entries[0].Name())
	}
}
