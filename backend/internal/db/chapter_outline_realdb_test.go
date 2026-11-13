//go:build realdb

package db_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

func chapterColumnNames(t *testing.T, pool *sql.DB) map[string]bool {
	t.Helper()
	rows, err := pool.Query(`SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='chapters'`)
	must(t, err)
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		must(t, rows.Scan(&name))
		columns[name] = true
	}
	must(t, rows.Err())
	return columns
}

func applyMigrationFile(t *testing.T, pool *sql.DB, name string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(migrationsDir, name))
	must(t, err)
	if _, err := pool.Exec(string(content)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

// Migration 000015 adds the outline columns to a table that already holds chapters, its down script
// removes exactly those columns without losing the chapters, and it can be applied again.
func TestRealDBChapterOutlineMigrationUpAndDownKeepChapters(t *testing.T) {
	pool, _, ctx := realDatabaseWithMigrations(t, false)
	entries, err := os.ReadDir(migrationsDir)
	must(t, err)
	var before []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") && entry.Name() < "000015" {
			before = append(before, entry.Name())
		}
	}
	for _, name := range before {
		applyMigrationFile(t, pool, name)
	}
	for _, column := range []string{"important_events", "character_names", "ending"} {
		if chapterColumnNames(t, pool)[column] {
			t.Fatalf("%s exists before migration 000015", column)
		}
	}

	userID, projectID, chapterID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'hash')`, []any{userID, userID + "@realdb.test"}},
		{`INSERT INTO projects (id, user_id, title, original_script, chapter_count, art_style, status) VALUES ($1, $2, 'P', 'script', 1, 'classic-comic', 'draft')`, []any{projectID, userID}},
		{`INSERT INTO chapters (id, project_id, chapter_number, title, summary) VALUES ($1, $2, 1, 'Old chapter', 'kept')`, []any{chapterID, projectID}},
	} {
		if _, err := pool.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed %q: %v", statement.query, err)
		}
	}

	applyMigrationFile(t, pool, "000015_chapter_outline_detail.up.sql")
	var events, names, ending string
	must(t, pool.QueryRowContext(ctx, `SELECT important_events::text, character_names::text, ending FROM chapters WHERE id=$1`, chapterID).Scan(&events, &names, &ending))
	if events != "[]" || names != "[]" || ending != "" {
		t.Fatalf("an existing chapter must get empty defaults, got %q %q %q", events, names, ending)
	}
	for _, bad := range []string{`UPDATE chapters SET important_events='{"a":1}'::jsonb`, `UPDATE chapters SET character_names='"x"'::jsonb`} {
		if _, err := pool.ExecContext(ctx, bad); err == nil {
			t.Fatalf("%s must be rejected: the lists are arrays", bad)
		}
	}

	_, err = pool.ExecContext(ctx, `UPDATE chapters SET important_events='["a"]'::jsonb, ending='the end' WHERE id=$1`, chapterID)
	must(t, err)
	applyMigrationFile(t, pool, "000015_chapter_outline_detail.down.sql")
	columns := chapterColumnNames(t, pool)
	for _, column := range []string{"important_events", "character_names", "ending"} {
		if columns[column] {
			t.Fatalf("down migration left column %s", column)
		}
	}
	var title string
	must(t, pool.QueryRowContext(ctx, `SELECT title FROM chapters WHERE id=$1`, chapterID).Scan(&title))
	if title != "Old chapter" {
		t.Fatalf("down migration lost the chapter, title = %q", title)
	}
	applyMigrationFile(t, pool, "000015_chapter_outline_detail.up.sql")
	must(t, pool.QueryRowContext(ctx, `SELECT important_events::text FROM chapters WHERE id=$1`, chapterID).Scan(&events))
	if events != "[]" {
		t.Fatalf("re-applied migration must start from defaults, got %q", events)
	}
}

func TestRealDBChapterOutlineDetailRoundTripsThroughRepository(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "fake-hash"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Outline", OriginalScript: "A hero.", ChapterCount: 2, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))

	created := &domain.Chapter{
		ProjectID: project.ID, ChapterNumber: 1, Title: "One", Summary: "S", Status: "pending",
		ImportantEvents: []string{"first", "second \"quoted\""}, CharacterNames: []string{"Maya", "Leo"}, Ending: "cliffhanger",
	}
	must(t, repo.CreateChapter(ctx, created))
	loaded, err := repo.GetChapter(ctx, created.ID)
	must(t, err)
	if !reflect.DeepEqual(loaded.ImportantEvents, created.ImportantEvents) || !reflect.DeepEqual(loaded.CharacterNames, created.CharacterNames) || loaded.Ending != "cliffhanger" {
		t.Fatalf("GetChapter lost outline detail: %+v", loaded)
	}

	loaded.ImportantEvents = []string{"only"}
	loaded.CharacterNames = nil
	loaded.Ending = ""
	must(t, repo.UpdateChapter(ctx, loaded))
	listed, err := repo.ListChapters(ctx, project.ID)
	must(t, err)
	if len(listed) != 1 || !reflect.DeepEqual(listed[0].ImportantEvents, []string{"only"}) || listed[0].CharacterNames == nil || len(listed[0].CharacterNames) != 0 || listed[0].Ending != "" {
		t.Fatalf("UpdateChapter/ListChapters did not round-trip the edit (cleared lists must read back as []): %+v", listed)
	}
	if raw, _ := json.Marshal(listed[0]); !strings.Contains(string(raw), `"character_names":[]`) {
		t.Fatalf("an empty cast must serialize as [], got %s", raw)
	}

	replacement := []*domain.Chapter{
		{ProjectID: project.ID, ChapterNumber: 1, Title: "A", Status: "pending", ImportantEvents: []string{"x"}, CharacterNames: []string{"Maya"}, Ending: "e1"},
		{ProjectID: project.ID, ChapterNumber: 2, Title: "B", Status: "pending", Ending: "e2"},
	}
	must(t, repo.ReplaceChapterOutline(ctx, project.ID, replacement))
	listed, err = repo.ListChapters(ctx, project.ID)
	must(t, err)
	if len(listed) != 2 || !reflect.DeepEqual(listed[0].ImportantEvents, []string{"x"}) || listed[0].Ending != "e1" || listed[1].Ending != "e2" || listed[1].ImportantEvents == nil {
		t.Fatalf("ReplaceChapterOutline lost outline detail: %+v", listed)
	}
}

// The editor patches visual_attributes; against PostgreSQL the stored JSON keeps every other key.
func TestRealDBCharacterAttributePatchKeepsUnnamedAttributesThroughHTTP(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Attributes", OriginalScript: "A hero.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	character := &domain.Character{ProjectID: project.ID, Name: "Hero", ReferenceImageURL: "/media/hero.png", VisualAttributes: map[string]interface{}{
		"age_range": "20-25", "hair_style": "teal pixie", "appearance": "athletic", "reference_role": "main", "nested": map[string]interface{}{"k": "v"},
	}}
	must(t, repo.CreateCharacter(ctx, character))
	server := api.NewServer(repo, credits.NewInMemoryCreditService(map[string]float64{user.ID: 50}), jobs.NewInMemoryJobService(), gen.NewFakeAIServiceClient(), auth.NewDevAuthenticator(true, user.ID), auth.NewDefaultImageHosting("/media"), 5)
	handler := server.NewRouter()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/characters/"+character.ID, strings.NewReader(`{"name":"Hero","visual_attributes":{"appearance":"tall","clothing":"red coat"}}`))
	req.Header.Set("X-User-ID", user.ID)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit character: HTTP %d: %s", rec.Code, rec.Body.String())
	}
	stored, err := repo.GetCharacter(ctx, character.ID)
	must(t, err)
	want := map[string]interface{}{
		"age_range": "20-25", "hair_style": "teal pixie", "appearance": "tall", "reference_role": "main", "clothing": "red coat", "nested": map[string]interface{}{"k": "v"},
	}
	if !reflect.DeepEqual(stored.VisualAttributes, want) {
		t.Fatalf("stored attributes\n got: %v\nwant: %v", stored.VisualAttributes, want)
	}
}
