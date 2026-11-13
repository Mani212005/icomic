package api

import (
	"time"

	"icomic/backend/internal/domain"
)

// mergeVisualAttributes applies a patch to a character's stored attributes without mutating either map.
// Keys the patch does not name keep their value; a nil value deletes the key.
func mergeVisualAttributes(stored, patch map[string]interface{}) map[string]interface{} {
	if patch == nil {
		return stored
	}
	merged := make(map[string]interface{}, len(stored)+len(patch))
	for key, value := range stored {
		merged[key] = value
	}
	for key, value := range patch {
		if value == nil {
			delete(merged, key)
			continue
		}
		merged[key] = value
	}
	return merged
}

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
