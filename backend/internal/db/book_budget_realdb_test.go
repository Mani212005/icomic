//go:build realdb

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/jobs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

func bookProject(t *testing.T, repo *db.PostgresRepository, ctx context.Context, user *domain.User) *domain.Project {
	t.Helper()
	project := &domain.Project{UserID: user.ID, Title: "Another book", OriginalScript: "A hero saves the town.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	return project
}

func bookCharacter(t *testing.T, repo *db.PostgresRepository, ctx context.Context, project *domain.Project) *domain.Character {
	t.Helper()
	c := &domain.Character{ProjectID: project.ID, Name: "Hero", Description: "A hero"}
	must(t, repo.CreateCharacter(ctx, c))
	return c
}

func TestRealDBBookAndDailyBudgets(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, first := realUserProject(t, repo, ctx)
	second := bookProject(t, repo, ctx, user)
	third := bookProject(t, repo, ctx, user)
	must(t, repo.RecordAdjustment(ctx, user.ID, "", 100))
	var book, daily, monthly int
	must(t, pool.QueryRowContext(ctx, `SELECT max_book_images,max_daily_images,max_monthly_images FROM generation_policy`).Scan(&book, &daily, &monthly))
	if book != 40 || daily != 80 || monthly != 800 {
		t.Fatalf("defaults: %d/%d/%d", book, daily, monthly)
	}
	chars := []*domain.Character{bookCharacter(t, repo, ctx, first), bookCharacter(t, repo, ctx, second), bookCharacter(t, repo, ctx, third)}
	var last *domain.GenerationJob
	for p, project := range []*domain.Project{first, second} {
		for n := 0; n < 40; n++ {
			job, replay, err := repo.CreateGeneration(ctx, realJob(user, project, "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: chars[p].ID}, .01)
			must(t, err)
			if replay {
				t.Fatal("new image treated as replay")
			}
			status := "done"
			if p == 1 && n == 39 {
				status = "failed"
			}
			must(t, repo.TransitionGeneration(ctx, job.ID, status, nil, nil, []string{"pending"}))
			last = job
		}
		_, _, err := repo.CreateGeneration(ctx, realJob(user, project, "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: chars[p].ID}, .01)
		if !errors.Is(err, domain.ErrBookBudgetExceeded) {
			t.Fatalf("41st book image: %v", err)
		}
	}
	assertBudgetHTTP(t, repo, user, chars[0], "book_budget_reached")
	assertBudgetHTTP(t, repo, user, chars[2], "daily_image_limit_reached")
	// The 80th image succeeds; a fresh project cannot bypass the user's daily cap.
	_, _, err := repo.CreateGeneration(ctx, realJob(user, third, "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: chars[2].ID}, .01)
	if !errors.Is(err, domain.ErrDailyImageLimitReached) {
		t.Fatalf("81st daily image: %v", err)
	}
	replay, existed, err := repo.CreateGeneration(ctx, last, db.GenerationOptions{EntityID: chars[1].ID}, .01)
	must(t, err)
	if !existed || replay.ID != last.ID {
		t.Fatal("replay at boundary failed")
	}
	count, err := repo.GetUserDailyImageCount(ctx, user.ID, time.Now().Add(-24*time.Hour))
	must(t, err)
	if count != 80 {
		t.Fatalf("replay changed count: %d", count)
	}

	// Failure alone holds the slot. Full refund and its replay release it once.

	for n := 0; n < 2; n++ {
		_, err = repo.RefundGeneration(ctx, last.ID, .01)
		must(t, err)
	}
	count, err = repo.GetUserDailyImageCount(ctx, user.ID, time.Now().Add(-24*time.Hour))
	must(t, err)
	if count != 79 {
		t.Fatalf("refund count: %d", count)
	}
	_, existed, err = repo.CreateGeneration(ctx, last, db.GenerationOptions{EntityID: chars[1].ID}, .01)
	must(t, err)
	if !existed {
		t.Fatal("refunded key created another image")
	}
	regenerated, _, err := repo.CreateGeneration(ctx, realJob(user, second, "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: chars[1].ID}, .01)
	must(t, err)
	must(t, repo.TransitionGeneration(ctx, regenerated.ID, "failed", nil, nil, []string{"pending"}))
	count, err = repo.GetUserDailyImageCount(ctx, user.ID, time.Now().Add(-24*time.Hour))
	must(t, err)
	if count != 80 {
		t.Fatalf("regeneration/failure count: %d", count)
	}
	must(t, repo.DeleteProject(ctx, first.ID))
	count, err = repo.GetUserDailyImageCount(ctx, user.ID, time.Now().Add(-24*time.Hour))
	must(t, err)
	if count != 80 {
		t.Fatalf("deleting book restored daily count: %d", count)
	}
	_, _, err = repo.CreateGeneration(ctx, realJob(user, third, "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: chars[2].ID}, .01)
	if !errors.Is(err, domain.ErrDailyImageLimitReached) {
		t.Fatalf("delete bypassed daily cap: %v", err)
	}
	assertLedgerInvariants(t, pool)
}

func TestRealDBConcurrentBookBudgetBoundaries(t *testing.T) {
	for _, scope := range []string{"book", "daily"} {
		t.Run(scope, func(t *testing.T) {
			pool, repo, ctx := realDatabase(t)
			user, project := realUserProject(t, repo, ctx)
			second := bookProject(t, repo, ctx, user)
			must(t, repo.RecordAdjustment(ctx, user.ID, "", 100))
			seed := 39
			seedTime := time.Now().Add(-31 * 24 * time.Hour) // Book usage never expires.
			if scope == "daily" {
				seed = 79
				seedTime = time.Now()
			}
			// Historical or deleted-project attempts need no extant job FK.
			var seedProject any = project.ID
			if scope == "daily" {
				seedProject = nil
			}
			_, err := pool.ExecContext(ctx, `INSERT INTO image_quota_usage(user_id,project_id,created_at) SELECT $1::uuid,$2::uuid,$3::timestamptz FROM generate_series(1,$4::int)`, user.ID, seedProject, seedTime, seed)
			must(t, err)
			projects := make([]*domain.Project, concurrentCallers)
			characters := make([]*domain.Character, concurrentCallers)
			for i := range projects {
				projects[i] = project
				if scope == "daily" && i%2 == 1 {
					projects[i] = second
				}
				characters[i] = bookCharacter(t, repo, ctx, projects[i])
			}
			errs := concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
				_, _, err := repo.CreateGeneration(ctx, realJob(user, projects[i], "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: characters[i].ID}, .01)
				return err
			})
			created, rejected := 0, 0
			want := domain.ErrBookBudgetExceeded
			if scope == "daily" {
				want = domain.ErrDailyImageLimitReached
			}
			for _, err := range errs {
				if err == nil {
					created++
				} else if errors.Is(err, want) {
					rejected++
				} else {
					t.Fatalf("unexpected concurrent result: %v", err)
				}
			}
			if created != 1 || rejected != 49 {
				t.Fatalf("created=%d rejected=%d", created, rejected)
			}
			var usage, jobs, debits int
			must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE user_id=$1::uuid`, user.ID).Scan(&usage))
			must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_jobs WHERE user_id=$1::uuid`, user.ID).Scan(&jobs))
			must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_ledger WHERE user_id=$1::uuid AND transaction_type='debit'`, user.ID).Scan(&debits))
			if usage != seed+1 || jobs != 1 || debits != 1 {
				t.Fatalf("partial or excessive admissions: usage=%d jobs=%d debits=%d", usage, jobs, debits)
			}
			assertLedgerInvariants(t, pool)
		})
	}
}

func assertBudgetHTTP(t *testing.T, repo *db.PostgresRepository, user *domain.User, character *domain.Character, code string) {
	t.Helper()
	server := api.NewServer(repo, credits.NewPersistentCreditService(repo), jobs.NewPersistentJobService(repo), gen.NewFakeAIServiceClient(), auth.NewDevAuthenticator(true, user.ID), auth.NewDefaultImageHosting(""), 5)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+character.ID+"/reference-image", nil)
	request.Header.Set("X-User-ID", user.ID)
	server.NewRouter().ServeHTTP(response, request)
	var body struct {
		Code string `json:"code"`
	}
	must(t, json.Unmarshal(response.Body.Bytes(), &body))
	if response.Code != http.StatusTooManyRequests || body.Code != code {
		t.Fatalf("budget API response: %d %s", response.Code, response.Body.String())
	}
}

func TestRealDBBookBudgetUpgradePreservesOverridesAndBackfills(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint(custom), func(t *testing.T) {
			pool, repo, ctx := realDatabaseWithMigrations(t, false)
			files, err := filepath.Glob(filepath.Join(migrationsDir, "*.up.sql"))
			must(t, err)
			sort.Strings(files)
			for _, file := range files {
				if strings.Contains(file, "000006_") {
					continue
				}
				body, err := os.ReadFile(file)
				must(t, err)
				_, err = pool.ExecContext(ctx, string(body))
				must(t, err)
			}
			user, project := realUserProject(t, repo, ctx)
			jobID := uuid.NewString()
			_, err = pool.ExecContext(ctx, `INSERT INTO generation_jobs(id,user_id,project_id,job_type,stage,model_id,status,idempotency_key,estimated_cost,hashed_user_id) VALUES ($1::uuid,$2::uuid,$3::uuid,'character_reference','reference','fake','failed',$1::text,.01,'hash')`, jobID, user.ID, project.ID)
			must(t, err)
			_, err = pool.ExecContext(ctx, `INSERT INTO image_quota_usage(user_id,generation_job_id) VALUES($1::uuid,$2::uuid),($1::uuid,NULL)`, user.ID, jobID)
			must(t, err)
			_, err = pool.ExecContext(ctx, `INSERT INTO credit_ledger(user_id,project_id,generation_job_id,transaction_type,amount,balance_after) VALUES($1::uuid,$2::uuid,$3::uuid,'debit',.01,0),($1::uuid,$2::uuid,$3::uuid,'refund',.01,.01)`, user.ID, project.ID, jobID)
			must(t, err)
			keptJob := uuid.NewString()
			_, err = pool.ExecContext(ctx, `INSERT INTO generation_jobs(id,user_id,project_id,job_type,stage,model_id,status,idempotency_key,estimated_cost,hashed_user_id) VALUES ($1::uuid,$2::uuid,$3::uuid,'character_reference','reference','fake','done',$1::text,.01,'hash')`, keptJob, user.ID, project.ID)
			must(t, err)
			_, err = pool.ExecContext(ctx, `INSERT INTO image_quota_usage(user_id,generation_job_id) VALUES($1::uuid,$2::uuid)`, user.ID, keptJob)
			must(t, err)
			if custom {
				_, err = pool.ExecContext(ctx, `UPDATE generation_policy SET max_daily_images=1234,max_monthly_images=5678`)
				must(t, err)
			}
			body, err := os.ReadFile(filepath.Join(migrationsDir, "000006_book_image_budget.up.sql"))
			must(t, err)
			_, err = pool.ExecContext(ctx, string(body))
			must(t, err)
			var book, daily, monthly, usage int
			must(t, pool.QueryRowContext(ctx, `SELECT max_book_images,max_daily_images,max_monthly_images FROM generation_policy`).Scan(&book, &daily, &monthly))
			wantDaily, wantMonthly := 80, 800
			if custom {
				wantDaily, wantMonthly = 1234, 5678
			}
			if book != 40 || daily != wantDaily || monthly != wantMonthly {
				t.Fatalf("upgrade policy: %d/%d/%d", book, daily, monthly)
			}
			must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE user_id=$1::uuid`, user.ID).Scan(&usage))
			if usage != 2 {
				t.Fatalf("refund not released or deleted-job usage lost: %d", usage)
			}
			must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE project_id=$1::uuid`, project.ID).Scan(&usage))
			if usage != 1 {
				t.Fatalf("existing image not attributed to its book: %d", usage)
			}

		})
	}
}

func TestRealDBConcurrentImageReplayAndRefund(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	character := bookCharacter(t, repo, ctx, project)
	must(t, repo.RecordAdjustment(ctx, user.ID, "", 10))
	key := uuid.NewString()
	replay := func(ctx context.Context, _ int) error {
		_, _, err := repo.CreateGeneration(ctx, realJob(user, project, "character_reference", "reference", key), db.GenerationOptions{EntityID: character.ID}, .01)
		return err
	}
	for _, err := range concurrentRun(t, concurrentCallers, replay) {
		must(t, err)
	}
	var jobID string
	must(t, pool.QueryRowContext(ctx, `SELECT id FROM generation_jobs WHERE idempotency_key=$1`, key).Scan(&jobID))
	var count int
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE user_id=$1::uuid`, user.ID).Scan(&count))
	if count != 1 {
		t.Fatalf("concurrent replay counted %d images", count)
	}
	must(t, repo.TransitionGeneration(ctx, jobID, "failed", nil, nil, []string{"pending"}))
	for _, err := range concurrentRun(t, concurrentCallers, func(ctx context.Context, _ int) error { _, err := repo.RefundGeneration(ctx, jobID, .01); return err }) {
		must(t, err)
	}
	for _, err := range concurrentRun(t, concurrentCallers, replay) {
		must(t, err)
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE user_id=$1::uuid`, user.ID).Scan(&count))
	if count != 0 {
		t.Fatalf("refund/replay retained or restored %d images", count)
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid`, jobID).Scan(&count))
	if count != 2 {
		t.Fatalf("image charged/refunded more than once: %d", count)
	}
	assertLedgerInvariants(t, pool)
}
