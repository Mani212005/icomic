package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"icomic/backend/internal/domain"
)

var (
	ErrJobNotFound        = errors.New("generation job not found")
	ErrCannotCancelJob    = errors.New("job cannot be cancelled in current status")
	ErrDuplicateIdempotent = errors.New("idempotent job already exists")
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

// JobService defines job management operations.
type JobService interface {
	CreateJob(ctx context.Context, req CreateJobRequest) (*domain.GenerationJob, bool, error)
	GetJob(ctx context.Context, jobID string) (*domain.GenerationJob, error)
	CancelJob(ctx context.Context, jobID string) error
	UpdateJobStatus(ctx context.Context, jobID string, status string, actualCost *float64, errMsg *string) error
}

// InMemoryJobService is a thread-safe in-memory job store for testing and dev.
type InMemoryJobService struct {
	mu             sync.Mutex
	jobsByID       map[string]*domain.GenerationJob
	jobsByIdempKey map[string]*domain.GenerationJob
}

func NewInMemoryJobService() *InMemoryJobService {
	return &InMemoryJobService{
		jobsByID:       make(map[string]*domain.GenerationJob),
		jobsByIdempKey: make(map[string]*domain.GenerationJob),
	}
}

func generateJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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
		Status:         "pending",
		IdempotencyKey: req.IdempotencyKey,
		EstimatedCost:  req.EstimatedCost,
		HashedUserID:   req.HashedUserID,
		CreatedAt:      time.Now().UTC(),
	}

	s.jobsByID[job.ID] = job
	s.jobsByIdempKey[req.IdempotencyKey] = job
	return job, false, nil
}

func (s *InMemoryJobService) GetJob(ctx context.Context, jobID string) (*domain.GenerationJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return nil, ErrJobNotFound
	}
	return job, nil
}

func (s *InMemoryJobService) CancelJob(ctx context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return ErrJobNotFound
	}
	if job.Status == "done" || job.Status == "failed" {
		return ErrCannotCancelJob
	}
	job.Status = "cancelled"
	now := time.Now().UTC()
	job.CompletedAt = &now
	return nil
}

func (s *InMemoryJobService) UpdateJobStatus(ctx context.Context, jobID string, status string, actualCost *float64, errMsg *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return ErrJobNotFound
	}
	job.Status = status
	job.ActualCost = actualCost
	job.ErrorMessage = errMsg
	if status == "done" || status == "failed" || status == "cancelled" {
		now := time.Now().UTC()
		job.CompletedAt = &now
	}
	return nil
}
