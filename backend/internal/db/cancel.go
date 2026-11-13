package db

import (
	"context"
	"database/sql"
	"errors"

	"icomic/backend/internal/domain"
)

// CancelOutcome is the honest answer to a cancel request.
type CancelOutcome string

const (
	// CancelOutcomeCancelled: the job had not reached the provider. It is terminal, the
	// dispatcher will never send it, and its debit is refunded.
	CancelOutcomeCancelled CancelOutcome = "cancelled"
	// CancelOutcomeSettling: the job was already claimed or sent. It keeps running to its own
	// terminal state with its result and cost; nothing was stopped and nothing is refunded.
	CancelOutcomeSettling CancelOutcome = "settling"
	// CancelOutcomeFinished: the job was already terminal (done, failed or uncertain) and is unchanged.
	CancelOutcomeFinished CancelOutcome = "finished"
)

// CancelMessage is the error message stored on a job cancelled before it reached the provider.
const CancelMessage = "Cancelled before it reached the model. Nothing was charged."

// CancelResult reports a cancel request. Refunded is the amount returned to the user's balance
// by the cancellation, zero unless the outcome is cancelled and the job had been debited.
type CancelResult struct {
	Outcome  CancelOutcome
	Job      *domain.GenerationJob
	Refunded float64
}

// GenerationCanceller cancels a job that has not reached the provider.
type GenerationCanceller interface {
	CancelGeneration(ctx context.Context, jobID string) (*CancelResult, error)
}

// TerminalStatus reports a job status that is final. A terminal status is never reversed.
func TerminalStatus(status string) bool {
	switch status {
	case "done", "failed", "uncertain", "cancelled":
		return true
	}
	return false
}

// CancelGeneration atomically cancels a job that is still pending and whose envelope has not
// been claimed. The job row is locked first and then the envelope row, which the dispatcher's
// claim also takes with FOR UPDATE SKIP LOCKED, so a cancel and a claim of the same envelope
// have exactly one winner: a claim that commits first makes this answer settling, and a cancel
// that commits first makes every later claim skip the envelope. The terminal status, the
// cancelled envelope, the refund row and the released quota slot commit together or not at all.
// Repeating the request on a cancelled job returns the same result and never refunds twice.
func (p *PostgresRepository) CancelGeneration(ctx context.Context, jobID string) (*CancelResult, error) {
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Same lock order as creation and refunds: policy, then user, then the job.
	var singleton bool
	if err := tx.QueryRowContext(ctx, `SELECT singleton FROM generation_policy WHERE singleton=TRUE FOR UPDATE`).Scan(&singleton); err != nil {
		return nil, err
	}
	var userID, projectID string
	err = tx.QueryRowContext(ctx, `SELECT user_id,project_id FROM generation_jobs WHERE id=$1::uuid`, jobID).Scan(&userID, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1::uuid FOR UPDATE`, userID).Scan(&locked); err != nil {
		return nil, err
	}
	job, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM generation_jobs WHERE id=$1::uuid FOR UPDATE`, jobID))
	if err != nil {
		return nil, err
	}
	switch job.Status {
	case "cancelled":
		var refunded float64
		err := tx.QueryRowContext(ctx, `SELECT amount FROM credit_ledger WHERE generation_job_id=$1::uuid AND transaction_type='refund'`, jobID).Scan(&refunded)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return &CancelResult{Outcome: CancelOutcomeCancelled, Job: job, Refunded: refunded}, nil
	case "done", "failed", "uncertain":
		return &CancelResult{Outcome: CancelOutcomeFinished, Job: job}, nil
	}
	var envelopeState string
	var attempts int
	hasEnvelope := true
	err = tx.QueryRowContext(ctx, `SELECT state,attempts FROM generation_dispatches WHERE job_id=$1::uuid FOR UPDATE`, jobID).Scan(&envelopeState, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		hasEnvelope = false
	} else if err != nil {
		return nil, err
	}
	// Only a pending job whose envelope no dispatcher has claimed can still be stopped. A claim
	// counts as an attempt, so any attempt means the request may already be with the provider.
	unsent := job.Status == "pending" && (!hasEnvelope || (envelopeState == "pending" && attempts == 0))
	if !unsent {
		return &CancelResult{Outcome: CancelOutcomeSettling, Job: job}, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE generation_jobs SET status='cancelled', error_message=$2::text,
        completed_at=COALESCE(completed_at,clock_timestamp()) WHERE id=$1::uuid`, jobID, CancelMessage); err != nil {
		return nil, err
	}
	if hasEnvelope {
		if _, err := tx.ExecContext(ctx, `UPDATE generation_dispatches SET state='cancelled', updated_at=NOW() WHERE job_id=$1::uuid`, jobID); err != nil {
			return nil, err
		}
	}
	var debit float64
	var refunded float64
	err = tx.QueryRowContext(ctx, `SELECT amount FROM credit_ledger WHERE generation_job_id=$1::uuid AND transaction_type='debit'`, jobID).Scan(&debit)
	if err == nil {
		if _, err := refundLocked(ctx, tx, userID, projectID, jobID, debit); err != nil {
			return nil, err
		}
		refunded = debit
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	cancelled, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM generation_jobs WHERE id=$1::uuid`, jobID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &CancelResult{Outcome: CancelOutcomeCancelled, Job: cancelled, Refunded: refunded}, nil
}
