package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// likePrefixEscaper neutralizes SQLite LIKE wildcards in user-supplied
// prefixes. Escape `\` first so we don't double-escape the chars we add.
var likePrefixEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Session is one row from the sessions table. ParentID is "" when NULL.
type Session struct {
	ID        string
	ParentID  string
	Title     string
	Model     string
	AgentName string
	Status    string // "active" | "ended"
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateSessionInput bundles the inputs to CreateSession. Declared before
// the function per CLAUDE.md.
type CreateSessionInput struct {
	Model     string
	AgentName string // defaults to "build"
	ParentID  string // "" for primary sessions
	Title     string // optional initial title
}

// CreateSession inserts a new session row and returns it. A fresh UUID is
// generated for the session ID.
func (s *Store) CreateSession(ctx context.Context, in CreateSessionInput) (Session, error) {
	if in.Model == "" {
		return Session{}, fmt.Errorf("store: CreateSession: Model is required")
	}
	agentName := in.AgentName
	if agentName == "" {
		agentName = "build"
	}

	now := time.Now()
	sess := Session{
		ID:        uuid.New().String(),
		ParentID:  in.ParentID,
		Title:     in.Title,
		Model:     in.Model,
		AgentName: agentName,
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}

	parent := nullableString(in.ParentID)
	title := nullableString(in.Title)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, parent_id, title, model, agent_name, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, parent, title, sess.Model, sess.AgentName, sess.Status,
		now.UnixMilli(), now.UnixMilli(),
	)
	if err != nil {
		return Session{}, fmt.Errorf("store: inserting session: %w", err)
	}
	return sess, nil
}

// GetSession fetches a session by exact id.
func (s *Store) GetSession(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, parent_id, title, model, agent_name, status, created_at, updated_at
		 FROM sessions WHERE id = ?`, id,
	)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	return sess, err
}

// FindSessionByPrefix resolves a session by id prefix (LIKE 'prefix%').
// Returns ErrPrefixTooShort when len(prefix) < MinSessionPrefix,
// ErrSessionNotFound when no match, ErrSessionAmbiguous when multiple.
//
// SQLite LIKE treats `%` and `_` as wildcards, so any user input is
// escaped before being bound — `/resume %` would otherwise match every
// session, and `/resume _abc` would match any-char-followed-by-abc.
// The escape character is `\`, declared explicitly with ESCAPE.
func (s *Store) FindSessionByPrefix(ctx context.Context, prefix string) (Session, error) {
	if len(prefix) < MinSessionPrefix {
		return Session{}, ErrPrefixTooShort
	}
	escaped := likePrefixEscaper.Replace(prefix)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, parent_id, title, model, agent_name, status, created_at, updated_at
		 FROM sessions WHERE id LIKE ? ESCAPE '\' LIMIT 2`,
		escaped+"%",
	)
	if err != nil {
		return Session{}, fmt.Errorf("store: prefix lookup: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var matches []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return Session{}, err
		}
		matches = append(matches, sess)
	}
	if err := rows.Err(); err != nil {
		return Session{}, err
	}
	switch len(matches) {
	case 0:
		return Session{}, ErrSessionNotFound
	case 1:
		return matches[0], nil
	default:
		return Session{}, ErrSessionAmbiguous
	}
}

// ListSessions returns up to limit sessions ordered by updated_at DESC.
// limit <= 0 applies a default of 50.
func (s *Store) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, parent_id, title, model, agent_name, status, created_at, updated_at
		 FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// SetSessionStatus updates status and bumps updated_at.
