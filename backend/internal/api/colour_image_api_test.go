package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
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

var fixturePNG = func() string {
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLttAAAAABJRU5ErkJggg==")
	if err != nil {
		panic(err)
	}
	return string(data)
}()

func colourImageFixture(t *testing.T) (*api.Server, http.Handler, db.Repository, string) {
	t.Helper()
	repo := db.NewInMemoryRepository()
	project := &domain.Project{UserID: "owner", Title: "Colour fixture", Status: "draft"}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "Chapter 1"}
	if err := repo.CreateChapter(context.Background(), chapter); err != nil {
		t.Fatal(err)
	}
	approvedAt := time.Now().UTC().Add(-time.Minute)
	page := &domain.Page{
		ChapterID:         chapter.ID,
		PageNumber:        1,
		DraftImageURL:     "http://storage/draft.png",
		DraftApprovedAt:   &approvedAt,
		LineArtImageURL:   "http://storage/line-art.png",
		LineArtApprovedAt: &approvedAt,
		UpdatedAt:         approvedAt.Add(-time.Microsecond),
	}
	if err := repo.CreatePage(context.Background(), page); err != nil {
		t.Fatal(err)
	}
	server := api.NewServer(repo, credits.NewInMemoryCreditService(map[string]float64{"owner": 10}), jobs.NewInMemoryJobService(), gen.NewFakeAIServiceClient(), auth.NewDevAuthenticator(true, "owner"), auth.NewDefaultImageHosting("/media"), 4)
	return server, server.NewRouter(), repo, page.ID
}

