package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icomic/backend/internal/domain"
)

func TestKillSwitchEnforcementAcrossAllStages(t *testing.T) {
	s, handler, repo := setupTestServer(true, "user-1")

	// Create project, chapter, page, character
	proj := &domain.Project{
		UserID:         "user-1",
		Title:          "Killswitch Test",
		OriginalScript: "A lone survivor walks the ruins.",
		ChapterCount:   1,
		Status:         "created",
	}
	_ = repo.CreateProject(nil, proj)

	ch := &domain.Character{
		ProjectID: proj.ID,
		Name:      "Survivor",
	}
	_ = repo.CreateCharacter(nil, ch)

	chap := &domain.Chapter{
		ProjectID:     proj.ID,
		ChapterNumber: 1,
		Title:         "The Ruins",
	}
	_ = repo.CreateChapter(nil, chap)

	pg := &domain.Page{
		ChapterID:  chap.ID,
		PageNumber: 1,
	}
	_ = repo.CreatePage(nil, pg)

	// Activate kill switch via runtime provider
	s.KillSwitch.SetActive(true)

	// 1. Character reference
	reqChar := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+ch.ID+"/reference-image", nil)
	recChar := httptest.NewRecorder()
	handler.ServeHTTP(recChar, reqChar)
	if recChar.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for character reference with kill switch active, got: %d", recChar.Code)
	}

	// 2. Draft sketch
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/draft", nil)
	recDraft := httptest.NewRecorder()
	handler.ServeHTTP(recDraft, reqDraft)
	if recDraft.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for draft sketch with kill switch active, got: %d", recDraft.Code)
	}

	// 3. Line art
	reqLine := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/line-art?variant=ink", nil)
	recLine := httptest.NewRecorder()
	handler.ServeHTTP(recLine, reqLine)
	if recLine.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for line art with kill switch active, got: %d", recLine.Code)
	}

	// 4. Suggest colour palette
	reqCol := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/colour-palette/suggest", nil)
	recCol := httptest.NewRecorder()
	handler.ServeHTTP(recCol, reqCol)
	if recCol.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for colour palette suggestion with kill switch active, got: %d", recCol.Code)
	}

	// Deactivate kill switch
	s.KillSwitch.SetActive(false)

	// Verify character reference is no longer blocked by kill switch
	recChar2 := httptest.NewRecorder()
	handler.ServeHTTP(recChar2, reqChar)
	if recChar2.Code != http.StatusAccepted {
		t.Fatalf("expected 202 after kill switch disabled, got: %d", recChar2.Code)
	}
}

func TestQuotaEnforcementAcrossAllStages(t *testing.T) {
	s, handler, repo := setupTestServer(true, "user-1")
	s.DefaultUserQuota = 1.0 // Allow only 1 image

	proj := &domain.Project{
		UserID:         "user-1",
		Title:          "Quota Project",
		OriginalScript: "Act 1: Space.",
		ChapterCount:   1,
	}
	_ = repo.CreateProject(nil, proj)

	ch := &domain.Character{
		ProjectID: proj.ID,
		Name:      "Astronaut",
	}
	_ = repo.CreateCharacter(nil, ch)

	// Consume quota by generating 1 character reference
	reqChar := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+ch.ID+"/reference-image", nil)
	recChar := httptest.NewRecorder()
	handler.ServeHTTP(recChar, reqChar)
	if recChar.Code != http.StatusAccepted {
		t.Fatalf("first image should succeed, got: %d", recChar.Code)
	}

	// Second image attempt exceeds daily quota of 1.0
	ch2 := &domain.Character{
		ProjectID: proj.ID,
		Name:      "Alien",
	}
	_ = repo.CreateCharacter(nil, ch2)

	reqChar2 := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+ch2.ID+"/reference-image", nil)
	recChar2 := httptest.NewRecorder()
	handler.ServeHTTP(recChar2, reqChar2)
	if recChar2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after daily quota reached, got: %d", recChar2.Code)
	}
}