func (s *Store) SetSessionStatus(ctx context.Context, id, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("store: set status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// CreateChildSessionInput mirrors agent.CreateChildSessionInput so a
// *Store structurally satisfies agent.SpawnerStore. Defined here to keep
// the call site in agent's NewSpawner from importing internal/store.
type CreateChildSessionInput struct {
	ParentID  string
	AgentName string
	Model     string
	Title     string
}

// CreateChildSession is the SpawnerStore-shape entry point used by
// agent.NewSpawner. Wraps CreateSession and returns only the new id.
func (s *Store) CreateChildSession(ctx context.Context, in CreateChildSessionInput) (string, error) {
	sess, err := s.CreateSession(ctx, CreateSessionInput{
		Model:     in.Model,
		AgentName: in.AgentName,
		ParentID:  in.ParentID,
		Title:     in.Title,
	})
	if err != nil {
		return "", err
	}
	return sess.ID, nil
}

// SetAgentName updates agent_name and bumps updated_at. Used when --resume
// re-anchors a session whose agent has changed since the last run, or by
// future /agent commands. agent_name is NOT NULL in the schema, so an empty
// value is rejected here.
func (s *Store) SetAgentName(ctx context.Context, id, agentName string) error {
	if agentName == "" {
		return fmt.Errorf("store: SetAgentName: agentName is required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET agent_name = ?, updated_at = ? WHERE id = ?`,
		agentName, time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("store: set agent_name: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// SetModel updates the session's stored model and bumps updated_at. Used
// when /model switches the active model so the choice survives --resume.
// The resume path reads this value and falls back to cfg.Default.Model
// when the stored name is no longer in config.
func (s *Store) SetModel(ctx context.Context, id, model string) error {
	if model == "" {
		return fmt.Errorf("store: SetModel: model is required")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET model = ?, updated_at = ? WHERE id = ?`,
		model, time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("store: set model: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// DeleteAllSessions wipes every session row in this store along with
// its dependent messages, file_changes, compactions, and todos. Runs
// in a single transaction so a failure mid-delete leaves the DB
// untouched. Returns the number of sessions removed.
//
// Children are removed before parents because most relations only
// declare REFERENCES (not ON DELETE CASCADE) — a bare DELETE FROM
// sessions would fail with a foreign-key violation while
// foreign_keys=ON.
//
// This is the destructive backend behind the --clear-history CLI
// flag. Callers are expected to gate it behind a user confirmation.
func (s *Store) DeleteAllSessions(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: clear history: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Order matters: child rows first.
	for _, stmt := range []string{
		`DELETE FROM file_changes`,
		`DELETE FROM compactions`,
		`DELETE FROM messages`,
		`DELETE FROM todos`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return 0, fmt.Errorf("store: clear history: %s: %w", stmt, err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sessions`)
	if err != nil {
		return 0, fmt.Errorf("store: clear history: DELETE FROM sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: clear history: commit: %w", err)
	}
	return int(n), nil
}

// ListChildren returns every session whose parent_id matches parentID,
// ordered by created_at ASC so the TUI can render them in spawn order.
// Returns an empty slice when there are no children.
func (s *Store) ListChildren(ctx context.Context, parentID string) ([]Session, error) {
	if parentID == "" {
		return nil, fmt.Errorf("store: ListChildren: parentID is required")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, parent_id, title, model, agent_name, status, created_at, updated_at
		 FROM sessions WHERE parent_id = ? ORDER BY created_at ASC`, parentID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list children: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// SetSessionTitle updates title and bumps updated_at.
func (s *Store) SetSessionTitle(ctx context.Context, id, title string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
		nullableString(title), time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("store: set title: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// SetPlanPath records the plan markdown's chosen filesystem path on
// the session row. The plan agent picks a slug at first plan write
// (e.g. `.prompto/plans/2026-04-30-add-undo-flag.md`) and the run
// loop calls this to persist the path so resume reliably finds it.
//
// An empty path clears the column (NULL); callers that want to wipe
// a stale association can pass "".
func (s *Store) SetPlanPath(ctx context.Context, id, path string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET plan_path = ?, updated_at = ? WHERE id = ?`,
		nullableString(path), time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("store: set plan_path: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// LoadPlanPath returns the persisted plan markdown path for a
// session, or "" when the column is NULL (legacy sessions, or new
// sessions where the model hasn't written its plan yet). The empty
// string is the canonical "not yet chosen" sentinel — never an
// error condition.
func (s *Store) LoadPlanPath(ctx context.Context, id string) (string, error) {
	var path sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT plan_path FROM sessions WHERE id = ?`, id,
	).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: load plan_path: %w", err)
	}
	if !path.Valid {
		return "", nil
	}
	return path.String, nil
}

// rowScanner is the shared interface between sql.Row and sql.Rows for
// Scan so scanSession works on both.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(r rowScanner) (Session, error) {
	var (
		sess      Session
		parentID  sql.NullString
		title     sql.NullString
		createdMs int64
		updatedMs int64
	)
	if err := r.Scan(
		&sess.ID, &parentID, &title, &sess.Model, &sess.AgentName,
		&sess.Status, &createdMs, &updatedMs,
	); err != nil {
		return Session{}, err
	}
	sess.ParentID = parentID.String
	sess.Title = title.String
	sess.CreatedAt = time.UnixMilli(createdMs)
	sess.UpdatedAt = time.UnixMilli(updatedMs)
	return sess, nil
}

// nullableString converts an empty string to sql.NullString{Valid: false}
// and any non-empty string to Valid. Used to keep NULL semantics for text
// columns that treat "" as missing.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
