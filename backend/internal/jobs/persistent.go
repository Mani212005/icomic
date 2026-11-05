package jobs

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

// GenerationStore keeps the commit boundary below the service. CreateGeneration
// returns only after commit, so callers may dispatch only after a successful return.
type GenerationStore interface {
	CreateGeneration(context.Context, *domain.GenerationJob, db.GenerationOptions, float64) (*domain.GenerationJob, bool, error)
	GetJob(context.Context, string) (*domain.GenerationJob, error)
	TransitionGeneration(context.Context, string, string, *float64, *string, []string) error
}
type PersistentJobService struct{ store GenerationStore }

func NewPersistentJobService(store GenerationStore) *PersistentJobService {
	return &PersistentJobService{store}
}
func (s *PersistentJobService) CreateJob(ctx context.Context, req CreateJobRequest) (*domain.GenerationJob, bool, error) {
	stage, kind := req.Stage, req.JobType
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = uuid.NewString()
	}
	if req.DebitAmount == 0 {
		switch kind {
		case "character_reference", "draft_sketch", "final_line_art":
			req.DebitAmount = credits.ImageUnitPrice
		case "colour_suggestion":
			req.DebitAmount = credits.ColourSuggestionUnitPrice
		}
	}
	switch stage {
	case "script", "panels", "outline":
		stage = "text"
	}
	if kind == "assemble_pdf" {
		kind = "pdf_assembly"
	}
	job := &domain.GenerationJob{ID: uuid.NewString(), ProjectID: req.ProjectID, UserID: req.UserID, JobType: kind, Stage: stage, ModelID: req.ModelID, Status: StatusPending, IdempotencyKey: req.IdempotencyKey, EstimatedCost: req.EstimatedCost, HashedUserID: req.HashedUserID}
	created, existed, err := s.store.CreateGeneration(ctx, job, db.GenerationOptions{EntityID: req.EntityID, KillSwitchActive: req.KillSwitchActive, AllowDirectFromBrief: req.AllowDirectFromBrief}, req.DebitAmount)
	return publicJob(created), existed, err
}
func (s *PersistentJobService) GetJob(ctx context.Context, id string) (*domain.GenerationJob, error) {
	job, err := s.store.GetJob(ctx, id)
	if errors.Is(err, db.ErrNotFound) {
		err = ErrJobNotFound
	}
	return publicJob(job), err
}
func (s *PersistentJobService) transition(ctx context.Context, id, status string, cost *float64, msg *string, allowed []string) error {
	if !isValidStatus(status) {
		return ErrInvalidStatus
	}
	err := s.store.TransitionGeneration(ctx, id, status, cost, msg, allowed)
	if errors.Is(err, db.ErrNotFound) {
		return ErrJobNotFound
	}
	if errors.Is(err, db.ErrConflict) {
		return ErrJobAlreadyTerminal
	}
	return err
}
func (s *PersistentJobService) CancelJob(ctx context.Context, id string) error {
	err := s.transition(ctx, id, StatusCancelled, nil, nil, []string{StatusPending, StatusRunning})
	if errors.Is(err, ErrJobAlreadyTerminal) {
		return ErrCannotCancelJob
	}
	return err
}
func (s *PersistentJobService) UpdateJobStatus(ctx context.Context, id, status string, cost *float64, msg *string) error {
	return s.transition(ctx, id, status, cost, msg, nil)
}
func (s *PersistentJobService) StartJob(ctx context.Context, id string) error {
	return s.transition(ctx, id, StatusRunning, nil, nil, []string{StatusPending, StatusRunning})
}
func (s *PersistentJobService) CompleteJob(ctx context.Context, id string, cost float64) error {
	return s.transition(ctx, id, StatusDone, &cost, nil, []string{StatusPending, StatusRunning})
}
func (s *PersistentJobService) FailJob(ctx context.Context, id, msg string, cost *float64) error {
	return s.transition(ctx, id, StatusFailed, cost, &msg, []string{StatusPending, StatusRunning})
}

// DispatchAsync hands committed work to the AI queue through the supplied port
// call. The HTTP compatibility facade waits for its saved result. The Python
// consumer owns generation; no production work is held in a Go goroutine.
func (s *PersistentJobService) DispatchAsync(ctx context.Context, id string, dispatch func(context.Context) (float64, error)) error {
	if err := s.StartJob(ctx, id); err != nil {
		return err
	}
	cost, err := dispatch(ctx)
	if err != nil {
		if failErr := s.FailJob(ctx, id, err.Error(), &cost); failErr != nil {
			return errors.Join(err, failErr)
		}
		return err
	}
	// The handler persists the generated entity before completing the job.
	return nil
}

var _ JobService = (*PersistentJobService)(nil)

// Preserve the existing HTTP job labels while storing the schema's canonical enums.
func publicJob(job *domain.GenerationJob) *domain.GenerationJob {
	if job == nil {
		return nil
	}
	result := *job
	switch result.JobType {
	case "script_analysis":
		result.Stage = "script"
	case "chapter_outline":
		result.Stage = "outline"
	case "panel_text":
		result.Stage = "panels"
	case "pdf_assembly":
		result.JobType = "assemble_pdf"
	}
	return &result
}

// EnforcesPolicyTransactionally lets API decorators retain the policy boundary.
func (s *PersistentJobService) EnforcesPolicyTransactionally() bool { return true }
