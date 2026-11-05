package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
	"icomic/backend/gen"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

type JSONResponse struct {
	Status  string      `json:"status,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

func optionalInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func optionalStringSlice(value []string) *[]string {
	if len(value) == 0 {
		return nil
	}
	return &value
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, JSONResponse{Error: message})
}

func (s *Server) aiActualCost(ctx context.Context, jobID string) float64 {
	status, err := s.AIClient.GetJobStatus(ctx, jobID)
	if err != nil || status == nil || (status.Status != "done" && status.Status != "failed") || status.ActualCost == nil {
		return 0
	}
	return *status.ActualCost
}

func (s *Server) getUserID(r *http.Request) string {
	return auth.UserIDFromContext(r.Context())
}

func (s *Server) getOwnedProject(r *http.Request, projectID string) (*domain.Project, int) {
	proj, err := s.Repo.GetProject(r.Context(), projectID)
	if err != nil {
		return nil, http.StatusNotFound
	}
	if proj.UserID != s.getUserID(r) {
		return nil, http.StatusForbidden
	}
	return proj, http.StatusOK
}

func (s *Server) getOwnedPage(r *http.Request, pageID string) (*domain.Page, int) {
	page, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		return nil, http.StatusNotFound
	}
	chapter, err := s.Repo.GetChapter(r.Context(), page.ChapterID)
	if err != nil {
		return nil, http.StatusNotFound
	}
	_, status := s.getOwnedProject(r, chapter.ProjectID)
	return page, status
}

// checkPromptModeration screens a prompt with the AI service before dispatch (SPEC 4.5).
func (s *Server) checkPromptModeration(r *http.Request, prompt string, hashedUID string) (bool, string, error) {
	if s.AIClient == nil || strings.TrimSpace(prompt) == "" {
		return false, "", nil
	}
	resp, err := s.AIClient.ModeratePrompt(r.Context(), gen.ModeratePromptRequest{
		JobID:        uuid.NewString(),
		HashedUserID: hashedUID,
		Prompt:       prompt,
	})
	if err != nil {
		return false, "", err
	}
	if resp.Blocked {
		reason := "Prompt blocked by content moderation policy"
		if resp.Reason != nil && *resp.Reason != "" {
			reason = fmt.Sprintf("Prompt blocked by content moderation: %s", *resp.Reason)
		}
		return true, reason, nil
	}
	return false, "", nil
}

// ModeratePromptHandler handles POST /api/v1/prompts/moderate
func (s *Server) ModeratePromptHandler(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	hashedUID := domain.HashUserID(userID)
	blocked, reason, err := s.checkPromptModeration(r, body.Prompt, hashedUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Moderation check failed: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"blocked": blocked,
		"reason":  reason,
	})
}

// ServeMedia handles GET /media/{path...} serving or proxying media per ImageHostingSeam (SPEC 1.4, ARCHITECTURE 9).
func (s *Server) ServeMedia(w http.ResponseWriter, r *http.Request) {
	subPath := r.PathValue("path")
	if subPath == "" {
		subPath = strings.TrimPrefix(r.URL.Path, "/media/")
		subPath = strings.TrimPrefix(subPath, "/media")
	}
	subPath = strings.TrimPrefix(subPath, "/")
	if subPath == "" {
		writeError(w, http.StatusBadRequest, "Media path required")
		return
	}

	resolved := s.ImageHosting.ResolveURL(subPath)

	// Proxy if resolved URL is HTTP/HTTPS
	if strings.HasPrefix(resolved, "http://") || strings.HasPrefix(resolved, "https://") {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, resolved, nil)
		if err == nil {
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				for k, v := range resp.Header {
					for _, val := range v {
						w.Header().Add(k, val)
					}
				}
				w.WriteHeader(resp.StatusCode)
				_, _ = io.Copy(w, resp.Body)
				return
			}
		}
	}

	// Check local filesystem
	mediaDir := os.Getenv("MEDIA_DIR")
	if mediaDir == "" {
		mediaDir = "./media"
	}
	cleanRel := filepath.Clean("/" + subPath)
	localPath := filepath.Join(mediaDir, cleanRel)
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		http.ServeFile(w, r, localPath)
		return
	}

	// Proxy to Python fake storage if AI client has BaseURL
	if s.AIClient != nil {
		if client, ok := s.AIClient.(*gen.AIServiceClient); ok && client.BaseURL != "" {
			targetURL := strings.TrimRight(client.BaseURL, "/") + "/storage/fake/" + subPath
			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
			if err == nil {
				resp, err := http.DefaultClient.Do(req)
				if err == nil && resp.StatusCode == http.StatusOK {
					defer resp.Body.Close()
					for k, v := range resp.Header {
						for _, val := range v {
							w.Header().Add(k, val)
						}
					}
					w.WriteHeader(resp.StatusCode)
					_, _ = io.Copy(w, resp.Body)
					return
				}
			}
		}
	}

	writeError(w, http.StatusNotFound, "Media not found")
}

// HealthCheck handles GET /healthz and GET /internal/health
func (s *Server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// -------------------------------------------------------------
// Projects
// -------------------------------------------------------------

// CreateProject handles POST /api/v1/projects
func (s *Server) CreateProject(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

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
		body.Title = "Untitled Comic"
	}

	wordCount, err := domain.ValidateScript(body.OriginalScript)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if body.ChapterCount <= 0 {
		body.ChapterCount = domain.SuggestedChapterCount(len(strings.Fields(body.OriginalScript)))
	}
	if body.ArtStyle == "" {
		body.ArtStyle = "classic-comic"
	}

	proj := &domain.Project{
		ID:             uuid.NewString(),
		UserID:         userID,
		Title:          body.Title,
		OriginalScript: body.OriginalScript,
		ChapterCount:   body.ChapterCount,
		ArtStyle:       body.ArtStyle,
		Status:         "draft",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
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
	userID := s.getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	projects, err := s.Repo.ListProjects(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"projects": projects})
}

// GetProject handles GET /api/v1/projects/{id}
func (s *Server) GetProject(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	proj, err := s.Repo.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Strict ownership enforcement (SPEC 10, 11.1)
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"project": proj})
}

// UpdateProject handles PATCH /api/v1/projects/{id}
func (s *Server) UpdateProject(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	proj, err := s.Repo.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		Title          *string `json:"title"`
		OriginalScript *string `json:"original_script"`
		ChapterCount   *int    `json:"chapter_count"`
		ArtStyle       *string `json:"art_style"`
		Status         *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if body.Title != nil {
		proj.Title = *body.Title
	}
	if body.OriginalScript != nil {
		if _, err := domain.ValidateScript(*body.OriginalScript); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		proj.OriginalScript = *body.OriginalScript
	}
	if body.ChapterCount != nil && *body.ChapterCount > 0 {
		proj.ChapterCount = *body.ChapterCount
	}
	if body.ArtStyle != nil {
		proj.ArtStyle = *body.ArtStyle
	}
	if body.Status != nil {
		proj.Status = *body.Status
	}

	proj.UpdatedAt = time.Now().UTC()
	if err := s.Repo.UpdateProject(r.Context(), proj); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"project": proj})
}

// DeleteProject handles DELETE /api/v1/projects/{id}
func (s *Server) DeleteProject(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	proj, err := s.Repo.GetProject(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Strict ownership check (SPEC 10, 11.1)
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if err := s.Repo.DeleteProject(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// -------------------------------------------------------------
// Characters
// -------------------------------------------------------------

// ListCharacters handles GET /api/v1/projects/{id}/characters
func (s *Server) ListCharacters(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	projectID := r.PathValue("id")

	proj, err := s.Repo.GetProject(r.Context(), projectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	chars, err := s.Repo.ListCharacters(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"characters": chars})
}

// UpdateCharacter handles PATCH /api/v1/characters/{id}
func (s *Server) UpdateCharacter(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	charID := r.PathValue("id")

	ch, err := s.Repo.GetCharacter(r.Context(), charID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Character not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), ch.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		Name             *string                `json:"name"`
		Description      *string                `json:"description"`
		VisualAttributes map[string]interface{} `json:"visual_attributes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if body.Name != nil {
		ch.Name = *body.Name
	}
	if body.Description != nil {
		ch.Description = *body.Description
	}
	if body.VisualAttributes != nil {
		ch.VisualAttributes = body.VisualAttributes
	}

	// Editing character makes approval stale (SPEC 3.2, 3.5, ARCHITECTURE 5.3)
	ch.UpdatedAt = nextCharacterActivity(ch)
	if err := s.Repo.UpdateCharacter(r.Context(), ch); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"character": ch})
}

