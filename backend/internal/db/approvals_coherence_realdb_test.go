//go:build realdb

package db_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

type coherenceDB struct {
	pool    *sql.DB
	ctx     context.Context
	repo    *db.PostgresRepository
	user    *domain.User
	project *domain.Project
}

func newCoherenceDB(t *testing.T) *coherenceDB {
	t.Helper()
	pool, repo, ctx := realDatabase(t)
	user, project := realUserProject(t, repo, ctx)
	return &coherenceDB{pool: pool, ctx: ctx, repo: repo, user: user, project: project}
}

func (c *coherenceDB) chapter(t *testing.T, number int) *domain.Chapter {
	t.Helper()
	ch := &domain.Chapter{ProjectID: c.project.ID, ChapterNumber: number, Title: "Chapter", Status: "pending"}
	must(t, c.repo.CreateChapter(c.ctx, ch))
	return ch
}

// allocation creates panels and pages with the given shape and returns the panels in creation order.
func (c *coherenceDB) allocation(t *testing.T, chapter *domain.Chapter, shape ...int) ([]*domain.Panel, []*domain.Page) {
	t.Helper()
	var panels []*domain.Panel
	var pages []*domain.Page
	number := 0
	for n, size := range shape {
		var ids []string
		for i := 0; i < size; i++ {
			number++
			p := &domain.Panel{ChapterID: chapter.ID, PanelNumber: number, SceneDescription: "scene", Status: "ready"}
			must(t, c.repo.CreatePanel(c.ctx, p))
			panels = append(panels, p)
			ids = append(ids, p.ID)
		}
		approved := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		pg := &domain.Page{ChapterID: chapter.ID, PageNumber: n + 1, PanelIDs: ids, DraftApprovedAt: &approved, LineArtApprovedAt: &approved}
		must(t, c.repo.CreatePage(c.ctx, pg))
		pages = append(pages, pg)
	}
	return panels, pages
}

func (c *coherenceDB) pageIDs(t *testing.T, chapterID string) [][]string {
	t.Helper()
	pages, err := c.repo.ListPages(c.ctx, chapterID)
	must(t, err)
	var out [][]string
	for _, pg := range pages {
		out = append(out, pg.PanelIDs)
	}
	return out
}

func sameAllocation(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.Join(a[i], ",") != strings.Join(b[i], ",") {
			return false
		}
	}
	return true
}

func TestRealDBApprovalStampsEntityAndRecordTogether(t *testing.T) {
	c := newCoherenceDB(t)
	ch := &domain.Character{ProjectID: c.project.ID, Name: "Hero", ReferenceImageURL: "/media/hero.png"}
	must(t, c.repo.CreateCharacter(c.ctx, ch))
	loaded, err := c.repo.GetCharacter(c.ctx, ch.ID)
	must(t, err)
	approval := func(at time.Time) *domain.StageApproval {
		return &domain.StageApproval{ProjectID: c.project.ID, EntityType: "character_reference", EntityID: ch.ID, ApprovedByUserID: c.user.ID, ApprovedAt: at}
	}

	// The record cannot be written (unknown project), so the entity must not be stamped either.
	bad := approval(loaded.UpdatedAt.Add(time.Millisecond))
	bad.ProjectID = uuid.NewString()
	if err := c.repo.ApproveStage(c.ctx, bad, loaded.UpdatedAt); err == nil {
		t.Fatal("approval with an invalid record must fail")
	}
	after, err := c.repo.GetCharacter(c.ctx, ch.ID)
	must(t, err)
	if after.ApprovedAt != nil {
		t.Fatal("entity was stamped although the approval record was not written")
	}

	// An edit between validation and commit is reported and writes nothing.
	edited := *loaded
	edited.Description = "changed"
	edited.UpdatedAt = loaded.UpdatedAt.Add(time.Second)
	must(t, c.repo.UpdateCharacter(c.ctx, &edited))
	if err := c.repo.ApproveStage(c.ctx, approval(loaded.UpdatedAt.Add(time.Millisecond)), loaded.UpdatedAt); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("approval of an outdated read: %v", err)
	}
	if _, err := c.repo.GetLatestApproval(c.ctx, "character_reference", ch.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("conflicting approval left a record: %v", err)
	}

	// Against the current version both writes land.
	must(t, c.repo.ApproveStage(c.ctx, approval(edited.UpdatedAt.Add(time.Millisecond)), edited.UpdatedAt))
	final, err := c.repo.GetCharacter(c.ctx, ch.ID)
	must(t, err)
	record, err := c.repo.GetLatestApproval(c.ctx, "character_reference", ch.ID)
	must(t, err)
	if final.ApprovedAt == nil || !final.ApprovedAt.Equal(record.ApprovedAt) || !final.ApprovedAt.After(final.UpdatedAt) {
		t.Fatalf("stamp %v and record %v must agree and postdate the edit %v", final.ApprovedAt, record.ApprovedAt, final.UpdatedAt)
	}
}

