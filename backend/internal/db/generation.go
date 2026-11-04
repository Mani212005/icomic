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

type GenerationOptions struct {
	EntityID         string
	KillSwitchActive bool
}

const jobColumns = `id, project_id, user_id, job_type, stage, model_id, status, idempotency_key, estimated_cost, actual_cost, hashed_user_id, error_message, created_at, completed_at`

func scanJob(row Row) (*domain.GenerationJob, error) {
	var j domain.GenerationJob
	err := row.Scan(&j.ID, &j.ProjectID, &j.UserID, &j.JobType, &j.Stage, &j.ModelID, &j.Status, &j.IdempotencyKey, &j.EstimatedCost, &j.ActualCost, &j.HashedUserID, &j.ErrorMessage, &j.CreatedAt, &j.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &j, err
}
func existingGeneration(ctx context.Context, tx Transaction, job *domain.GenerationJob) (*domain.GenerationJob, error) {
	existing, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM generation_jobs WHERE idempotency_key=$1`, job.IdempotencyKey))
	if err == nil && (existing.UserID != job.UserID || existing.ProjectID != job.ProjectID || existing.JobType != job.JobType) {
		return nil, ErrConflict
	}
	return existing, err
}
func imageJob(kind string) bool {
	return kind == "character_reference" || kind == "draft_sketch" || kind == "final_line_art"
}
func validAmount(amount float64) bool {
	return !math.IsNaN(amount) && !math.IsInf(amount, 0) && amount >= 0
}

// CreateGeneration commits the approval, policy, quota, job and debit boundary.
// Quota usage rows count image attempts, including failed/refunded/deleted jobs.
func (p *PostgresRepository) CreateGeneration(ctx context.Context, job *domain.GenerationJob, opts GenerationOptions, amount float64) (*domain.GenerationJob, bool, error) {
	if !validAmount(amount) {
		return nil, false, fmt.Errorf("invalid debit amount")
	}
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if existing, err := existingGeneration(ctx, tx, job); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	// Serialize policy changes and platform spend, then serialize each user's ledger.
	var spend float64
	var active bool
	var threshold float64
	var dailyLimit, monthlyLimit int
	if err := tx.QueryRowContext(ctx, `SELECT kill_switch_active, spend_threshold, max_daily_images, max_monthly_images FROM generation_policy WHERE singleton=TRUE FOR UPDATE`).Scan(&active, &threshold, &dailyLimit, &monthlyLimit); err != nil {
		return nil, false, err
	}
	var userID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, job.UserID).Scan(&userID); err != nil {
		return nil, false, err
	}
	// A concurrent request may have committed while we waited for the policy lock.
	if existing, err := existingGeneration(ctx, tx, job); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	if err := domain.CheckKillSwitchAndQuota(active || opts.KillSwitchActive, 0, 0, 0, 0); err != nil {
		return nil, false, err
	}
	if threshold > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE transaction_type WHEN 'debit' THEN amount WHEN 'refund' THEN -amount ELSE 0 END),0) FROM credit_ledger`).Scan(&spend); err != nil {
			return nil, false, err
		}
		if spend >= threshold {
			return nil, false, domain.ErrKillSwitchActive
		}
	}
	now := time.Now().UTC()
	var daily, monthly int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE created_at >= $2), COUNT(*)
        FROM image_quota_usage
        WHERE user_id=$1 AND created_at >= $3`, job.UserID, now.Add(-24*time.Hour), now.Add(-30*24*time.Hour)).Scan(&daily, &monthly); err != nil {
		return nil, false, err
	}
	if err := domain.CheckKillSwitchAndQuota(false, float64(daily), float64(dailyLimit), float64(monthly), float64(monthlyLimit)); err != nil {
		return nil, false, err
	}
	if err := checkGenerationApproval(ctx, tx, job, opts.EntityID); err != nil {
		return nil, false, err
	}
	if opts.EntityID != "" {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM generation_jobs WHERE project_id=$1 AND entity_id=$2 AND job_type=$3 AND status IN ('pending','running'))`, job.ProjectID, opts.EntityID, job.JobType).Scan(&exists); err != nil {
			return nil, false, err
		}
		if exists {
			return nil, false, domain.ErrActiveJobExists
		}
	}
	balance, err := transactionBalance(ctx, tx, job.UserID)
	if err != nil {
		return nil, false, err
	}
	if balance < amount {
		return nil, false, ErrInsufficientCredit
	}
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	job.CreatedAt = now
	job.Status = "pending"
	var inserted string
	err = tx.QueryRowContext(ctx, `INSERT INTO generation_jobs (id,project_id,user_id,job_type,stage,model_id,status,idempotency_key,estimated_cost,hashed_user_id,created_at,entity_id)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid)
        ON CONFLICT (idempotency_key) DO NOTHING
        RETURNING id`, job.ID, job.ProjectID, job.UserID, job.JobType, job.Stage, job.ModelID, job.Status, job.IdempotencyKey, job.EstimatedCost, job.HashedUserID, now, opts.EntityID).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		existing, err := existingGeneration(ctx, tx, job)
		return existing, true, err
	}
	if err != nil {
		return nil, false, err
	}
	if amount > 0 || imageJob(job.JobType) {
		if _, err := insertCredit(ctx, tx, job.UserID, job.ProjectID, job.ID, "debit", amount, balance-amount); err != nil {
			return nil, false, err
		}
	}
	if imageJob(job.JobType) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO image_quota_usage (user_id,generation_job_id,created_at) VALUES ($1,$2,$3)`, job.UserID, job.ID, now); err != nil {
			return nil, false, err
		}
	}

	if threshold > 0 && spend+amount >= threshold {
		if _, err := tx.ExecContext(ctx, `UPDATE generation_policy SET kill_switch_active=TRUE WHERE singleton=TRUE`); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, false, nil
}

func checkGenerationApproval(ctx context.Context, tx Transaction, job *domain.GenerationJob, entityID string) error {
	var script string
	if err := tx.QueryRowContext(ctx, `SELECT original_script FROM projects WHERE id=$1 AND user_id=$2 FOR UPDATE`, job.ProjectID, job.UserID).Scan(&script); err != nil {
		return err
	}
	prereqs := domain.ApprovedPrereqs{HasScript: len(script) > 0}
	if !imageJob(job.JobType) && job.JobType != "colour_suggestion" {
		return nil
	}
	if entityID == "" {
		return fmt.Errorf("%w: generation entity is required", domain.ErrMissingPrerequisiteApproval)
	}
	if job.JobType == "character_reference" {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM characters WHERE id=$1 AND project_id=$2 FOR SHARE`, entityID, job.ProjectID).Scan(&id); err != nil {
			return err
		}
		_, err := domain.ValidateScript(script)
		if err != nil {
			return fmt.Errorf("%w: script must be submitted and validated", domain.ErrMissingPrerequisiteApproval)
		}
		return domain.CheckApprovalGate(job.JobType, prereqs)
	}
	var chapterID string
	var pageUpdated time.Time
	if err := tx.QueryRowContext(ctx, `SELECT p.chapter_id,p.updated_at FROM pages p JOIN chapters c ON c.id=p.chapter_id WHERE p.id=$1 AND c.project_id=$2 FOR SHARE OF p,c`, entityID, job.ProjectID).Scan(&chapterID, &pageUpdated); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM panels WHERE id::text IN (SELECT jsonb_array_elements_text(panel_ids) FROM pages WHERE id=$1) FOR SHARE`, entityID); err != nil {
		return err
	}
	var panelUpdated sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT MAX(updated_at) FROM panels WHERE id::text IN (SELECT jsonb_array_elements_text(panel_ids) FROM pages WHERE id=$1)`, entityID).Scan(&panelUpdated); err != nil {
		return err
	}
	if panelUpdated.Valid && panelUpdated.Time.After(pageUpdated) {
		pageUpdated = panelUpdated.Time
	}

	if job.JobType == "draft_sketch" {
		// Hold the prerequisite rows against edits through commit. Approvals are append-only.
		if _, err := tx.ExecContext(ctx, `SELECT id FROM characters WHERE project_id=$1 FOR SHARE`, job.ProjectID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)>0
        AND COALESCE(BOOL_AND(EXISTS (SELECT 1
        FROM stage_approvals a
        WHERE a.project_id=c.project_id
        AND a.entity_type='character_reference'
        AND a.entity_id=c.id
        AND NOT EXISTS (SELECT 1 FROM generation_jobs active
            WHERE active.project_id=c.project_id AND active.entity_id=c.id
            AND active.job_type='character_reference' AND active.status IN ('pending','running'))
        AND a.approved_at>GREATEST(c.updated_at,COALESCE((SELECT MAX(j.completed_at)
        FROM generation_jobs j
        WHERE j.project_id=c.project_id
        AND j.job_type='character_reference'
        AND j.status='done'
        AND (j.entity_id=c.id OR j.id=c.reference_image_job_id)),c.updated_at)))),FALSE)
        FROM characters c
        WHERE c.project_id=$1`, job.ProjectID).Scan(&prereqs.AllCharactersApproved); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1
        FROM stage_approvals a JOIN chapters c ON c.id=a.entity_id
        WHERE c.id=$1
        AND a.project_id=c.project_id
        AND a.entity_type='chapter_outline'
        AND NOT EXISTS (SELECT 1 FROM generation_jobs active
            WHERE active.project_id=c.project_id AND active.job_type='chapter_outline'
            AND active.status IN ('pending','running'))
        AND a.approved_at>GREATEST(c.updated_at,COALESCE((SELECT MAX(j.completed_at)
        FROM generation_jobs j
        WHERE j.project_id=c.project_id
        AND j.job_type='chapter_outline'
        AND j.status='done'),c.updated_at)))`, chapterID).Scan(&prereqs.ChapterOutlineApproved); err != nil {
			return err
		}
	} else {
		approvalType, previousJob := "draft_page", "draft_sketch"
		if job.JobType == "colour_suggestion" {
			approvalType, previousJob = "line_art_page", "final_line_art"
		}
		var approved bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1
        FROM stage_approvals a
        WHERE a.project_id=$1
        AND a.entity_id=$2
        AND a.entity_type=$3
        AND NOT EXISTS (SELECT 1 FROM generation_jobs active
            WHERE active.project_id=$1 AND active.entity_id=$2
            AND active.job_type=$5 AND active.status IN ('pending','running'))
        AND a.approved_at>GREATEST($4::timestamptz,COALESCE((SELECT MAX(j.completed_at)
        FROM generation_jobs j
        WHERE j.project_id=$1
        AND j.entity_id=$2
        AND j.job_type=$5
        AND j.status='done'),$4::timestamptz)))`, job.ProjectID, entityID, approvalType, pageUpdated, previousJob).Scan(&approved); err != nil {
			return err
		}
		prereqs.DraftSketchApproved = approved
		prereqs.LineArtApproved = approved
	}
	return domain.CheckApprovalGate(job.JobType, prereqs)
}

func (p *PostgresRepository) TransitionGeneration(ctx context.Context, id, status string, cost *float64, msg *string, allowed []string) error {
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current string
	err = tx.QueryRowContext(ctx, `SELECT status FROM generation_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if len(allowed) > 0 {
		found := false
		for _, s := range allowed {
			if s == current {
				found = true
			}
		}
		if !found {
			return ErrConflict
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE generation_jobs
        SET status=$2, actual_cost=COALESCE($3,actual_cost),error_message=COALESCE($4,error_message),completed_at=CASE WHEN $2 IN ('done','failed','cancelled') THEN COALESCE(completed_at,clock_timestamp()) ELSE completed_at END
        WHERE id=$1`, id, status, cost, msg)
	if err != nil {
		return err
	}
	return tx.Commit()
}
