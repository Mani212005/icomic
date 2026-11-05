ALTER TABLE stage_approvals DROP CONSTRAINT IF EXISTS stage_approvals_entity_type_check;
ALTER TABLE stage_approvals ADD CONSTRAINT stage_approvals_entity_type_check CHECK (entity_type IN (
    'chapter_outline',
    'character_reference',
    'draft_page',
    'line_art_page',
    'colour_page'
));

ALTER TABLE pages
    DROP COLUMN IF EXISTS painted_page_variant,
    DROP COLUMN IF EXISTS painted_page_approved_at,
    DROP COLUMN IF EXISTS painted_page_image_job_id,
    DROP COLUMN IF EXISTS painted_page_image_url;
