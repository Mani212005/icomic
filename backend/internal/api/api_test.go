package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

func setupTestServer(devAuthEnabled bool, defaultUID string) (*api.Server, http.Handler, db.Repository) {
	repo := db.NewInMemoryRepository()
	balances := map[string]float64{
		"test-user": 50.0,
		"user-1":    50.0,
		"user-2":    50.0,
	}
	if defaultUID != "" {
		balances[defaultUID] = 50.0
	}
	creditSvc := credits.NewInMemoryCreditService(balances)
	jobSvc := jobs.NewInMemoryJobService()
	authenticator := auth.NewDevAuthenticator(devAuthEnabled, defaultUID)
	imageHosting := auth.NewDefaultImageHosting("/media")
	fakeAI := gen.NewFakeAIServiceClient()

	s := api.NewServer(
		repo,
		creditSvc,
		jobSvc,
		fakeAI,
		authenticator,
		imageHosting,
		2, // 2 panels per page for tests
	)
	return s, s.NewRouter(), repo
}

func TestHealthCheck(t *testing.T) {
	_, handler, _ := setupTestServer(true, "test-user")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status 'ok', got '%s'", body["status"])
	}
}

func TestAuthEnforcementAndDevAuthenticator(t *testing.T) {
	// Dev auth disabled: passing X-User-ID must be rejected with 401
	_, handlerDisabled, _ := setupTestServer(false, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("X-User-ID", "attacker")
	rec := httptest.NewRecorder()
	handlerDisabled.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when dev auth disabled, got %d", rec.Code)
	}

	// Dev auth enabled: accepted
	_, handlerEnabled, _ := setupTestServer(true, "test-user")
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rec2 := httptest.NewRecorder()
	handlerEnabled.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when dev auth enabled, got %d", rec2.Code)
	}
}

