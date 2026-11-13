package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
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
	if job.Status != jobs.StatusPending || job.IdempotencyKey != "idemp-abc" {
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

	// 5. Cancelling a running job is not a cancellation: it is settling and nothing changes
	result, err := svc.CancelJob(ctx, job.ID)
	if err != nil || result.Outcome != db.CancelOutcomeSettling || result.Refunded != 0 {
		t.Fatalf("expected settling for a running job, got %+v %v", result, err)
	}
	running, _ := svc.GetJob(ctx, job.ID)
	if running.Status != jobs.StatusRunning || running.CompletedAt != nil {
		t.Fatalf("a settling job must keep running, got %+v", running)
	}

	// 6. The job finishes; a late cancel reports it finished and a terminal status is never reversed
	if err := svc.UpdateJobStatus(ctx, job.ID, jobs.StatusDone, &actualCost, nil); err != nil {
		t.Fatalf("finish running job: %v", err)
	}
	result, err = svc.CancelJob(ctx, job.ID)
	if err != nil || result.Outcome != db.CancelOutcomeFinished || result.Job.Status != jobs.StatusDone {
		t.Fatalf("expected finished for a done job, got %+v %v", result, err)
	}
	if err := svc.UpdateJobStatus(ctx, job.ID, jobs.StatusFailed, nil, nil); !errors.Is(err, jobs.ErrJobAlreadyTerminal) {
		t.Fatalf("a done job must not become failed, got %v", err)
	}
}

