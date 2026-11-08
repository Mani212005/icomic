package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
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

func uncertainProviderOutcome(err error) bool {
	var serviceErr *gen.AIServiceError
	if errors.As(err, &serviceErr) && serviceErr.Code == "OUTCOME_UNKNOWN" {
		return true
	}
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "outcome is unknown")
}

// Persist a terminal generation outcome and refund only known failed attempts.
// Unknown provider outcomes keep the debit because the upstream may have billed.
func (s *Server) failGeneration(ctx context.Context, job *domain.GenerationJob, cause error, amount float64) error {
	defer s.finishDispatch(ctx, job.ID)
	message := safeGenerationMessage(cause)
	if uncertainProviderOutcome(cause) {
		return s.JobService.UpdateJobStatus(ctx, job.ID, "uncertain", nil, &message)
	}
	statusErr := s.JobService.UpdateJobStatus(ctx, job.ID, "failed", nil, &message)
	_, refundErr := s.CreditService.Refund(ctx, credits.RefundRequest{
		UserID: job.UserID, ProjectID: job.ProjectID, GenerationJobID: job.ID, Amount: amount,
	})
	return errors.Join(statusErr, refundErr)
}

func safeGenerationMessage(err error) string {
	var serviceErr *gen.AIServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr.Message
	}
	switch {
	case errors.Is(err, credits.ErrInsufficientCredits), errors.Is(err, db.ErrInsufficientCredit),
		errors.Is(err, domain.ErrKillSwitchActive), errors.Is(err, domain.ErrQuotaExceeded),
		errors.Is(err, domain.ErrMissingPrerequisiteApproval), errors.Is(err, domain.ErrStaleApproval),
		errors.Is(err, domain.ErrActiveJobExists), errors.Is(err, domain.ErrBookBudgetExceeded),
		errors.Is(err, domain.ErrDailyImageLimitReached):
		return err.Error()
	default:
		return "Generation could not be completed. Try again later."
	}
}

func (s *Server) refundFailedGeneration(ctx context.Context, job *domain.GenerationJob) error {
	amount := s.generationDebit(job)
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
		statusErr := s.recordGenerationFailure(ctx, job, result.err, result.cost)
		return result.result, errors.Join(result.err, statusErr)
	}
	return result.result, result.err
}
