package jobs_test

import (
	"context"
	"errors"
	"testing"

	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

type durableJobStore struct {
	db.Repository
	entity string
	amount float64
}

func (s *durableJobStore) CreateGeneration(ctx context.Context, job *domain.GenerationJob, opts db.GenerationOptions, amount float64) (*domain.GenerationJob, bool, error) {
	if existing, err := s.GetJobByIdempotencyKey(ctx, job.IdempotencyKey); err == nil {
		return existing, true, nil
	}
	s.entity = opts.EntityID
	s.amount = amount
	if err := s.CreateJobWithDebit(ctx, job, amount); err != nil {
		return nil, false, err
	}
	return job, false, nil
}
func (s *durableJobStore) TransitionGeneration(ctx context.Context, id, status string, cost *float64, msg *string, allowed []string) error {
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if len(allowed) > 0 {
		found := false
		for _, s := range allowed {
			if s == job.Status {
				found = true
			}
		}
		if !found {
			return db.ErrConflict
		}
	}
	job.Status = status
	job.ActualCost = cost
	job.ErrorMessage = msg
	return s.UpdateJob(ctx, job)
}
func TestPersistentJobsSurviveServiceRecreationWithoutSecondCharge(t *testing.T) {
	ctx := context.Background()
	repo := db.NewInMemoryRepository()
	user := &domain.User{Email: "creator@example.com"}
	if err := repo.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	store := &durableJobStore{Repository: repo}
	req := jobs.CreateJobRequest{ProjectID: "project", UserID: user.ID, EntityID: "character", JobType: "character_reference", Stage: "reference", IdempotencyKey: "key", EstimatedCost: credits.ImageUnitPrice}
	first := jobs.NewPersistentJobService(store)
	job, existed, err := first.CreateJob(ctx, req)
	if err != nil || existed {
		t.Fatalf("create: %v %v", existed, err)
	}
	if store.entity != "character" || store.amount != credits.ImageUnitPrice {
		t.Fatal("entity or backend debit amount was lost")
	}
	if err := first.StartJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	second := jobs.NewPersistentJobService(store)
	stored, err := second.GetJob(ctx, job.ID)
	if err != nil || stored.Status != jobs.StatusRunning {
		t.Fatalf("restarted service: %v %v", stored, err)
	}
	replay, existed, err := second.CreateJob(ctx, req)
	if err != nil || !existed || replay.ID != job.ID {
		t.Fatalf("replay: %v %v %v", replay, existed, err)
	}
	ledger, err := repo.ListCreditLedger(ctx, user.ID)
	if err != nil || len(ledger) != 1 || ledger[0].Amount != credits.ImageUnitPrice {
		t.Fatalf("unexpected ledger: %v %v", ledger, err)
	}
	if err := second.CompleteJob(ctx, job.ID, credits.ImageUnitPrice); err != nil {
		t.Fatal(err)
	}
	if err := second.CancelJob(ctx, job.ID); !errors.Is(err, jobs.ErrCannotCancelJob) {
		t.Fatalf("terminal cancellation: %v", err)
	}
	if err := second.StartJob(ctx, "missing"); !errors.Is(err, jobs.ErrJobNotFound) {
		t.Fatalf("missing job: %v", err)
	}
	if err := second.UpdateJobStatus(ctx, job.ID, "invalid", nil, nil); !errors.Is(err, jobs.ErrInvalidStatus) {
		t.Fatalf("invalid status: %v", err)
	}
	called := false
	if err := second.DispatchAsync(ctx, job.ID, func(context.Context) (float64, error) { called = true; return 0, nil }); err == nil || called {
		t.Fatal("unconfigured dispatcher started work")
	}
}

func TestPersistentJobsUseConfiguredDebitAmount(t *testing.T) {
	ctx := context.Background()
	repo := db.NewInMemoryRepository()
	user := &domain.User{Email: "priced@example.com"}
	if err := repo.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	store := &durableJobStore{Repository: repo}
	service := jobs.NewPersistentJobService(store)
	job, _, err := service.CreateJob(ctx, jobs.CreateJobRequest{
		ProjectID: "project", UserID: user.ID, EntityID: "page", JobType: "draft_sketch",
		Stage: "draft", IdempotencyKey: "configured-price", ModelID: "muse-image-configured",
		DebitAmount: 0.0275, EstimatedCost: 0.0275,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.amount != 0.0275 || job.EstimatedCost != 0.0275 || job.ModelID != "muse-image-configured" {
		t.Fatalf("configured pricing did not reach durable job/debit: amount=%v job=%+v", store.amount, job)
	}
}

func TestPersistentJobStorageEnumsPreservePublicLabels(t *testing.T) {
	for _, test := range []struct{ kind, stage, storedKind, storedStage string }{
		{"script_analysis", "script", "script_analysis", "text"},
		{"chapter_outline", "outline", "chapter_outline", "text"},
		{"panel_text", "panels", "panel_text", "text"},
		{"assemble_pdf", "export", "pdf_assembly", "export"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			ctx := context.Background()
			store := &durableJobStore{Repository: db.NewInMemoryRepository()}
			service := jobs.NewPersistentJobService(store)
			job, _, err := service.CreateJob(ctx, jobs.CreateJobRequest{JobType: test.kind, Stage: test.stage, IdempotencyKey: "key"})
			if err != nil {
				t.Fatal(err)
			}
			stored, err := store.GetJob(ctx, job.ID)
			if err != nil || stored.JobType != test.storedKind || stored.Stage != test.storedStage {
				t.Fatalf("stored=%v err=%v", stored, err)
			}
			read, err := jobs.NewPersistentJobService(store).GetJob(ctx, job.ID)
			if err != nil || job.JobType != test.kind || job.Stage != test.stage || read.JobType != test.kind || read.Stage != test.stage {
				t.Fatalf("public labels changed: create=%v read=%v err=%v", job, read, err)
			}
		})
	}
}

func TestPersistentDispatchRunsOnlyCommittedJobsAndLeavesCompletionToHandler(t *testing.T) {
	ctx := context.Background()
	store := &durableJobStore{Repository: db.NewInMemoryRepository()}
	service := jobs.NewPersistentJobService(store)
	job, _, err := service.CreateJob(ctx, jobs.CreateJobRequest{JobType: "script_analysis", Stage: "script", IdempotencyKey: "dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = service.DispatchAsync(ctx, job.ID, func(ctx context.Context) (float64, error) {
		saved, err := service.GetJob(ctx, job.ID)
		if err != nil || saved.Status != jobs.StatusRunning {
			t.Fatalf("dispatch before running state: %v %v", saved, err)
		}
		calls++
		return 0, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("dispatch: calls=%d err=%v", calls, err)
	}
	saved, err := jobs.NewPersistentJobService(store).GetJob(ctx, job.ID)
	if err != nil || saved.Status != jobs.StatusRunning {
		t.Fatalf("job completed before entity persistence: %v %v", saved, err)
	}
	failure := errors.New("queue worker rejected request")
	if err := service.DispatchAsync(ctx, job.ID, func(context.Context) (float64, error) { return 0, failure }); !errors.Is(err, failure) {
		t.Fatalf("failure: %v", err)
	}
	saved, err = jobs.NewPersistentJobService(store).GetJob(ctx, job.ID)
	if err != nil || saved.Status != jobs.StatusFailed || saved.ErrorMessage == nil || *saved.ErrorMessage != failure.Error() {
		t.Fatalf("failed status lost: %v %v", saved, err)
	}
}
