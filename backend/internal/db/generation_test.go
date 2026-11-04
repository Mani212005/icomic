package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"icomic/backend/internal/domain"
)

var injectedFailure = errors.New("injected transaction failure")

type transactionFixture struct {
	fail                       string
	active                     bool
	threshold, spend, balance  float64
	daily, monthly, book       int
	missingApproval, activeJob bool
	jobs                       map[string]*domain.GenerationJob
	entries                    []*domain.CreditLedger
	commits, rollbacks         int
	last                       *fixtureTransaction
}
type fixtureTransaction struct {
	quotaIncrement bool
	quotaRelease   bool
	state          *transactionFixture
	job            *domain.GenerationJob
	entries        []*domain.CreditLedger
	latched        bool
	committed      bool
}
type fixtureRow struct {
	values []any
	err    error
}

func (r fixtureRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan: got %d destinations for %d values", len(dest), len(r.values))
	}
	for i, value := range r.values {
		target := reflect.ValueOf(dest[i]).Elem()
		if value == nil {
			target.SetZero()
		} else {
			target.Set(reflect.ValueOf(value))
		}
	}
	return nil
}
func (s *transactionFixture) Begin(context.Context) (Transaction, error) {
	if s.fail == "begin" {
		return nil, injectedFailure
	}
	tx := &fixtureTransaction{state: s}
	s.last = tx
	return tx, nil
}
func (t *fixtureTransaction) row(step string, values ...any) Row {
	if t.state.fail == step {
		return fixtureRow{err: injectedFailure}
	}
	return fixtureRow{values: values}
}
func jobRow(job *domain.GenerationJob) Row {
	return fixtureRow{values: []any{job.ID, job.ProjectID, job.UserID, job.JobType, job.Stage, job.ModelID, job.Status, job.IdempotencyKey, job.EstimatedCost, job.ActualCost, job.HashedUserID, job.ErrorMessage, job.CreatedAt, job.CompletedAt}}
}
func (t *fixtureTransaction) QueryRowContext(_ context.Context, q string, args ...any) Row {
	q = strings.Join(strings.Fields(q), " ")
	switch {
	case strings.Contains(q, "WHERE idempotency_key="):
		if job := t.state.jobs[args[0].(string)]; job != nil {
			return jobRow(job)
		}
		return fixtureRow{err: sql.ErrNoRows}
	case strings.Contains(q, "SELECT singleton FROM generation_policy"):
		return t.row("policy", true)
	case strings.Contains(q, "FROM generation_policy"):
		return t.row("policy", t.state.active, t.state.threshold, 80, 800, 40)
	case strings.Contains(q, "SELECT id FROM users"):
		return t.row("user", args[0])
	case strings.Contains(q, "SUM(CASE transaction_type WHEN 'debit' THEN amount"):
		return t.row("spend", t.state.spend)
	case strings.Contains(q, "COUNT(*) FROM image_quota_usage WHERE project_id"):
		return t.row("book_quota", t.state.book)
	case strings.Contains(q, "COUNT(*) FILTER"):
		return t.row("quota", t.state.daily, t.state.monthly)
	case strings.Contains(q, "original_script FROM projects"):
		return t.row("project", "A submitted script.")
	case strings.Contains(q, "SELECT id FROM characters"):
		return t.row("entity", "character")
	case strings.Contains(q, "p.chapter_id,p.updated_at"):
		return t.row("entity", "chapter", time.Now().Add(-time.Hour))
	case strings.Contains(q, "SELECT MAX(updated_at) FROM panels"):
		return t.row("panel_activity", sql.NullTime{})
	case strings.Contains(q, "BOOL_AND"):
		return t.row("characters", !t.state.missingApproval)
	case strings.Contains(q, "SELECT EXISTS (SELECT 1 FROM stage_approvals"):
		return t.row("approval", !t.state.missingApproval)
	case strings.Contains(q, "SELECT EXISTS (SELECT 1 FROM generation_jobs"):
		return t.row("active", t.state.activeJob)
	case strings.Contains(q, "SUM(CASE transaction_type WHEN 'debit' THEN -amount"):
		return t.row("balance", t.state.balance)
	case strings.HasPrefix(q, "INSERT INTO generation_jobs"):
		if t.state.fail == "insert_job" {
			return fixtureRow{err: injectedFailure}
		}
		t.job = &domain.GenerationJob{ID: args[0].(string), ProjectID: args[1].(string), UserID: args[2].(string), JobType: args[3].(string), Stage: args[4].(string), ModelID: args[5].(string), Status: args[6].(string), IdempotencyKey: args[7].(string), EstimatedCost: args[8].(float64), HashedUserID: args[9].(string), CreatedAt: args[10].(time.Time)}
		return t.row("insert_job", t.job.ID)
	case strings.HasPrefix(q, "SELECT user_id,project_id"):
		return t.row("refund_job", "user", "project")
	case strings.HasPrefix(q, "SELECT amount FROM credit_ledger"):
		return t.row("refund_debit", 0.01)
	case strings.Contains(q, "transaction_type='refund'"):
		for _, e := range t.state.entries {
			if e.TransactionType == "refund" {
				return fixtureRow{values: []any{e.ID, e.UserID, sql.NullString{String: e.ProjectID, Valid: true}, sql.NullString{String: *e.GenerationJobID, Valid: true}, e.TransactionType, e.Amount, e.BalanceAfter, e.CreatedAt}}
			}
		}
		return fixtureRow{err: sql.ErrNoRows}
	default:
		return fixtureRow{err: fmt.Errorf("unexpected query: %s", q)}
	}
}
func (t *fixtureTransaction) ExecContext(_ context.Context, q string, args ...any) (sql.Result, error) {
	q = strings.Join(strings.Fields(q), " ")
	switch {
	case strings.HasPrefix(q, "INSERT INTO credit_ledger"):
		if t.state.fail == "insert_debit" {
			return nil, injectedFailure
		}
		jobID := args[3].(string)
		t.entries = append(t.entries, &domain.CreditLedger{ID: args[0].(string), UserID: args[1].(string), ProjectID: args[2].(string), GenerationJobID: &jobID, TransactionType: args[4].(string), Amount: args[5].(float64), BalanceAfter: args[6].(float64), CreatedAt: args[7].(time.Time)})
	case strings.HasPrefix(q, "INSERT INTO image_quota_usage"):
		if t.state.fail == "insert_quota" {
			return nil, injectedFailure
		}
		t.quotaIncrement = true
	case strings.HasPrefix(q, "DELETE FROM image_quota_usage"):
		if t.state.fail == "release_quota" {
			return nil, injectedFailure
		}
		t.quotaRelease = true
	case strings.HasPrefix(q, "UPDATE generation_policy"):
		if t.state.fail == "latch" {
			return nil, injectedFailure
		}
		t.latched = true
	case strings.Contains(q, "SELECT id FROM panels"):
		if t.state.fail == "lock_panels" {
			return nil, injectedFailure
		}
	case strings.Contains(q, "SELECT id FROM characters"):
		if t.state.fail == "lock_characters" {
			return nil, injectedFailure
		}
	default:
		return nil, fmt.Errorf("unexpected exec: %s", q)
	}
	return nil, nil
}
func (t *fixtureTransaction) Commit() error {
	if t.state.fail == "commit" {
		return injectedFailure
	}
	t.committed = true
	t.state.commits++
	if t.job != nil {
		t.state.jobs[t.job.IdempotencyKey] = t.job

	}
	if t.quotaIncrement {
		t.state.daily++
		t.state.monthly++
		t.state.book++
	}
	if t.quotaRelease {
		t.state.daily--
		t.state.monthly--
		t.state.book--
	}
	for _, e := range t.entries {
		t.state.entries = append(t.state.entries, e)
		t.state.balance = e.BalanceAfter
	}
	if t.latched {
		t.state.active = true
	}
	return nil
}
func (t *fixtureTransaction) Rollback() error {
	if !t.committed {
		t.state.rollbacks++
		t.job = nil
		t.entries = nil
		t.quotaIncrement = false
	}
	return nil
}
func newGenerationFixture() (*PostgresRepository, *transactionFixture, *domain.GenerationJob) {
	state := &transactionFixture{balance: 10, jobs: make(map[string]*domain.GenerationJob)}
	repo := NewPostgresRepositoryWithTransactions(nil, state)
	job := &domain.GenerationJob{ID: "job", ProjectID: "project", UserID: "user", JobType: "character_reference", Stage: "reference", ModelID: "fake", IdempotencyKey: "key", EstimatedCost: 0.01}
	return repo, state, job
}
func TestCreateGenerationRollsBackEveryFailure(t *testing.T) {
	for _, step := range []string{"begin", "policy", "user", "book_quota", "quota", "project", "entity", "active", "balance", "insert_job", "insert_debit", "insert_quota", "commit"} {
		t.Run(step, func(t *testing.T) {
			repo, state, job := newGenerationFixture()
			state.fail = step
			result, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "character"}, 0.01)
			if !errors.Is(err, injectedFailure) || result != nil {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if len(state.jobs) != 0 || len(state.entries) != 0 || state.daily != 0 || state.monthly != 0 || state.balance != 10 || state.commits != 0 {
				t.Fatalf("partial commit: %+v", state)
			}
			if step != "begin" && state.rollbacks != 1 {
				t.Fatal("transaction was not rolled back")
			}
		})
	}
}
func TestGenerationCommitAndIdempotentReplay(t *testing.T) {
	repo, state, job := newGenerationFixture()
	created, existed, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "character"}, 0.01)
	if err != nil || existed {
		t.Fatalf("create: %v %v", existed, err)
	}
	if len(state.jobs) != 1 || len(state.entries) != 1 || state.daily != 1 || state.monthly != 1 || state.balance != 9.99 {
		t.Fatalf("missing atomic state: %+v", state)
	}
	state.active = true // A replay returns the committed result even after policy changes.
	replay := *job
	replay.ID = "different"
	repeated, existed, err := repo.CreateGeneration(context.Background(), &replay, GenerationOptions{EntityID: "character"}, 0.01)
	if err != nil || !existed || repeated.ID != created.ID || len(state.entries) != 1 || state.commits != 1 {
		t.Fatalf("replay: job=%v existed=%v err=%v state=%+v", repeated, existed, err, state)
	}
	replay.UserID = "other-user"
	if _, _, err := repo.CreateGeneration(context.Background(), &replay, GenerationOptions{}, 0.01); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-user replay: %v", err)
	}
}
func TestGenerationRejectsPolicyAndMissingApproval(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*transactionFixture, *domain.GenerationJob)
		want  error
	}{
		{"kill_switch", func(s *transactionFixture, _ *domain.GenerationJob) { s.active = true }, domain.ErrKillSwitchActive},
		{"book", func(s *transactionFixture, _ *domain.GenerationJob) { s.book = 40 }, domain.ErrBookBudgetExceeded},
		{"daily", func(s *transactionFixture, _ *domain.GenerationJob) { s.daily = 80 }, domain.ErrQuotaExceeded},
		{"monthly", func(s *transactionFixture, _ *domain.GenerationJob) { s.monthly = 800 }, domain.ErrQuotaExceeded},
		{"balance", func(s *transactionFixture, _ *domain.GenerationJob) { s.balance = 0 }, ErrInsufficientCredit},
		{"active_entity", func(s *transactionFixture, _ *domain.GenerationJob) { s.activeJob = true }, domain.ErrActiveJobExists},
		{"draft_approval", func(s *transactionFixture, j *domain.GenerationJob) {
			s.missingApproval = true
			j.JobType = "draft_sketch"
		}, domain.ErrMissingPrerequisiteApproval},
		{"line_art_approval", func(s *transactionFixture, j *domain.GenerationJob) {
			s.missingApproval = true
			j.JobType = "final_line_art"
		}, domain.ErrMissingPrerequisiteApproval},
		{"colour_approval", func(s *transactionFixture, j *domain.GenerationJob) {
			s.missingApproval = true
			j.JobType = "colour_suggestion"
		}, domain.ErrMissingPrerequisiteApproval},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, state, job := newGenerationFixture()
			test.setup(state, job)
			result, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "entity"}, 0.01)
			if !errors.Is(err, test.want) || result != nil || state.commits != 0 {
				t.Fatalf("result=%v err=%v commits=%d", result, err, state.commits)
			}
		})
	}
}
func TestGenerationSpendThresholdLatchesInSameTransaction(t *testing.T) {
	for _, failure := range []string{"", "latch", "commit"} {
		t.Run(failure, func(t *testing.T) {
			repo, state, job := newGenerationFixture()
			state.threshold = 1
			state.spend = 0.99
			state.fail = failure
			_, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "character"}, 0.01)
			if failure == "" {
				if err != nil || !state.active {
					t.Fatalf("did not latch: %v", err)
				}
			} else if err == nil || state.active || len(state.jobs) != 0 || len(state.entries) != 0 {
				t.Fatalf("partial latch: err=%v state=%+v", err, state)
			}
		})
	}
}
func TestRefundUsesSeparateTransactionAndIsIdempotent(t *testing.T) {
	repo, state, job := newGenerationFixture()
	if _, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "character"}, 0.01); err != nil {
		t.Fatal(err)
	}
	state.fail = "insert_debit"
	if _, err := repo.RefundGeneration(context.Background(), job.ID, 0.01); err == nil {
		t.Fatal("expected refund write failure")
	}
	if len(state.jobs) != 1 || len(state.entries) != 1 || state.balance != 9.99 {
		t.Fatal("failed refund changed the debit transaction")
	}
	state.fail = ""
	refund, err := repo.RefundGeneration(context.Background(), job.ID, 0.01)
	if err != nil || refund.BalanceAfter != 10 || state.commits != 2 {
		t.Fatalf("refund=%v err=%v", refund, err)
	}
	if _, err := repo.RefundGeneration(context.Background(), job.ID, 0.01); err != nil {
		t.Fatal(err)
	}
	if len(state.entries) != 2 || state.balance != 10 || state.daily != 0 || state.book != 0 || state.monthly != 0 {
		t.Fatal("refund replay changed balance or quota")
	}
}

