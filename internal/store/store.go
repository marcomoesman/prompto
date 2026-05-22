// Package store persists sessions, messages, and file changes to a
// project-local SQLite database. It uses the pure-Go modernc.org/sqlite
// driver so prompto continues to build with CGO_ENABLED=0.
//
// The interface is intentionally narrow: the agent and tool packages don't
// import store directly. They use the agent.Store and agent.FileChangeSink
// interfaces, and main.go wires the concrete types from this package.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/marcomoesman/prompto/internal/privatefs"

	_ "modernc.org/sqlite"
)

// Store is the handle for the prompto SQLite database. Construct via Open;
// release with Close. Safe for concurrent callers; underlying database/sql
// handles pool management.
type Store struct {
	db *sql.DB
}

// OpenInput bundles the inputs to Open. Declared before the function per
// CLAUDE.md.
type OpenInput struct {
	// Path is the SQLite database path. Use ":memory:" for tests. Parent
	// directories are created if missing.
	Path string
	// Ctx is the context that bounds startup work — pragmas, schema
	// version probe, and pending migrations. A nil/zero context falls
	// back to context.Background(). Migrations on a large DB can take
	// seconds; passing a cancellable context lets a Ctrl+C at startup
	// abort cleanly instead of hanging the process.
	Ctx context.Context
}

// Open connects to the SQLite database at in.Path (creating parent dirs and
// the DB file as needed), applies WAL + foreign-key PRAGMAs, and runs any
// pending migrations. The returned Store is ready for use.
func Open(in OpenInput) (*Store, error) {
	if in.Path == "" {
		return nil, fmt.Errorf("store: Path is required")
	}
	ctx := in.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if in.Path != ":memory:" {
		if err := privatefs.EnsureDir(filepath.Dir(in.Path)); err != nil {
			return nil, fmt.Errorf("store: creating parent dir: %w", err)
		}
	}

	db, err := sql.Open("sqlite", in.Path)
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", in.Path, err)
	}

	// SQLite is single-writer at the file level. Letting Go's pool
	// open multiple connections only produces SQLITE_BUSY storms
	// among them; serializing through one connection gives clean,
	// queued writes (busy_timeout below covers any contention with
	// external readers). Reads on this single connection are still
	// fast because WAL mode below avoids reader-blocks-writer
	// contention with external processes — but for prompto's workload
	// (one main agent goroutine + occasional reads from compaction)
	// a single session-scoped goroutine is the norm anyway.
	//
	// This was tried as a 4-connection pool with busy_timeout=5000 in
	// May 2026 to satisfy a "could become a bottleneck during
	// compaction" audit suggestion. The change immediately broke
	// TestAppendMessage_ConcurrentOrdinalsAreUniqueAndContiguous: 32
	// concurrent AppendMessage calls produced ~20 SQLITE_BUSY
	// failures because PRAGMA busy_timeout applies per-connection
	// and the second writer's connection bounces back to Go before
	// the first writer releases the lock. Until the modernc driver
	// gains a connection-initializer hook (or we move to
	// BEGIN IMMEDIATE-style write coordination at the Go layer),
	// MaxOpenConns(1) is the correct, regression-test-validated
	// choice. Don't change without re-running that test.
	db.SetMaxOpenConns(1)

	// Pragmas run before any schema work. busy_timeout helps under light
	// contention; foreign_keys enforces references; journal_mode=WAL allows
	// concurrent readers during a write (skipped on :memory:).
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}
	if in.Path != ":memory:" {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL")
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: %s: %w", p, err)
		}
	}

	s := &Store{db: db}
	if err := s.applyMigrations(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if in.Path != ":memory:" {
		_ = privatefs.HardenFile(in.Path)
		_ = privatefs.HardenFile(in.Path + "-wal")
		_ = privatefs.HardenFile(in.Path + "-shm")
	}
	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