func TestCreateProject(t *testing.T) {
	_, handler, _ := setupTestServer(true, "test-user")

	payload := map[string]interface{}{
		"title":           "The Quantum Detective",
		"original_script": "Page 1: The neon lights flicker in Neo-Tokyo as detective Jax ignites his pipe.",
		"art_style":       "cyberpunk-noir",
		"chapter_count":   3,
	}
	data, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestProjectFinishPresetIsOwnedAndPatchable(t *testing.T) {
	_, handler, repo := setupTestServer(true, "test-user")
	project := &domain.Project{ID: "43ceac9c-587c-4d4f-8342-0ac640c74fe3", UserID: "test-user", Title: "Finish", OriginalScript: "A short story", ChapterCount: 1}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetProject(context.Background(), project.ID)
	if err != nil || loaded.FinishPreset != "print-heavy" {
		t.Fatalf("new project preset = %q, err = %v", loaded.FinishPreset, err)
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID, nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"finish_preset":"print-heavy"`) {
		t.Fatalf("project setting read did not return preset: %d %s", get.Code, get.Body.String())
	}
	patch := httptest.NewRecorder()
	handler.ServeHTTP(patch, httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+project.ID, strings.NewReader(`{"finish_preset":"print-plus"}`)))
	if patch.Code != http.StatusOK {
		t.Fatalf("preset patch returned %d: %s", patch.Code, patch.Body.String())
	}
	loaded, err = repo.GetProject(context.Background(), project.ID)
	if err != nil || loaded.FinishPreset != "print-plus" || !loaded.FinishPresetExplicit {
		t.Fatalf("patched preset = %q (explicit %t), err = %v", loaded.FinishPreset, loaded.FinishPresetExplicit, err)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+project.ID, strings.NewReader(`{"finish_preset":"grain"}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid preset returned %d, want 400", invalid.Code)
	}
}

func TestFinishPageImageUsesOwnedApprovedArtAndProjectPreset(t *testing.T) {
	server, handler, repo := setupTestServer(true, "test-user")
	project := &domain.Project{ID: "b93d9508-1571-45ea-b8bf-bb87347f3b1f", UserID: "test-user", Title: "Finish", FinishPreset: "print-plus", FinishPresetExplicit: true}
	chapter := &domain.Chapter{ID: "311227f2-48c4-4091-bad8-d461cc571f6a", ProjectID: project.ID}
	approved := time.Now().UTC().Add(-time.Minute)
	page := &domain.Page{ID: "6fdbcb4a-ec3d-49ac-ae76-07ca3730c030", ChapterID: chapter.ID, LineArtImageURL: "pages/raw.png", LineArtApprovedAt: &approved, UpdatedAt: approved.Add(-time.Second)}
	for _, create := range []func() error{
		func() error { return repo.CreateProject(context.Background(), project) },
		func() error { return repo.CreateChapter(context.Background(), chapter) },
		func() error { return repo.CreatePage(context.Background(), page) },
	} {
		if err := create(); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	server.AIClient.(*gen.FakeAIServiceClient).FinishPageImageFunc = func(_ context.Context, request gen.FinishPageImageRequest) (*gen.FinishPageImageResponse, error) {
		called = true
		if request.PageID != page.ID || request.ImageURL != page.LineArtImageURL || request.FinishPreset != gen.PrintPlus {
			t.Fatalf("unexpected finish request: %+v", request)
		}
		return &gen.FinishPageImageResponse{ImageURL: "finishes/page.jpg"}, nil
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+page.ID+"/finish?variant=ink", nil))
	var finished map[string]string
	if response.Code != http.StatusOK || !called || json.Unmarshal(response.Body.Bytes(), &finished) != nil {
		t.Fatalf("finish endpoint returned %d (%s), called=%t", response.Code, response.Body.String(), called)
	}
	for field, key := range map[string]string{"finished_image_url": "finishes/page.jpg", "raw_master_image_url": "pages/raw.png"} {
		link, err := url.Parse(finished[field])
		if err != nil || link.Host != "example.com" || link.Path != "/media/"+key || link.Query().Get("sig") == "" || link.Query().Get("u") != "test-user" {
			t.Fatalf("%s = %q, want a scoped /media/%s link for test-user", field, finished[field], key)
		}
	}
}

func TestGenerationResponsePreservesStructuredAIError(t *testing.T) {
	s, handler, repo := setupTestServer(true, "test-user")
	project := &domain.Project{ID: "f3149d43-28d9-5d7f-a24f-961e9de91774", UserID: "test-user", Title: "Test", OriginalScript: "A short story", ChapterCount: 1}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	retryAfter := 11.0
	s.AIClient.(*gen.FakeAIServiceClient).AnalyzeScriptFunc = func(context.Context, gen.AnalyzeScriptRequest) (*gen.AnalyzeScriptResponse, error) {
		return nil, &gen.AIServiceError{Code: "RATE_LIMITED", Message: "Generation is temporarily rate limited.", StatusCode: http.StatusTooManyRequests, Retryable: true, RetryAfterSeconds: &retryAfter}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/analyze", nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("HTTP %d, want 429: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != "RATE_LIMITED" || response["message"] != "Generation is temporarily rate limited." || response["retryable"] != true || response["retry_after_seconds"] != 11.0 {
		t.Fatalf("structured AI error was not preserved: %v", response)
	}
	if got := recorder.Header().Get("Retry-After"); got != "11" {
		t.Fatalf("Retry-After header = %q, want 11", got)
	}
}

func TestEstimateCost(t *testing.T) {
	server, handler, repo := setupTestServer(true, "test-user")
	server.Pricing = credits.PricingConfig{
		ImageModelID: "muse-image-configured", VisionModelID: "vision-configured",
		ImagePriceUSD: 0.0275, ColourEstimateUSD: 0.003, Version: "api-test-v2", Verified: false,
	}
	if err := repo.CreateProject(context.Background(), &domain.Project{ID: "03f4a2ef-fa27-509e-a9e3-feb2b84c0a28", UserID: "test-user"}); err != nil {
		t.Fatal(err)
	}

	payload := map[string]interface{}{
		"stage":       "draft_sketch",
		"image_count": 5,
	}
	data, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/03f4a2ef-fa27-509e-a9e3-feb2b84c0a28/estimate", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["estimated_cost"] != 0.1375 || resp["model_id"] != "muse-image-configured" || resp["pricing_version"] != "api-test-v2" || resp["price_verified"] != false {
		t.Fatalf("estimate did not use configured pricing metadata: %+v", resp)
	}
}

func TestPanelPatchPersistsEditableFieldsAndRejectsMalformedBody(t *testing.T) {
	_, handler, repo := setupTestServer(true, "test-user")
	ctx := context.Background()
	project := &domain.Project{UserID: "test-user", Title: "Panel edits", OriginalScript: "A story", ChapterCount: 1}
	if err := repo.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "Chapter"}
	if err := repo.CreateChapter(ctx, chapter); err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	panel := &domain.Panel{
		ChapterID: chapter.ID, PanelNumber: 1, SceneDescription: "Old scene", CharacterActions: "Old action",
		CameraAngle: "Old angle", Background: "Old background", Dialogue: "Old dialogue", Narration: "Old narration",
		ImagePrompt: "Old prompt", UpdatedAt: before,
	}
	if err := repo.CreatePanel(ctx, panel); err != nil {
		t.Fatal(err)
	}
	page := &domain.Page{ChapterID: chapter.ID, PageNumber: 1, PanelIDs: []string{panel.ID}, DraftApprovedAt: &before, UpdatedAt: before}
	if err := repo.CreatePage(ctx, page); err != nil {
		t.Fatal(err)
	}

	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/panels/"+panel.ID, strings.NewReader(body))
		req.Header.Set("X-User-ID", "test-user")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	malformed := request(`{"dialogue":`)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed PATCH expected 400, got %d: %s", malformed.Code, malformed.Body.String())
	}
	unchanged, err := repo.GetPanel(ctx, panel.ID)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := repo.ListPages(ctx, chapter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.UpdatedAt.Equal(before) || len(pages) != 1 || pages[0].DraftApprovedAt == nil {
		t.Fatalf("malformed PATCH changed panel timestamp or approval: panel=%+v pages=%+v", unchanged, pages)
	}

	want := map[string]string{
		"scene_description": "New scene", "character_actions": "New action", "camera_angle": "New angle",
		"background": "New background", "dialogue": "New dialogue", "narration": "New narration", "image_prompt": "New prompt",
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	updated := request(string(encoded))
	if updated.Code != http.StatusOK {
		t.Fatalf("valid PATCH expected 200, got %d: %s", updated.Code, updated.Body.String())
	}
	var response struct {
		Panel domain.Panel `json:"panel"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	assertPanelFields(t, &response.Panel, want)
	listed := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/chapters/"+chapter.ID+"/panels", nil)
	listReq.Header.Set("X-User-ID", "test-user")
	handler.ServeHTTP(listed, listReq)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET panels expected 200, got %d: %s", listed.Code, listed.Body.String())
	}
	var listResponse struct {
		Panels []domain.Panel `json:"panels"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listResponse); err != nil {
		t.Fatal(err)
	}
	if len(listResponse.Panels) != 1 {
		t.Fatalf("GET returned %d panels, want 1", len(listResponse.Panels))
	}
	assertPanelFields(t, &listResponse.Panels[0], want)
	if response.Panel.UpdatedAt.Equal(before) {
		t.Fatal("successful PATCH did not update panel timestamp")
	}
	pages, err = repo.ListPages(ctx, chapter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].DraftApprovedAt != nil {
		t.Fatalf("successful edit did not stale draft approval: %+v", pages)
	}
}

func assertPanelFields(t *testing.T, panel *domain.Panel, want map[string]string) {
	t.Helper()
	got := map[string]string{
		"scene_description": panel.SceneDescription, "character_actions": panel.CharacterActions, "camera_angle": panel.CameraAngle,
		"background": panel.Background, "dialogue": panel.Dialogue, "narration": panel.Narration, "image_prompt": panel.ImagePrompt,
	}
	for field, value := range want {
		if got[field] != value {
			t.Errorf("%s = %q, want %q", field, got[field], value)
		}
	}
}

func TestPaletteRoutesRequirePageOwnership(t *testing.T) {
	_, handler, repo := setupTestServer(true, "owner")
	ctx := context.Background()
	if err := repo.CreateProject(ctx, &domain.Project{ID: "ba298efb-07bc-58ba-b92c-d1798edf7606", UserID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateChapter(ctx, &domain.Chapter{ID: "946e2bd7-facc-54af-8a76-892c5ae92445", ProjectID: "ba298efb-07bc-58ba-b92c-d1798edf7606"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePage(ctx, &domain.Page{ID: "374dc62a-f5bc-5af6-b94e-b35eeea746c4", ChapterID: "946e2bd7-facc-54af-8a76-892c5ae92445"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveColourPalette(ctx, &domain.ColourPalette{PageID: "374dc62a-f5bc-5af6-b94e-b35eeea746c4", Palette: map[string]interface{}{"skin": "#123456"}}); err != nil {
		t.Fatal(err)
	}

	request := func(method, path, userID, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-User-ID", userID)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := request(http.MethodGet, "/api/v1/pages/374dc62a-f5bc-5af6-b94e-b35eeea746c4/colour-palette", "owner", ""); rec.Code != http.StatusOK {
		t.Fatalf("owner GET expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPatch, "/api/v1/pages/374dc62a-f5bc-5af6-b94e-b35eeea746c4/colour-palette", "owner", `{"palette":{"skin":"#654321"}}`); rec.Code != http.StatusOK {
		t.Fatalf("owner PATCH expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodGet, "/api/v1/pages/374dc62a-f5bc-5af6-b94e-b35eeea746c4/colour-palette", "other-user", ""); rec.Code != http.StatusForbidden {
		t.Errorf("nonowner GET expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPatch, "/api/v1/pages/374dc62a-f5bc-5af6-b94e-b35eeea746c4/colour-palette", "other-user", `{"palette":{"attacker":"#FF0000"}}`); rec.Code != http.StatusForbidden {
		t.Errorf("nonowner PATCH expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPatch, "/api/v1/pages/f8b9e358-7f01-5884-b6db-842b1cb962e4/colour-palette", "owner", `{"palette":{"skin":"#FFFFFF"}}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing page PATCH expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	palette, err := repo.GetColourPalette(ctx, "374dc62a-f5bc-5af6-b94e-b35eeea746c4")
	if err != nil {
		t.Fatal(err)
	}
	if palette.Palette["skin"] != "#654321" || palette.Palette["attacker"] != nil {
		t.Errorf("denied PATCH changed palette: %#v", palette.Palette)
	}
}

func TestEstimateCostRequiresProjectOwnership(t *testing.T) {
	_, handler, repo := setupTestServer(true, "owner")
	if err := repo.CreateProject(context.Background(), &domain.Project{ID: "cb73dc07-e18e-5e50-b34d-6c17be0cdb0d", UserID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProject(context.Background(), &domain.Project{ID: "2607af95-8543-526a-b237-a60c92bac95e", UserID: "other-user"}); err != nil {
		t.Fatal(err)
	}
	request := func(projectID, userID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/estimate", strings.NewReader(`{"stage":"draft_sketch","image_count":1}`))
		req.Header.Set("X-User-ID", userID)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := request("cb73dc07-e18e-5e50-b34d-6c17be0cdb0d", "owner"); rec.Code != http.StatusOK {
		t.Errorf("owner estimate expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request("2607af95-8543-526a-b237-a60c92bac95e", "attacker"); rec.Code != http.StatusForbidden {
		t.Errorf("foreign estimate expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request("ef200fd9-20d9-55d6-a146-98645aba9be2", "owner"); rec.Code != http.StatusNotFound {
		t.Errorf("missing estimate project expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreditUsage(t *testing.T) {
	_, handler, _ := setupTestServer(true, "test-user")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/credit-usage", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["balance"] != 50.0 {
		t.Fatalf("expected balance 50.0, got %v", resp["balance"])
	}
}

func TestStrictOwnershipEnforcement(t *testing.T) {
	_, handler, repo := setupTestServer(true, "user-1")

	// User 1 creates project
	proj := &domain.Project{
		ID:             "c1028831-7513-5f58-8fab-f9b6868a80e5",
		UserID:         "user-1",
		Title:          "User 1 Only Project",
		OriginalScript: "Act 1. Secret notes.",
	}
	_ = repo.CreateProject(context.Background(), proj)

	// User 1 can fetch project
	reqOwner := httptest.NewRequest(http.MethodGet, "/api/v1/projects/c1028831-7513-5f58-8fab-f9b6868a80e5", nil)
	reqOwner.Header.Set("X-User-ID", "user-1")
	recOwner := httptest.NewRecorder()
	handler.ServeHTTP(recOwner, reqOwner)
	if recOwner.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner, got %d", recOwner.Code)
	}

	// User 2 CANNOT fetch User 1's project -> 403 Forbidden (SPEC 10, 11.1)
	reqAttacker := httptest.NewRequest(http.MethodGet, "/api/v1/projects/c1028831-7513-5f58-8fab-f9b6868a80e5", nil)
	reqAttacker.Header.Set("X-User-ID", "user-2")
	recAttacker := httptest.NewRecorder()
	handler.ServeHTTP(recAttacker, reqAttacker)
	if recAttacker.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-user get, got %d", recAttacker.Code)
	}

	// User 2 CANNOT delete User 1's project -> 403 Forbidden
	reqDeleteAttacker := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/c1028831-7513-5f58-8fab-f9b6868a80e5", nil)
	reqDeleteAttacker.Header.Set("X-User-ID", "user-2")
	recDeleteAttacker := httptest.NewRecorder()
	handler.ServeHTTP(recDeleteAttacker, reqDeleteAttacker)
	if recDeleteAttacker.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for cross-user delete, got %d", recDeleteAttacker.Code)
	}
}

func TestEndToEndPipelineAndApprovalGates(t *testing.T) {
	server, handler, _ := setupTestServer(true, "creator-1")

	// 1. Create project
	projBody, _ := json.Marshal(map[string]interface{}{
		"title":           "Cosmic Quest",
		"original_script": "Chapter 1: The Starship departs. Chapter 2: Destination reached.",
		"chapter_count":   2,
	})
	reqProj := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(projBody))
	recProj := httptest.NewRecorder()
	handler.ServeHTTP(recProj, reqProj)
	if recProj.Code != http.StatusCreated {
		t.Fatalf("create project failed: %d", recProj.Code)
	}
	var projResp map[string]interface{}
	_ = json.NewDecoder(recProj.Body).Decode(&projResp)
	projID := projResp["project"].(map[string]interface{})["id"].(string)

	// 2. Analyze script
	reqAnalyze := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/analyze", nil)
	recAnalyze := httptest.NewRecorder()
	handler.ServeHTTP(recAnalyze, reqAnalyze)
	if recAnalyze.Code != http.StatusAccepted {
		t.Fatalf("analyze script failed: %d", recAnalyze.Code)
	}

	// 3. List characters
	reqChars := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projID+"/characters", nil)
	recChars := httptest.NewRecorder()
	handler.ServeHTTP(recChars, reqChars)
	var charsResp map[string]interface{}
	_ = json.NewDecoder(recChars.Body).Decode(&charsResp)
	chars := charsResp["characters"].([]interface{})
	if len(chars) == 0 {
		t.Fatal("expected characters extracted")
	}

	// 4. Generate a reference image for every character
	for _, ch := range chars {
		cID := ch.(map[string]interface{})["id"].(string)
		reqRef := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+cID+"/reference-image", nil)
		recRef := httptest.NewRecorder()
		handler.ServeHTTP(recRef, reqRef)
		if recRef.Code != http.StatusAccepted {
			t.Fatalf("generate reference image failed: %d", recRef.Code)
		}
	}

	// 5. Approve characters
	for _, ch := range chars {
		cID := ch.(map[string]interface{})["id"].(string)
		reqApp := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+cID+"/approve", nil)
		recApp := httptest.NewRecorder()
		handler.ServeHTTP(recApp, reqApp)
		if recApp.Code != http.StatusOK {
			t.Fatalf("approve character failed: %d", recApp.Code)
		}
	}

	// 6. Generate chapters
	reqChaps := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/chapters", nil)
	recChaps := httptest.NewRecorder()
	handler.ServeHTTP(recChaps, reqChaps)
	if recChaps.Code != http.StatusOK {
		t.Fatalf("generate chapters failed: %d", recChaps.Code)
	}
	var chapsResp map[string]interface{}
	_ = json.NewDecoder(recChaps.Body).Decode(&chapsResp)
	chapters := chapsResp["chapters"].([]interface{})
	chapID := chapters[0].(map[string]interface{})["id"].(string)

	// 7. Approve chapter outline
	reqAppChap := httptest.NewRequest(http.MethodPost, "/api/v1/chapters/"+chapID+"/approve", nil)
	recAppChap := httptest.NewRecorder()
	handler.ServeHTTP(recAppChap, reqAppChap)
	if recAppChap.Code != http.StatusOK {
		t.Fatalf("approve chapter failed: %d", recAppChap.Code)
	}

	// 8. Generate panels and pages
	reqPanels := httptest.NewRequest(http.MethodPost, "/api/v1/chapters/"+chapID+"/panels", nil)
	recPanels := httptest.NewRecorder()
	handler.ServeHTTP(recPanels, reqPanels)
	if recPanels.Code != http.StatusOK {
		t.Fatalf("generate panels failed: %d", recPanels.Code)
	}
	var panelsResp map[string]interface{}
	_ = json.NewDecoder(recPanels.Body).Decode(&panelsResp)
	pages := panelsResp["pages"].([]interface{})
	pageID := pages[0].(map[string]interface{})["id"].(string)

	// 9. Generate draft sketch on page
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/draft", nil)
	recDraft := httptest.NewRecorder()
	handler.ServeHTTP(recDraft, reqDraft)
	if recDraft.Code != http.StatusAccepted {
		t.Fatalf("generate draft failed: %d", recDraft.Code)
	}

	// 10. Attempt line art before draft approval -> must fail
	reqLinePremature := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art?variant=ink", nil)
	recLinePremature := httptest.NewRecorder()
	handler.ServeHTTP(recLinePremature, reqLinePremature)
	if recLinePremature.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unapproved draft, got %d", recLinePremature.Code)
	}

	// 11. Approve draft sketch
	reqAppDraft := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/draft/approve", nil)
	recAppDraft := httptest.NewRecorder()
	handler.ServeHTTP(recAppDraft, reqAppDraft)
	if recAppDraft.Code != http.StatusOK {
		t.Fatalf("approve draft failed: %d", recAppDraft.Code)
	}

	// 12. Generate line art
	reqLine := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art?variant=ink", nil)
	recLine := httptest.NewRecorder()
	handler.ServeHTTP(recLine, reqLine)
	if recLine.Code != http.StatusAccepted {
		t.Fatalf("generate line art failed: %d", recLine.Code)
	}

	// 13. Approve line art
	reqAppLine := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art/approve?variant=ink", nil)
	recAppLine := httptest.NewRecorder()
	handler.ServeHTTP(recAppLine, reqAppLine)
	if recAppLine.Code != http.StatusOK {
		t.Fatalf("approve line art failed: %d", recAppLine.Code)
	}

	// 14. Colouring: suggest, edit, approve
	reqPalSuggest := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/colour-palette/suggest", nil)
	recPalSuggest := httptest.NewRecorder()
	handler.ServeHTTP(recPalSuggest, reqPalSuggest)
	if recPalSuggest.Code != http.StatusAccepted {
		t.Fatalf("suggest colour palette failed: %d", recPalSuggest.Code)
	}
	reqColourImage := httptest.NewRequest(http.MethodPut, "/api/v1/pages/"+pageID+"/colour-image", bytes.NewReader([]byte(fixturePNG)))
	recColourImage := httptest.NewRecorder()
	handler.ServeHTTP(recColourImage, reqColourImage)
	if recColourImage.Code != http.StatusOK {
		t.Fatalf("upload colour image failed: %d (%s)", recColourImage.Code, recColourImage.Body.String())
	}

	reqAppColour := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/colour/approve", nil)
	recAppColour := httptest.NewRecorder()
	handler.ServeHTTP(recAppColour, reqAppColour)
	if recAppColour.Code != http.StatusOK {
		t.Fatalf("approve colour failed: %d", recAppColour.Code)
	}

	// 15. Export and Download
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("%PDF-1.4\n% Real assembled test comic\n%%EOF\n"))
	}))
	defer storageServer.Close()

	fakeAI := server.AIClient.(*gen.FakeAIServiceClient)
	fakeAI.AssemblePdfFunc = func(ctx context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		return &gen.AssemblePdfResponse{
			JobID:                       req.JobID,
			PdfURL:                      fmt.Sprintf("%s/storage/fake/exports/%s/comic.pdf", storageServer.URL, req.ProjectID),
			ProvenanceMetadataPreserved: true,
			PageCount:                   len(req.Pages) + 1,
		}, nil
	}

	reqExport := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projID+"/export", nil)
	recExport := httptest.NewRecorder()
	handler.ServeHTTP(recExport, reqExport)
	if recExport.Code != http.StatusAccepted {
		t.Fatalf("export failed: %d", recExport.Code)
	}
	var expResp map[string]interface{}
	_ = json.NewDecoder(recExport.Body).Decode(&expResp)
	exportID := expResp["export"].(map[string]interface{})["id"].(string)

	reqDownload := httptest.NewRequest(http.MethodGet, "/api/v1/exports/"+exportID+"/download", nil)
	recDownload := httptest.NewRecorder()
	handler.ServeHTTP(recDownload, reqDownload)
	if recDownload.Code != http.StatusOK || recDownload.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("download failed: %d, content-type: %s", recDownload.Code, recDownload.Header().Get("Content-Type"))
	}
	if !bytes.Contains(recDownload.Body.Bytes(), []byte("% Real assembled test comic")) {
		t.Fatalf("expected real assembled PDF bytes, got %s", recDownload.Body.String())
	}
}

func TestCORSMiddleware(t *testing.T) {
	_, handler, _ := setupTestServer(true, "test-user")

	// 1. Preflight OPTIONS request
	reqOptions := httptest.NewRequest(http.MethodOptions, "/api/v1/projects", nil)
	reqOptions.Header.Set("Origin", "http://localhost:5173")
	reqOptions.Header.Set("Access-Control-Request-Method", "POST")
	recOptions := httptest.NewRecorder()
	handler.ServeHTTP(recOptions, reqOptions)

	if recOptions.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for preflight, got %d", recOptions.Code)
	}
	if recOptions.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("expected origin http://localhost:5173, got %s", recOptions.Header().Get("Access-Control-Allow-Origin"))
	}
	if recOptions.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Errorf("expected Access-Control-Allow-Methods header to be set")
	}

	// 2. Normal GET request
	reqGet := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	reqGet.Header.Set("Origin", "http://localhost:5173")
	recGet := httptest.NewRecorder()
	handler.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recGet.Code)
	}
	if recGet.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("expected origin http://localhost:5173, got %s", recGet.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestPromptPreModeration(t *testing.T) {
	server, handler, repo := setupTestServer(true, "creator-1")

	// 1. Create project with blocked keyword in script
	proj := &domain.Project{
		ID:             "proj-mod-1",
		UserID:         "creator-1",
		Title:          "Moderation Test",
		OriginalScript: "This scene has extreme violence and illegal stuff.",
		ChapterCount:   1,
		ArtStyle:       "manga",
	}
	_ = repo.CreateProject(context.Background(), proj)

	// AnalyzeScript should be rejected by pre-moderation screening
	reqAnalyze := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+proj.ID+"/analyze", nil)
	recAnalyze := httptest.NewRecorder()
	handler.ServeHTTP(recAnalyze, reqAnalyze)

	if recAnalyze.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for blocked prompt, got %d (body: %s)", recAnalyze.Code, recAnalyze.Body.String())
	}

	// 2. Test utility endpoint POST /api/v1/prompts/moderate
	modBody, _ := json.Marshal(map[string]string{"prompt": "A peaceful landscape"})
	reqMod := httptest.NewRequest(http.MethodPost, "/api/v1/prompts/moderate", bytes.NewReader(modBody))
	recMod := httptest.NewRecorder()
	handler.ServeHTTP(recMod, reqMod)

	if recMod.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for prompt moderation, got %d", recMod.Code)
	}
	var modResp map[string]interface{}
	_ = json.NewDecoder(recMod.Body).Decode(&modResp)
	if modResp["blocked"].(bool) != false {
		t.Errorf("expected unblocked prompt, got %+v", modResp)
	}

	_ = server
}

func TestAIServiceFailureRefund(t *testing.T) {
	server, handler, repo := setupTestServer(true, "user-fail")

	proj := &domain.Project{
		ID:       "proj-fail-1",
		UserID:   "user-fail",
		Title:    "Fail Test",
		ArtStyle: "manga",
	}
	_ = repo.CreateProject(context.Background(), proj)
	char := &domain.Character{
		ID:          "char-fail-1",
		ProjectID:   proj.ID,
		Name:        "Hero",
		Description: "A hero",
	}
	_ = repo.CreateCharacter(context.Background(), char)

	// Inject AI failure
	fakeAI := server.AIClient.(*gen.FakeAIServiceClient)
	fakeAI.GenerateCharacterReferenceFunc = func(ctx context.Context, req gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
		return nil, errors.New("upstream AI generation service timed out")
	}

	initialBalance, _ := server.CreditService.GetBalance(context.Background(), "user-fail")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/characters/"+char.ID+"/reference-image", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on AI failure, got %d", rec.Code)
	}

	finalBalance, _ := server.CreditService.GetBalance(context.Background(), "user-fail")
	if finalBalance != initialBalance {
		t.Errorf("expected credit refund after AI failure: initial %f, final %f", initialBalance, finalBalance)
	}
}
