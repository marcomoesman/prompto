CREATE TABLE IF NOT EXISTS compactions (
    id                       TEXT PRIMARY KEY,
    session_id               TEXT NOT NULL REFERENCES sessions(id),
    summary_message_id       TEXT NOT NULL REFERENCES messages(id),
    replaced_through_ordinal INTEGER NOT NULL,
    created_at               INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS compactions_by_session ON compactions(session_id, created_at DESC);
