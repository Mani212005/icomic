package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"icomic/backend/internal/api"
)

func TestHealthCheck(t *testing.T) {
	server := api.NewServer()
	handler := server.NewRouter()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status 'ok', got '%s'", body["status"])
	}
}
