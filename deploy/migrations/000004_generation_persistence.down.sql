DROP TABLE IF EXISTS image_quota_usage;
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_project_id_fkey;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE;
ALTER TABLE credit_ledger DROP CONSTRAINT credit_ledger_generation_job_id_fkey;
ALTER TABLE credit_ledger ADD CONSTRAINT credit_ledger_generation_job_id_fkey
    FOREIGN KEY (generation_job_id) REFERENCES generation_jobs(id) ON DELETE CASCADE;
DROP TABLE IF EXISTS generation_policy;
DROP INDEX IF EXISTS uq_generation_jobs_active_entity;
ALTER TABLE generation_jobs DROP COLUMN entity_id;
