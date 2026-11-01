package credits_test

import (
	"context"
	"testing"

	"icomic/backend/internal/credits"
)

func TestCreditService(t *testing.T) {
	ctx := context.Background()
	svc := credits.NewInMemoryCreditService(map[string]float64{
		"user-1": 10.00,
	})

	bal, err := svc.GetBalance(ctx, "user-1")
	if err != nil || bal != 10.00 {
		t.Fatalf("expected 10.00, got %v, err %v", bal, err)
	}

	// Debit 2.50
	entry, err := svc.Debit(ctx, credits.DebitRequest{
		UserID:          "user-1",
		ProjectID:       "proj-1",
		GenerationJobID: "job-1",
		Amount:          2.50,
	})
	if err != nil {
		t.Fatalf("unexpected debit error: %v", err)
	}
	if entry.BalanceAfter != 7.50 {
		t.Fatalf("expected balance after 7.50, got %v", entry.BalanceAfter)
	}

	// Refund 1.00
	refEntry, err := svc.Refund(ctx, credits.RefundRequest{
		UserID:          "user-1",
		ProjectID:       "proj-1",
		GenerationJobID: "job-1",
		Amount:          1.00,
	})
	if err != nil {
		t.Fatalf("unexpected refund error: %v", err)
	}
	if refEntry.BalanceAfter != 8.50 {
		t.Fatalf("expected balance after 8.50, got %v", refEntry.BalanceAfter)
	}

	// Debit exceeding balance
	_, err = svc.Debit(ctx, credits.DebitRequest{
		UserID:          "user-1",
		ProjectID:       "proj-1",
		GenerationJobID: "job-2",
		Amount:          100.00,
	})
	if err == nil {
		t.Fatal("expected error for excessive debit")
	}

	history, err := svc.GetUsageHistory(ctx, "user-1")
	if err != nil || len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d, err %v", len(history), err)
	}
}
