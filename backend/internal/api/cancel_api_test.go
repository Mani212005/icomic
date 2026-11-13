package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/domain"
)

// cancelFixture is one user with one character and a provider the test controls.
type cancelFixture struct {
	t         *testing.T
	server    *api.Server
	handler   http.Handler
	character *domain.Character
	calls     int
	started   chan string
	release   chan struct{}
}

func newCancelFixture(t *testing.T) *cancelFixture {
	t.Helper()
	s, handler, repo := setupTestServer(true, "test-user")
	project := &domain.Project{UserID: "test-user", OriginalScript: "A hero saves the town.", Title: "Comic"}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	character := &domain.Character{ProjectID: project.ID, Name: "Hero"}
	if err := repo.CreateCharacter(context.Background(), character); err != nil {
		t.Fatal(err)
	}
	f := &cancelFixture{t: t, server: s, handler: handler, character: character, started: make(chan string, 4), release: make(chan struct{})}
	s.AIClient.(*gen.FakeAIServiceClient).GenerateCharacterReferenceFunc = func(ctx context.Context, req gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
		f.calls++
		f.started <- req.JobID
		<-f.release
		return &gen.GenerateCharacterReferenceResponse{JobID: req.JobID, ImageURL: "https://images.example/" + req.JobID + ".png", ModelID: "fake"}, nil
	}
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
		s.WaitDispatches()
	})
	return f
}

func (f *cancelFixture) generate(key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+f.character.ID+"/reference-image", strings.NewReader(`{}`))
	req.Header.Set("X-User-ID", "test-user")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// generateUnsent commits a job whose envelope nobody delivers, like a backend that crashed right
// after the receipt, so the test can act while the job is still pending and unsent.
func (f *cancelFixture) generateUnsent() string {
	f.t.Helper()
	dispatches := f.server.Dispatches
	f.server.Dispatches = nil
	defer func() { f.server.Dispatches = dispatches }()
	return decodeReceipt(f.t, f.generate("")).JobID
}

func (f *cancelFixture) cancel(jobID, user string) (int, gen.CancelJobResponse) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/generation-jobs/"+jobID+"/cancel", nil)
	req.Header.Set("X-User-ID", user)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	var body gen.CancelJobResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func (f *cancelFixture) ledger() (debits, refunds int, balance float64) {
	history, err := f.server.CreditService.GetUsageHistory(context.Background(), "test-user")
	if err != nil {
		f.t.Fatal(err)
	}
	for _, entry := range history {
		switch entry.TransactionType {
		case "debit":
			debits++
		case "refund":
			refunds++
		}
	}
	balance, _ = f.server.CreditService.GetBalance(context.Background(), "test-user")
	return
}

func (f *cancelFixture) status(jobID string) string {
	job, err := f.server.JobService.GetJob(context.Background(), jobID)
	if err != nil {
		f.t.Fatal(err)
	}
	return job.Status
}

func TestCancelPendingJobNeverReachesProviderAndRefundsOnce(t *testing.T) {
	f := newCancelFixture(t)
	jobID := f.generateUnsent()
	if debits, refunds, balance := f.ledger(); debits != 1 || refunds != 0 || balance >= 50 {
		t.Fatalf("setup: expected one debit, got %d debits %d refunds balance %v", debits, refunds, balance)
	}

	code, answer := f.cancel(jobID, "test-user")
	if code != http.StatusOK || answer.Outcome != gen.CancelJobResponseOutcome("cancelled") || answer.Job.Status != "cancelled" || answer.Refunded <= 0 {
		t.Fatalf("expected an honest cancelled answer with the refund, got HTTP %d %+v", code, answer)
	}
	if !strings.Contains(answer.Message, "Nothing was charged") {
		t.Fatalf("the message must say nothing was charged, got %q", answer.Message)
	}
	debits, refunds, balance := f.ledger()
	if debits != 1 || refunds != 1 || balance != 50 {
		t.Fatalf("expected one debit, one visible refund and the balance restored, got %d %d %v", debits, refunds, balance)
	}
	if f.status(jobID) != "cancelled" {
		t.Fatalf("job must be terminal cancelled, got %s", f.status(jobID))
	}

	// The dispatcher never sends it, even when it recovers everything.
	if n, err := f.server.DispatchPending(context.Background(), true); err != nil || n != 0 {
		t.Fatalf("a cancelled job must not be dispatched, got %d, err %v", n, err)
	}
	if f.calls != 0 {
		t.Fatalf("no provider call may happen for a cancelled job, got %d", f.calls)
	}

	// Repeating the cancel is idempotent: same answer, one refund.
	for i := 0; i < 2; i++ {
		code, again := f.cancel(jobID, "test-user")
		if code != http.StatusOK || again.Outcome != gen.CancelJobResponseOutcome("cancelled") || again.Refunded != answer.Refunded {
			t.Fatalf("repeat %d: expected the same answer, got HTTP %d %+v", i, code, again)
		}
	}
	if _, refunds, balance := f.ledger(); refunds != 1 || balance != 50 {
		t.Fatalf("repeated cancels must refund once, got %d refunds, balance %v", refunds, balance)
	}
}