// GenerateCharacterReference handles POST /api/v1/characters/{id}/reference-image
func (s *Server) GenerateCharacterReference(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	charID := r.PathValue("id")

	ch, err := s.Repo.GetCharacter(r.Context(), charID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Character not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), ch.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Kill switch and quota check (SPEC 5.5)
	if err := s.CheckKillSwitchAndQuota(r, userID); err != nil {
		writeGenerationError(w, err)
		return
	}

	var body struct {
		Prompt string `json:"prompt"`
		Style  string `json:"style"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	prompt := body.Prompt
	if prompt == "" {
		prompt, err = defaultCharacterReferencePrompt(ch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Invalid character visual attributes")
			return
		}
	}
	style := body.Style
	if style == "" {
		style = proj.ArtStyle
	}
	if style == "" {
		style = "classic-comic"
	}

	hashedUID := domain.HashUserID(userID)

	// Pre-moderation
	blocked, reason, err := s.checkPromptModeration(r, prompt, hashedUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Prompt moderation failed: %v", err))
		return
	}
	if blocked {
		writeError(w, http.StatusBadRequest, reason)
		return
	}

	idempKey := generationKey(r)
	job, existed, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		EntityID:         ch.ID,
		DebitAmount:      s.Pricing.ImagePriceUSD,
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "character_reference",
		Stage:            "reference",
		ModelID:          s.Pricing.ImageModelID,
		IdempotencyKey:   idempKey,
		EstimatedCost:    s.Pricing.ImagePriceUSD,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	if existed {
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"job": job, "image_url": ch.ReferenceImageURL})
		return
	}

	if s.AIClient == nil {
		_, _ = s.CreditService.Refund(r.Context(), credits.RefundRequest{
			UserID:          userID,
			ProjectID:       proj.ID,
			GenerationJobID: job.ID,
			Amount:          s.Pricing.ImagePriceUSD,
		})
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	refResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.GenerateCharacterReferenceResponse, error) {
		return s.AIClient.GenerateCharacterReference(ctx, gen.GenerateCharacterReferenceRequest{
			JobID:           job.ID,
			HashedUserID:    hashedUID,
			ProjectID:       proj.ID,
			CharacterID:     ch.ID,
			CharacterName:   ch.Name,
			Prompt:          prompt,
			ReferenceImages: nil,
			Style:           style,
		})
	})
	if err != nil {
		if failErr := s.failGeneration(r.Context(), job, err, s.Pricing.ImagePriceUSD); failErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to persist generation failure or refund: %v", failErr))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Character reference generation failed: %v", err))
		return
	}

	imgURL := s.ImageHosting.ResolveURL(refResp.ImageURL)

	ch.ReferenceImageURL = imgURL
	ch.ReferenceImageJobID = &job.ID
	ch.UpdatedAt = nextCharacterActivity(ch)
	if err := s.Repo.UpdateCharacter(r.Context(), ch); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save character")
		return
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	if err := s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	job, err = s.JobService.GetJob(r.Context(), job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job":       job,
		"image_url": imgURL,
	})
}

// ApproveCharacter handles POST /api/v1/characters/{id}/approve
func (s *Server) ApproveCharacter(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	charID := r.PathValue("id")

	ch, err := s.Repo.GetCharacter(r.Context(), charID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Character not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), ch.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	now := nextCharacterActivity(ch)
	ch.ApprovedAt = &now
	if err := s.Repo.UpdateCharacter(r.Context(), ch); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save character")
		return
	}

	if err := s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "character_reference",
		EntityID:                 ch.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save approval")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "character": ch})
}

// -------------------------------------------------------------
// Story Generation & Chapters
// -------------------------------------------------------------

// AnalyzeScript handles POST /api/v1/projects/{id}/analyze
func (s *Server) AnalyzeScript(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	projectID := r.PathValue("id")

	proj, err := s.Repo.GetProject(r.Context(), projectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	if !s.validateSavedStory(w, proj.OriginalScript, proj.ChapterCount) {
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	hashedUID := domain.HashUserID(userID)

	// Pre-moderation
	blocked, reason, err := s.checkPromptModeration(r, proj.OriginalScript, hashedUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Prompt moderation failed: %v", err))
		return
	}
	if blocked {
		writeError(w, http.StatusBadRequest, reason)
		return
	}

	idempKey := uuid.NewString()
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "script_analysis",
		Stage:            "script",
		ModelID:          "meta-llama-3.1-8b-instruct",
		IdempotencyKey:   idempKey,
		EstimatedCost:    0.0,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}

	targetChapters := proj.ChapterCount
	r = r.WithContext(context.WithoutCancel(r.Context()))
	aiResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.AnalyzeScriptResponse, error) {
		return s.AIClient.AnalyzeScript(ctx, gen.AnalyzeScriptRequest{
			JobID:              job.ID,
			HashedUserID:       hashedUID,
			ProjectID:          proj.ID,
			Script:             proj.OriginalScript,
			TargetChapterCount: &targetChapters,
		})
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("AI script analysis failed: %v", err))
		return
	}

	now := time.Now().UTC()
	var chars []*domain.Character
	for _, p := range aiResp.CharacterProfiles {
		visualAttrs := p.VisualAttributes
		if visualAttrs == nil {
			visualAttrs = make(map[string]interface{})
		}
		if p.AgeRange != "" {
			visualAttrs["age_range"] = p.AgeRange
		}
		if p.Appearance != "" {
			visualAttrs["appearance"] = p.Appearance
		}
		if p.HairStyle != "" {
			visualAttrs["hair_style"] = p.HairStyle
		}
		if p.Clothing != "" {
			visualAttrs["clothing"] = p.Clothing
		}
		if p.Personality != "" {
			visualAttrs["personality"] = p.Personality
		}

		ch := &domain.Character{
			ID:               uuid.NewString(),
			ProjectID:        proj.ID,
			Name:             p.Name,
			Description:      p.Description,
			VisualAttributes: visualAttrs,
			UpdatedAt:        now,
		}
		if err := s.Repo.CreateCharacter(r.Context(), ch); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save character")
			return
		}
		chars = append(chars, ch)
	}

	if aiResp.SuggestedChapterCount > 0 && proj.ChapterCount <= 0 {
		proj.ChapterCount = aiResp.SuggestedChapterCount
		if err := s.Repo.UpdateProject(r.Context(), proj); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save project")
			return
		}
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":             job.ID,
		"character_profiles": chars,
		"chapter_count":      proj.ChapterCount,
	})
}

// GenerateChapters handles POST /api/v1/projects/{id}/chapters
func (s *Server) GenerateChapters(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	projectID := r.PathValue("id")

	proj, err := s.Repo.GetProject(r.Context(), projectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Check character reference approval gate
	chars, _ := s.Repo.ListCharacters(r.Context(), proj.ID)
	for _, ch := range chars {
		if ch.ApprovedAt == nil || domain.IsApprovalStale(ch.ApprovedAt, ch.UpdatedAt) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Character %s must be approved", ch.Name))
			return
		}
	}

	if !s.validateSavedStory(w, proj.OriginalScript, proj.ChapterCount) {
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	hashedUID := domain.HashUserID(userID)

	// Pre-moderation
	blocked, reason, err := s.checkPromptModeration(r, proj.OriginalScript, hashedUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Prompt moderation failed: %v", err))
		return
	}
	if blocked {
		writeError(w, http.StatusBadRequest, reason)
		return
	}

	idempKey := uuid.NewString()
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		EntityID:         proj.ID,
		JobType:          "chapter_outline",
		Stage:            "outline",
		ModelID:          "meta-llama-3.1-8b-instruct",
		IdempotencyKey:   idempKey,
		EstimatedCost:    0.0,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}

	var approvedProfiles []gen.CharacterProfile
	for _, ch := range chars {
		visAttrs := ch.VisualAttributes
		if visAttrs == nil {
			visAttrs = make(map[string]interface{})
		}
		ageRange, _ := visAttrs["age_range"].(string)
		appearance, _ := visAttrs["appearance"].(string)
		hairStyle, _ := visAttrs["hair_style"].(string)
		clothing, _ := visAttrs["clothing"].(string)
		personality, _ := visAttrs["personality"].(string)

		approvedProfiles = append(approvedProfiles, gen.CharacterProfile{
			Name:             ch.Name,
			Description:      ch.Description,
			AgeRange:         ageRange,
			Appearance:       appearance,
			HairStyle:        hairStyle,
			Clothing:         clothing,
			Personality:      personality,
			VisualAttributes: visAttrs,
		})
	}

	chapCount := proj.ChapterCount
	if chapCount <= 0 {
		chapCount = 2
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	outlineResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.GenerateChapterOutlineResponse, error) {
		return s.AIClient.GenerateChapterOutline(ctx, gen.GenerateChapterOutlineRequest{
			JobID:              job.ID,
			HashedUserID:       hashedUID,
			ProjectID:          proj.ID,
			Script:             proj.OriginalScript,
			ChapterCount:       chapCount,
			ApprovedCharacters: approvedProfiles,
		})
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Chapter outline generation failed: %v", err))
		return
	}

	var createdChapters []*domain.Chapter
	for _, cItem := range outlineResp.Chapters {
		chap := &domain.Chapter{
			ID:            uuid.NewString(),
			ProjectID:     proj.ID,
			ChapterNumber: cItem.ChapterNumber,
			Title:         cItem.Title,
			Summary:       cItem.Summary,
			Status:        "pending",
		}
		if err := s.Repo.CreateChapter(r.Context(), chap); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save chapter")
			return
		}
		createdChapters = append(createdChapters, chap)
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	writeJSON(w, http.StatusOK, map[string]interface{}{"chapters": createdChapters, "job_id": job.ID})
}

// ListChapters handles GET /api/v1/projects/{id}/chapters
func (s *Server) ListChapters(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	projectID := r.PathValue("id")

	proj, err := s.Repo.GetProject(r.Context(), projectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	chapters, err := s.Repo.ListChapters(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"chapters": chapters})
}

// UpdateChapter handles PATCH /api/v1/chapters/{id}
func (s *Server) UpdateChapter(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	chapID := r.PathValue("id")

	c, err := s.Repo.GetChapter(r.Context(), chapID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		Title   *string `json:"title"`
		Summary *string `json:"summary"`
		Status  *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if body.Title != nil {
		c.Title = *body.Title
	}
	if body.Summary != nil {
		c.Summary = *body.Summary
	}
	if body.Status != nil {
		c.Status = *body.Status
	}

	c.UpdatedAt = time.Now().UTC()
	if err := s.Repo.UpdateChapter(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save chapter")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"chapter": c})
}

// ApproveChapter handles POST /api/v1/chapters/{id}/approve
func (s *Server) ApproveChapter(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	chapID := r.PathValue("id")

	c, err := s.Repo.GetChapter(r.Context(), chapID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	c.Status = "approved"
	if err := s.Repo.UpdateChapter(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save chapter")
		return
	}

	now := timestampAfterActivity(c.UpdatedAt)
	if err := s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "chapter_outline",
		EntityID:                 c.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: 0.01,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save approval")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "chapter": c})
}

// -------------------------------------------------------------
// Panels & Pages
// -------------------------------------------------------------

// GeneratePanels handles POST /api/v1/chapters/{id}/panels
func (s *Server) GeneratePanels(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	chapID := r.PathValue("id")

	c, err := s.Repo.GetChapter(r.Context(), chapID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if !s.validateSavedStory(w, proj.OriginalScript, proj.ChapterCount) {
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	// Keep the complete chapter source and allocate the project panel budget evenly.
	words := strings.Fields(proj.OriginalScript)
	chapterCount := max(1, proj.ChapterCount)
	chapterIndex := c.ChapterNumber - 1
	if chapterIndex < 0 || chapterIndex >= chapterCount {
		writeError(w, http.StatusConflict, "Chapter number is outside the project plan")
		return
	}
	chapterScript := strings.Join(words[chapterIndex*len(words)/chapterCount:(chapterIndex+1)*len(words)/chapterCount], " ")
	totalPanels := domain.TargetPanelCount(len(words))
	panelCount := max(1, (chapterIndex+1)*totalPanels/chapterCount-chapterIndex*totalPanels/chapterCount)
	hashedUID := domain.HashUserID(userID)

	// Pre-moderation
	blocked, reason, err := s.checkPromptModeration(r, chapterScript+" "+c.Summary+" "+c.Title, hashedUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Prompt moderation failed: %v", err))
		return
	}
	if blocked {
		writeError(w, http.StatusBadRequest, reason)
		return
	}

	idempKey := uuid.NewString()
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		EntityID:         c.ID,
		JobType:          "panel_text",
		Stage:            "panels",
		ModelID:          "meta-llama-3.1-8b-instruct",
		IdempotencyKey:   idempKey,
		EstimatedCost:    0.0,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}

	chars, _ := s.Repo.ListCharacters(r.Context(), proj.ID)
	var charNames []string
	for _, ch := range chars {
		charNames = append(charNames, ch.Name)
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	panelResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.GeneratePanelTextResponse, error) {
		return s.AIClient.GeneratePanelText(ctx, gen.GeneratePanelTextRequest{
			JobID:          job.ID,
			HashedUserID:   hashedUID,
			ProjectID:      proj.ID,
			ChapterID:      c.ID,
			ChapterNumber:  c.ChapterNumber,
			ChapterTitle:   c.Title,
			ChapterSummary: c.Summary,
			ChapterScript:  &chapterScript,
			PanelCount:     &panelCount,
			Characters:     charNames,
		})
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Panel text generation failed: %v", err))
		return
	}

	if len(panelResp.Panels) != panelCount {
		errMsg := fmt.Sprintf("Expected %d panels, received %d", panelCount, len(panelResp.Panels))
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadGateway, errMsg)
		return
	}

	var pids []string
	var panels []*domain.Panel
	now := time.Now().UTC()
	for _, pItem := range panelResp.Panels {
		p := &domain.Panel{
			ID:               uuid.NewString(),
			ChapterID:        c.ID,
			PanelNumber:      pItem.PanelNumber,
			SceneDescription: pItem.SceneDescription,
			CharacterActions: pItem.CharacterActions,
			CameraAngle:      pItem.CameraAngle,
			Background:       pItem.Background,
			Narration:        pItem.Narration,
			ImagePrompt:      pItem.ImagePrompt,
			Dialogue:         pItem.Dialogue,
			Status:           "ready",
			UpdatedAt:        now,
		}
		if err := s.Repo.CreatePanel(r.Context(), p); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save panel")
			return
		}
		pids = append(pids, p.ID)
		panels = append(panels, p)
	}

	// Automatically partition panels into pages (SPEC 3.5.1)
	pageSize := s.MaxPanelsPerPage
	if pageSize <= 0 {
		pageSize = 5
	}

	var pages []*domain.Page
	pageNum := 1
	pageCount := (len(pids) + pageSize - 1) / pageSize
	for pageIndex := 0; pageIndex < pageCount; pageIndex++ {
		i := pageIndex * len(pids) / pageCount
		end := (pageIndex + 1) * len(pids) / pageCount
		pg := &domain.Page{
			ID:         uuid.NewString(),
			ChapterID:  c.ID,
			PageNumber: pageNum,
			PanelIDs:   pids[i:end],
			UpdatedAt:  now,
		}
		if err := s.Repo.CreatePage(r.Context(), pg); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save page")
			return
		}
		pages = append(pages, pg)
		pageNum++
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"job_id": job.ID,
		"panels": panels,
		"pages":  pages,
	})
}

// ListPanels handles GET /api/v1/chapters/{id}/panels
func (s *Server) ListPanels(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	chapID := r.PathValue("id")

	c, err := s.Repo.GetChapter(r.Context(), chapID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	panels, _ := s.Repo.ListPanels(r.Context(), chapID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"panels": panels})
}

// UpdatePanel handles PATCH /api/v1/panels/{id}
func (s *Server) UpdatePanel(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	panID := r.PathValue("id")
	var updates struct {
		SceneDescription *string `json:"scene_description"`
		CharacterActions *string `json:"character_actions"`
		CameraAngle      *string `json:"camera_angle"`
		Background       *string `json:"background"`
		Dialogue         *string `json:"dialogue"`
		Narration        *string `json:"narration"`
		ImagePrompt      *string `json:"image_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid panel update")
		return
	}
	if updates.SceneDescription == nil && updates.CharacterActions == nil && updates.CameraAngle == nil &&
		updates.Background == nil && updates.Dialogue == nil && updates.Narration == nil && updates.ImagePrompt == nil {
		writeError(w, http.StatusBadRequest, "Panel update must include at least one editable field")
		return
	}

	p, err := s.Repo.GetPanel(r.Context(), panID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Panel not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), p.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if updates.SceneDescription != nil {
		p.SceneDescription = *updates.SceneDescription
	}
	if updates.CharacterActions != nil {
		p.CharacterActions = *updates.CharacterActions
	}
	if updates.CameraAngle != nil {
		p.CameraAngle = *updates.CameraAngle
	}
	if updates.Background != nil {
		p.Background = *updates.Background
	}
	if updates.Dialogue != nil {
		p.Dialogue = *updates.Dialogue
	}
	if updates.Narration != nil {
		p.Narration = *updates.Narration
	}
	if updates.ImagePrompt != nil {
		p.ImagePrompt = *updates.ImagePrompt
	}

	now := time.Now().UTC()
	p.UpdatedAt = now
	if err := s.Repo.UpdatePanel(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save panel")
		return
	}

	// Cascade: panel edit stales parent page draft approval (SPEC 3.5, 3.6, ARCHITECTURE 5.3)
	pages, err := s.Repo.ListPages(r.Context(), p.ChapterID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load pages for approval invalidation")
		return
	}
	for _, pg := range pages {
		for _, pid := range pg.PanelIDs {
			if pid == p.ID {
				domain.StaleDraftOnPanelEdit(pg, now)
				if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
					writeError(w, http.StatusInternalServerError, "Failed to save page")
					return
				}
				break
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"panel": p})
}

