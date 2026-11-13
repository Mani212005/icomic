//go:build realdb

package db_test

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

type cancelAnswer struct {
	Outcome  string  `json:"outcome"`
	Refunded float64 `json:"refunded"`
	Message  string  `json:"message"`
	Job      struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"job"`
}

// cancel posts the public cancel request as the given user.
func (f *dispatchFixture) cancel(t *testing.T, server *api.Server, jobID, userID string) (int, cancelAnswer) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/generation-jobs/"+jobID+"/cancel", nil)
	req.Header.Set("X-User-ID", userID)
	server.NewRouter().ServeHTTP(rec, req)
	var answer cancelAnswer
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

func (f *dispatchFixture) balance(t *testing.T) float64 {
	t.Helper()
	var balance float64
	must(t, f.pool.QueryRowContext(f.ctx, `SELECT COALESCE(SUM(CASE transaction_type WHEN 'debit' THEN -amount ELSE amount END),0) FROM credit_ledger WHERE user_id=$1::uuid`, f.user.ID).Scan(&balance))
	return balance
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func (f *dispatchFixture) quotaRows(t *testing.T) int {
	return f.count(t, `SELECT COUNT(*) FROM image_quota_usage WHERE project_id=$1::uuid`, f.project.ID)
}

// A pending job that never reached the provider is really stopped: terminal cancelled, its envelope
// cancelled so the dispatcher never sends it, no provider call, one refund visible in the ledger,
// and the book budget slot released. Repeating the cancel changes and refunds nothing more.
func TestRealDBCancelPendingStopsTheJobAndRefundsOnce(t *testing.T) {
	f := newDispatchFixture(t)
	queue := &savedQueue{}
	crashed := f.crashedServer(queue.client())
	start := f.balance(t)
	jobID, _, _, _ := receiptOf(t, f.generate(crashed))
	if !near(f.balance(t), start-0.01) || f.quotaRows(t) != 1 {
		t.Fatalf("setup: expected one debit and one quota slot, got balance %v and %d slots", f.balance(t), f.quotaRows(t))
	}

	server := f.server(f.repo, queue.client())
	code, answer := f.cancel(t, server, jobID, f.user.ID)
	if code != http.StatusOK || answer.Outcome != "cancelled" || answer.Job.Status != "cancelled" || answer.Refunded != 0.01 {
		t.Fatalf("expected an honest cancelled answer with the refund, got HTTP %d %+v", code, answer)
	}
	if !strings.Contains(answer.Message, "Nothing was charged") {
		t.Fatalf("the message must say nothing was charged, got %q", answer.Message)
	}
	if _, status := f.jobStatus(t); status != "cancelled" {
		t.Fatalf("job must be terminal cancelled, got %s", status)
	}
	if state, attempts := f.dispatchState(t, jobID); state != "cancelled" || attempts != 0 {
		t.Fatalf("the envelope must be cancelled and never claimed, got %s after %d attempts", state, attempts)
	}
	if f.ledger(t, "debit") != 1 || f.ledger(t, "refund") != 1 || !near(f.balance(t), start) || f.quotaRows(t) != 0 {
		t.Fatalf("expected one debit, one refund, the balance restored and the quota slot released, got %d/%d %v %d",
			f.ledger(t, "debit"), f.ledger(t, "refund"), f.balance(t), f.quotaRows(t))
	}

	// The dispatcher, including restart recovery, never sends it.
	if n, err := server.DispatchPending(f.ctx, true); err != nil || n != 0 {
		t.Fatalf("a cancelled envelope must never be claimed, got %d, err %v", n, err)
	}
	if queue.providerCalls != 0 {
		t.Fatalf("no provider call may happen for a cancelled job, got %d", queue.providerCalls)
	}

	// Repeats are idempotent: the same answer, one refund.
	for i := 0; i < 3; i++ {
		code, again := f.cancel(t, server, jobID, f.user.ID)
		if code != http.StatusOK || again.Outcome != "cancelled" || again.Refunded != 0.01 {
			t.Fatalf("repeat %d: expected the same cancelled answer, got HTTP %d %+v", i, code, again)
		}
	}
	if f.ledger(t, "refund") != 1 || !near(f.balance(t), start) {
		t.Fatalf("repeated cancels must refund exactly once, got %d refunds, balance %v", f.ledger(t, "refund"), f.balance(t))
	}
	// The user can generate again with a fresh key.
	if rec := f.generate(f.crashedServer(queue.client())); rec.Code != http.StatusAccepted {
		t.Fatalf("a cancelled job must not block a new generation, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
}

// Cancel is pending-only. A job already with the provider is settling: nothing is stopped or
// refunded, the result and cost are kept, and the job is never reversed.
func TestRealDBCancelWhileRunningIsSettlingAndKeepsResultAndCost(t *testing.T) {
	f := newDispatchFixture(t)
	start := f.balance(t)
	f.generate(f.crashedServer((&savedQueue{}).client()))
	jobID, _ := f.jobStatus(t)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	queue := &savedQueue{before: func(gen.GenerateCharacterReferenceRequest) error {
		started <- struct{}{}
		<-release
		return nil
	}}
	server := f.server(f.repo, queue.client())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = server.DispatchPending(f.ctx, true)
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the provider call never started")
	}

	code, answer := f.cancel(t, server, jobID, f.user.ID)
	if code != http.StatusOK || answer.Outcome != "settling" || answer.Job.Status != "running" || answer.Refunded != 0 {
		t.Fatalf("a job already with the provider must be settling, got HTTP %d %+v", code, answer)
	}
	if !strings.Contains(answer.Message, "you will be charged") {
		t.Fatalf("the message must say the user will be charged, got %q", answer.Message)
	}
	if code, again := f.cancel(t, server, jobID, f.user.ID); code != http.StatusOK || again.Outcome != "settling" {
		t.Fatalf("repeating a settling cancel stays settling, got HTTP %d %+v", code, again)
	}
	if f.ledger(t, "refund") != 0 {
		t.Fatal("a settling job must not be refunded")
	}

	close(release)
	<-finished
	if _, status := f.jobStatus(t); status != "done" {
		t.Fatalf("the settling job must finish with its result, got %s", status)
	}
	if !strings.HasSuffix(f.referenceURL(t), jobID+".png") || queue.providerCalls != 1 {
		t.Fatalf("the result must be kept and linked after a cancel request, got %q after %d calls", f.referenceURL(t), queue.providerCalls)
	}
	if f.ledger(t, "debit") != 1 || f.ledger(t, "refund") != 0 || !near(f.balance(t), start-0.01) {
		t.Fatalf("the cost must be kept, got %d debits, %d refunds, balance %v", f.ledger(t, "debit"), f.ledger(t, "refund"), f.balance(t))
	}
	// Once finished, a cancel says so and changes nothing; terminal states are never reversed.
	if code, last := f.cancel(t, server, jobID, f.user.ID); code != http.StatusOK || last.Outcome != "finished" || last.Job.Status != "done" {
		t.Fatalf("a finished job must be reported finished, got HTTP %d %+v", code, last)
	}
	jobService := jobs.NewPersistentJobService(f.repo)
	if err := jobService.UpdateJobStatus(f.ctx, jobID, jobs.StatusFailed, nil, nil); !errors.Is(err, jobs.ErrJobAlreadyTerminal) {
		t.Fatalf("a done job must never be reversed, got %v", err)
	}
	if _, status := f.jobStatus(t); status != "done" {
		t.Fatalf("status must stay done, got %s", status)
	}
}

// A completion or failure arriving after a cancel can never overwrite the cancelled status, and
// a cancelled job can never be claimed, charged again or finished.
func TestRealDBCancelledJobIsNeverOverwrittenClaimedOrFinished(t *testing.T) {
	f := newDispatchFixture(t)
	f.generate(f.crashedServer((&savedQueue{}).client()))
	jobID, _ := f.jobStatus(t)
	result, err := f.repo.CancelGeneration(f.ctx, jobID)
	must(t, err)
	if result.Outcome != db.CancelOutcomeCancelled {
		t.Fatalf("expected cancelled, got %s", result.Outcome)
	}
	service := jobs.NewPersistentJobService(f.repo)
	cost := 0.01
	for name, attempt := range map[string]func() error{
		"complete": func() error { return service.CompleteJob(f.ctx, jobID, cost) },
		"update":   func() error { return service.UpdateJobStatus(f.ctx, jobID, jobs.StatusDone, &cost, nil) },
		"fail":     func() error { return service.FailJob(f.ctx, jobID, "late failure", &cost) },
		"start":    func() error { return service.StartJob(f.ctx, jobID) },
		"uncertain": func() error {
			message := "late"
			return service.UpdateJobStatus(f.ctx, jobID, jobs.StatusUncertain, nil, &message)
		},
	} {
		if err := attempt(); !errors.Is(err, jobs.ErrJobAlreadyTerminal) {
			t.Fatalf("%s must be refused on a cancelled job, got %v", name, err)
		}
	}
	if job, err := service.GetJob(f.ctx, jobID); err != nil || job.Status != "cancelled" || job.ActualCost != nil {
		t.Fatalf("the cancelled job must be untouched, got %+v %v", job, err)
	}
	if claimed, err := f.repo.ClaimDispatches(f.ctx, 10, true); err != nil || len(claimed) != 0 {
		t.Fatalf("a cancelled job can never be claimed, got %d, err %v", len(claimed), err)
	}
	// The envelope stays cancelled even if something retires it afterwards.
	must(t, f.repo.FinishDispatch(f.ctx, jobID))
	if state, _ := f.dispatchState(t, jobID); state != "cancelled" {
		t.Fatalf("retiring must not rewrite a cancelled envelope, got %s", state)
	}
}

// Cancel and claim of the same envelope race: exactly one wins. Either the dispatcher claimed
// first (the cancel answers settling, nothing is refunded, the job is still sent) or the cancel
// committed first (the claim finds nothing, the job is cancelled and refunded once).
func TestRealDBCancelRacesClaimWithExactlyOneWinner(t *testing.T) {
	f := newDispatchFixture(t)
	var cancelWins, claimWins int
	for round := 0; round < 40; round++ {
		ch := &domain.Character{ProjectID: f.project.ID, Name: "Racer", Description: "Races"}
		must(t, f.repo.CreateCharacter(f.ctx, ch))
		job := &domain.GenerationJob{ID: uuid.NewString(), ProjectID: f.project.ID, UserID: f.user.ID, JobType: "character_reference", Stage: "reference", ModelID: "fake", IdempotencyKey: uuid.NewString(), HashedUserID: domain.HashUserID(f.user.ID)}
		_, _, err := f.repo.CreateGeneration(f.ctx, job, db.GenerationOptions{EntityID: ch.ID, Envelope: &db.GenerationEnvelope{Operation: "generate-character-reference", Request: []byte(`{}`)}}, .01)
		must(t, err)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var claimed []db.PendingDispatch
		var cancelled *db.CancelResult
		var claimErr, cancelErr error
		wg.Add(2)
		// Most rounds are a true race; a few delay one side so both winners are certain to occur.
		var claimDelay, cancelDelay time.Duration
		switch round % 4 {
		case 0:
			claimDelay = 15 * time.Millisecond
		case 1:
			cancelDelay = 15 * time.Millisecond
		}
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(claimDelay)
			claimed, claimErr = f.repo.ClaimDispatches(f.ctx, 10, false)
		}()
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(cancelDelay)
			cancelled, cancelErr = f.repo.CancelGeneration(f.ctx, job.ID)
		}()
		close(start)
		wg.Wait()
		must(t, claimErr)
		must(t, cancelErr)

		state, attempts := f.dispatchState(t, job.ID)
		var status string
		must(t, f.pool.QueryRowContext(f.ctx, `SELECT status FROM generation_jobs WHERE id=$1::uuid`, job.ID).Scan(&status))
		refunds := f.count(t, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid AND transaction_type='refund'`, job.ID)
		mine := false
		for _, d := range claimed {
			mine = mine || d.JobID == job.ID
		}
		switch {
		case mine && cancelled.Outcome == db.CancelOutcomeSettling:
			claimWins++
			if status != "pending" || state != "pending" || attempts != 1 || refunds != 0 {
				t.Fatalf("round %d: claim won, expected a sent job and no refund, got %s %s %d %d", round, status, state, attempts, refunds)
			}
		case !mine && cancelled.Outcome == db.CancelOutcomeCancelled:
			cancelWins++
			if status != "cancelled" || state != "cancelled" || attempts != 0 || refunds != 1 {
				t.Fatalf("round %d: cancel won, expected a cancelled job and one refund, got %s %s %d %d", round, status, state, attempts, refunds)
			}
		default:
			t.Fatalf("round %d: not exactly one winner: claimed=%v cancel=%s", round, mine, cancelled.Outcome)
		}
		// Whatever happened, a later cancel and claim still agree with the first outcome.
		again, err := f.repo.CancelGeneration(f.ctx, job.ID)
		must(t, err)
		if again.Outcome != cancelled.Outcome {
			t.Fatalf("round %d: a repeated cancel changed the outcome from %s to %s", round, cancelled.Outcome, again.Outcome)
		}
		if refundsAfter := f.count(t, `SELECT COUNT(*) FROM credit_ledger WHERE generation_job_id=$1::uuid AND transaction_type='refund'`, job.ID); refundsAfter != refunds {
			t.Fatalf("round %d: a repeated cancel refunded again", round)
		}
	}
	if cancelWins == 0 || claimWins == 0 {
		t.Fatalf("both winners must occur over the rounds, got cancel %d, claim %d", cancelWins, claimWins)
	}
	t.Logf("race outcomes over 40 rounds: cancel won %d, claim won %d", cancelWins, claimWins)
}

