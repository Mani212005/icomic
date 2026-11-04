-- A 12-page book needs roughly 30 images. Leave room for revisions, and
-- permit two books per rolling day while retaining a monthly spend guard.
ALTER TABLE generation_policy ADD COLUMN max_book_images INT NOT NULL DEFAULT 40 CHECK (max_book_images >= 0);
ALTER TABLE generation_policy ALTER COLUMN max_daily_images SET DEFAULT 80;
ALTER TABLE generation_policy ALTER COLUMN max_monthly_images SET DEFAULT 800;
-- Preserve explicit operator overrides, including isolated e2e profiles.
UPDATE generation_policy SET max_daily_images=80 WHERE max_daily_images=25;
UPDATE generation_policy SET max_monthly_images=800 WHERE max_monthly_images=250;

-- SET NULL preserves daily/monthly usage when a project is deleted.
ALTER TABLE image_quota_usage ADD COLUMN project_id UUID REFERENCES projects(id) ON DELETE SET NULL;
UPDATE image_quota_usage q SET project_id=j.project_id FROM generation_jobs j WHERE q.generation_job_id=j.id;
CREATE INDEX idx_image_quota_usage_project ON image_quota_usage (project_id);
-- Full refunds release image slots; the credit ledger remains the audit trail.
DELETE FROM image_quota_usage q USING credit_ledger d, credit_ledger f
WHERE q.generation_job_id=d.generation_job_id AND f.generation_job_id=d.generation_job_id
AND d.transaction_type='debit' AND f.transaction_type='refund' AND f.amount=d.amount;
