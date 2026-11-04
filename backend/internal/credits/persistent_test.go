package credits_test

import (
	"context"
	"testing"

	"icomic/backend/internal/credits"
	"icomic/backend/internal/domain"
)

type ledgerStore struct {
	debit   domain.CreditLedger
	refunds int
}

func (s *ledgerStore) GetCreditBalance(context.Context, string) (float64, error) {
	return s.debit.BalanceAfter, nil
}
func (s *ledgerStore) ListCreditLedger(context.Context, string) ([]*domain.CreditLedger, error) {
	return []*domain.CreditLedger{&s.debit}, nil
}
func (s *ledgerStore) GenerationDebit(context.Context, string) (*domain.CreditLedger, error) {
	return &s.debit, nil
}
func (s *ledgerStore) RefundGeneration(context.Context, string, float64) (*domain.CreditLedger, error) {
	s.refunds++
	return &domain.CreditLedger{TransactionType: "refund"}, nil
}
func TestPersistentCreditsReadCommittedLedgerAndRejectForeignRefunds(t *testing.T) {
	ctx := context.Background()
	store := &ledgerStore{debit: domain.CreditLedger{UserID: "owner", ProjectID: "project", TransactionType: "debit", Amount: 0.01, BalanceAfter: 9.99}}
	service := credits.NewPersistentCreditService(store)
	for i := 0; i < 2; i++ {
		entry, err := service.Debit(ctx, credits.DebitRequest{UserID: "owner", ProjectID: "project", GenerationJobID: "job", Amount: 0.01})
		if err != nil || entry != &store.debit {
			t.Fatalf("debit replay: %v %v", entry, err)
		}
	}
	// Recreating the service reads the same persisted balance and usage.
	restarted := credits.NewPersistentCreditService(store)
	balance, err := restarted.GetBalance(ctx, "owner")
	if err != nil || balance != 9.99 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
	history, err := restarted.GetUsageHistory(ctx, "owner")
	if err != nil || len(history) != 1 {
		t.Fatalf("history=%v err=%v", history, err)
	}
	if _, err := service.Refund(ctx, credits.RefundRequest{UserID: "other", ProjectID: "project", GenerationJobID: "job", Amount: 0.01}); err == nil || store.refunds != 0 {
		t.Fatal("foreign refund reached the ledger writer")
	}
	if _, err := service.Debit(ctx, credits.DebitRequest{UserID: "owner", ProjectID: "project", GenerationJobID: "job", Amount: 1}); err == nil {
		t.Fatal("changed debit amount was accepted")
	}
	if _, err := service.Refund(ctx, credits.RefundRequest{UserID: "owner", ProjectID: "project", GenerationJobID: "job", Amount: 0.01}); err != nil || store.refunds != 1 {
		t.Fatalf("refund failed: %v", err)
	}
}
