package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupPlan_MissingSourceIsNoop(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, ".prompto", "plans", "2026-04-30-foo.md")
	if err := BackupPlan(plan); err != nil {
		t.Fatalf("BackupPlan on missing source = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(plan), ".history")); !os.IsNotExist(err) {
		t.Errorf(".history dir should not exist when source missing: stat err = %v", err)
	}
}

func TestBackupPlan_CreatesSnapshot(t *testing.T) {
	dir := t.TempDir()
	plansDir := filepath.Join(dir, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	plan := filepath.Join(plansDir, "2026-04-30-foo.md")
	body := "# original plan\n## Context\nbecause\n"
	if err := os.WriteFile(plan, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := BackupPlan(plan); err != nil {
		t.Fatalf("BackupPlan: %v", err)
	}

	historyDir := filepath.Join(plansDir, ".history")
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("history entries = %d, want 1", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "2026-04-30-foo.") || !strings.HasSuffix(name, ".md") {
		t.Errorf("backup filename %q lacks expected stem/suffix", name)
	}
	got, err := os.ReadFile(filepath.Join(historyDir, name))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != body {
		t.Errorf("backup body mismatch:\n got: %q\nwant: %q", got, body)
	}
}

func TestBackupPlan_CollisionGetsCounterSuffix(t *testing.T) {
	dir := t.TempDir()
	plansDir := filepath.Join(dir, ".prompto", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	plan := filepath.Join(plansDir, "2026-04-30-foo.md")
	if err := os.WriteFile(plan, []byte("v1"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Three back-to-back backups within the same millisecond should
	// produce three distinct files. The exact file names are
	// timing-dependent — we assert distinctness, not the exact
	// counter values.
	for range 3 {
		if err := BackupPlan(plan); err != nil {
			t.Fatalf("BackupPlan: %v", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(plansDir, ".history"))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("history entries = %d, want 3", len(entries))
	}
	seen := make(map[string]bool)
	for _, e := range entries {
		if seen[e.Name()] {
			t.Errorf("duplicate filename: %q", e.Name())
		}
		seen[e.Name()] = true
	}
}

func TestLatestPlanBackup_NoHistoryDir(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, ".prompto", "plans", "2026-04-30-foo.md")
	got, err := LatestPlanBackup(plan)
	if err != nil {
		t.Fatalf("LatestPlanBackup err = %v, want nil", err)
	}
	if got != "" {
		t.Errorf("LatestPlanBackup = %q, want \"\"", got)
	}
}

func TestLatestPlanBackup_ReturnsLargestMS(t *testing.T) {
	dir := t.TempDir()
	historyDir := filepath.Join(dir, ".prompto", "plans", ".history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	plan := filepath.Join(filepath.Dir(historyDir), "2026-04-30-foo.md")

	for _, name := range []string{
		"2026-04-30-foo.100.md",
		"2026-04-30-foo.200.md",
		"2026-04-30-foo.150.md",
		"2026-04-30-foo.200-1.md",
		"2026-04-30-foo.200-2.md",
		"2026-04-30-other.300.md", // unrelated stem — must be ignored
		"2026-04-30-foo.bogus.md", // unparseable — must be skipped
	} {
		path := filepath.Join(historyDir, name)
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	got, err := LatestPlanBackup(plan)
	if err != nil {
		t.Fatalf("LatestPlanBackup: %v", err)
	}
	want := filepath.Join(historyDir, "2026-04-30-foo.200-2.md")
	if got != want {
		t.Errorf("latest = %q, want %q", got, want)
	}
}

func TestBackupPlan_EmptyPathErrors(t *testing.T) {
	if err := BackupPlan(""); err == nil {
		t.Error("BackupPlan(\"\") = nil, want error")
	}
}

func TestLatestPlanBackup_EmptyPathErrors(t *testing.T) {
	if _, err := LatestPlanBackup(""); err == nil {
		t.Error("LatestPlanBackup(\"\") = nil err, want error")
	}
}
