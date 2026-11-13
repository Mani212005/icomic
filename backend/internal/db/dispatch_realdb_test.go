//go:build realdb

package db_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

// crashedServer is a backend process that dies right after returning its receipts: nothing
// delivers the committed envelopes until another process starts.
func (f *dispatchFixture) crashedServer(ai gen.AIService) *api.Server {
	server := f.server(f.repo, ai)
	server.Dispatches = nil
	return server
}

func (f *dispatchFixture) generate(server *api.Server) *httptest.ResponseRecorder {
	return f.generateKeyed(server, "")
}

func (f *dispatchFixture) generateKeyed(server *api.Server, key string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+f.character.ID+"/reference-image", strings.NewReader(`{}`))
	req.Header.Set("X-User-ID", f.user.ID)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
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
	// A committed envelope is claimable at once, a claim takes at most the limit and counts the attempt.
	first, err := f.repo.ClaimDispatches(f.ctx, 2, false)
	must(t, err)
	if len(first) != 2 || first[0].Attempts != 1 || first[1].Attempts != 1 {
		t.Fatalf("a claim must take at most the limit and count the attempt, got %+v", first)
	}
	// Each claim leases what it took, so no other claimer hands the same job out twice.
	rest, err := f.repo.ClaimDispatches(f.ctx, 10, false)
	must(t, err)
	if len(rest) != 3 {
		t.Fatalf("the unclaimed envelopes remain, got %d", len(rest))
	}
	if again, err := f.repo.ClaimDispatches(f.ctx, 10, false); err != nil || len(again) != 0 {
		t.Fatalf("claimed envelopes are leased, got %d reclaimed, err %v", len(again), err)
	}
	// Startup recovery takes what a previous process left, whatever its lease.
	if recovered, err := f.repo.ClaimDispatches(f.ctx, 10, true); err != nil || len(recovered) != 5 || recovered[0].Attempts != 2 {
		t.Fatalf("recovery must take every leased envelope, got %d, err %v", len(recovered), err)
	}
}