func TestRealDBPanelEditPersistsActivityAndStalesPagesTogether(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	panels, pages := c.allocation(t, chapter, 2, 1)
	edit := *panels[0]
	edit.Dialogue = "Edited"
	now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Minute)
	must(t, c.repo.SavePanelEdit(c.ctx, &edit, now))

	saved, err := c.repo.GetPanel(c.ctx, panels[0].ID)
	must(t, err)
	if saved.Dialogue != "Edited" || !saved.UpdatedAt.Equal(now) {
		t.Fatalf("panel edit and activity time must persist: %+v", saved)
	}
	listed, err := c.repo.ListPanels(c.ctx, chapter.ID)
	must(t, err)
	if !listed[0].UpdatedAt.Equal(now) {
		t.Fatalf("listed activity time lost: %v", listed[0].UpdatedAt)
	}
	hit, err := c.repo.GetPage(c.ctx, pages[0].ID)
	must(t, err)
	if hit.DraftApprovedAt != nil || hit.LineArtApprovedAt != nil || !hit.UpdatedAt.Equal(now) {
		t.Fatalf("page holding the panel must be staled: %+v", hit)
	}
	other, err := c.repo.GetPage(c.ctx, pages[1].ID)
	must(t, err)
	if other.DraftApprovedAt == nil {
		t.Fatal("an unrelated page was staled")
	}
	ghost := *panels[0]
	ghost.ID = uuid.NewString()
	if err := c.repo.SavePanelEdit(c.ctx, &ghost, now); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("editing a missing panel: %v", err)
	}
}

func TestRealDBReassignPanelsValidatesAndAppliesAtomically(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	other := c.chapter(t, 2)
	panels, pages := c.allocation(t, chapter, 2, 1)
	foreign, _ := c.allocation(t, other, 1)
	id := func(i int) string { return panels[i].ID }
	before := c.pageIDs(t, chapter.ID)
	now := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)

	for name, tc := range map[string]struct {
		page string
		ids  []string
		max  int
	}{
		"nonexistent":     {pages[0].ID, []string{id(0), id(1), uuid.NewString()}, 5},
		"foreign chapter": {pages[0].ID, []string{id(0), id(1), foreign[0].ID}, 5},
		"duplicate":       {pages[1].ID, []string{id(2), id(2)}, 5},
		"overfull":        {pages[1].ID, []string{id(2), id(0), id(1)}, 2},
		"dropped":         {pages[0].ID, []string{id(0)}, 5},
		"empties source":  {pages[1].ID, []string{id(2), id(0), id(1)}, 5},
		"missing page":    {uuid.NewString(), []string{id(0)}, 5},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.repo.ReassignPanels(c.ctx, tc.page, tc.ids, tc.max, now)
			if name == "missing page" {
				if !errors.Is(err, db.ErrNotFound) {
					t.Fatalf("got %v", err)
				}
			} else if !errors.Is(err, domain.ErrInvalidPanelAllocation) {
				t.Fatalf("got %v", err)
			}
			if got := c.pageIDs(t, chapter.ID); !sameAllocation(got, before) {
				t.Fatalf("rejected edit changed allocation: %v", got)
			}
			if pg, _ := c.repo.GetPage(c.ctx, pages[0].ID); pg.DraftApprovedAt == nil {
				t.Fatal("rejected edit staled an approval")
			}
		})
	}

	changed, err := c.repo.ReassignPanels(c.ctx, pages[1].ID, []string{id(2), id(1)}, 5, now)
	must(t, err)
	if len(changed) != 2 {
		t.Fatalf("a move changes both pages, got %v", changed)
	}
	if got := c.pageIDs(t, chapter.ID); !sameAllocation(got, [][]string{{id(0)}, {id(2), id(1)}}) {
		t.Fatalf("after move: %v", got)
	}
	for _, pageID := range []string{pages[0].ID, pages[1].ID} {
		pg, _ := c.repo.GetPage(c.ctx, pageID)
		if pg.DraftApprovedAt != nil || pg.LineArtApprovedAt != nil || !pg.UpdatedAt.Equal(now.Truncate(time.Microsecond)) {
			t.Fatalf("page %s not staled: %+v", pageID, pg)
		}
	}
	moved, _ := c.repo.GetPanel(c.ctx, id(1))
	if !moved.UpdatedAt.Equal(now.Truncate(time.Microsecond)) {
		t.Fatalf("a moved panel's source changed, so its activity time must too: %v", moved.UpdatedAt)
	}
	if changed, err := c.repo.ReassignPanels(c.ctx, pages[1].ID, []string{id(2), id(1)}, 5, now.Add(time.Minute)); err != nil || len(changed) != 0 {
		t.Fatalf("no-op: %v %v", changed, err)
	}
}