func TestCascadeChapterOutlineEditStalesDraft(t *testing.T) {
	_, handler, repo := setupTestServer(true, "test-user")

	proj := &domain.Project{
		UserID:         "test-user",
		Title:          "Cascade Outline Test",
		OriginalScript: "The hero begins the journey.",
		ChapterCount:   1,
	}
	_ = repo.CreateProject(nil, proj)

	// Approved character
	now := time.Now().UTC()
	approvedAt := now.Add(time.Microsecond)
	ch := &domain.Character{
		ProjectID:  proj.ID,
		Name:       "Hero",
		ApprovedAt: &approvedAt,
		UpdatedAt:  now,
	}
	_ = repo.CreateCharacter(nil, ch)

	// Chapter created
	chap := &domain.Chapter{
		ProjectID:     proj.ID,
		ChapterNumber: 1,
		Title:         "Initial Title",
		Summary:       "Initial Summary",
		UpdatedAt:     now,
	}
	_ = repo.CreateChapter(nil, chap)

	pg := &domain.Page{
		ChapterID:  chap.ID,
		PageNumber: 1,
		UpdatedAt:  now,
	}
	_ = repo.CreatePage(nil, pg)

	// 1. Approve chapter outline
	reqApp := httptest.NewRequest(http.MethodPost, "/api/v1/chapters/"+chap.ID+"/approve", nil)
	recApp := httptest.NewRecorder()
	handler.ServeHTTP(recApp, reqApp)
	if recApp.Code != http.StatusOK {
		t.Fatalf("approve chapter failed: %d", recApp.Code)
	}

	// 2. Draft generation succeeds when outline approval is fresh
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/draft", nil)
	recDraft := httptest.NewRecorder()
	handler.ServeHTTP(recDraft, reqDraft)
	if recDraft.Code != http.StatusAccepted {
		t.Fatalf("draft generation should succeed with fresh outline approval, got: %d", recDraft.Code)
	}

	// 3. User edits chapter outline -> sets chapter UpdatedAt to newer timestamp
	time.Sleep(10 * time.Millisecond) // Ensure newer timestamp
	editPayload, _ := json.Marshal(map[string]string{
		"title":   "Modified Chapter Title",
		"summary": "Modified Summary",
	})
	reqEdit := httptest.NewRequest(http.MethodPatch, "/api/v1/chapters/"+chap.ID, bytes.NewReader(editPayload))
	recEdit := httptest.NewRecorder()
	handler.ServeHTTP(recEdit, reqEdit)
	if recEdit.Code != http.StatusOK {
		t.Fatalf("edit chapter failed: %d", recEdit.Code)
	}

	// 4. Attempt draft generation again -> BLOCKED because outline approval is stale!
	recDraftStale := httptest.NewRecorder()
	handler.ServeHTTP(recDraftStale, reqDraft)
	if recDraftStale.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when chapter outline approval is stale, got: %d", recDraftStale.Code)
	}

	// 5. Re-approve chapter outline
	recApp2 := httptest.NewRecorder()
	handler.ServeHTTP(recApp2, reqApp)
	if recApp2.Code != http.StatusOK {
		t.Fatalf("re-approving chapter outline failed: %d", recApp2.Code)
	}

	// 6. Draft generation now succeeds again
	recDraftFresh := httptest.NewRecorder()
	handler.ServeHTTP(recDraftFresh, reqDraft)
	if recDraftFresh.Code != http.StatusAccepted {
		t.Fatalf("expected 202 after outline re-approved, got: %d", recDraftFresh.Code)
	}
}

