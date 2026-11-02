package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrEmptyScript                 = errors.New("script cannot be empty")
	ErrKillSwitchActive            = errors.New("global image generation kill switch is active")
	ErrQuotaExceeded               = errors.New("user image generation quota exceeded")
	ErrMissingPrerequisiteApproval = errors.New("prerequisite stage approvals missing")
	ErrActiveJobExists             = errors.New("an active generation job already exists for this entity")
	ErrStaleApproval               = errors.New("approval is stale due to subsequent edit or regeneration")
)

// HashUserID computes the SHA-256 hex digest of the internal user UUID.
// Required by Meta API terms (SPEC 11.3).
func HashUserID(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])
}

// ValidateScript validates user script text and returns the word count.
func ValidateScript(script string) (int, error) {
	trimmed := strings.TrimSpace(script)
	if trimmed == "" {
		return 0, ErrEmptyScript
	}
	words := strings.Fields(trimmed)
	return len(words), nil
}

// ApprovedPrereqs holds approval flags required before image generation stages.
type ApprovedPrereqs struct {
	HasScript              bool
	AllCharactersApproved  bool
	ChapterOutlineApproved bool
	DraftSketchApproved    bool
	LineArtApproved        bool
}

// CheckApprovalGate verifies that prerequisite approvals are recorded before
// permitting a generation job to be created (SPEC 5.3, ARCHITECTURE 5.3).
func CheckApprovalGate(stage string, prereqs ApprovedPrereqs) error {
	switch stage {
	case "character_reference":
		if !prereqs.HasScript {
			return fmt.Errorf("%w: script must be submitted and validated", ErrMissingPrerequisiteApproval)
		}
		return nil

	case "draft_sketch":
		if !prereqs.AllCharactersApproved {
			return fmt.Errorf("%w: all character references must be approved before generating draft sketch", ErrMissingPrerequisiteApproval)
		}
		if !prereqs.ChapterOutlineApproved {
			return fmt.Errorf("%w: chapter outline must be approved before generating draft sketch", ErrMissingPrerequisiteApproval)
		}
		return nil

	case "final_line_art":
		if !prereqs.DraftSketchApproved {
			return fmt.Errorf("%w: draft sketch must be approved before generating final line art", ErrMissingPrerequisiteApproval)
		}
		return nil

	case "colour_suggestion":
		if !prereqs.LineArtApproved {
			return fmt.Errorf("%w: final line art must be approved before colour palette suggestion", ErrMissingPrerequisiteApproval)
		}
		return nil

	default:
		return nil
	}
}

// IsApprovalStale checks if an approval occurred before the entity's latest update or generation (SPEC 3.2, 3.5, 3.6, 3.7).
func IsApprovalStale(approvedAt *time.Time, latestActivity time.Time) bool {
	if approvedAt == nil {
		return true
	}
	// An approval only counts if its approved_at is newer than the latest completed generation job or edit.
	return approvedAt.Before(latestActivity)
}

// CheckKillSwitchAndQuota enforces global spend protection and per-user quota (SPEC 5.5).
func CheckKillSwitchAndQuota(killSwitchEnabled bool, currentDailyUsage, maxDailyQuota float64, currentMonthlyUsage, maxMonthlyQuota float64) error {
	if killSwitchEnabled {
		return ErrKillSwitchActive
	}
	if maxDailyQuota > 0 && currentDailyUsage >= maxDailyQuota {
		return fmt.Errorf("%w: daily image limit reached", ErrQuotaExceeded)
	}
	if maxMonthlyQuota > 0 && currentMonthlyUsage >= maxMonthlyQuota {
		return fmt.Errorf("%w: monthly image limit reached", ErrQuotaExceeded)
	}
	return nil
}
