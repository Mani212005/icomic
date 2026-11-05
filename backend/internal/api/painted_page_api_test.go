package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
