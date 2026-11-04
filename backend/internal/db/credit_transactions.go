package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/domain"
)

// Sum the ledger under the stable user-row lock. Locking the latest ledger row
// alone is unsafe: another transaction can append a newer row while we wait.
func transactionBalance(ctx context.Context, tx Transaction, userID string) (float64, error) {
	var balance float64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE transaction_type WHEN 'debit' THEN -amount ELSE amount END),0) FROM credit_ledger WHERE user_id=$1`, userID).Scan(&balance)
	return balance, err
}
func insertCredit(ctx context.Context, tx Transaction, userID, projectID, jobID, kind string, amount, balance float64) (*domain.CreditLedger, error) {
	entry := &domain.CreditLedger{ID: uuid.NewString(), UserID: userID, ProjectID: projectID, TransactionType: kind, Amount: amount, BalanceAfter: math.Round(balance*10000) / 10000, CreatedAt: time.Now().UTC()}
	if jobID != "" {
		entry.GenerationJobID = &jobID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO credit_ledger (id,user_id,project_id,generation_job_id,transaction_type,amount,balance_after,created_at)
        VALUES ($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8)`, entry.ID, userID, projectID, jobID, kind, amount, entry.BalanceAfter, entry.CreatedAt)
	return entry, err
}

// RefundGeneration is a separate transaction and refunds a job at most once.
// User and project are derived from the job; the amount is bounded by its debit.
func (p *PostgresRepository) RefundGeneration(ctx context.Context, jobID string, amount float64) (*domain.CreditLedger, error) {
	if !validAmount(amount) {
		return nil, fmt.Errorf("invalid refund amount")
	}
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Match creation's lock order so refunds cannot race the global spend check.
	var singleton bool
	if err := tx.QueryRowContext(ctx, `SELECT singleton FROM generation_policy WHERE singleton=TRUE FOR UPDATE`).Scan(&singleton); err != nil {
		return nil, err
	}
	var userID, projectID string
	err = tx.QueryRowContext(ctx, `SELECT user_id,project_id FROM generation_jobs WHERE id=$1`, jobID).Scan(&userID, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		return nil, err
	}
	var debit float64
	if err := tx.QueryRowContext(ctx, `SELECT amount FROM credit_ledger WHERE generation_job_id=$1 AND transaction_type='debit'`, jobID).Scan(&debit); err != nil {
		return nil, err
	}
	if amount > debit {
		return nil, fmt.Errorf("refund exceeds the job debit")
	}
	existing, err := scanCredit(tx.QueryRowContext(ctx, `SELECT id,user_id,project_id,generation_job_id,transaction_type,amount,balance_after,created_at
        FROM credit_ledger
        WHERE generation_job_id=$1
        AND transaction_type='refund'`, jobID))
	if err == nil {
		if existing.Amount != amount {
			return nil, fmt.Errorf("refund amount differs from the committed refund")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	balance, err := transactionBalance(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	entry, err := insertCredit(ctx, tx, userID, projectID, jobID, "refund", amount, balance+amount)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return entry, nil
}
func scanCredit(row Row) (*domain.CreditLedger, error) {
	var entry domain.CreditLedger
	var projectID, jobID sql.NullString
	err := row.Scan(&entry.ID, &entry.UserID, &projectID, &jobID, &entry.TransactionType, &entry.Amount, &entry.BalanceAfter, &entry.CreatedAt)
	entry.ProjectID = projectID.String
	if jobID.Valid {
		entry.GenerationJobID = &jobID.String
	}
	return &entry, err
}

// GenerationDebit returns the debit already committed with job creation. It
// deliberately cannot add a later standalone debit to an existing generation.
func (p *PostgresRepository) GenerationDebit(ctx context.Context, jobID string) (*domain.CreditLedger, error) {
	return scanCredit(p.db.QueryRowContext(ctx, `SELECT id,user_id,project_id,generation_job_id,transaction_type,amount,balance_after,created_at
        FROM credit_ledger
        WHERE generation_job_id=$1
        AND transaction_type='debit'`, jobID))
}
