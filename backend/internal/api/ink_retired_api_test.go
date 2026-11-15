package api_test

import (
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

// legacyInkFixture is a page that went through the retired Ink route: approved draft and line art,
// an uploaded colour image and (when approveColour is set) an approved colour. No painted page.
func legacyInkFixture(t *testing.T, approveColour bool) (*api.Server, http.Handler, db.Repository, string, string) {
	t.Helper()
	repo := db.NewInMemoryRepository()
	ctx := context.Background()
	project := &domain.Project{UserID: "owner", Title: "Legacy ink", Status: "draft"}
	if err := repo.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "Chapter 1"}
	if err := repo.CreateChapter(ctx, chapter); err != nil {
		t.Fatal(err)
	}
	lineArtApproved := time.Now().UTC().Add(-3 * time.Minute)
	uploaded := lineArtApproved.Add(time.Minute)
	page := &domain.Page{
		ChapterID:             chapter.ID,
		PageNumber:            1,
		DraftImageURL:         "http://storage/draft.png",
		DraftApprovedAt:       &lineArtApproved,
		LineArtImageURL:       "http://storage/line-art.png",
		LineArtApprovedAt:     &lineArtApproved,
		ColourImageURL:        "http://storage/colour-page.png",
		ColourImageUploadedAt: &uploaded,
		UpdatedAt:             lineArtApproved.Add(-time.Microsecond),
	}
	if err := repo.CreatePage(ctx, page); err != nil {
		t.Fatal(err)
	}
	if approveColour {
		if err := repo.RecordApproval(ctx, &domain.StageApproval{
			ProjectID: project.ID, EntityType: "colour_page", EntityID: page.ID, ApprovedByUserID: "owner",
			ApprovedAt: uploaded.Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	server := api.NewServer(repo, credits.NewInMemoryCreditService(map[string]float64{"owner": 10}), jobs.NewInMemoryJobService(), gen.NewFakeAIServiceClient(), auth.NewDevAuthenticator(true, "owner"), auth.NewDefaultImageHosting("/media"), 4)
	return server, server.NewRouter(), repo, project.ID, page.ID
}

func ownerRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRetiredInkRoutesAnswerNotFound(t *testing.T) {
	_, handler, _, _, pageID := legacyInkFixture(t, true)
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/colour-palette", ""},
		{http.MethodPatch, "/colour-palette", `{"palette":{"sky":"#123456"}}`},
		{http.MethodPost, "/colour-palette/suggest", ""},
		{http.MethodPut, "/colour-image", "png bytes"},
		{http.MethodPost, "/colour/approve", ""},
	} {
		rec := ownerRequest(handler, route.method, "/api/v1/pages/"+pageID+route.path, route.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s returned %d, want the normal 404: %s", route.method, route.path, rec.Code, rec.Body.String())
		}
	}
}

func TestPageFinishedWithInkStillListsAndExports(t *testing.T) {
	server, handler, repo, projectID, pageID := legacyInkFixture(t, false)
	fake := server.AIClient.(*gen.FakeAIServiceClient)
	seedProvenance(t, repo, projectID, "http://storage/line-art.png")
	var exportedImageURL string
	var exportedOriginal *string
	fake.AssemblePdfFunc = func(_ context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		if len(req.Pages) != 1 {
			t.Fatalf("expected one exported page, got %d", len(req.Pages))
		}
		exportedImageURL, exportedOriginal = req.Pages[0].ImageURL, req.Pages[0].OriginalImageURL
		return &gen.AssemblePdfResponse{JobID: req.JobID, PdfURL: "http://storage/export.pdf", ProvenanceMetadataPreserved: true, PageCount: 2}, nil
	}
	export := func() string {
		rec := ownerRequest(handler, http.MethodPost, "/api/v1/projects/"+projectID+"/export", "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("export returned %d: %s", rec.Code, rec.Body.String())
		}
		return exportedImageURL
	}

	// The coloured image was never approved, so the export keeps the approved line art.
	if url := export(); url != "http://storage/line-art.png" {
		t.Fatalf("an unapproved coloured page was exported as %q", url)
	}

	// An approved colour is still exported, with the line art it was coloured from attached.
	if err := repo.RecordApproval(context.Background(), &domain.StageApproval{
		ProjectID: projectID, EntityType: "colour_page", EntityID: pageID, ApprovedByUserID: "owner",
		ApprovedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if url := export(); url != "http://storage/colour-page.png" {
		t.Fatalf("export used %q instead of the approved coloured page", url)
	}
	if exportedOriginal == nil || *exportedOriginal != "http://storage/line-art.png" {
		t.Fatalf("coloured export did not keep the original line art: %v", exportedOriginal)
	}

	// Listing the chapter's pages still reports the colour approval and both images.
	chapter, err := repo.GetPage(context.Background(), pageID)
	if err != nil {
		t.Fatal(err)
	}
	rec := ownerRequest(handler, http.MethodGet, "/api/v1/chapters/"+chapter.ChapterID+"/pages", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("page list returned %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Pages []domain.Page `json:"pages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Pages) != 1 {
		t.Fatalf("unexpected page list: %s (%v)", rec.Body.String(), err)
	}
	page := body.Pages[0]
	if page.ColourApprovedAt == nil || !strings.Contains(page.ColourImageURL, "colour-page.png") || !strings.Contains(page.LineArtImageURL, "line-art.png") {
		t.Fatalf("a page finished with Ink lost its colour data: %+v", page)
	}
}

func TestLegacyInkPageStillPreviewsThroughTheFinishRoute(t *testing.T) {
	server, handler, repo, _, pageID := legacyInkFixture(t, true)
	project, err := repo.ListProjects(context.Background(), "owner")
	if err != nil || len(project) != 1 {
		t.Fatalf("projects: %v %v", project, err)
	}
	project[0].FinishPreset, project[0].FinishPresetExplicit = "print", true
	if err := repo.UpdateProject(context.Background(), project[0]); err != nil {
		t.Fatal(err)
	}
	var source string
	server.AIClient.(*gen.FakeAIServiceClient).FinishPageImageFunc = func(_ context.Context, req gen.FinishPageImageRequest) (*gen.FinishPageImageResponse, error) {
		source = req.ImageURL
		return &gen.FinishPageImageResponse{ImageURL: "finishes/page.jpg"}, nil
	}
	rec := ownerRequest(handler, http.MethodPost, "/api/v1/pages/"+pageID+"/finish?variant=painted", "")
	if rec.Code != http.StatusOK || source != "http://storage/line-art.png" {
		t.Fatalf("finish preview of an Ink page returned %d from %q: %s", rec.Code, source, rec.Body.String())
	}
}

// A job committed before the Ink route was removed can still be waiting in the dispatch outbox. It
// must never reach a provider and must give the user's money back.
func TestEnvelopeOfRetiredInkOperationIsFailedAndRefunded(t *testing.T) {
	for _, retired := range []struct {
		operation, jobType, stage string
		debit                     func(*api.Server) float64
	}{
		{"generate-line-art", "final_line_art", "line_art", func(s *api.Server) float64 { return s.Pricing.ImagePriceUSD }},
		{"suggest-colour-palette", "colour_suggestion", "colour", func(s *api.Server) float64 { return s.Pricing.ColourEstimateUSD }},
	} {
		t.Run(retired.operation, func(t *testing.T) {
			server, _, _, projectID, pageID := legacyInkFixture(t, false)
			ctx := context.Background()
			debit := retired.debit(server)
			fake := server.AIClient.(*gen.FakeAIServiceClient)
			fake.GeneratePaintedPageFunc = func(context.Context, gen.GeneratePaintedPageRequest) (*gen.GeneratePaintedPageResponse, error) {
				t.Error("a retired Ink envelope reached the painted provider")
				return nil, nil
			}
			before, _ := server.CreditService.GetBalance(ctx, "owner")
			job, _, err := server.JobService.CreateJob(ctx, jobs.CreateJobRequest{
				EntityID: pageID, DebitAmount: debit, ProjectID: projectID, UserID: "owner",
				JobType: retired.jobType, Stage: retired.stage, IdempotencyKey: "retired-" + retired.operation,
				EstimatedCost: debit, HashedUserID: domain.HashUserID("owner"),
				Envelope: &db.GenerationEnvelope{Operation: retired.operation, Request: []byte(`{"page_id":"` + pageID + `"}`)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if debited, _ := server.CreditService.GetBalance(ctx, "owner"); debited >= before {
				t.Fatalf("the committed job did not debit: %v -> %v", before, debited)
			}
			if _, err := server.DispatchPending(ctx, true); err != nil {
				t.Fatal(err)
			}
			settled, err := server.JobService.GetJob(ctx, job.ID)
			if err != nil || settled.Status != jobs.StatusFailed {
				t.Fatalf("a retired Ink envelope must fail its job, got %+v err=%v", settled, err)
			}
			if after, _ := server.CreditService.GetBalance(ctx, "owner"); after != before {
				t.Fatalf("a retired Ink envelope was not refunded: balance %v, was %v", after, before)
			}
		})
	}
}
