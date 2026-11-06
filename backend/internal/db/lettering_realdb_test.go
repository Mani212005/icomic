//go:build realdb

package db_test

import (
	"encoding/json"
	"errors"
	"icomic/backend/internal/db"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"icomic/backend/internal/domain"
)

func TestRealDBLetteringUpgradeKeepsExistingBookBaked(t *testing.T) {
	pool, repo, ctx := realDatabaseWithMigrations(t, false)
	entries, err := os.ReadDir(migrationsDir)
	must(t, err)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") && !strings.HasPrefix(entry.Name(), "000009_") {
			data, err := os.ReadFile(filepath.Join(migrationsDir, entry.Name()))
			must(t, err)
			_, err = pool.ExecContext(ctx, string(data))
			must(t, err)
		}
	}
	var userID, projectID, chapterID, pageID string
	must(t, pool.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES ('lettering@test.invalid','fake') RETURNING id`).Scan(&userID))
	must(t, pool.QueryRowContext(ctx, `INSERT INTO projects(user_id,title,original_script) VALUES ($1,'Running book','Story') RETURNING id`, userID).Scan(&projectID))
	must(t, pool.QueryRowContext(ctx, `INSERT INTO chapters(project_id,chapter_number,title) VALUES ($1,1,'One') RETURNING id`, projectID).Scan(&chapterID))
	must(t, pool.QueryRowContext(ctx, `INSERT INTO pages(chapter_id,page_number,painted_page_image_url,line_art_image_url) VALUES ($1,1,'fake://original-painted','fake://original-ink') RETURNING id`, chapterID).Scan(&pageID))
	migration, err := os.ReadFile(filepath.Join(migrationsDir, "000009_page_lettering.up.sql"))
	must(t, err)
	_, err = pool.ExecContext(ctx, string(migration))
	must(t, err)
	project, err := repo.GetProject(ctx, projectID)
	must(t, err)
	page, err := repo.GetPage(ctx, pageID)
	must(t, err)
	if project.LetteringMode != "baked" || page.LetteringMode != "baked" || len(page.Lettering) != 0 || page.PaintedPageImageURL != "fake://original-painted" || page.LineArtImageURL != "fake://original-ink" {
		t.Fatal("migration changed existing art or eligibility", project, page)
	}
	// A new eligible page persists all versioned state through create/get/list/update.
	input := json.RawMessage(`{"version":"page-lettering-v1","input":{"version":"lettering-v1"},"overrides":{},"zone_overrides":{},"layout":{"version":"lettering-layout-v1"}}`)
	overlay := &domain.Page{ChapterID: chapterID, PageNumber: 2, PanelIDs: []string{}, LetteringMode: "overlay", Lettering: input, PaintedPageImageURL: "fake://text-free"}
	must(t, repo.CreatePage(ctx, overlay))
	saved, err := repo.GetPage(ctx, overlay.ID)
	must(t, err)
	previous := append(json.RawMessage(nil), saved.Lettering...)
	saved.Lettering = json.RawMessage(`{"version":"page-lettering-v1","input":{},"overrides":{"u":{"center":{"x":0.5,"y":0.2}}},"zone_overrides":{},"layout":{}}`)
	must(t, repo.SavePageLettering(ctx, saved.ID, previous, saved.Lettering))
	if err := repo.SavePageLettering(ctx, saved.ID, previous, json.RawMessage(`{}`)); !errors.Is(err, db.ErrConflict) {
		t.Fatal("stale layer write was not rejected", err)
	}
	// An image write that began before the lettering edit cannot replace it.
	overlay.PaintedPageImageURL = "fake://new-text-free"
	must(t, repo.UpdatePage(ctx, overlay))
	pages, err := repo.ListPages(ctx, chapterID)
	must(t, err)
	if len(pages) != 2 || pages[1].LetteringMode != "overlay" || pages[1].PaintedPageImageURL != overlay.PaintedPageImageURL {
		t.Fatal(pages)
	}
	var actual, expected any
	must(t, json.Unmarshal(pages[1].Lettering, &actual))
	must(t, json.Unmarshal(saved.Lettering, &expected))
	a, _ := json.Marshal(actual)
	e, _ := json.Marshal(expected)
	if string(a) != string(e) {
		t.Fatal("lettering JSONB did not round trip")
	}
	_, err = pool.ExecContext(ctx, `UPDATE pages SET lettering='{}'::jsonb WHERE id=$1`, pageID)
	if err == nil {
		t.Fatal("baked pages must reject lettering state")
	}
	_, err = pool.ExecContext(ctx, `UPDATE pages SET line_art_image_url='fake://ink' WHERE id=$1`, saved.ID)
	if err == nil {
		t.Fatal("ink cannot be assigned to an overlay page")
	}

}

func TestRealDBLetteringTextIsAtomicAndPreservesImageState(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := &domain.User{Email: "text@realdb.test", PasswordHash: "fake"}
	must(t, repo.CreateUser(ctx, user))
	project := &domain.Project{UserID: user.ID, Title: "Text", OriginalScript: "Story", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	chapter := &domain.Chapter{ProjectID: project.ID, ChapterNumber: 1, Title: "One", Status: "pending"}
	must(t, repo.CreateChapter(ctx, chapter))
	panel := &domain.Panel{ChapterID: chapter.ID, PanelNumber: 1, Dialogue: "Nia: Hello", Narration: "Dawn"}
	must(t, repo.CreatePanel(ctx, panel))
	beforeLayer := json.RawMessage(`{"version":"page-lettering-v1","input":{},"overrides":{},"layout":{}}`)
	nextLayer := json.RawMessage(`{"version":"page-lettering-v1","input":{"text":"Hello!"},"overrides":{},"layout":{}}`)
	page := &domain.Page{ChapterID: chapter.ID, PageNumber: 1, PanelIDs: []string{panel.ID}, LetteringMode: "overlay", Lettering: beforeLayer, DraftImageURL: "fake://draft", PaintedPageImageURL: "fake://painted"}
	must(t, repo.CreatePage(ctx, page))
	original, err := repo.GetPanel(ctx, panel.ID)
	must(t, err)
	beforePage, err := repo.GetPage(ctx, page.ID)
	must(t, err)
	next := *original
	next.Dialogue = "Nia: Hello!"
	next.UpdatedAt = next.UpdatedAt.Add(time.Second)
	must(t, repo.SaveLetteringText(ctx, page.ID, original, &next, beforeLayer, nextLayer))
	// Both possible compare failures must roll back the complete transaction.
	if err := repo.SaveLetteringText(ctx, page.ID, original, &next, nextLayer, json.RawMessage(`{}`)); !errors.Is(err, db.ErrConflict) {
		t.Fatal("stale source accepted", err)
	}
	source, err := repo.GetPanel(ctx, panel.ID)
	must(t, err)
	if err := repo.SaveLetteringText(ctx, page.ID, source, &next, beforeLayer, json.RawMessage(`{}`)); !errors.Is(err, db.ErrConflict) {
		t.Fatal("stale layer accepted", err)
	}
	afterPage, err := repo.GetPage(ctx, page.ID)
	must(t, err)
	if !afterPage.UpdatedAt.Equal(beforePage.UpdatedAt) || afterPage.DraftImageURL != beforePage.DraftImageURL || afterPage.PaintedPageImageURL != beforePage.PaintedPageImageURL {
		t.Fatal("image state changed")
	}
	var data map[string]any
	must(t, json.Unmarshal(afterPage.Lettering, &data))
	if data["input"].(map[string]any)["text"] != "Hello!" || source.Dialogue != "Nia: Hello!" {
		t.Fatal("source and layer did not commit atomically")
	}
}
