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
	"icomic/backend/internal/domain"
)

// receiptFixture is a character whose provider call blocks until the test releases it, like a slow
// image model.
type receiptFixture struct {
	t         *testing.T
	handler   http.Handler
	started   chan string
	release   chan struct{}
	calls     int
	character *domain.Character
}

func newReceiptFixture(t *testing.T) (*receiptFixture, func() int) {
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
	f := &receiptFixture{t: t, handler: handler, started: make(chan string, 4), release: make(chan struct{}), character: character}
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
	debits := func() int {
		history, err := s.CreditService.GetUsageHistory(context.Background(), "test-user")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, entry := range history {
			if entry.TransactionType == "debit" {
				n++
			}
		}
		return n
	}
	return f, debits
}

func (f *receiptFixture) post(key string) (*httptest.ResponseRecorder, time.Duration) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+f.character.ID+"/reference-image", strings.NewReader(`{}`))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	begin := time.Now()
	f.handler.ServeHTTP(rec, req)
	return rec, time.Since(begin)
}

func (f *receiptFixture) job(id string) domain.GenerationJob {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/generation-jobs/"+id, nil))
	if rec.Code != http.StatusOK {
		f.t.Fatalf("poll: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Job domain.GenerationJob `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatal(err)
	}
	return body.Job
}

type receiptBody struct {
	Job        domain.GenerationJob `json:"job"`
	JobID      string               `json:"job_id"`
	Status     string               `json:"status"`
	EntityType string               `json:"entity_type"`
	EntityID   string               `json:"entity_id"`
	ProjectID  string               `json:"project_id"`
	ImageURL   string               `json:"image_url"`
}

func decodeReceipt(t *testing.T, rec *httptest.ResponseRecorder) receiptBody {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected a 202 receipt, got %d: %s", rec.Code, rec.Body.String())
	}
	var receipt receiptBody
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A slow provider must not hold the HTTP request: the receipt names the job and the entity to
// resume, the client polls the job, and the finished entity appears when the job is done.
func TestGenerationReturnsReceiptBeforeSlowProviderFinishes(t *testing.T) {
	f, debits := newReceiptFixture(t)

	rec, elapsed := f.post("")
	receipt := decodeReceipt(t, rec)
	if elapsed > time.Second {
		t.Fatalf("receipt took %s while the provider was still running", elapsed)
	}
	if receipt.JobID == "" || receipt.Job.ID != receipt.JobID || receipt.EntityType != "character" || receipt.EntityID != f.character.ID || receipt.ProjectID != f.character.ProjectID {
		t.Fatalf("receipt lacks the job and the entity needed to resume: %s", rec.Body.String())
	}
	if receipt.Status != "pending" && receipt.Status != "running" {
		t.Fatalf("a receipt describes unfinished work, got status %q", receipt.Status)
	}
	if receipt.ImageURL != "" {
		t.Fatal("a receipt carries no result: the image is read from the entity once the job is done")
	}
	if debits() != 1 {
		t.Fatal("the debit must be committed before the receipt is returned")
	}

	// The provider is running; the job is observable by ID and unfinished.
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatcher never started the provider call")
	}
	if status := f.job(receipt.JobID).Status; status != "running" {
		t.Fatalf("polling a job in flight must report running, got %s", status)
	}

	close(f.release)
	waitFor(t, "job done", func() bool { return f.job(receipt.JobID).Status == "done" })
	listRec := httptest.NewRecorder()
	f.handler.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+f.character.ProjectID+"/characters", nil))
	if !strings.Contains(listRec.Body.String(), "https://images.example/"+receipt.JobID+".png") {
		t.Fatalf("the finished character must carry the generated image: %s", listRec.Body.String())
	}
	if debits() != 1 || f.calls != 1 {
		t.Fatalf("one job means one debit and one provider call, got %d debits %d calls", debits(), f.calls)
	}
}

// A client that lost its connection repeats the request with the same key and recovers the same
// job, whether the provider is still running or already finished, without a second charge.
func TestRepeatedKeyRecoversTheSameJobWithoutSecondCharge(t *testing.T) {
	f, debits := newReceiptFixture(t)

	first, _ := f.post("flaky-tunnel-click")
	original := decodeReceipt(t, first)
	<-f.started

	again, elapsed := f.post("flaky-tunnel-click")
	if recovered := decodeReceipt(t, again); recovered.JobID != original.JobID {
		t.Fatalf("a repeated key must return the original job %s, got %s", original.JobID, recovered.JobID)
	} else if recovered.Status != "running" || elapsed > time.Second {
		t.Fatalf("the recovered receipt reports the job in flight promptly, got %s after %s", recovered.Status, elapsed)
	}

	close(f.release)
	waitFor(t, "job done", func() bool { return f.job(original.JobID).Status == "done" })

	late, _ := f.post("flaky-tunnel-click")
	if recovered := decodeReceipt(t, late); recovered.JobID != original.JobID || recovered.Status != "done" {
		t.Fatalf("a repeat after completion returns the finished job, got %+v", recovered)
	}
	if debits() != 1 || f.calls != 1 {
		t.Fatalf("recovery must not charge or call the provider again, got %d debits %d calls", debits(), f.calls)
	}
}
