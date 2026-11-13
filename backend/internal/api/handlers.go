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
	"strconv"
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
	Status            string                 `json:"status,omitempty"`
	Data              interface{}            `json:"data,omitempty"`
	Error             string                 `json:"error,omitempty"`
	Message           string                 `json:"message,omitempty"`
	Code              string                 `json:"code,omitempty"`
	Details           map[string]interface{} `json:"details,omitempty"`
	Retryable         bool                   `json:"retryable,omitempty"`
	RetryAfterSeconds *float64               `json:"retry_after_seconds,omitempty"`
}

// Change this one value to select the default finish used by new pages.
const defaultPaintedPageVariant = "direct-from-brief"

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

func writeLookupError(w http.ResponseWriter, err error, resource string) {
	if errors.Is(err, db.ErrNotFound) || errors.Is(err, jobs.ErrJobNotFound) {
		writeError(w, http.StatusNotFound, resource+" not found")
		return
	}
	writeJSON(w, http.StatusInternalServerError, JSONResponse{
		Code: "INTERNAL_ERROR", Message: "Unable to load the requested resource.",
	})
}

func lookupStatus(err error) int {
	if errors.Is(err, db.ErrNotFound) || errors.Is(err, jobs.ErrJobNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
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
		return nil, lookupStatus(err)
	}
	if proj.UserID != s.getUserID(r) {
		return nil, http.StatusForbidden
	}
	return proj, http.StatusOK
}

func (s *Server) getOwnedPage(r *http.Request, pageID string) (*domain.Page, int) {
	page, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		return nil, lookupStatus(err)
	}
	chapter, err := s.Repo.GetChapter(r.Context(), page.ChapterID)
	if err != nil {
		return nil, lookupStatus(err)
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
		writeGenerationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"blocked": blocked,
		"reason":  reason,
	})
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
		FinishPreset:   "print-heavy",
	}

	proj.LetteringMode = "baked"
	if s.LetteringOverlayEnabled {
		proj.LetteringMode = "overlay"
	}
	access := auth.IdentityFromContext(r.Context()).ProjectAccess()
	if err := s.Repo.CreateProjectForAccount(r.Context(), proj, access); err != nil {
		var limit *domain.ProjectLimitError
		if errors.As(err, &limit) {
			writeProjectLimit(w, limit)
			return
		}
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
		writeLookupError(w, err, "Project list")
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
		writeLookupError(w, err, "Project")
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
		writeLookupError(w, err, "Project")
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
		FinishPreset   *string `json:"finish_preset"`
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
	if body.FinishPreset != nil {
		if !domain.ValidFinishPreset(*body.FinishPreset) {
			writeError(w, http.StatusBadRequest, "finish_preset must be one of off, print, print-plus, print-heavy")
			return
		}
		proj.FinishPreset = *body.FinishPreset
		proj.FinishPresetExplicit = true
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
		writeLookupError(w, err, "Project")
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
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	chars, err := s.Repo.ListCharacters(r.Context(), projectID)
	if err != nil {
		writeLookupError(w, err, "Character list")
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
		writeLookupError(w, err, "Character")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), ch.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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
		writeLookupError(w, err, "Character")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), ch.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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
		writeGenerationError(w, err)
		return
	}
	if blocked {
		writeError(w, http.StatusBadRequest, reason)
		return
	}

	idempKey := generationKey(r)
	aiReq := gen.GenerateCharacterReferenceRequest{
		HashedUserID:    hashedUID,
		ProjectID:       proj.ID,
		CharacterID:     ch.ID,
		CharacterName:   ch.Name,
		Prompt:          prompt,
		ReferenceImages: nil,
		Style:           style,
	}
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
		Envelope:         newEnvelope(opCharacterReference, aiReq),
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	if existed {
		s.writeReceipt(w, job, gen.Character, "")
		return
	}

	if s.AIClient == nil {
		_, _ = s.CreditService.Refund(r.Context(), credits.RefundRequest{
			UserID:          userID,
			ProjectID:       proj.ID,
			GenerationJobID: job.ID,
			Amount:          s.Pricing.ImagePriceUSD,
		})
		s.finishDispatch(r.Context(), job.ID)
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	s.writeReceipt(w, job, gen.Character, "")
}

