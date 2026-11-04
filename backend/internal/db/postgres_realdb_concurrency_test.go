//go:build realdb

package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

const concurrentCallers = 50

func realUserProject(t *testing.T, repo *db.PostgresRepository, ctx context.Context) (*domain.User, *domain.Project) {
	t.Helper()
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "fake-hash"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Concurrent", OriginalScript: "A hero saves the town.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	return user, project
}

func realJob(user *domain.User, project *domain.Project, kind, stage, key string) *domain.GenerationJob {
	return &domain.GenerationJob{ID: uuid.NewString(), ProjectID: project.ID, UserID: user.ID, JobType: kind, Stage: stage, ModelID: "fake", IdempotencyKey: key, EstimatedCost: .01, HashedUserID: domain.HashUserID(user.ID)}
}

func concurrentRun(t *testing.T, count int, run func(context.Context, int) error) []error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	start := make(chan struct{})
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = run(ctx, i)
		}(i)
	}
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("concurrent database operations timed out: %v", ctx.Err())
	}
	return errs
}

// assertLedgerInvariants checks the durable ledger rather than repository
// return values, including a balance chain independent of timestamp tie order.
func assertLedgerInvariants(t *testing.T, pool *sql.DB) {
	t.Helper()
	rows, err := pool.Query(`SELECT user_id::text, transaction_type, amount::float8, balance_after::float8 FROM credit_ledger ORDER BY user_id`)
	must(t, err)
	type entry struct {
		kind          string
		amount, after float64
	}
	byUser := map[string][]entry{}
	for rows.Next() {
		var user, kind string
		var amount, after float64
		must(t, rows.Scan(&user, &kind, &amount, &after))
		byUser[user] = append(byUser[user], entry{kind, amount, after})
	}
	must(t, rows.Err())
	must(t, rows.Close())
	for user, ledger := range byUser {
		used := make([]bool, len(ledger))
		balance := 0.0
		for n := 0; n < len(ledger); n++ {
			found := -1
			for i, row := range ledger {
				if used[i] {
					continue
				}
				before := row.after
				switch row.kind {
				case "debit":
					before += row.amount
				case "refund", "adjustment":
					before -= row.amount
				default:
					t.Fatalf("user %s has unknown ledger kind %q", user, row.kind)
				}
				if math.Abs(before-balance) < .00005 {
					found = i
					break
				}
			}
			if found < 0 {
				t.Fatalf("user %s ledger has no row continuing balance %.4f", user, balance)
			}
			row := ledger[found]
			delta := row.amount
			if row.kind == "debit" {
				delta = -delta
			}
			balance += delta
			if math.Abs(balance-row.after) >= .00005 {
				t.Fatalf("user %s ledger chain mismatch: computed %.4f, recorded %.4f", user, balance, row.after)
			}
			used[found] = true
		}
		var sum float64
		must(t, pool.QueryRow(`SELECT COALESCE(SUM(CASE transaction_type WHEN 'debit' THEN -amount WHEN 'refund' THEN amount ELSE amount END),0)::float8 FROM credit_ledger WHERE user_id=$1::uuid`, user).Scan(&sum))
		if math.Abs(sum-balance) >= .00005 {
			t.Fatalf("user %s balance %.4f differs from ledger sum %.4f", user, balance, sum)
		}
	}
	var orphans, duplicateDebits int
	must(t, pool.QueryRow(`SELECT COUNT(*) FROM generation_jobs j LEFT JOIN users u ON u.id=j.user_id LEFT JOIN projects p ON p.id=j.project_id WHERE u.id IS NULL OR p.id IS NULL`).Scan(&orphans))
	must(t, pool.QueryRow(`SELECT COUNT(*) FROM (SELECT generation_job_id FROM credit_ledger WHERE transaction_type='debit' AND generation_job_id IS NOT NULL GROUP BY generation_job_id HAVING COUNT(*)>1) d`).Scan(&duplicateDebits))
	if orphans != 0 || duplicateDebits != 0 {
		t.Fatalf("ledger invariant failure: orphan jobs=%d double-charged jobs=%d", orphans, duplicateDebits)
	}
}

func TestRealDBConcurrentGenerationForOneAndManyUsers(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	errs := concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
		_, _, err := repo.CreateGeneration(ctx, realJob(user, project, "script_analysis", "text", uuid.NewString()), db.GenerationOptions{}, .01)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("same-user request %d: %v", i, err)
		}
	}
	users := make([]*domain.User, concurrentCallers)
	projects := make([]*domain.Project, concurrentCallers)
	for i := range users {
		users[i], projects[i] = realUserProject(t, repo, ctx)
	}
	errs = concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
		_, _, err := repo.CreateGeneration(ctx, realJob(users[i], projects[i], "script_analysis", "text", uuid.NewString()), db.GenerationOptions{}, .01)
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("many-user request %d: %v", i, err)
		}
	}
	assertLedgerInvariants(t, pool)
}

