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

type committedDispatcher struct {
	jobs.JobService
	ledger     credits.CreditService
	dispatched int
}

// StartJob is the first step of every delivery, so it proves the debit committed before the
// provider is called.
func (d *committedDispatcher) StartJob(ctx context.Context, id string) error {
	job, err := d.GetJob(ctx, id)
	if err != nil {
		return err
	}
	entries, err := d.ledger.GetUsageHistory(ctx, job.UserID)
	if err != nil {
		return err
	}
	committed := false
	for _, entry := range entries {
		if entry.GenerationJobID != nil && *entry.GenerationJobID == id && entry.TransactionType == "debit" {
			committed = true
		}
	}
	if !committed {
		return errors.New("dispatch before committed debit")
	}
	d.dispatched++
	return d.JobService.StartJob(ctx, id)
}

func TestHTTPQueueDispatchReplayAndRefund(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			s, handler, repo := setupTestServer(true, "test-user")
			project := &domain.Project{UserID: "test-user", OriginalScript: "A hero saves the town.", Title: "Comic"}
			if err := repo.CreateProject(context.Background(), project); err != nil {
				t.Fatal(err)
			}
			character := &domain.Character{ProjectID: project.ID, Name: "Hero"}
			if err := repo.CreateCharacter(context.Background(), character); err != nil {
				t.Fatal(err)
			}
			dispatcher := &committedDispatcher{JobService: s.JobService, ledger: s.CreditService}
			s.JobService = dispatcher
			calls := 0
			providerCost := 0.01
			providerStatus := "done"
			if fail {
				providerCost = 0.0042
				providerStatus = "failed"
			}
			s.AIClient.(*gen.FakeAIServiceClient).GetJobStatusFunc = func(ctx context.Context, id string) (*gen.JobStatusResponse, error) {
				return &gen.JobStatusResponse{JobID: id, Status: gen.JobStatusResponseStatus(providerStatus), ActualCost: &providerCost}, nil
			}
			s.AIClient.(*gen.FakeAIServiceClient).GenerateCharacterReferenceFunc = func(ctx context.Context, req gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
				calls++
				if fail {
					return nil, &gen.AIServiceError{Code: "UPSTREAM_UNAVAILABLE", Message: "worker generation failed", StatusCode: http.StatusServiceUnavailable}
				}
				return &gen.GenerateCharacterReferenceResponse{JobID: req.JobID, ImageURL: "https://images.example/saved.png", ModelID: "fake"}, nil
			}
			var jobID string
			for attempt := 0; attempt < 2; attempt++ {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+character.ID+"/reference-image", strings.NewReader(`{}`))
				req.Header.Set("Idempotency-Key", "same-client-request")
				rec := serveGeneration(s, handler, req)
				if rec.Code != http.StatusAccepted {
					t.Fatalf("attempt %d: %d %s", attempt, rec.Code, rec.Body.String())
				}
				settled := settledJob(t, s, rec)
				jobID = settled.ID
				expectedStatus := jobs.StatusDone
				if fail {
					expectedStatus = jobs.StatusFailed
				}
				if settled.Status != expectedStatus {
					t.Fatalf("job status %s", settled.Status)
				}
			}
			if calls != 1 || dispatcher.dispatched != 1 {
				t.Fatalf("generation calls=%d dispatches=%d", calls, dispatcher.dispatched)
			}
			saved, err := s.JobService.GetJob(context.Background(), jobID)
			if err != nil || saved.ActualCost == nil || *saved.ActualCost != providerCost {
				t.Fatalf("provider cost lost: job=%+v err=%v", saved, err)
			}
			for i := 0; i < 2; i++ {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/generation-jobs/"+jobID, nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("poll: %d %s", rec.Code, rec.Body.String())
				}
			}
			entries, err := s.CreditService.GetUsageHistory(context.Background(), "test-user")
			expectedEntries := 1
			if fail {
				expectedEntries = 2
			}
			if err != nil || len(entries) != expectedEntries {
				t.Fatalf("ledger=%v err=%v", entries, err)
			}
			if fail && entries[1].TransactionType != "refund" {
				t.Fatalf("missing refund: %v", entries)
			}
		})
	}
}
