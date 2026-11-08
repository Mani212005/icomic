-- The provider's provenance for each generated image, kept exactly as the AI service
-- returned it. The image URL is unique per generation and never reused, so a row is
-- written once and export reads it back to prove what it attached.
CREATE TABLE IF NOT EXISTS artifact_provenance (
    image_url TEXT PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provenance JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_artifact_provenance_project_id ON artifact_provenance(project_id);
