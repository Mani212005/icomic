package jobs_test

import (
	"context"
	"testing"

	"icomic/backend/internal/jobs"
)

func TestJobServiceIdempotency(t *testing.T) {
	ctx := context.Background()
	svc := jobs.NewInMemoryJobService()

	req := jobs.CreateJobRequest{
		ProjectID:      "proj-1",
		UserID:         "user-1",
		JobType:        "draft_sketch",
		Stage:          "draft",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: "idemp-key-12345",
		EstimatedCost:  0.01,
		HashedUserID:   "hash-user-1",
	}

	job1, existed1, err := svc.CreateJob(ctx, req)
	if err != nil || existed1 {
		t.Fatalf("first creation failed: err=%v, existed=%v", err, existed1)
	}

	// Re-attempting creation with the same idempotency key
	job2, existed2, err := svc.CreateJob(ctx, req)
	if err != nil || !existed2 {
		t.Fatalf("second creation failed: err=%v, existed=%v", err, existed2)
	}

	if job1.ID != job2.ID {
		t.Fatalf("expected same job ID for idempotent retry, got %s and %s", job1.ID, job2.ID)
	}

	// Cancel job
	if err := svc.CancelJob(ctx, job1.ID); err != nil {
		t.Fatalf("unexpected cancel error: %v", err)
	}

	jobFetched, err := svc.GetJob(ctx, job1.ID)
	if err != nil || jobFetched.Status != "cancelled" {
		t.Fatalf("expected cancelled status, got %s, err %v", jobFetched.Status, err)
	}
}