// ListPages handles GET /api/v1/chapters/{id}/pages
func (s *Server) ListPages(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	chapID := r.PathValue("id")

	c, err := s.Repo.GetChapter(r.Context(), chapID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	pages, _ := s.Repo.ListPages(r.Context(), chapID)
	for _, page := range pages {
		approval, approvalErr := s.Repo.GetLatestApproval(r.Context(), "colour_page", page.ID)
		if approvalErr == nil && page.ColourImageUploadedAt != nil && approval.ApprovedAt.After(*page.ColourImageUploadedAt) && !domain.IsApprovalStale(&approval.ApprovedAt, page.UpdatedAt) {
			approvedAt := approval.ApprovedAt
			page.ColourApprovedAt = &approvedAt
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"pages": pages})
}

// UpdatePage handles PATCH /api/v1/pages/{id}
func (s *Server) UpdatePage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		PanelIDs []string `json:"panel_ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.PanelIDs != nil {
		pg.PanelIDs = body.PanelIDs
	}

	// Editing page allocation makes draft approval stale (SPEC 3.5.1, 3.6)
	pg.UpdatedAt = time.Now().UTC()
	if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save page")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"page": pg})
}

// -------------------------------------------------------------
// Draft & Line Art
// -------------------------------------------------------------

// GenerateDraft handles POST /api/v1/pages/{id}/draft
func (s *Server) GenerateDraft(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Kill switch and quota check (SPEC 5.5)
	if err := s.CheckKillSwitchAndQuota(r, userID); err != nil {
		writeGenerationError(w, err)
		return
	}

	// Prerequisite approval check: character references & chapter outline (ARCHITECTURE 5.3)
	chars, _ := s.Repo.ListCharacters(r.Context(), proj.ID)
	allCharsApproved := len(chars) > 0
	for _, ch := range chars {
		if ch.ApprovedAt == nil || domain.IsApprovalStale(ch.ApprovedAt, ch.UpdatedAt) {
			allCharsApproved = false
			break
		}
	}
	outlineApp, _ := s.Repo.GetLatestApproval(r.Context(), "chapter_outline", c.ID)
	outlineApproved := outlineApp != nil && !domain.IsApprovalStale(&outlineApp.ApprovedAt, c.UpdatedAt)

	prereqs := domain.ApprovedPrereqs{
		HasScript:              proj.OriginalScript != "",
		AllCharactersApproved:  allCharsApproved,
		ChapterOutlineApproved: outlineApproved,
	}
	if err := domain.CheckApprovalGate("draft_sketch", prereqs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	allPanels, _ := s.Repo.ListPanels(r.Context(), c.ID)
	panelMap := make(map[string]*domain.Panel)
	for _, p := range allPanels {
		panelMap[p.ID] = p
	}

	var panelItems []gen.PanelTextItem
	var combinedText []string
	for idx, pid := range pg.PanelIDs {
		if p, ok := panelMap[pid]; ok {
			panelItems = append(panelItems, gen.PanelTextItem{
				PanelNumber:      p.PanelNumber,
				SceneDescription: p.SceneDescription,
				CharacterActions: p.CharacterActions,
				CameraAngle:      p.CameraAngle,
				Background:       p.Background,
				Narration:        p.Narration,
				Dialogue:         p.Dialogue,
				ImagePrompt:      p.ImagePrompt,
			})
			combinedText = append(combinedText, p.SceneDescription, p.Dialogue)
		} else {
			panelItems = append(panelItems, gen.PanelTextItem{
				PanelNumber:      idx + 1,
				SceneDescription: fmt.Sprintf("Panel %d", idx+1),
				ImagePrompt:      fmt.Sprintf("Panel %d", idx+1),
			})
		}
	}

	hashedUID := domain.HashUserID(userID)

	// Pre-moderation
	blocked, reason, err := s.checkPromptModeration(r, strings.Join(combinedText, " "), hashedUID)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Prompt moderation failed: %v", err))
		return
	}
	if blocked {
		writeError(w, http.StatusBadRequest, reason)
		return
	}

	idempKey := generationKey(r)
	job, existed, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		EntityID:         pg.ID,
		DebitAmount:      s.Pricing.ImagePriceUSD,
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "draft_sketch",
		Stage:            "draft",
		ModelID:          s.Pricing.ImageModelID,
		IdempotencyKey:   idempKey,
		EstimatedCost:    s.Pricing.ImagePriceUSD,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	if existed {
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"job": job, "image_url": pg.DraftImageURL})
		return
	}

	draftStyle := proj.ArtStyle
	if draftStyle == "" {
		draftStyle = "classic-comic"
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	draftResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.GenerateDraftSketchResponse, error) {
		return s.AIClient.GenerateDraftSketch(ctx, gen.GenerateDraftSketchRequest{
			JobID:               job.ID,
			HashedUserID:        hashedUID,
			ProjectID:           proj.ID,
			PageID:              pg.ID,
			PageNumber:          pg.PageNumber,
			Panels:              panelItems,
			Style:               draftStyle,
			CharacterIdentities: characterRenderIdentities(chars),
		})
	})
	if err != nil {
		if failErr := s.failGeneration(r.Context(), job, err, s.Pricing.ImagePriceUSD); failErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to persist generation failure or refund: %v", failErr))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Draft sketch generation failed: %v", err))
		return
	}

	imgURL := s.ImageHosting.ResolveURL(draftResp.ImageURL)
	now := time.Now().UTC()

	pg.DraftImageURL = imgURL
	pg.DraftImageJobID = &job.ID
	// Cascade: draft regeneration stales line art approval (SPEC 3.6, 3.7, ARCHITECTURE 5.3)
	domain.StaleOnDraftRegeneration(pg, now)
	if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save page")
		return
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	if err := s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	job, err = s.JobService.GetJob(r.Context(), job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{"job": job, "image_url": imgURL})
}

// ApproveDraft handles POST /api/v1/pages/{id}/draft/approve
func (s *Server) ApproveDraft(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	now := time.Now().UTC()
	pg.DraftApprovedAt = &now
	if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save page")
		return
	}

	if err := s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "draft_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save approval")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "page": pg})
}

// GenerateLineArt handles POST /api/v1/pages/{id}/line-art
func (s *Server) GenerateLineArt(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		Variant string `json:"variant,omitempty"`
		Route   string `json:"route,omitempty"`
	}
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		_ = json.Unmarshal(bodyBytes, &reqBody)
	}
	if r.URL.Query().Get("variant") == "painted" || reqBody.Variant == "painted" || reqBody.Route == "painted" {
		s.GeneratePaintedPage(w, r)
		return
	}

	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Kill switch and quota check (SPEC 5.5)
	if err := s.CheckKillSwitchAndQuota(r, userID); err != nil {
		writeGenerationError(w, err)
		return
	}

	// Draft approval gate (ARCHITECTURE 5.3)
	draftApproved := pg.DraftApprovedAt != nil && !domain.IsApprovalStale(pg.DraftApprovedAt, pg.UpdatedAt)
	if !draftApproved {
		writeError(w, http.StatusBadRequest, "Draft sketch must be approved before generating final line art")
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	chars, _ := s.Repo.ListCharacters(r.Context(), proj.ID)

	allPanels, _ := s.Repo.ListPanels(r.Context(), c.ID)
	panelMap := make(map[string]*domain.Panel)
	for _, p := range allPanels {
		panelMap[p.ID] = p
	}

	var panelItems []gen.PanelTextItem
	for idx, pid := range pg.PanelIDs {
		if p, ok := panelMap[pid]; ok {
			panelItems = append(panelItems, gen.PanelTextItem{
				PanelNumber:      p.PanelNumber,
				SceneDescription: p.SceneDescription,
				CharacterActions: p.CharacterActions,
				CameraAngle:      p.CameraAngle,
				Background:       p.Background,
				Narration:        p.Narration,
				Dialogue:         p.Dialogue,
				ImagePrompt:      p.ImagePrompt,
			})
		} else {
			panelItems = append(panelItems, gen.PanelTextItem{
				PanelNumber:      idx + 1,
				SceneDescription: fmt.Sprintf("Panel %d", idx+1),
				ImagePrompt:      fmt.Sprintf("Panel %d", idx+1),
			})
		}
	}

	idempKey := generationKey(r)
	hashedUID := domain.HashUserID(userID)

	job, existed, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		EntityID:         pg.ID,
		DebitAmount:      s.Pricing.ImagePriceUSD,
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "final_line_art",
		Stage:            "line_art",
		ModelID:          s.Pricing.ImageModelID,
		IdempotencyKey:   idempKey,
		EstimatedCost:    s.Pricing.ImagePriceUSD,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	if existed {
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"job": job, "image_url": pg.LineArtImageURL})
		return
	}

	lineArtStyle := proj.ArtStyle
	if lineArtStyle == "" {
		lineArtStyle = "classic-comic"
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	lineArtResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.GenerateLineArtResponse, error) {
		return s.AIClient.GenerateLineArt(ctx, gen.GenerateLineArtRequest{
			JobID:               job.ID,
			HashedUserID:        hashedUID,
			ProjectID:           proj.ID,
			PageID:              pg.ID,
			PageNumber:          pg.PageNumber,
			DraftImageURL:       pg.DraftImageURL,
			Panels:              panelItems,
			Style:               lineArtStyle,
			CharacterIdentities: characterRenderIdentities(chars),
		})
	})
	if err != nil {
		if failErr := s.failGeneration(r.Context(), job, err, s.Pricing.ImagePriceUSD); failErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to persist generation failure or refund: %v", failErr))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Line art generation failed: %v", err))
		return
	}

	imgURL := s.ImageHosting.ResolveURL(lineArtResp.ImageURL)
	now := time.Now().UTC()

	pg.LineArtImageURL = imgURL
	pg.LineArtImageJobID = &job.ID
	pg.ColourImageURL = ""
	pg.ColourImageUploadedAt = nil
	domain.StaleOnLineArtRegeneration(pg, now)
	if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save page")
		return
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	if err := s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	job, err = s.JobService.GetJob(r.Context(), job.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{"job": job, "image_url": imgURL})
}

// ApproveLineArt handles POST /api/v1/pages/{id}/line-art/approve
func (s *Server) ApproveLineArt(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	now := time.Now().UTC()
	pg.LineArtApprovedAt = &now
	if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save page")
		return
	}

	if err := s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "line_art_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save approval")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "page": pg})
}

// GeneratePaintedPage handles POST /api/v1/pages/{id}/painted-page (opt-in prototype route)
func (s *Server) GeneratePaintedPage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Kill switch and quota check (SPEC 5.5)
	if err := s.CheckKillSwitchAndQuota(r, userID); err != nil {
		writeGenerationError(w, err)
		return
	}

	var reqBody struct {
		Variant string `json:"variant"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
	}

	chosenVariant := reqBody.Variant
	if chosenVariant == "" || chosenVariant == "painted" {
		chosenVariant = "draft-conditioned"
	}
	if chosenVariant != "draft-conditioned" && chosenVariant != "direct-from-brief" {
		chosenVariant = "draft-conditioned"
	}

	// Gate checks:
	// 1. Chapter outline and character references must be approved
	allChars, _ := s.Repo.ListCharacters(r.Context(), proj.ID)
	allRefsApproved := len(allChars) > 0
	for _, ch := range allChars {
		approval, err := s.Repo.GetLatestApproval(r.Context(), "character_reference", ch.ID)
		if err != nil || ch.ReferenceImageURL == "" || domain.IsApprovalStale(&approval.ApprovedAt, ch.UpdatedAt) {
			allRefsApproved = false
			break
		}
	}
	outlineApproval, err := s.Repo.GetLatestApproval(r.Context(), "chapter_outline", c.ID)
	outlineApproved := err == nil && domain.CheckChapterOutlineApproval(&outlineApproval.ApprovedAt, c.UpdatedAt) == nil

	if chosenVariant == "draft-conditioned" {
		draftApproved := pg.DraftApprovedAt != nil && !domain.IsApprovalStale(pg.DraftApprovedAt, pg.UpdatedAt)
		if err := domain.CheckApprovalGate("final_line_art", domain.ApprovedPrereqs{
			AllCharactersApproved:  allRefsApproved,
			ChapterOutlineApproved: outlineApproved,
			DraftSketchApproved:    draftApproved,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "Draft sketch must be approved before generating draft-conditioned painted page")
			return
		}
	} else {
		// direct-from-brief variant: does not require draft approval, but requires character & outline approvals
		if err := domain.CheckApprovalGate("draft_sketch", domain.ApprovedPrereqs{
			AllCharactersApproved:  allRefsApproved,
			ChapterOutlineApproved: outlineApproved,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "Prerequisite stage approvals missing for direct-from-brief generation")
			return
		}
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	allPanels, _ := s.Repo.ListPanels(r.Context(), c.ID)
	panelMap := make(map[string]*domain.Panel)
	for _, p := range allPanels {
		panelMap[p.ID] = p
	}

	var panelItems []gen.PanelTextItem
	for idx, pid := range pg.PanelIDs {
		if p, ok := panelMap[pid]; ok {
			panelItems = append(panelItems, gen.PanelTextItem{
				PanelNumber:      p.PanelNumber,
				SceneDescription: p.SceneDescription,
				CharacterActions: p.CharacterActions,
				CameraAngle:      p.CameraAngle,
				Background:       p.Background,
				Narration:        p.Narration,
				Dialogue:         p.Dialogue,
				ImagePrompt:      p.ImagePrompt,
			})
		} else {
			panelItems = append(panelItems, gen.PanelTextItem{
				PanelNumber:      idx + 1,
				SceneDescription: fmt.Sprintf("Panel %d", idx+1),
				ImagePrompt:      fmt.Sprintf("Panel %d", idx+1),
			})
		}
	}

	idempKey := generationKey(r)
	hashedUID := domain.HashUserID(userID)

	job, existed, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		EntityID:         pg.ID,
		DebitAmount:      s.Pricing.ImagePriceUSD,
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "final_line_art",
		Stage:            "line_art",
		ModelID:          s.Pricing.ImageModelID,
		IdempotencyKey:   idempKey,
		EstimatedCost:    s.Pricing.ImagePriceUSD,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	if existed {
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"job": job, "image_url": pg.PaintedPageImageURL, "variant": pg.PaintedPageVariant})
		return
	}

	artStyle := proj.ArtStyle
	if artStyle == "" {
		artStyle = "classic-comic"
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	variantVal := gen.GeneratePaintedPageRequestVariant(chosenVariant)
	var draftURL *string
	if chosenVariant == "draft-conditioned" && pg.DraftImageURL != "" {
		draftURL = &pg.DraftImageURL
	}

	paintedResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.GeneratePaintedPageResponse, error) {
		return s.AIClient.GeneratePaintedPage(ctx, gen.GeneratePaintedPageRequest{
			JobID:               job.ID,
			HashedUserID:        hashedUID,
			ProjectID:           proj.ID,
			PageID:              pg.ID,
			PageNumber:          pg.PageNumber,
			DraftImageURL:       draftURL,
			Panels:              panelItems,
			Style:               artStyle,
			Variant:             &variantVal,
			CharacterIdentities: characterRenderIdentities(allChars),
		})
	})
	if err != nil {
		if failErr := s.failGeneration(r.Context(), job, err, s.Pricing.ImagePriceUSD); failErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to persist generation failure or refund: %v", failErr))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Painted page generation failed: %v", err))
		return
	}

	imgURL := s.ImageHosting.ResolveURL(paintedResp.ImageURL)
	now := time.Now().UTC()

	pg.PaintedPageImageURL = imgURL
	pg.PaintedPageImageJobID = &job.ID
	pg.PaintedPageVariant = chosenVariant
	domain.StaleOnPaintedPageRegeneration(pg, now)
	if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save page")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"job":       job,
		"image_url": imgURL,
		"variant":   chosenVariant,
		"page":      pg,
	})
}

