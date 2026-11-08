package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func paintedPageFixture(t *testing.T) (*api.Server, http.Handler, db.Repository, string, string) {
	t.Helper()
	repo := db.NewInMemoryRepository()
	ctx := context.Background()

	project := &domain.Project{UserID: "owner", Title: "Painted Prototype", Status: "draft"}
	if err := repo.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}

	past := time.Now().UTC().Add(-2 * time.Hour)
	approvedAt := time.Now().UTC().Add(-time.Hour)

	char := &domain.Character{
		ProjectID:         project.ID,
		Name:              "Nia",
		ReferenceImageURL: "http://storage/nia.png",
		UpdatedAt:         past,
	}
	if err := repo.CreateCharacter(ctx, char); err != nil {
		t.Fatal(err)
	}
	charApproval := &domain.StageApproval{
		ProjectID:  project.ID,
		EntityType: "character_reference",
		EntityID:   char.ID,
		ApprovedAt: approvedAt,
	}
	if err := repo.RecordApproval(ctx, charApproval); err != nil {
		t.Fatal(err)
	}

	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "Chapter 1", UpdatedAt: past}
	if err := repo.CreateChapter(ctx, chapter); err != nil {
		t.Fatal(err)
	}
	chapterApproval := &domain.StageApproval{
		ProjectID:  project.ID,
		EntityType: "chapter_outline",
		EntityID:   chapter.ID,
		ApprovedAt: approvedAt,
	}
	if err := repo.RecordApproval(ctx, chapterApproval); err != nil {
		t.Fatal(err)
	}

	panel := &domain.Panel{
		ChapterID:        chapter.ID,
		PanelNumber:      1,
		SceneDescription: "Storm over the lighthouse",
	}
	if err := repo.CreatePanel(ctx, panel); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(-10 * time.Minute)
	page := &domain.Page{
		ChapterID:       chapter.ID,
		PageNumber:      1,
		PanelIDs:        []string{panel.ID},
		DraftImageURL:   "http://storage/draft.png",
		DraftApprovedAt: &now,
		UpdatedAt:       now.Add(-time.Microsecond),
	}
	if err := repo.CreatePage(ctx, page); err != nil {
		t.Fatal(err)
	}

	server := api.NewServer(
		repo,
		credits.NewInMemoryCreditService(map[string]float64{"owner": 50}),
		jobs.NewInMemoryJobService(),
		gen.NewFakeAIServiceClient(),
		auth.NewDevAuthenticator(true, "owner"),
		auth.NewDefaultImageHosting("/media"),
		4,
	)
	return server, server.NewRouter(), repo, project.ID, page.ID
}

