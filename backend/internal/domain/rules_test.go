package domain_test

import (
	"errors"
	"testing"
	"time"

	"icomic/backend/internal/domain"
)

func TestHashUserID(t *testing.T) {
	uid := "123e4567-e89b-12d3-a456-426614174000"
	hash := domain.HashUserID(uid)
	if len(hash) != 64 {
		t.Fatalf("expected 64 char hex hash, got %d", len(hash))
	}
	// Verify deterministic
	if hash != domain.HashUserID(uid) {
		t.Fatal("expected identical hash for identical input")
	}
}

func TestValidateScript(t *testing.T) {
	_, err := domain.ValidateScript("   ")
	if err != domain.ErrEmptyScript {
		t.Fatalf("expected ErrEmptyScript, got %v", err)
	}

	wc, err := domain.ValidateScript("Once upon a time in a galaxy far away.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wc != 9 {
		t.Fatalf("expected 9 words, got %d", wc)
	}
}

func TestCheckApprovalGate(t *testing.T) {
	// Character reference requires script
	if err := domain.CheckApprovalGate("character_reference", domain.ApprovedPrereqs{HasScript: false}); err == nil {
		t.Fatal("expected error when script is missing")
	}
	if err := domain.CheckApprovalGate("character_reference", domain.ApprovedPrereqs{HasScript: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Draft sketch requires all characters approved and chapter outline approved
	if err := domain.CheckApprovalGate("draft_sketch", domain.ApprovedPrereqs{AllCharactersApproved: false, ChapterOutlineApproved: true}); err == nil {
		t.Fatal("expected error when characters not approved")
	}
	if err := domain.CheckApprovalGate("draft_sketch", domain.ApprovedPrereqs{AllCharactersApproved: true, ChapterOutlineApproved: false}); err == nil {
		t.Fatal("expected error when outline not approved")
	}
	if err := domain.CheckApprovalGate("draft_sketch", domain.ApprovedPrereqs{AllCharactersApproved: true, ChapterOutlineApproved: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Final line art requires draft sketch approved
	if err := domain.CheckApprovalGate("final_line_art", domain.ApprovedPrereqs{DraftSketchApproved: false}); err == nil {
		t.Fatal("expected error when draft not approved")
	}
	if err := domain.CheckApprovalGate("final_line_art", domain.ApprovedPrereqs{DraftSketchApproved: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Colour suggestion requires line art approved
	if err := domain.CheckApprovalGate("colour_suggestion", domain.ApprovedPrereqs{LineArtApproved: false}); err == nil {
		t.Fatal("expected error when line art not approved")
	}
	if err := domain.CheckApprovalGate("colour_suggestion", domain.ApprovedPrereqs{LineArtApproved: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsApprovalStale(t *testing.T) {
	now := time.Now()
	approvalTime := now.Add(-10 * time.Minute)
	editTime := now.Add(-5 * time.Minute)

	// Approval was before edit -> stale!
	if !domain.IsApprovalStale(&approvalTime, editTime) {
		t.Fatal("expected approval to be stale")
	}

	// Approval was after edit -> fresh!
	freshApprovalTime := now
	if domain.IsApprovalStale(&freshApprovalTime, editTime) {
		t.Fatal("expected approval not to be stale")
	}

	// Approval must be strictly newer than activity to be fresh.
	sameTime := now
	if !domain.IsApprovalStale(&sameTime, sameTime) {
		t.Fatal("expected approval at the same timestamp as activity to be stale")
	}

	// Nil approval -> stale
	if !domain.IsApprovalStale(nil, editTime) {
		t.Fatal("expected nil approval to be stale")
	}
}

func TestCascadeChapterOutlineApproval(t *testing.T) {
	now := time.Now()

	// 1. Missing approval
	err := domain.CheckChapterOutlineApproval(nil, now)
	if !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("expected ErrMissingPrerequisiteApproval, got: %v", err)
	}

	// 2. Approved after chapter edit -> fresh, passes
	approvedAt := now.Add(2 * time.Minute)
	editedAt := now
	if err := domain.CheckChapterOutlineApproval(&approvedAt, editedAt); err != nil {
		t.Fatalf("expected fresh approval to pass, got: %v", err)
	}

	// 3. Chapter edited after approval -> stale cascade!
	chapterEditedLater := now.Add(5 * time.Minute)
	err = domain.CheckChapterOutlineApproval(&approvedAt, chapterEditedLater)
	if !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("expected ErrStaleApproval when chapter edited after outline approval, got: %v", err)
	}
}

func TestCascadeCharacterApproval(t *testing.T) {
	now := time.Now()

	// Missing approval
	if err := domain.CheckCharacterApproval(nil, now); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("expected ErrMissingPrerequisiteApproval, got: %v", err)
	}

	// Fresh approval
	approvedAt := now.Add(1 * time.Minute)
	if err := domain.CheckCharacterApproval(&approvedAt, now); err != nil {
		t.Fatalf("expected fresh character approval to pass, got: %v", err)
	}

	// Character edited after approval -> stale
	charEdited := now.Add(3 * time.Minute)
	if err := domain.CheckCharacterApproval(&approvedAt, charEdited); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("expected ErrStaleApproval when character modified, got: %v", err)
	}
}

func TestCascadePanelEditStalesDraftApproval(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(5 * time.Minute)

	page := &domain.Page{
		ID:              "page-1",
		ChapterID:       "chap-1",
		DraftApprovedAt: &t0,
		UpdatedAt:       t0,
	}

	// Before edit: draft approval is fresh
	if err := domain.CheckDraftApproval(page.DraftApprovedAt, page.UpdatedAt.Add(-1*time.Minute)); err != nil {
		t.Fatalf("expected fresh draft approval, got: %v", err)
	}

	// User edits a panel at t1 -> trigger panel edit cascade
	domain.StaleDraftOnPanelEdit(page, t1)

	// Page DraftApprovedAt is cleared
	if page.DraftApprovedAt != nil {
		t.Fatalf("expected DraftApprovedAt to be nil, got: %v", page.DraftApprovedAt)
	}
	if !page.UpdatedAt.Equal(t1) {
		t.Fatalf("expected UpdatedAt to match edit time %v, got: %v", t1, page.UpdatedAt)
	}

	// Downstream draft approval check fails
	if err := domain.CheckDraftApproval(page.DraftApprovedAt, page.UpdatedAt); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("expected ErrMissingPrerequisiteApproval after panel edit, got: %v", err)
	}
}

func TestCascadeDraftRegenerationStalesLineArtApproval(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(5 * time.Minute)
	t2 := t1.Add(5 * time.Minute)

	page := &domain.Page{
		ID:                "page-1",
		ChapterID:         "chap-1",
		DraftApprovedAt:   &t0,
		LineArtApprovedAt: &t1,
		UpdatedAt:         t1,
	}

	// Draft regeneration occurs at t2
	domain.StaleOnDraftRegeneration(page, t2)

	// Both draft and line art approvals are invalidated
	if page.DraftApprovedAt != nil {
		t.Fatalf("expected DraftApprovedAt to be nil after draft regeneration")
	}
	if page.LineArtApprovedAt != nil {
		t.Fatalf("expected LineArtApprovedAt to be nil after draft regeneration")
	}
	if !page.UpdatedAt.Equal(t2) {
		t.Fatalf("expected page UpdatedAt to be updated to regeneration time %v, got: %v", t2, page.UpdatedAt)
	}

	// Check line art approval check
	if err := domain.CheckLineArtApproval(page.LineArtApprovedAt, page.UpdatedAt); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("expected ErrMissingPrerequisiteApproval for line art after draft regeneration, got: %v", err)
	}

	// Check draft approval check
	if err := domain.CheckDraftApproval(page.DraftApprovedAt, page.UpdatedAt); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("expected ErrMissingPrerequisiteApproval for draft after draft regeneration, got: %v", err)
	}
}

func TestCascadeLineArtRegenerationStalesApproval(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(5 * time.Minute)

	page := &domain.Page{
		ID:                "page-1",
		ChapterID:         "chap-1",
		DraftApprovedAt:   &t0,
		LineArtApprovedAt: &t0,
		UpdatedAt:         t0,
	}

	// Line art regeneration occurs at t1
	domain.StaleOnLineArtRegeneration(page, t1)

	// Line art approval is invalidated
	if page.LineArtApprovedAt != nil {
		t.Fatalf("expected LineArtApprovedAt to be nil after line art regeneration")
	}
	if !page.UpdatedAt.Equal(t1) {
		t.Fatalf("expected UpdatedAt %v, got %v", t1, page.UpdatedAt)
	}

	// Check line art approval check fails
	if err := domain.CheckLineArtApproval(page.LineArtApprovedAt, page.UpdatedAt); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("expected ErrMissingPrerequisiteApproval, got: %v", err)
	}
}

func TestKillSwitchProvider(t *testing.T) {
	// 1. Initial state
	provider := domain.NewInMemoryKillSwitchProvider(false, 50.0)
	if provider.IsActive() {
		t.Fatal("expected kill switch to be initially inactive")
	}
	if provider.SpendThreshold() != 50.0 {
		t.Fatalf("expected threshold 50.0, got %f", provider.SpendThreshold())
	}

	// 2. SetActive toggles runtime state
	provider.SetActive(true)
	if !provider.IsActive() {
		t.Fatal("expected kill switch to be active after SetActive(true)")
	}
	provider.SetActive(false)
	if provider.IsActive() {
		t.Fatal("expected kill switch to be inactive after SetActive(false)")
	}

	// 3. Spend below threshold does not trip
	if provider.CheckSpend(49.99) {
		t.Fatal("expected kill switch to remain inactive below spend threshold")
	}
	if provider.IsActive() {
		t.Fatal("expected provider to report inactive")
	}

	// 4. Spend reaching threshold trips the kill switch
	if !provider.CheckSpend(50.0) {
		t.Fatal("expected kill switch to trip at threshold")
	}
	if !provider.IsActive() {
		t.Fatal("expected provider to report active")
	}

	// 5. Update threshold at runtime (admin seam)
	provider.SetActive(false)
	provider.SetSpendThreshold(100.0)
	if provider.SpendThreshold() != 100.0 {
		t.Fatalf("expected threshold 100.0, got %f", provider.SpendThreshold())
	}
	if provider.CheckSpend(75.0) {
		t.Fatal("expected spend 75.0 not to trip new 100.0 threshold")
	}
	if !provider.CheckSpend(105.0) {
		t.Fatal("expected spend 105.0 to trip 100.0 threshold")
	}
}

func TestQuotaBoundaries(t *testing.T) {
	maxDaily := 20.0
	maxMonthly := 100.0

	// 1. Strictly below daily quota
	if err := domain.CheckKillSwitchAndQuota(false, 19.0, maxDaily, 50.0, maxMonthly); err != nil {
		t.Fatalf("expected usage under daily quota to pass, got: %v", err)
	}
	if err := domain.CheckKillSwitchAndQuota(false, 19.99, maxDaily, 50.0, maxMonthly); err != nil {
		t.Fatalf("expected usage 19.99 under 20.0 to pass, got: %v", err)
	}

	// 2. Exact daily quota boundary -> must reject
	errDailyExact := domain.CheckKillSwitchAndQuota(false, 20.0, maxDaily, 50.0, maxMonthly)
	if !errors.Is(errDailyExact, domain.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded at exact daily quota limit, got: %v", errDailyExact)
	}

	// 3. Above daily quota boundary -> must reject
	errDailyOver := domain.CheckKillSwitchAndQuota(false, 21.0, maxDaily, 50.0, maxMonthly)
	if !errors.Is(errDailyOver, domain.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded above daily quota limit, got: %v", errDailyOver)
	}

	// 4. Unlimited daily quota (maxDaily = 0)
	if err := domain.CheckKillSwitchAndQuota(false, 5000.0, 0, 50.0, maxMonthly); err != nil {
		t.Fatalf("expected unlimited daily quota (0) to pass, got: %v", err)
	}

	// 5. Strictly below monthly quota
	if err := domain.CheckKillSwitchAndQuota(false, 5.0, maxDaily, 99.0, maxMonthly); err != nil {
		t.Fatalf("expected usage under monthly quota to pass, got: %v", err)
	}
	if err := domain.CheckKillSwitchAndQuota(false, 5.0, maxDaily, 99.99, maxMonthly); err != nil {
		t.Fatalf("expected usage 99.99 under 100.0 to pass, got: %v", err)
	}

	// 6. Exact monthly quota boundary -> must reject
	errMonthlyExact := domain.CheckKillSwitchAndQuota(false, 5.0, maxDaily, 100.0, maxMonthly)
	if !errors.Is(errMonthlyExact, domain.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded at exact monthly quota limit, got: %v", errMonthlyExact)
	}

	// 7. Above monthly quota boundary -> must reject
	errMonthlyOver := domain.CheckKillSwitchAndQuota(false, 5.0, maxDaily, 101.0, maxMonthly)
	if !errors.Is(errMonthlyOver, domain.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded above monthly quota limit, got: %v", errMonthlyOver)
	}

	// 8. Unlimited monthly quota (maxMonthly = 0)
	if err := domain.CheckKillSwitchAndQuota(false, 5.0, maxDaily, 50000.0, 0); err != nil {
		t.Fatalf("expected unlimited monthly quota (0) to pass, got: %v", err)
	}

	// 9. Kill switch active with usage 0 -> must reject with ErrKillSwitchActive
	if err := domain.CheckKillSwitchAndQuota(true, 0, maxDaily, 0, maxMonthly); !errors.Is(err, domain.ErrKillSwitchActive) {
		t.Fatalf("expected ErrKillSwitchActive, got: %v", err)
	}

	// 10. Kill switch active while over quota -> ErrKillSwitchActive takes priority
	if err := domain.CheckKillSwitchAndQuota(true, 25.0, maxDaily, 150.0, maxMonthly); !errors.Is(err, domain.ErrKillSwitchActive) {
		t.Fatalf("expected ErrKillSwitchActive to take priority over quota, got: %v", err)
	}
}
