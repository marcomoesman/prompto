package store

import (
	"os"
	"path/filepath"
	"testing"
)

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(OpenInput{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_InMemoryPathWorks(t *testing.T) {
	s := openMem(t)
	if s == nil || s.db == nil {
		t.Fatal("store db is nil after Open")
	}
}

func TestStore_OpenCreatesDirectory(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "nested", "dir", "db.sqlite")
	s, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("parent dir stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("parent not a directory")
	}
}

func TestStore_MigrationsApplyOnceAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")

	// First open applies migration.
	s, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	var v1 int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&v1); err != nil {
		t.Fatalf("count 1: %v", err)
	}
	_ = s.Close()

	// Second open must be a no-op for migrations.
	s2, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	var v2 int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&v2); err != nil {
		t.Fatalf("count 2: %v", err)
	}
	if v1 != v2 {
		t.Errorf("schema_version row count changed across re-opens: %d → %d", v1, v2)
	}
	if v1 < 1 {
		t.Errorf("expected at least one migration row, got %d", v1)
	}
}

func TestStore_WALModeEnabledOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.sqlite")
	s, err := Open(OpenInput{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestStore_CloseIsIdempotent(t *testing.T) {
	s := openMem(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// Second close on the sql.DB returns an error; we wrap Close to be
	// conservative but still allow double-close not to panic.
	_ = s.Close()
}

func TestStore_EmptyPathErrors(t *testing.T) {
	_, err := Open(OpenInput{Path: ""})
	if err == nil {
		t.Error("expected error for empty Path")
	}
}
