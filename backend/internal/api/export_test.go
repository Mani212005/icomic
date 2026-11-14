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
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

// seedProvenance records the provider provenance generation would have kept for each image.
func seedProvenance(t *testing.T, repo db.Repository, projectID string, urls ...string) {
	t.Helper()
	for _, u := range urls {
		if err := repo.SaveArtifactProvenance(context.Background(), projectID, u, map[string]interface{}{"source_hash": "hash-of-" + u}); err != nil {
			t.Fatal(err)
		}
	}
}

func postExport(handler http.Handler, projectID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/export", strings.NewReader(body))
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// approvedPaintedProject returns a one-page project whose painted page is approved and current.
func approvedPaintedProject(t *testing.T) (*gen.FakeAIServiceClient, http.Handler, db.Repository, string, string) {
	t.Helper()
	server, handler, repo, projectID, pageID := paintedPageFixture(t)
	ctx := context.Background()
	approvedAt := time.Now().UTC().Add(time.Minute)
	pg, err := repo.GetPage(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	pg.LineArtImageURL = ""
	pg.PaintedPageImageURL = "http://storage/painted-page.png"
	pg.PaintedPageApprovedAt = &approvedAt
	if err := repo.UpdatePage(ctx, pg); err != nil {
		t.Fatal(err)
	}
	seedProvenance(t, repo, projectID, "http://storage/painted-page.png")
	return server.AIClient.(*gen.FakeAIServiceClient), handler, repo, projectID, pageID
}

func TestExportRefusesProjectsThatCannotBeProvenReady(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, repo db.Repository, projectID, pageID string)
		code   string
		want   string
	}{
		{"no approved artwork", func(t *testing.T, repo db.Repository, _, pageID string) {
			pg, _ := repo.GetPage(context.Background(), pageID)
			pg.PaintedPageApprovedAt = nil
			if err := repo.UpdatePage(context.Background(), pg); err != nil {
				t.Fatal(err)
			}
		}, "missing_approved_art", "Page 1 has no approved, current artwork"},
		{"stale approval", func(t *testing.T, repo db.Repository, _, pageID string) {
			pg, _ := repo.GetPage(context.Background(), pageID)
			past := time.Now().UTC().Add(-48 * time.Hour)
			pg.PaintedPageApprovedAt = &past
			if err := repo.UpdatePage(context.Background(), pg); err != nil {
				t.Fatal(err)
			}
		}, "missing_approved_art", "Page 1 has no approved, current artwork"},
		{"no recorded provenance", func(t *testing.T, repo db.Repository, _, pageID string) {
			pg, _ := repo.GetPage(context.Background(), pageID)
			pg.PaintedPageImageURL = "http://storage/legacy-painted.png"
			if err := repo.UpdatePage(context.Background(), pg); err != nil {
				t.Fatal(err)
			}
		}, "missing_provenance", "no recorded Meta provenance"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake, handler, repo, projectID, pageID := approvedPaintedProject(t)
			assembled := false
			fake.AssemblePdfFunc = func(context.Context, gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
				assembled = true
				return &gen.AssemblePdfResponse{PdfURL: "http://storage/never.pdf", ProvenanceMetadataPreserved: true}, nil
			}
			tc.mutate(t, repo, projectID, pageID)
			rec := postExport(handler, projectID, "")
			if rec.Code != http.StatusConflict {
				t.Fatalf("export returned %d: %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details struct {
					Problems []struct {
						Code       string `json:"code"`
						PageNumber int    `json:"page_number"`
					} `json:"problems"`
				} `json:"details"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "EXPORT_NOT_READY" || !strings.Contains(body.Message, tc.want) || len(body.Details.Problems) != 1 || body.Details.Problems[0].Code != tc.code || body.Details.Problems[0].PageNumber != 1 {
				t.Fatalf("unexpected refusal: %s", rec.Body.String())
			}
			if assembled {
				t.Fatal("an unready project reached PDF assembly")
			}
		})
	}
}

func TestExportOfAProjectWithoutPagesIsRefusedNotPlaceholderArt(t *testing.T) {
	_, handler, repo := setupTestServer(true, "owner")
	project := &domain.Project{UserID: "owner", Title: "Empty", Status: "draft"}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	rec := postExport(handler, project.ID, "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"EXPORT_NOT_READY"`) || !strings.Contains(rec.Body.String(), "no pages") {
		t.Fatalf("empty project: %d %s", rec.Code, rec.Body.String())
	}
}

func TestExportForwardsStoredProvenanceNotAFabricatedMarker(t *testing.T) {
	fake, handler, repo, projectID, pageID := approvedPaintedProject(t)
	pg, _ := repo.GetPage(context.Background(), pageID)
	pg.PaintedPageImageURL = "http://storage/recorded-painted.png"
	if err := repo.UpdatePage(context.Background(), pg); err != nil {
		t.Fatal(err)
	}
	stored := map[string]interface{}{"source_hash": "abc", "provider_raw": map[string]interface{}{"vendor": "kept"}}
	if err := repo.SaveArtifactProvenance(context.Background(), projectID, "http://storage/recorded-painted.png", stored); err != nil {
		t.Fatal(err)
	}
	var sent gen.PageExportItem
	fake.AssemblePdfFunc = func(_ context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		sent = req.Pages[0]
		return &gen.AssemblePdfResponse{PdfURL: "http://storage/comic.pdf", ProvenanceMetadataPreserved: true}, nil
	}
	if rec := postExport(handler, projectID, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("export returned %d: %s", rec.Code, rec.Body.String())
	}
	if sent.ProvenanceMetadata == nil {
		t.Fatal("no provenance forwarded")
	}
	if _, fabricated := (*sent.ProvenanceMetadata)["c2pa"]; fabricated {
		t.Fatalf("a c2pa marker was invented: %v", *sent.ProvenanceMetadata)
	}
	raw := (*sent.ProvenanceMetadata)["provider_raw"].(map[string]interface{})
	if (*sent.ProvenanceMetadata)["source_hash"] != "abc" || raw["vendor"] != "kept" {
		t.Fatalf("stored provenance was altered: %v", *sent.ProvenanceMetadata)
	}
}

func TestExportThatCannotPreserveProvenanceIsNotCompleted(t *testing.T) {
	fake, handler, repo, projectID, _ := approvedPaintedProject(t)
	fake.AssemblePdfFunc = func(_ context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		return &gen.AssemblePdfResponse{JobID: req.JobID, PdfURL: "http://storage/comic.pdf", ProvenanceMetadataPreserved: false}, nil
	}
	rec := postExport(handler, projectID, "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"EXPORT_PROVENANCE_NOT_PRESERVED"`) {
		t.Fatalf("unpreserved export: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"export"`) {
		t.Fatalf("an export record was returned: %s", rec.Body.String())
	}
	_ = repo
}

func TestExportHonorsSelectedFormatAndPageSize(t *testing.T) {
	cases := []struct {
		body   string
		size   gen.AssemblePdfRequestPageSize
		format gen.AssemblePdfRequestFormat
		stored string
	}{
		{``, gen.A4, gen.Pdf, "pdf"},
		{`{}`, gen.A4, gen.Pdf, "pdf"},
		{`{"format":"pdf","page_size":"a4"}`, gen.A4, gen.Pdf, "pdf"},
		{`{"format":"pdf","page_size":"us_letter"}`, gen.USLetter, gen.Pdf, "pdf"},
		{`{"format":"pdf","page_size":"US_Letter"}`, gen.USLetter, gen.Pdf, "pdf"},
		{`{"format":"pdf","page_size":"custom"}`, gen.CustomComic, gen.Pdf, "pdf"},
		{`{"format":"png","page_size":"a4"}`, gen.A4, gen.PngZip, "png_zip"},
		{`{"format":"png_zip"}`, gen.A4, gen.PngZip, "png_zip"},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			fake, handler, repo, projectID, _ := approvedPaintedProject(t)
			var sent gen.AssemblePdfRequest
			fake.AssemblePdfFunc = func(_ context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
				sent = req
				return &gen.AssemblePdfResponse{JobID: req.JobID, PdfURL: "http://storage/out", ProvenanceMetadataPreserved: true}, nil
			}
			rec := postExport(handler, projectID, tc.body)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("export returned %d: %s", rec.Code, rec.Body.String())
			}
			if sent.PageSize != tc.size || sent.Format == nil || *sent.Format != tc.format {
				t.Fatalf("selection not forwarded: size %q format %v", sent.PageSize, sent.Format)
			}
			var body struct {
				Export domain.Export `json:"export"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Export.Format != tc.stored {
				t.Fatalf("stored format %q, want %q", body.Export.Format, tc.stored)
			}
			saved, err := repo.GetExport(context.Background(), body.Export.ID)
			if err != nil || saved.Format != tc.stored {
				t.Fatalf("persisted export: %+v %v", saved, err)
			}
		})
	}
}

func TestExportRejectsUnsupportedSelections(t *testing.T) {
	fake, handler, _, projectID, _ := approvedPaintedProject(t)
	assembled := false
	fake.AssemblePdfFunc = func(context.Context, gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		assembled = true
		return nil, nil
	}
	for _, body := range []string{`{"page_size":"tabloid"}`, `{"format":"gif"}`, `{"page_size":"a4,us_letter"}`, `{"format":`} {
		if rec := postExport(handler, projectID, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if assembled {
		t.Fatal("an invalid selection reached assembly")
	}
}

func TestPNGExportForwardsStoredOverlayLetteringLikeThePDF(t *testing.T) {
	for _, format := range []string{"pdf", "png"} {
		t.Run(format, func(t *testing.T) {
			fake, handler, repo, projectID, pageID := approvedPaintedProject(t)
			pg, _ := repo.GetPage(context.Background(), pageID)
			pg.LetteringMode = "overlay"
			pg.Lettering = json.RawMessage(`{"version":"page-lettering-v1","input":{"version":"lettering-v1","art_mode":"text_free","page_id":"stored","page_aspect":0.6666666666666666,"panels":[]},"overrides":{},"zone_overrides":{},"layout":{"version":"lettering-layout-v1","page_id":"stored","bubbles":[],"issues":[]}}`)
			if err := repo.CreatePage(context.Background(), pg); err != nil {
				t.Fatal(err)
			}
			var sent gen.AssemblePdfRequest
			fake.AssemblePdfFunc = func(_ context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
				sent = req
				warnings := []gen.PdfLetteringWarning{{PageNumber: 1, Code: gen.StaleLettering, Message: "Review stale lettering"}}
				return &gen.AssemblePdfResponse{JobID: req.JobID, PdfURL: "http://storage/out", ProvenanceMetadataPreserved: true, Warnings: &warnings}, nil
			}
			rec := postExport(handler, projectID, `{"format":"`+format+`"}`)
			if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"code":"stale_lettering"`) {
				t.Fatalf("overlay %s export: %d %s", format, rec.Code, rec.Body.String())
			}
			if len(sent.Pages) != 1 {
				t.Fatalf("assembled %d pages", len(sent.Pages))
			}
			item := sent.Pages[0]
			if item.LetteringMode == nil || *item.LetteringMode != gen.PageExportItemLetteringModeOverlay || item.Lettering == nil || item.Lettering.Input.PageID != "stored" || item.LetteringStale == nil || !*item.LetteringStale {
				t.Fatalf("stored layer and stale source must be forwarded: %+v", item)
			}
		})
	}
}

func TestDownloadNamesAZipExportAfterItsFormat(t *testing.T) {
	fake, handler, repo, projectID, _ := approvedPaintedProject(t)
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("PK\x03\x04archive"))
	}))
	defer storage.Close()
	fake.AssemblePdfFunc = func(_ context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		return &gen.AssemblePdfResponse{JobID: req.JobID, PdfURL: storage.URL + "/storage/fake/exports/p/comic-pages.zip", ProvenanceMetadataPreserved: true}, nil
	}
	rec := postExport(handler, projectID, `{"format":"png"}`)
	var created struct {
		Export domain.Export `json:"export"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || rec.Code != http.StatusAccepted {
		t.Fatalf("export returned %d: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/"+created.Export.ID+"/download", nil)
	req.Header.Set("X-User-ID", "owner")
	download := httptest.NewRecorder()
	handler.ServeHTTP(download, req)
	if download.Code != http.StatusOK || download.Header().Get("Content-Type") != "application/zip" ||
		!strings.HasSuffix(download.Header().Get("Content-Disposition"), `.zip"`) || !strings.HasPrefix(download.Body.String(), "PK") {
		t.Fatalf("zip download: %d %v %q", download.Code, download.Header(), download.Body.String())
	}
	_ = repo
}
