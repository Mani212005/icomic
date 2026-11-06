-- Existing books retain their current appearance. New projects opt into the
-- print-heavy finish in application code.
ALTER TABLE projects ADD COLUMN finish_preset TEXT NOT NULL DEFAULT 'off'
    CHECK (finish_preset IN ('off', 'print', 'print-plus', 'print-heavy'));
ALTER TABLE projects ADD COLUMN finish_preset_explicit BOOLEAN NOT NULL DEFAULT FALSE;
