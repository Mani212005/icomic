package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrEmptyScript                 = errors.New("script cannot be empty")
	ErrKillSwitchActive            = errors.New("global image generation kill switch is active")
	ErrQuotaExceeded               = errors.New("user image generation quota exceeded")
	ErrBookBudgetExceeded          = fmt.Errorf("%w: book image budget reached", ErrQuotaExceeded)
	ErrDailyImageLimitReached      = fmt.Errorf("%w: daily image limit reached", ErrQuotaExceeded)
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
// An approval only counts if its approved_at timestamp is newer than the latest completed generation job or edit.
func IsApprovalStale(approvedAt *time.Time, latestActivity time.Time) bool {
	if approvedAt == nil {
		return true
	}
	return !approvedAt.After(latestActivity)
}

// CheckChapterOutlineApproval verifies that a chapter outline has a valid, non-stale approval (SPEC 3.4, ARCHITECTURE 5.3).
func CheckChapterOutlineApproval(outlineApprovedAt *time.Time, chapterUpdatedAt time.Time) error {
	if outlineApprovedAt == nil {
		return fmt.Errorf("%w: chapter outline must be approved before generating draft sketch", ErrMissingPrerequisiteApproval)
	}
	if IsApprovalStale(outlineApprovedAt, chapterUpdatedAt) {
		return fmt.Errorf("%w: chapter outline was modified after approval", ErrStaleApproval)
	}
	return nil
}

// CheckCharacterApproval verifies that a character reference has a valid, non-stale approval (SPEC 3.2).
func CheckCharacterApproval(approvedAt *time.Time, characterUpdatedAt time.Time) error {
	if approvedAt == nil {
		return fmt.Errorf("%w: character reference must be approved", ErrMissingPrerequisiteApproval)
	}
	if IsApprovalStale(approvedAt, characterUpdatedAt) {
		return fmt.Errorf("%w: character reference was modified after approval", ErrStaleApproval)
	}
	return nil
}

// CheckDraftApproval verifies that a page draft sketch has a valid, non-stale approval (SPEC 3.6, ARCHITECTURE 5.3).
func CheckDraftApproval(draftApprovedAt *time.Time, pageUpdatedAt time.Time) error {
	if draftApprovedAt == nil {
		return fmt.Errorf("%w: draft sketch must be approved before generating final line art", ErrMissingPrerequisiteApproval)
	}
	if IsApprovalStale(draftApprovedAt, pageUpdatedAt) {
		return fmt.Errorf("%w: draft sketch or panel was modified after approval", ErrStaleApproval)
	}
	return nil
}

// CheckLineArtApproval verifies that a page line art has a valid, non-stale approval (SPEC 3.7, ARCHITECTURE 5.3).
func CheckLineArtApproval(lineArtApprovedAt *time.Time, pageUpdatedAt time.Time) error {
	if lineArtApprovedAt == nil {
		return fmt.Errorf("%w: final line art must be approved before colour palette suggestion", ErrMissingPrerequisiteApproval)
	}
	if IsApprovalStale(lineArtApprovedAt, pageUpdatedAt) {
		return fmt.Errorf("%w: line art was regenerated after approval", ErrStaleApproval)
	}
	return nil
}

// CheckPaintedPageApproval verifies that a page painted art has a valid, non-stale approval.
func CheckPaintedPageApproval(paintedPageApprovedAt *time.Time, pageUpdatedAt time.Time) error {
	if paintedPageApprovedAt == nil {
		return fmt.Errorf("%w: painted page has not been approved", ErrMissingPrerequisiteApproval)
	}
	if IsApprovalStale(paintedPageApprovedAt, pageUpdatedAt) {
		return fmt.Errorf("%w: painted page was regenerated after approval", ErrStaleApproval)
	}
	return nil
}

// StaleDraftOnPanelEdit invalidates the parent page's draft approval when any of its panels are edited (SPEC 3.5, 3.6).
func StaleDraftOnPanelEdit(page *Page, editTime time.Time) {
	page.DraftApprovedAt = nil
	page.LineArtApprovedAt = nil
	page.PaintedPageApprovedAt = nil
	page.UpdatedAt = editTime
}

