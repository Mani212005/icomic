package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// DispatchLease is how long a claim owns an envelope. A committed envelope is claimable at
// once: the request that created it only returns a receipt, and the one bounded dispatcher
// delivers it. A claim that never finishes (a crash) becomes claimable again when its lease
// lapses. The lease exceeds the AI queue's own delivery lease.
const DispatchLease = 20 * time.Minute

// DispatchRetryDelay spaces a released dispatch's next attempt.
const DispatchRetryDelay = 10 * time.Second

// GenerationEnvelope is the validated provider request committed with the job and debit.
// CreateGeneration stamps the new job ID into the request, which doubles as the queue key.
type GenerationEnvelope struct {
	Operation string
	Request   json.RawMessage
}

// PendingDispatch is a committed envelope whose job has not reached a terminal state.
type PendingDispatch struct {
	JobID     string
	Operation string
	Request   json.RawMessage
	Attempts  int
}

// DispatchStore is the outbox seam the dispatcher uses.
type DispatchStore interface {
	// ClaimDispatches leases up to limit pending envelopes. ignoreLeases reclaims envelopes
	// still leased to a previous process, which a restarting single-instance backend uses.
	ClaimDispatches(ctx context.Context, limit int, ignoreLeases bool) ([]PendingDispatch, error)
	// FinishDispatch retires an envelope once its job is terminal. It is idempotent.
	FinishDispatch(ctx context.Context, jobID string) error
	// ReleaseDispatch makes a pending envelope claimable again after DispatchRetryDelay.
	ReleaseDispatch(ctx context.Context, jobID string) error
}

// StampJobID writes the job ID into a request envelope, which is also the AI queue key.
func StampJobID(request json.RawMessage, jobID string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(request, &fields); err != nil {
		return nil, fmt.Errorf("generation envelope request must be a JSON object: %w", err)
	}
	id, err := json.Marshal(jobID)
	if err != nil {
		return nil, err
	}
	fields["job_id"] = id
	return json.Marshal(fields)
}

func insertDispatch(ctx context.Context, tx Transaction, jobID string, envelope *GenerationEnvelope) error {
	request, err := StampJobID(envelope.Request, jobID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO generation_dispatches (job_id,operation,request,lease_until)
        VALUES ($1::uuid,$2::varchar,$3::jsonb,NOW())`,
		jobID, envelope.Operation, string(request))
	return err
}

func (p *PostgresRepository) ClaimDispatches(ctx context.Context, limit int, ignoreLeases bool) ([]PendingDispatch, error) {
	// A job another path already settled (failed by status reconciliation, cancelled) needs no dispatch.
	if _, err := p.db.ExecContext(ctx, `UPDATE generation_dispatches d SET state='delivered', updated_at=NOW()
        FROM generation_jobs j WHERE j.id=d.job_id AND d.state='pending' AND j.status NOT IN ('pending','running')`); err != nil {
		return nil, err
	}
	rows, err := p.db.QueryContext(ctx, `WITH due AS (
            SELECT job_id FROM generation_dispatches
            WHERE state='pending' AND ($2::boolean OR lease_until<=NOW())
            ORDER BY created_at LIMIT $1 FOR UPDATE SKIP LOCKED)
        UPDATE generation_dispatches d SET attempts=d.attempts+1, updated_at=NOW(),
            lease_until=NOW()+$3::bigint*INTERVAL '1 microsecond'
        FROM due WHERE d.job_id=due.job_id
        RETURNING d.job_id::text, d.operation, d.request::text, d.attempts`, limit, ignoreLeases, DispatchLease.Microseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var claimed []PendingDispatch
	for rows.Next() {
		var d PendingDispatch
		var request string
		if err := rows.Scan(&d.JobID, &d.Operation, &request, &d.Attempts); err != nil {
			return nil, err
		}
		d.Request = json.RawMessage(request)
		claimed = append(claimed, d)
	}
	return claimed, rows.Err()
}

func (p *PostgresRepository) FinishDispatch(ctx context.Context, jobID string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE generation_dispatches SET state='delivered', updated_at=NOW() WHERE job_id=$1::uuid`, jobID)
	return err
}

func (p *PostgresRepository) ReleaseDispatch(ctx context.Context, jobID string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE generation_dispatches SET updated_at=NOW(),
        lease_until=NOW()+$2::bigint*INTERVAL '1 microsecond' WHERE job_id=$1::uuid AND state='pending'`, jobID, DispatchRetryDelay.Microseconds())
	return err
}