func colourImageRequest(handler http.Handler, pageID, userID string, data []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pages/"+pageID+"/colour-image", bytes.NewReader(data))
	req.Header.Set("X-User-ID", userID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestUploadColourImageRejectsUnsupportedTypeTooLargeAndWrongOwner(t *testing.T) {
	server, handler, _, pageID := colourImageFixture(t)
	fake := server.AIClient.(*gen.FakeAIServiceClient)
	storeCalls := 0
	fake.StoreColourImageFunc = func(context.Context, gen.StoreColourImageRequest) (*gen.StoreColourImageResponse, error) {
		storeCalls++
		return &gen.StoreColourImageResponse{ImageURL: "http://storage/colour.png"}, nil
	}

	if rec := colourImageRequest(handler, pageID, "owner", []byte("not an image")); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported image type returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec := colourImageRequest(handler, pageID, "owner", bytes.Repeat([]byte("x"), 10*1024*1024+1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized image returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec := colourImageRequest(handler, pageID, "intruder", []byte(fixturePNG)); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong owner returned %d: %s", rec.Code, rec.Body.String())
	}
	if storeCalls != 0 {
		t.Fatalf("invalid uploads reached storage %d times", storeCalls)
	}
}

func TestUploadColourImagePreservesLineArtAndExportUsesApprovedColour(t *testing.T) {
	server, handler, repo, pageID := colourImageFixture(t)
	fake := server.AIClient.(*gen.FakeAIServiceClient)
	fake.StoreColourImageFunc = func(_ context.Context, req gen.StoreColourImageRequest) (*gen.StoreColourImageResponse, error) {
		if req.PageID != pageID || req.MediaType != gen.ImagePng || req.ImageBase64 == "" {
			t.Fatalf("unexpected image store request: %+v", req)
		}
		return &gen.StoreColourImageResponse{ImageURL: "http://storage/colour-page.png"}, nil
	}
	approveBeforeUpload := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/colour/approve", nil)
	approveBeforeUpload.Header.Set("X-User-ID", "owner")
	approveBeforeUploadRec := httptest.NewRecorder()
	handler.ServeHTTP(approveBeforeUploadRec, approveBeforeUpload)
	if approveBeforeUploadRec.Code != http.StatusBadRequest {
		t.Fatalf("approval before upload returned %d: %s", approveBeforeUploadRec.Code, approveBeforeUploadRec.Body.String())
	}

	if rec := colourImageRequest(handler, pageID, "owner", []byte(fixturePNG)); rec.Code != http.StatusOK {
		t.Fatalf("colour image upload returned %d: %s", rec.Code, rec.Body.String())
	}
	page, err := repo.GetPage(context.Background(), pageID)
	if err != nil {
		t.Fatal(err)
	}
	if page.LineArtImageURL != "http://storage/line-art.png" || page.ColourImageURL != "http://storage/colour-page.png" {
		t.Fatalf("upload did not preserve both image URLs: %+v", page)
	}
	if page.ColourImageUploadedAt == nil || !page.ColourImageUploadedAt.After(*page.LineArtApprovedAt) {
		t.Fatal("upload timestamp was not recorded after line art approval")
	}
	seedProvenance(t, repo, pageProjectID(t, repo, pageID), "http://storage/line-art.png")
	var exportedImageURL string
	fake.AssemblePdfFunc = func(_ context.Context, req gen.AssemblePdfRequest) (*gen.AssemblePdfResponse, error) {
		if len(req.Pages) != 1 {
			t.Fatalf("expected one exported page, got %d", len(req.Pages))
		}
		if req.Pages[0].ImageURL == "http://storage/colour-page.png" && (req.Pages[0].OriginalImageURL == nil || *req.Pages[0].OriginalImageURL != "http://storage/line-art.png") {
			t.Fatalf("coloured export did not retain original line art URL: %+v", req.Pages[0])
		}
		exportedImageURL = req.Pages[0].ImageURL
		return &gen.AssemblePdfResponse{JobID: req.JobID, PdfURL: "http://storage/export.pdf", ProvenanceMetadataPreserved: true, PageCount: 2}, nil
	}
	exportProject := func() string {
		export := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+pageProjectID(t, repo, pageID)+"/export", nil)
		export.Header.Set("X-User-ID", "owner")
		exportRec := httptest.NewRecorder()
		handler.ServeHTTP(exportRec, export)
		if exportRec.Code != http.StatusAccepted {
			t.Fatalf("export returned %d: %s", exportRec.Code, exportRec.Body.String())
		}
		return exportedImageURL
	}
	if imageURL := exportProject(); imageURL != "http://storage/line-art.png" {
		t.Fatalf("unapproved coloured page was exported as %q", imageURL)
	}

	approve := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/colour/approve", nil)
	approve.Header.Set("X-User-ID", "owner")
	approveRec := httptest.NewRecorder()
	handler.ServeHTTP(approveRec, approve)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("colour approval returned %d: %s", approveRec.Code, approveRec.Body.String())
	}

	if imageURL := exportProject(); imageURL != "http://storage/colour-page.png" {
		t.Fatalf("export used %q instead of approved coloured page", imageURL)
	}
	var approvalResponse map[string]interface{}
	if err := json.Unmarshal(approveRec.Body.Bytes(), &approvalResponse); err != nil || approvalResponse["status"] != "approved" {
		t.Fatalf("unexpected approval response: %s", approveRec.Body.String())
	}

	regenerate := httptest.NewRequest(http.MethodPost, "/api/v1/pages/"+pageID+"/line-art?variant=ink", nil)
	regenerate.Header.Set("X-User-ID", "owner")
	regenerateRec := httptest.NewRecorder()
	handler.ServeHTTP(regenerateRec, regenerate)
	if regenerateRec.Code != http.StatusAccepted {
		t.Fatalf("line art regeneration returned %d: %s", regenerateRec.Code, regenerateRec.Body.String())
	}
	page, err = repo.GetPage(context.Background(), pageID)
	if err != nil {
		t.Fatal(err)
	}
	if page.ColourImageURL != "" || page.ColourImageUploadedAt != nil {
		t.Fatalf("line art regeneration retained stale coloured image: %+v", page)
	}
}

func pageProjectID(t *testing.T, repo db.Repository, pageID string) string {
	t.Helper()
	page, err := repo.GetPage(context.Background(), pageID)
	if err != nil {
		t.Fatal(err)
	}
	chapter, err := repo.GetChapter(context.Background(), page.ChapterID)
	if err != nil {
		t.Fatal(err)
	}
	return chapter.ProjectID
}

func TestUploadRejectsImagePixelBombBeforeStorage(t *testing.T) {
	server, handler, _, pageID := colourImageFixture(t)
	calls := 0
	server.AIClient.(*gen.FakeAIServiceClient).StoreColourImageFunc = func(context.Context, gen.StoreColourImageRequest) (*gen.StoreColourImageResponse, error) {
		calls++
		return nil, fmt.Errorf("must not store")
	}
	// Change only IHDR dimensions and checksum, without allocating image pixels.
	bomb := []byte(fixturePNG)
	binary.BigEndian.PutUint32(bomb[16:20], 100000)
	binary.BigEndian.PutUint32(bomb[20:24], 100000)
	binary.BigEndian.PutUint32(bomb[29:33], crc32.ChecksumIEEE(bomb[12:29]))
	rec := colourImageRequest(handler, pageID, "owner", bomb)
	if rec.Code != 413 || calls != 0 {
		t.Fatalf("pixel bomb: HTTP %d calls %d %s", rec.Code, calls, rec.Body.String())
	}
	rec = colourImageRequest(handler, pageID, "owner", []byte("\x89PNG\r\n\x1a\ncorrupt"))
	if rec.Code != 400 || calls != 0 {
		t.Fatalf("corrupt image: HTTP %d calls %d", rec.Code, calls)
	}
}

func TestUploadWebPUsesConfiguredPixelLimit(t *testing.T) {
	server, handler, _, pageID := colourImageFixture(t)
	// A locally encoded, lossless 2x3 white WebP fixture.
	data, err := base64.StdEncoding.DecodeString("UklGRh4AAABXRUJQVlA4TBEAAAAvAYAAAAfQ//73v/+BiOh/AAA=")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server.AIClient.(*gen.FakeAIServiceClient).StoreColourImageFunc = func(_ context.Context, req gen.StoreColourImageRequest) (*gen.StoreColourImageResponse, error) {
		calls++
		if req.MediaType != gen.ImageWebp {
			t.Fatalf("unexpected media type %s", req.MediaType)
		}
		return &gen.StoreColourImageResponse{ImageURL: "http://storage/colour.webp"}, nil
	}
	server.Limits.ImagePixels = 6
	if rec := colourImageRequest(handler, pageID, "owner", data); rec.Code != 200 {
		t.Fatalf("valid WebP: %d %s", rec.Code, rec.Body.String())
	}
	server.Limits.ImagePixels = 5
	if rec := colourImageRequest(handler, pageID, "owner", data); rec.Code != 413 {
		t.Fatalf("oversized WebP: %d %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("storage calls %d, want 1", calls)
	}
}
