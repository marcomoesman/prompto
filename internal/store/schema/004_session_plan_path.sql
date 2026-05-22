-- Track the plan markdown file's path per session so the
-- model can pick a human-readable slug at first write
-- (.prompto/plans/YYYY-MM-DD-<slug>.md) and resume can find it later.
-- NULL on existing rows means "not yet recorded" — callers fall
-- back to the legacy .prompto/plans/<sessionID>.md path.
ALTER TABLE sessions ADD COLUMN plan_path TEXT;
