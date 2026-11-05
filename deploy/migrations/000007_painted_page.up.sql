ALTER TABLE pages
    ADD COLUMN painted_page_image_url TEXT,
    ADD COLUMN painted_page_image_job_id UUID REFERENCES generation_jobs(id) ON DELETE SET NULL,
    ADD COLUMN painted_page_approved_at TIMESTAMPTZ,
    ADD COLUMN painted_page_variant VARCHAR(32);

ALTER TABLE stage_approvals DROP CONSTRAINT IF EXISTS stage_approvals_entity_type_check;
ALTER TABLE stage_approvals ADD CONSTRAINT stage_approvals_entity_type_check CHECK (entity_type IN (
    'chapter_outline',
    'character_reference',
    'draft_page',
    'line_art_page',
    'colour_page',
    'painted_page'
));
