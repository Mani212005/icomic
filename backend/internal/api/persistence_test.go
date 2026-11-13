package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icomic/backend/gen"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

// Exercise the user-visible failure: an unaffordable generation must not leave
// an orphan job behind, even when the user retries the request.
func TestGenerationDebitFailureLeavesNoJob(t *testing.T) {
	s, handler, repo := setupTestServer(true, "test-user")
	_, err := s.CreditService.Debit(context.Background(), credits.DebitRequest{UserID: "test-user", Amount: 50})
	if err != nil {
		t.Fatal(err)
	}
	project := &domain.Project{UserID: "test-user", OriginalScript: "A hero saves the town.", Title: "Comic"}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	character := &domain.Character{ProjectID: project.ID, Name: "Hero"}
	if err := repo.CreateCharacter(context.Background(), character); err != nil {
		t.Fatal(err)
	}
	observer := &observingJobs{JobService: s.JobService}
	s.JobService = observer
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+character.ID+"/reference-image", strings.NewReader(`{}`)))
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if observer.createdID != "" {
		t.Fatalf("debit failure left job %s", observer.createdID)
	}
}

type observingJobs struct {
	jobs.JobService
	createdID string
}

func (s *observingJobs) CreateJob(ctx context.Context, req jobs.CreateJobRequest) (*domain.GenerationJob, bool, error) {
	job, existed, err := s.JobService.CreateJob(ctx, req)
	if err == nil {
		s.createdID = job.ID
	}
	return job, existed, err
}

// A failed commit must return before calling the image adapter.
func TestGenerationTransactionFailureDoesNotDispatch(t *testing.T) {
	s, handler, repo := setupTestServer(true, "test-user")
	project := &domain.Project{UserID: "test-user", OriginalScript: "A hero saves the town.", Title: "Comic"}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	character := &domain.Character{ProjectID: project.ID, Name: "Hero"}
	if err := repo.CreateCharacter(context.Background(), character); err != nil {
		t.Fatal(err)
	}
	s.JobService = &failingCreation{JobService: s.JobService}
	ai := &generationObserver{AIService: s.AIClient}
	s.AIClient = ai
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+character.ID+"/reference-image", strings.NewReader(`{}`)))
	if rec.Code != http.StatusInternalServerError || ai.calls != 0 {
		t.Fatalf("status=%d image calls=%d", rec.Code, ai.calls)
	}
	history, err := s.CreditService.GetUsageHistory(context.Background(), "test-user")
	if err != nil || len(history) != 0 {
		t.Fatalf("ledger changed after failed transaction: %v %v", history, err)
	}
}

type failingCreation struct{ jobs.JobService }

func (s *failingCreation) CreateJob(context.Context, jobs.CreateJobRequest) (*domain.GenerationJob, bool, error) {
	return nil, false, errors.New("commit failed")
}

type generationObserver struct {
	gen.AIService
	calls int
}

func (s *generationObserver) GenerateCharacterReference(ctx context.Context, req gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
	s.calls++
	return s.AIService.GenerateCharacterReference(ctx, req)
}

// Model queue retry delivery by presenting the same creation key twice through
// the HTTP handler. Neither a second image call nor a second debit is allowed.
func TestGenerationReplayReturnsExistingImageWithoutDispatch(t *testing.T) {
	s, handler, repo := setupTestServer(true, "test-user")
	project := &domain.Project{UserID: "test-user", OriginalScript: "A hero saves the town.", Title: "Comic"}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	character := &domain.Character{ProjectID: project.ID, Name: "Hero"}
	if err := repo.CreateCharacter(context.Background(), character); err != nil {
		t.Fatal(err)
	}
	s.JobService = &fixedKeyCreation{JobService: s.JobService}
	ai := &generationObserver{AIService: s.AIClient}
	s.AIClient = ai
	for attempt := 0; attempt < 2; attempt++ {
		rec := serveGeneration(s, handler, httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+character.ID+"/reference-image", strings.NewReader(`{}`)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: %d %s", attempt, rec.Code, rec.Body.String())
		}
	}
	history, err := s.CreditService.GetUsageHistory(context.Background(), "test-user")
	if err != nil || len(history) != 1 || ai.calls != 1 {
		t.Fatalf("history=%v calls=%d err=%v", history, ai.calls, err)
	}
}

type fixedKeyCreation struct{ jobs.JobService }

func (s *fixedKeyCreation) CreateJob(ctx context.Context, req jobs.CreateJobRequest) (*domain.GenerationJob, bool, error) {
	req.IdempotencyKey = "same-retried-key"
	return s.JobService.CreateJob(ctx, req)
}
