package credits_test

import (
	"context"
	"testing"

	"icomic/backend/internal/credits"
)

func TestInMemoryCreditService(t *testing.T) {
	ctx := context.Background()
	svc := credits.NewInMemoryCreditService(map[string]float64{
		"user-1": 10.0,
	})

	bal, err := svc.GetBalance(ctx, "user-1")
	if err != nil || bal != 10.0 {
		t.Fatalf("expected 10.0, got %f (err: %v)", bal, err)
	}

	// Debit
	entry, err := svc.Debit(ctx, credits.DebitRequest{
		UserID:          "user-1",
		ProjectID:       "proj-1",
		GenerationJobID: "job-1",
		Amount:          0.01,
	})
	if err != nil || entry.BalanceAfter != 9.99 {
		t.Fatalf("unexpected debit result: %+v, err: %v", entry, err)
	}

	// Insufficient balance
	_, err = svc.Debit(ctx, credits.DebitRequest{
		UserID: "user-1",
		Amount: 100.0,
	})
	if err != credits.ErrInsufficientCredits {
		t.Fatalf("expected ErrInsufficientCredits, got %v", err)
	}

	// Refund
	refEntry, err := svc.Refund(ctx, credits.RefundRequest{
		UserID:          "user-1",
		ProjectID:       "proj-1",
		GenerationJobID: "job-1",
		Amount:          0.01,
	})
	if err != nil || refEntry.BalanceAfter != 10.0 {
		t.Fatalf("unexpected refund result: %+v, err: %v", refEntry, err)
	}

	// Usage history
	history, err := svc.GetUsageHistory(ctx, "user-1")
	if err != nil || len(history) != 2 {
		t.Fatalf("expected 2 usage entries, got %d", len(history))
	}
}

func TestCreditEstimation(t *testing.T) {
	draftEst := credits.EstimateCost("draft_sketch", 5)
	if draftEst.EstimatedCost != 0.05 || draftEst.ImageCount != 5 {
		t.Fatalf("unexpected draft estimate: %+v", draftEst)
	}

	lineEst := credits.EstimateCost("final_line_art", 3)
	if lineEst.EstimatedCost != 0.03 || lineEst.ImageCount != 3 {
		t.Fatalf("unexpected line art estimate: %+v", lineEst)
	}

	charEst := credits.EstimateCost("character_reference", 2)
	if charEst.EstimatedCost != 0.02 || charEst.ImageCount != 2 {
		t.Fatalf("unexpected character estimate: %+v", charEst)
	}
}
