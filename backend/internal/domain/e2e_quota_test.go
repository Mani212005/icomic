package domain_test

import (
	"testing"

	"icomic/backend/internal/domain"
)

func TestE2EImageQuotaRequiresExplicitIsolatedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, flag, daily, monthly string
		wantError                  bool
	}{
		{"default", "", "", "", false},
		{"isolated", "1", "100", "1000", false},
		{"unscoped", "", "100", "1000", true},
		{"missing", "1", "", "", true},
		{"invalid", "1", "bad", "1000", true},
		{"zero", "1", "0", "1000", true},
		{"monthly-too-low", "1", "100", "99", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"ICOMIC_E2E_ISOLATED_STACK": tc.flag, "ICOMIC_E2E_DAILY_IMAGES": tc.daily, "ICOMIC_E2E_MONTHLY_IMAGES": tc.monthly}
			limits, err := domain.E2EImageQuota(func(key string) string { return env[key] })
			if (err != nil) != tc.wantError {
				t.Fatalf("limits=%v err=%v", limits, err)
			}
			if tc.name == "default" && limits != nil {
				t.Fatal("default deployment received an E2E override")
			}
			if tc.name == "isolated" && (limits.Daily != 100 || limits.Monthly != 1000) {
				t.Fatalf("unexpected E2E limits: %+v", limits)
			}
		})
	}
}
