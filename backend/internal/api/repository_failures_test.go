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
	for _, err := range []error{
		repo.CreateProject(nil, &domain.Project{ID: "project", UserID: "test-user", OriginalScript: "A hero finds a bird", ChapterCount: 1}),
		repo.CreateCharacter(nil, &domain.Character{ID: "character", ProjectID: "project", Name: "Hero", ReferenceImageURL: "https://storage.example/reference.png", ApprovedAt: &now, UpdatedAt: now}),
		repo.CreateChapter(nil, &domain.Chapter{ID: "chapter", ProjectID: "project", ChapterNumber: 1, Title: "The bird", UpdatedAt: now}),
		repo.CreatePanel(nil, &domain.Panel{ID: "panel", ChapterID: "chapter", PanelNumber: 1, SceneDescription: "A bird", UpdatedAt: now}),
		repo.CreatePage(nil, &domain.Page{ID: "page", ChapterID: "chapter", PageNumber: 1, PanelIDs: []string{"panel"}, DraftImageURL: "https://storage.example/draft.png", LineArtImageURL: "https://storage.example/lineart.png", DraftApprovedAt: &now, LineArtApprovedAt: &now, UpdatedAt: now}),
		repo.RecordApproval(nil, &domain.StageApproval{ProjectID: "project", EntityType: "chapter_outline", EntityID: "chapter", ApprovedAt: time.Now().UTC()}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandlersSurfaceRepositoryWriteFailures(t *testing.T) {
	cases := []struct{ name, method, path, operation string }{
		{"analysis character", "POST", "/projects/project/analyze", "CreateCharacter"},
		{"analysis project", "POST", "/projects/project/analyze", "UpdateProject"},
		{"reference image", "POST", "/characters/character/reference-image", "UpdateCharacter"},
		{"character approval update", "POST", "/characters/character/approve", "UpdateCharacter"},
		{"character approval record", "POST", "/characters/character/approve", "RecordApproval"},
		{"chapter generation", "POST", "/projects/project/chapters", "CreateChapter"},
		{"chapter edit", "PATCH", "/chapters/chapter", "UpdateChapter"},
		{"chapter approval update", "POST", "/chapters/chapter/approve", "UpdateChapter"},
		{"chapter approval record", "POST", "/chapters/chapter/approve", "RecordApproval"},
		{"panel generation", "POST", "/chapters/chapter/panels", "CreatePanel"},
		{"page partition", "POST", "/chapters/chapter/panels", "CreatePage"},
		{"panel edit", "PATCH", "/panels/panel", "UpdatePanel"},
		{"panel approval invalidation", "PATCH", "/panels/panel", "UpdatePage"},
		{"page edit", "PATCH", "/pages/page", "UpdatePage"},
		{"draft generation", "POST", "/pages/page/draft", "UpdatePage"},
		{"draft approval update", "POST", "/pages/page/draft/approve", "UpdatePage"},
		{"draft approval record", "POST", "/pages/page/draft/approve", "RecordApproval"},
		{"line art generation", "POST", "/pages/page/line-art", "UpdatePage"},
		{"line art approval update", "POST", "/pages/page/line-art/approve", "UpdatePage"},
		{"line art approval record", "POST", "/pages/page/line-art/approve", "RecordApproval"},
		{"palette suggestion", "POST", "/pages/page/colour-palette/suggest", "SaveColourPalette"},
		{"palette edit", "PATCH", "/pages/page/colour-palette", "SaveColourPalette"},
		{"colour approval", "POST", "/pages/page/colour/approve", "RecordApproval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, repo := setupTestServer(true, "test-user")
			seedWriteFailureFlow(t, repo)
			if tc.operation == "UpdateProject" {
				project, err := repo.GetProject(nil, "project")
				if err != nil {
					t.Fatal(err)
				}
				project.ChapterCount = 0
				if err := repo.UpdateProject(nil, project); err != nil {
					t.Fatal(err)
				}
			}
			failing := &failingWriteRepository{Repository: repo, operation: tc.operation}
			s.Repo = failing
			rec := httptest.NewRecorder()
			s.NewRouter().ServeHTTP(rec, httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader(`{}`)))
			if rec.Code != http.StatusInternalServerError || failing.calls != 1 {
				t.Fatalf("expected one failed %s and HTTP 500, calls=%d HTTP %d: %s", tc.operation, failing.calls, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"error":`) {
				t.Fatalf("missing error: %s", rec.Body.String())
			}
		})
	}
}

func TestPaletteRequiresStoredLineArtBeforeCharging(t *testing.T) {
	for _, imageURL := range []string{"", "lineart/page.png", "/media/page.png", "file:///page.png", "https://storage.example/lineart.png"} {
		t.Run(imageURL, func(t *testing.T) {
			s, _, repo := setupTestServer(true, "test-user")
			seedWriteFailureFlow(t, repo)
			page, err := repo.GetPage(nil, "page")
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
			s.NewRouter().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/pages/page/colour-palette/suggest", nil))
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
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/pages/page/colour/approve", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	approval, err := repo.GetLatestApproval(nil, "colour_page", "page")
	if err != nil {
		t.Fatal(err)
	}
	if approval.ProjectID != "project" {
		t.Fatalf("missing project foreign key: %+v", approval)
	}
}