// ApproveCharacter handles POST /api/v1/characters/{id}/approve
func (s *Server) ApproveCharacter(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	charID := r.PathValue("id")

	ch, err := s.Repo.GetCharacter(r.Context(), charID)
	if err != nil {
		writeLookupError(w, err, "Character")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), ch.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Extras are approved as profiles only; every other character needs the image it is approving.
	if ch.ReferenceImageURL == "" && ch.VisualAttributes["reference_role"] != "extra" {
		writeError(w, http.StatusBadRequest, "Generate a reference image before approving the character")
		return
	}

	now := nextCharacterActivity(ch)
	if !s.commitApproval(w, r, &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "character_reference",
		EntityID:                 ch.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}, ch.UpdatedAt) {
		return
	}

	ch.ApprovedAt = &now
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
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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
		writeGenerationError(w, err)
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
		errMsg := safeGenerationMessage(err)
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeGenerationError(w, err)
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
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	// Check character reference approval gate
	chars, err := s.Repo.ListCharacters(r.Context(), proj.ID)
	if err != nil {
		writeLookupError(w, err, "Character list")
		return
	}
	if len(chars) == 0 {
		writeError(w, http.StatusBadRequest, "Analyze the script and approve its character references before generating the outline")
		return
	}
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

	existing, err := s.Repo.ListChapters(r.Context(), proj.ID)
	if err != nil {
		writeLookupError(w, err, "Chapter list")
		return
	}
	for _, ch := range existing {
		hasArt, err := s.chapterHasArt(r.Context(), ch.ID)
		if err != nil {
			writeLookupError(w, err, "Page list")
			return
		}
		if hasArt {
			writeError(w, http.StatusConflict, "Chapters with generated page art cannot be regenerated")
			return
		}
	}

	hashedUID := domain.HashUserID(userID)

	// Pre-moderation
	blocked, reason, err := s.checkPromptModeration(r, proj.OriginalScript, hashedUID)
	if err != nil {
		writeGenerationError(w, err)
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
		errMsg := safeGenerationMessage(err)
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeGenerationError(w, err)
		return
	}

	numbers := make(map[int]bool, len(outlineResp.Chapters))
	createdChapters := make([]*domain.Chapter, 0, len(outlineResp.Chapters))
	for _, cItem := range outlineResp.Chapters {
		if cItem.ChapterNumber < 1 || numbers[cItem.ChapterNumber] {
			errMsg := "Outline chapters must have unique positive numbers"
			_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
			writeError(w, http.StatusBadGateway, errMsg)
			return
		}
		numbers[cItem.ChapterNumber] = true
		createdChapters = append(createdChapters, &domain.Chapter{
			ID:            uuid.NewString(),
			ProjectID:     proj.ID,
			ChapterNumber: cItem.ChapterNumber,
			Title:         cItem.Title,
			Summary:       cItem.Summary,
			Status:        "pending",
		})
	}
	if err := s.Repo.ReplaceChapterOutline(r.Context(), proj.ID, createdChapters); err != nil {
		s.failReplacement(w, r, job.ID, err, "Failed to save chapters")
		return
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
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	chapters, err := s.Repo.ListChapters(r.Context(), projectID)
	if err != nil {
		writeLookupError(w, err, "Chapter list")
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
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	now := timestampAfterActivity(c.UpdatedAt)
	if !s.commitApproval(w, r, &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "chapter_outline",
		EntityID:                 c.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: 0.01,
	}, c.UpdatedAt) {
		return
	}

	c.Status = "approved"
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
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if !s.validateSavedStory(w, proj.OriginalScript, proj.ChapterCount) {
		return
	}

	// Panels follow an approved, unedited outline (SPEC 3.4).
	outlineApp, err := s.Repo.GetLatestApproval(r.Context(), "chapter_outline", c.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		writeLookupError(w, err, "Chapter outline approval")
		return
	}
	var outlineApprovedAt *time.Time
	if outlineApp != nil {
		outlineApprovedAt = &outlineApp.ApprovedAt
	}
	if err := domain.CheckChapterOutlineApproval(outlineApprovedAt, c.UpdatedAt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hasArt, err := s.chapterHasArt(r.Context(), c.ID)
	if err != nil {
		writeLookupError(w, err, "Page list")
		return
	}
	if hasArt {
		writeError(w, http.StatusConflict, "Panels with generated page art cannot be regenerated")
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
		writeGenerationError(w, err)
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

	chars, err := s.Repo.ListCharacters(r.Context(), proj.ID)
	if err != nil {
		writeLookupError(w, err, "Character list")
		return
	}
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
		errMsg := safeGenerationMessage(err)
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeGenerationError(w, err)
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
		pids = append(pids, p.ID)
		panels = append(panels, p)
	}

	// Automatically partition panels into pages (SPEC 3.5.1)
	pageSize := s.maxPanelsPerPage()

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
		pg.LetteringMode = "baked"
		if s.LetteringOverlayEnabled && proj.LetteringMode == "overlay" {
			pg.LetteringMode = "overlay"
		}
		pages = append(pages, pg)
		pageNum++
	}
	if err := s.Repo.ReplaceChapterPanels(r.Context(), c.ID, panels, pages); err != nil {
		s.failReplacement(w, r, job.ID, err, "Failed to save panels")
		return
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	// The layout reads the stored panels, so it follows the commit. A failure here leaves a complete
	// revision whose layout is recomputed by the next draft or lettering request (ensureLettering).
	for _, pg := range pages {
		if pg.LetteringMode != "overlay" {
			continue
		}
		if err := s.refreshLettering(r.Context(), pg); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to prepare lettering")
			return
		}
		if !s.saveLettering(w, r, pg, nil) {
			return
		}
	}

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
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	panels, err := s.Repo.ListPanels(r.Context(), chapID)
	if err != nil {
		writeLookupError(w, err, "Panel list")
		return
	}
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
		writeLookupError(w, err, "Panel")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), p.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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

	// The edit and the staling of every page holding the panel commit together (SPEC 3.5, 3.6).
	now := time.Now().UTC()
	if err := s.Repo.SavePanelEdit(r.Context(), p, now); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeLookupError(w, err, "Panel")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to save panel")
		}
		return
	}

	pages, err := s.Repo.ListPages(r.Context(), p.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Page list")
		return
	}
	for _, pg := range pages {
		for _, pid := range pg.PanelIDs {
			if pid == p.ID {
				previous := append(json.RawMessage(nil), pg.Lettering...)
				if err := s.refreshLettering(r.Context(), pg); err != nil {
					writeError(w, 502, "Failed to refresh lettering")
					return
				}
				if !s.saveLettering(w, r, pg, previous) {
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
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	pages, err := s.Repo.ListPages(r.Context(), chapID)
	if err != nil {
		writeLookupError(w, err, "Page list")
		return
	}
	for _, page := range pages {
		approval, approvalErr := s.Repo.GetLatestApproval(r.Context(), "colour_page", page.ID)
		if approvalErr != nil && !errors.Is(approvalErr, db.ErrNotFound) {
			writeLookupError(w, approvalErr, "Colour approval")
			return
		}
		if approvalErr == nil && page.ColourImageUploadedAt != nil && approval.ApprovedAt.After(*page.ColourImageUploadedAt) && !domain.IsApprovalStale(&approval.ApprovedAt, page.UpdatedAt) {
			approvedAt := approval.ApprovedAt
			page.ColourApprovedAt = &approvedAt
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"pages": pages})
}

// FinishPageImage creates or retrieves the immutable display derivative for the
// page's currently approved art and owning project's preset.
func (s *Server) FinishPageImage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")
	page, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeLookupError(w, err, "Page")
		return
	}
	chapter, err := s.Repo.GetChapter(r.Context(), page.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}
	project, err := s.Repo.GetProject(r.Context(), chapter.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if project.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	inkVariant := r.URL.Query().Get("variant") == "ink"
	if variant := r.URL.Query().Get("variant"); variant != "" && variant != "ink" && variant != "painted" {
		writeError(w, http.StatusBadRequest, "variant must be painted or ink")
		return
	}
	eligible := (page.LetteringMode == "overlay" && page.LineArtImageURL == "" && !inkVariant) || project.FinishPresetExplicit
	if !eligible || project.FinishPreset == "off" {
		writeJSON(w, http.StatusOK, map[string]string{"finished_image_url": ""})
		return
	}
	var sourceURL string
	if inkVariant {
		if page.LineArtImageURL != "" && page.LineArtApprovedAt != nil && !domain.IsApprovalStale(page.LineArtApprovedAt, page.UpdatedAt) {
			sourceURL = page.LineArtImageURL
		}
	} else if page.PaintedPageImageURL != "" && page.PaintedPageApprovedAt != nil && !domain.IsApprovalStale(page.PaintedPageApprovedAt, page.UpdatedAt) {
		sourceURL = page.PaintedPageImageURL
	} else if page.LineArtImageURL != "" && page.LineArtApprovedAt != nil && !domain.IsApprovalStale(page.LineArtApprovedAt, page.UpdatedAt) {
		sourceURL = page.LineArtImageURL
	} else {
		writeError(w, http.StatusConflict, "page has no approved final art")
		return
	}
	if sourceURL == "" {
		writeError(w, http.StatusConflict, "page has no approved final art for this variant")
		return
	}
	preset := gen.FinishPreset(project.FinishPreset)
	finished, err := s.AIClient.FinishPageImage(r.Context(), gen.FinishPageImageRequest{
		PageID: page.ID, ImageURL: sourceURL, FinishPreset: preset,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"raw_master_image_url": s.ImageHosting.ResolveURL(sourceURL),
		"finished_image_url":   s.ImageHosting.ResolveURL(finished.ImageURL),
	})
}

// UpdatePage handles PATCH /api/v1/pages/{id}
func (s *Server) UpdatePage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeLookupError(w, err, "Page")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	var body struct {
		PanelIDs []string `json:"panel_ids"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.PanelIDs == nil {
		writeError(w, http.StatusBadRequest, "Page update must be a JSON object with panel_ids")
		return
	}

	// Listing a panel that sits on another page moves it; the move, the reorder and the staling of every
	// affected page commit together, so panels stay assigned exactly once (SPEC 3.5.1, 3.6).
	changed, err := s.Repo.ReassignPanels(r.Context(), pg.ID, body.PanelIDs, s.maxPanelsPerPage(), time.Now().UTC())
	if err != nil {
		if errors.Is(err, domain.ErrInvalidPanelAllocation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, db.ErrNotFound) {
			writeLookupError(w, err, "Page")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to save page")
		}
		return
	}
	for _, id := range changed {
		moved, err := s.Repo.GetPage(r.Context(), id)
		if err != nil {
			writeLookupError(w, err, "Page")
			return
		}
		previous := append(json.RawMessage(nil), moved.Lettering...)
		if err := s.refreshLettering(r.Context(), moved); err != nil {
			writeError(w, 500, "Failed to refresh lettering")
			return
		}
		if !s.saveLettering(w, r, moved, previous) {
			return
		}
	}

	pg, err = s.Repo.GetPage(r.Context(), pg.ID)
	if err != nil {
		writeLookupError(w, err, "Page")
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
		writeLookupError(w, err, "Page")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if !s.ensureLettering(w, r, pg) {
		return
	}

	// Kill switch and quota check (SPEC 5.5)
	if err := s.CheckKillSwitchAndQuota(r, userID); err != nil {
		writeGenerationError(w, err)
		return
	}

	// Prerequisite approval check: character references & chapter outline (ARCHITECTURE 5.3)
	chars, err := s.Repo.ListCharacters(r.Context(), proj.ID)
	if err != nil {
		writeLookupError(w, err, "Character list")
		return
	}
	allCharsApproved := len(chars) > 0
	for _, ch := range chars {
		if ch.ApprovedAt == nil || domain.IsApprovalStale(ch.ApprovedAt, ch.UpdatedAt) {
			allCharsApproved = false
			break
		}
	}
	outlineApp, err := s.Repo.GetLatestApproval(r.Context(), "chapter_outline", c.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		writeLookupError(w, err, "Chapter outline approval")
		return
	}
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

	allPanels, err := s.Repo.ListPanels(r.Context(), c.ID)
	if err != nil {
		writeLookupError(w, err, "Panel list")
		return
	}
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
		writeGenerationError(w, err)
		return
	}
	if blocked {
		writeError(w, http.StatusBadRequest, reason)
		return
	}

	idempKey := generationKey(r)
	draftStyle := proj.ArtStyle
	if draftStyle == "" {
		draftStyle = "classic-comic"
	}
	aiReq := gen.GenerateDraftSketchRequest{
		HashedUserID:        hashedUID,
		ProjectID:           proj.ID,
		PageID:              pg.ID,
		PageNumber:          pg.PageNumber,
		LetteringDocument:   letteringDocument(pg),
		Panels:              panelItems,
		Style:               draftStyle,
		CharacterIdentities: characterRenderIdentities(chars),
	}
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
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
		Envelope:         newEnvelope(opDraftSketch, aiReq),
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	s.writeReceipt(w, job, gen.Page, "")
}

// ApproveDraft handles POST /api/v1/pages/{id}/draft/approve
func (s *Server) ApproveDraft(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeLookupError(w, err, "Page")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if pg.DraftImageURL == "" {
		writeError(w, http.StatusBadRequest, "Generate a draft sketch before approving it")
		return
	}
	if !s.requireCurrentArt(w, r, pg, pg.DraftImageJobID, "draft sketch") {
		return
	}

	now := timestampAfterActivity(pg.UpdatedAt)
	if !s.commitApproval(w, r, &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "draft_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}, pg.UpdatedAt) {
		return
	}

	pg.DraftApprovedAt = &now
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
	variant := r.URL.Query().Get("variant")
	if variant == "" {
		variant = reqBody.Variant
	}
	if variant != "ink" {
		s.GeneratePaintedPage(w, r)
		return
	}

	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeLookupError(w, err, "Page")
		return
	}

	if pg.LetteringMode == "overlay" {
		writeError(w, http.StatusConflict, "Overlay pages require painted finish; ink remains baked")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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

	chars, err := s.Repo.ListCharacters(r.Context(), proj.ID)
	if err != nil {
		writeLookupError(w, err, "Character list")
		return
	}

	allPanels, err := s.Repo.ListPanels(r.Context(), c.ID)
	if err != nil {
		writeLookupError(w, err, "Panel list")
		return
	}
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

	lineArtStyle := proj.ArtStyle
	if lineArtStyle == "" {
		lineArtStyle = "classic-comic"
	}
	aiReq := gen.GenerateLineArtRequest{
		HashedUserID:        hashedUID,
		ProjectID:           proj.ID,
		PageID:              pg.ID,
		PageNumber:          pg.PageNumber,
		DraftImageURL:       pg.DraftImageURL,
		Panels:              panelItems,
		Style:               lineArtStyle,
		CharacterIdentities: characterRenderIdentities(chars),
	}
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
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
		Envelope:         newEnvelope(opLineArt, aiReq),
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	s.writeReceipt(w, job, gen.Page, "")
}

// ApproveLineArt handles POST /api/v1/pages/{id}/line-art/approve
func (s *Server) ApproveLineArt(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("variant") != "ink" {
		if page, err := s.Repo.GetPage(r.Context(), r.PathValue("id")); err == nil && page.PaintedPageImageURL != "" {
			s.ApprovePaintedPage(w, r)
			return
		}
	}
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeLookupError(w, err, "Page")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if pg.LineArtImageURL == "" {
		writeError(w, http.StatusBadRequest, "Generate line art before approving it")
		return
	}
	if !s.requireCurrentArt(w, r, pg, pg.LineArtImageJobID, "line art") {
		return
	}

	now := timestampAfterActivity(pg.UpdatedAt)
	if !s.commitApproval(w, r, &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "line_art_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}, pg.UpdatedAt) {
		return
	}

	pg.LineArtApprovedAt = &now
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "approved", "page": pg})
}

// GeneratePaintedPage handles the default finished-page route and its explicit API alias.
func (s *Server) GeneratePaintedPage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeLookupError(w, err, "Page")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if !s.ensureLettering(w, r, pg) {
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
	if chosenVariant == "" {
		chosenVariant = r.URL.Query().Get("variant")
	}
	if chosenVariant == "" || chosenVariant == "painted" {
		chosenVariant = defaultPaintedPageVariant
	}
	if chosenVariant == "ink" {
		writeError(w, http.StatusBadRequest, "Use the line-art route for ink generation")
		return
	}
	if chosenVariant != "draft-conditioned" && chosenVariant != "direct-from-brief" {
		writeError(w, http.StatusBadRequest, "Unknown painted page variant")
		return
	}

	// Gate checks:
	// 1. Chapter outline and character references must be approved
	allChars, err := s.Repo.ListCharacters(r.Context(), proj.ID)
	if err != nil {
		writeLookupError(w, err, "Character list")
		return
	}
	allRefsApproved := len(allChars) > 0
	mainReferenceCount := 0
	for _, ch := range allChars {
		if ch.VisualAttributes["reference_role"] == "extra" {
			continue
		}
		mainReferenceCount++
		approval, err := s.Repo.GetLatestApproval(r.Context(), "character_reference", ch.ID)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			writeLookupError(w, err, "Character approval")
			return
		}
		if err != nil || ch.ReferenceImageURL == "" || domain.IsApprovalStale(&approval.ApprovedAt, ch.UpdatedAt) {
			allRefsApproved = false
			break
		}
	}
	if mainReferenceCount == 0 {
		allRefsApproved = false
	}
	outlineApproval, err := s.Repo.GetLatestApproval(r.Context(), "chapter_outline", c.ID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		writeLookupError(w, err, "Chapter outline approval")
		return
	}
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

	allPanels, err := s.Repo.ListPanels(r.Context(), c.ID)
	if err != nil {
		writeLookupError(w, err, "Panel list")
		return
	}
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

	artStyle := proj.ArtStyle
	if artStyle == "" {
		artStyle = "classic-comic"
	}
	var draftURL *string
	if chosenVariant == "draft-conditioned" && pg.DraftImageURL != "" {
		draftURL = &pg.DraftImageURL
	}
	aiReq := gen.GeneratePaintedPageRequest{
		HashedUserID:        hashedUID,
		ProjectID:           proj.ID,
		PageID:              pg.ID,
		PageNumber:          pg.PageNumber,
		LetteringDocument:   letteringDocument(pg),
		DraftImageURL:       draftURL,
		Panels:              panelItems,
		Style:               artStyle,
		Variant:             gen.GeneratePaintedPageRequestVariant(chosenVariant),
		CharacterIdentities: characterRenderIdentities(allChars),
	}
	job, existed, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		EntityID:             pg.ID,
		AllowDirectFromBrief: chosenVariant == "direct-from-brief",
		DebitAmount:          s.Pricing.ImagePriceUSD,
		KillSwitchActive:     s.IsKillSwitchActive(),
		ProjectID:            proj.ID,
		UserID:               userID,
		JobType:              "final_line_art",
		Stage:                "line_art",
		ModelID:              s.Pricing.ImageModelID,
		IdempotencyKey:       idempKey,
		EstimatedCost:        s.Pricing.ImagePriceUSD,
		HashedUserID:         hashedUID,
		Envelope:             newEnvelope(opPaintedPage, aiReq),
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	if existed {
		s.writeReceipt(w, job, gen.Page, pg.PaintedPageVariant)
		return
	}
	s.writeReceipt(w, job, gen.Page, chosenVariant)
}

// ApprovePaintedPage handles POST /api/v1/pages/{id}/painted-page/approve
func (s *Server) ApprovePaintedPage(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	pageID := r.PathValue("id")

	pg, err := s.Repo.GetPage(r.Context(), pageID)
	if err != nil {
		writeLookupError(w, err, "Page")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if pg.PaintedPageImageURL == "" {
		writeError(w, http.StatusBadRequest, "No painted page generated to approve")
		return
	}

	if !s.requireCurrentArt(w, r, pg, pg.PaintedPageImageJobID, "painted page") {
		return
	}

	now := timestampAfterActivity(pg.UpdatedAt)
	if !s.commitApproval(w, r, &domain.StageApproval{
		ProjectID:                proj.ID,
		EntityType:               "painted_page",
		EntityID:                 pg.ID,
		ApprovedByUserID:         userID,
		ApprovedAt:               now,
		CreditEstimateAtApproval: s.Pricing.ImagePriceUSD,
	}, pg.UpdatedAt) {
		return
	}

	pg.PaintedPageApprovedAt = &now
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
		if status >= http.StatusInternalServerError {
			message = "Unable to load the requested resource."
		}
		writeError(w, status, message)
		return
	}
	cp, err := s.Repo.GetColourPalette(r.Context(), pageID)
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"palette": map[string]string{}})
		return
	}
	if err != nil || cp == nil {
		if err == nil {
			err = errors.New("palette repository returned no data")
		}
		writeLookupError(w, err, "Colour palette")
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
		writeLookupError(w, err, "Page")
		return
	}

	c, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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

	aiReq := gen.SuggestColourPaletteRequest{
		HashedUserID:    hashedUID,
		ProjectID:       proj.ID,
		PageID:          pg.ID,
		LineArtImageURL: lineArtURL,
		PanelContext:    fmt.Sprintf("Page %d of chapter %d", pg.PageNumber, c.ChapterNumber),
		Style:           &proj.ArtStyle,
	}
	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
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
		Envelope:         newEnvelope(opColourPalette, aiReq),
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}
	s.writeReceipt(w, job, gen.Page, "")
}

// UpdateColourPalette handles PATCH /api/v1/pages/{id}/colour-palette
func (s *Server) UpdateColourPalette(w http.ResponseWriter, r *http.Request) {
	pageID := r.PathValue("id")
	if _, status := s.getOwnedPage(r, pageID); status != http.StatusOK {
		message := "forbidden"
		if status == http.StatusNotFound {
			message = "Page not found"
		}
		if status >= http.StatusInternalServerError {
			message = "Unable to load the requested resource."
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
		writeLookupError(w, err, "Page")
		return
	}
	chapter, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}
	project, err := s.Repo.GetProject(r.Context(), chapter.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if project.UserID != userID {
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
		writeLookupError(w, err, "Page")
		return
	}
	chapter, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return
	}

	project, err := s.Repo.GetProject(r.Context(), chapter.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if project.UserID != userID {
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
		if status == http.StatusNotFound || status == http.StatusForbidden {
			message = "Project not found"
		}
		if status >= http.StatusInternalServerError {
			message = "Unable to load the requested resource."
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
		writeLookupError(w, err, "Job")
		return
	}

	if job.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var workerStatus *gen.JobStatusResponse
	if (job.Status == jobs.StatusPending || job.Status == jobs.StatusRunning || job.Status == jobs.StatusFailed || job.Status == jobs.StatusUncertain) && s.AIClient != nil {
		// Reconcile a failed worker delivery even if the submitting HTTP connection
		// was interrupted. Successful jobs complete after saving the entity link.
		if status, statusErr := s.AIClient.GetJobStatus(r.Context(), job.ID); statusErr == nil && status != nil && (status.Status == jobs.StatusFailed || status.Status == jobs.StatusUncertain) {
			workerStatus = status
			if job.Status == jobs.StatusPending || job.Status == jobs.StatusRunning {
				// A cancellation or completion that won the race stands: terminal states are never reversed.
				if err := s.JobService.UpdateJobStatus(r.Context(), job.ID, string(status.Status), status.ActualCost, status.ErrorMessage); err != nil && !errors.Is(err, jobs.ErrJobAlreadyTerminal) {
					writeError(w, http.StatusInternalServerError, "Unable to update generation status.")
					return
				}
				job, err = s.JobService.GetJob(r.Context(), job.ID)
				if err != nil {
					writeLookupError(w, err, "Job")
					return
				}
			}
		}
	}
	if job.Status == jobs.StatusFailed {
		if err := s.refundFailedGeneration(r.Context(), job); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to refund generation: %v", err))
			return
		}
	}

	jobView := struct {
		*domain.GenerationJob
		ErrorCode         *string  `json:"error_code,omitempty"`
		Retryable         *bool    `json:"retryable,omitempty"`
		RetryAfterSeconds *float64 `json:"retry_after_seconds,omitempty"`
	}{GenerationJob: job}
	if workerStatus != nil {
		jobView.ErrorCode = workerStatus.ErrorCode
		jobView.Retryable = workerStatus.Retryable
		jobView.RetryAfterSeconds = workerStatus.RetryAfterSeconds
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": jobView})
}

// Cancel answers tell the user exactly what happened. Cancel is pending-only: it never claims to
// have stopped a provider call that was already made.
const (
	cancelledMessage = "Cancelled before it reached the model. Nothing was charged."
	settlingMessage  = "Already with the model. It will finish and you will be charged; the result is kept."
	finishedMessage  = "This job had already finished."
)

// CancelJob handles POST /api/v1/generation-jobs/{id}/cancel
func (s *Server) CancelJob(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	id := r.PathValue("id")

	job, err := s.JobService.GetJob(r.Context(), id)
	if err != nil {
		writeLookupError(w, err, "Job")
		return
	}

	if job.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	result, err := s.JobService.CancelJob(r.Context(), id)
	if err != nil {
		writeLookupError(w, err, "Job")
		return
	}
	message := finishedMessage
	switch result.Outcome {
	case db.CancelOutcomeCancelled:
		message = cancelledMessage
	case db.CancelOutcomeSettling:
		message = settlingMessage
	}
	job = result.Job
	writeJSON(w, http.StatusOK, gen.CancelJobResponse{
		Outcome: gen.CancelJobResponseOutcome(result.Outcome),
		Job: gen.GenerationJobRecord{
			ID: job.ID, EntityID: &job.EntityID, ProjectID: job.ProjectID, UserID: job.UserID,
			JobType: job.JobType, Stage: job.Stage, ModelID: job.ModelID, Status: gen.GenerationJobRecordStatus(job.Status),
			IdempotencyKey: job.IdempotencyKey, EstimatedCost: job.EstimatedCost, ActualCost: job.ActualCost,
			HashedUserID: job.HashedUserID, ErrorMessage: job.ErrorMessage, CreatedAt: job.CreatedAt, CompletedAt: job.CompletedAt,
		},
		Refunded: result.Refunded,
		Message:  message,
	})
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

// GetExport handles GET /api/v1/exports/{id}
func (s *Server) GetExport(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	id := r.PathValue("id")

	exp, err := s.Repo.GetExport(r.Context(), id)
	if err != nil {
		writeLookupError(w, err, "Export")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), exp.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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

// setExportDownloadHeaders names the file and its media type after the export's format.
func setExportDownloadHeaders(w http.ResponseWriter, exp *domain.Export) {
	contentType, extension := "application/pdf", "pdf"
	if exp.Format == exportFormatPNGZip {
		contentType, extension = "application/zip", "zip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"comic_%s.%s\"", exp.ProjectID, extension))
}

// DownloadExport handles GET /api/v1/exports/{id}/download
func (s *Server) DownloadExport(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	id := r.PathValue("id")

	exp, err := s.Repo.GetExport(r.Context(), id)
	if err != nil {
		writeLookupError(w, err, "Export")
		return
	}

	proj, err := s.Repo.GetProject(r.Context(), exp.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
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
			setExportDownloadHeaders(w, exp)
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

	// 3. Proxy request upstream through the bounded, redirect-free media client
	resp, err := fetchStored(r.Context(), mediaClient, targetURL)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Export file not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "Failed to retrieve export file from storage")
		return
	}
	defer resp.Body.Close()

	setExportDownloadHeaders(w, exp)
	w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxMediaBytes))
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

// stopSwitchMessage is what a refused paid generation tells the user while the global stop switch is on.
const stopSwitchMessage = "Paid generation is stopped by the global kill switch, so no new generation can start. Finished pages, downloads and exports still work."

// Stable error codes let clients distinguish lifetime book and rolling daily caps.
func writeGenerationError(w http.ResponseWriter, err error) {
	var serviceErr *gen.AIServiceError
	if errors.As(err, &serviceErr) {
		if serviceErr.RetryAfterSeconds != nil {
			seconds := int(*serviceErr.RetryAfterSeconds)
			if float64(seconds) < *serviceErr.RetryAfterSeconds {
				seconds++
			}
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		writeJSON(w, serviceErr.StatusCode, JSONResponse{
			Code: serviceErr.Code, Message: serviceErr.Message,
			Retryable: serviceErr.Retryable, RetryAfterSeconds: serviceErr.RetryAfterSeconds,
		})
		return
	}
	code := ""
	switch {
	case errors.Is(err, domain.ErrBookBudgetExceeded):
		code = "book_budget_reached"
	case errors.Is(err, domain.ErrDailyImageLimitReached):
		code = "daily_image_limit_reached"
	case errors.Is(err, domain.ErrKillSwitchActive):
		// The global stop switch halts paid generation only; finished pages and exports still work.
		code = "generation_stopped"
		writeJSON(w, http.StatusTooManyRequests, JSONResponse{Error: err.Error(), Code: code, Message: stopSwitchMessage})
		return
	}
	if code == "" {
		status := generationErrorStatus(err)
		message := err.Error()
		if status >= http.StatusInternalServerError {
			code = "INTERNAL_ERROR"
			message = "Generation could not be completed. Try again later."
		} else {
			code = "GENERATION_REJECTED"
		}
		writeJSON(w, status, JSONResponse{Code: code, Message: message})
		return
	}
	writeJSON(w, generationErrorStatus(err), JSONResponse{Error: err.Error(), Code: code, Message: err.Error()})
}
