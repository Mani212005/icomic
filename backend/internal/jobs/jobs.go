package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"icomic/backend/internal/domain"
)

// Lifecycle statuses for generation jobs (SPEC 8.3, 8.5, ARCHITECTURE 5.1).
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

var (
	ErrJobNotFound         = errors.New("generation job not found")
	ErrCannotCancelJob     = errors.New("job cannot be cancelled in current status")
	ErrDuplicateIdempotent = errors.New("idempotent job already exists")
	ErrInvalidStatus       = errors.New("invalid job status")
	ErrJobAlreadyTerminal  = errors.New("job is already in a terminal status")
)

type CreateJobRequest struct {
	ProjectID      string
	UserID         string
	JobType        string
	Stage          string
	ModelID        string
	IdempotencyKey string
	EstimatedCost  float64
	HashedUserID   string
}

// JobService defines generation job lifecycle management (SPEC 8.3, 8.5, ARCHITECTURE 5).
// Exported methods are kept strictly backward compatible.
type JobService interface {
	CreateJob(ctx context.Context, req CreateJobRequest) (*domain.GenerationJob, bool, error)
	GetJob(ctx context.Context, jobID string) (*domain.GenerationJob, error)
	CancelJob(ctx context.Context, jobID string) error
	UpdateJobStatus(ctx context.Context, jobID string, status string, actualCost *float64, errMsg *string) error

	// Asynchronous lifecycle extensions
	StartJob(ctx context.Context, jobID string) error
	CompleteJob(ctx context.Context, jobID string, actualCost float64) error
	FailJob(ctx context.Context, jobID string, errMsg string, actualCost *float64) error
	DispatchAsync(ctx context.Context, jobID string, task func(ctx context.Context) (actualCost float64, err error)) error
}

// InMemoryJobService is a thread-safe in-memory job store with full lifecycle support.
type InMemoryJobService struct {
	mu             sync.Mutex
	jobsByID       map[string]*domain.GenerationJob
	jobsByIdempKey map[string]*domain.GenerationJob
	cancelFuncs    map[string]context.CancelFunc
}

func NewInMemoryJobService() *InMemoryJobService {
	return &InMemoryJobService{
		jobsByID:       make(map[string]*domain.GenerationJob),
		jobsByIdempKey: make(map[string]*domain.GenerationJob),
		cancelFuncs:    make(map[string]context.CancelFunc),
	}
}

func generateJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isValidStatus(status string) bool {
	switch status {
	case StatusPending, StatusRunning, StatusDone, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

// CreateJob creates a job idempotently. If a job with the idempotency key already exists,
// it returns the existing job and sets existed=true.
func (s *InMemoryJobService) CreateJob(ctx context.Context, req CreateJobRequest) (*domain.GenerationJob, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, found := s.jobsByIdempKey[req.IdempotencyKey]; found {
		return existing, true, nil
	}

	job := &domain.GenerationJob{
		ID:             generateJobID(),
		ProjectID:      req.ProjectID,
		UserID:         req.UserID,
		JobType:        req.JobType,
		Stage:          req.Stage,
		ModelID:        req.ModelID,
		Status:         StatusPending,
		IdempotencyKey: req.IdempotencyKey,
		EstimatedCost:  req.EstimatedCost,
		HashedUserID:   req.HashedUserID,
		CreatedAt:      time.Now().UTC(),
	}

	s.jobsByID[job.ID] = job
	s.jobsByIdempKey[req.IdempotencyKey] = job
	return job, false, nil
}

// GetJob retrieves a generation job by its ID.
func (s *InMemoryJobService) GetJob(ctx context.Context, jobID string) (*domain.GenerationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return nil, ErrJobNotFound
	}
	// Return shallow copy
	jobCopy := *job
	return &jobCopy, nil
}

// CancelJob cancels an active (pending or running) job.
func (s *InMemoryJobService) CancelJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return ErrJobNotFound
	}

	if job.Status == StatusDone || job.Status == StatusFailed || job.Status == StatusCancelled {
		return ErrCannotCancelJob
	}

	job.Status = StatusCancelled
	now := time.Now().UTC()
	job.CompletedAt = &now

	if cancel, ok := s.cancelFuncs[jobID]; ok {
		cancel()
		delete(s.cancelFuncs, jobID)
	}

	return nil
}

