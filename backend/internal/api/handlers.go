package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

type Server struct {
	Repo              db.Repository
	CreditService     credits.CreditService
	JobService        jobs.JobService
	AgeVerifier       auth.AgeVerifier
	GeoBlocker        auth.GeoBlocker
	KillSwitchEnabled bool
	MaxPanelsPerPage  int
	DefaultUserQuota  float64
}

type JSONResponse struct {
	Status  string      `json:"status,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, JSONResponse{Error: message})
}

func getUserID(r *http.Request) string {
	uid := r.Header.Get("X-User-ID")
	if uid == "" {
		uid = auth.UserIDFromContext(r.Context())
	}
	if uid == "" {
		uid = "00000000-0000-0000-0000-000000000001" // Default test user in dev
	}
	return uid
}

// HealthCheck handles GET /healthz
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// CreateProject handles POST /api/v1/projects
func (s *Server) CreateProject(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	var body struct {
		Title          string `json:"title"`
		OriginalScript string `json:"original_script"`
		ArtStyle       string `json:"art_style"`
		ChapterCount   int    `json:"chapter_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if strings.TrimSpace(body.Title) == "" {
		writeError(w, http.StatusBadRequest, "Title is required")
		return
	}

	wordCount, err := domain.ValidateScript(body.OriginalScript)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	proj := &domain.Project{
		ID:             domain.HashUserID(body.Title + userID)[:32],
		UserID:         userID,
		Title:          body.Title,
		OriginalScript: body.OriginalScript,
		ChapterCount:   body.ChapterCount,
		ArtStyle:       body.ArtStyle,
		Status:         "created",
	}

	if err := s.Repo.CreateProject(r.Context(), proj); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"project":    proj,
		"word_count": wordCount,
	})
}

// ListProjects handles GET /api/v1/projects
func (s *Server) ListProjects(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	projects, err := s.Repo.ListProjects(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"projects": projects})
}

// GetProject handles GET /api/v1/projects/{id}
func (s *Server) GetProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	proj, err := s.Repo.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"project": proj})
}

// CreditUsage handles GET /api/v1/me/credit-usage
func (s *Server) CreditUsage(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	balance, err := s.CreditService.GetBalance(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	history, err := s.CreditService.GetUsageHistory(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":       userID,
		"balance":       balance,
		"usage_history": history,
	})
}

// EstimateCost handles POST /api/v1/projects/{id}/estimate
func (s *Server) EstimateCost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stage      string `json:"stage"`
		ImageCount int    `json:"image_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	// Meta image generation pricing: flat $0.01 per image
	costPerImage := 0.01
	totalCost := float64(body.ImageCount) * costPerImage

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"stage":          body.Stage,
		"image_count":    body.ImageCount,
		"cost_per_image": costPerImage,
		"estimated_cost": totalCost,
		"currency":       "USD",
		"assumptions":    "Flat $0.01 per image attempt via Meta muse-image-1.0",
	})
}

// GetJobStatus handles GET /api/v1/generation-jobs/{id}
func (s *Server) GetJobStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.JobService.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": job})
}

// CancelJob handles POST /api/v1/generation-jobs/{id}/cancel
func (s *Server) CancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.JobService.CancelJob(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Job cancelled successfully"})
}
