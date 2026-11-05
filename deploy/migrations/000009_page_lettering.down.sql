ALTER TABLE pages DROP CONSTRAINT pages_overlay_without_ink;
ALTER TABLE pages DROP CONSTRAINT pages_baked_without_lettering;
ALTER TABLE pages DROP COLUMN lettering;
ALTER TABLE pages DROP COLUMN lettering_mode;
ALTER TABLE projects DROP COLUMN lettering_mode;