func TestRealDBConcurrentPanelMovesKeepEveryPanelAssignedOnce(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	panels, pages := c.allocation(t, chapter, 2, 2, 2)
	// Callers fight over the same panel from two directions; capacity lets both succeed in turn.
	concurrentRun(t, 6, func(ctx context.Context, i int) error {
		var err error
		if i%2 == 0 {
			_, err = c.repo.ReassignPanels(ctx, pages[1].ID, []string{panels[2].ID, panels[3].ID, panels[0].ID}, 3, time.Now().UTC())
		} else {
			_, err = c.repo.ReassignPanels(ctx, pages[2].ID, []string{panels[4].ID, panels[5].ID, panels[0].ID}, 3, time.Now().UTC())
		}
		if errors.Is(err, domain.ErrInvalidPanelAllocation) {
			return nil
		}
		return err
	})
	seen := map[string]int{}
	for _, ids := range c.pageIDs(t, chapter.ID) {
		if len(ids) == 0 {
			t.Fatal("a page was left empty")
		}
		for _, id := range ids {
			seen[id]++
		}
	}
	if len(seen) != len(panels) {
		t.Fatalf("panels lost: %v", seen)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("panel %s assigned %d times", id, n)
		}
	}
}

func TestRealDBRegenerationReplacesRevisionOrKeepsTheOldOne(t *testing.T) {
	c := newCoherenceDB(t)
	old := c.chapter(t, 1)
	oldPanels, oldPages := c.allocation(t, old, 1)
	next := func(numbers ...int) []*domain.Chapter {
		var out []*domain.Chapter
		for _, n := range numbers {
			out = append(out, &domain.Chapter{ProjectID: c.project.ID, ChapterNumber: n, Title: "Regenerated", Status: "pending"})
		}
		return out
	}
	numbers := func() []int {
		chapters, err := c.repo.ListChapters(c.ctx, c.project.ID)
		must(t, err)
		var out []int
		for _, ch := range chapters {
			out = append(out, ch.ChapterNumber)
		}
		return out
	}

	// Duplicate numbers fail inside the commit; the old revision, panels and pages stay.
	if err := c.repo.ReplaceChapterOutline(c.ctx, c.project.ID, next(1, 1)); err == nil {
		t.Fatal("duplicate chapter numbers must fail")
	}
	if got := numbers(); len(got) != 1 {
		t.Fatalf("failed replacement changed chapters: %v", got)
	}
	if _, err := c.repo.GetPanel(c.ctx, oldPanels[0].ID); err != nil {
		t.Fatalf("failed replacement lost a panel: %v", err)
	}

	// Generated art makes replacement refuse rather than delete it.
	art := *oldPages[0]
	art.DraftImageURL = "/media/draft.png"
	art.UpdatedAt = time.Now().UTC()
	must(t, c.repo.UpdatePage(c.ctx, &art))
	if err := c.repo.ReplaceChapterOutline(c.ctx, c.project.ID, next(1, 2)); !errors.Is(err, db.ErrArtExists) {
		t.Fatalf("outline over art: %v", err)
	}
	if err := c.repo.ReplaceChapterPanels(c.ctx, old.ID, nil, nil); !errors.Is(err, db.ErrArtExists) {
		t.Fatalf("panels over art: %v", err)
	}
	if kept, _ := c.repo.GetPage(c.ctx, oldPages[0].ID); kept.DraftImageURL == "" {
		t.Fatal("art was deleted")
	}
	art.DraftImageURL = ""
	art.UpdatedAt = time.Now().UTC()
	must(t, c.repo.UpdatePage(c.ctx, &art))

	// Panels: a half-written revision (duplicate panel number) rolls back to the old one.
	now := time.Now().UTC()
	p1 := &domain.Panel{ID: uuid.NewString(), ChapterID: old.ID, PanelNumber: 1, Status: "ready", UpdatedAt: now}
	p2 := &domain.Panel{ID: uuid.NewString(), ChapterID: old.ID, PanelNumber: 1, Status: "ready", UpdatedAt: now}
	page := &domain.Page{ID: uuid.NewString(), ChapterID: old.ID, PageNumber: 1, PanelIDs: []string{p1.ID, p2.ID}, UpdatedAt: now}
	if err := c.repo.ReplaceChapterPanels(c.ctx, old.ID, []*domain.Panel{p1, p2}, []*domain.Page{page}); err == nil {
		t.Fatal("duplicate panel numbers must fail")
	}
	if got := c.pageIDs(t, old.ID); !sameAllocation(got, [][]string{{oldPanels[0].ID}}) {
		t.Fatalf("failed panel replacement changed the chapter: %v", got)
	}
	p2.PanelNumber = 2
	must(t, c.repo.ReplaceChapterPanels(c.ctx, old.ID, []*domain.Panel{p1, p2}, []*domain.Page{page}))
	if got := c.pageIDs(t, old.ID); !sameAllocation(got, [][]string{{p1.ID, p2.ID}}) {
		t.Fatalf("after replacement: %v", got)
	}
	if _, err := c.repo.GetPanel(c.ctx, oldPanels[0].ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("old panel survived replacement: %v", err)
	}

	// Outline: the new revision replaces the old one wholesale, children included.
	must(t, c.repo.ReplaceChapterOutline(c.ctx, c.project.ID, next(1, 2)))
	if got := numbers(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("after outline replacement: %v", got)
	}
	if _, err := c.repo.GetChapter(c.ctx, old.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("old chapter survived: %v", err)
	}
	must(t, c.repo.ReplaceChapterOutline(c.ctx, c.project.ID, next(1, 2)))
	if got := numbers(); len(got) != 2 {
		t.Fatalf("re-running the outline duplicated chapters: %v", got)
	}
}

