package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

// Lifecycle statuses for generation jobs (SPEC 8.3, 8.5, ARCHITECTURE 5.1).
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusUncertain = "uncertain"
	StatusCancelled = "cancelled"
)

var (
	ErrJobNotFound         = errors.New("generation job not found")
	ErrCancelUnsupported   = errors.New("job store cannot cancel jobs")
	ErrDuplicateIdempotent = errors.New("idempotent job already exists")
	ErrInvalidStatus       = errors.New("invalid job status")
	ErrJobAlreadyTerminal  = errors.New("job is already in a terminal status")
)

type CreateJobRequest struct {
	EntityID             string
	DebitAmount          float64
	KillSwitchActive     bool
	AllowDirectFromBrief bool
	ProjectID            string
	UserID               string
	JobType              string
	Stage                string
	ModelID              string
	IdempotencyKey       string
	EstimatedCost        float64
	HashedUserID         string
	// Envelope, when set, is committed with the job and debit by persistent stores.
	Envelope *db.GenerationEnvelope
}

// JobService defines generation job lifecycle management (SPEC 8.3, 8.5, ARCHITECTURE 5).
// Exported methods are kept strictly backward compatible.
type JobService interface {
	CreateJob(ctx context.Context, req CreateJobRequest) (*domain.GenerationJob, bool, error)
	GetJob(ctx context.Context, jobID string) (*domain.GenerationJob, error)
	// CancelJob stops a job that has not reached the provider (outcome cancelled, debit refunded)
	// and otherwise reports honestly: a job already claimed or sent is settling and keeps running
	// to its own terminal state, and a job that already finished is returned unchanged.
	CancelJob(ctx context.Context, jobID string) (*db.CancelResult, error)
	UpdateJobStatus(ctx context.Context, jobID string, status string, actualCost *float64, errMsg *string) error

	// Asynchronous lifecycle extensions
	StartJob(ctx context.Context, jobID string) error
	CompleteJob(ctx context.Context, jobID string, actualCost float64) error
	FailJob(ctx context.Context, jobID string, errMsg string, actualCost *float64) error
	DispatchAsync(ctx context.Context, jobID string, task func(ctx context.Context) (actualCost float64, err error)) error
}

// InMemoryJobService is a thread-safe in-memory job store with full lifecycle support.
type InMemoryJobService struct {
	creditService  credits.CreditService
	mu             sync.Mutex
	jobsByID       map[string]*domain.GenerationJob
	jobsByIdempKey map[string]*domain.GenerationJob
	cancelFuncs    map[string]context.CancelFunc
	outbox         []*memoryDispatch
	debits         map[string]float64
}

// memoryDispatch is the in-memory twin of a generation_dispatches row.
type memoryDispatch struct {
	pending    db.PendingDispatch
	delivered  bool
	cancelled  bool
	leaseUntil time.Time
}

func NewInMemoryJobService() *InMemoryJobService {
	return &InMemoryJobService{
		jobsByID:       make(map[string]*domain.GenerationJob),
		jobsByIdempKey: make(map[string]*domain.GenerationJob),
		cancelFuncs:    make(map[string]context.CancelFunc),
		debits:         make(map[string]float64),
	}
}

// SetCreditService attaches the fake ledger so creation can fail without publishing a job.
func (s *InMemoryJobService) SetCreditService(c credits.CreditService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creditService = c
}

func generateJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isValidStatus(status string) bool {
	switch status {
	case StatusPending, StatusRunning, StatusDone, StatusFailed, StatusUncertain, StatusCancelled:
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
		copy := *existing
		return &copy, true, nil
	}
	job := &domain.GenerationJob{
		EntityID:       req.EntityID,
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

	// A request that cannot be serialized fails before any debit, like the PostgreSQL commit.
	var envelope *memoryDispatch
	if req.Envelope != nil {
		request, err := db.StampJobID(req.Envelope.Request, job.ID)
		if err != nil {
			return nil, false, err
		}
		envelope = &memoryDispatch{
			pending:    db.PendingDispatch{JobID: job.ID, Operation: req.Envelope.Operation, Request: request},
			leaseUntil: time.Now(),
		}
	}

	if req.DebitAmount > 0 {
		if s.creditService == nil {
			return nil, false, errors.New("credit service is required")
		}
		if _, err := s.creditService.Debit(ctx, credits.DebitRequest{UserID: req.UserID, ProjectID: req.ProjectID, GenerationJobID: job.ID, Amount: req.DebitAmount}); err != nil {
			return nil, false, err
		}
	}
	if envelope != nil {
		s.outbox = append(s.outbox, envelope)
	}
	if req.DebitAmount > 0 {
		s.debits[job.ID] = req.DebitAmount
	}
	s.jobsByID[job.ID] = job
	s.jobsByIdempKey[req.IdempotencyKey] = job
	copy := *job
	return &copy, false, nil
}

// ClaimDispatches mirrors the PostgreSQL outbox for the in-memory fakes: a committed envelope is
// claimable at once, a claim leases it, and a settled job never dispatches.
func (s *InMemoryJobService) ClaimDispatches(ctx context.Context, limit int, ignoreLeases bool) ([]db.PendingDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var claimed []db.PendingDispatch
	for _, d := range s.outbox {
		if d.delivered || d.cancelled {
			continue
		}
		if job, found := s.jobsByID[d.pending.JobID]; found && job.Status != StatusPending && job.Status != StatusRunning {
			d.delivered = true
			continue
		}
		if len(claimed) >= limit || (!ignoreLeases && d.leaseUntil.After(now)) {
			continue
		}
		d.pending.Attempts++
		d.leaseUntil = now.Add(db.DispatchLease)
		claimed = append(claimed, d.pending)
	}
	return claimed, nil
}

// FinishDispatch retires an envelope once its job is terminal. It is idempotent.
func (s *InMemoryJobService) FinishDispatch(ctx context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.outbox {
		if d.pending.JobID == jobID && !d.cancelled {
			d.delivered = true
		}
	}
	return nil
}

// ReleaseDispatch makes a pending envelope claimable again after db.DispatchRetryDelay.
func (s *InMemoryJobService) ReleaseDispatch(ctx context.Context, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.outbox {
		if d.pending.JobID == jobID && !d.delivered && !d.cancelled {
			d.leaseUntil = time.Now().Add(db.DispatchRetryDelay)
		}
	}
	return nil
}

var _ db.DispatchStore = (*InMemoryJobService)(nil)

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

// CancelJob mirrors the PostgreSQL cancellation for the in-memory fakes. A pending job whose
// envelope no dispatcher has claimed is cancelled and refunded; a claimed or running job is
// settling and untouched; a terminal job is returned as it is. Repeating a cancel is idempotent.
func (s *InMemoryJobService) CancelJob(ctx context.Context, jobID string) (*db.CancelResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, found := s.jobsByID[jobID]
	if !found {
		return nil, ErrJobNotFound
	}
	result := func(outcome db.CancelOutcome, refunded float64) (*db.CancelResult, error) {
		snapshot := *job
		return &db.CancelResult{Outcome: outcome, Job: &snapshot, Refunded: refunded}, nil
	}
	switch job.Status {
	case StatusCancelled:
		return result(db.CancelOutcomeCancelled, s.debits[jobID])
	case StatusDone, StatusFailed, StatusUncertain:
		return result(db.CancelOutcomeFinished, 0)
	}
	var envelope *memoryDispatch
	for _, d := range s.outbox {
		if d.pending.JobID == jobID {
			envelope = d
		}
	}
	unsent := job.Status == StatusPending && (envelope == nil || (!envelope.delivered && !envelope.cancelled && envelope.pending.Attempts == 0))
	if !unsent {
		return result(db.CancelOutcomeSettling, 0)
	}
	refund := s.debits[jobID]
	if refund > 0 {
		if s.creditService == nil {
			return nil, errors.New("credit service is required")
		}
		if _, err := s.creditService.Refund(ctx, credits.RefundRequest{UserID: job.UserID, ProjectID: job.ProjectID, GenerationJobID: jobID, Amount: refund}); err != nil {
			return nil, err
		}
	}
	job.Status = StatusCancelled
	message := db.CancelMessage
	job.ErrorMessage = &message
	now := time.Now().UTC()
	job.CompletedAt = &now
	if envelope != nil {
		envelope.cancelled = true
	}
	if cancel, ok := s.cancelFuncs[jobID]; ok {
		cancel()
		delete(s.cancelFuncs, jobID)
	}
	return result(db.CancelOutcomeCancelled, refund)
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
	// A terminal status is never reversed, and a cancelled job is never touched again.
	if db.TerminalStatus(job.Status) && (job.Status == StatusCancelled || status != job.Status) {
		return fmt.Errorf("%w: cannot change %s to %s", ErrJobAlreadyTerminal, job.Status, status)
	}

	job.Status = status
	if actualCost != nil {
		job.ActualCost = actualCost
	}
	if errMsg != nil {
		job.ErrorMessage = errMsg
	}
	if status == StatusDone || status == StatusFailed || status == StatusUncertain || status == StatusCancelled {
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

	if job.Status == StatusDone || job.Status == StatusFailed || job.Status == StatusUncertain || job.Status == StatusCancelled {
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

	if job.Status == StatusDone || job.Status == StatusFailed || job.Status == StatusUncertain || job.Status == StatusCancelled {
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
		if currentJob.Status != StatusRunning {
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
