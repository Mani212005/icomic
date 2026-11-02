package credits

import (
	"icomic/backend/internal/domain"
)

// KillSwitchProvider is an alias to domain.KillSwitchProvider (SPEC 5.5).
type KillSwitchProvider = domain.KillSwitchProvider

// NewInMemoryKillSwitchProvider returns a new in-memory kill switch provider (SPEC 5.5).
func NewInMemoryKillSwitchProvider(initialActive bool, initialThreshold float64) domain.KillSwitchProvider {
	return domain.NewInMemoryKillSwitchProvider(initialActive, initialThreshold)
}
