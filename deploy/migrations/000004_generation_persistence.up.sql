-- Entity identity is required to guard concurrency per entity rather than per project.
ALTER TABLE generation_jobs ADD COLUMN entity_id UUID;
UPDATE generation_jobs j SET entity_id=c.id
    FROM characters c WHERE c.reference_image_job_id=j.id;
UPDATE generation_jobs j SET entity_id=p.id
    FROM pages p WHERE p.draft_image_job_id=j.id OR p.line_art_image_job_id=j.id;
CREATE UNIQUE INDEX uq_generation_jobs_active_entity
    ON generation_jobs (project_id, entity_id, job_type)
    WHERE entity_id IS NOT NULL AND status IN ('pending', 'running');

-- Runtime policy is durable and can be updated by operators without a deployment.
CREATE TABLE generation_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    kill_switch_active BOOLEAN NOT NULL DEFAULT FALSE,
    spend_threshold NUMERIC(14,4) NOT NULL DEFAULT 0 CHECK (spend_threshold >= 0),
    max_daily_images INT NOT NULL DEFAULT 25 CHECK (max_daily_images >= 0),
    max_monthly_images INT NOT NULL DEFAULT 250 CHECK (max_monthly_images >= 0)
);
INSERT INTO generation_policy (singleton) VALUES (TRUE);

-- Quota increments survive project/job deletion and are committed with each debit.
CREATE TABLE image_quota_usage (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generation_job_id UUID UNIQUE REFERENCES generation_jobs(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_image_quota_usage_user_created ON image_quota_usage (user_id, created_at);
INSERT INTO image_quota_usage (user_id, generation_job_id, created_at)
    SELECT user_id, id, created_at FROM generation_jobs
    WHERE job_type IN ('character_reference', 'draft_sketch', 'final_line_art');

-- A credit ledger is an audit history, including after its project is deleted.
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_project_id_fkey;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL;
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_generation_job_id_fkey;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_generation_job_id_fkey
    FOREIGN KEY (generation_job_id) REFERENCES generation_jobs(id) ON DELETE SET NULL;
