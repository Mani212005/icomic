package db_test

import (
	"context"
	"testing"
	"time"

	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

func TestInMemoryRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	repo := db.NewInMemoryRepository()

	// 1. User
	user := &domain.User{
		Email:        "creator@example.com",
		PasswordHash: "hashed-secret",
	}
	if err := repo.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	fetchedUser, err := repo.GetUserByID(ctx, user.ID)
	if err != nil || fetchedUser.Email != user.Email {
		t.Fatalf("GetUserByID failed: %v, got %+v", err, fetchedUser)
	}

	byEmail, err := repo.GetUserByEmail(ctx, "creator@example.com")
	if err != nil || byEmail.ID != user.ID {
		t.Fatalf("GetUserByEmail failed: %v", err)
	}

	// Duplicate email should fail
	if err := repo.CreateUser(ctx, user); err != db.ErrDuplicateKey {
		t.Fatalf("expected ErrDuplicateKey, got %v", err)
	}

	// 2. Project
	proj := &domain.Project{
		UserID:         user.ID,
		Title:          "Space Adventures",
		OriginalScript: "Chapter 1: The Launch",
		ChapterCount:   1,
		ArtStyle:       "manga",
		Status:         "created",
	}
	if err := repo.CreateProject(ctx, proj); err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	fetchedProj, err := repo.GetProject(ctx, proj.ID)
	if err != nil || fetchedProj.Title != proj.Title {
		t.Fatalf("GetProject failed: %v", err)
	}

	projects, err := repo.ListProjects(ctx, user.ID)
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects failed: %v", err)
	}

	// 3. Chapter
	chap := &domain.Chapter{
		ProjectID:     proj.ID,
		ChapterNumber: 1,
		Title:         "The Launch",
		Summary:       "Rocket lifts off into orbit",
		Status:        "pending",
	}
	if err := repo.CreateChapter(ctx, chap); err != nil {
		t.Fatalf("CreateChapter failed: %v", err)
	}

	chapters, err := repo.ListChapters(ctx, proj.ID)
	if err != nil || len(chapters) != 1 {
		t.Fatalf("ListChapters failed: %v", err)
	}

	// 4. Character
	ch := &domain.Character{
		ProjectID:        proj.ID,
		Name:             "Astra",
		Description:      "Star pilot",
		VisualAttributes: map[string]interface{}{"suit": "silver"},
	}
	if err := repo.CreateCharacter(ctx, ch); err != nil {
		t.Fatalf("CreateCharacter failed: %v", err)
	}
	chars, err := repo.ListCharacters(ctx, proj.ID)
	if err != nil || len(chars) != 1 {
		t.Fatalf("ListCharacters failed: %v", err)
	}

	// 5. Panel & Page
	pan := &domain.Panel{
		ChapterID:        chap.ID,
		PanelNumber:      1,
		SceneDescription: "Cockpit view of astronaut preparing for flight",
		Dialogue:         "Ready for ignition.",
		ImagePrompt:      "Astronaut inside high-tech rocket cockpit",
		Status:           "pending",
	}
	if err := repo.CreatePanel(ctx, pan); err != nil {
		t.Fatalf("CreatePanel failed: %v", err)
	}
	panels, err := repo.ListPanels(ctx, chap.ID)
	if err != nil || len(panels) != 1 {
		t.Fatalf("ListPanels failed: %v", err)
	}

	page := &domain.Page{
		ChapterID:  chap.ID,
		PageNumber: 1,
		PanelIDs:   []string{pan.ID},
	}
	if err := repo.CreatePage(ctx, page); err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}
	pages, err := repo.ListPages(ctx, chap.ID)
	if err != nil || len(pages) != 1 {
		t.Fatalf("ListPages failed: %v", err)
	}

	// 6. Stage Approval
	approval := &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "chapter_outline",
		EntityID:                 chap.ID,
		ApprovedByUserID:         user.ID,
		CreditEstimateAtApproval: 0.01,
	}
	if err := repo.RecordApproval(ctx, approval); err != nil {
		t.Fatalf("RecordApproval failed: %v", err)
	}
	latestApp, err := repo.GetLatestApproval(ctx, "chapter_outline", chap.ID)
	if err != nil || latestApp.ID != approval.ID {
		t.Fatalf("GetLatestApproval failed: %v", err)
	}
}

func TestCreditTransactionsAndJobs(t *testing.T) {
	ctx := context.Background()
	repo := db.NewInMemoryRepository()

	user := &domain.User{
		Email: "user@example.com",
	}
	_ = repo.CreateUser(ctx, user)

	initialBal, err := repo.GetCreditBalance(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetCreditBalance failed: %v", err)
	}
	if initialBal != 100.0 {
		t.Fatalf("expected 100.0 initial balance, got %f", initialBal)
	}

	// Create job with debit
	job := &domain.GenerationJob{
		ProjectID:      "proj-1",
		UserID:         user.ID,
		JobType:        "draft_sketch",
		Stage:          "draft",
		ModelID:        "muse-image-1.0",
		Status:         "pending",
		IdempotencyKey: "idemp-001",
		EstimatedCost:  0.01,
		HashedUserID:   domain.HashUserID(user.ID),
	}

	if err := repo.CreateJobWithDebit(ctx, job, 0.01); err != nil {
		t.Fatalf("CreateJobWithDebit failed: %v", err)
	}

	// Verify balance decremented
	balAfterDebit, _ := repo.GetCreditBalance(ctx, user.ID)
	if balAfterDebit != 99.99 {
		t.Fatalf("expected 99.99, got %f", balAfterDebit)
	}

	// Idempotency check: duplicate key fails
	dupJob := &domain.GenerationJob{
		ProjectID:      "proj-1",
		UserID:         user.ID,
		JobType:        "draft_sketch",
		Stage:          "draft",
		IdempotencyKey: "idemp-001",
	}
	if err := repo.CreateJobWithDebit(ctx, dupJob, 0.01); err == nil {
		t.Fatal("expected duplicate key error")
	}

	// Test refund
	if err := repo.RecordRefund(ctx, job.ID, 0.01); err != nil {
		t.Fatalf("RecordRefund failed: %v", err)
	}
	balAfterRefund, _ := repo.GetCreditBalance(ctx, user.ID)
	if balAfterRefund != 100.0 {
		t.Fatalf("expected 100.0 after refund, got %f", balAfterRefund)
	}

	// Verify ledger entries
	ledger, err := repo.ListCreditLedger(ctx, user.ID)
	if err != nil || len(ledger) != 2 {
		t.Fatalf("expected 2 ledger entries (debit + refund), got %d: %v", len(ledger), err)
	}

	// Test daily image count
	since := time.Now().Add(-24 * time.Hour)
	count, err := repo.GetUserDailyImageCount(ctx, user.ID, since)
	if err != nil || count != 1 {
		t.Fatalf("expected 1 daily image count, got %d (err: %v)", count, err)
	}
}
