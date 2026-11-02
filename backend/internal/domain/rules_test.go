package domain_test

import (
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

	// Nil approval -> stale
	if !domain.IsApprovalStale(nil, editTime) {
		t.Fatal("expected nil approval to be stale")
	}
}

func TestCheckKillSwitchAndQuota(t *testing.T) {
	// Kill switch active
	if err := domain.CheckKillSwitchAndQuota(true, 5, 20, 50, 500); err != domain.ErrKillSwitchActive {
		t.Fatalf("expected ErrKillSwitchActive, got %v", err)
	}

	// Daily quota exceeded
	if err := domain.CheckKillSwitchAndQuota(false, 20, 20, 50, 500); err == nil {
		t.Fatal("expected daily quota error")
	}

	// Monthly quota exceeded
	if err := domain.CheckKillSwitchAndQuota(false, 5, 20, 500, 500); err == nil {
		t.Fatal("expected monthly quota error")
	}

	// Under quota
	if err := domain.CheckKillSwitchAndQuota(false, 5, 20, 50, 500); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