func TestRealDBCancelRefusesAnotherUsersJob(t *testing.T) {
	f := newDispatchFixture(t)
	jobID, _, _, _ := receiptOf(t, f.generate(f.crashedServer((&savedQueue{}).client())))
	other := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	must(t, f.repo.CreateUser(f.ctx, other))
	server := f.server(f.repo, (&savedQueue{}).client())
	if code, _ := f.cancel(t, server, jobID, other.ID); code != http.StatusForbidden {
		t.Fatalf("a non-owner must be refused, got HTTP %d", code)
	}
	if code, _ := f.cancel(t, server, uuid.NewString(), f.user.ID); code != http.StatusNotFound {
		t.Fatalf("an unknown job is not found, got HTTP %d", code)
	}
	if _, status := f.jobStatus(t); status != "pending" || f.ledger(t, "refund") != 0 {
		t.Fatalf("a refused cancel must change nothing, got %s", status)
	}
}

func (f *dispatchFixture) setStopSwitch(t *testing.T, on bool) {
	t.Helper()
	_, err := f.pool.ExecContext(f.ctx, `UPDATE generation_policy SET kill_switch_active=$1 WHERE singleton=TRUE`, on)
	must(t, err)
}

// The global stop switch blocks paid generation only: new requests are refused with a structured
// error and unsent jobs are held (and can be cancelled for a refund), while reading jobs and
// exporting keep working.
func TestRealDBStopSwitchHoldsPaidGenerationButNotExportsOrReads(t *testing.T) {
	f := newDispatchFixture(t)
	queue := &savedQueue{}
	jobID, _, _, _ := receiptOf(t, f.generate(f.crashedServer(queue.client())))
	f.setStopSwitch(t, true)

	// A new paid request is refused with a structured error and creates nothing.
	rec := f.generateKeyed(f.server(f.repo, queue.client()), "after-stop")
	var refusal struct{ Code, Message string }
	_ = json.Unmarshal(rec.Body.Bytes(), &refusal)
	if rec.Code != http.StatusTooManyRequests || refusal.Code != "generation_stopped" || !strings.Contains(refusal.Message, "exports still work") {
		t.Fatalf("expected a structured generation_stopped refusal, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if n := f.count(t, `SELECT COUNT(*) FROM generation_jobs WHERE project_id=$1::uuid`, f.project.ID); n != 1 {
		t.Fatalf("a refused request must not create a job, got %d jobs", n)
	}

	// The pending-but-unsent job is held: not claimed, never sent.
	server := f.server(f.repo, queue.client())
	if n, err := server.DispatchPending(f.ctx, true); err != nil || n != 0 || queue.providerCalls != 0 {
		t.Fatalf("a held job must not be sent while the switch is on, got %d dispatched, %d calls, err %v", n, queue.providerCalls, err)
	}
	if claimed, err := f.repo.ClaimDispatches(f.ctx, 10, true); err != nil || len(claimed) != 0 {
		t.Fatalf("the store must not claim while the switch is on, got %d, err %v", len(claimed), err)
	}
	if got := f.pollJob(t, server, jobID); got != "pending" {
		t.Fatalf("reads keep working and show the held job pending, got %s", got)
	}

	// PDF/PNG export spends nothing at the provider: its job is admitted with the switch on.
	exportJob := &domain.GenerationJob{ID: uuid.NewString(), ProjectID: f.project.ID, UserID: f.user.ID, JobType: "pdf_assembly", Stage: "export", ModelID: "pdf-assembler", IdempotencyKey: uuid.NewString(), HashedUserID: domain.HashUserID(f.user.ID)}
	if _, _, err := f.repo.CreateGeneration(f.ctx, exportJob, db.GenerationOptions{KillSwitchActive: true}, 0); err != nil {
		t.Fatalf("export must still work while the stop switch is on, got %v", err)
	}
	// A text job calls the provider and is stopped like any other paid generation.
	textJob := &domain.GenerationJob{ID: uuid.NewString(), ProjectID: f.project.ID, UserID: f.user.ID, JobType: "script_analysis", Stage: "text", ModelID: "fake", IdempotencyKey: uuid.NewString(), HashedUserID: domain.HashUserID(f.user.ID)}
	if _, _, err := f.repo.CreateGeneration(f.ctx, textJob, db.GenerationOptions{}, 0); !errors.Is(err, domain.ErrKillSwitchActive) {
		t.Fatalf("paid text generation must be stopped, got %v", err)
	}

	// The held job can be cancelled for a full refund while the switch is on.
	if code, answer := f.cancel(t, server, jobID, f.user.ID); code != http.StatusOK || answer.Outcome != "cancelled" || f.ledger(t, "refund") != 1 {
		t.Fatalf("a held job must be cancellable, got HTTP %d %+v", code, answer)
	}

	// Turning the switch off resumes sending for jobs that were held.
	f.setStopSwitch(t, false)
	resumed := &domain.Character{ProjectID: f.project.ID, Name: "Later", Description: "Later"}
	must(t, f.repo.CreateCharacter(f.ctx, resumed))
	f.setStopSwitch(t, true)
	held := &domain.GenerationJob{ID: uuid.NewString(), ProjectID: f.project.ID, UserID: f.user.ID, JobType: "character_reference", Stage: "reference", ModelID: "fake", IdempotencyKey: uuid.NewString(), HashedUserID: domain.HashUserID(f.user.ID)}
	f.setStopSwitch(t, false)
	_, _, err := f.repo.CreateGeneration(f.ctx, held, db.GenerationOptions{EntityID: resumed.ID, Envelope: &db.GenerationEnvelope{Operation: "generate-character-reference", Request: []byte(`{"character_id":"` + resumed.ID + `"}`)}}, .01)
	must(t, err)
	f.setStopSwitch(t, true)
	if n, _ := server.DispatchPending(f.ctx, true); n != 0 {
		t.Fatalf("held while on, got %d", n)
	}
	f.setStopSwitch(t, false)
	if n, err := server.DispatchPending(f.ctx, true); err != nil || n != 1 {
		t.Fatalf("a held job is sent once the switch is off, got %d, err %v", n, err)
	}
}