func TestPageGenerationApprovalReadFailuresRollBack(t *testing.T) {
	for _, step := range []string{"lock_panels", "panel_activity", "lock_characters", "characters", "approval"} {
		t.Run(step, func(t *testing.T) {
			repo, state, job := newGenerationFixture()
			job.JobType = "draft_sketch"
			state.fail = step
			result, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "page"}, 0.01)
			if !errors.Is(err, injectedFailure) || result != nil || state.commits != 0 || len(state.entries) != 0 || state.rollbacks != 1 {
				t.Fatalf("result=%v err=%v state=%+v", result, err, state)
			}
		})
	}
}

func TestImageQuotaDoesNotBlockTextOrExport(t *testing.T) {
	for _, kind := range []string{"script_analysis", "chapter_outline", "panel_text", "export"} {
		t.Run(kind, func(t *testing.T) {
			repo, state, job := newGenerationFixture()
			state.daily, state.monthly, state.book = 80, 800, 40
			job.JobType = kind
			if _, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{}, 0); err != nil {
				t.Fatal(err)
			}
			if state.daily != 80 || state.monthly != 800 || state.book != 40 {
				t.Fatal("non-image generation changed image quota")
			}
		})
	}
}

func TestRefundQuotaReleaseRollsBackWithLedger(t *testing.T) {
	for _, failure := range []string{"release_quota", "commit"} {
		t.Run(failure, func(t *testing.T) {
			repo, state, job := newGenerationFixture()
			if _, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "character"}, .01); err != nil {
				t.Fatal(err)
			}
			state.fail = failure
			if _, err := repo.RefundGeneration(context.Background(), job.ID, .01); !errors.Is(err, injectedFailure) {
				t.Fatalf("refund failure: %v", err)
			}
			if state.daily != 1 || state.book != 1 || state.monthly != 1 || len(state.entries) != 1 || state.balance != 9.99 {
				t.Fatalf("partial refund commit: %+v", state)
			}
		})
	}
}
func TestPartialRefundRetainsImageSlot(t *testing.T) {
	repo, state, job := newGenerationFixture()
	if _, _, err := repo.CreateGeneration(context.Background(), job, GenerationOptions{EntityID: "character"}, .01); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RefundGeneration(context.Background(), job.ID, .005); err != nil {
		t.Fatal(err)
	}
	if state.daily != 1 || state.book != 1 || state.monthly != 1 {
		t.Fatal("partial refund released an image")
	}
}
