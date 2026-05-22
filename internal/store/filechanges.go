package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// FileChangeContentCapBytes is the per-side size cap. When either
// ContentBefore or ContentAfter exceeds this, both are stored as NULL and
// Truncated is set to 1.
const FileChangeContentCapBytes = 1 << 20 // 1 MB

// FileChange is one row from the file_changes table.
type FileChange struct {
	ID            int64
	SessionID     string
	MessageID     string
	ToolCallID    string
	Path          string
	Op            string // "create" | "modify" | "delete"
	Truncated     bool
	ContentBefore []byte
	ContentAfter  []byte
	// Mode is the filesystem mode bits of the file at change-record
	// time. Zero means "not recorded" (legacy rows from before the
	// 005 migration); /undo treats zero as a signal to fall back to
	// 0o644 on restore.
	Mode      uint32
	CreatedAt time.Time
}

// RecordFileChangeInput bundles the inputs to RecordFileChange. Declared
// before the function per CLAUDE.md.
type RecordFileChangeInput struct {
	SessionID     string
	MessageID     string // empty when unattributed
	ToolCallID    string // specific tool_use id within the assistant message
	Path          string
	Op            string // "create" | "modify" | "delete"
	ContentBefore []byte
	ContentAfter  []byte
	// Mode captures the file's filesystem mode bits at the moment
	// the change was recorded. Pass 0 to leave the column NULL.
	// /undo reads this back to preserve the executable bit on
	// restored deletes (a 0755 script restored as 0644 won't run).
	Mode uint32
}

// RecordFileChange writes a file_changes row. If either ContentBefore or
// ContentAfter exceeds FileChangeContentCapBytes, both sides are stored as
// NULL and truncated=1 is set on the row. The path and op are always
// preserved so phase 7's /undo can at least surface the change.
func (s *Store) RecordFileChange(ctx context.Context, in RecordFileChangeInput) error {
	if in.SessionID == "" {
		return fmt.Errorf("store: RecordFileChange: SessionID is required")
	}
	if in.Path == "" {
		return fmt.Errorf("store: RecordFileChange: Path is required")
	}
	if in.Op == "" {
		return fmt.Errorf("store: RecordFileChange: Op is required")
	}

	truncated := 0
	before, after := in.ContentBefore, in.ContentAfter
	if len(before) > FileChangeContentCapBytes || len(after) > FileChangeContentCapBytes {
		truncated = 1
		before, after = nil, nil
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO file_changes
		 (session_id, message_id, tool_call_id, path, op, truncated, content_before, content_after, mode, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.SessionID,
		nullableString(in.MessageID),
		nullableString(in.ToolCallID),
		in.Path, in.Op, truncated,
		nullableBytes(before), nullableBytes(after),
		nullableMode(in.Mode),
		time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("store: inserting file change: %w", err)
	}
	return nil
}

// ListFileChangesBySession returns file changes ordered by created_at DESC.
func (s *Store) ListFileChangesBySession(ctx context.Context, sessionID string) ([]FileChange, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, message_id, tool_call_id, path, op, truncated,
		        content_before, content_after, mode, created_at
		 FROM file_changes WHERE session_id = ?
		 ORDER BY created_at DESC, id DESC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list file changes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []FileChange
	for rows.Next() {
		var (
			fc         FileChange
			messageID  sql.NullString
			toolCallID sql.NullString
			truncated  int
			before     []byte
			after      []byte
			mode       sql.NullInt64
			createdMs  int64
		)
		if err := rows.Scan(
			&fc.ID, &fc.SessionID, &messageID, &toolCallID,
			&fc.Path, &fc.Op, &truncated, &before, &after, &mode, &createdMs,
		); err != nil {
			return nil, fmt.Errorf("store: scan file change: %w", err)
		}
		fc.MessageID = messageID.String
		fc.ToolCallID = toolCallID.String
		fc.Truncated = truncated != 0
		fc.ContentBefore = before
		fc.ContentAfter = after
		if mode.Valid {
			fc.Mode = uint32(mode.Int64)
		}
		fc.CreatedAt = time.UnixMilli(createdMs)
		out = append(out, fc)
	}
	return out, rows.Err()
}

// DeleteFileChange removes one file_changes row by id. Used by /undo after
// a successful revert so subsequent /undo invocations advance to older
// changes. Missing id returns ErrFileChangeNotFound.
func (s *Store) DeleteFileChange(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM file_changes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete file change: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFileChangeNotFound
	}
	return nil
}

// nullableBytes maps nil/empty slices to NULL so downstream tools can
// distinguish "no content recorded" from "empty content".
func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// nullableMode maps a zero mode to NULL so the column distinguishes
// "not recorded" (legacy rows / explicit skip) from "recorded as 0".
// File modes are never legitimately zero on Unix, so this is unambiguous.
func nullableMode(m uint32) any {
	if m == 0 {
		return nil
	}
	return int64(m)
}