// ApprovePaintedPage handles POST /api/v1/pages/{id}/painted-page/approve
func (s *Server) ApprovePaintedPage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if pg.PaintedPageImageURL == "" {
		writeError(w, http.StatusBadRequest, "No painted page generated to approve")
		return
	}

	now := time.Now().UTC()
	pg.PaintedPageApprovedAt = &now
	if err := s.Repo.UpdatePage(r.Context(), pg); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save page")
		return
	}

	if err := s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "painted_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save approval")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "page": pg})
}

// -------------------------------------------------------------
// Colouring
// -------------------------------------------------------------

// GetColourPalette handles GET /api/v1/pages/{id}/colour-palette
func (s *Server) GetColourPalette(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	if _, status := s.getOwnedPage(r, pageID); status != http.StatusOK {
		message := "forbidden"
		if status == http.StatusNotFound {
			message = "Page not found"
		}
		writeError(w, status, message)
		return
	}
	cp, err := s.Repo.GetColourPalette(r.Context(), pageID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"palette": map[string]string{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"palette": cp.Palette})
}

// SuggestColourPalette handles POST /api/v1/pages/{id}/colour-palette/suggest
func (s *Server) SuggestColourPalette(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Kill switch and quota check (SPEC 5.5)
	if err := domain.CheckKillSwitchAndQuota(s.IsKillSwitchActive(), 0, 0, 0, 0); err != nil {
		writeGenerationError(w, err)
		return
	}

	// Line art approval gate (SPEC 5.3, ARCHITECTURE 5.3)
	lineArtApproved := pg.LineArtApprovedAt != nil && !domain.IsApprovalStale(pg.LineArtApprovedAt, pg.UpdatedAt)
	if !lineArtApproved {
		writeError(w, http.StatusBadRequest, "Final line art must be approved before colour palette suggestion")
		return
	}

	lineArtURL := pg.LineArtImageURL
	parsedURL, parseErr := url.Parse(lineArtURL)
	if parseErr != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		writeError(w, http.StatusConflict, "Page has no valid stored line art image URL; generate and approve line art before suggesting a palette")
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	idempKey := generationKey(r)
	hashedUID := domain.HashUserID(userID)

	job, existed, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		EntityID:         pg.ID,
		DebitAmount:      s.Pricing.ColourEstimateUSD,
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "colour_suggestion",
		Stage:            "colour",
		ModelID:          s.Pricing.VisionModelID,
		IdempotencyKey:   idempKey,
		EstimatedCost:    s.Pricing.ColourEstimateUSD,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	if existed {
		palette := map[string]interface{}{}
		if saved, err := s.Repo.GetColourPalette(r.Context(), pageID); err == nil {
			palette = saved.Palette
		}
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"job_id": job.ID, "palette": palette})
		return
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	palResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.SuggestColourPaletteResponse, error) {
		return s.AIClient.SuggestColourPalette(ctx, gen.SuggestColourPaletteRequest{
			JobID:           job.ID,
			HashedUserID:    hashedUID,
			ProjectID:       proj.ID,
			PageID:          pg.ID,
			LineArtImageURL: lineArtURL,
			PanelContext:    fmt.Sprintf("Page %d of chapter %d", pg.PageNumber, c.ChapterNumber),
			Style:           &proj.ArtStyle,
		})
	})
	if err != nil {
		if failErr := s.failGeneration(r.Context(), job, err, s.Pricing.ColourEstimateUSD); failErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to persist generation failure or refund: %v", failErr))
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Colour palette suggestion failed: %v", err))
		return
	}

	palette := make(map[string]interface{})
	for _, item := range palResp.Palette {
		palette[item.RegionName] = item.HexColor
	}

	if err := s.Repo.SaveColourPalette(r.Context(), &domain.ColourPalette{
		PageID:          pageID,
		SuggestionJobID: &job.ID,
		Palette:         palette,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save colour palette")
		return
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	if err := s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":  job.ID,
		"palette": palette,
	})
}

