package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

// TestPostgresRepositoryIntegration runs integration tests against real PostgreSQL.
// It is skipped unless TEST_DATABASE_URL environment variable is provided.
// Never required in CI.
func TestPostgresRepositoryIntegration(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping live PostgreSQL integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo, err := db.NewPostgresRepository(dbURL)
	if err != nil {
		t.Fatalf("failed to connect to PostgreSQL: %v", err)
	}
	defer repo.Close()

	// 1. Run migrations idempotently
	migrationsDir := "../../../deploy/migrations"
	candidates := []string{
		"../../../deploy/migrations",
		"../../deploy/migrations",
		"deploy/migrations",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			migrationsDir = c
			break
		}
	}
	applied, err := repo.RunMigrations(ctx, migrationsDir)
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	t.Logf("Migrations applied: %v", applied)

	// Second run must be idempotent (0 newly applied)
	reapplied, err := repo.RunMigrations(ctx, migrationsDir)
	if err != nil {
		t.Fatalf("RunMigrations idempotent re-run failed: %v", err)
	}
	if len(reapplied) != 0 {
		t.Fatalf("expected 0 reapplied migrations on second run, got %d", len(reapplied))
	}

	// 2. Test CreateUser and initial credit ledger insertion (nullable generation_job_id and nullable project_id)
	userEmail := "test-" + uuid.NewString()[:8] + "@example.com"
	user := &domain.User{
		Email:        userEmail,
		PasswordHash: "hashed-pw",
	}
	if err := repo.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	// Duplicate email must return ErrDuplicateKey
	dupUser := &domain.User{
		Email:        userEmail,
		PasswordHash: "hashed-pw-2",
	}
	if err := repo.CreateUser(ctx, dupUser); err != db.ErrDuplicateKey {
		t.Fatalf("expected ErrDuplicateKey for duplicate email, got %v", err)
	}

	// Verify initial credit balance is 100.0
	bal, err := repo.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetCreditBalance failed: %v", err)
	}
	if bal != 100.0 {
		t.Fatalf("expected initial balance 100.0, got %f", bal)
	}

	// 3. Test Chapter creation, UpdatedAt scanning, and update
	proj := &domain.Project{
		UserID:         user.ID,
		Title:          "Integration Comic",
		OriginalScript: "Act 1",
		ChapterCount:   1,
		ArtStyle:       "classic-comic",
		Status:         "draft",
	}
	if err := repo.CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	chap := &domain.Chapter{
		ProjectID:     proj.ID,
		ChapterNumber: 1,
		Title:         "Prologue",
		Summary:       "In the beginning...",
		Status:        "pending",
	}
	if err := repo.CreateChapter(ctx, chap); err != nil {
		t.Fatalf("CreateChapter failed: %v", err)
	}
	if chap.UpdatedAt.IsZero() {
		t.Fatal("expected chap.UpdatedAt to be populated")
	}

	fetchedChap, err := repo.GetChapter(ctx, chap.ID)
	if err != nil {
		t.Fatalf("GetChapter failed: %v", err)
	}
	if fetchedChap.UpdatedAt.IsZero() {
		t.Fatal("expected fetchedChap.UpdatedAt to be scanned from DB")
	}

	time.Sleep(5 * time.Millisecond)
	fetchedChap.Title = "Prologue (Edited)"
	if err := repo.UpdateChapter(ctx, fetchedChap); err != nil {
		t.Fatalf("UpdateChapter failed: %v", err)
	}
	updatedChap, err := repo.GetChapter(ctx, chap.ID)
	if err != nil {
		t.Fatalf("GetChapter after update failed: %v", err)
	}
	if !updatedChap.UpdatedAt.After(chap.UpdatedAt) {
		t.Fatalf("expected updatedChap.UpdatedAt (%v) to be after initial (%v)", updatedChap.UpdatedAt, chap.UpdatedAt)
	}

	// 4. Test RecordAdjustment with nullable generation_job_id and empty projectID
	if err := repo.RecordAdjustment(ctx, user.ID, "", 25.0); err != nil {
		t.Fatalf("RecordAdjustment without projectID failed: %v", err)
	}
	balAfterAdj, err := repo.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetCreditBalance failed: %v", err)
	}
	if balAfterAdj != 125.0 {
		t.Fatalf("expected balance 125.0, got %f", balAfterAdj)
	}

	// Test RecordAdjustment with a valid projectID
	if err := repo.RecordAdjustment(ctx, user.ID, proj.ID, -10.0); err != nil {
		t.Fatalf("RecordAdjustment with projectID failed: %v", err)
	}
	balAfterAdj2, err := repo.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetCreditBalance failed: %v", err)
	}
	if balAfterAdj2 != 115.0 {
		t.Fatalf("expected balance 115.0, got %f", balAfterAdj2)
	}

	// 5. Test ListCreditLedger (verifies scanning of nullable project_id and nullable generation_job_id)
	ledger, err := repo.ListCreditLedger(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListCreditLedger failed: %v", err)
	}
	if len(ledger) < 3 {
		t.Fatalf("expected at least 3 ledger entries, got %d", len(ledger))
	}
}
