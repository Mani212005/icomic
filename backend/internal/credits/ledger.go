package credits

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
	ErrInsufficientCredits = errors.New("insufficient credit balance")
	ErrNegativeAmount      = errors.New("credit transaction amount must be positive")
)

const (
	// Flat per-image attempt cost for Meta muse-image-1.0 (SPEC 5.2).
	ImageUnitPrice = 0.01
	// Estimated token cost for colour palette suggestion (SPEC 5.2).
	ColourSuggestionUnitPrice = 0.005
)

// DebitRequest parameters for a credit deduction.
type DebitRequest struct {
	UserID          string
	ProjectID       string
	GenerationJobID string
	Amount          float64
}

// RefundRequest parameters for a credit refund.
type RefundRequest struct {
	UserID          string
	ProjectID       string
	GenerationJobID string
	Amount          float64
}

// CreditService defines the interface for credit ledger management (SPEC 5, ARCHITECTURE 5.1).
type CreditService interface {
	GetBalance(ctx context.Context, userID string) (float64, error)
	Debit(ctx context.Context, req DebitRequest) (*domain.CreditLedger, error)
	Refund(ctx context.Context, req RefundRequest) (*domain.CreditLedger, error)
	GetUsageHistory(ctx context.Context, userID string) ([]domain.CreditLedger, error)
	EstimateCost(stage string, count int) domain.CostEstimate
}

// InMemoryCreditService is a thread-safe in-memory credit ledger for tests and development.
type InMemoryCreditService struct {
	mu                 sync.Mutex
	balances           map[string]float64
	entries            []domain.CreditLedger
	killSwitchProvider domain.KillSwitchProvider
	totalPlatformSpend float64
}

func NewInMemoryCreditService(initialBalances map[string]float64) *InMemoryCreditService {
	b := make(map[string]float64)
	for k, v := range initialBalances {
		b[k] = v
	}
	return &InMemoryCreditService{
		balances: b,
		entries:  make([]domain.CreditLedger, 0),
	}
}

// SetKillSwitchProvider connects a KillSwitchProvider to auto-trip when platform-wide spend hits threshold (SPEC 5.5).
func (s *InMemoryCreditService) SetKillSwitchProvider(provider domain.KillSwitchProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.killSwitchProvider = provider
}

// KillSwitchProvider returns the attached kill switch provider, if any.
func (s *InMemoryCreditService) KillSwitchProvider() domain.KillSwitchProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.killSwitchProvider
}

// TotalPlatformSpend returns the cumulative debited platform spend across all users.
func (s *InMemoryCreditService) TotalPlatformSpend() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalPlatformSpend
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *InMemoryCreditService) GetBalance(ctx context.Context, userID string) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.balances[userID], nil
}

func (s *InMemoryCreditService) Debit(ctx context.Context, req DebitRequest) (*domain.CreditLedger, error) {
	if req.Amount < 0 {
		return nil, ErrNegativeAmount
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.balances[req.UserID]
	if current < req.Amount {
		return nil, ErrInsufficientCredits
	}

	newBalance := current - req.Amount
	s.balances[req.UserID] = newBalance
	s.totalPlatformSpend += req.Amount

	// If kill switch provider is attached with a spend threshold, check if tripped (SPEC 5.5)
	if s.killSwitchProvider != nil {
		s.killSwitchProvider.CheckSpend(s.totalPlatformSpend)
	}

	jobID := req.GenerationJobID
	entry := domain.CreditLedger{
		ID:              generateID(),
		UserID:          req.UserID,
		ProjectID:       req.ProjectID,
		GenerationJobID: &jobID,
		TransactionType: "debit",
		Amount:          req.Amount,
		BalanceAfter:    newBalance,
		CreatedAt:       time.Now().UTC(),
	}
	s.entries = append(s.entries, entry)
	return &entry, nil
}

func (s *InMemoryCreditService) Refund(ctx context.Context, req RefundRequest) (*domain.CreditLedger, error) {
	if req.Amount < 0 {
		return nil, ErrNegativeAmount
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.entries {
		if entry.TransactionType == "refund" && entry.GenerationJobID != nil && *entry.GenerationJobID == req.GenerationJobID {
			copy := entry
			return &copy, nil
		}
	}

	current := s.balances[req.UserID]
	newBalance := current + req.Amount
	s.balances[req.UserID] = newBalance
	s.totalPlatformSpend -= req.Amount
	if s.totalPlatformSpend < 0 {
		s.totalPlatformSpend = 0
	}

	jobID := req.GenerationJobID
	entry := domain.CreditLedger{
		ID:              generateID(),
		UserID:          req.UserID,
		ProjectID:       req.ProjectID,
		GenerationJobID: &jobID,
		TransactionType: "refund",
		Amount:          req.Amount,
		BalanceAfter:    newBalance,
		CreatedAt:       time.Now().UTC(),
	}
	s.entries = append(s.entries, entry)
	return &entry, nil
}

func (s *InMemoryCreditService) GetUsageHistory(ctx context.Context, userID string) ([]domain.CreditLedger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := make([]domain.CreditLedger, 0)
	for _, e := range s.entries {
		if e.UserID == userID {
			res = append(res, e)
		}
	}
	return res, nil
}

// GetUserDailySpend returns the total credit debited for a user since the specified time.
func (s *InMemoryCreditService) GetUserDailySpend(ctx context.Context, userID string, since time.Time) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0.0
	for _, e := range s.entries {
		if e.UserID == userID && e.TransactionType == "debit" && e.CreatedAt.After(since) {
			total += e.Amount
		}
	}
	return total, nil
}

func (s *InMemoryCreditService) EstimateCost(stage string, count int) domain.CostEstimate {
	return EstimateCost(stage, count)
}

// EstimateCost calculates stage-specific credit costs based on verified pricing (SPEC 5.2).
func EstimateCost(stage string, count int) domain.CostEstimate {
	return DefaultPricing().Estimate(stage, count)
}
