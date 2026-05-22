-- Capture the original filesystem mode bits of files modified or
-- deleted by the agent so /undo can restore the correct permissions.
-- Without this, undoing a deleted 0755 script restored it as 0644 and
-- broke its executable bit. NULL on existing rows means "mode not
-- recorded" — undo falls back to 0o644 to match the prior behavior.
ALTER TABLE file_changes ADD COLUMN mode INTEGER;
