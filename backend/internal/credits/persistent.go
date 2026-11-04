package credits

import (
	"context"
	"fmt"

	"icomic/backend/internal/domain"
)

type LedgerStore interface {
	GetCreditBalance(context.Context, string) (float64, error)
	ListCreditLedger(context.Context, string) ([]*domain.CreditLedger, error)
	RefundGeneration(context.Context, string, float64) (*domain.CreditLedger, error)
	GenerationDebit(context.Context, string) (*domain.CreditLedger, error)
}
type PersistentCreditService struct{ store LedgerStore }

func NewPersistentCreditService(store LedgerStore) *PersistentCreditService {
	return &PersistentCreditService{store}
}
func (s *PersistentCreditService) GetBalance(ctx context.Context, id string) (float64, error) {
	return s.store.GetCreditBalance(ctx, id)
}
func (s *PersistentCreditService) GetUsageHistory(ctx context.Context, id string) ([]domain.CreditLedger, error) {
	entries, err := s.store.ListCreditLedger(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]domain.CreditLedger, 0, len(entries))
	for _, e := range entries {
		result = append(result, *e)
	}
	return result, nil
}
func (s *PersistentCreditService) EstimateCost(stage string, count int) domain.CostEstimate {
	return EstimateCost(stage, count)
}
func (s *PersistentCreditService) Debit(ctx context.Context, req DebitRequest) (*domain.CreditLedger, error) {
	entry, err := s.store.GenerationDebit(ctx, req.GenerationJobID)
	if err != nil {
		return nil, err
	}
	if entry.UserID != req.UserID || entry.ProjectID != req.ProjectID || entry.Amount != req.Amount {
		return nil, fmt.Errorf("debit does not match the committed generation")
	}
	return entry, nil
}
func (s *PersistentCreditService) Refund(ctx context.Context, req RefundRequest) (*domain.CreditLedger, error) {
	debit, err := s.store.GenerationDebit(ctx, req.GenerationJobID)
	if err != nil {
		return nil, err
	}
	if debit.UserID != req.UserID || debit.ProjectID != req.ProjectID {
		return nil, fmt.Errorf("refund does not match the committed generation")
	}
	return s.store.RefundGeneration(ctx, req.GenerationJobID, req.Amount)
}

var _ CreditService = (*PersistentCreditService)(nil)