func TestRealDBDispatcherSendsJobLostBeforeQueueAcceptanceOnce(t *testing.T) {
	f := newDispatchFixture(t)
	crash := &savedQueue{}
	if rec := f.generate(f.crashedServer(crash.client())); rec.Code != http.StatusAccepted {
		t.Fatalf("the receipt precedes any delivery, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
	jobID, status := f.jobStatus(t)
	if status != "pending" || f.ledger(t, "debit") != 1 || crash.providerCalls != 0 {
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
	failingServer := f.server(failing, queue.client())
	if rec := f.generate(failingServer); rec.Code != http.StatusAccepted {
		t.Fatalf("the receipt does not wait for the save, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
	failingServer.WaitDispatches()
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
	f.generate(f.crashedServer((&savedQueue{}).client()))
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
	f.generate(f.crashedServer((&savedQueue{}).client()))
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

// receiptOf decodes a generation receipt: the job and the entity a client resumes from.
func receiptOf(t *testing.T, rec *httptest.ResponseRecorder) (jobID, status, entityType, entityID string) {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected a 202 receipt, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Job        struct{ ID, Status string } `json:"job"`
		JobID      string                      `json:"job_id"`
		EntityType string                      `json:"entity_type"`
		EntityID   string                      `json:"entity_id"`
		ImageURL   string                      `json:"image_url"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &body))
	if body.Job.ID == "" || body.Job.ID != body.JobID || body.ImageURL != "" {
		t.Fatalf("malformed receipt: %s", rec.Body.String())
	}
	return body.Job.ID, body.Job.Status, body.EntityType, body.EntityID
}

func (f *dispatchFixture) pollJob(t *testing.T, server *api.Server, jobID string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generation-jobs/"+jobID, nil)
	req.Header.Set("X-User-ID", f.user.ID)
	server.NewRouter().ServeHTTP(rec, req)
	var body struct {
		Job struct{ Status string } `json:"job"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Job.Status
}

// A slow provider must not hold the request. The receipt follows the commit of the job, the debit
// and the envelope; the client polls by job ID; a repeated key returns the same job at any point
// and never charges or calls the provider again.
func TestRealDBReceiptPrecedesSlowProviderAndRepeatedKeyNeverChargesTwice(t *testing.T) {
	f := newDispatchFixture(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	queue := &savedQueue{before: func(gen.GenerateCharacterReferenceRequest) error {
		started <- struct{}{}
		<-release
		return nil
	}}
	server := f.server(f.repo, queue.client())

	begin := time.Now()
	rec := f.generateKeyed(server, "click-1")
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("receipt took %s while the provider was still running", elapsed)
	}
	jobID, status, entityType, entityID := receiptOf(t, rec)
	if (status != "pending" && status != "running") || entityType != "character" || entityID != f.character.ID {
		t.Fatalf("receipt must name an unfinished job and its entity, got %s %s %s", status, entityType, entityID)
	}
	if f.ledger(t, "debit") != 1 {
		t.Fatal("the receipt may only follow the committed debit")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the dispatcher never started the provider call")
	}
	if got := f.pollJob(t, server, jobID); got != "running" {
		t.Fatalf("polling a job in flight must report running, got %s", got)
	}
	if state, _ := f.dispatchState(t, jobID); state != "pending" {
		t.Fatalf("the envelope stays durable until the job is terminal, got %s", state)
	}

	// The connection dropped and the browser retries: the same job, no second charge or provider call.
	if again, _, _, _ := receiptOf(t, f.generateKeyed(server, "click-1")); again != jobID {
		t.Fatalf("a repeated key must return the original job %s, got %s", jobID, again)
	}
	// A second click without a key is refused while the first job is active.
	if rec := f.generate(server); rec.Code != http.StatusConflict {
		t.Fatalf("a duplicate click must be refused while a job is active, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if f.ledger(t, "debit") != 1 {
		t.Fatalf("recovery and duplicate clicks must not charge again, got %d debits", f.ledger(t, "debit"))
	}

	close(release)
	server.WaitDispatches()
	if got := f.pollJob(t, server, jobID); got != "done" {
		t.Fatalf("the job must finish, got %s", got)
	}
	if got := f.referenceURL(t); !strings.HasSuffix(got, jobID+".png") {
		t.Fatalf("the finished entity must carry the artifact, got %q", got)
	}
	if again, status, _, _ := receiptOf(t, f.generateKeyed(server, "click-1")); again != jobID || status != "done" {
		t.Fatalf("a repeat after completion returns the finished job, got %s %s", again, status)
	}
	if queue.providerCalls != 1 || f.ledger(t, "debit") != 1 || f.ledger(t, "refund") != 0 {
		t.Fatalf("one job is one provider call and one charge, got %d calls %d debits", queue.providerCalls, f.ledger(t, "debit"))
	}
	if state, _ := f.dispatchState(t, jobID); state != "delivered" {
		t.Fatalf("a finished job retires its envelope, got %s", state)
	}
}

// The process stops after the receipt while the provider is running. A restart takes the envelope
// back, the queue returns its saved result, and the job finishes with one provider call and charge.
func TestRealDBRestartAfterReceiptFinishesJobWithoutSecondCharge(t *testing.T) {
	f := newDispatchFixture(t)
	queue := &savedQueue{}
	first := f.server(f.repo, queue.client())
	first.Dispatches = nil // the process dies before delivering anything
	jobID, _, _, _ := receiptOf(t, f.generateKeyed(first, "click-restart"))

	restarted := f.server(f.repo, queue.client())
	if n, err := restarted.DispatchPending(f.ctx, true); err != nil || n != 1 {
		t.Fatalf("restart must deliver the committed envelope, got %d, err %v", n, err)
	}
	if got := f.pollJob(t, restarted, jobID); got != "done" {
		t.Fatalf("job must finish after the restart, got %s", got)
	}
	// The browser, which never saw the result, reloads and repeats its request with the same key.
	if again, status, _, _ := receiptOf(t, f.generateKeyed(restarted, "click-restart")); again != jobID || status != "done" {
		t.Fatalf("recovery by key returns the finished job, got %s %s", again, status)
	}
	if queue.providerCalls != 1 || f.ledger(t, "debit") != 1 {
		t.Fatalf("one provider call and one charge expected, got %d calls %d debits", queue.providerCalls, f.ledger(t, "debit"))
	}
}
