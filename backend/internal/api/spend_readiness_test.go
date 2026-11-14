package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

func creditUsage(t *testing.T, handler http.Handler) map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me/credit-usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("credit usage: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCreditUsageCarriesRunningProviderSpendAndCapRemaining(t *testing.T) {
	s, handler, _ := setupTestServer(true, "test-user")
	cap, spent, left, calls := 2.0, 0.37, 1.63, 5
	s.AIClient.(*gen.FakeAIServiceClient).ProviderSpendFunc = func(context.Context) (*gen.ProviderSpendResponse, error) {
		return &gen.ProviderSpendResponse{Enabled: true, CapUsd: &cap, SpentUsd: &spent, RemainingUsd: &left, Calls: &calls}, nil
	}
	spend, ok := creditUsage(t, handler)["provider_spend"].(map[string]interface{})
	if !ok {
		t.Fatal("provider_spend missing")
	}
	if spend["enabled"] != true || spend["cap_usd"] != 2.0 || spend["spent_usd"] != 0.37 || spend["remaining_usd"] != 1.63 {
		t.Fatalf("unexpected provider spend: %v", spend)
	}
}

func TestCreditUsageReportsNoCapAndSurvivesAnUnreachableAIService(t *testing.T) {
	s, handler, _ := setupTestServer(true, "test-user")
	disabled := creditUsage(t, handler)["provider_spend"].(map[string]interface{})
	if disabled["enabled"] != false || disabled["cap_usd"] != nil {
		t.Fatalf("a missing cap must read as disabled: %v", disabled)
	}
	s.AIClient.(*gen.FakeAIServiceClient).ProviderSpendFunc = func(context.Context) (*gen.ProviderSpendResponse, error) {
		return nil, errors.New("connection refused")
	}
	body := creditUsage(t, handler)
	if _, present := body["provider_spend"]; present {
		t.Fatalf("an unknown spend must be omitted, not invented: %v", body["provider_spend"])
	}
	if body["balance"] != 50.0 {
		t.Fatalf("credit usage must still load: %v", body)
	}
}

type pingingRepo struct {
	db.Repository
	err error
}

func (p *pingingRepo) Ping(context.Context) error { return p.err }

func readyz(t *testing.T, handler http.Handler) (int, map[string]interface{}) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func TestReadyzIsDistinctFromLivenessAndNamesTheDependencyThatIsDown(t *testing.T) {
	s, handler, _ := setupTestServer(true, "test-user")
	if code, body := readyz(t, handler); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("expected ready, got %d %v", code, body)
	}
	s.AIClient.(*gen.FakeAIServiceClient).CheckReadyFunc = func(context.Context) error {
		return errors.New("dial tcp 10.0.0.9:8000: password=hunter2")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("expected a 503 with no connection details, got %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string
		Checks map[string]string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Status != "not_ready" || body.Checks["ai"] != "unavailable" || body.Checks["database"] != "ok" {
		t.Fatalf("unexpected readiness body: %+v", body)
	}
	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("liveness must stay up while a dependency is down, got %d", live.Code)
	}
}

func TestReadyzFailsWhileTheDatabaseDoesNotAnswer(t *testing.T) {
	repo := &pingingRepo{Repository: db.NewInMemoryRepository(), err: errors.New("pq: connection reset")}
	server := api.NewServer(repo, credits.NewInMemoryCreditService(nil), jobs.NewInMemoryJobService(),
		gen.NewFakeAIServiceClient(), auth.NewDevAuthenticator(true, "test-user"), auth.NewDefaultImageHosting("/media"), 2)
	code, body := readyz(t, server.NewRouter())
	checks, _ := body["checks"].(map[string]interface{})
	if code != http.StatusServiceUnavailable || checks["database"] != "unavailable" || checks["ai"] != "ok" {
		t.Fatalf("expected the database to be named, got %d %v", code, body)
	}
	repo.err = nil
	if code, _ := readyz(t, server.NewRouter()); code != http.StatusOK {
		t.Fatalf("expected ready once the database answers, got %d", code)
	}
}

func TestFailedGenerationLogsOneCorrelatedLineWithJobIDAndNoPrompt(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	s, handler, repo := setupTestServer(true, "test-user")
	project := &domain.Project{UserID: "test-user", OriginalScript: "SECRET-SCRIPT-TEXT", Title: "Comic"}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	character := &domain.Character{ProjectID: project.ID, Name: "Hero"}
	if err := repo.CreateCharacter(context.Background(), character); err != nil {
		t.Fatal(err)
	}
	s.AIClient.(*gen.FakeAIServiceClient).GenerateCharacterReferenceFunc = func(ctx context.Context, req gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
		return nil, &gen.AIServiceError{Code: "SPEND_CAP_REACHED", Message: "cap reached", StatusCode: http.StatusTooManyRequests}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+character.ID+"/reference-image", strings.NewReader(`{}`)))
	s.WaitDispatches()

	var line string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "generation failed") {
			if line != "" {
				t.Fatalf("expected exactly one failure line, got more:\n%s", logs.String())
			}
			line = l
		}
	}
	history, _ := s.CreditService.GetUsageHistory(context.Background(), "test-user")
	jobID := ""
	for _, entry := range history {
		if entry.GenerationJobID != nil {
			jobID = *entry.GenerationJobID
		}
	}
	if jobID == "" || !strings.Contains(line, "job_id="+jobID) || !strings.Contains(line, "code=SPEND_CAP_REACHED") || !strings.Contains(line, "state=failed") {
		t.Fatalf("failure line does not carry the job id and code: %q (job %q)\nall logs:\n%s", line, jobID, logs.String())
	}
	if strings.Contains(logs.String(), "SECRET-SCRIPT-TEXT") || strings.Contains(line, "refund_error") {
		t.Fatalf("log leaked prompt text or reported a refund failure: %s", logs.String())
	}
}
