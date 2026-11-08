package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

const coherenceScript = "A hero finds a bird and follows it across the quiet town to the old lighthouse where a storm waits."

type coherenceFixture struct {
	server  *api.Server
	handler http.Handler
	repo    db.Repository
	ai      *gen.FakeAIServiceClient
	project *domain.Project
	chapter *domain.Chapter
}

func newCoherenceFixture(t *testing.T) *coherenceFixture {
	t.Helper()
	server, handler, repo := setupTestServer(true, "test-user")
	f := &coherenceFixture{server: server, handler: handler, repo: repo, ai: server.AIClient.(*gen.FakeAIServiceClient)}
	f.project = &domain.Project{UserID: "test-user", Title: "Coherence", OriginalScript: coherenceScript, ChapterCount: 1, Status: "draft"}
	if err := repo.CreateProject(nil, f.project); err != nil {
		t.Fatal(err)
	}
	f.chapter = &domain.Chapter{ProjectID: f.project.ID, ChapterNumber: 1, Title: "The bird", Summary: "A bird leads the way", Status: "pending"}
	if err := repo.CreateChapter(nil, f.chapter); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *coherenceFixture) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
	req.Header.Set("X-User-ID", "test-user")
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f *coherenceFixture) character(t *testing.T, imageURL string) *domain.Character {
	t.Helper()
	ch := &domain.Character{ProjectID: f.project.ID, Name: "Hero", ReferenceImageURL: imageURL}
	if err := f.repo.CreateCharacter(nil, ch); err != nil {
		t.Fatal(err)
	}
	return ch
}

func (f *coherenceFixture) noApproval(t *testing.T, entityType, entityID string) {
	t.Helper()
	if a, err := f.repo.GetLatestApproval(nil, entityType, entityID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("%s %s must have no approval, got %+v err=%v", entityType, entityID, a, err)
	}
}

func TestApprovalsRequireTheArtifactTheyApprove(t *testing.T) {
	f := newCoherenceFixture(t)
	bare := f.character(t, "")
	panel := &domain.Panel{ChapterID: f.chapter.ID, PanelNumber: 1, SceneDescription: "A bird"}
	if err := f.repo.CreatePanel(nil, panel); err != nil {
		t.Fatal(err)
	}
	page := &domain.Page{ChapterID: f.chapter.ID, PageNumber: 1, PanelIDs: []string{panel.ID}}
	if err := f.repo.CreatePage(nil, page); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, entityType, entityID string }{
		{"/characters/" + bare.ID + "/approve", "character_reference", bare.ID},
		{"/pages/" + page.ID + "/draft/approve", "draft_page", page.ID},
		{"/pages/" + page.ID + "/line-art/approve?variant=ink", "line_art_page", page.ID},
	} {
		if rec := f.do(t, "POST", tc.path, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s without an artifact: HTTP %d %s", tc.path, rec.Code, rec.Body.String())
		}
		f.noApproval(t, tc.entityType, tc.entityID)
	}
	loaded, _ := f.repo.GetCharacter(nil, bare.ID)
	if loaded.ApprovedAt != nil {
		t.Fatal("character without a reference image was stamped approved")
	}

	withImage := f.character(t, "https://storage.example/hero.png")
	if rec := f.do(t, "POST", "/characters/"+withImage.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve with reference: HTTP %d %s", rec.Code, rec.Body.String())
	}
}

func TestApprovalFailsWhenEntityChangesBetweenCheckAndCommit(t *testing.T) {
	f := newCoherenceFixture(t)
	ch := f.character(t, "https://storage.example/hero.png")
	loaded, _ := f.repo.GetCharacter(nil, ch.ID)
	loaded.Description = "edited after the approval was validated"
	loaded.UpdatedAt = loaded.UpdatedAt.Add(time.Second)
	if err := f.repo.UpdateCharacter(nil, loaded); err != nil {
		t.Fatal(err)
	}
	err := f.repo.ApproveStage(nil, &domain.StageApproval{ProjectID: f.project.ID, EntityType: "character_reference", EntityID: ch.ID, ApprovedAt: time.Now().UTC()}, ch.UpdatedAt)
	if !errors.Is(err, db.ErrConflict) {
		t.Fatalf("approval of an outdated read: %v", err)
	}
	f.noApproval(t, "character_reference", ch.ID)
	if after, _ := f.repo.GetCharacter(nil, ch.ID); after.ApprovedAt != nil {
		t.Fatal("conflicting approval stamped the character")
	}
}