func TestCascadePanelEditStalesDraftApproval(t *testing.T) {
	_, handler, repo := setupTestServer(true, "test-user")

	now := time.Now().UTC()
	approvedAt := now.Add(time.Microsecond)
	proj := &domain.Project{
		UserID:         "test-user",
		Title:          "Panel Cascade Test",
		OriginalScript: "A knight enters the dragon cave.",
		ChapterCount:   1,
	}
	_ = repo.CreateProject(nil, proj)

	ch := &domain.Character{
		ProjectID:  proj.ID,
		Name:       "Knight",
		ApprovedAt: &approvedAt,
		UpdatedAt:  now,
	}
	_ = repo.CreateCharacter(nil, ch)

	chap := &domain.Chapter{
		ProjectID:     proj.ID,
		ChapterNumber: 1,
		Title:         "The Cave",
		UpdatedAt:     now,
	}
	_ = repo.CreateChapter(nil, chap)

	pan := &domain.Panel{
		ChapterID:   chap.ID,
		PanelNumber: 1,
		Dialogue:    "Draw your sword!",
		UpdatedAt:   now,
	}
	_ = repo.CreatePanel(nil, pan)

	pg := &domain.Page{
		ChapterID:  chap.ID,
		PageNumber: 1,
		PanelIDs:   []string{pan.ID},
		UpdatedAt:  now,
	}
	_ = repo.CreatePage(nil, pg)

	// Approve outline
	_ = repo.RecordApproval(nil, &domain.StageApproval{
		ProjectID:  proj.ID,
		EntityType: "chapter_outline",
		EntityID:   chap.ID,
		ApprovedAt: now.Add(time.Microsecond),
	})

	// Generate and approve draft
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/draft", nil)
	recDraft := httptest.NewRecorder()
	handler.ServeHTTP(recDraft, reqDraft)
	if recDraft.Code != http.StatusAccepted {
		t.Fatalf("draft generation failed: %d", recDraft.Code)
	}
	pageBeforeApproval, err := repo.GetPage(nil, pg.ID)
	if err != nil {
		t.Fatal(err)
	}

	reqAppDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/draft/approve", nil)
	recAppDraft := httptest.NewRecorder()
	handler.ServeHTTP(recAppDraft, reqAppDraft)
	if recAppDraft.Code != http.StatusOK {
		t.Fatalf("approve draft failed: %d", recAppDraft.Code)
	}
	approvedPage, err := repo.GetPage(nil, pg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !approvedPage.UpdatedAt.Equal(pageBeforeApproval.UpdatedAt) {
		t.Fatalf("draft approval changed page content timestamp: got %s, want %s", approvedPage.UpdatedAt, pageBeforeApproval.UpdatedAt)
	}

	// 1. Line art generation succeeds with approved draft
	reqLine := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/line-art?variant=ink", nil)
	recLine := httptest.NewRecorder()
	handler.ServeHTTP(recLine, reqLine)
	if recLine.Code != http.StatusAccepted {
		t.Fatalf("line art generation should succeed with approved draft, got: %d", recLine.Code)
	}

	// 2. User edits panel -> triggers panel cascade staling parent page draft approval
	time.Sleep(10 * time.Millisecond)
	reqEditPan := httptest.NewRequest(http.MethodPatch, "/api/v1/panels/"+pan.ID, strings.NewReader(`{"dialogue":"Edited panel"}`))
	recEditPan := httptest.NewRecorder()
	handler.ServeHTTP(recEditPan, reqEditPan)
	if recEditPan.Code != http.StatusOK {
		t.Fatalf("panel edit failed: %d", recEditPan.Code)
	}

	// 3. Line art generation is now BLOCKED because draft approval was staled!
	recLineStale := httptest.NewRecorder()
	handler.ServeHTTP(recLineStale, reqLine)
	if recLineStale.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 generating line art after panel edit stales draft, got: %d", recLineStale.Code)
	}

	// 4. The old draft was drawn from the unedited panel, so approving it again is refused
	recAppDraft2 := httptest.NewRecorder()
	handler.ServeHTTP(recAppDraft2, reqAppDraft)
	if recAppDraft2.Code != http.StatusConflict {
		t.Fatalf("re-approving a draft that predates the panel edit should be refused, got: %d", recAppDraft2.Code)
	}

	// 5. Regenerate the draft from the edited panel, approve it -> line art generation succeeds again
	reqRedraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/draft", nil)
	recRedraft := httptest.NewRecorder()
	handler.ServeHTTP(recRedraft, reqRedraft)
	if recRedraft.Code != http.StatusAccepted {
		t.Fatalf("draft regeneration failed: %d", recRedraft.Code)
	}
	recAppDraft3 := httptest.NewRecorder()
	handler.ServeHTTP(recAppDraft3, reqAppDraft)
	if recAppDraft3.Code != http.StatusOK {
		t.Fatalf("approving the regenerated draft failed: %d", recAppDraft3.Code)
	}

	recLineFresh := httptest.NewRecorder()
	handler.ServeHTTP(recLineFresh, reqLine)
	if recLineFresh.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for line art after draft re-approved, got: %d", recLineFresh.Code)
	}
}

