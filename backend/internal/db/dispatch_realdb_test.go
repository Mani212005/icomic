//go:build realdb

package db_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

// savedQueue stands in for the Python queue: the first call for a job ID runs the "provider",
// every later call for the same ID returns the saved result without invoking it again.
type savedQueue struct {
	mu            sync.Mutex
	saved         map[string]*gen.GenerateCharacterReferenceResponse
	providerCalls int
	before        func(gen.GenerateCharacterReferenceRequest) error
}

func (q *savedQueue) client() *gen.FakeAIServiceClient {
	fake := gen.NewFakeAIServiceClient()
	fake.GenerateCharacterReferenceFunc = func(_ context.Context, req gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.saved == nil {
			q.saved = map[string]*gen.GenerateCharacterReferenceResponse{}
		}
		if resp, ok := q.saved[req.JobID]; ok {
			return resp, nil
		}
		if q.before != nil {
			if err := q.before(req); err != nil {
				return nil, err
			}
		}
		q.providerCalls++
		resp := &gen.GenerateCharacterReferenceResponse{JobID: req.JobID, ImageURL: "http://localhost:8000/storage/fake/characters/" + req.JobID + ".png", ModelID: "fake"}
		q.saved[req.JobID] = resp
		return resp, nil
	}
	return fake
}

// failingCharacterSaves makes the entity link fail, like a database error after the provider finished.
type failingCharacterSaves struct {
	db.Repository
	fail bool
}

func (r *failingCharacterSaves) UpdateCharacter(ctx context.Context, ch *domain.Character) error {
	if r.fail {
		return errors.New("injected persistence failure")
	}
	return r.Repository.UpdateCharacter(ctx, ch)
}

type dispatchFixture struct {
	pool      *sql.DB
	repo      *db.PostgresRepository
	ctx       context.Context
	user      *domain.User
	project   *domain.Project
	character *domain.Character
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	pool, repo, ctx := realDatabase(t)
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Dispatch", OriginalScript: "A hero finds the lost key.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	character := &domain.Character{ProjectID: project.ID, Name: "Hero", Description: "A careful explorer"}
	must(t, repo.CreateCharacter(ctx, character))
	_, err := pool.ExecContext(ctx, `INSERT INTO credit_ledger(user_id,transaction_type,amount,balance_after) VALUES($1::uuid,'adjustment',50,50)`, user.ID)
	must(t, err)
	return &dispatchFixture{pool, repo, ctx, user, project, character}
}

// server builds a backend process over the shared database: a "restart" is a second server.
func (f *dispatchFixture) server(repo db.Repository, ai gen.AIService) *api.Server {
	server := api.NewServer(repo, credits.NewPersistentCreditService(f.repo), jobs.NewPersistentJobService(f.repo), ai, auth.NewDevAuthenticator(true, f.user.ID), auth.NewDefaultImageHosting("/media"), 5)
	server.Dispatches = f.repo
	return server
}

func (f *dispatchFixture) generate(server *api.Server) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+f.character.ID+"/reference-image", strings.NewReader(`{}`))
	req.Header.Set("X-User-ID", f.user.ID)
	server.NewRouter().ServeHTTP(rec, req)
	return rec
}

func (f *dispatchFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	must(t, f.pool.QueryRowContext(f.ctx, query, args...).Scan(&n))
	return n
}

func (f *dispatchFixture) jobStatus(t *testing.T) (id, status string) {
	t.Helper()
	must(t, f.pool.QueryRowContext(f.ctx, `SELECT id::text, status FROM generation_jobs WHERE project_id=$1::uuid`, f.project.ID).Scan(&id, &status))
	return id, status
}

func (f *dispatchFixture) dispatchState(t *testing.T, jobID string) (state string, attempts int) {
	t.Helper()
	must(t, f.pool.QueryRowContext(f.ctx, `SELECT state, attempts FROM generation_dispatches WHERE job_id=$1::uuid`, jobID).Scan(&state, &attempts))
	return state, attempts
}

func (f *dispatchFixture) ledger(t *testing.T, kind string) int {
	return f.count(t, `SELECT COUNT(*) FROM credit_ledger WHERE user_id=$1::uuid AND transaction_type=$2::varchar AND generation_job_id IS NOT NULL`, f.user.ID, kind)
}

func (f *dispatchFixture) referenceURL(t *testing.T) string {
	t.Helper()
	ch, err := f.repo.GetCharacter(f.ctx, f.character.ID)
	must(t, err)
	return ch.ReferenceImageURL
}

