package api

import (
	"time"

	"icomic/backend/internal/domain"
)

func nextCharacterActivity(character *domain.Character) time.Time {
	latest := character.UpdatedAt
	if character.ApprovedAt != nil && character.ApprovedAt.After(latest) {
		latest = *character.ApprovedAt
	}
	return timestampAfterActivity(latest)
}

func timestampAfterActivity(latest time.Time) time.Time {
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !now.After(latest) {
		return latest.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
	}
	return now
}
