package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/domain"
)

// generationKey lets a caller retry a request without creating another charge.
// Scope the caller's key to its owner, entity and operation before persistence.
func generationKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return uuid.NewString()
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(r.Method+":"+r.URL.Path+":"+auth.UserIDFromContext(r.Context())+":"+key)).String()
}

// Persist the failed state and the separate, idempotent refund transaction.
// Surface storage failures so an unsuccessful refund never looks successful.
func (s *Server) failGeneration(ctx context.Context, job *domain.GenerationJob, cause error, amount float64) error {
	message := cause.Error()
	statusErr := s.JobService.UpdateJobStatus(ctx, job.ID, "failed", nil, &message)
	_, refundErr := s.CreditService.Refund(ctx, credits.RefundRequest{
		UserID: job.UserID, ProjectID: job.ProjectID, GenerationJobID: job.ID, Amount: amount,
	})
	return errors.Join(statusErr, refundErr)
}

func (s *Server) refundFailedGeneration(ctx context.Context, job *domain.GenerationJob) error {
	amount := 0.0
	switch job.JobType {
	case "character_reference", "draft_sketch", "final_line_art":
		amount = s.Pricing.ImagePriceUSD
	case "colour_suggestion":
		amount = s.Pricing.ColourEstimateUSD
	}
	if amount == 0 {
		return nil
	}
	_, err := s.CreditService.Refund(ctx, credits.RefundRequest{
		UserID: job.UserID, ProjectID: job.ProjectID, GenerationJobID: job.ID, Amount: amount,
	})
	return err
}

// dispatchGeneration preserves the completed-response HTTP contract. The port
// sends a serializable job request to Python, which durably enqueues it before
// waiting for the consumer. Fake queue delivery runs synchronously in tests.
func dispatchGeneration[T any](s *Server, ctx context.Context, job *domain.GenerationJob, dispatch func(context.Context) (*T, error)) (*T, error) {
	type outcome struct {
		result *T
		err    error
		cost   float64
	}
	done := make(chan outcome, 1)
	err := s.JobService.DispatchAsync(ctx, job.ID, func(taskCtx context.Context) (float64, error) {
		result, err := dispatch(taskCtx)
		// Failed output validation still incurs the provider's recorded usage.
		cost := s.aiActualCost(taskCtx, job.ID)
		done <- outcome{result, err, cost}
		return cost, err
	})
	if err != nil {
		return nil, err
	}
	result := <-done
	if result.err != nil {
		// The in-memory dispatcher may still be returning from its task. Save
		// usage before HTTP failure handling marks the job terminal.
		message := result.err.Error()
		statusErr := s.JobService.UpdateJobStatus(ctx, job.ID, "failed", &result.cost, &message)
		return result.result, errors.Join(result.err, statusErr)
	}
	return result.result, result.err
}
