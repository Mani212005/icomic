-- The panel repository and AI contract include these scene details.
-- Upgrade existing installations without replacing the initial migration.
ALTER TABLE panels
    ADD COLUMN character_actions TEXT NOT NULL DEFAULT '',
    ADD COLUMN camera_angle TEXT NOT NULL DEFAULT '',
    ADD COLUMN background TEXT NOT NULL DEFAULT '';
