package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/marcomoesman/prompto/internal/api"
)

// AppendMessage persists one message against the session, computing the
// next ordinal transactionally. Usage may be nil. The call also bumps
// sessions.updated_at so --resume (most recent) stays accurate.
func (s *Store) AppendMessage(ctx context.Context, sessionID string, msg api.Message, usage *api.Usage) error {
	if sessionID == "" {
		return fmt.Errorf("store: AppendMessage: sessionID is required")
	}
	contentJSON, err := json.Marshal(msg.Content)
	if err != nil {
		return fmt.Errorf("store: marshaling content: %w", err)
	}
	var usageJSON sql.NullString
	if usage != nil {
		ub, err := json.Marshal(usage)
		if err != nil {
			return fmt.Errorf("store: marshaling usage: %w", err)
		}
		usageJSON = sql.NullString{String: string(ub), Valid: true}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UnixMilli()
	// Single-statement INSERT…SELECT computes MAX(ordinal)+1 at
	// insert time rather than via a separate SELECT, eliminating
	// the TOCTOU window that two concurrent transactions could fall
	// into. (SQLite's UNIQUE(session_id, ordinal) constraint would
	// catch a collision either way, but this avoids the constraint
	// firing in the first place — and removes the assumption that
	// AppendMessage is only ever called single-goroutine per session,
	// which we'd like to relax for concurrent summarizer paths.)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (id, session_id, ordinal, role, content, usage, created_at)
		 SELECT ?, ?, IFNULL(MAX(ordinal), -1) + 1, ?, ?, ?, ?
		   FROM messages WHERE session_id = ?`,
		msg.ID, sessionID, string(msg.Role),
		string(contentJSON), usageJSON, now,
		sessionID,
	); err != nil {
		return fmt.Errorf("store: inserting message: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET updated_at = ? WHERE id = ?`, now, sessionID,
	); err != nil {
		return fmt.Errorf("store: touching session: %w", err)
	}

	return tx.Commit()
}

// AppendSummaryMessage persists the summary message produced by a compaction
// AND records a compaction marker in a single transaction. The marker
// captures the persisted ordinal of replacedThroughMessageID — every
// message at or before that ordinal is implicitly replaced by the summary
// for resume purposes (see LoadMessages). Atomic: a failure between the
// summary insert and the compactions insert rolls both back.
//
// replacedThroughMessageID must reference an existing message in this
// session. ErrUnknownMessage is returned otherwise.
func (s *Store) AppendSummaryMessage(ctx context.Context, sessionID string, msg api.Message, replacedThroughMessageID string) error {
	if sessionID == "" {
		return fmt.Errorf("store: AppendSummaryMessage: sessionID is required")
	}
	if replacedThroughMessageID == "" {
		return fmt.Errorf("store: AppendSummaryMessage: replacedThroughMessageID is required")
	}
	contentJSON, err := json.Marshal(msg.Content)
	if err != nil {
		return fmt.Errorf("store: marshaling content: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin append summary: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var boundaryOrdinal int
	err = tx.QueryRowContext(ctx,
		`SELECT ordinal FROM messages WHERE id = ? AND session_id = ?`,
		replacedThroughMessageID, sessionID,
	).Scan(&boundaryOrdinal)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: AppendSummaryMessage: %w: %s", ErrUnknownMessage, replacedThroughMessageID)
		}
		return fmt.Errorf("store: looking up boundary ordinal: %w", err)
	}

	now := time.Now().UnixMilli()
	// Same INSERT…SELECT idiom as AppendMessage above — see that
	// comment for the TOCTOU rationale.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO messages (id, session_id, ordinal, role, content, usage, created_at)
		 SELECT ?, ?, IFNULL(MAX(ordinal), -1) + 1, ?, ?, NULL, ?
		   FROM messages WHERE session_id = ?`,
		msg.ID, sessionID, string(msg.Role),
		string(contentJSON), now,
		sessionID,
	); err != nil {
		return fmt.Errorf("store: inserting summary message: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO compactions (id, session_id, summary_message_id, replaced_through_ordinal, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		uuid.New().String(), sessionID, msg.ID, boundaryOrdinal, now,
	); err != nil {
		return fmt.Errorf("store: inserting compaction marker: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET updated_at = ? WHERE id = ?`, now, sessionID,
	); err != nil {
		return fmt.Errorf("store: touching session: %w", err)
	}

	return tx.Commit()
}

// LoadMessages returns the live messages for a session: every persisted row
// past the most recent compaction boundary, in ordinal order. When no
// compaction has occurred, the COALESCE(... , -1) floor lets every row
// through. The summary message itself sits at an ordinal greater than the
// boundary, so it is included naturally.
func (s *Store) LoadMessages(ctx context.Context, sessionID string) ([]api.Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, content, created_at FROM messages
		 WHERE session_id = ?
		   AND ordinal > COALESCE(
		       (SELECT MAX(replaced_through_ordinal) FROM compactions WHERE session_id = ?),
		       -1)
		 ORDER BY ordinal ASC`,
		sessionID, sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: loading messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []api.Message
	for rows.Next() {
		var (
			id        string
			role      string
			contentS  string
			createdMs int64
		)
		if err := rows.Scan(&id, &role, &contentS, &createdMs); err != nil {
			return nil, fmt.Errorf("store: scanning message: %w", err)
		}
		var blocks []api.ContentBlock
		if err := json.Unmarshal([]byte(contentS), &blocks); err != nil {
			return nil, fmt.Errorf("store: unmarshaling content for %s: %w", id, err)
		}
		out = append(out, api.Message{
			ID:        id,
			Role:      api.Role(role),
			Content:   blocks,
			CreatedAt: time.UnixMilli(createdMs),
		})
	}
	return out, rows.Err()
}

// CountMessages returns the number of persisted messages for a session.
func (s *Store) CountMessages(ctx context.Context, sessionID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count messages: %w", err)
	}
	return n, nil
}
