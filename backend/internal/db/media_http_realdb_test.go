//go:build realdb

package db_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

// Media keys carry the id of the resource that owns them; ownership is resolved through the persisted
// project, chapter, page and character rows, so one user's keys never open for another.
func TestRealDBMediaIsServedOnlyToTheOwnerThroughHTTP(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	owner := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	other := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	must(t, repo.CreateUser(ctx, owner))
	must(t, repo.CreateUser(ctx, other))
	project := &domain.Project{UserID: owner.ID, Title: "Media", OriginalScript: "A hero finds the lost key.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	character := &domain.Character{ProjectID: project.ID, Name: "Hero", Description: "Explorer"}
	must(t, repo.CreateCharacter(ctx, character))
	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "One", Status: "pending"}
	must(t, repo.CreateChapter(ctx, chapter))
	page := &domain.Page{ChapterID: chapter.ID, PageNumber: 1, PanelIDs: []string{}}
	must(t, repo.CreatePage(ctx, page))

	png := "\x89PNG\r\n\x1a\nowner-pixels"
	keys := []string{
		"characters/" + character.ID + "/a1/reference.png",
		"pages/" + page.ID + "/b2/draft_page_1.png",
		"finishes/" + page.ID + "/print-plus/c3.jpg",
		"exports/" + project.ID + "/d4/comic.pdf",
	}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".pdf") {
			w.Header().Set("Content-Type", "application/pdf")
		}
		_, _ = w.Write([]byte(png))
	}))
	defer storage.Close()
	server := api.NewServer(repo, credits.NewInMemoryCreditService(map[string]float64{owner.ID: 50, other.ID: 50}), jobs.NewInMemoryJobService(),
		gen.NewAIServiceClient(storage.URL, 5*time.Second), auth.NewDevAuthenticator(true, ""), auth.NewDefaultImageHosting("/media"), 5)
	handler := server.NewRouter()
	get := func(target, user string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if user != "" {
			req.Header.Set("X-User-ID", user)
		}
		handler.ServeHTTP(rec, req)
		return rec
	}

	for _, key := range keys {
		if rec := get("/media/"+key, owner.ID); rec.Code != http.StatusOK || rec.Body.String() != png {
			t.Errorf("owner GET %s = %d", key, rec.Code)
		}
		if rec := get("/media/"+key, other.ID); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "owner-pixels") {
			t.Errorf("other user GET %s = %d, want 404", key, rec.Code)
		}
		if rec := get("/media/"+key, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", key, rec.Code)
		}
	}

	// A link minted by the real API response opens the artifact with no other credential.
	must(t, repo.UpdateCharacter(ctx, &domain.Character{ID: character.ID, ProjectID: project.ID, Name: "Hero", Description: "Explorer",
		ReferenceImageURL: "http://ai:8000/storage/fake/" + keys[0], UpdatedAt: time.Now().UTC()}))
	rec := get("/api/v1/projects/"+project.ID+"/characters", owner.ID)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "http://ai:8000") {
		t.Fatalf("characters = %d %s, want scoped links and no internal host", rec.Code, rec.Body.String())
	}
	start := strings.Index(rec.Body.String(), `"reference_image_url":"`) + len(`"reference_image_url":"`)
	link, err := url.Parse(rec.Body.String()[start : start+strings.Index(rec.Body.String()[start:], `"`)])
	if err != nil {
		t.Fatal(err)
	}
	if got := get(link.RequestURI(), ""); got.Code != http.StatusOK || got.Body.String() != png {
		t.Fatalf("signed link = %d", got.Code)
	}
}