func TestCancelRunningJobIsSettlingAndKeepsResultAndCost(t *testing.T) {
	f := newCancelFixture(t)
	jobID := decodeReceipt(t, f.generate("")).JobID
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the provider call never started")
	}

	code, answer := f.cancel(jobID, "test-user")
	if code != http.StatusOK || answer.Outcome != gen.CancelJobResponseOutcome("settling") || answer.Job.Status != "running" || answer.Refunded != 0 {
		t.Fatalf("a job already with the provider must be settling, got HTTP %d %+v", code, answer)
	}
	if !strings.Contains(answer.Message, "you will be charged") {
		t.Fatalf("the message must say the user will be charged, got %q", answer.Message)
	}
	if _, refunds, _ := f.ledger(); refunds != 0 {
		t.Fatal("a settling job is not refunded")
	}

	close(f.release)
	f.server.WaitDispatches()
	if f.status(jobID) != "done" || f.calls != 1 {
		t.Fatalf("the settling job must finish with its result, got %s after %d calls", f.status(jobID), f.calls)
	}
	if debits, refunds, _ := f.ledger(); debits != 1 || refunds != 0 {
		t.Fatalf("the cost is kept, got %d debits %d refunds", debits, refunds)
	}
	if code, last := f.cancel(jobID, "test-user"); code != http.StatusOK || last.Outcome != gen.CancelJobResponseOutcome("finished") || last.Job.Status != "done" {
		t.Fatalf("a finished job stays finished, got HTTP %d %+v", code, last)
	}
}

func TestCancelRefusesAnotherUserAndUnknownJobs(t *testing.T) {
	f := newCancelFixture(t)
	jobID := f.generateUnsent()
	if code, _ := f.cancel(jobID, "user-2"); code != http.StatusForbidden {
		t.Fatalf("a non-owner must be refused, got HTTP %d", code)
	}
	if code, _ := f.cancel("5b0e3f2a-9c1d-4c55-8e2b-7d1a1f0c3b11", "test-user"); code != http.StatusNotFound {
		t.Fatalf("an unknown job is not found, got HTTP %d", code)
	}
	if f.status(jobID) != "pending" {
		t.Fatal("a refused cancel must change nothing")
	}
	if _, refunds, _ := f.ledger(); refunds != 0 {
		t.Fatal("a refused cancel must not refund")
	}
}

// The global stop switch refuses new paid generation with a structured error, holds jobs that are
// still unsent (cancellable for a refund) and leaves reads and exports alone.
func TestStopSwitchBlocksPaidGenerationButNotReadsOrExports(t *testing.T) {
	f := newCancelFixture(t)
	jobID := f.generateUnsent()
	f.server.KillSwitch.SetActive(true)

	rec := f.generate("after-stop")
	var refusal struct{ Code, Message string }
	_ = json.Unmarshal(rec.Body.Bytes(), &refusal)
	if rec.Code != http.StatusTooManyRequests || refusal.Code != "generation_stopped" || !strings.Contains(refusal.Message, "exports still work") {
		t.Fatalf("expected a structured generation_stopped refusal, got HTTP %d: %s", rec.Code, rec.Body.String())
	}

	// The unsent job is held, never sent, still readable, and cancellable.
	if n, err := f.server.DispatchPending(context.Background(), true); err != nil || n != 0 || f.calls != 0 {
		t.Fatalf("a held job must not be sent while the switch is on, got %d dispatched, %d calls, err %v", n, f.calls, err)
	}
	read := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generation-jobs/"+jobID, nil)
	req.Header.Set("X-User-ID", "test-user")
	f.handler.ServeHTTP(read, req)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"status":"pending"`) {
		t.Fatalf("reads keep working, got %d %s", read.Code, read.Body.String())
	}
	if code, answer := f.cancel(jobID, "test-user"); code != http.StatusOK || answer.Outcome != gen.CancelJobResponseOutcome("cancelled") {
		t.Fatalf("a held job can be cancelled, got HTTP %d %+v", code, answer)
	}

	// Turning the switch off sends what was held.
	f.server.KillSwitch.SetActive(false)
	held := f.generateUnsent()
	f.server.KillSwitch.SetActive(true)
	if n, _ := f.server.DispatchPending(context.Background(), true); n != 0 || f.calls != 0 {
		t.Fatalf("held while the switch is on, got %d dispatched", n)
	}
	f.server.KillSwitch.SetActive(false)
	close(f.release)
	if n, err := f.server.DispatchPending(context.Background(), true); err != nil || n != 1 || f.status(held) != "done" {
		t.Fatalf("a held job is sent once the switch is off, got %d, status %s, err %v", n, f.status(held), err)
	}
}

func TestStopSwitchStillAllowsExport(t *testing.T) {
	server, handler, repo, projectID, pageID := paintedPageFixture(t)
	approvedAt := time.Now().UTC().Add(time.Minute)
	pg, err := repo.GetPage(context.Background(), pageID)
	if err != nil {
		t.Fatal(err)
	}
	pg.LineArtImageURL = ""
	pg.PaintedPageImageURL = "http://storage/painted-page.png"
	pg.PaintedPageApprovedAt = &approvedAt
	if err := repo.UpdatePage(context.Background(), pg); err != nil {
		t.Fatal(err)
	}
	seedProvenance(t, repo, projectID, "http://storage/painted-page.png")

	server.KillSwitch.SetActive(true)
	if rec := postExport(handler, projectID, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("export must keep working while the stop switch is on, got %d: %s", rec.Code, rec.Body.String())
	}
}