func TestRealDBConcurrentDebitsNeverOverdraw(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	_, err := pool.ExecContext(ctx, `UPDATE credit_ledger SET amount=1,balance_after=1 WHERE user_id=$1::uuid AND transaction_type='adjustment'`, user.ID)
	must(t, err)
	errs := concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
		_, _, err := repo.CreateGeneration(ctx, realJob(user, project, "script_analysis", "text", uuid.NewString()), db.GenerationOptions{}, .04)
		return err
	})
	succeeded, refused := 0, 0
	for i, err := range errs {
		if err == nil {
			succeeded++
		} else if errors.Is(err, db.ErrInsufficientCredit) {
			refused++
		} else {
			t.Fatalf("debit %d failed unexpectedly (including deadlocks): %v", i, err)
		}
	}
	if succeeded != 25 || refused != concurrentCallers-25 {
		t.Fatalf("affordable debit count: succeeded=%d refused=%d", succeeded, refused)
	}
	var balance float64
	must(t, pool.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE transaction_type WHEN 'debit' THEN -amount ELSE amount END),0)::float8 FROM credit_ledger WHERE user_id=$1::uuid`, user.ID).Scan(&balance))
	if math.Abs(balance) > .00005 {
		t.Fatalf("overdraw or lost update: final balance %.4f", balance)
	}
	assertLedgerInvariants(t, pool)
}

func TestRealDBConcurrentIdempotencyReplayAndRefund(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	key := uuid.NewString()
	jobs := make([]*domain.GenerationJob, concurrentCallers)
	for i := range jobs {
		jobs[i] = realJob(user, project, "script_analysis", "text", key)
	}
	ids := make([]string, concurrentCallers)
	errs := concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
		job, _, err := repo.CreateGeneration(ctx, jobs[i], db.GenerationOptions{}, .01)
		if err == nil {
			ids[i] = job.ID
		}
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("idempotent replay %d: %v", i, err)
		}
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("caller %d received job %s, want %s", i, id, ids[0])
		}
	}
	var count int
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_jobs WHERE idempotency_key=$1`, key).Scan(&count))
	if count != 1 {
		t.Fatalf("same key created %d jobs", count)
	}
	refunds := make([]string, concurrentCallers)
	errs = concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
		entry, err := repo.RefundGeneration(ctx, ids[0], .01)
		if err == nil {
			refunds[i] = entry.ID
		}
		return err
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("refund replay %d: %v", i, err)
		}
	}
	for i, id := range refunds {
		if id != refunds[0] {
			t.Fatalf("refund caller %d received distinct refund %s", i, id)
		}
	}
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid AND transaction_type='refund'`, ids[0]).Scan(&count))
	if count != 1 {
		t.Fatalf("same job received %d refunds", count)
	}
	assertLedgerInvariants(t, pool)
}

func TestRealDBConcurrentQuotaAndActiveJobGuards(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	_, err := pool.ExecContext(ctx, `UPDATE generation_policy SET max_daily_images=13,max_monthly_images=1000`)
	must(t, err)
	characters := make([]*domain.Character, concurrentCallers)
	for i := range characters {
		characters[i] = &domain.Character{ProjectID: project.ID, Name: fmt.Sprintf("Hero %02d", i)}
		must(t, repo.CreateCharacter(ctx, characters[i]))
	}
	errs := concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
		_, _, err := repo.CreateGeneration(ctx, realJob(user, project, "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: characters[i].ID}, .01)
		return err
	})
	created, limited := 0, 0
	for i, err := range errs {
		if err == nil {
			created++
		} else if errors.Is(err, domain.ErrQuotaExceeded) {
			limited++
		} else {
			t.Fatalf("quota request %d failed unexpectedly (including deadlocks): %v", i, err)
		}
	}
	if created != 13 || limited != concurrentCallers-13 {
		t.Fatalf("quota results: created=%d limited=%d", created, limited)
	}
	var daily int
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM image_quota_usage WHERE user_id=$1::uuid AND created_at>=NOW()-INTERVAL '24 hours'`, user.ID).Scan(&daily))
	if daily != 13 {
		t.Fatalf("quota lost or exceeded increments: daily=%d", daily)
	}
	_, err = pool.ExecContext(ctx, `UPDATE generation_policy SET max_daily_images=1000`)
	must(t, err)

	guardCharacter := &domain.Character{ProjectID: project.ID, Name: "Guarded"}
	must(t, repo.CreateCharacter(ctx, guardCharacter))
	errs = concurrentRun(t, concurrentCallers, func(ctx context.Context, i int) error {
		_, _, err := repo.CreateGeneration(ctx, realJob(user, project, "character_reference", "reference", uuid.NewString()), db.GenerationOptions{EntityID: guardCharacter.ID}, .01)
		return err
	})
	created, guarded := 0, 0
	for i, err := range errs {
		if err == nil {
			created++
		} else if errors.Is(err, domain.ErrActiveJobExists) {
			guarded++
		} else {
			t.Fatalf("active guard request %d failed unexpectedly (including deadlocks): %v", i, err)
		}
	}
	if created != 1 || guarded != concurrentCallers-1 {
		t.Fatalf("active guard results: created=%d guarded=%d", created, guarded)
	}
	assertLedgerInvariants(t, pool)
}

