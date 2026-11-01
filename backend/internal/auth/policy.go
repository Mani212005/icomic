package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"icomic/backend/internal/domain"
)

var (
	ErrUnderageUser = errors.New("user does not meet minimum age requirement (18+)")
	ErrGeoBlocked   = errors.New("service is unavailable in the specified region")
)

// AgeVerifier defines the pluggable policy interface for age gating.
// Open captain decision: specific verification provider/mechanism is swappable.
type AgeVerifier interface {
	VerifyAge(dob time.Time) (bool, error)
}

// DefaultAgeVerifier checks date of birth against a minimum age threshold.
type DefaultAgeVerifier struct {
	MinAge int
	Now    func() time.Time
}

func NewDefaultAgeVerifier(minAge int) *DefaultAgeVerifier {
	if minAge <= 0 {
		minAge = 18
	}
	return &DefaultAgeVerifier{
		MinAge: minAge,
		Now:    time.Now,
	}
}

func (v *DefaultAgeVerifier) VerifyAge(dob time.Time) (bool, error) {
	age := domain.CalculateAge(dob, v.Now())
	if age < v.MinAge {
		return false, ErrUnderageUser
	}
	return true, nil
}

// GeoBlocker defines the pluggable policy interface for country/IP geo-blocking.
// Open captain decision: IP-based geo-detection vs sign-up country check is swappable.
type GeoBlocker interface {
	IsBlocked(countryCode string) bool
}

// ConfigurableGeoBlocker checks country codes against a blocked list.
type ConfigurableGeoBlocker struct {
	BlockedCountries map[string]struct{}
}

func NewConfigurableGeoBlocker(blocked []string) *ConfigurableGeoBlocker {
	m := make(map[string]struct{}, len(blocked))
	for _, c := range blocked {
		m[strings.ToUpper(strings.TrimSpace(c))] = struct{}{}
	}
	return &ConfigurableGeoBlocker{BlockedCountries: m}
}

func (g *ConfigurableGeoBlocker) IsBlocked(countryCode string) bool {
	norm := strings.ToUpper(strings.TrimSpace(countryCode))
	_, blocked := g.BlockedCountries[norm]
	return blocked
}

type contextKey string

const userContextKey contextKey = "auth_user_id"

func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userContextKey, userID)
}

func UserIDFromContext(ctx context.Context) string {
	val, ok := ctx.Value(userContextKey).(string)
	if !ok {
		return ""
	}
	return val
}
