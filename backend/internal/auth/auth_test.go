package auth_test

import (
	"testing"
	"time"

	"icomic/backend/internal/auth"
)

func TestAgeVerifier(t *testing.T) {
	verifier := auth.NewDefaultAgeVerifier(18)
	verifier.Now = func() time.Time {
		return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	}

	validDOB := time.Date(2005, 5, 20, 0, 0, 0, 0, time.UTC)
	ok, err := verifier.VerifyAge(validDOB)
	if err != nil || !ok {
		t.Fatalf("expected valid age, got ok=%v, err=%v", ok, err)
	}

	underageDOB := time.Date(2010, 5, 20, 0, 0, 0, 0, time.UTC)
	ok, err = verifier.VerifyAge(underageDOB)
	if err == nil || ok {
		t.Fatalf("expected under 18 to be rejected, got ok=%v, err=%v", ok, err)
	}
}

func TestGeoBlocker(t *testing.T) {
	blocker := auth.NewConfigurableGeoBlocker([]string{"IN", "KP"})

	if !blocker.IsBlocked("IN") {
		t.Fatal("expected India (IN) to be blocked")
	}
	if !blocker.IsBlocked("in") {
		t.Fatal("expected lower-case 'in' to be blocked")
	}
	if !blocker.IsBlocked("KP") {
		t.Fatal("expected North Korea (KP) to be blocked")
	}
	if blocker.IsBlocked("US") {
		t.Fatal("expected United States (US) not to be blocked")
	}
}
