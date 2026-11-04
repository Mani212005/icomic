//go:build realdb

package db_test

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

const migrationsDir = "../../../deploy/migrations"

func realDatabase(t *testing.T) (*sql.DB, *db.PostgresRepository, context.Context) {
	return realDatabaseWithMigrations(t, true)
}

func realDatabaseWithMigrations(t *testing.T, applyMigrations bool) (*sql.DB, *db.PostgresRepository, context.Context) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TEST_DATABASE_URL must point to a local PostgreSQL test database")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatal("realdb tests only permit a localhost PostgreSQL server")
	}
	admin, err := sql.Open("postgres", raw)
	if err != nil {
		t.Fatalf("open local PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("connect to local PostgreSQL: %v", err)
	}
	schema := "realdb_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	u.RawQuery = addSearchPath(u.Query(), schema)
	pool, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatalf("open schema-scoped PostgreSQL pool: %v", err)
	}
	pool.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = pool.Close() })
	if err := pool.PingContext(ctx); err != nil {
		t.Fatalf("connect to schema-scoped PostgreSQL: %v", err)
	}
	repo := db.NewPostgresRepositoryWithDB(pool)
	if applyMigrations {
		if _, err := repo.RunMigrations(ctx, migrationsDir); err != nil {
			t.Fatalf("apply migrations to empty schema: %v", err)
		}
	}
	return pool, repo, ctx
}

func addSearchPath(values url.Values, schema string) string {
	values.Set("search_path", schema+",public")
	return values.Encode()
}

