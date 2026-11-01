package api

import (
	"net/http"
)

// NewRouter sets up all HTTP routes for the Go API server.
func (s *Server) NewRouter() http.Handler {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /healthz", s.HealthCheck)
	mux.HandleFunc("GET /internal/health", s.HealthCheck)

	// Projects
	mux.HandleFunc("POST /api/v1/projects", s.CreateProject)
	mux.HandleFunc("GET /api/v1/projects", s.ListProjects)
	mux.HandleFunc("GET /api/v1/projects/{id}", s.GetProject)
	mux.HandleFunc("PATCH /api/v1/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	})
	mux.HandleFunc("DELETE /api/v1/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		_ = s.Repo.DeleteProject(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	})

	// Characters
	mux.HandleFunc("GET /api/v1/projects/{id}/characters", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		chars, _ := s.Repo.ListCharacters(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]interface{}{"characters": chars})
	})
	mux.HandleFunc("PATCH /api/v1/characters/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	})
	mux.HandleFunc("POST /api/v1/characters/{id}/reference-image", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": "job-ref-stub"})
	})
	mux.HandleFunc("POST /api/v1/characters/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
	})

	// Story and Chapters
	mux.HandleFunc("POST /api/v1/projects/{id}/analyze", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": "job-analyze-stub"})
	})
	mux.HandleFunc("POST /api/v1/projects/{id}/chapters", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": "job-chapters-stub"})
	})
	mux.HandleFunc("GET /api/v1/projects/{id}/chapters", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		chapters, _ := s.Repo.ListChapters(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]interface{}{"chapters": chapters})
	})
	mux.HandleFunc("PATCH /api/v1/chapters/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	})
	mux.HandleFunc("POST /api/v1/chapters/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
	})
	mux.HandleFunc("POST /api/v1/chapters/{id}/panels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": "job-panels-stub"})
	})
	mux.HandleFunc("GET /api/v1/chapters/{id}/panels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"panels": []interface{}{}})
	})
	mux.HandleFunc("PATCH /api/v1/panels/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	})

	// Pages
	mux.HandleFunc("GET /api/v1/chapters/{id}/pages", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		pages, _ := s.Repo.ListPages(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]interface{}{"pages": pages})
	})
	mux.HandleFunc("PATCH /api/v1/pages/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	})

	// Draft Sketch
	mux.HandleFunc("POST /api/v1/pages/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": "job-draft-stub"})
	})
	mux.HandleFunc("POST /api/v1/pages/{id}/draft/approve", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
	})

	// Final Line Art
	mux.HandleFunc("POST /api/v1/pages/{id}/line-art", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": "job-line-art-stub"})
	})
	mux.HandleFunc("POST /api/v1/pages/{id}/line-art/approve", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
	})

	// Colouring
	mux.HandleFunc("GET /api/v1/pages/{id}/colour-palette", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"palette": []interface{}{}})
	})
	mux.HandleFunc("POST /api/v1/pages/{id}/colour-palette/suggest", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": "job-palette-stub"})
	})
	mux.HandleFunc("PATCH /api/v1/pages/{id}/colour-palette", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	})
	mux.HandleFunc("POST /api/v1/pages/{id}/colour/approve", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
	})

	// Jobs and Credits
	mux.HandleFunc("POST /api/v1/projects/{id}/estimate", s.EstimateCost)
	mux.HandleFunc("GET /api/v1/generation-jobs/{id}", s.GetJobStatus)
	mux.HandleFunc("POST /api/v1/generation-jobs/{id}/cancel", s.CancelJob)
	mux.HandleFunc("GET /api/v1/me/credit-usage", s.CreditUsage)

	// Export
	mux.HandleFunc("POST /api/v1/projects/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]string{"export_id": "export-stub-123"})
	})
	mux.HandleFunc("GET /api/v1/exports/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":                             r.PathValue("id"),
			"status":                         "completed",
			"provenance_metadata_preserved": true,
		})
	})
	mux.HandleFunc("GET /api/v1/exports/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("%PDF-1.4 minimal test stub"))
	})

	return mux
}