func TestRealDBApprovalAndRegenerationRaceKeepsApprovalStale(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "First", Status: "pending"}
	must(t, repo.CreateChapter(ctx, chapter))
	page := &domain.Page{ChapterID: chapter.ID, PageNumber: 1}
	must(t, repo.CreatePage(ctx, page))
	page, err := repo.GetPage(ctx, page.ID)
	must(t, err)
	regen := realJob(user, project, "draft_sketch", "draft", uuid.NewString())
	must(t, pool.QueryRowContext(ctx, `INSERT INTO generation_jobs (id,project_id,user_id,job_type,stage,model_id,status,idempotency_key,estimated_cost,hashed_user_id,created_at,entity_id) VALUES ($1::uuid,$2::uuid,$3::uuid,'draft_sketch','draft','fake','pending',$4,.01,$5,NOW(),$6::uuid) RETURNING id`, regen.ID, project.ID, user.ID, regen.IdempotencyKey, regen.HashedUserID, page.ID).Scan(new(string)))
	approvalAt := page.UpdatedAt.Add(time.Microsecond)
	approval := &domain.StageApproval{ProjectID: project.ID, EntityType: "draft_page", EntityID: page.ID, ApprovedByUserID: user.ID, ApprovedAt: approvalAt}
	errs := concurrentRun(t, 2, func(ctx context.Context, i int) error {
		if i == 0 {
			return repo.RecordApproval(ctx, approval)
		}
		return repo.TransitionGeneration(ctx, regen.ID, "done", nil, nil, []string{"pending"})
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("approval/regeneration operation %d: %v", i, err)
		}
	}
	lineArt := realJob(user, project, "final_line_art", "line_art", uuid.NewString())
	if _, _, err := repo.CreateGeneration(ctx, lineArt, db.GenerationOptions{EntityID: page.ID}, .01); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("stale approval allowed line art generation: %v", err)
	}
	var count int
	must(t, pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM generation_jobs WHERE idempotency_key=$1`, lineArt.IdempotencyKey).Scan(&count))
	if count != 0 {
		t.Fatalf("stale approval race created %d jobs", count)
	}
	assertLedgerInvariants(t, pool)
}

func TestRealDBConcurrentTerminalTransitionsNeverRegress(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	job := realJob(user, project, "script_analysis", "text", uuid.NewString())
	must(t, repo.CreateJobWithDebit(ctx, job, .01))
	statuses := []string{"done", "failed", "cancelled", "done", "failed"}
	errs := concurrentRun(t, len(statuses), func(ctx context.Context, i int) error {
		return repo.TransitionGeneration(ctx, job.ID, statuses[i], nil, nil, []string{"pending"})
	})
	won, conflicted := 0, 0
	for i, err := range errs {
		if err == nil {
			won++
		} else if errors.Is(err, db.ErrConflict) {
			conflicted++
		} else {
			t.Fatalf("transition %d failed unexpectedly (including deadlocks): %v", i, err)
		}
	}
	if won != 1 || conflicted != len(statuses)-1 {
		t.Fatalf("transition results: terminal writes=%d conflicts=%d", won, conflicted)
	}
	if err := repo.TransitionGeneration(ctx, job.ID, "running", nil, nil, []string{"pending", "running"}); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("terminal job regressed to running: %v", err)
	}
	stored, err := repo.GetJob(ctx, job.ID)
	must(t, err)
	if stored.Status != "done" && stored.Status != "failed" && stored.Status != "cancelled" {
		t.Fatalf("unexpected final status %q", stored.Status)
	}
	assertLedgerInvariants(t, pool)
}
