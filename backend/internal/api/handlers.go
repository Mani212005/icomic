package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
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
	if err != nil || status == nil || status.Status != "done" || status.ActualCost == nil {
		return 0
	}
	return *status.ActualCost
}

func (s *Server) getUserID(r *http.Request) string {
	return auth.UserIDFromContext(r.Context())
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
		body.ChapterCount = 1
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
	ch.UpdatedAt = time.Now().UTC()
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
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	var body struct {
		Prompt string `json:"prompt"`
		Style  string `json:"style"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	prompt := body.Prompt
	if prompt == "" {
		prompt = ch.Description
	}
	if prompt == "" {
		prompt = ch.Name
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

	idempKey := uuid.NewString()
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "character_reference",
		Stage:          "reference",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: idempKey,
		EstimatedCost:  credits.ImageUnitPrice,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Debit credit ledger
	_, err = s.CreditService.Debit(r.Context(), credits.DebitRequest{
		UserID:          userID,
		ProjectID:       proj.ID,
		GenerationJobID: job.ID,
		Amount:          credits.ImageUnitPrice,
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusPaymentRequired, fmt.Sprintf("Debit failed: %v", err))
		return
	}

	if s.AIClient == nil {
		_, _ = s.CreditService.Refund(r.Context(), credits.RefundRequest{
			UserID:          userID,
			ProjectID:       proj.ID,
			GenerationJobID: job.ID,
			Amount:          credits.ImageUnitPrice,
		})
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	refResp, err := s.AIClient.GenerateCharacterReference(r.Context(), gen.GenerateCharacterReferenceRequest{
		JobID:           job.ID,
		HashedUserID:    hashedUID,
		ProjectID:       proj.ID,
		CharacterID:     ch.ID,
		CharacterName:   ch.Name,
		Prompt:          prompt,
		ReferenceImages: nil,
		Style:           style,
	})
	if err != nil {
		_, _ = s.CreditService.Refund(r.Context(), credits.RefundRequest{
			UserID:          userID,
			ProjectID:       proj.ID,
			GenerationJobID: job.ID,
			Amount:          credits.ImageUnitPrice,
		})
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Character reference generation failed: %v", err))
		return
	}

	imgURL := s.ImageHosting.ResolveURL(refResp.ImageURL)
	now := time.Now().UTC()
	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	ch.ReferenceImageURL = imgURL
	ch.ReferenceImageJobID = &job.ID
	ch.UpdatedAt = now
	_ = s.Repo.UpdateCharacter(r.Context(), ch)

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

	now := time.Now().UTC()
	ch.ApprovedAt = &now
	ch.UpdatedAt = now
	_ = s.Repo.UpdateCharacter(r.Context(), ch)

	_ = s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "character_reference",
		EntityID:                 ch.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: credits.ImageUnitPrice,
	})

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
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "script_analysis",
		Stage:          "script",
		ModelID:        "meta-llama-3.1-8b-instruct",
		IdempotencyKey: idempKey,
		EstimatedCost:  0.0,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	targetChapters := proj.ChapterCount
	aiResp, err := s.AIClient.AnalyzeScript(r.Context(), gen.AnalyzeScriptRequest{
		JobID:              job.ID,
		HashedUserID:       hashedUID,
		ProjectID:          proj.ID,
		Script:             proj.OriginalScript,
		TargetChapterCount: &targetChapters,
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
		_ = s.Repo.CreateCharacter(r.Context(), ch)
		chars = append(chars, ch)
	}

	if aiResp.SuggestedChapterCount > 0 && proj.ChapterCount <= 0 {
		proj.ChapterCount = aiResp.SuggestedChapterCount
		_ = s.Repo.UpdateProject(r.Context(), proj)
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
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "chapter_outline",
		Stage:          "outline",
		ModelID:        "meta-llama-3.1-8b-instruct",
		IdempotencyKey: idempKey,
		EstimatedCost:  0.0,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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

	outlineResp, err := s.AIClient.GenerateChapterOutline(r.Context(), gen.GenerateChapterOutlineRequest{
		JobID:              job.ID,
		HashedUserID:       hashedUID,
		ProjectID:          proj.ID,
		Script:             proj.OriginalScript,
		ChapterCount:       chapCount,
		ApprovedCharacters: approvedProfiles,
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
		_ = s.Repo.CreateChapter(r.Context(), chap)
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
	_ = s.Repo.UpdateChapter(r.Context(), c)
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
	_ = s.Repo.UpdateChapter(r.Context(), c)

	now := time.Now().UTC()
	_ = s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "chapter_outline",
		EntityID:                 c.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: 0.01,
	})

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

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	hashedUID := domain.HashUserID(userID)

	// Pre-moderation
	blocked, reason, err := s.checkPromptModeration(r, c.Summary+" "+c.Title, hashedUID)
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
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "panel_text",
		Stage:          "panels",
		ModelID:        "meta-llama-3.1-8b-instruct",
		IdempotencyKey: idempKey,
		EstimatedCost:  0.0,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	chars, _ := s.Repo.ListCharacters(r.Context(), proj.ID)
	var charNames []string
	for _, ch := range chars {
		charNames = append(charNames, ch.Name)
	}

	panelResp, err := s.AIClient.GeneratePanelText(r.Context(), gen.GeneratePanelTextRequest{
		JobID:          job.ID,
		HashedUserID:   hashedUID,
		ProjectID:      proj.ID,
		ChapterID:      c.ID,
		ChapterNumber:  c.ChapterNumber,
		ChapterTitle:   c.Title,
		ChapterSummary: c.Summary,
		Characters:     charNames,
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Panel text generation failed: %v", err))
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
			Dialogue:         pItem.Dialogue,
			Status:           "ready",
			UpdatedAt:        now,
		}
		_ = s.Repo.CreatePanel(r.Context(), p)
		pids = append(pids, p.ID)
		panels = append(panels, p)
	}

	// Automatically partition panels into pages (SPEC 3.5.1)
	pageSize := s.MaxPanelsPerPage
	if pageSize <= 0 {
		pageSize = 4
	}

	var pages []*domain.Page
	pageNum := 1
	for i := 0; i < len(pids); i += pageSize {
		end := i + pageSize
		if end > len(pids) {
			end = len(pids)
		}
		pg := &domain.Page{
			ID:         uuid.NewString(),
			ChapterID:  c.ID,
			PageNumber: pageNum,
			PanelIDs:   pids[i:end],
			UpdatedAt:  now,
		}
		_ = s.Repo.CreatePage(r.Context(), pg)
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

	now := time.Now().UTC()
	p.UpdatedAt = now
	_ = s.Repo.UpdatePanel(r.Context(), p)

	// Cascade: panel edit stales parent page draft approval (SPEC 3.5, 3.6, ARCHITECTURE 5.3)
	if pages, err := s.Repo.ListPages(r.Context(), p.ChapterID); err == nil {
		for _, pg := range pages {
			for _, pid := range pg.PanelIDs {
				if pid == p.ID {
					domain.StaleDraftOnPanelEdit(pg, now)
					_ = s.Repo.UpdatePage(r.Context(), pg)
					break
				}
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
	_ = s.Repo.UpdatePage(r.Context(), pg)
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
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	// Prerequisite approval check: character references & chapter outline (ARCHITECTURE 5.3)
	chars, _ := s.Repo.ListCharacters(r.Context(), proj.ID)
	allCharsApproved := len(chars) > 0
	var charImageURLs []string
	for _, ch := range chars {
		if ch.ApprovedAt == nil || domain.IsApprovalStale(ch.ApprovedAt, ch.UpdatedAt) {
			allCharsApproved = false
			break
		}
		if ch.ReferenceImageURL != "" {
			charImageURLs = append(charImageURLs, ch.ReferenceImageURL)
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
				Dialogue:         p.Dialogue,
				ImagePrompt:      p.SceneDescription,
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

	idempKey := uuid.NewString()
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "draft_sketch",
		Stage:          "draft",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: idempKey,
		EstimatedCost:  credits.ImageUnitPrice,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, err = s.CreditService.Debit(r.Context(), credits.DebitRequest{
		UserID:          userID,
		ProjectID:       proj.ID,
		GenerationJobID: job.ID,
		Amount:          credits.ImageUnitPrice,
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusPaymentRequired, fmt.Sprintf("Debit failed: %v", err))
		return
	}

	draftStyle := proj.ArtStyle
	if draftStyle == "" {
		draftStyle = "classic-comic"
	}

	draftResp, err := s.AIClient.GenerateDraftSketch(r.Context(), gen.GenerateDraftSketchRequest{
		JobID:                       job.ID,
		HashedUserID:                hashedUID,
		ProjectID:                   proj.ID,
		PageID:                      pg.ID,
		PageNumber:                  pg.PageNumber,
		Panels:                      panelItems,
		Style:                       draftStyle,
		CharacterReferenceImageURLs: optionalStringSlice(charImageURLs),
	})
	if err != nil {
		_, _ = s.CreditService.Refund(r.Context(), credits.RefundRequest{
			UserID:          userID,
			ProjectID:       proj.ID,
			GenerationJobID: job.ID,
			Amount:          credits.ImageUnitPrice,
		})
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Draft sketch generation failed: %v", err))
		return
	}

	imgURL := s.ImageHosting.ResolveURL(draftResp.ImageURL)
	now := time.Now().UTC()
	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	pg.DraftImageURL = imgURL
	pg.DraftImageJobID = &job.ID
	// Cascade: draft regeneration stales line art approval (SPEC 3.6, 3.7, ARCHITECTURE 5.3)
	domain.StaleOnDraftRegeneration(pg, now)
	_ = s.Repo.UpdatePage(r.Context(), pg)

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
	pg.UpdatedAt = now
	_ = s.Repo.UpdatePage(r.Context(), pg)

	_ = s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "draft_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: credits.ImageUnitPrice,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "page": pg})
}

// GenerateLineArt handles POST /api/v1/pages/{id}/line-art
func (s *Server) GenerateLineArt(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusTooManyRequests, err.Error())
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
	var charImageURLs []string
	for _, ch := range chars {
		if ch.ReferenceImageURL != "" {
			charImageURLs = append(charImageURLs, ch.ReferenceImageURL)
		}
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
				Dialogue:         p.Dialogue,
				ImagePrompt:      p.SceneDescription,
			})
		} else {
			panelItems = append(panelItems, gen.PanelTextItem{
				PanelNumber:      idx + 1,
				SceneDescription: fmt.Sprintf("Panel %d", idx+1),
				ImagePrompt:      fmt.Sprintf("Panel %d", idx+1),
			})
		}
	}

	idempKey := uuid.NewString()
	hashedUID := domain.HashUserID(userID)

	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "final_line_art",
		Stage:          "line_art",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: idempKey,
		EstimatedCost:  credits.ImageUnitPrice,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, err = s.CreditService.Debit(r.Context(), credits.DebitRequest{
		UserID:          userID,
		ProjectID:       proj.ID,
		GenerationJobID: job.ID,
		Amount:          credits.ImageUnitPrice,
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusPaymentRequired, fmt.Sprintf("Debit failed: %v", err))
		return
	}

	lineArtStyle := proj.ArtStyle
	if lineArtStyle == "" {
		lineArtStyle = "classic-comic"
	}

	lineArtResp, err := s.AIClient.GenerateLineArt(r.Context(), gen.GenerateLineArtRequest{
		JobID:                       job.ID,
		HashedUserID:                hashedUID,
		ProjectID:                   proj.ID,
		PageID:                      pg.ID,
		PageNumber:                  pg.PageNumber,
		DraftImageURL:               pg.DraftImageURL,
		Panels:                      panelItems,
		Style:                       lineArtStyle,
		CharacterReferenceImageURLs: optionalStringSlice(charImageURLs),
	})
	if err != nil {
		_, _ = s.CreditService.Refund(r.Context(), credits.RefundRequest{
			UserID:          userID,
			ProjectID:       proj.ID,
			GenerationJobID: job.ID,
			Amount:          credits.ImageUnitPrice,
		})
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Line art generation failed: %v", err))
		return
	}

	imgURL := s.ImageHosting.ResolveURL(lineArtResp.ImageURL)
	now := time.Now().UTC()
	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	pg.LineArtImageURL = imgURL
	pg.LineArtImageJobID = &job.ID
	domain.StaleOnLineArtRegeneration(pg, now)
	_ = s.Repo.UpdatePage(r.Context(), pg)

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
	pg.UpdatedAt = now
	_ = s.Repo.UpdatePage(r.Context(), pg)

	_ = s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "line_art_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: credits.ImageUnitPrice,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "page": pg})
}

// -------------------------------------------------------------
// Colouring
// -------------------------------------------------------------

// GetColourPalette handles GET /api/v1/pages/{id}/colour-palette
func (s *Server) GetColourPalette(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
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
	if err := s.CheckKillSwitchAndQuota(r, userID); err != nil {
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	// Line art approval gate (SPEC 5.3, ARCHITECTURE 5.3)
	lineArtApproved := pg.LineArtApprovedAt != nil && !domain.IsApprovalStale(pg.LineArtApprovedAt, pg.UpdatedAt)
	if !lineArtApproved {
		writeError(w, http.StatusBadRequest, "Final line art must be approved before colour palette suggestion")
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	idempKey := uuid.NewString()
	hashedUID := domain.HashUserID(userID)

	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "colour_suggestion",
		Stage:          "colour",
		ModelID:        "meta-vision-colour",
		IdempotencyKey: idempKey,
		EstimatedCost:  credits.ColourSuggestionUnitPrice,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, err = s.CreditService.Debit(r.Context(), credits.DebitRequest{
		UserID:          userID,
		ProjectID:       proj.ID,
		GenerationJobID: job.ID,
		Amount:          credits.ColourSuggestionUnitPrice,
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusPaymentRequired, fmt.Sprintf("Debit failed: %v", err))
		return
	}

	lineArtURL := pg.LineArtImageURL
	if lineArtURL == "" {
		lineArtURL = pg.DraftImageURL
	}
	if lineArtURL == "" {
		lineArtURL = fmt.Sprintf("lineart/page_%s.png", pg.ID)
	}

	palResp, err := s.AIClient.SuggestColourPalette(r.Context(), gen.SuggestColourPaletteRequest{
		JobID:           job.ID,
		HashedUserID:    hashedUID,
		ProjectID:       proj.ID,
		PageID:          pg.ID,
		LineArtImageURL: lineArtURL,
		PanelContext:    fmt.Sprintf("Page %d of chapter %d", pg.PageNumber, c.ChapterNumber),
	})
	if err != nil {
		_, _ = s.CreditService.Refund(r.Context(), credits.RefundRequest{
			UserID:          userID,
			ProjectID:       proj.ID,
			GenerationJobID: job.ID,
			Amount:          credits.ColourSuggestionUnitPrice,
		})
		errMsg := err.Error()
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Colour palette suggestion failed: %v", err))
		return
	}

	palette := make(map[string]interface{})
	for _, item := range palResp.Palette {
		palette[item.RegionName] = item.HexColor
	}

	_ = s.Repo.SaveColourPalette(r.Context(), &domain.ColourPalette{
		PageID:          pageID,
		SuggestionJobID: &job.ID,
		Palette:         palette,
	})

	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":  job.ID,
		"palette": palette,
	})
}

// UpdateColourPalette handles PATCH /api/v1/pages/{id}/colour-palette
func (s *Server) UpdateColourPalette(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	var body struct {
		Palette map[string]interface{} `json:"palette"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	_ = s.Repo.SaveColourPalette(r.Context(), &domain.ColourPalette{
		PageID:  pageID,
		Palette: body.Palette,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"palette": body.Palette})
}

// ApproveColour handles POST /api/v1/pages/{id}/colour/approve
func (s *Server) ApproveColour(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	_ = s.Repo.RecordApproval(r.Context(), &domain.StageApproval{
		EntityType:       "colour_page",
		EntityID:         pageID,
		ApprovedByUserID: userID,
		ApprovedAt:       time.Now().UTC(),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// -------------------------------------------------------------
// Estimates, Jobs, Credits
// -------------------------------------------------------------

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

	est := credits.EstimateCost(body.Stage, body.ImageCount)
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
			imgURL := pg.LineArtImageURL
			if imgURL == "" {
				imgURL = pg.DraftImageURL
			}
			if imgURL == "" {
				imgURL = fmt.Sprintf("pages/%s.png", pg.ID)
			}
			panelCount := len(pg.PanelIDs)
			provenance := map[string]interface{}{"c2pa": true}
			exportPages = append(exportPages, gen.PageExportItem{
				PageNumber:         pg.PageNumber,
				ImageURL:           imgURL,
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
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "assemble_pdf",
		Stage:          "export",
		ModelID:        "pdf-assembler",
		IdempotencyKey: idempKey,
		EstimatedCost:  0.0,
		HashedUserID:   hashedUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	pdfResp, err := s.AIClient.AssemblePdf(r.Context(), gen.AssemblePdfRequest{
		JobID:               job.ID,
		HashedUserID:        hashedUID,
		ProjectID:           proj.ID,
		Title:               proj.Title,
		PageSize:            gen.A4,
		IncludeAIDisclosure: true,
		Pages:               exportPages,
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
