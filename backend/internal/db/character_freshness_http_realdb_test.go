//go:build realdb

package db_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestRealDBCharacterEditInvalidatesApprovalThroughHTTP(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Freshness", OriginalScript: "A hero finds the lost key.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	character := &domain.Character{ProjectID: project.ID, Name: "Hero", Description: "A careful explorer", ReferenceImageURL: "/media/reference.png"}
	must(t, repo.CreateCharacter(ctx, character))
	server := api.NewServer(repo, credits.NewInMemoryCreditService(map[string]float64{user.ID: 50}), jobs.NewInMemoryJobService(), gen.NewFakeAIServiceClient(), auth.NewDevAuthenticator(true, user.ID), auth.NewDefaultImageHosting("/media"), 5)
	handler := server.NewRouter()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-User-ID", user.ID)
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := request(http.MethodPost, "/api/v1/characters/"+character.ID+"/approve", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("approve reference: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPatch, "/api/v1/characters/"+character.ID, `{"description":"A careful explorer with a red scarf"}`); rec.Code != http.StatusOK {
		t.Fatalf("edit character: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	charactersResponse := request(http.MethodGet, "/api/v1/projects/"+project.ID+"/characters", "")
	if charactersResponse.Code != http.StatusOK {
		t.Fatalf("get characters: HTTP %d: %s", charactersResponse.Code, charactersResponse.Body.String())
	}
	var charactersBody struct {
		Characters []*domain.Character `json:"characters"`
	}
	if err := json.Unmarshal(charactersResponse.Body.Bytes(), &charactersBody); err != nil {
		t.Fatal(err)
	}
	if len(charactersBody.Characters) != 1 || charactersBody.Characters[0].UpdatedAt.IsZero() || charactersBody.Characters[0].ApprovedAt == nil {
		t.Fatalf("expected persisted activity and approval timestamps, got %+v", charactersBody.Characters)
	}
	if !charactersBody.Characters[0].UpdatedAt.After(*charactersBody.Characters[0].ApprovedAt) {
		t.Fatalf("edit activity %s must be after approval %s", charactersBody.Characters[0].UpdatedAt, *charactersBody.Characters[0].ApprovedAt)
	}
	if rec := request(http.MethodPost, "/api/v1/projects/"+project.ID+"/chapters", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("stale character approval must block outline generation, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, "/api/v1/characters/"+character.ID+"/approve", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("reapprove reference: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, "/api/v1/projects/"+project.ID+"/chapters", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("fresh character approval should allow outline generation, got HTTP %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRealDBCharacterTimestampPrecisionRoundTrips(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Precision", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	activity := time.Date(2026, time.November, 5, 10, 0, 0, 123456000, time.UTC)
	character := &domain.Character{ProjectID: project.ID, Name: "Hero", UpdatedAt: activity}
	must(t, repo.CreateCharacter(ctx, character))
	loaded, err := repo.GetCharacter(ctx, character.ID)
	must(t, err)
	if !loaded.UpdatedAt.Equal(activity) {
		t.Fatalf("GetCharacter activity timestamp = %s, want %s", loaded.UpdatedAt, activity)
	}
	listed, err := repo.ListCharacters(ctx, project.ID)
	must(t, err)
	if len(listed) != 1 || !listed[0].UpdatedAt.Equal(activity) {
		t.Fatalf("ListCharacters activity timestamp = %+v, want %s", listed, activity)
	}
}