func TestAsyncJobLifecycleTransitions(t *testing.T) {
	ctx := context.Background()
	svc := jobs.NewInMemoryJobService()

	// 1. Pending -> Running -> Done
	req1 := jobs.CreateJobRequest{
		ProjectID:      "proj-1",
		UserID:         "user-1",
		JobType:        "character_reference",
		Stage:          "character",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: "idemp-1",
		EstimatedCost:  0.01,
	}
	j1, existed, err := svc.CreateJob(ctx, req1)
	if err != nil || existed {
		t.Fatalf("failed creating job 1: %v", err)
	}
	if j1.Status != jobs.StatusPending {
		t.Fatalf("expected status pending, got %s", j1.Status)
	}

	if err := svc.StartJob(ctx, j1.ID); err != nil {
		t.Fatalf("StartJob failed: %v", err)
	}
	runningJob, _ := svc.GetJob(ctx, j1.ID)
	if runningJob.Status != jobs.StatusRunning {
		t.Fatalf("expected running status, got %s", runningJob.Status)
	}

	cost := 0.01
	if err := svc.CompleteJob(ctx, j1.ID, cost); err != nil {
		t.Fatalf("CompleteJob failed: %v", err)
	}
	doneJob, _ := svc.GetJob(ctx, j1.ID)
	if doneJob.Status != jobs.StatusDone || doneJob.ActualCost == nil || *doneJob.ActualCost != 0.01 || doneJob.CompletedAt == nil {
		t.Fatalf("invalid done job state: %+v", doneJob)
	}

	// Cannot re-complete or fail terminal job
	if err := svc.CompleteJob(ctx, j1.ID, 0.02); err == nil {
		t.Fatalf("expected error completing already done job")
	}
	if err := svc.FailJob(ctx, j1.ID, "fail", nil); err == nil {
		t.Fatalf("expected error failing already done job")
	}

	// 2. Pending -> Running -> Failed
	req2 := jobs.CreateJobRequest{
		ProjectID:      "proj-2",
		UserID:         "user-2",
		JobType:        "line_art",
		Stage:          "line_art",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: "idemp-2",
		EstimatedCost:  0.01,
	}
	j2, _, _ := svc.CreateJob(ctx, req2)
	_ = svc.StartJob(ctx, j2.ID)
	failCost := 0.005
	if err := svc.FailJob(ctx, j2.ID, "GPU timeout", &failCost); err != nil {
		t.Fatalf("FailJob failed: %v", err)
	}
	failedJob, _ := svc.GetJob(ctx, j2.ID)
	if failedJob.Status != jobs.StatusFailed || failedJob.ErrorMessage == nil || *failedJob.ErrorMessage != "GPU timeout" {
		t.Fatalf("invalid failed job state: %+v", failedJob)
	}
	if failedJob.ActualCost == nil || *failedJob.ActualCost != 0.005 || failedJob.CompletedAt == nil {
		t.Fatalf("expected cost and completion timestamp on failed job: %+v", failedJob)
	}

	// 3. Pending -> Cancelled directly
	req3 := jobs.CreateJobRequest{
		ProjectID:      "proj-3",
		UserID:         "user-3",
		JobType:        "draft_sketch",
		Stage:          "draft",
		IdempotencyKey: "idemp-3",
	}
	j3, _, _ := svc.CreateJob(ctx, req3)
	if result, err := svc.CancelJob(ctx, j3.ID); err != nil || result.Outcome != db.CancelOutcomeCancelled {
		t.Fatalf("CancelJob on pending failed: %+v %v", result, err)
	}
	cancJob, _ := svc.GetJob(ctx, j3.ID)
	if cancJob.Status != jobs.StatusCancelled || cancJob.CompletedAt == nil {
		t.Fatalf("expected cancelled status, got %+v", cancJob)
	}

	// 4. Invalid status in UpdateJobStatus
	if err := svc.UpdateJobStatus(ctx, j1.ID, "invalid_status", nil, nil); !errors.Is(err, jobs.ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestUncertainJobIsTerminalAndRetainsItsReservation(t *testing.T) {
	ctx := context.Background()
	svc := jobs.NewInMemoryJobService()
	job, _, err := svc.CreateJob(ctx, jobs.CreateJobRequest{
		ProjectID: "project", UserID: "user", JobType: "draft_sketch", Stage: "draft",
		ModelID: "muse-image-1.0", IdempotencyKey: "uncertain-attempt", EstimatedCost: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := "provider request outcome is unknown"
	if err := svc.UpdateJobStatus(ctx, job.ID, jobs.StatusUncertain, nil, &message); err != nil {
		t.Fatal(err)
	}
	saved, err := svc.GetJob(ctx, job.ID)
	if err != nil || saved.Status != jobs.StatusUncertain || saved.ActualCost != nil {
		t.Fatalf("uncertain attempt lost its state or reservation: %+v, %v", saved, err)
	}
	if err := svc.StartJob(ctx, job.ID); !errors.Is(err, jobs.ErrJobAlreadyTerminal) {
		t.Fatalf("uncertain attempt was restartable: %v", err)
	}
}

func TestDispatchAsync(t *testing.T) {
	ctx := context.Background()
	svc := jobs.NewInMemoryJobService()

	// 1. Success dispatch
	req1 := jobs.CreateJobRequest{
		ProjectID:      "proj-async-1",
		UserID:         "user-async-1",
		JobType:        "panel_text",
		Stage:          "panel",
		IdempotencyKey: "async-key-1",
		EstimatedCost:  0.0,
	}
	j1, _, err := svc.CreateJob(ctx, req1)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	err = svc.DispatchAsync(ctx, j1.ID, func(taskCtx context.Context) (float64, error) {
		defer wg.Done()
		return 0.015, nil
	})
	if err != nil {
		t.Fatalf("DispatchAsync failed: %v", err)
	}
	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	res1, _ := svc.GetJob(ctx, j1.ID)
	if res1.Status != jobs.StatusDone || res1.ActualCost == nil || *res1.ActualCost != 0.015 {
		t.Fatalf("unexpected async success state: %+v", res1)
	}

	// 2. Failure dispatch
	req2 := jobs.CreateJobRequest{
		ProjectID:      "proj-async-2",
		UserID:         "user-async-2",
		JobType:        "assemble_pdf",
		Stage:          "export",
		IdempotencyKey: "async-key-2",
	}
	j2, _, _ := svc.CreateJob(ctx, req2)
	wg.Add(1)
	err = svc.DispatchAsync(ctx, j2.ID, func(taskCtx context.Context) (float64, error) {
		defer wg.Done()
		return 0.0, errors.New("font rendering failure")
	})
	if err != nil {
		t.Fatalf("DispatchAsync failed: %v", err)
	}
	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	res2, _ := svc.GetJob(ctx, j2.ID)
	if res2.Status != jobs.StatusFailed || res2.ErrorMessage == nil || *res2.ErrorMessage != "font rendering failure" {
		t.Fatalf("unexpected async failure state: %+v", res2)
	}

	// 3. Cancel while in flight
	req3 := jobs.CreateJobRequest{
		ProjectID:      "proj-async-3",
		UserID:         "user-async-3",
		JobType:        "character_reference",
		Stage:          "character",
		IdempotencyKey: "async-key-3",
	}
	j3, _, _ := svc.CreateJob(ctx, req3)
	taskStarted := make(chan struct{})
	taskBlocked := make(chan struct{})

	err = svc.DispatchAsync(ctx, j3.ID, func(taskCtx context.Context) (float64, error) {
		close(taskStarted)
		select {
		case <-taskCtx.Done():
			return 0.0, taskCtx.Err()
		case <-taskBlocked:
			return 0.02, nil
		}
	})
	if err != nil {
		t.Fatalf("DispatchAsync failed: %v", err)
	}

	<-taskStarted
	// The task is already with the provider: cancelling reports settling and stops nothing.
	result, err := svc.CancelJob(ctx, j3.ID)
	if err != nil || result.Outcome != db.CancelOutcomeSettling {
		t.Fatalf("expected settling for an in-flight job, got %+v %v", result, err)
	}
	close(taskBlocked)
	deadline := time.Now().Add(2 * time.Second)
	var res3 *domain.GenerationJob
	for time.Now().Before(deadline) {
		res3, _ = svc.GetJob(ctx, j3.ID)
		if res3.Status != jobs.StatusRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if res3.Status != jobs.StatusDone || res3.ActualCost == nil || *res3.ActualCost != 0.02 {
		t.Fatalf("a settling job must finish with its result and cost, got %+v", res3)
	}
}
