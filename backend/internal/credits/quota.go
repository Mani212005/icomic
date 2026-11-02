package credits

import (
	"context"

	"icomic/backend/internal/domain"
)

// QuotaEnforcer evaluates per-user quota and global kill switch against configured limits (SPEC 5.5).
type QuotaEnforcer interface {
	CheckQuota(ctx context.Context, userID string, dailyUsage, monthlyUsage float64) error
}

// UserQuotaConfig holds daily and monthly image and spend limits (SPEC 5.5).
type UserQuotaConfig struct {
	MaxDailyQuota   float64
	MaxMonthlyQuota float64
}

// StandardQuotaEnforcer implements QuotaEnforcer using domain rules and an injected KillSwitchProvider.
type StandardQuotaEnforcer struct {
	killSwitch domain.KillSwitchProvider
	config     UserQuotaConfig
}

// NewStandardQuotaEnforcer creates a new quota enforcer.
func NewStandardQuotaEnforcer(killSwitch domain.KillSwitchProvider, cfg UserQuotaConfig) *StandardQuotaEnforcer {
	return &StandardQuotaEnforcer{
		killSwitch: killSwitch,
		config:     cfg,
	}
}

// CheckQuota validates current usage against limits and kill switch state.
func (e *StandardQuotaEnforcer) CheckQuota(_ context.Context, _ string, dailyUsage, monthlyUsage float64) error {
	killSwitchActive := false
	if e.killSwitch != nil {
		killSwitchActive = e.killSwitch.IsActive()
	}
	return domain.CheckKillSwitchAndQuota(
		killSwitchActive,
		dailyUsage,
		e.config.MaxDailyQuota,
		monthlyUsage,
		e.config.MaxMonthlyQuota,
	)
}