func TestRealDBMigrationsApplyFromEmptyAndReverse(t *testing.T) {
	pool, _, ctx := realDatabaseWithMigrations(t, false)
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	var ups []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			ups = append(ups, entry.Name())
		}
	}
	sort.Strings(ups)
	var downs []string
	for _, up := range ups {
		content, err := os.ReadFile(filepath.Join(migrationsDir, up))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.ExecContext(ctx, string(content)); err != nil {
			t.Fatalf("apply %s from empty schema: %v", up, err)
		}
		down := strings.TrimSuffix(up, ".up.sql") + ".down.sql"
		if _, err := os.Stat(filepath.Join(migrationsDir, down)); err == nil {
			downs = append(downs, down)
		}
	}
	for i := len(downs) - 1; i >= 0; i-- {
		content, err := os.ReadFile(filepath.Join(migrationsDir, downs[i]))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.ExecContext(ctx, string(content)); err != nil {
			t.Fatalf("reverse %s: %v", downs[i], err)
		}
	}
	var remaining int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema()`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("down migrations left %d tables in the per-test schema", remaining)
	}
}

func TestRealDBGenerationTransactionsAndRepositories(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "fake-hash"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Real DB", OriginalScript: "A hero saves the town.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "First", Status: "pending"}
	must(t, repo.CreateChapter(ctx, chapter))
	character := &domain.Character{ProjectID: project.ID, Name: "Hero", Description: "A kind hero", VisualAttributes: map[string]any{"cape": true}}
	must(t, repo.CreateCharacter(ctx, character))
	gotCharacter, err := repo.GetCharacter(ctx, character.ID)
	must(t, err)
	if gotCharacter.VisualAttributes["cape"] != true {
		t.Fatal("character JSON did not round trip")
	}
	characters, err := repo.ListCharacters(ctx, project.ID)
	must(t, err)
	if len(characters) != 1 {
		t.Fatalf("expected one character, got %d", len(characters))
	}

	panel := &domain.Panel{ChapterID: chapter.ID, PanelNumber: 1, SceneDescription: "Town square", CharacterActions: "waves", CameraAngle: "wide", Background: "town", Dialogue: "Hello", Narration: "Morning", ImagePrompt: "comic town", Status: "ready"}
	must(t, repo.CreatePanel(ctx, panel))
	gotPanel, err := repo.GetPanel(ctx, panel.ID)
	must(t, err)
	gotPanel.SceneDescription = "Busy town square"
	must(t, repo.UpdatePanel(ctx, gotPanel))
	panels, err := repo.ListPanels(ctx, chapter.ID)
	must(t, err)
	if len(panels) != 1 || panels[0].SceneDescription != "Busy town square" {
		t.Fatal("panel create, read, update, list failed")
	}
	page := &domain.Page{ChapterID: chapter.ID, PageNumber: 1, PanelIDs: []string{panel.ID}}
	must(t, repo.CreatePage(ctx, page))
	gotPage, err := repo.GetPage(ctx, page.ID)
	must(t, err)
	if len(gotPage.PanelIDs) != 1 || gotPage.PanelIDs[0] != panel.ID {
		t.Fatal("page panel IDs did not round trip")
	}
	pages, err := repo.ListPages(ctx, chapter.ID)
	must(t, err)
	if len(pages) != 1 {
		t.Fatalf("expected one page, got %d", len(pages))
	}
	gotPage.DraftImageURL = "fake://draft"
	gotPage.LineArtImageURL = "fake://line-art-original"
	must(t, repo.UpdatePage(ctx, gotPage))
	pageBeforeColour, err := repo.GetPage(ctx, page.ID)
	must(t, err)
	colouredAt := time.Now().UTC().Truncate(time.Microsecond)
	must(t, repo.SavePageColourImage(ctx, page.ID, "fake://coloured-page", colouredAt))
	pageAfterColour, err := repo.GetPage(ctx, page.ID)
	must(t, err)
	if pageAfterColour.LineArtImageURL != "fake://line-art-original" || pageAfterColour.ColourImageURL != "fake://coloured-page" {
		t.Fatalf("coloured page persistence overwrote line art: %+v", pageAfterColour)
	}
	if pageAfterColour.ColourImageUploadedAt == nil || !pageAfterColour.ColourImageUploadedAt.Equal(colouredAt) {
		t.Fatalf("coloured page upload timestamp did not round trip: %+v", pageAfterColour.ColourImageUploadedAt)
	}
	if !pageAfterColour.UpdatedAt.Equal(pageBeforeColour.UpdatedAt) {
		t.Fatalf("colour upload changed the line-art activity timestamp: before=%s after=%s", pageBeforeColour.UpdatedAt, pageAfterColour.UpdatedAt)
	}

	makeJob := func(kind, stage, entity, key string) *domain.GenerationJob {
		return &domain.GenerationJob{ID: uuid.NewString(), ProjectID: project.ID, UserID: user.ID, JobType: kind, Stage: stage, ModelID: "fake", IdempotencyKey: key, EstimatedCost: .01, HashedUserID: domain.HashUserID(user.ID)}
	}
	// A missing stage approval must reject a page image without writing a job or debit.
	unapproved := makeJob("draft_sketch", "draft", page.ID, uuid.NewString())
	if _, _, err := repo.CreateGeneration(ctx, unapproved, db.GenerationOptions{EntityID: page.ID}, .01); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("expected approval-gate rejection, got %v", err)
	}
	var count int
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_jobs WHERE id=$1::uuid`, unapproved.ID).Scan(&count))
	if count != 0 {
		t.Fatal("approval-gate rejection created a job")
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid`, unapproved.ID).Scan(&count))
	if count != 0 {
		t.Fatal("approval-gate rejection charged credits")
	}

	// Fail the debit after the job insert and prove PostgreSQL rolls both writes back.
	_, err = pool.ExecContext(ctx, `CREATE FUNCTION reject_test_debit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.transaction_type='debit' THEN RAISE EXCEPTION 'injected debit failure'; END IF; RETURN NEW; END $$`)
	must(t, err)
	_, err = pool.ExecContext(ctx, `CREATE TRIGGER reject_test_debit BEFORE INSERT ON credit_ledger FOR EACH ROW EXECUTE FUNCTION reject_test_debit()`)
	must(t, err)
	failed := makeJob("character_reference", "reference", character.ID, uuid.NewString())
	if _, _, err := repo.CreateGeneration(ctx, failed, db.GenerationOptions{EntityID: character.ID}, .01); err == nil {
		t.Fatal("expected injected debit failure")
	}
	_, err = pool.ExecContext(ctx, `DROP TRIGGER reject_test_debit ON credit_ledger`)
	must(t, err)
	_, err = pool.ExecContext(ctx, `DROP FUNCTION reject_test_debit()`)
	must(t, err)
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_jobs WHERE id=$1::uuid`, failed.ID).Scan(&count))
	if count != 0 {
		t.Fatal("job survived rollback after debit failure")
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid`, failed.ID).Scan(&count))
	if count != 0 {
		t.Fatal("debit survived rollback after injected failure")
	}

	// Same key is a no-op: one pending job, one debit and one quota usage row.
	job := makeJob("character_reference", "reference", character.ID, uuid.NewString())
	first, existed, err := repo.CreateGeneration(ctx, job, db.GenerationOptions{EntityID: character.ID}, .01)
	must(t, err)
	if existed {
		t.Fatal("first creation unexpectedly replayed")
	}
	replay, existed, err := repo.CreateGeneration(ctx, makeJob("character_reference", "reference", character.ID, job.IdempotencyKey), db.GenerationOptions{EntityID: character.ID}, .01)
	must(t, err)
	if !existed || replay.ID != first.ID {
		t.Fatal("same idempotency key did not return the original job")
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_jobs WHERE idempotency_key=$1`, job.IdempotencyKey).Scan(&count))
	if count != 1 {
		t.Fatalf("same key produced %d jobs", count)
	}
	stored, err := repo.GetJob(ctx, first.ID)
	must(t, err)
	if stored.ID != first.ID {
		t.Fatal("job lookup returned wrong row")
	}
	stored, err = repo.GetJobByIdempotencyKey(ctx, job.IdempotencyKey)
	must(t, err)
	if stored.ID != first.ID {
		t.Fatal("idempotency lookup returned wrong row")
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid AND transaction_type='debit'`, first.ID).Scan(&count))
	if count != 1 {
		t.Fatalf("same key produced %d debits", count)
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE generation_job_id=$1::uuid`, first.ID).Scan(&count))
	if count != 1 {
		t.Fatalf("same key produced %d quota rows", count)
	}

	active, err := repo.GetActiveJobForEntity(ctx, project.ID, "character_reference")
	must(t, err)
	if active.ID != first.ID {
		t.Fatal("active job lookup returned wrong row")
	}
	if _, _, err := repo.CreateGeneration(ctx, makeJob("character_reference", "reference", character.ID, uuid.NewString()), db.GenerationOptions{EntityID: character.ID}, .01); !errors.Is(err, domain.ErrActiveJobExists) {
		t.Fatalf("expected active-job guard, got %v", err)
	}

	// Concurrent distinct keys for the same entity yield exactly one active job.
	otherCharacter := &domain.Character{ProjectID: project.ID, Name: "Sidekick"}
	must(t, repo.CreateCharacter(ctx, otherCharacter))
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, e := repo.CreateGeneration(ctx, makeJob("character_reference", "reference", otherCharacter.ID, uuid.NewString()), db.GenerationOptions{EntityID: otherCharacter.ID}, .01)
			errs <- e
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes, guarded := 0, 0
	for e := range errs {
		if e == nil {
			successes++
		} else if errors.Is(e, domain.ErrActiveJobExists) {
			guarded++
		} else {
			t.Fatalf("concurrent creation failed unexpectedly: %v", e)
		}
	}
	if successes != 1 || guarded != 1 {
		t.Fatalf("concurrent active guard results: success=%d guarded=%d", successes, guarded)
	}

	// Completing and separately refunding exercises transition, debit/refund reads, and balances.
	cost := .01
	must(t, repo.TransitionGeneration(ctx, first.ID, "done", &cost, nil, []string{"pending"}))
	updated, err := repo.GetJob(ctx, first.ID)
	must(t, err)
	if updated.Status != "done" || updated.CompletedAt == nil {
		t.Fatal("transition did not persist terminal fields")
	}
	debit, err := repo.GenerationDebit(ctx, first.ID)
	must(t, err)
	if debit.Amount != .01 {
		t.Fatalf("wrong debit amount: %v", debit.Amount)
	}
	refund, err := repo.RefundGeneration(ctx, first.ID, .01)
	must(t, err)
	again, err := repo.RefundGeneration(ctx, first.ID, .01)
	must(t, err)
	if refund.ID != again.ID {
		t.Fatal("refund retry inserted a second row")
	}
	balance, err := repo.GetCreditBalance(ctx, user.ID)
	must(t, err)
	if balance != 99.99 {
		t.Fatalf("refund did not restore the refunded debit while retaining the concurrent job charge: %v", balance)
	}
	ledger, err := repo.ListCreditLedger(ctx, user.ID)
	must(t, err)
	if len(ledger) != 4 {
		t.Fatalf("expected seed, two generation debits, and one refund ledger rows, got %d", len(ledger))
	}
	since := time.Now().UTC().Add(-24 * time.Hour)
	daily, err := repo.GetUserDailyImageCount(ctx, user.ID, since)
	must(t, err)
	monthly, err := repo.GetUserMonthlyImageCount(ctx, user.ID, since.Add(-29*24*time.Hour))
	must(t, err)
	if daily != 2 || monthly != 2 {
		t.Fatalf("quota counters did not commit atomically: daily=%d monthly=%d", daily, monthly)
	}

	// Exercise the legacy job entry point and nullable status-update parameters
	// against PostgreSQL, including the previously inconsistent parameter path.
	legacy := makeJob("script_analysis", "text", "", uuid.NewString())
	must(t, repo.CreateJobWithDebit(ctx, legacy, .02))
	message := "injected transition failure"
	must(t, repo.TransitionGeneration(ctx, legacy.ID, "failed", nil, &message, []string{"pending"}))
	legacyUpdate := makeJob("script_analysis", "text", "", uuid.NewString())
	must(t, repo.CreateJobWithDebit(ctx, legacyUpdate, .01))
	completedAt := time.Now().UTC()
	actualCost := .01
	legacyUpdate.Status = "done"
	legacyUpdate.ActualCost = &actualCost
	legacyUpdate.CompletedAt = &completedAt
	must(t, repo.UpdateJob(ctx, legacyUpdate))
	updated, err = repo.GetJob(ctx, legacyUpdate.ID)
	must(t, err)
	if updated.Status != "done" || updated.ActualCost == nil || *updated.ActualCost != actualCost {
		t.Fatal("legacy job update did not persist")
	}
	must(t, repo.RecordAdjustment(ctx, user.ID, "", 1))
	must(t, repo.RecordAdjustment(ctx, user.ID, project.ID, 1))

	// A failure inserting quota usage also rolls the job and debit back.
	_, err = pool.ExecContext(ctx, `CREATE FUNCTION reject_test_quota() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected quota failure'; END $$`)
	must(t, err)
	_, err = pool.ExecContext(ctx, `CREATE TRIGGER reject_test_quota BEFORE INSERT ON image_quota_usage FOR EACH ROW EXECUTE FUNCTION reject_test_quota()`)
	must(t, err)
	quotaFailure := makeJob("character_reference", "reference", character.ID, uuid.NewString())
	if _, _, err := repo.CreateGeneration(ctx, quotaFailure, db.GenerationOptions{EntityID: character.ID}, .01); err == nil {
		t.Fatal("expected injected quota failure")
	}
	_, err = pool.ExecContext(ctx, `DROP TRIGGER reject_test_quota ON image_quota_usage`)
	must(t, err)
	_, err = pool.ExecContext(ctx, `DROP FUNCTION reject_test_quota()`)
	must(t, err)
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_jobs WHERE id=$1::uuid`, quotaFailure.ID).Scan(&count))
	if count != 0 {
		t.Fatal("job survived rollback after quota failure")
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid`, quotaFailure.ID).Scan(&count))
	if count != 0 {
		t.Fatal("debit survived rollback after quota failure")
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE generation_job_id=$1::uuid`, quotaFailure.ID).Scan(&count))
	if count != 0 {
		t.Fatal("quota counter survived rollback after injected failure")
	}

	// Exercise stage approval persistence and updates across page and character records.
	approval := &domain.StageApproval{ProjectID: project.ID, EntityType: "character_reference", EntityID: character.ID, ApprovedByUserID: user.ID, ApprovedAt: time.Now().UTC().Add(time.Second)}
	must(t, repo.RecordApproval(ctx, approval))
	latest, err := repo.GetLatestApproval(ctx, approval.EntityType, approval.EntityID)
	must(t, err)
	if latest.ID != approval.ID {
		t.Fatal("latest approval lookup failed")
	}
	approvals, err := repo.ListApprovals(ctx, project.ID)
	must(t, err)
	if len(approvals) != 1 {
		t.Fatalf("expected one approval, got %d", len(approvals))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
