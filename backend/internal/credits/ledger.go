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
	mu       sync.Mutex
	balances map[string]float64
	entries  []domain.CreditLedger
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

	current := s.balances[req.UserID]
	newBalance := current + req.Amount
	s.balances[req.UserID] = newBalance

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

func (s *InMemoryCreditService) EstimateCost(stage string, count int) domain.CostEstimate {
	return EstimateCost(stage, count)
}

// EstimateCost calculates stage-specific credit costs based on verified pricing (SPEC 5.2).
func EstimateCost(stage string, count int) domain.CostEstimate {
	if count < 1 {
		count = 1
	}

	switch stage {
	case "draft", "draft_sketch":
		return domain.CostEstimate{
			StageName:       "draft_sketch",
			ImageCount:      count,
			EstimatedCost:   float64(count) * ImageUnitPrice,
			Assumptions:     []string{"$0.01 per page image draft attempt using Meta muse-image-1.0"},
			IsEstimatePrice: false,
		}

	case "line_art", "final_line_art":
		return domain.CostEstimate{
			StageName:       "final_line_art",
			ImageCount:      count,
			EstimatedCost:   float64(count) * ImageUnitPrice,
			Assumptions:     []string{"$0.01 per page final line art image attempt using Meta muse-image-1.0"},
			IsEstimatePrice: false,
		}

	case "character_reference", "reference":
		return domain.CostEstimate{
			StageName:       "character_reference",
			ImageCount:      count,
			EstimatedCost:   float64(count) * ImageUnitPrice,
			Assumptions:     []string{"$0.01 per character reference image attempt using Meta muse-image-1.0"},
			IsEstimatePrice: false,
		}

	case "colour_suggestion", "colour":
		return domain.CostEstimate{
			StageName:       "colour_suggestion",
			ImageCount:      0,
			EstimatedCost:   float64(count) * ColourSuggestionUnitPrice,
			Assumptions:     []string{"Token cost estimate for language/vision palette suggestion model call"},
			IsEstimatePrice: true,
		}

	default:
		return domain.CostEstimate{
			StageName:       stage,
			ImageCount:      count,
			EstimatedCost:   float64(count) * ImageUnitPrice,
			Assumptions:     []string{"Standard flat per-image cost basis"},
			IsEstimatePrice: true,
		}
	}
}
