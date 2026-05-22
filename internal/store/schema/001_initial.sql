CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER NOT NULL PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    parent_id  TEXT REFERENCES sessions(id),
    title      TEXT,
    model      TEXT NOT NULL,
    agent_name TEXT NOT NULL DEFAULT 'build',
    status     TEXT NOT NULL DEFAULT 'active',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_by_updated ON sessions(updated_at DESC);
CREATE INDEX IF NOT EXISTS sessions_by_parent  ON sessions(parent_id);

CREATE TABLE IF NOT EXISTS messages (
    id         TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id),
    ordinal    INTEGER NOT NULL,
    role       TEXT NOT NULL,
    content    TEXT NOT NULL,
    usage      TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE(session_id, ordinal)
);
CREATE INDEX IF NOT EXISTS messages_by_session ON messages(session_id, ordinal);

CREATE TABLE IF NOT EXISTS file_changes (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id     TEXT NOT NULL REFERENCES sessions(id),
    message_id     TEXT REFERENCES messages(id),
    tool_call_id   TEXT,
    path           TEXT NOT NULL,
    op             TEXT NOT NULL,
    truncated      INTEGER NOT NULL DEFAULT 0,
    content_before BLOB,
    content_after  BLOB,
    created_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS filechanges_by_session ON file_changes(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS filechanges_by_message ON file_changes(message_id);