// UpdateJobStatus updates the status, actual cost, and error message of a job.
func (s *InMemoryJobService) UpdateJobStatus(ctx context.Context, jobID string, status string, actualCost *float64, errMsg *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !isValidStatus(status) {
		return fmt.Errorf("%w: %s", ErrInvalidStatus, status)
	}

	job, found := s.jobsByID[jobID]
	if !found {
		return ErrJobNotFound
	}

	job.Status = status
	if actualCost != nil {
		job.ActualCost = actualCost
	}
	if errMsg != nil {
		job.ErrorMessage = errMsg
	}
	if status == StatusDone || status == StatusFailed || status == StatusCancelled {
		if job.CompletedAt == nil {
			now := time.Now().UTC()
			job.CompletedAt = &now
		}
	}
	return nil
}

// StartJob transitions a pending job to running status.
func (s *InMemoryJobService) StartJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return ErrJobNotFound
	}

	if job.Status == StatusRunning {
		return nil
	}

	if job.Status != StatusPending {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrJobAlreadyTerminal, job.Status, StatusRunning)
	}

	job.Status = StatusRunning
	return nil
}

// CompleteJob transitions a running or pending job to done and records actual cost.
func (s *InMemoryJobService) CompleteJob(ctx context.Context, jobID string, actualCost float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return ErrJobNotFound
	}

	if job.Status == StatusDone || job.Status == StatusFailed || job.Status == StatusCancelled {
		return fmt.Errorf("%w: cannot complete job in status %s", ErrJobAlreadyTerminal, job.Status)
	}

	job.Status = StatusDone
	costCopy := actualCost
	job.ActualCost = &costCopy
	now := time.Now().UTC()
	job.CompletedAt = &now

	delete(s.cancelFuncs, jobID)
	return nil
}

// FailJob transitions a running or pending job to failed status with an error message and optional cost.
func (s *InMemoryJobService) FailJob(ctx context.Context, jobID string, errMsg string, actualCost *float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return ErrJobNotFound
	}

	if job.Status == StatusDone || job.Status == StatusFailed || job.Status == StatusCancelled {
		return fmt.Errorf("%w: cannot fail job in status %s", ErrJobAlreadyTerminal, job.Status)
	}

	job.Status = StatusFailed
	errCopy := errMsg
	job.ErrorMessage = &errCopy
	if actualCost != nil {
		job.ActualCost = actualCost
	}
	now := time.Now().UTC()
	job.CompletedAt = &now

	delete(s.cancelFuncs, jobID)
	return nil
}

// DispatchAsync runs a task in a background goroutine, managing the job lifecycle through
// pending -> running -> done/failed/cancelled.
func (s *InMemoryJobService) DispatchAsync(ctx context.Context, jobID string, task func(ctx context.Context) (actualCost float64, err error)) error {
	s.mu.Lock()
	job, found := s.jobsByID[jobID]
	if !found {
		s.mu.Unlock()
		return ErrJobNotFound
	}
	if job.Status != StatusPending {
		s.mu.Unlock()
		return fmt.Errorf("%w: cannot dispatch job in status %s", ErrJobAlreadyTerminal, job.Status)
	}

	taskCtx, cancel := context.WithCancel(ctx)
	s.cancelFuncs[jobID] = cancel
	job.Status = StatusRunning
	s.mu.Unlock()

	go func() {
		defer cancel()

		cost, err := task(taskCtx)

		s.mu.Lock()
		defer s.mu.Unlock()

		currentJob, exists := s.jobsByID[jobID]
		if !exists {
			return
		}

		// If job was cancelled while task was running, do not overwrite status
		if currentJob.Status == StatusCancelled {
			delete(s.cancelFuncs, jobID)
			return
		}

		delete(s.cancelFuncs, jobID)
		now := time.Now().UTC()
		currentJob.CompletedAt = &now

		if err != nil {
			currentJob.Status = StatusFailed
			errMsg := err.Error()
			currentJob.ErrorMessage = &errMsg
			currentJob.ActualCost = &cost
			return
		}

		currentJob.Status = StatusDone
		currentJob.ActualCost = &cost
	}()

	return nil
}
