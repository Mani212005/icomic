package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

// Structural panel edits: add, move or reorder, and delete. Each is chapter-scoped and one repository
// transaction (db.AddPanel, MovePanel, DeletePanel), which stales the approvals of every page it changes, so
// art made from the old allocation cannot be approved as current (SPEC 3.5.1, 3.6).

// ownedChapter loads a chapter the caller owns, answering the error itself when it cannot.
func (s *Server) ownedChapter(w http.ResponseWriter, r *http.Request) (*domain.Chapter, *domain.Project, bool) {
	c, err := s.Repo.GetChapter(r.Context(), r.PathValue("id"))
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return nil, nil, false
	}
	proj, err := s.Repo.GetProject(r.Context(), c.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return nil, nil, false
	}
	if proj.UserID != s.getUserID(r) {
		writeError(w, http.StatusForbidden, "forbidden")
		return nil, nil, false
	}
	return c, proj, true
}

// panelLayoutResult is the answer to a structural edit. The client reloads the chapter, so it names what changed
// instead of repeating pages whose colour approvals the list route derives.
type panelLayoutResult struct {
	Panel          *domain.Panel `json:"panel,omitempty"`
	ChangedPageIDs []string      `json:"changed_page_ids"`
	RemovedPageIDs []string      `json:"removed_page_ids"`
}

func writePanelLayoutError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidPanelAllocation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, db.ErrArtExists):
		writeError(w, http.StatusConflict, "This is the last panel on a page that already has generated art. Move it to another page instead, or keep it.")
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "Panel or page not found")
	default:
		writeError(w, http.StatusInternalServerError, "Failed to save panel layout")
	}
}

// finishPanelLayout refreshes the lettering of every changed overlay page, then answers. A page that was removed
// has none; a page that was created gets its first layout here.
func (s *Server) finishPanelLayout(w http.ResponseWriter, r *http.Request, edit *domain.PanelLayoutEdit, panel *domain.Panel, status int) {
	for _, id := range edit.Changed {
		pg, err := s.Repo.GetPage(r.Context(), id)
		if err != nil {
			writeLookupError(w, err, "Page")
			return
		}
		previous := append(json.RawMessage(nil), pg.Lettering...)
		if err := s.refreshLettering(r.Context(), pg); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to refresh lettering")
			return
		}
		if !s.saveLettering(w, r, pg, previous) {
			return
		}
	}
	result := panelLayoutResult{Panel: panel, ChangedPageIDs: edit.Changed, RemovedPageIDs: edit.Removed}
	if result.ChangedPageIDs == nil {
		result.ChangedPageIDs = []string{}
	}
	if result.RemovedPageIDs == nil {
		result.RemovedPageIDs = []string{}
	}
	writeJSON(w, status, result)
}

// AddPanel handles POST /api/v1/chapters/{id}/panels/add. page_id names the page to add to, at position (0 is
// first; omitted means last); without page_id the panel starts a new page at the end of the chapter. The panel
// needs a scene description, because a blank panel gives a draft nothing to draw.
func (s *Server) AddPanel(w http.ResponseWriter, r *http.Request) {
	c, proj, ok := s.ownedChapter(w, r)
	if !ok {
		return
	}
	var body struct {
		PageID           *string `json:"page_id"`
		Position         *int    `json:"position"`
		SceneDescription string  `json:"scene_description"`
		CharacterActions string  `json:"character_actions"`
		CameraAngle      string  `json:"camera_angle"`
		Background       string  `json:"background"`
		Dialogue         string  `json:"dialogue"`
		Narration        string  `json:"narration"`
		ImagePrompt      string  `json:"image_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid panel")
		return
	}
	if strings.TrimSpace(body.SceneDescription) == "" {
		writeError(w, http.StatusBadRequest, "A new panel needs a scene description")
		return
	}
	now := time.Now().UTC()
	pan := &domain.Panel{
		ID: uuid.NewString(), ChapterID: c.ID, Status: "ready", UpdatedAt: now,
		SceneDescription: body.SceneDescription, CharacterActions: body.CharacterActions, CameraAngle: body.CameraAngle,
		Background: body.Background, Dialogue: body.Dialogue, Narration: body.Narration, ImagePrompt: body.ImagePrompt,
	}
	target, position := "", -1
	var newPage *domain.Page
	if body.PageID != nil {
		target = *body.PageID
	} else {
		newPage = &domain.Page{ID: uuid.NewString(), ChapterID: c.ID, UpdatedAt: now, LetteringMode: s.newPageLetteringMode(proj)}
	}
	if body.Position != nil {
		position = *body.Position
	}
	edit, err := s.Repo.AddPanel(r.Context(), c.ID, pan, newPage, target, position, s.maxPanelsPerPage(), now)
	if err != nil {
		writePanelLayoutError(w, err)
		return
	}
	s.finishPanelLayout(w, r, edit, pan, http.StatusCreated)
}

// MovePanel handles POST /api/v1/chapters/{id}/panels/move: panel_id goes to page_id at position (0 is first;
// omitted means last). The same call reorders a panel within its page and moves it to another.
func (s *Server) MovePanel(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.ownedChapter(w, r)
	if !ok {
		return
	}
	var body struct {
		PanelID  string `json:"panel_id"`
		PageID   string `json:"page_id"`
		Position *int   `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PanelID == "" || body.PageID == "" {
		writeError(w, http.StatusBadRequest, "A move needs panel_id and page_id")
		return
	}
	position := -1
	if body.Position != nil {
		position = *body.Position
	}
	edit, err := s.Repo.MovePanel(r.Context(), c.ID, body.PanelID, body.PageID, position, s.maxPanelsPerPage(), time.Now().UTC())
	if err != nil {
		writePanelLayoutError(w, err)
		return
	}
	s.finishPanelLayout(w, r, edit, nil, http.StatusOK)
}

// DeletePanel handles DELETE /api/v1/chapters/{id}/panels/{panel_id}. Panels after it, and pages after a page that
// empties, close the gap. Deleting the last panel of a page with generated art is refused, never silent.
func (s *Server) DeletePanel(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.ownedChapter(w, r)
	if !ok {
		return
	}
	panelID := r.PathValue("panel_id")
	if id, err := uuid.Parse(panelID); err != nil || id == uuid.Nil || len(panelID) != 36 {
		writeError(w, http.StatusBadRequest, "Invalid resource ID: expected a UUID")
		return
	}
	edit, err := s.Repo.DeletePanel(r.Context(), c.ID, panelID, time.Now().UTC())
	if err != nil {
		writePanelLayoutError(w, err)
		return
	}
	s.finishPanelLayout(w, r, edit, nil, http.StatusOK)
}

// newPageLetteringMode is the mode a page created after the project was planned starts in, as generation gives it.
func (s *Server) newPageLetteringMode(proj *domain.Project) string {
	if s.LetteringOverlayEnabled && proj.LetteringMode == "overlay" {
		return "overlay"
	}
	return "baked"
}
