UPDATE generation_jobs SET status = 'failed' WHERE status = 'uncertain';

ALTER TABLE generation_jobs
    DROP CONSTRAINT IF EXISTS generation_jobs_status_check;

ALTER TABLE generation_jobs
    ADD CONSTRAINT generation_jobs_status_check
    CHECK (status IN ('pending', 'running', 'done', 'failed', 'cancelled'));
