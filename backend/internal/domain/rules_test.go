package domain_test

import (
	"testing"
	"time"

	"icomic/backend/internal/domain"
)

func TestHashUserID(t *testing.T) {
	uid := "123e4567-e89b-12d3-a456-426614174000"
	hash1 := domain.HashUserID(uid)
	hash2 := domain.HashUserID(uid)

	if hash1 == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash1 != hash2 {
		t.Fatal("expected deterministic hash")
	}
	if len(hash1) != 64 {
		t.Fatalf("expected sha-256 hex 64 characters, got %d", len(hash1))
	}
}

func TestValidateScript(t *testing.T) {
	_, err := domain.ValidateScript("   \n\t")
	if err == nil {
		t.Fatal("expected error on empty script")
	}

	count, err := domain.ValidateScript("The hero arrives in the misty valley.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 7 {
		t.Fatalf("expected 7 words, got %d", count)
	}
}

func TestCheckApprovalGate(t *testing.T) {
	tests := []struct {
		name    string
		stage   string
		prereqs domain.ApprovedPrereqs
		wantErr bool
	}{
		{
			name:    "character reference without script",
			stage:   "character_reference",
			prereqs: domain.ApprovedPrereqs{HasApprovedScript: false},
			wantErr: true,
		},
		{
			name:    "character reference with script",
			stage:   "character_reference",
			prereqs: domain.ApprovedPrereqs{HasApprovedScript: true},
			wantErr: false,
		},
		{
			name:    "draft sketch without characters",
			stage:   "draft_sketch",
			prereqs: domain.ApprovedPrereqs{AllCharactersApproved: false, ChapterOutlineApproved: true},
			wantErr: true,
		},
		{
			name:    "draft sketch without outline",
			stage:   "draft_sketch",
			prereqs: domain.ApprovedPrereqs{AllCharactersApproved: true, ChapterOutlineApproved: false},
			wantErr: true,
		},
		{
			name:    "draft sketch with all prereqs",
			stage:   "draft_sketch",
			prereqs: domain.ApprovedPrereqs{AllCharactersApproved: true, ChapterOutlineApproved: true},
			wantErr: false,
		},
		{
			name:    "line art without draft approval",
			stage:   "final_line_art",
			prereqs: domain.ApprovedPrereqs{DraftSketchApproved: false},
			wantErr: true,
		},
		{
			name:    "line art with draft approval",
			stage:   "final_line_art",
			prereqs: domain.ApprovedPrereqs{DraftSketchApproved: true},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.CheckApprovalGate(tt.stage, tt.prereqs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckApprovalGate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckKillSwitchAndQuota(t *testing.T) {
	if err := domain.CheckKillSwitchAndQuota(true, 10.0, 50.0); err == nil {
		t.Fatal("expected error when kill switch active")
	}

	if err := domain.CheckKillSwitchAndQuota(false, 55.0, 50.0); err == nil {
		t.Fatal("expected error when quota exceeded")
	}

	if err := domain.CheckKillSwitchAndQuota(false, 10.0, 50.0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCalculateAge(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	dob18 := time.Date(2008, 10, 15, 0, 0, 0, 0, time.UTC)
	dob17 := time.Date(2009, 1, 1, 0, 0, 0, 0, time.UTC)

	if age := domain.CalculateAge(dob18, now); age < 18 {
		t.Fatalf("expected age >= 18, got %d", age)
	}
	if age := domain.CalculateAge(dob17, now); age >= 18 {
		t.Fatalf("expected age < 18, got %d", age)
	}
}
