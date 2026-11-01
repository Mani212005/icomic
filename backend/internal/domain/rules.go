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
	ErrEmptyScript               = errors.New("script cannot be empty")
	ErrKillSwitchActive          = errors.New("global image generation kill switch is active")
	ErrQuotaExceeded             = errors.New("user image generation quota exceeded")
	ErrMissingPrerequisiteApproval = errors.New("prerequisite stage approvals missing")
	ErrActiveJobExists           = errors.New("an active generation job already exists for this entity")
)

// HashUserID computes the SHA-256 hex digest of the internal user UUID.
// Required by Meta API terms (SPEC 11.3).
func HashUserID(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])
}

// ValidateScript validates user script text and returns word count.
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
	HasApprovedScript        bool
	AllCharactersApproved    bool
	ChapterOutlineApproved   bool
	DraftSketchApproved      bool
	LineArtApproved          bool
}

// CheckApprovalGate verifies that prerequisite approvals are recorded before
// permitting a generation job to be created (SPEC 5.3).
func CheckApprovalGate(stage string, prereqs ApprovedPrereqs) error {
	switch stage {
	case "character_reference":
		if !prereqs.HasApprovedScript {
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

// CheckKillSwitchAndQuota enforces global spend protection and per-user quota (SPEC 5.5).
func CheckKillSwitchAndQuota(killSwitchEnabled bool, currentUsage, maxQuota float64) error {
	if killSwitchEnabled {
		return ErrKillSwitchActive
	}
	if maxQuota > 0 && currentUsage >= maxQuota {
		return ErrQuotaExceeded
	}
	return nil
}

// CalculateAge computes full completed years between date of birth and now.
func CalculateAge(dob time.Time, now time.Time) int {
	years := now.Year() - dob.Year()
	if now.YearDay() < dob.YearDay() {
		years--
	}
	return years
}