// UpdateColourPalette handles PATCH /api/v1/pages/{id}/colour-palette
func (s *Server) UpdateColourPalette(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	if _, status := s.getOwnedPage(r, pageID); status != http.StatusOK {
		message := "forbidden"
		if status == http.StatusNotFound {
			message = "Page not found"
		}
		writeError(w, status, message)
		return
	}
	var body struct {
		Palette map[string]interface{} `json:"palette"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := s.Repo.SaveColourPalette(r.Context(), &domain.ColourPalette{
		PageID:  pageID,
		Palette: body.Palette,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save colour palette")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"palette": body.Palette})
}

// detectColourImageType identifies the supported media type from image bytes, never from a client header.
func detectColourImageType(data []byte) (gen.StoreColourImageRequestMediaType, bool) {
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return gen.ImagePng, true
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return gen.ImageWebp, true
	}
	return "", false
}

// UploadColourImage stores the browser-coloured page image beside its unchanged line art.
func (s *Server) UploadColourImage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")
	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}
	chapter, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}
	project, err := s.Repo.GetProject(r.Context(), chapter.ProjectID)
	if err != nil || project.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if pg.LineArtImageURL == "" || pg.LineArtApprovedAt == nil || domain.IsApprovalStale(pg.LineArtApprovedAt, pg.UpdatedAt) {
		writeError(w, http.StatusBadRequest, "Approved line art is required before uploading page colours")
		return
	}
	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}
	imageBytes, err := io.ReadAll(io.LimitReader(r.Body, s.Limits.ImageBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not read coloured page image")
		return
	}
	if len(imageBytes) == 0 {
		writeError(w, http.StatusBadRequest, "Coloured page image is empty")
		return
	}
	if int64(len(imageBytes)) > s.Limits.ImageBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Coloured page image exceeds configured byte limit")
		return
	}
	mediaType, ok := detectColourImageType(imageBytes)
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "Coloured page image must be PNG or WebP")
		return
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		writeError(w, http.StatusBadRequest, "Invalid coloured page image")
		return
	}
	if int64(config.Width) > s.Limits.ImagePixels/int64(config.Height) {
		writeError(w, http.StatusRequestEntityTooLarge, "Coloured page image exceeds configured pixel limit")
		return
	}
	stored, err := s.AIClient.StoreColourImage(r.Context(), gen.StoreColourImageRequest{
		PageID:      pageID,
		ImageBase64: base64.StdEncoding.EncodeToString(imageBytes),
		MediaType:   mediaType,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Could not store coloured page image: %v", err))
		return
	}
	imageURL := s.ImageHosting.ResolveURL(stored.ImageURL)
	uploadedAt := time.Now().UTC()
	if err := s.Repo.SavePageColourImage(r.Context(), pageID, imageURL, uploadedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save coloured page image")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"colour_image_url":         imageURL,
		"colour_image_uploaded_at": uploadedAt,
	})
}

// ApproveColour handles POST /api/v1/pages/{id}/colour/approve
func (s *Server) ApproveColour(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Page not found")
		return
	}
	chapter, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Chapter not found")
		return
	}

	project, err := s.Repo.GetProject(r.Context(), chapter.ProjectID)
	if err != nil || project.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if pg.ColourImageURL == "" || pg.ColourImageUploadedAt == nil {
		writeError(w, http.StatusBadRequest, "Upload the coloured page image before approving colour")
		return
	}
	if pg.LineArtApprovedAt == nil || domain.IsApprovalStale(pg.LineArtApprovedAt, pg.UpdatedAt) {
		writeError(w, http.StatusBadRequest, "Current approved line art is required before approving colour")
		return
	}

	now := time.Now().UTC()
	if err := s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:        chapter.ProjectID,
		EntityType:       "colour_page",
		EntityID:         pageID,
		ApprovedByUserID: userID,
		ApprovedAt:       now,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save approval")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "colour_approved_at": now})
}

// -------------------------------------------------------------
// Estimates, Jobs, Credits
// -------------------------------------------------------------

// EstimateCost handles POST /api/v1/projects/{id}/estimate
func (s *Server) EstimateCost(w http.ResponseWriter, r *http.Request) {
	if _, status := s.getOwnedProject(r, r.PathValue("id")); status != http.StatusOK {
		message := "forbidden"
		if status == http.StatusNotFound {
			message = "Project not found"
		}
		writeError(w, status, message)
		return
	}
	var body struct {
		Stage      string `json:"stage"`
		ImageCount int    `json:"image_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	est := s.Pricing.Estimate(body.Stage, body.ImageCount)
	writeJSON(w, http.StatusOK, est)
}

// GetJobStatus handles GET /api/v1/generation-jobs/{id}
func (s *Server) GetJobStatus(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	id := r.PathValue("id")

	job, err := s.JobService.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}

	if job.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if (job.Status == jobs.StatusPending || job.Status == jobs.StatusRunning) && s.AIClient != nil {
		// Reconcile a failed worker delivery even if the submitting HTTP connection
		// was interrupted. Successful jobs complete after saving the entity link.
		if status, statusErr := s.AIClient.GetJobStatus(r.Context(), job.ID); statusErr == nil && status != nil && status.Status == jobs.StatusFailed {
			if err := s.JobService.UpdateJobStatus(r.Context(), job.ID, jobs.StatusFailed, status.ActualCost, status.ErrorMessage); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			job, err = s.JobService.GetJob(r.Context(), job.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	if job.Status == jobs.StatusFailed {
		if err := s.refundFailedGeneration(r.Context(), job); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to refund generation: %v", err))
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"job": job})
}

// CancelJob handles POST /api/v1/generation-jobs/{id}/cancel
func (s *Server) CancelJob(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	id := r.PathValue("id")

	job, err := s.JobService.GetJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Job not found")
		return
	}

	if job.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if err := s.JobService.CancelJob(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Job cancelled successfully"})
}

// CreditUsage handles GET /api/v1/me/credit-usage
func (s *Server) CreditUsage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

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

// -------------------------------------------------------------
// Exports
// -------------------------------------------------------------

// ExportProject handles POST /api/v1/projects/{id}/export
func (s *Server) ExportProject(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	projectID := r.PathValue("id")

	proj, err := s.Repo.GetProject(r.Context(), projectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	chapters, _ := s.Repo.ListChapters(r.Context(), proj.ID)
	var exportPages []gen.PageExportItem
	for _, ch := range chapters {
		pages, _ := s.Repo.ListPages(r.Context(), ch.ID)
		for _, pg := range pages {
			type finishCandidate struct {
				url         string
				originalURL *string
				approvedAt  time.Time
			}
			var candidates []finishCandidate

			if pg.LineArtImageURL != "" && pg.LineArtApprovedAt != nil && !domain.IsApprovalStale(pg.LineArtApprovedAt, pg.UpdatedAt) {
				var orig *string
				if pg.DraftImageURL != "" {
					orig = &pg.DraftImageURL
				}
				candidates = append(candidates, finishCandidate{
					url:         pg.LineArtImageURL,
					originalURL: orig,
					approvedAt:  *pg.LineArtApprovedAt,
				})
			}

			if pg.ColourImageURL != "" && pg.ColourImageUploadedAt != nil {
				approval, approvalErr := s.Repo.GetLatestApproval(r.Context(), "colour_page", pg.ID)
				if approvalErr == nil && approval.ApprovedAt.After(*pg.ColourImageUploadedAt) && !domain.IsApprovalStale(&approval.ApprovedAt, pg.UpdatedAt) {
					var orig *string
					if pg.LineArtImageURL != "" {
						orig = &pg.LineArtImageURL
					} else if pg.DraftImageURL != "" {
						orig = &pg.DraftImageURL
					}
					candidates = append(candidates, finishCandidate{
						url:         pg.ColourImageURL,
						originalURL: orig,
						approvedAt:  approval.ApprovedAt,
					})
				}
			}

			if pg.PaintedPageImageURL != "" && pg.PaintedPageApprovedAt != nil && !domain.IsApprovalStale(pg.PaintedPageApprovedAt, pg.UpdatedAt) {
				var orig *string
				if pg.DraftImageURL != "" {
					orig = &pg.DraftImageURL
				} else if pg.LineArtImageURL != "" {
					orig = &pg.LineArtImageURL
				}
				candidates = append(candidates, finishCandidate{
					url:         pg.PaintedPageImageURL,
					originalURL: orig,
					approvedAt:  *pg.PaintedPageApprovedAt,
				})
			}

			var imgURL string
			var originalImageURL *string

			if len(candidates) > 0 {
				best := candidates[0]
				for _, cand := range candidates[1:] {
					if cand.approvedAt.After(best.approvedAt) {
						best = cand
					}
				}
				imgURL = best.url
				originalImageURL = best.originalURL
			} else {
				if pg.LineArtImageURL != "" {
					imgURL = pg.LineArtImageURL
				} else if pg.PaintedPageImageURL != "" {
					imgURL = pg.PaintedPageImageURL
				} else if pg.DraftImageURL != "" {
					imgURL = pg.DraftImageURL
				} else {
					imgURL = fmt.Sprintf("pages/%s.png", pg.ID)
				}
			}
			panelCount := len(pg.PanelIDs)
			provenance := map[string]interface{}{"c2pa": true}
			exportPages = append(exportPages, gen.PageExportItem{
				PageNumber:         len(exportPages) + 1,
				ImageURL:           imgURL,
				OriginalImageURL:   originalImageURL,
				PanelCount:         optionalInt(panelCount),
				ProvenanceMetadata: &provenance,
			})
		}
	}
	if len(exportPages) == 0 {
		panelCount := 1
		provenance := map[string]interface{}{"c2pa": true}
		exportPages = append(exportPages, gen.PageExportItem{
			PageNumber:         1,
			ImageURL:           fmt.Sprintf("projects/%s/page_1.png", proj.ID),
			PanelCount:         &panelCount,
			ProvenanceMetadata: &provenance,
		})
	}

	hashedUID := domain.HashUserID(userID)
	idempKey := uuid.NewString()

	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		KillSwitchActive: s.IsKillSwitchActive(),
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "assemble_pdf",
		Stage:            "export",
		ModelID:          "pdf-assembler",
		IdempotencyKey:   idempKey,
		EstimatedCost:    0.0,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	pdfResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.AssemblePdfResponse, error) {
		return s.AIClient.AssemblePdf(ctx, gen.AssemblePdfRequest{
			JobID:               job.ID,
			HashedUserID:        hashedUID,
			ProjectID:           proj.ID,
			Title:               proj.Title,
			PageSize:            gen.A4,
			IncludeAIDisclosure: true,
			Pages:               exportPages,
		})
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Assemble PDF failed: %v", err))
		return
	}

	exp := &domain.Export{
		ID:                          uuid.NewString(),
		ProjectID:                   proj.ID,
		FileURL:                     pdfResp.PdfURL,
		Format:                      "pdf",
		Status:                      "completed",
		ProvenanceMetadataPreserved: pdfResp.ProvenanceMetadataPreserved,
		CreatedAt:                   time.Now().UTC(),
	}
	if err := s.Repo.CreateExport(r.Context(), exp); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save export: %v", err))
		return
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{"export": exp, "job_id": job.ID})
}

// GetExport handles GET /api/v1/exports/{id}
func (s *Server) GetExport(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	id := r.PathValue("id")

	exp, err := s.Repo.GetExport(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Export not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), exp.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"export": exp})
}

