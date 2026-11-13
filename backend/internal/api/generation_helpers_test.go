package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"icomic/backend/internal/api"
	"icomic/backend/internal/domain"
)

// serveGeneration serves a generation request. An accepted receipt returns before the provider
// runs, so it then waits for the server's dispatcher to finish the delivery and the test can
// assert the settled state, exactly as a client does by polling the job.
func serveGeneration(s *api.Server, handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusAccepted {
		s.WaitDispatches()
	}
	return rec
}

// receiptJob decodes the job from a generation receipt and returns its settled state.
func settledJob(t *testing.T, s *api.Server, rec *httptest.ResponseRecorder) *domain.GenerationJob {
	t.Helper()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected a 202 receipt, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Job domain.GenerationJob `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Job.ID == "" {
		t.Fatalf("receipt carries no job: %v %s", err, rec.Body.String())
	}
	job, err := s.JobService.GetJob(context.Background(), body.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
