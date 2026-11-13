ALTER TABLE chapters
    DROP CONSTRAINT IF EXISTS chapters_important_events_array,
    DROP CONSTRAINT IF EXISTS chapters_character_names_array;

ALTER TABLE chapters
    DROP COLUMN IF EXISTS important_events,
    DROP COLUMN IF EXISTS character_names,
    DROP COLUMN IF EXISTS ending;
