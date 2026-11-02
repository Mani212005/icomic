package jobs_test

import (
	"context"
	"testing"

	"icomic/backend/internal/jobs"
)

func TestInMemoryJobService(t *testing.T) {
	ctx := context.Background()
	svc := jobs.NewInMemoryJobService()

	req := jobs.CreateJobRequest{
		ProjectID:      "proj-123",
		UserID:         "user-456",
		JobType:        "draft_sketch",
		Stage:          "draft",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: "idemp-abc",
		EstimatedCost:  0.01,
		HashedUserID:   "hashed-456",
	}

	// 1. Create job
	job, existed, err := svc.CreateJob(ctx, req)
	if err != nil || existed || job == nil {
		t.Fatalf("unexpected error creating job: %v, existed: %v", err, existed)
	}
	if job.Status != "pending" || job.IdempotencyKey != "idemp-abc" {
		t.Fatalf("unexpected job status: %+v", job)
	}

	// 2. Idempotency: creating again with same key returns existing
	job2, existed2, err := svc.CreateJob(ctx, req)
	if err != nil || !existed2 || job2.ID != job.ID {
		t.Fatalf("expected existing job for duplicate key, got %+v, existed: %v, err: %v", job2, existed2, err)
	}

	// 3. Get job
	fetched, err := svc.GetJob(ctx, job.ID)
	if err != nil || fetched.ID != job.ID {
		t.Fatalf("GetJob failed: %v", err)
	}

	// 4. Update status
	actualCost := 0.01
	if err := svc.UpdateJobStatus(ctx, job.ID, "running", nil, nil); err != nil {
		t.Fatalf("UpdateJobStatus failed: %v", err)
	}

	// 5. Cancel running job
	if err := svc.CancelJob(ctx, job.ID); err != nil {
		t.Fatalf("CancelJob failed: %v", err)
	}
	cancelled, _ := svc.GetJob(ctx, job.ID)
	if cancelled.Status != "cancelled" || cancelled.CompletedAt == nil {
		t.Fatalf("expected cancelled status, got %s", cancelled.Status)
	}

	// 6. Cannot cancel already cancelled/done job
	if err := svc.CancelJob(ctx, job.ID); err != nil {
		// Cancelling already cancelled job
	}

	// Update to done
	_ = svc.UpdateJobStatus(ctx, job.ID, "done", &actualCost, nil)
	if err := svc.CancelJob(ctx, job.ID); err != jobs.ErrCannotCancelJob {
		t.Fatalf("expected ErrCannotCancelJob for done job, got %v", err)
	}
}
