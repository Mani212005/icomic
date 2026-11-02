package api

import (
	"net/http"
	"strings"

	"icomic/backend/internal/auth"
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
		} else if origin != "" {
			origins := strings.Split(allowedOrigin, ",")
			for _, o := range origins {
				if strings.TrimSpace(o) == origin {
					allowHeader = origin
					break
				}
			}
			if allowHeader == "" && len(origins) == 1 && origins[0] == allowedOrigin {
				allowHeader = allowedOrigin
			}
		} else {
			allowHeader = allowedOrigin
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
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := s.Authenticator.Authenticate(r)
		if err != nil || userID == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized: authentication required")
			return
		}
		ctx := auth.WithUserID(r.Context(), userID)
		next(w, r.WithContext(ctx))
	}
}

// NewRouter sets up all HTTP routes for the Go API server.
func (s *Server) NewRouter() http.Handler {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /healthz", s.HealthCheck)
	mux.HandleFunc("GET /internal/health", s.HealthCheck)

	// Media (serves or proxies stored media per ImageHostingSeam)
	mux.HandleFunc("GET /media/{path...}", s.ServeMedia)
	mux.HandleFunc("GET /media", s.ServeMedia)

	// Projects
	mux.HandleFunc("POST /api/v1/projects", s.authMiddleware(s.CreateProject))
	mux.HandleFunc("GET /api/v1/projects", s.authMiddleware(s.ListProjects))
	mux.HandleFunc("GET /api/v1/projects/{id}", s.authMiddleware(s.GetProject))
	mux.HandleFunc("PATCH /api/v1/projects/{id}", s.authMiddleware(s.UpdateProject))
	mux.HandleFunc("DELETE /api/v1/projects/{id}", s.authMiddleware(s.DeleteProject))

	// Characters
	mux.HandleFunc("GET /api/v1/projects/{id}/characters", s.authMiddleware(s.ListCharacters))
	mux.HandleFunc("PATCH /api/v1/characters/{id}", s.authMiddleware(s.UpdateCharacter))
	mux.HandleFunc("POST /api/v1/characters/{id}/reference-image", s.authMiddleware(s.GenerateCharacterReference))
	mux.HandleFunc("POST /api/v1/characters/{id}/approve", s.authMiddleware(s.ApproveCharacter))

	// Story and Chapters
	mux.HandleFunc("POST /api/v1/projects/{id}/analyze", s.authMiddleware(s.AnalyzeScript))
	mux.HandleFunc("POST /api/v1/projects/{id}/chapters", s.authMiddleware(s.GenerateChapters))
	mux.HandleFunc("GET /api/v1/projects/{id}/chapters", s.authMiddleware(s.ListChapters))
	mux.HandleFunc("PATCH /api/v1/chapters/{id}", s.authMiddleware(s.UpdateChapter))
	mux.HandleFunc("POST /api/v1/chapters/{id}/approve", s.authMiddleware(s.ApproveChapter))
	mux.HandleFunc("POST /api/v1/chapters/{id}/panels", s.authMiddleware(s.GeneratePanels))
	mux.HandleFunc("GET /api/v1/chapters/{id}/panels", s.authMiddleware(s.ListPanels))
	mux.HandleFunc("PATCH /api/v1/panels/{id}", s.authMiddleware(s.UpdatePanel))

	// Pages
	mux.HandleFunc("GET /api/v1/chapters/{id}/pages", s.authMiddleware(s.ListPages))
	mux.HandleFunc("PATCH /api/v1/pages/{id}", s.authMiddleware(s.UpdatePage))

	// Draft Sketch
	mux.HandleFunc("POST /api/v1/pages/{id}/draft", s.authMiddleware(s.GenerateDraft))
	mux.HandleFunc("POST /api/v1/pages/{id}/draft/approve", s.authMiddleware(s.ApproveDraft))

	// Final Line Art
	mux.HandleFunc("POST /api/v1/pages/{id}/line-art", s.authMiddleware(s.GenerateLineArt))
	mux.HandleFunc("POST /api/v1/pages/{id}/line-art/approve", s.authMiddleware(s.ApproveLineArt))

	// Colouring
	mux.HandleFunc("GET /api/v1/pages/{id}/colour-palette", s.authMiddleware(s.GetColourPalette))
	mux.HandleFunc("POST /api/v1/pages/{id}/colour-palette/suggest", s.authMiddleware(s.SuggestColourPalette))
	mux.HandleFunc("PATCH /api/v1/pages/{id}/colour-palette", s.authMiddleware(s.UpdateColourPalette))
	mux.HandleFunc("POST /api/v1/pages/{id}/colour/approve", s.authMiddleware(s.ApproveColour))

	// Jobs and Credits
	mux.HandleFunc("POST /api/v1/projects/{id}/estimate", s.authMiddleware(s.EstimateCost))
	mux.HandleFunc("GET /api/v1/generation-jobs/{id}", s.authMiddleware(s.GetJobStatus))
	mux.HandleFunc("POST /api/v1/generation-jobs/{id}/cancel", s.authMiddleware(s.CancelJob))
	mux.HandleFunc("GET /api/v1/me/credit-usage", s.authMiddleware(s.CreditUsage))

	// Export
	mux.HandleFunc("POST /api/v1/projects/{id}/export", s.authMiddleware(s.ExportProject))
	mux.HandleFunc("GET /api/v1/exports/{id}", s.authMiddleware(s.GetExport))
	mux.HandleFunc("GET /api/v1/exports/{id}/download", s.authMiddleware(s.DownloadExport))

	// Prompt Moderation
	mux.HandleFunc("POST /api/v1/prompts/moderate", s.authMiddleware(s.ModeratePromptHandler))

	return s.corsMiddleware(mux)
}