// StaleOnDraftRegeneration invalidates downstream draft, line art and painted page approvals when a draft sketch is regenerated (SPEC 3.6, 3.7).
func StaleOnDraftRegeneration(page *Page, generationTime time.Time) {
	page.DraftApprovedAt = nil
	page.LineArtApprovedAt = nil
	page.PaintedPageApprovedAt = nil
	page.UpdatedAt = generationTime
}

// StaleOnLineArtRegeneration invalidates line art approval when line art is regenerated (SPEC 3.7).
func StaleOnLineArtRegeneration(page *Page, generationTime time.Time) {
	page.LineArtApprovedAt = nil
	page.UpdatedAt = generationTime
}

// StaleOnPaintedPageRegeneration invalidates painted page approval when painted page is regenerated.
func StaleOnPaintedPageRegeneration(page *Page, generationTime time.Time) {
	page.PaintedPageApprovedAt = nil
	page.UpdatedAt = generationTime
}

// KillSwitchProvider defines the admin seam to query and control the global kill switch at runtime (SPEC 5.5).
type KillSwitchProvider interface {
	// IsActive reports whether the global kill switch is currently active.
	IsActive() bool
	// SetActive enables or disables the kill switch at runtime.
	SetActive(active bool)
	// SpendThreshold returns the platform-wide spend threshold that trips the switch, or 0 if disabled.
	SpendThreshold() float64
	// SetSpendThreshold updates the spending limit threshold at runtime without redeployment (SPEC 5.5).
	SetSpendThreshold(threshold float64)
	// CheckSpend evaluates platform-wide spend against the threshold and trips the switch if reached.
	CheckSpend(totalSpend float64) bool
}

// InMemoryKillSwitchProvider is a thread-safe in-memory implementation of KillSwitchProvider (SPEC 5.5).
type InMemoryKillSwitchProvider struct {
	mu             sync.RWMutex
	active         bool
	spendThreshold float64
}

// NewInMemoryKillSwitchProvider creates an InMemoryKillSwitchProvider with initial status and budget threshold.
func NewInMemoryKillSwitchProvider(initialActive bool, initialThreshold float64) *InMemoryKillSwitchProvider {
	return &InMemoryKillSwitchProvider{
		active:         initialActive,
		spendThreshold: initialThreshold,
	}
}

// IsActive returns whether the kill switch is currently active.
func (p *InMemoryKillSwitchProvider) IsActive() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.active
}

// SetActive dynamically sets the kill switch state at runtime.
func (p *InMemoryKillSwitchProvider) SetActive(active bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = active
}

// SpendThreshold returns the configured budget threshold.
func (p *InMemoryKillSwitchProvider) SpendThreshold() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.spendThreshold
}

// SetSpendThreshold updates the spending threshold at runtime.
func (p *InMemoryKillSwitchProvider) SetSpendThreshold(threshold float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.spendThreshold = threshold
}

// CheckSpend compares total spend against the threshold and trips the switch if exceeded.
func (p *InMemoryKillSwitchProvider) CheckSpend(totalSpend float64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.spendThreshold > 0 && totalSpend >= p.spendThreshold {
		p.active = true
	}
	return p.active
}

// CheckKillSwitchAndQuota enforces global spend protection and per-user quota (SPEC 5.5).
func CheckKillSwitchAndQuota(killSwitchEnabled bool, currentDailyUsage, maxDailyQuota float64, currentMonthlyUsage, maxMonthlyQuota float64) error {
	if killSwitchEnabled {
		return ErrKillSwitchActive
	}
	if maxDailyQuota > 0 && currentDailyUsage >= maxDailyQuota {
		return ErrDailyImageLimitReached
	}
	if maxMonthlyQuota > 0 && currentMonthlyUsage >= maxMonthlyQuota {
		return fmt.Errorf("%w: monthly image limit reached", ErrQuotaExceeded)
	}
	return nil
}