func TestRealDBTextGenerationRequiresApprovedInputsInTransaction(t *testing.T) {
	c := newCoherenceDB(t)
	svc := jobs.NewPersistentJobService(c.repo)
	create := func(kind, entity, stage string) error {
		_, _, err := svc.CreateJob(c.ctx, jobs.CreateJobRequest{ProjectID: c.project.ID, UserID: c.user.ID, EntityID: entity, JobType: kind, Stage: stage, ModelID: "fake", IdempotencyKey: uuid.NewString(), HashedUserID: domain.HashUserID(c.user.ID)})
		return err
	}
	jobCount := func() int {
		var n int
		must(t, c.pool.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM generation_jobs WHERE project_id=$1::uuid`, c.project.ID).Scan(&n))
		return n
	}

	// No characters at all: the outline gate cannot be skipped.
	if err := create("chapter_outline", c.project.ID, "outline"); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("outline with no characters: %v", err)
	}
	ch := &domain.Character{ProjectID: c.project.ID, Name: "Hero", ReferenceImageURL: "/media/hero.png"}
	must(t, c.repo.CreateCharacter(c.ctx, ch))
	if err := create("chapter_outline", c.project.ID, "outline"); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("outline with unapproved character: %v", err)
	}
	loaded, err := c.repo.GetCharacter(c.ctx, ch.ID)
	must(t, err)
	must(t, c.repo.ApproveStage(c.ctx, &domain.StageApproval{ProjectID: c.project.ID, EntityType: "character_reference", EntityID: ch.ID, ApprovedByUserID: c.user.ID, ApprovedAt: loaded.UpdatedAt.Add(time.Millisecond)}, loaded.UpdatedAt))
	if err := create("chapter_outline", c.project.ID, "outline"); err != nil {
		t.Fatalf("outline with approved character: %v", err)
	}

	chapter := c.chapter(t, 1)
	if err := create("panel_text", chapter.ID, "panels"); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("panels before outline approval: %v", err)
	}
	if err := create("panel_text", "", "panels"); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) {
		t.Fatalf("panels without entity: %v", err)
	}
	if n := jobCount(); n != 1 {
		t.Fatalf("rejected generations created jobs: %d", n)
	}
	// The outline job above is still pending and holds the outline gate; finish it first.
	var outlineJob string
	must(t, c.pool.QueryRowContext(c.ctx, `SELECT id FROM generation_jobs WHERE project_id=$1::uuid`, c.project.ID).Scan(&outlineJob))
	must(t, c.repo.TransitionGeneration(c.ctx, outlineJob, "done", nil, nil, nil))
	fresh, err := c.repo.GetChapter(c.ctx, chapter.ID)
	must(t, err)
	must(t, c.repo.ApproveStage(c.ctx, &domain.StageApproval{ProjectID: c.project.ID, EntityType: "chapter_outline", EntityID: chapter.ID, ApprovedByUserID: c.user.ID, ApprovedAt: time.Now().UTC()}, fresh.UpdatedAt))
	if err := create("panel_text", chapter.ID, "panels"); err != nil {
		t.Fatalf("panels after outline approval: %v", err)
	}

	// Editing the outline stales that approval inside the same gate.
	var panelJob string
	must(t, c.pool.QueryRowContext(c.ctx, `SELECT id FROM generation_jobs WHERE project_id=$1::uuid AND job_type='panel_text'`, c.project.ID).Scan(&panelJob))
	must(t, c.repo.TransitionGeneration(c.ctx, panelJob, "done", nil, nil, nil))
	edit, err := c.repo.GetChapter(c.ctx, chapter.ID)
	must(t, err)
	edit.Summary = "rewritten"
	must(t, c.repo.UpdateChapter(c.ctx, edit))
	if err := create("panel_text", chapter.ID, "panels"); !errors.Is(err, domain.ErrMissingPrerequisiteApproval) && !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("panels after outline edit: %v", err)
	}
}

func TestRealDBApprovalAndAllocationFlowThroughHTTP(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := &domain.User{Email: uuid.NewString() + "@realdb.test", PasswordHash: "test-hash"}
	must(t, repo.CreateUser(ctx, user))
	script := strings.Repeat("A hero follows a bird through the quiet town. ", 30)
	project := &domain.Project{UserID: user.ID, Title: "Flow", OriginalScript: script, ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
	must(t, repo.CreateProject(ctx, project))
	character := &domain.Character{ProjectID: project.ID, Name: "Hero"}
	must(t, repo.CreateCharacter(ctx, character))
	ai := gen.NewFakeAIServiceClient()
	must(t, repo.RecordAdjustment(ctx, user.ID, project.ID, 50))
	server := api.NewServer(repo, credits.NewPersistentCreditService(repo), jobs.NewPersistentJobService(repo), ai, auth.NewDevAuthenticator(true, user.ID), auth.NewDefaultImageHosting("/media"), 2)
	handler := server.NewRouter()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("X-User-ID", user.ID)
		handler.ServeHTTP(rec, req)
		return rec
	}
	expect := func(rec *httptest.ResponseRecorder, want int, what string) {
		t.Helper()
		if rec.Code != want {
			t.Fatalf("%s: HTTP %d, want %d: %s", what, rec.Code, want, rec.Body.String())
		}
	}

	expect(do("POST", "/characters/"+character.ID+"/approve", ""), http.StatusBadRequest, "approve character without reference image")
	expect(do("POST", "/characters/"+character.ID+"/reference-image", ""), http.StatusAccepted, "reference image")
	server.WaitDispatches() // the receipt precedes the provider call; approval needs the saved image
	expect(do("POST", "/characters/"+character.ID+"/approve", ""), http.StatusOK, "approve character")

	outline := func() string {
		expect(do("POST", "/projects/"+project.ID+"/chapters", ""), http.StatusOK, "outline")
		chapters, err := repo.ListChapters(ctx, project.ID)
		must(t, err)
		if len(chapters) != 1 {
			t.Fatalf("outline must leave one chapter, got %d", len(chapters))
		}
		return chapters[0].ID
	}
	outline()
	chapterID := outline() // regeneration: a second run must not trip the unique chapter number
	expect(do("POST", "/chapters/"+chapterID+"/panels", ""), http.StatusBadRequest, "panels before outline approval")
	expect(do("POST", "/chapters/"+chapterID+"/approve", ""), http.StatusOK, "approve outline")
	expect(do("POST", "/chapters/"+chapterID+"/panels", ""), http.StatusOK, "panels")
	expect(do("POST", "/chapters/"+chapterID+"/panels", ""), http.StatusOK, "panel regeneration") // unique panel/page numbers

	pages, err := repo.ListPages(ctx, chapterID)
	must(t, err)
	if len(pages) < 2 {
		t.Fatalf("need two pages, got %d", len(pages))
	}
	panels, err := repo.ListPanels(ctx, chapterID)
	must(t, err)
	assigned := 0
	for _, pg := range pages {
		assigned += len(pg.PanelIDs)
	}
	if assigned != len(panels) {
		t.Fatalf("regeneration left %d panels but %d assignments", len(panels), assigned)
	}

	expect(do("POST", "/pages/"+pages[0].ID+"/draft/approve", ""), http.StatusBadRequest, "approve draft without image")
	expect(do("PATCH", "/pages/"+pages[0].ID, `{"panel_ids":["`+uuid.NewString()+`"]}`), http.StatusBadRequest, "unknown panel")

	// Five panels over two-panel pages give sizes 1, 2, 2: move the second panel of page 2 onto page 1.
	dst, src := pages[0], pages[1]
	if len(dst.PanelIDs) != 1 || len(src.PanelIDs) != 2 {
		t.Fatalf("unexpected partition %d/%d", len(dst.PanelIDs), len(src.PanelIDs))
	}
	moving := src.PanelIDs[1]
	expect(do("PATCH", "/pages/"+dst.ID, `{"panel_ids":["`+dst.PanelIDs[0]+`","`+moving+`"]}`), http.StatusOK, "move a panel between pages")
	after, err := repo.ListPages(ctx, chapterID)
	must(t, err)
	if len(after[0].PanelIDs) != 2 || after[0].PanelIDs[1] != moving || len(after[1].PanelIDs) != 1 || after[1].PanelIDs[0] != src.PanelIDs[0] {
		t.Fatalf("move not applied: %v", after)
	}
	first := after[0].PanelIDs

	// Editing a panel persists its activity time and the generation gate reads it.
	var edited struct {
		Panel domain.Panel `json:"panel"`
	}
	rec := do("PATCH", "/panels/"+first[0], `{"dialogue":"Edited line"}`)
	expect(rec, http.StatusOK, "panel edit")
	must(t, json.Unmarshal(rec.Body.Bytes(), &edited))
	reloaded, err := repo.GetPanel(ctx, first[0])
	must(t, err)
	if reloaded.Dialogue != "Edited line" || reloaded.UpdatedAt.IsZero() || !reloaded.UpdatedAt.Equal(edited.Panel.UpdatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("panel activity lost: reloaded=%v response=%v", reloaded.UpdatedAt, edited.Panel.UpdatedAt)
	}
}
