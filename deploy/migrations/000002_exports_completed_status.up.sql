ALTER TABLE exports DROP CONSTRAINT IF EXISTS exports_status_check;
ALTER TABLE exports ADD CONSTRAINT exports_status_check
    CHECK (status IN ('pending', 'running', 'done', 'completed', 'failed', 'cancelled'));
ALTER TABLE exports DROP CONSTRAINT IF EXISTS chk_exports_provenance_preserved;
ALTER TABLE exports ADD CONSTRAINT chk_exports_provenance_preserved
    CHECK (status NOT IN ('done', 'completed') OR provenance_metadata_preserved = TRUE);
