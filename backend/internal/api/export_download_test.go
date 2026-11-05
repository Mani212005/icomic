package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

func createServerWithAI(aiClient gen.AIService, defaultUID string) (*api.Server, http.Handler, db.Repository) {
	repo := db.NewInMemoryRepository()
	balances := map[string]float64{defaultUID: 50.0}
	creditSvc := credits.NewInMemoryCreditService(balances)
	jobSvc := jobs.NewInMemoryJobService()
	authenticator := auth.NewDevAuthenticator(true, defaultUID)
	imageHosting := auth.NewDefaultImageHosting("/media")

	s := api.NewServer(
		repo,
		creditSvc,
		jobSvc,
		aiClient,
		authenticator,
		imageHosting,
		2,
	)
	return s, s.NewRouter(), repo
}

// TestDownloadExport_EphemeralStorageProxyKeyDerivation verifies that DownloadExport derives
// the storage key correctly from an absolute stored URL containing http://localhost:8000/storage/fake/
// and proxies through the AI service base URL configured on an ephemeral port.
func TestDownloadExport_EphemeralStorageProxyKeyDerivation(t *testing.T) {
	var requestedURI string
	var requestedMethod string
	assembledPDF := []byte("%PDF-1.4\n% Real assembled comic with C2PA metadata and AI disclosure\n%%EOF\n")

	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedURI = r.URL.RequestURI()
		requestedMethod = r.Method
		if r.URL.Path == "/storage/fake/exports/proj-ephem-1/comic.pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(assembledPDF)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"not found"}`))
	}))
	defer storageServer.Close()

	aiClient := gen.NewAIServiceClient(storageServer.URL, 5*time.Second)
	_, handler, repo := createServerWithAI(aiClient, "user-ephem")

	ctx := context.Background()
	_ = repo.CreateUser(ctx, &domain.User{ID: "user-ephem", Email: "ephem@example.com"})
	_ = repo.CreateProject(ctx, &domain.Project{ID: "proj-ephem-1", UserID: "user-ephem", Title: "Ephem Comic"})
	_ = repo.CreateExport(ctx, &domain.Export{
		ID:                          "5859e062-706d-53bb-aee9-6d12b0aa61af",
		ProjectID:                   "proj-ephem-1",
		FileURL:                     "http://localhost:8000/storage/fake/exports/proj-ephem-1/comic.pdf",
		Status:                      "completed",
		ProvenanceMetadataPreserved: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/5859e062-706d-53bb-aee9-6d12b0aa61af/download", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: body=%s", rec.Code, rec.Body.String())
	}
	if requestedMethod != http.MethodGet {
		t.Fatalf("expected GET to upstream storage, got %s", requestedMethod)
	}
	if requestedURI != "/storage/fake/exports/proj-ephem-1/comic.pdf" {
		t.Fatalf("expected key derivation to request /storage/fake/exports/proj-ephem-1/comic.pdf, got %s", requestedURI)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("expected Content-Type application/pdf, got %s", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "attachment; filename=\"comic_proj-ephem-1.pdf\"" {
		t.Fatalf("expected Content-Disposition attachment; filename=\"comic_proj-ephem-1.pdf\", got %s", cd)
	}
	if !bytes.Equal(rec.Body.Bytes(), assembledPDF) {
		t.Fatalf("expected assembled PDF bytes, got %s", rec.Body.String())
	}
	// Verify no fabricated stub bytes are present
	if strings.Contains(rec.Body.String(), "% Assembled comic export") {
		t.Fatal("unexpected fabricated stub bytes present in download response")
	}
}

// TestDownloadExport_UpstreamStorageUnavailable502 verifies that DownloadExport returns HTTP 502
// with a JSON error body (and never stub bytes) when the upstream storage service is unreachable.
func TestDownloadExport_UpstreamStorageUnavailable502(t *testing.T) {
	// Start and immediately close server so port refuses connections
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := storageServer.URL
	storageServer.Close()

	aiClient := gen.NewAIServiceClient(closedURL, 1*time.Second)
	_, handler, repo := createServerWithAI(aiClient, "user-unavail")

	ctx := context.Background()
	_ = repo.CreateUser(ctx, &domain.User{ID: "user-unavail", Email: "unavail@example.com"})
	_ = repo.CreateProject(ctx, &domain.Project{ID: "proj-unavail-1", UserID: "user-unavail", Title: "Unavail Comic"})
	_ = repo.CreateExport(ctx, &domain.Export{
		ID:        "4716b3c6-8d83-5c70-bed0-c8bee38c1b13",
		ProjectID: "proj-unavail-1",
		FileURL:   "http://localhost:8000/storage/fake/exports/proj-unavail-1/comic.pdf",
		Status:    "completed",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/4716b3c6-8d83-5c70-bed0-c8bee38c1b13/download", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway on upstream failure, got %d: body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON Content-Type on 502, got %s", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("expected no Content-Disposition header on 502 error, got %s", cd)
	}

	var errBody map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("expected JSON response body on 502, got error: %v, raw=%s", err, rec.Body.String())
	}
	if errBody["error"] == nil || errBody["error"] == "" {
		t.Fatalf("expected error field in JSON response, got: %v", errBody)
	}

	// Verify no PDF stub bytes ever
	if strings.Contains(rec.Body.String(), "%PDF") || strings.Contains(rec.Body.String(), "% Assembled comic export") {
		t.Fatalf("stub bytes fabricated on upstream failure: %s", rec.Body.String())
	}
}

// TestDownloadExport_UpstreamStorageNotFound404 verifies that DownloadExport returns HTTP 404
// with a JSON error body (and never stub bytes) when the upstream storage returns 404.
func TestDownloadExport_UpstreamStorageNotFound404(t *testing.T) {
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"Storage object not found"}`))
	}))
	defer storageServer.Close()

	aiClient := gen.NewAIServiceClient(storageServer.URL, 5*time.Second)
	_, handler, repo := createServerWithAI(aiClient, "user-404")

	ctx := context.Background()
	_ = repo.CreateUser(ctx, &domain.User{ID: "user-404", Email: "user404@example.com"})
	_ = repo.CreateProject(ctx, &domain.Project{ID: "proj-404-1", UserID: "user-404", Title: "404 Comic"})
	_ = repo.CreateExport(ctx, &domain.Export{
		ID:        "208046c2-e6d8-5190-a520-e694b06c922a",
		ProjectID: "proj-404-1",
		FileURL:   "http://localhost:8000/storage/fake/exports/proj-404-1/missing.pdf",
		Status:    "completed",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/208046c2-e6d8-5190-a520-e694b06c922a/download", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d: body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON Content-Type on 404, got %s", ct)
	}

	var errBody map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("expected JSON response body on 404, got error: %v, raw=%s", err, rec.Body.String())
	}
	if errBody["error"] == nil || errBody["error"] == "" {
		t.Fatalf("expected error field in JSON response, got: %v", errBody)
	}

	// Verify no PDF stub bytes ever
	if strings.Contains(rec.Body.String(), "%PDF") || strings.Contains(rec.Body.String(), "% Assembled comic export") {
		t.Fatalf("stub bytes fabricated on upstream 404: %s", rec.Body.String())
	}
}

// TestDownloadExport_PathTraversalPrevention verifies that directory traversal attempts
// in stored file URLs are rejected with HTTP 400 and never forwarded or stubbed.
func TestDownloadExport_PathTraversalPrevention(t *testing.T) {
	storageQueried := false
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageQueried = true
		w.WriteHeader(http.StatusOK)
	}))
	defer storageServer.Close()

	aiClient := gen.NewAIServiceClient(storageServer.URL, 5*time.Second)
	_, handler, repo := createServerWithAI(aiClient, "user-traversal")

	ctx := context.Background()
	_ = repo.CreateUser(ctx, &domain.User{ID: "user-traversal", Email: "traversal@example.com"})
	_ = repo.CreateProject(ctx, &domain.Project{ID: "proj-trav-1", UserID: "user-traversal", Title: "Trav Comic"})

	maliciousURLs := []string{
		"http://localhost:8000/storage/fake/../../etc/passwd",
		"http://localhost:8000/storage/fake/exports/../secret.pdf",
		"http://localhost:8000/storage/fake/%2e%2e/passwd",
		"/storage/fake/../../etc/shadow",
	}

	for i, badURL := range maliciousURLs {
		storageQueried = false
		expID := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		_ = repo.CreateExport(ctx, &domain.Export{
			ID:        expID,
			ProjectID: "proj-trav-1",
			FileURL:   badURL,
			Status:    "completed",
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/"+expID+"/download", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for traversal URL %q, got %d: body=%s", badURL, rec.Code, rec.Body.String())
		}
		if storageQueried {
			t.Fatalf("upstream storage was queried on path traversal attempt with %q", badURL)
		}
		if strings.Contains(rec.Body.String(), "%PDF") {
			t.Fatalf("stub PDF returned on traversal attack: %s", rec.Body.String())
		}
	}
}

// TestDownloadExport_ForbiddenForDifferentUser verifies strict ownership enforcement on download.
func TestDownloadExport_ForbiddenForDifferentUser(t *testing.T) {
	_, handler, repo := createServerWithAI(nil, "attacker-user")

	ctx := context.Background()
	_ = repo.CreateUser(ctx, &domain.User{ID: "victim-user", Email: "victim@example.com"})
	_ = repo.CreateUser(ctx, &domain.User{ID: "attacker-user", Email: "attacker@example.com"})
	_ = repo.CreateProject(ctx, &domain.Project{ID: "proj-victim", UserID: "victim-user", Title: "Victim Comic"})
	_ = repo.CreateExport(ctx, &domain.Export{
		ID:        "2a421c82-cbe4-5eb6-956e-8df592ee4a4a",
		ProjectID: "proj-victim",
		FileURL:   "http://localhost:8000/storage/fake/exports/proj-victim/comic.pdf",
		Status:    "completed",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/2a421c82-cbe4-5eb6-956e-8df592ee4a4a/download", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-owner, got %d", rec.Code)
	}
}

// TestDownloadExport_ExportNotFound verifies 404 when export ID does not exist.
func TestDownloadExport_ExportNotFound(t *testing.T) {
	_, handler, _ := createServerWithAI(nil, "some-user")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/c320467d-9873-51cb-a719-c91de23c01a4/download", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for missing export, got %d", rec.Code)
	}
}

// TestDeriveStorageKey unit tests key derivation and path traversal protection.
func TestDeriveStorageKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "absolute URL with localhost:8000",
			input:   "http://localhost:8000/storage/fake/exports/proj-1/comic.pdf",
			want:    "exports/proj-1/comic.pdf",
			wantErr: false,
		},
		{
			name:    "absolute URL with custom host and port",
			input:   "http://ai-service:9000/storage/fake/pages/1/line.png",
			want:    "pages/1/line.png",
			wantErr: false,
		},
		{
			name:    "absolute URL with query params and fragment",
			input:   "https://storage.example.com/storage/fake/exports/p2/comic.pdf?token=xyz#page1",
			want:    "exports/p2/comic.pdf",
			wantErr: false,
		},
		{
			name:    "relative path with leading slash",
			input:   "/storage/fake/exports/p3/comic.pdf",
			want:    "exports/p3/comic.pdf",
			wantErr: false,
		},
		{
			name:    "relative path without leading slash",
			input:   "storage/fake/exports/p4/comic.pdf",
			want:    "exports/p4/comic.pdf",
			wantErr: false,
		},
		{
			name:    "raw key without fake prefix",
			input:   "exports/p5/comic.pdf",
			want:    "exports/p5/comic.pdf",
			wantErr: false,
		},
		{
			name:    "empty input",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "traversal with dot-dot leading",
			input:   "http://localhost:8000/storage/fake/../../etc/passwd",
			want:    "",
			wantErr: true,
		},
		{
			name:    "traversal in path component",
			input:   "http://localhost:8000/storage/fake/exports/../secret.pdf",
			want:    "",
			wantErr: true,
		},
		{
			name:    "traversal URL encoded",
			input:   "http://localhost:8000/storage/fake/%2e%2e/passwd",
			want:    "",
			wantErr: true,
		},
		{
			name:    "traversal bare dot-dot",
			input:   "..",
			want:    "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := api.DeriveStorageKey(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("DeriveStorageKey(%q) error = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("DeriveStorageKey(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
