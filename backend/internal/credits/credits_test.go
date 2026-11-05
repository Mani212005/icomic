package credits_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"icomic/backend/internal/credits"
	"icomic/backend/internal/domain"
)

func TestPricingConfigDrivesEstimateAndStageMetadata(t *testing.T) {
	pricing, err := credits.PricingFromEnv(func(key string) string {
		switch key {
		case "META_IMAGE_MODEL":
			return "muse-image-test"
		case "META_IMAGE_PRICE_USD":
			return "0.0275"
		case "META_VISION_MODEL":
			return "meta-vision-test"
		case "META_COLOUR_ESTIMATE_USD":
			return "0.003"
		case "PRICING_VERSION":
			return "test-prices-2"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	image := pricing.Estimate("draft_sketch", 3)
	if image.EstimatedCost != 0.0825 || image.ModelID != "muse-image-test" || image.PricingVersion != "test-prices-2" {
		t.Fatalf("configured image price/model/version not reflected in quote: %+v", image)
	}
	colour := pricing.Estimate("colour_suggestion", 2)
	if colour.EstimatedCost != 0.006 || colour.ModelID != "meta-vision-test" {
		t.Fatalf("configured colour price/model not reflected in quote: %+v", colour)
	}
}

func TestDefaultPricingIsUnverifiedAndConfigurationRejectsInvalidValues(t *testing.T) {
	if quote := credits.DefaultPricing().Estimate("draft_sketch", 1); quote.PriceVerified {
		t.Fatalf("default pricing must not claim verified provider or retail pricing: %+v", quote)
	}
	for _, value := range []string{"0", "-0.01", "NaN", "not-a-price"} {
		t.Run(value, func(t *testing.T) {
			_, err := credits.PricingFromEnv(func(key string) string {
				if key == "META_IMAGE_PRICE_USD" {
					return value
				}
				return ""
			})
			if err == nil {
				t.Fatalf("expected invalid price %q to be rejected", value)
			}
		})
	}
}

func TestPricingVersionChangesWhenPricingChangesWithoutExplicitVersion(t *testing.T) {
	getenv := func(price string) func(string) string {
		return func(key string) string {
			if key == "META_IMAGE_PRICE_USD" {
				return price
			}
			return ""
		}
	}
	first, err := credits.PricingFromEnv(getenv("0.02"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := credits.PricingFromEnv(getenv("0.03"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Version == second.Version || first.Version == "default" || second.Version == "default" {
		t.Fatalf("pricing version did not identify changed configuration: first=%q second=%q", first.Version, second.Version)
	}
}

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

	if svc.TotalPlatformSpend() != 0.01 {
		t.Fatalf("expected platform spend 0.01, got %f", svc.TotalPlatformSpend())
	}

	// Insufficient balance
	_, err = svc.Debit(ctx, credits.DebitRequest{
		UserID: "user-1",
		Amount: 100.0,
	})
	if err != credits.ErrInsufficientCredits {
		t.Fatalf("expected ErrInsufficientCredits, got %v", err)
	}

	// Negative amount rejected
	_, err = svc.Debit(ctx, credits.DebitRequest{
		UserID: "user-1",
		Amount: -5.0,
	})
	if !errors.Is(err, credits.ErrNegativeAmount) {
		t.Fatalf("expected ErrNegativeAmount, got %v", err)
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

	if svc.TotalPlatformSpend() != 0.0 {
		t.Fatalf("expected platform spend reset to 0.0, got %f", svc.TotalPlatformSpend())
	}

	// Usage history
	history, err := svc.GetUsageHistory(ctx, "user-1")
	if err != nil || len(history) != 2 {
		t.Fatalf("expected 2 usage entries, got %d", len(history))
	}

	// Daily spend tracking
	dailySpend, err := svc.GetUserDailySpend(ctx, "user-1", time.Now().UTC().Add(-1*time.Hour))
	if err != nil || dailySpend != 0.01 {
		t.Fatalf("expected daily spend 0.01, got %f (err: %v)", dailySpend, err)
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

	colourEst := credits.EstimateCost("colour_suggestion", 1)
	if colourEst.EstimatedCost != 0.005 || !colourEst.IsEstimatePrice {
		t.Fatalf("unexpected colour estimate: %+v", colourEst)
	}
}

func TestKillSwitchIntegrationWithCreditService(t *testing.T) {
	ctx := context.Background()
	svc := credits.NewInMemoryCreditService(map[string]float64{
		"user-1": 100.0,
	})

	// Threshold of 0.02
	ksProvider := credits.NewInMemoryKillSwitchProvider(false, 0.02)
	svc.SetKillSwitchProvider(ksProvider)

	if svc.KillSwitchProvider() != ksProvider {
		t.Fatal("expected KillSwitchProvider to match injected provider")
	}

	// First debit: $0.01 -> below threshold
	_, err := svc.Debit(ctx, credits.DebitRequest{
		UserID:          "user-1",
		ProjectID:       "proj-1",
		GenerationJobID: "job-1",
		Amount:          0.01,
	})
	if err != nil {
		t.Fatalf("debit failed: %v", err)
	}
	if ksProvider.IsActive() {
		t.Fatal("kill switch should NOT be active at $0.01 spend with $0.02 threshold")
	}

	// Second debit: $0.01 -> reaches threshold ($0.02) -> trips kill switch!
	_, err = svc.Debit(ctx, credits.DebitRequest{
		UserID:          "user-1",
		ProjectID:       "proj-1",
		GenerationJobID: "job-2",
		Amount:          0.01,
	})
	if err != nil {
		t.Fatalf("debit failed: %v", err)
	}
	if !ksProvider.IsActive() {
		t.Fatal("kill switch SHOULD be active after total platform spend reached $0.02")
	}
}

func TestStandardQuotaEnforcer(t *testing.T) {
	ctx := context.Background()
	ks := credits.NewInMemoryKillSwitchProvider(false, 0)
	enforcer := credits.NewStandardQuotaEnforcer(ks, credits.UserQuotaConfig{
		MaxDailyQuota:   20.0,
		MaxMonthlyQuota: 200.0,
	})

	// 1. Normal usage
	if err := enforcer.CheckQuota(ctx, "user-1", 5.0, 50.0); err != nil {
		t.Fatalf("expected quota check to pass, got: %v", err)
	}

	// 2. Daily limit reached
	err := enforcer.CheckQuota(ctx, "user-1", 20.0, 50.0)
	if !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded on daily limit, got: %v", err)
	}

	// 3. Monthly limit reached
	err = enforcer.CheckQuota(ctx, "user-1", 10.0, 200.0)
	if !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded on monthly limit, got: %v", err)
	}

	// 4. Kill switch tripped
	ks.SetActive(true)
	err = enforcer.CheckQuota(ctx, "user-1", 1.0, 1.0)
	if !errors.Is(err, domain.ErrKillSwitchActive) {
		t.Fatalf("expected ErrKillSwitchActive when kill switch active, got: %v", err)
	}
}