func TestOutlineAndPanelGenerationRequireApprovedInputs(t *testing.T) {
	f := newCoherenceFixture(t)
	outlineCalls, panelCalls := 0, 0
	f.ai.GenerateChapterOutlineFunc = func(context.Context, gen.GenerateChapterOutlineRequest) (*gen.GenerateChapterOutlineResponse, error) {
		outlineCalls++
		return &gen.GenerateChapterOutlineResponse{}, nil
	}
	f.ai.GeneratePanelTextFunc = func(_ context.Context, req gen.GeneratePanelTextRequest) (*gen.GeneratePanelTextResponse, error) {
		panelCalls++
		var panels []gen.PanelTextItem
		for i := 1; i <= *req.PanelCount; i++ {
			panels = append(panels, gen.PanelTextItem{PanelNumber: i, SceneDescription: "scene"})
		}
		return &gen.GeneratePanelTextResponse{JobID: req.JobID, Panels: panels}, nil
	}

	// A project with no characters cannot skip the character gate.
	if rec := f.do(t, "POST", "/projects/"+f.project.ID+"/chapters", ""); rec.Code != http.StatusBadRequest || outlineCalls != 0 {
		t.Fatalf("outline without characters: HTTP %d calls=%d %s", rec.Code, outlineCalls, rec.Body.String())
	}

	// Panels need the outline approval; none yet.
	if rec := f.do(t, "POST", "/chapters/"+f.chapter.ID+"/panels", ""); rec.Code != http.StatusBadRequest || panelCalls != 0 {
		t.Fatalf("panels before outline approval: HTTP %d calls=%d %s", rec.Code, panelCalls, rec.Body.String())
	}
	if rec := f.do(t, "POST", "/chapters/"+f.chapter.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve outline: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "POST", "/chapters/"+f.chapter.ID+"/panels", ""); rec.Code != http.StatusOK || panelCalls != 1 {
		t.Fatalf("panels after outline approval: HTTP %d calls=%d %s", rec.Code, panelCalls, rec.Body.String())
	}

	// Editing the outline stales the approval again.
	if rec := f.do(t, "PATCH", "/chapters/"+f.chapter.ID, `{"summary":"A different story"}`); rec.Code != http.StatusOK {
		t.Fatalf("edit chapter: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "POST", "/chapters/"+f.chapter.ID+"/panels", ""); rec.Code != http.StatusBadRequest || panelCalls != 1 {
		t.Fatalf("panels after outline edit: HTTP %d calls=%d %s", rec.Code, panelCalls, rec.Body.String())
	}
}

func TestRegenerationReplacesOneRevisionAndNeverDeletesArt(t *testing.T) {
	f := newCoherenceFixture(t)
	ch := f.character(t, "https://storage.example/hero.png")
	if rec := f.do(t, "POST", "/characters/"+ch.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve character: HTTP %d %s", rec.Code, rec.Body.String())
	}

	first := f.chapterIDs(t)
	for run := 0; run < 2; run++ {
		if rec := f.do(t, "POST", "/projects/"+f.project.ID+"/chapters", ""); rec.Code != http.StatusOK {
			t.Fatalf("outline run %d: HTTP %d %s", run, rec.Code, rec.Body.String())
		}
	}
	ids := f.chapterIDs(t)
	if len(ids) != 1 || ids[0] == first[0] {
		t.Fatalf("outline regeneration must leave exactly one new revision, got %v (was %v)", ids, first)
	}
	chapterID := ids[0]
	if rec := f.do(t, "POST", "/chapters/"+chapterID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve outline: HTTP %d %s", rec.Code, rec.Body.String())
	}

	var lastPanels []string
	for run := 0; run < 2; run++ {
		rec := f.do(t, "POST", "/chapters/"+chapterID+"/panels", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("panel run %d: HTTP %d %s", run, rec.Code, rec.Body.String())
		}
		panels, _ := f.repo.ListPanels(nil, chapterID)
		pages, _ := f.repo.ListPages(nil, chapterID)
		seen := map[string]int{}
		for _, pg := range pages {
			for _, id := range pg.PanelIDs {
				seen[id]++
			}
		}
		if len(panels) == 0 || len(seen) != len(panels) {
			t.Fatalf("run %d: %d panels but %d distinct assigned", run, len(panels), len(seen))
		}
		for _, p := range panels {
			if seen[p.ID] != 1 {
				t.Fatalf("run %d: panel %s assigned %d times", run, p.ID, seen[p.ID])
			}
		}
		if run == 1 && len(lastPanels) > 0 && lastPanels[0] == panels[0].ID {
			t.Fatal("regeneration kept the previous revision's panels")
		}
		lastPanels = []string{panels[0].ID}
	}

	// Once a page has art, neither regeneration may delete it, and no paid call is made.
	pages, _ := f.repo.ListPages(nil, chapterID)
	pages[0].DraftImageURL = "https://storage.example/draft.png"
	if err := f.repo.UpdatePage(nil, pages[0]); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.ai.GenerateChapterOutlineFunc = func(context.Context, gen.GenerateChapterOutlineRequest) (*gen.GenerateChapterOutlineResponse, error) {
		calls++
		return &gen.GenerateChapterOutlineResponse{}, nil
	}
	f.ai.GeneratePanelTextFunc = func(context.Context, gen.GeneratePanelTextRequest) (*gen.GeneratePanelTextResponse, error) {
		calls++
		return &gen.GeneratePanelTextResponse{}, nil
	}
	for _, path := range []string{"/projects/" + f.project.ID + "/chapters", "/chapters/" + chapterID + "/panels"} {
		if rec := f.do(t, "POST", path, ""); rec.Code != http.StatusConflict || calls != 0 {
			t.Fatalf("%s over existing art: HTTP %d calls=%d %s", path, rec.Code, calls, rec.Body.String())
		}
	}
	kept, err := f.repo.GetPage(nil, pages[0].ID)
	if err != nil || kept.DraftImageURL == "" {
		t.Fatalf("existing art was removed: %+v %v", kept, err)
	}
}

func (f *coherenceFixture) chapterIDs(t *testing.T) []string {
	t.Helper()
	chapters, err := f.repo.ListChapters(nil, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range chapters {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestOutlineWithDuplicateNumbersStoresNothing(t *testing.T) {
	f := newCoherenceFixture(t)
	ch := f.character(t, "https://storage.example/hero.png")
	if rec := f.do(t, "POST", "/characters/"+ch.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve character: HTTP %d", rec.Code)
	}
	f.ai.GenerateChapterOutlineFunc = func(_ context.Context, req gen.GenerateChapterOutlineRequest) (*gen.GenerateChapterOutlineResponse, error) {
		return &gen.GenerateChapterOutlineResponse{JobID: req.JobID, Chapters: []gen.ChapterOutlineItem{{ChapterNumber: 1, Title: "A"}, {ChapterNumber: 1, Title: "B"}}}, nil
	}
	before := f.chapterIDs(t)
	if rec := f.do(t, "POST", "/projects/"+f.project.ID+"/chapters", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("duplicate chapter numbers: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if after := f.chapterIDs(t); len(after) != 1 || after[0] != before[0] {
		t.Fatalf("failed regeneration changed stored chapters: %v -> %v", before, after)
	}
}

type allocationFixture struct {
	*coherenceFixture
	panels []*domain.Panel
	pages  []*domain.Page
}

func newAllocationFixture(t *testing.T) *allocationFixture {
	t.Helper()
	f := &allocationFixture{coherenceFixture: newCoherenceFixture(t)}
	for i := 1; i <= 3; i++ {
		p := &domain.Panel{ChapterID: f.chapter.ID, PanelNumber: i, SceneDescription: "scene"}
		if err := f.repo.CreatePanel(nil, p); err != nil {
			t.Fatal(err)
		}
		f.panels = append(f.panels, p)
	}
	approved := time.Now().UTC().Add(time.Hour)
	for n, ids := range [][]string{{f.panels[0].ID, f.panels[1].ID}, {f.panels[2].ID}} {
		pg := &domain.Page{ChapterID: f.chapter.ID, PageNumber: n + 1, PanelIDs: ids, DraftApprovedAt: &approved, LineArtApprovedAt: &approved}
		if err := f.repo.CreatePage(nil, pg); err != nil {
			t.Fatal(err)
		}
		f.pages = append(f.pages, pg)
	}
	other := &domain.Chapter{ProjectID: f.project.ID, ChapterNumber: 2, Title: "Other"}
	if err := f.repo.CreateChapter(nil, other); err != nil {
		t.Fatal(err)
	}
	foreign := &domain.Panel{ChapterID: other.ID, PanelNumber: 1, SceneDescription: "foreign"}
	if err := f.repo.CreatePanel(nil, foreign); err != nil {
		t.Fatal(err)
	}
	f.panels = append(f.panels, foreign)
	return f
}

func (f *allocationFixture) allocation(t *testing.T) [][]string {
	t.Helper()
	pages, err := f.repo.ListPages(nil, f.chapter.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, pg := range pages {
		out = append(out, pg.PanelIDs)
	}
	return out
}

func (f *allocationFixture) patch(t *testing.T, page *domain.Page, body string) *httptest.ResponseRecorder {
	return f.do(t, "PATCH", "/pages/"+page.ID, body)
}

func idsJSON(ids ...string) string {
	b, _ := json.Marshal(map[string][]string{"panel_ids": ids})
	return string(b)
}

func TestPageAllocationRejectsInvalidEditsWithoutPartialWrites(t *testing.T) {
	f := newAllocationFixture(t)
	p1, p2, p3, foreign := f.panels[0].ID, f.panels[1].ID, f.panels[2].ID, f.panels[3].ID
	before := f.allocation(t)
	for name, tc := range map[string]struct{ page, body string }{
		"malformed JSON":          {f.pages[0].ID, `{"panel_ids":`},
		"missing panel_ids":       {f.pages[0].ID, `{}`},
		"null panel_ids":          {f.pages[0].ID, `{"panel_ids":null}`},
		"unknown field":           {f.pages[0].ID, `{"panel_ids":["` + p1 + `","` + p2 + `"],"extra":1}`},
		"nonexistent panel":       {f.pages[0].ID, idsJSON(p1, p2, "00000000-0000-0000-0000-000000000009")},
		"foreign chapter panel":   {f.pages[0].ID, idsJSON(p1, p2, foreign)},
		"duplicate panel":         {f.pages[1].ID, idsJSON(p3, p3)},
		"overfull page":           {f.pages[0].ID, idsJSON(p1, p2, p3)},
		"dropped panel":           {f.pages[0].ID, idsJSON(p1)},
		"empty list":              {f.pages[0].ID, idsJSON()},
		"move empties its source": {f.pages[1].ID, idsJSON(p3, p1)},
	} {
		t.Run(name, func(t *testing.T) {
			f.server.MaxPanelsPerPage = 2
			if name == "move empties its source" {
				// Within capacity, but taking both of page 1's panels would leave it empty.
				f.server.MaxPanelsPerPage = 3
				tc.body = idsJSON(p3, p1, p2)
			}
			rec := f.patch(t, &domain.Page{ID: tc.page}, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP %d %s", rec.Code, rec.Body.String())
			}
			if got := f.allocation(t); !equalAllocations(got, before) {
				t.Fatalf("rejected edit changed the allocation: %v -> %v", before, got)
			}
			page, _ := f.repo.GetPage(nil, f.pages[0].ID)
			if page.DraftApprovedAt == nil {
				t.Fatal("rejected edit staled an approval")
			}
		})
	}
}

func equalAllocations(a, b [][]string) bool {
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

func TestPageAllocationMoveAndReorderAreAtomicAndStaleApprovals(t *testing.T) {
	f := newAllocationFixture(t)
	p1, p2, p3 := f.panels[0].ID, f.panels[1].ID, f.panels[2].ID

	// Reorder within a page.
	if rec := f.patch(t, f.pages[0], idsJSON(p2, p1)); rec.Code != http.StatusOK {
		t.Fatalf("reorder: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p2, p1}, {p3}}) {
		t.Fatalf("after reorder: %v", got)
	}
	reordered, _ := f.repo.GetPage(nil, f.pages[0].ID)
	if reordered.DraftApprovedAt != nil || reordered.LineArtApprovedAt != nil {
		t.Fatal("reordering must stale the page's approvals")
	}
	untouched, _ := f.repo.GetPage(nil, f.pages[1].ID)
	if untouched.DraftApprovedAt == nil {
		t.Fatal("reordering one page staled another")
	}

	// Move p1 to page 2: one request, p1 leaves page 1 and joins page 2 exactly once.
	if rec := f.patch(t, f.pages[1], idsJSON(p3, p1)); rec.Code != http.StatusOK {
		t.Fatalf("move: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p2}, {p3, p1}}) {
		t.Fatalf("after move: %v", got)
	}
	source, _ := f.repo.GetPage(nil, f.pages[0].ID)
	target, _ := f.repo.GetPage(nil, f.pages[1].ID)
	if source.DraftApprovedAt != nil || target.DraftApprovedAt != nil || target.LineArtApprovedAt != nil {
		t.Fatal("a move must stale both pages' approvals")
	}

	// An unchanged allocation is accepted and changes nothing.
	approved := time.Now().UTC().Add(time.Hour)
	target.DraftApprovedAt = &approved
	if err := f.repo.UpdatePage(nil, target); err != nil {
		t.Fatal(err)
	}
	if rec := f.patch(t, f.pages[1], idsJSON(p3, p1)); rec.Code != http.StatusOK {
		t.Fatalf("no-op: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if again, _ := f.repo.GetPage(nil, f.pages[1].ID); again.DraftApprovedAt == nil {
		t.Fatal("a no-op allocation edit staled the approval")
	}
}

func TestPageAllocationEditHonoursConfiguredCapacityAndOwnership(t *testing.T) {
	f := newAllocationFixture(t)
	f.server.MaxPanelsPerPage = 3
	p1, p2, p3 := f.panels[0].ID, f.panels[1].ID, f.panels[2].ID
	if rec := f.patch(t, f.pages[1], idsJSON(p3, p2)); rec.Code != http.StatusOK {
		t.Fatalf("move within raised capacity: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p1}, {p3, p2}}) {
		t.Fatalf("allocation: %v", got)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/pages/"+f.pages[0].ID, strings.NewReader(idsJSON(p1)))
	req.Header.Set("X-User-ID", "user-2")
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner edit: HTTP %d %s", rec.Code, rec.Body.String())
	}
}
