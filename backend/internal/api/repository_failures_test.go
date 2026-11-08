package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

// Reads use real in-memory state; a selected persistence operation fails as PostgreSQL would.
type failingWriteRepository struct {
	db.Repository
	operation string
	calls     int
}

type failingProjectReadRepository struct{ db.Repository }

func (r *failingProjectReadRepository) GetProject(context.Context, string) (*domain.Project, error) {
	return nil, errors.New("injected database read failure")
}

type failingPaletteReadRepository struct{ db.Repository }

func (r *failingPaletteReadRepository) GetColourPalette(context.Context, string) (*domain.ColourPalette, error) {
	return nil, errors.New("injected palette database read failure")
}

func (r *failingWriteRepository) fail(operation string) bool {
	if operation != r.operation {
		return false
	}
	r.calls++
	return true
}

func (r *failingWriteRepository) CreateCharacter(ctx context.Context, value *domain.Character) error {
	if r.fail("CreateCharacter") {
		return errors.New("injected database write failure")
	}
	return r.Repository.CreateCharacter(ctx, value)
}

func (r *failingWriteRepository) UpdateCharacter(ctx context.Context, value *domain.Character) error {
	if r.fail("UpdateCharacter") {
		return errors.New("injected database write failure")
	}
	return r.Repository.UpdateCharacter(ctx, value)
}

func (r *failingWriteRepository) UpdateProject(ctx context.Context, value *domain.Project) error {
	if r.fail("UpdateProject") {
		return errors.New("injected database write failure")
	}
	return r.Repository.UpdateProject(ctx, value)
}

func (r *failingWriteRepository) CreateChapter(ctx context.Context, value *domain.Chapter) error {
	if r.fail("CreateChapter") {
		return errors.New("injected database write failure")
	}
	return r.Repository.CreateChapter(ctx, value)
}

func (r *failingWriteRepository) UpdateChapter(ctx context.Context, value *domain.Chapter) error {
	if r.fail("UpdateChapter") {
		return errors.New("injected database write failure")
	}
	return r.Repository.UpdateChapter(ctx, value)
}

func (r *failingWriteRepository) CreatePanel(ctx context.Context, value *domain.Panel) error {
	if r.fail("CreatePanel") {
		return errors.New("injected database write failure")
	}
	return r.Repository.CreatePanel(ctx, value)
}

func (r *failingWriteRepository) UpdatePanel(ctx context.Context, value *domain.Panel) error {
	if r.fail("UpdatePanel") {
		return errors.New("injected database write failure")
	}
	return r.Repository.UpdatePanel(ctx, value)
}

func (r *failingWriteRepository) CreatePage(ctx context.Context, value *domain.Page) error {
	if r.fail("CreatePage") {
		return errors.New("injected database write failure")
	}
	return r.Repository.CreatePage(ctx, value)
}

func (r *failingWriteRepository) UpdatePage(ctx context.Context, value *domain.Page) error {
	if r.fail("UpdatePage") {
		return errors.New("injected database write failure")
	}
	return r.Repository.UpdatePage(ctx, value)
}

func (r *failingWriteRepository) ApproveStage(ctx context.Context, value *domain.StageApproval, expectedUpdatedAt time.Time) error {
	if r.fail("ApproveStage") {
		return errors.New("injected database write failure")
	}
	return r.Repository.ApproveStage(ctx, value, expectedUpdatedAt)
}

func (r *failingWriteRepository) SavePanelEdit(ctx context.Context, value *domain.Panel, now time.Time) error {
	if r.fail("SavePanelEdit") {
		return errors.New("injected database write failure")
	}
	return r.Repository.SavePanelEdit(ctx, value, now)
}

func (r *failingWriteRepository) ReassignPanels(ctx context.Context, pageID string, panelIDs []string, maxPerPage int, now time.Time) ([]string, error) {
	if r.fail("ReassignPanels") {
		return nil, errors.New("injected database write failure")
	}
	return r.Repository.ReassignPanels(ctx, pageID, panelIDs, maxPerPage, now)
}

func (r *failingWriteRepository) ReplaceChapterOutline(ctx context.Context, projectID string, chapters []*domain.Chapter) error {
	if r.fail("ReplaceChapterOutline") {
		return errors.New("injected database write failure")
	}
	return r.Repository.ReplaceChapterOutline(ctx, projectID, chapters)
}

func (r *failingWriteRepository) ReplaceChapterPanels(ctx context.Context, chapterID string, panels []*domain.Panel, pages []*domain.Page) error {
	if r.fail("ReplaceChapterPanels") {
		return errors.New("injected database write failure")
	}
	return r.Repository.ReplaceChapterPanels(ctx, chapterID, panels, pages)
}

func (r *failingWriteRepository) SaveColourPalette(ctx context.Context, value *domain.ColourPalette) error {
	if r.fail("SaveColourPalette") {
		return errors.New("injected database write failure")
	}
	return r.Repository.SaveColourPalette(ctx, value)
}

