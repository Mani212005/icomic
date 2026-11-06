-- Existing projects remain baked even if they allocate more pages after enablement.
ALTER TABLE projects ADD COLUMN lettering_mode TEXT NOT NULL DEFAULT 'baked'
    CHECK (lettering_mode IN ('baked', 'overlay'));
-- Upgrade only: existing pages, including the running book, stay baked.
ALTER TABLE pages ADD COLUMN lettering_mode TEXT NOT NULL DEFAULT 'baked'
    CHECK (lettering_mode IN ('baked', 'overlay'));
ALTER TABLE pages ADD COLUMN lettering JSONB;
ALTER TABLE pages ADD CONSTRAINT pages_baked_without_lettering
    CHECK (lettering_mode = 'overlay' OR lettering IS NULL);
ALTER TABLE pages ADD CONSTRAINT pages_overlay_without_ink
    CHECK (lettering_mode = 'baked' OR COALESCE(line_art_image_url, '') = '');
