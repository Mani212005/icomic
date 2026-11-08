-- Durable dispatch outbox: the validated provider request is committed in the same
-- transaction as the job and its debit, so a crash after commit cannot leave a charged
-- job that was never attempted. One bounded dispatcher replays pending rows through the
-- idempotent AI queue (the job ID is the queue key) and finalizes saved results.
CREATE TABLE generation_dispatches (
    job_id UUID PRIMARY KEY REFERENCES generation_jobs(id) ON DELETE CASCADE,
    operation VARCHAR(64) NOT NULL,
    request JSONB NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered')),
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_generation_dispatches_pending ON generation_dispatches(lease_until) WHERE state = 'pending';
