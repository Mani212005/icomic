-- The outline the model returns carries more than a title and summary: the story events the chapter
-- must show, the characters in it and how it ends. They are kept (and editable) so panel generation
-- works from the approved outline, not from the summary alone.
ALTER TABLE chapters
    ADD COLUMN IF NOT EXISTS important_events JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS character_names JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS ending TEXT NOT NULL DEFAULT '';

ALTER TABLE chapters
    ADD CONSTRAINT chapters_important_events_array CHECK (jsonb_typeof(important_events) = 'array'),
    ADD CONSTRAINT chapters_character_names_array CHECK (jsonb_typeof(character_names) = 'array');
