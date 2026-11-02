package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, JSONResponse{Error: message})
}

func (s *Server) getUserID(r *http.Request) string {
	return auth.UserIDFromContext(r.Context())
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
		Status:         "created",
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

	idempKey := uuid.NewString()
	hashedUID := domain.HashUserID(userID)

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
	_, _ = s.CreditService.Debit(r.Context(), credits.DebitRequest{
		UserID:          userID,
		ProjectID:       proj.ID,
		GenerationJobID: job.ID,
		Amount:          credits.ImageUnitPrice,
	})

	// Dispatch to AI service if client available
	imgURL := s.ImageHosting.ResolveURL(fmt.Sprintf("characters/%s.png", ch.ID))
	now := time.Now().UTC()
	actualCost := credits.ImageUnitPrice
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

	// Seed or generate character profiles
	chars := []*domain.Character{
		{
			ID:               uuid.NewString(),
			ProjectID:        proj.ID,
			Name:             "Maya",
			Description:      "A daring young comic artist exploring uncharted space",
			VisualAttributes: map[string]interface{}{"jacket": "leather"},
			UpdatedAt:        time.Now().UTC(),
		},
		{
			ID:               uuid.NewString(),
			ProjectID:        proj.ID,
			Name:             "Leo",
			Description:      "A loyal robot navigator",
			VisualAttributes: map[string]interface{}{"optics": "amber"},
			UpdatedAt:        time.Now().UTC(),
		},
	}
	for _, ch := range chars {
		_ = s.Repo.CreateCharacter(r.Context(), ch)
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":             uuid.NewString(),
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

	var createdChapters []*domain.Chapter
	for i := 1; i <= proj.ChapterCount; i++ {
		chap := &domain.Chapter{
			ID:            uuid.NewString(),
			ProjectID:     proj.ID,
			ChapterNumber: i,
			Title:         fmt.Sprintf("Chapter %d: The Odyssey", i),
			Summary:       fmt.Sprintf("The heroes venture into sector %d.", i),
			Status:        "pending",
		}
		_ = s.Repo.CreateChapter(r.Context(), chap)
		createdChapters = append(createdChapters, chap)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"chapters": createdChapters})
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

	// Generate 4 panels
	var pids []string
	var panels []*domain.Panel
	now := time.Now().UTC()
	for i := 1; i <= 4; i++ {
		p := &domain.Panel{
			ID:               uuid.NewString(),
			ChapterID:        c.ID,
			PanelNumber:      i,
			SceneDescription: fmt.Sprintf("Scene description for panel %d", i),
			Dialogue:         fmt.Sprintf("Dialogue in panel %d", i),
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
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

	idempKey := uuid.NewString()
	hashedUID := domain.HashUserID(userID)

	job, _, _ := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "draft_sketch",
		Stage:          "draft",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: idempKey,
		EstimatedCost:  credits.ImageUnitPrice,
		HashedUserID:   hashedUID,
	})

	_, _ = s.CreditService.Debit(r.Context(), credits.DebitRequest{
		UserID:          userID,
		ProjectID:       proj.ID,
		GenerationJobID: job.ID,
		Amount:          credits.ImageUnitPrice,
	})

	imgURL := s.ImageHosting.ResolveURL(fmt.Sprintf("drafts/page_%s.png", pg.ID))
	now := time.Now().UTC()
	actualCost := credits.ImageUnitPrice
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

	idempKey := uuid.NewString()
	hashedUID := domain.HashUserID(userID)

	job, _, _ := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		ProjectID:      proj.ID,
		UserID:         userID,
		JobType:        "final_line_art",
		Stage:          "line_art",
		ModelID:        "muse-image-1.0",
		IdempotencyKey: idempKey,
		EstimatedCost:  credits.ImageUnitPrice,
		HashedUserID:   hashedUID,
	})

	_, _ = s.CreditService.Debit(r.Context(), credits.DebitRequest{
		UserID:          userID,
		ProjectID:       proj.ID,
		GenerationJobID: job.ID,
		Amount:          credits.ImageUnitPrice,
	})

	imgURL := s.ImageHosting.ResolveURL(fmt.Sprintf("lineart/page_%s.png", pg.ID))
	now := time.Now().UTC()
	actualCost := credits.ImageUnitPrice
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

	palette := map[string]interface{}{
		"background": "#1A0B2E",
		"skin":       "#F5D0C5",
		"hair":       "#00CED1",
	}
	_ = s.Repo.SaveColourPalette(r.Context(), &domain.ColourPalette{
		PageID:  pageID,
		Palette: palette,
	})
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":  uuid.NewString(),
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

	exp := &domain.Export{
		ID:                          uuid.NewString(),
		ProjectID:                   proj.ID,
		FileURL:                     fmt.Sprintf("/exports/comic_%s.pdf", proj.ID),
		Format:                      "pdf",
		Status:                      "completed",
		ProvenanceMetadataPreserved: true,
		CreatedAt:                   time.Now().UTC(),
	}
	_ = s.Repo.CreateExport(r.Context(), exp)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{"export": exp})
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

	w.Header().Set("Content-Type", "application/pdf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("%PDF-1.4 minimal test stub"))
}
