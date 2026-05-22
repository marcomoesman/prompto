package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed schema/*.sql
var migrationFS embed.FS

// applyMigrations applies every migration whose numeric prefix is greater
// than the maximum version recorded in schema_version. Each migration runs
// in its own transaction. Idempotent: safe to call on an up-to-date DB.
//
// ctx scopes the schema-version probe and every migration transaction;
// a Ctrl+C at startup aborts a slow migration cleanly instead of
// hanging until SQLite finishes naturally.
func (s *Store) applyMigrations(ctx context.Context) error {
	// The schema_version table is created by 001_initial.sql itself. Before
	// the first migration runs, the table doesn't exist; treat that as
	// version 0.
	current, err := s.currentSchemaVersion(ctx)
	if err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationFS, "schema")
	if err != nil {
		return fmt.Errorf("store: reading embedded schema: %w", err)
	}

	type migration struct {
		version int
		name    string
	}
	var migrations []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := parseMigrationVersion(e.Name())
		if err != nil {
			return fmt.Errorf("store: %w", err)
		}
		migrations = append(migrations, migration{version: v, name: e.Name()})
	}
	slices.SortFunc(migrations, func(a, b migration) int { return a.version - b.version })

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("schema/" + m.name)
		if err != nil {
			return fmt.Errorf("store: reading %s: %w", m.name, err)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: begin migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: applying %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
			m.version, time.Now().UnixMilli(),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: recording version for %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) currentSchemaVersion(ctx context.Context) (int, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`,
	).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("store: checking schema_version: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}
	var v sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(version) FROM schema_version`,
	).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: reading schema_version: %w", err)
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

// parseMigrationVersion pulls the leading integer out of a filename like
// "001_initial.sql".
func parseMigrationVersion(name string) (int, error) {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0, fmt.Errorf("migration %q: missing version prefix", name)
	}
	v, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0, fmt.Errorf("migration %q: bad version prefix: %w", name, err)
	}
	return v, nil
}