func TestRealDBGenerationEnvelopeCommitsAtomicallyWithDebit(t *testing.T) {
	f := newDispatchFixture(t)
	envelope := &db.GenerationEnvelope{Operation: "generate-character-reference", Request: []byte(`{"character_id":"` + f.character.ID + `"}`)}
	makeJob := func() *domain.GenerationJob {
		return &domain.GenerationJob{ID: uuid.NewString(), ProjectID: f.project.ID, UserID: f.user.ID, JobType: "character_reference", Stage: "reference", ModelID: "fake", IdempotencyKey: uuid.NewString(), EstimatedCost: .01, HashedUserID: domain.HashUserID(f.user.ID)}
	}
	job, existed, err := f.repo.CreateGeneration(f.ctx, makeJob(), db.GenerationOptions{EntityID: f.character.ID, Envelope: envelope}, .01)
	must(t, err)
	if existed {
		t.Fatal("new generation reported as existing")
	}
	var stamped string
	must(t, f.pool.QueryRowContext(f.ctx, `SELECT request->>'job_id' FROM generation_dispatches WHERE job_id=$1::uuid AND state='pending' AND operation='generate-character-reference'`, job.ID).Scan(&stamped))
	if stamped != job.ID || f.ledger(t, "debit") != 1 {
		t.Fatalf("envelope must be committed once with the debit and stamped with its job ID, got %q", stamped)
	}

	other := &domain.Character{ProjectID: f.project.ID, Name: "Other", Description: "Another"}
	must(t, f.repo.CreateCharacter(f.ctx, other))
	rejected := makeJob()
	if _, _, err := f.repo.CreateGeneration(f.ctx, rejected, db.GenerationOptions{EntityID: other.ID, Envelope: envelope}, 1000); !errors.Is(err, db.ErrInsufficientCredit) {
		t.Fatalf("expected insufficient credit, got %v", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM generation_dispatches WHERE job_id=$1::uuid`, rejected.ID); n != 0 {
		t.Fatal("a rolled-back generation left an envelope to dispatch")
	}
	if n := f.count(t, `SELECT COUNT(*) FROM generation_dispatches`); n != 1 {
		t.Fatalf("expected exactly the committed envelope, got %d", n)
	}
}

func TestRealDBDispatcherBoundsClaimsAndRespectsLeases(t *testing.T) {
	f := newDispatchFixture(t)
	for i := 0; i < 5; i++ {
		ch := &domain.Character{ProjectID: f.project.ID, Name: "Extra", Description: "Another"}
		must(t, f.repo.CreateCharacter(f.ctx, ch))
		job := &domain.GenerationJob{ID: uuid.NewString(), ProjectID: f.project.ID, UserID: f.user.ID, JobType: "character_reference", Stage: "reference", ModelID: "fake", IdempotencyKey: uuid.NewString(), HashedUserID: domain.HashUserID(f.user.ID)}
		_, _, err := f.repo.CreateGeneration(f.ctx, job, db.GenerationOptions{EntityID: ch.ID, Envelope: &db.GenerationEnvelope{Operation: "generate-character-reference", Request: []byte(`{}`)}}, .01)
		must(t, err)
	}
	// The request that committed each job owns it until its lease lapses.
	if claimed, err := f.repo.ClaimDispatches(f.ctx, 10, false); err != nil || len(claimed) != 0 {
		t.Fatalf("a freshly committed envelope belongs to its request, got %d claimed, err %v", len(claimed), err)
	}
	// Startup recovery takes what a previous process left, bounded by the limit.
	first, err := f.repo.ClaimDispatches(f.ctx, 2, true)
	must(t, err)
	if len(first) != 2 || first[0].Attempts != 1 || first[1].Attempts != 1 {
		t.Fatalf("a claim must take at most the limit and count the attempt, got %+v", first)
	}
	// Each claim leases what it took, so the polling dispatcher cannot hand the same job out twice.
	if again, err := f.repo.ClaimDispatches(f.ctx, 10, false); err != nil || len(again) != 0 {
		t.Fatalf("claimed envelopes are leased, got %d reclaimed, err %v", len(again), err)
	}
}

func TestRealDBDispatcherSendsJobLostBeforeQueueAcceptanceOnce(t *testing.T) {
	f := newDispatchFixture(t)
	crash := &savedQueue{before: func(gen.GenerateCharacterReferenceRequest) error { panic("process killed after the debit commit") }}
	func() {
		defer func() { _ = recover() }()
		f.generate(f.server(f.repo, crash.client()))
	}()
	jobID, status := f.jobStatus(t)
	if status != "running" || f.ledger(t, "debit") != 1 || crash.providerCalls != 0 {
		t.Fatalf("crash must leave one charged job that never reached the provider, got status %s", status)
	}
	if state, _ := f.dispatchState(t, jobID); state != "pending" {
		t.Fatalf("committed envelope must survive the crash, got %s", state)
	}

	restarted := &savedQueue{}
	server := f.server(f.repo, restarted.client())
	if n, err := server.DispatchPending(f.ctx, true); err != nil || n != 1 {
		t.Fatalf("restart must dispatch the stranded envelope, got %d, err %v", n, err)
	}
	if _, status := f.jobStatus(t); status != "done" {
		t.Fatalf("recovered job must finish, got %s", status)
	}
	if restarted.providerCalls != 1 || f.ledger(t, "debit") != 1 || f.ledger(t, "refund") != 0 {
		t.Fatalf("one provider call and one debit expected, got %d calls, %d debits", restarted.providerCalls, f.ledger(t, "debit"))
	}
	if got := f.referenceURL(t); !strings.HasSuffix(got, jobID+".png") {
		t.Fatalf("character must link the recovered artifact, got %q", got)
	}
	if state, _ := f.dispatchState(t, jobID); state != "delivered" {
		t.Fatalf("finished envelope must be retired, got %s", state)
	}
	if n, err := server.DispatchPending(f.ctx, true); err != nil || n != 0 || restarted.providerCalls != 1 {
		t.Fatalf("a retired envelope must never dispatch again, got %d dispatched, %d calls, err %v", n, restarted.providerCalls, err)
	}
}

func TestRealDBDispatcherFinalizesSavedResultAfterPersistenceFailure(t *testing.T) {
	f := newDispatchFixture(t)
	queue := &savedQueue{}
	failing := &failingCharacterSaves{Repository: f.repo, fail: true}
	if rec := f.generate(f.server(failing, queue.client())); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save failure must be reported, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
	jobID, status := f.jobStatus(t)
	if status != "running" || f.referenceURL(t) != "" || queue.providerCalls != 1 {
		t.Fatalf("provider finished but result was not linked, got status %s", status)
	}

	// Go restarts, the database works again: the saved result is applied without another provider run.
	server := f.server(f.repo, queue.client())
	if n, err := server.DispatchPending(f.ctx, true); err != nil || n != 1 {
		t.Fatalf("saved result must be reconciled, got %d, err %v", n, err)
	}
	if _, status := f.jobStatus(t); status != "done" {
		t.Fatalf("job must finish, got %s", status)
	}
	if got := f.referenceURL(t); got != queue.saved[jobID].ImageURL {
		t.Fatalf("character must link the same saved artifact, got %q", got)
	}
	if queue.providerCalls != 1 || f.ledger(t, "debit") != 1 || f.ledger(t, "refund") != 0 {
		t.Fatalf("one provider call and one charge expected, got %d calls", queue.providerCalls)
	}
}

func TestRealDBDispatcherKeepsUncertainOutcomeDebited(t *testing.T) {
	f := newDispatchFixture(t)
	crash := &savedQueue{before: func(gen.GenerateCharacterReferenceRequest) error { panic("process killed mid-request") }}
	func() {
		defer func() { _ = recover() }()
		f.generate(f.server(f.repo, crash.client()))
	}()
	jobID, _ := f.jobStatus(t)
	unknown := &savedQueue{before: func(gen.GenerateCharacterReferenceRequest) error {
		return &gen.AIServiceError{Code: "OUTCOME_UNKNOWN", Message: "Generation outcome is unknown.", StatusCode: http.StatusConflict}
	}}
	if _, err := f.server(f.repo, unknown.client()).DispatchPending(f.ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, status := f.jobStatus(t); status != "uncertain" {
		t.Fatalf("expired unknown result must stay uncertain, got %s", status)
	}
	if f.ledger(t, "debit") != 1 || f.ledger(t, "refund") != 0 || f.referenceURL(t) != "" {
		t.Fatal("an uncertain outcome keeps the debit and links nothing")
	}
	if state, _ := f.dispatchState(t, jobID); state != "delivered" {
		t.Fatalf("uncertain outcome must not be retried, got %s", state)
	}
}

func TestRealDBDispatcherFailsAndRefundsAfterBoundedRetries(t *testing.T) {
	f := newDispatchFixture(t)
	crash := &savedQueue{before: func(gen.GenerateCharacterReferenceRequest) error { panic("process killed") }}
	func() {
		defer func() { _ = recover() }()
		f.generate(f.server(f.repo, crash.client()))
	}()
	jobID, _ := f.jobStatus(t)
	down := &savedQueue{before: func(gen.GenerateCharacterReferenceRequest) error { return errors.New("connection refused") }}
	server := f.server(f.repo, down.client())
	for attempt := 1; attempt < 5; attempt++ {
		if _, err := server.DispatchPending(f.ctx, true); err != nil {
			t.Fatal(err)
		}
		if _, status := f.jobStatus(t); status != "running" {
			t.Fatalf("an unreachable queue must not fail the job on attempt %d, got %s", attempt, status)
		}
		if state, attempts := f.dispatchState(t, jobID); state != "pending" || attempts != attempt {
			t.Fatalf("attempt %d must stay queued, got %s after %d attempts", attempt, state, attempts)
		}
	}
	if _, err := server.DispatchPending(f.ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, status := f.jobStatus(t); status != "failed" {
		t.Fatalf("retries are bounded, expected failed, got %s", status)
	}
	if f.ledger(t, "refund") != 1 || down.providerCalls != 0 {
		t.Fatal("a job that never reached a provider must be refunded exactly once")
	}
	if n, err := server.DispatchPending(f.ctx, true); err != nil || n != 0 {
		t.Fatalf("a failed job must leave the outbox, got %d, err %v", n, err)
	}
}