func TestCascadeDraftRegenerationStalesLineArtApproval(t *testing.T) {
	_, handler, repo := setupTestServer(true, "test-user")

	now := time.Now().UTC()
	approvedAt := now.Add(time.Microsecond)
	proj := &domain.Project{
		UserID:         "test-user",
		Title:          "Regen Cascade Test",
		OriginalScript: "The spaceship escapes the nebula.",
		ChapterCount:   1,
	}
	_ = repo.CreateProject(nil, proj)

	ch := &domain.Character{
		ProjectID:  proj.ID,
		Name:       "Pilot",
		ApprovedAt: &approvedAt,
		UpdatedAt:  now,
	}
	_ = repo.CreateCharacter(nil, ch)

	chap := &domain.Chapter{
		ProjectID:     proj.ID,
		ChapterNumber: 1,
		Title:         "Escape",
		UpdatedAt:     now,
	}
	_ = repo.CreateChapter(nil, chap)

	pg := &domain.Page{
		ChapterID:  chap.ID,
		PageNumber: 1,
		UpdatedAt:  now,
	}
	_ = repo.CreatePage(nil, pg)

	_ = repo.RecordApproval(nil, &domain.StageApproval{
		ProjectID:  proj.ID,
		EntityType: "chapter_outline",
		EntityID:   chap.ID,
		ApprovedAt: now.Add(time.Microsecond),
	})

	// Generate draft & approve
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/draft", nil)
	recDraft := httptest.NewRecorder()
	handler.ServeHTTP(recDraft, reqDraft)

	reqAppDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/draft/approve", nil)
	recAppDraft := httptest.NewRecorder()
	handler.ServeHTTP(recAppDraft, reqAppDraft)

	// Generate line art & approve
	reqLine := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/line-art?variant=ink", nil)
	recLine := httptest.NewRecorder()
	handler.ServeHTTP(recLine, reqLine)

	reqAppLine := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/line-art/approve?variant=ink", nil)
	recAppLine := httptest.NewRecorder()
	handler.ServeHTTP(recAppLine, reqAppLine)

	// Colour palette suggestion succeeds with approved line art
	reqCol := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pg.ID+"/colour-palette/suggest", nil)
	recCol := httptest.NewRecorder()
	handler.ServeHTTP(recCol, reqCol)
	if recCol.Code != http.StatusAccepted {
		t.Fatalf("colour suggestion should succeed with approved line art, got: %d", recCol.Code)
	}

	// 1. User regenerates draft sketch -> triggers cascade staling line art approval
	time.Sleep(10 * time.Millisecond)
	recRegenDraft := httptest.NewRecorder()
	handler.ServeHTTP(recRegenDraft, reqDraft)
	if recRegenDraft.Code != http.StatusAccepted {
		t.Fatalf("draft regeneration failed: %d", recRegenDraft.Code)
	}

	// 2. Colour suggestion is now BLOCKED because line art approval was staled!
	recColStale := httptest.NewRecorder()
	handler.ServeHTTP(recColStale, reqCol)
	if recColStale.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for colour suggestion after draft regeneration, got: %d", recColStale.Code)
	}

	// 3. Line art generation is also BLOCKED because draft sketch needs new approval
	recLineStale := httptest.NewRecorder()
	handler.ServeHTTP(recLineStale, reqLine)
	if recLineStale.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for line art before new draft is approved, got: %d", recLineStale.Code)
	}

	// 4. Re-approve draft -> generate line art -> approve line art -> colour suggestion succeeds!
	recAppDraft2 := httptest.NewRecorder()
	handler.ServeHTTP(recAppDraft2, reqAppDraft)

	recLineFresh := httptest.NewRecorder()
	handler.ServeHTTP(recLineFresh, reqLine)

	recAppLineFresh := httptest.NewRecorder()
	handler.ServeHTTP(recAppLineFresh, reqAppLine)

	recColFresh := httptest.NewRecorder()
	handler.ServeHTTP(recColFresh, reqCol)
	if recColFresh.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for colour suggestion after line art re-approved, got: %d", recColFresh.Code)
	}
}