// DeriveStorageKey extracts and validates a relative storage key from a raw URL or path,
// stripping any prefix up to /storage/fake/ and guarding against path traversal.
func DeriveStorageKey(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("empty storage URL")
	}

	clean := trimmed
	if u, err := url.Parse(trimmed); err == nil && u.Path != "" {
		clean = u.Path
	}

	const marker = "/storage/fake/"
	if idx := strings.Index(clean, marker); idx != -1 {
		clean = clean[idx+len(marker):]
	} else if strings.HasPrefix(clean, "storage/fake/") {
		clean = strings.TrimPrefix(clean, "storage/fake/")
	} else {
		clean = strings.TrimPrefix(clean, "/")
	}

	if unescaped, err := url.PathUnescape(clean); err == nil {
		clean = unescaped
	}

	clean = strings.TrimPrefix(clean, "/")
	norm := path.Clean("/" + clean)
	norm = strings.TrimPrefix(norm, "/")

	if norm == "" || norm == "." || norm == ".." || strings.HasPrefix(norm, "../") {
		return "", fmt.Errorf("invalid storage key: path traversal detected in %q", rawURL)
	}

	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("invalid storage key: path traversal detected in %q", rawURL)
		}
	}

	return norm, nil
}

// DownloadExport handles GET /api/v1/exports/{id}/download
func (s *Server) DownloadExport(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	id := r.PathValue("id")

	exp, err := s.Repo.GetExport(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Export not found")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), exp.ProjectID)
	if err != nil || proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	fileURL := strings.TrimSpace(exp.FileURL)
	if fileURL == "" {
		writeError(w, http.StatusNotFound, "Export file not found")
		return
	}

	// 1. Local file (if fileURL is a relative local path that doesn't target fake storage)
	if strings.HasPrefix(fileURL, "/") && !strings.Contains(fileURL, "/storage/fake/") {
		mediaDir := os.Getenv("MEDIA_DIR")
		if mediaDir == "" {
			mediaDir = "./media"
		}
		localPath := filepath.Join(mediaDir, filepath.Clean(fileURL))
		if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"comic_%s.pdf\"", exp.ProjectID))
			http.ServeFile(w, r, localPath)
			return
		}
	}

	// 2. Resolve upstream target URL
	var targetURL string
	var aiBaseURL string
	if client, ok := s.AIClient.(*gen.AIServiceClient); ok && client.BaseURL != "" {
		aiBaseURL = client.BaseURL
	}

	if aiBaseURL != "" {
		key, err := DeriveStorageKey(fileURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid storage key")
			return
		}
		targetURL = strings.TrimRight(aiBaseURL, "/") + "/storage/fake/" + key
	} else if strings.HasPrefix(fileURL, "http://") || strings.HasPrefix(fileURL, "https://") {
		targetURL = fileURL
	} else {
		key, err := DeriveStorageKey(fileURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid storage key")
			return
		}
		targetURL = "http://localhost:8000/storage/fake/" + key
	}

	// 3. Proxy request upstream
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to create upstream request")
		return
	}

	httpClient := http.DefaultClient
	if client, ok := s.AIClient.(*gen.AIServiceClient); ok && client.HTTPClient != nil {
		httpClient = client.HTTPClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to retrieve export file from storage")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"comic_%s.pdf\"", exp.ProjectID))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	if resp.StatusCode == http.StatusNotFound {
		writeError(w, http.StatusNotFound, "Export file not found")
		return
	}

	writeError(w, http.StatusBadGateway, "Failed to retrieve export file from storage")
}

// Keep the public failure statuses while the service owns the transaction.
func generationErrorStatus(err error) int {
	switch {
	case errors.Is(err, credits.ErrInsufficientCredits), errors.Is(err, db.ErrInsufficientCredit):
		return http.StatusPaymentRequired
	case errors.Is(err, domain.ErrKillSwitchActive), errors.Is(err, domain.ErrQuotaExceeded):
		return http.StatusTooManyRequests
	case errors.Is(err, domain.ErrMissingPrerequisiteApproval), errors.Is(err, domain.ErrStaleApproval):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrActiveJobExists):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// Stable error codes let clients distinguish lifetime book and rolling daily caps.
func writeGenerationError(w http.ResponseWriter, err error) {
	code := ""
	switch {
	case errors.Is(err, domain.ErrBookBudgetExceeded):
		code = "book_budget_reached"
	case errors.Is(err, domain.ErrDailyImageLimitReached):
		code = "daily_image_limit_reached"
	}
	if code == "" {
		writeError(w, generationErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, generationErrorStatus(err), map[string]string{"error": err.Error(), "code": code})
}