func TestGeneratePaintedPage_VariantsAndGates(t *testing.T) {
	server, handler, repo, _, pageID := paintedPageFixture(t)
	fake := server.AIClient.(*gen.FakeAIServiceClient)

	var lastDraftURL *string
	var lastVariant gen.GeneratePaintedPageRequestVariant
	fake.GeneratePaintedPageFunc = func(ctx context.Context, req gen.GeneratePaintedPageRequest) (*gen.GeneratePaintedPageResponse, error) {
		lastDraftURL = req.DraftImageURL
		lastVariant = req.Variant
		variantStr := string(req.Variant)
		return &gen.GeneratePaintedPageResponse{
			JobID:              req.JobID,
			ImageURL:           "http://storage/pages/painted-" + variantStr + ".png",
			ModelID:            "muse-image-1.0",
			Variant:            gen.GeneratePaintedPageResponseVariant(variantStr),
			ProvenanceMetadata: map[string]interface{}{"c2pa": true},
		}, nil
	}

	// 1. Draft-conditioned variant with approved draft succeeds
	body, _ := json.Marshal(map[string]string{"variant": "draft-conditioned"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/painted-page", bytes.NewReader(body))
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("draft-conditioned generation returned %d: %s", rec.Code, rec.Body.String())
	}
	if lastDraftURL == nil || *lastDraftURL != "http://storage/draft.png" {
		t.Fatalf("expected draft URL attached, got: %v", lastDraftURL)
	}
	if lastVariant != gen.GeneratePaintedPageRequestVariantDraftConditioned {
		t.Fatalf("expected draft-conditioned variant, got: %v", lastVariant)
	}

	// 2. Draft-conditioned variant fails if draft approval is missing/stale
	pg, _ := repo.GetPage(context.Background(), pageID)
	pg.DraftApprovedAt = nil
	_ = repo.UpdatePage(context.Background(), pg)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/painted-page", bytes.NewReader(body))
	req.Header.Set("X-User-ID", "owner")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("draft-conditioned without approved draft should fail, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Direct-from-brief variant succeeds even without draft approval
	bodyDirect, _ := json.Marshal(map[string]string{"variant": "direct-from-brief"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/painted-page", bytes.NewReader(bodyDirect))
	req.Header.Set("X-User-ID", "owner")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("direct-from-brief generation should succeed without draft approval, got %d: %s", rec.Code, rec.Body.String())
	}
	if lastDraftURL != nil {
		t.Fatalf("direct-from-brief should omit draft URL, got: %v", *lastDraftURL)
	}
	if lastVariant != gen.GeneratePaintedPageRequestVariantDirectFromBrief {
		t.Fatalf("expected direct-from-brief variant, got: %v", lastVariant)
	}
}

func TestApprovePaintedPage_AndExportChoosesApprovedFinish(t *testing.T) {
	server, handler, repo, projectID, pageID := paintedPageFixture(t)
	ctx := context.Background()

	// Set up page with both line art and painted page
	now := time.Now().UTC()
	lineApprovedAt := now.Add(-5 * time.Minute)
	pg, _ := repo.GetPage(ctx, pageID)
	pg.LineArtImageURL = "http://storage/line-art.png"
	pg.LineArtApprovedAt = &lineApprovedAt
	pg.PaintedPageImageURL = "http://storage/painted-page.png"
	pg.PaintedPageVariant = "draft-conditioned"
	_ = repo.UpdatePage(ctx, pg)
	_ = repo.RecordApproval(ctx, &domain.StageApproval{
		ProjectID:  projectID,
		EntityType: "line_art_page",
		EntityID:   pageID,
		ApprovedAt: lineApprovedAt,
	})

	seedProvenance(t, repo, projectID, "http://storage/line-art.png", "http://storage/painted-page.png")
	var exportedPages []gen.PageExportItem
	fake := server.AIClient.(*gen.FakeAIServiceClient)
	fake.AssemblePdfFunc = func(ctx context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		exportedPages = req.Pages
		return &gen.AssemblePdfResponse{
			JobID:                       req.JobID,
			PdfURL:                      "http://storage/comic.pdf",
			PageCount:                   len(req.Pages),
			ProvenanceMetadataPreserved: true,
		}, nil
	}

	// Before painted page approval: line art is the latest approved finish
	reqExport := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/export", nil)
	reqExport.Header.Set("X-User-ID", "owner")
	recExport := httptest.NewRecorder()
	handler.ServeHTTP(recExport, reqExport)
	if recExport.Code != http.StatusAccepted {
		t.Fatalf("export returned %d: %s", recExport.Code, recExport.Body.String())
	}
	if len(exportedPages) != 1 || exportedPages[0].ImageURL != "http://storage/line-art.png" {
		t.Fatalf("expected export to choose approved line art, got: %+v", exportedPages)
	}

	// Approve painted page
	reqApprove := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/painted-page/approve", nil)
	reqApprove.Header.Set("X-User-ID", "owner")
	recApprove := httptest.NewRecorder()
	handler.ServeHTTP(recApprove, reqApprove)
	if recApprove.Code != http.StatusOK {
		t.Fatalf("approve painted page returned %d: %s", recApprove.Code, recApprove.Body.String())
	}

	// After painted page approval: export chooses painted page as newest approved finish
	recExport = httptest.NewRecorder()
	handler.ServeHTTP(recExport, reqExport)
	if recExport.Code != http.StatusAccepted {
		t.Fatalf("export after painted approval returned %d: %s", recExport.Code, recExport.Body.String())
	}
	if len(exportedPages) != 1 || exportedPages[0].ImageURL != "http://storage/painted-page.png" {
		t.Fatalf("expected export to choose approved painted page, got: %+v", exportedPages)
	}
	if exportedPages[0].OriginalImageURL == nil || *exportedPages[0].OriginalImageURL != "http://storage/draft.png" {
		t.Fatalf("expected draft composition preserved as original attachment, got: %v", exportedPages[0].OriginalImageURL)
	}
}

func TestLineArtEndpoint_DefaultsToDirectFromBriefPaintedFinish(t *testing.T) {
	server, handler, _, _, pageID := paintedPageFixture(t)
	fake := server.AIClient.(*gen.FakeAIServiceClient)

	calledPainted := false
	var requestedVariant gen.GeneratePaintedPageRequestVariant
	fake.GeneratePaintedPageFunc = func(ctx context.Context, req gen.GeneratePaintedPageRequest) (*gen.GeneratePaintedPageResponse, error) {
		calledPainted = true
		requestedVariant = req.Variant
		return &gen.GeneratePaintedPageResponse{
			JobID:              req.JobID,
			ImageURL:           "http://storage/painted.png",
			ModelID:            "muse-image-1.0",
			Variant:            gen.GeneratePaintedPageResponseVariantDirectFromBrief,
			ProvenanceMetadata: map[string]interface{}{"c2pa": true},
		}, nil
	}

	// The ordinary finish endpoint creates the default painted page.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art", nil)
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("default line-art route returned %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Job struct {
			Status string `json:"status"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Job.Status != "done" {
		t.Fatalf("painted default returned job status %q, want done", response.Job.Status)
	}
	if !calledPainted {
		t.Fatal("expected GeneratePaintedPage to be called by the default finish route")
	}
	if requestedVariant != gen.GeneratePaintedPageRequestVariantDirectFromBrief {
		t.Fatalf("default finish variant = %q, want direct-from-brief", requestedVariant)
	}
}

func TestPaintedPageRegeneratesOverlayLayoutWithStoredArtURL(t *testing.T) {
	server, handler, repo, _, pageID := paintedPageFixture(t)
	ctx := context.Background()
	legacy, _ := repo.GetPage(ctx, pageID)
	server.LetteringOverlayEnabled = true
	pg := &domain.Page{
		ChapterID:     legacy.ChapterID,
		PageNumber:    2,
		PanelIDs:      legacy.PanelIDs,
		LetteringMode: "overlay",
	}
	if err := repo.CreatePage(ctx, pg); err != nil {
		t.Fatal(err)
	}
	pageID = pg.ID
	panel, _ := repo.GetPanel(ctx, pg.PanelIDs[0])
	panel.Dialogue = "Nia: Follow me!"
	if err := repo.UpdatePanel(ctx, panel); err != nil {
		t.Fatal(err)
	}

	fake := server.AIClient.(*gen.FakeAIServiceClient)
	generated := false
	fake.GeneratePaintedPageFunc = func(_ context.Context, req gen.GeneratePaintedPageRequest) (*gen.GeneratePaintedPageResponse, error) {
		generated = true
		return &gen.GeneratePaintedPageResponse{
			JobID: req.JobID, ImageURL: "http://storage/painted.png", Variant: gen.GeneratePaintedPageResponseVariantDirectFromBrief,
		}, nil
	}
	var layoutRequest *gen.LetteringLayoutRequest
	fake.LayoutLetteringFunc = func(_ context.Context, req gen.LetteringLayoutRequest) (*gen.LetteringLayout, error) {
		layoutRequest = &req
		// Marks which layouts were computed over the painted art.
		digest := "without-art"
		if req.ArtImageURL != nil {
			digest = "with-art"
		}
		return &gen.LetteringLayout{Version: "lettering-layout-v1", PageID: req.Document.PageID, PageAspect: req.Document.PageAspect, SourceDigest: &digest, Bubbles: []gen.LetteringBubble{}, Issues: []gen.LetteringIssue{}}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art", nil)
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if !generated {
		t.Fatalf("painted page generation was skipped: %s", rec.Body.String())
	}
	if layoutRequest == nil || layoutRequest.ArtImageURL == nil || *layoutRequest.ArtImageURL != "http://storage/painted.png" {
		t.Fatalf("art-aware layout request did not receive the saved painted page URL: %+v", layoutRequest)
	}
	saved, err := repo.GetPage(ctx, pageID)
	if err != nil || len(saved.Lettering) == 0 {
		t.Fatalf("art-aware lettering state was not stored: %v", err)
	}
	// Regression: the layout computed over the painted art must be the one persisted. It once
	// stayed in memory (UpdatePage never writes lettering), so export used the art-blind layout.
	var state gen.PageLetteringState
	if err := json.Unmarshal(saved.Lettering, &state); err != nil {
		t.Fatal(err)
	}
	if state.Layout.SourceDigest == nil || *state.Layout.SourceDigest != "with-art" {
		t.Fatalf("the persisted layout was not the art-aware one: %+v", state.Layout.SourceDigest)
	}
}

func TestLineArtEndpointKeepsInkExplicit(t *testing.T) {
	server, handler, repo, _, pageID := paintedPageFixture(t)
	fake := server.AIClient.(*gen.FakeAIServiceClient)
	calledInk := false
	fake.GenerateLineArtFunc = func(ctx context.Context, req gen.GenerateLineArtRequest) (*gen.GenerateLineArtResponse, error) {
		calledInk = true
		return &gen.GenerateLineArtResponse{JobID: req.JobID, ImageURL: "http://storage/ink.png", ModelID: "muse-image-1.0"}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art?variant=ink", nil)
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || !calledInk {
		t.Fatalf("explicit ink route returned %d (called=%v): %s", rec.Code, calledInk, rec.Body.String())
	}
	page, err := repo.GetPage(context.Background(), pageID)
	if err != nil || page.LineArtImageURL != "http://storage/ink.png" || page.PaintedPageImageURL != "" {
		t.Fatalf("ink result was not saved independently: page=%+v err=%v", page, err)
	}
}

// overlayPaintedPageFixture returns an overlay page whose painted page generation answers
// with the given subject detection result, and the layout requests the Go API then makes.
func overlayPaintedPageFixture(t *testing.T, subjects *[]gen.LetteringSubject, source *gen.LetteringSubjectsSource) (http.Handler, db.Repository, string, *[]gen.LetteringLayoutRequest) {
	t.Helper()
	server, handler, repo, _, pageID := paintedPageFixture(t)
	ctx := context.Background()
	legacy, _ := repo.GetPage(ctx, pageID)
	server.LetteringOverlayEnabled = true
	pg := &domain.Page{ChapterID: legacy.ChapterID, PageNumber: 2, PanelIDs: legacy.PanelIDs, LetteringMode: "overlay"}
	if err := repo.CreatePage(ctx, pg); err != nil {
		t.Fatal(err)
	}
	panel, _ := repo.GetPanel(ctx, pg.PanelIDs[0])
	panel.Dialogue = "Nia: Follow me!"
	if err := repo.UpdatePanel(ctx, panel); err != nil {
		t.Fatal(err)
	}
	fake := server.AIClient.(*gen.FakeAIServiceClient)
	fake.GeneratePaintedPageFunc = func(_ context.Context, req gen.GeneratePaintedPageRequest) (*gen.GeneratePaintedPageResponse, error) {
		return &gen.GeneratePaintedPageResponse{
			JobID: req.JobID, ImageURL: "http://storage/painted.png", Variant: gen.GeneratePaintedPageResponseVariantDirectFromBrief,
			LetteringSubjects: subjects, LetteringSubjectsSource: source,
		}, nil
	}
	requests := []gen.LetteringLayoutRequest{}
	fake.LayoutLetteringFunc = func(_ context.Context, req gen.LetteringLayoutRequest) (*gen.LetteringLayout, error) {
		requests = append(requests, req)
		return &gen.LetteringLayout{Version: "lettering-layout-v1", PageID: req.Document.PageID, PageAspect: req.Document.PageAspect, Bubbles: []gen.LetteringBubble{}, Issues: []gen.LetteringIssue{}}, nil
	}
	return handler, repo, pg.ID, &requests
}

func paintOverlayPage(t *testing.T, handler http.Handler, pageID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art", nil)
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestPaintedPageCachesVisionSubjectsAndReusesThemForLaterLayouts(t *testing.T) {
	face := gen.LetteringSubject{Kind: gen.LetteringSubjectKind("face"), Confidence: 0.9, Character: strPtr("Nia"), Rect: gen.LetteringUnitRect{X: 0.2, Y: 0.1, Width: 0.1, Height: 0.1}}
	boxes := []gen.LetteringSubject{face}
	source := gen.Vision
	handler, repo, pageID, requests := overlayPaintedPageFixture(t, &boxes, &source)

	paintOverlayPage(t, handler, pageID)

	last := (*requests)[len(*requests)-1]
	if last.Subjects == nil || len(*last.Subjects) != 1 || (*last.Subjects)[0].Character == nil || *(*last.Subjects)[0].Character != "Nia" {
		t.Fatalf("layout after painting did not receive the vision boxes: %+v", last.Subjects)
	}
	saved, _ := repo.GetPage(context.Background(), pageID)
	var state gen.PageLetteringState
	if err := json.Unmarshal(saved.Lettering, &state); err != nil {
		t.Fatal(err)
	}
	if state.SubjectsSource == nil || *state.SubjectsSource != gen.Vision || state.Subjects == nil || len(*state.Subjects) != 1 {
		t.Fatalf("subject boxes were not cached with the stored layout: %+v", state)
	}

	// A later edit recomputes the layout from the cached boxes, without another vision call.
	panel, _ := repo.GetPanel(context.Background(), saved.PanelIDs[0])
	body := strings.NewReader(`{"text":"Follow the light!"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/pages/"+pageID+"/lettering/utterances/"+panel.ID+"-dialogue-0/text", body)
	req.Header.Set("X-User-ID", "owner")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	again := (*requests)[len(*requests)-1]
	if again.Subjects == nil || len(*again.Subjects) != 1 {
		t.Fatalf("a text edit lost the cached subject boxes: %+v", again.Subjects)
	}
}

func TestPaintedPageSubjectFallbackKeepsHeuristicsAndStillLetters(t *testing.T) {
	source := gen.Fallback
	handler, repo, pageID, requests := overlayPaintedPageFixture(t, nil, &source)

	paintOverlayPage(t, handler, pageID)

	last := (*requests)[len(*requests)-1]
	if last.Subjects != nil {
		t.Fatalf("a failed detection must not send empty boxes that disable the heuristics: %+v", last.Subjects)
	}
	saved, _ := repo.GetPage(context.Background(), pageID)
	var state gen.PageLetteringState
	if err := json.Unmarshal(saved.Lettering, &state); err != nil {
		t.Fatal(err)
	}
	if state.SubjectsSource == nil || *state.SubjectsSource != gen.Fallback || state.Subjects != nil {
		t.Fatalf("the fallback was not recorded on the stored layout: %+v", state)
	}
}

func strPtr(s string) *string { return &s }
