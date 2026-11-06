package api

import (
	"net/http"
	"strings"

	"icomic/backend/internal/auth"
	"icomic/backend/internal/domain"
)

// corsMiddleware adds CORS headers with configurable allowed origin (SPEC 12, ARCHITECTURE 2).
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedOrigin := s.AllowedOrigin
		if allowedOrigin == "" {
			allowedOrigin = "http://localhost:5173"
		}

		origin := r.Header.Get("Origin")
		var allowHeader string
		if allowedOrigin == "*" {
			allowHeader = "*"
		} else {
			w.Header().Add("Vary", "Origin")
			for _, configured := range strings.Split(allowedOrigin, ",") {
				if origin != "" && strings.TrimSpace(configured) == origin {
					allowHeader = origin
					break
				}
			}
		}

		if allowHeader != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowHeader)
			if allowHeader != "*" {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-User-ID, Idempotency-Key")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// authMiddleware enforces authentication using the configured Authenticator.
func (s *Server) authMiddleware(pattern string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := s.Authenticator.Authenticate(r)
		if err != nil || userID == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized: authentication required")
			return
		}
		if !s.admit(w, "user:"+domain.HashUserID(userID), s.Limits.RequestsPerUser) {
			return
		}
		if !s.validatePublicRequest(w, r, pattern) {
			return
		}
		ctx := auth.WithUserID(r.Context(), userID)
		next(w, r.WithContext(ctx))
	}
}

// NewRouter sets up all HTTP routes for the Go API server.
func (s *Server) NewRouter() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, s.authMiddleware(pattern, handler))
	}

	// Health
	mux.HandleFunc("GET /healthz", s.HealthCheck)
	mux.HandleFunc("GET /internal/health", s.HealthCheck)

	// Media (serves or proxies stored media per ImageHostingSeam)
	mux.HandleFunc("GET /media/{path...}", s.ServeMedia)
	mux.HandleFunc("GET /media", s.ServeMedia)

	// Projects
	handle("POST /api/v1/projects", s.CreateProject)
	handle("GET /api/v1/projects", s.ListProjects)
	handle("GET /api/v1/projects/{id}", s.GetProject)
	handle("PATCH /api/v1/projects/{id}", s.UpdateProject)
	handle("DELETE /api/v1/projects/{id}", s.DeleteProject)

	// Characters
	handle("GET /api/v1/projects/{id}/characters", s.ListCharacters)
	handle("PATCH /api/v1/characters/{id}", s.UpdateCharacter)
	handle("POST /api/v1/characters/{id}/reference-image", s.GenerateCharacterReference)
	handle("POST /api/v1/characters/{id}/approve", s.ApproveCharacter)

	// Story and Chapters
	handle("POST /api/v1/projects/{id}/analyze", s.AnalyzeScript)
	handle("POST /api/v1/projects/{id}/chapters", s.GenerateChapters)
	handle("GET /api/v1/projects/{id}/chapters", s.ListChapters)
	handle("PATCH /api/v1/chapters/{id}", s.UpdateChapter)
	handle("POST /api/v1/chapters/{id}/approve", s.ApproveChapter)
	handle("POST /api/v1/chapters/{id}/panels", s.GeneratePanels)
	handle("GET /api/v1/chapters/{id}/panels", s.ListPanels)
	handle("PATCH /api/v1/panels/{id}", s.UpdatePanel)

	// Pages
	handle("GET /api/v1/chapters/{id}/pages", s.ListPages)
	handle("PATCH /api/v1/pages/{id}", s.UpdatePage)

	// Draft Sketch
	handle("GET /api/v1/pages/{id}/lettering", s.GetLettering)
	handle("PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/override", s.PatchLetteringOverride)
	handle("PATCH /api/v1/pages/{id}/lettering/zones", s.PatchLetteringZones)
	handle("POST /api/v1/pages/{id}/draft", s.GenerateDraft)
	handle("POST /api/v1/pages/{id}/draft/approve", s.ApproveDraft)

	// Final Line Art
	handle("POST /api/v1/pages/{id}/line-art", s.GenerateLineArt)
	handle("POST /api/v1/pages/{id}/line-art/approve", s.ApproveLineArt)

	// Finished Painted Page Prototype (opt-in route)
	handle("POST /api/v1/pages/{id}/painted-page", s.GeneratePaintedPage)
	handle("POST /api/v1/pages/{id}/painted-page/approve", s.ApprovePaintedPage)

	// Colouring
	handle("GET /api/v1/pages/{id}/colour-palette", s.GetColourPalette)
	handle("POST /api/v1/pages/{id}/colour-palette/suggest", s.SuggestColourPalette)
	handle("PATCH /api/v1/pages/{id}/colour-palette", s.UpdateColourPalette)
	handle("PUT /api/v1/pages/{id}/colour-image", s.UploadColourImage)
	handle("POST /api/v1/pages/{id}/colour/approve", s.ApproveColour)

	// Jobs and Credits
	handle("POST /api/v1/projects/{id}/estimate", s.EstimateCost)
	handle("GET /api/v1/generation-jobs/{id}", s.GetJobStatus)
	handle("POST /api/v1/generation-jobs/{id}/cancel", s.CancelJob)
	handle("GET /api/v1/me/credit-usage", s.CreditUsage)

	// Export
	handle("POST /api/v1/projects/{id}/export", s.ExportProject)
	handle("GET /api/v1/exports/{id}", s.GetExport)
	handle("GET /api/v1/exports/{id}/download", s.DownloadExport)

	// Prompt Moderation
	handle("POST /api/v1/prompts/moderate", s.ModeratePromptHandler)

	return s.corsMiddleware(s.ipAdmission(mux))
}
