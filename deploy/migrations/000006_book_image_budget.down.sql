-- Restore the previous attempt-counting semantics for extant jobs.
INSERT INTO image_quota_usage (user_id,generation_job_id,created_at)
SELECT j.user_id,j.id,j.created_at FROM generation_jobs j
WHERE j.job_type IN ('character_reference','draft_sketch','final_line_art')
ON CONFLICT (generation_job_id) DO NOTHING;
DROP INDEX IF EXISTS idx_image_quota_usage_project;
ALTER TABLE image_quota_usage DROP COLUMN project_id;
ALTER TABLE generation_policy DROP COLUMN max_book_images;
ALTER TABLE generation_policy ALTER COLUMN max_daily_images SET DEFAULT 25;
ALTER TABLE generation_policy ALTER COLUMN max_monthly_images SET DEFAULT 250;
UPDATE generation_policy SET max_daily_images=25 WHERE max_daily_images=80;
UPDATE generation_policy SET max_monthly_images=250 WHERE max_monthly_images=800;