func (r *failingWriteRepository) RecordApproval(ctx context.Context, value *domain.StageApproval) error {
	if r.fail("RecordApproval") {
		return errors.New("injected database write failure")
	}
	return r.Repository.RecordApproval(ctx, value)
}

func seedWriteFailureFlow(t *testing.T, repo db.Repository) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	approvedAt := now.Add(time.Microsecond)
	for _, err := range []error{
		repo.CreateProject(nil, &domain.Project{ID: "a400e5d7-0409-5a81-9050-3991208ceec1", UserID: "test-user", OriginalScript: "A hero finds a bird", ChapterCount: 1}),
		repo.CreateCharacter(nil, &domain.Character{ID: "1bda3da7-79e9-55f5-b45e-67393fc73c26", ProjectID: "a400e5d7-0409-5a81-9050-3991208ceec1", Name: "Hero", ReferenceImageURL: "https://storage.example/reference.png", ApprovedAt: &approvedAt, UpdatedAt: now}),
		repo.CreateChapter(nil, &domain.Chapter{ID: "9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98", ProjectID: "a400e5d7-0409-5a81-9050-3991208ceec1", ChapterNumber: 1, Title: "The bird", UpdatedAt: now}),
		repo.CreatePanel(nil, &domain.Panel{ID: "a8a24397-3780-5a99-acea-8ab882a2ef5b", ChapterID: "9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98", PanelNumber: 1, SceneDescription: "A bird", UpdatedAt: now}),
		repo.CreatePage(nil, &domain.Page{ID: "a5669606-8764-5c90-b282-bad323a49baa", ChapterID: "9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98", PageNumber: 1, PanelIDs: []string{"a8a24397-3780-5a99-acea-8ab882a2ef5b"}, DraftImageURL: "https://storage.example/draft.png", LineArtImageURL: "https://storage.example/lineart.png", DraftApprovedAt: &approvedAt, LineArtApprovedAt: &approvedAt, ColourImageURL: "https://storage.example/coloured.png", ColourImageUploadedAt: &now, UpdatedAt: now}),
		repo.RecordApproval(nil, &domain.StageApproval{ProjectID: "a400e5d7-0409-5a81-9050-3991208ceec1", EntityType: "chapter_outline", EntityID: "9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98", ApprovedAt: now.Add(time.Microsecond)}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandlersSurfaceRepositoryWriteFailures(t *testing.T) {
	cases := []struct{ name, method, path, operation string }{
		{"analysis character", "POST", "/projects/a400e5d7-0409-5a81-9050-3991208ceec1/analyze", "CreateCharacter"},
		{"analysis project", "POST", "/projects/a400e5d7-0409-5a81-9050-3991208ceec1/analyze", "UpdateProject"},
		{"reference image", "POST", "/characters/1bda3da7-79e9-55f5-b45e-67393fc73c26/reference-image", "UpdateCharacter"},
		{"character approval", "POST", "/characters/1bda3da7-79e9-55f5-b45e-67393fc73c26/approve", "ApproveStage"},
		{"chapter generation", "POST", "/projects/a400e5d7-0409-5a81-9050-3991208ceec1/chapters", "ReplaceChapterOutline"},
		{"chapter edit", "PATCH", "/chapters/9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98", "UpdateChapter"},
		{"chapter approval", "POST", "/chapters/9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98/approve", "ApproveStage"},
		{"panel generation", "POST", "/chapters/9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98/panels", "ReplaceChapterPanels"},
		{"panel edit", "PATCH", "/panels/a8a24397-3780-5a99-acea-8ab882a2ef5b", "SavePanelEdit"},
		{"page edit", "PATCH", "/pages/a5669606-8764-5c90-b282-bad323a49baa", "ReassignPanels"},
		{"draft generation", "POST", "/pages/a5669606-8764-5c90-b282-bad323a49baa/draft", "UpdatePage"},
		{"draft approval", "POST", "/pages/a5669606-8764-5c90-b282-bad323a49baa/draft/approve", "ApproveStage"},
		{"line art generation", "POST", "/pages/a5669606-8764-5c90-b282-bad323a49baa/line-art?variant=ink", "UpdatePage"},
		{"line art approval", "POST", "/pages/a5669606-8764-5c90-b282-bad323a49baa/line-art/approve?variant=ink", "ApproveStage"},
		{"palette suggestion", "POST", "/pages/a5669606-8764-5c90-b282-bad323a49baa/colour-palette/suggest", "SaveColourPalette"},
		{"palette edit", "PATCH", "/pages/a5669606-8764-5c90-b282-bad323a49baa/colour-palette", "SaveColourPalette"},
		{"colour approval", "POST", "/pages/a5669606-8764-5c90-b282-bad323a49baa/colour/approve", "RecordApproval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, repo := setupTestServer(true, "test-user")
			seedWriteFailureFlow(t, repo)
			if tc.operation == "UpdateProject" {
				project, err := repo.GetProject(nil, "a400e5d7-0409-5a81-9050-3991208ceec1")
				if err != nil {
					t.Fatal(err)
				}
				project.ChapterCount = 0
				if err := repo.UpdateProject(nil, project); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(tc.operation, "ReplaceChapter") {
				// Regeneration refuses to delete art, so the failure case starts from art-free pages.
				page, err := repo.GetPage(nil, "a5669606-8764-5c90-b282-bad323a49baa")
				if err != nil {
					t.Fatal(err)
				}
				page.DraftImageURL, page.LineArtImageURL, page.ColourImageURL, page.ColourImageUploadedAt = "", "", "", nil
				if err := repo.UpdatePage(nil, page); err != nil {
					t.Fatal(err)
				}
			}
			failing := &failingWriteRepository{Repository: repo, operation: tc.operation}
			s.Repo = failing
			rec := httptest.NewRecorder()
			body := `{}`
			if tc.name == "panel edit" {
				body = `{"dialogue":"Updated dialogue"}`
			}
			if tc.name == "page edit" {
				body = `{"panel_ids":["a8a24397-3780-5a99-acea-8ab882a2ef5b"]}`
			}
			s.NewRouter().ServeHTTP(rec, httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader(body)))
			if rec.Code != http.StatusInternalServerError || failing.calls != 1 {
				t.Fatalf("expected one failed %s and HTTP 500, calls=%d HTTP %d: %s", tc.operation, failing.calls, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"error":`) {
				t.Fatalf("missing error: %s", rec.Body.String())
			}
		})
	}
}

func TestProjectReadFailureIsNotReportedAsNotFound(t *testing.T) {
	s, _, repo := setupTestServer(true, "test-user")
	s.Repo = &failingProjectReadRepository{Repository: repo}
	rec := httptest.NewRecorder()
	s.NewRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/projects/00000000-0000-0000-0000-000000000001", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("database read failure returned %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "injected database") {
		t.Fatalf("database details leaked to client: %s", rec.Body.String())
	}
}

func TestPaletteReadFailureIsNotReturnedAsEmptySuccess(t *testing.T) {
	s, _, repo := setupTestServer(true, "test-user")
	seedWriteFailureFlow(t, repo)
	s.Repo = &failingPaletteReadRepository{Repository: repo}
	rec := httptest.NewRecorder()
	s.NewRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/pages/a5669606-8764-5c90-b282-bad323a49baa/colour-palette", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("palette read failure returned %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "injected palette database") {
		t.Fatalf("database details leaked to client: %s", rec.Body.String())
	}
}

func TestPaletteRequiresStoredLineArtBeforeCharging(t *testing.T) {
	for _, imageURL := range []string{"", "lineart/page.png", "/media/page.png", "file:///page.png", "https://storage.example/lineart.png"} {
		t.Run(imageURL, func(t *testing.T) {
			s, _, repo := setupTestServer(true, "test-user")
			seedWriteFailureFlow(t, repo)
			page, err := repo.GetPage(nil, "a5669606-8764-5c90-b282-bad323a49baa")
			if err != nil {
				t.Fatal(err)
			}
			page.LineArtImageURL = imageURL
			if err := repo.UpdatePage(nil, page); err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.AIClient.(*gen.FakeAIServiceClient).SuggestColourPaletteFunc = func(_ context.Context, req gen.SuggestColourPaletteRequest) (*gen.SuggestColourPaletteResponse, error) {
				calls++
				if req.LineArtImageURL != imageURL {
					t.Errorf("expected stored URL %q, got %q", imageURL, req.LineArtImageURL)
				}
				return &gen.SuggestColourPaletteResponse{}, nil
			}
			before, err := s.CreditService.GetBalance(nil, "test-user")
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			s.NewRouter().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/pages/a5669606-8764-5c90-b282-bad323a49baa/colour-palette/suggest", nil))
			if strings.HasPrefix(imageURL, "https://") {
				if rec.Code != http.StatusAccepted || calls != 1 {
					t.Fatalf("HTTP %d calls=%d: %s", rec.Code, calls, rec.Body.String())
				}
			} else {
				after, err := s.CreditService.GetBalance(nil, "test-user")
				if err != nil {
					t.Fatal(err)
				}
				if rec.Code != http.StatusConflict || calls != 0 || before != after || !strings.Contains(rec.Body.String(), "stored line art image URL") {
					t.Fatalf("expected clear HTTP 409 without AI call or debit, HTTP %d calls=%d balances=%v/%v: %s", rec.Code, calls, before, after, rec.Body.String())
				}
			}
		})
	}
}

func TestColourApprovalIncludesProject(t *testing.T) {
	_, handler, repo := setupTestServer(true, "test-user")
	seedWriteFailureFlow(t, repo)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/pages/a5669606-8764-5c90-b282-bad323a49baa/colour/approve", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	approval, err := repo.GetLatestApproval(nil, "colour_page", "a5669606-8764-5c90-b282-bad323a49baa")
	if err != nil {
		t.Fatal(err)
	}
	if approval.ProjectID != "a400e5d7-0409-5a81-9050-3991208ceec1" {
		t.Fatalf("missing project foreign key: %+v", approval)
	}
}
