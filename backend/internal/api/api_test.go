package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/jobs"
)

func setupTestServer() http.Handler {
	s := &api.Server{
		Repo:              db.NewInMemoryRepository(),
		CreditService:     credits.NewInMemoryCreditService(map[string]float64{"test-user": 50.0}),
		JobService:        jobs.NewInMemoryJobService(),
		AgeVerifier:       auth.NewDefaultAgeVerifier(18),
		GeoBlocker:        auth.NewConfigurableGeoBlocker([]string{"IN"}),
		KillSwitchEnabled: false,
		MaxPanelsPerPage:  6,
		DefaultUserQuota:  25.0,
	}
	return s.NewRouter()
}

func TestHealthCheck(t *testing.T) {
	handler := setupTestServer()
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

func TestCreateProject(t *testing.T) {
	handler := setupTestServer()

	payload := map[string]interface{}{
		"title":           "The Quantum Detective",
		"original_script": "Page 1: The neon lights flicker in Neo-Tokyo as detective Jax ignites his pipe.",
		"art_style":       "cyberpunk-noir",
		"chapter_count":   3,
	}
	data, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(data))
	req.Header.Set("X-User-ID", "test-user")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestEstimateCost(t *testing.T) {
	handler := setupTestServer()

	payload := map[string]interface{}{
		"stage":       "draft_sketch",
		"image_count": 5,
	}
	data, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/proj-123/estimate", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["estimated_cost"] != 0.05 {
		t.Fatalf("expected 0.05 estimated cost, got %v", resp["estimated_cost"])
	}
}

func TestCreditUsage(t *testing.T) {
	handler := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/credit-usage", nil)
	req.Header.Set("X-User-ID", "test-user")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["balance"] != 50.0 {
		t.Fatalf("expected balance 50.0, got %v", resp["balance"])
	}
}
