//go:build realdb

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

// readingOrder lists a chapter's panel IDs by stored panel number, and fails when the numbers are not exactly
// 1..N, which is what every structural edit must leave behind.
func (c *coherenceDB) readingOrder(t *testing.T, chapterID string) []string {
	t.Helper()
	panels, err := c.repo.ListPanels(c.ctx, chapterID)
	must(t, err)
	var ids []string
	for i, p := range panels {
		if p.PanelNumber != i+1 {
			t.Fatalf("panel numbers must be 1..N in order, got %d at position %d", p.PanelNumber, i+1)
		}
		ids = append(ids, p.ID)
	}
	return ids
}

// pageNumbers fails unless a chapter's pages are numbered 1..N.
func (c *coherenceDB) pageNumbers(t *testing.T, chapterID string) {
	t.Helper()
	pages, err := c.repo.ListPages(c.ctx, chapterID)
	must(t, err)
	for i, pg := range pages {
		if pg.PageNumber != i+1 {
			t.Fatalf("page numbers must be 1..N in order, got %d at position %d", pg.PageNumber, i+1)
		}
	}
}

func newManualPanel(chapterID, scene string) *domain.Panel {
	return &domain.Panel{ID: uuid.NewString(), ChapterID: chapterID, SceneDescription: scene, Status: "ready"}
}

func equalIDs(a, b []string) bool { return sameAllocation([][]string{a}, [][]string{b}) }

func TestRealDBAddPanelPlacesStalesAndRenumbersInReadingOrder(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	panels, pages := c.allocation(t, chapter, 2, 1)
	p1, p2, p3 := panels[0].ID, panels[1].ID, panels[2].ID
	now := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)

	added := newManualPanel(chapter.ID, "A gull lands")
	edit, err := c.repo.AddPanel(c.ctx, chapter.ID, added, nil, pages[1].ID, 0, 5, now)
	must(t, err)
	if !equalIDs(edit.Changed, []string{pages[1].ID}) || edit.Created != "" {
		t.Fatalf("edit: %+v", edit)
	}
	if got := c.pageIDs(t, chapter.ID); !sameAllocation(got, [][]string{{p1, p2}, {added.ID, p3}}) {
		t.Fatalf("allocation: %v", got)
	}
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p1, p2, added.ID, p3}) {
		t.Fatalf("reading order: %v", got)
	}
	if added.PanelNumber != 3 {
		t.Fatalf("the new panel reports its final number, got %d", added.PanelNumber)
	}
	hit, _ := c.repo.GetPage(c.ctx, pages[1].ID)
	if hit.DraftApprovedAt != nil || hit.LineArtApprovedAt != nil || !hit.UpdatedAt.Equal(now) {
		t.Fatalf("the page that received the panel must be staled: %+v", hit)
	}
	other, _ := c.repo.GetPage(c.ctx, pages[0].ID)
	if other.DraftApprovedAt == nil {
		t.Fatal("an unrelated page was staled")
	}
	stored, err := c.repo.GetPanel(c.ctx, added.ID)
	must(t, err)
	if stored.SceneDescription != "A gull lands" || stored.Status != "ready" || !stored.UpdatedAt.Equal(now) {
		t.Fatalf("stored panel: %+v", stored)
	}

	// Alone on a new page at the end, in the lettering mode the caller chose.
	last := newManualPanel(chapter.ID, "The lighthouse")
	newPage := &domain.Page{ID: uuid.NewString(), LetteringMode: "overlay"}
	edit, err = c.repo.AddPanel(c.ctx, chapter.ID, last, newPage, "", -1, 5, now.Add(time.Minute))
	must(t, err)
	if edit.Created != newPage.ID {
		t.Fatalf("edit: %+v", edit)
	}
	created, err := c.repo.GetPage(c.ctx, newPage.ID)
	must(t, err)
	if created.PageNumber != 3 || created.LetteringMode != "overlay" || !equalIDs(created.PanelIDs, []string{last.ID}) {
		t.Fatalf("created page: %+v", created)
	}
	c.pageNumbers(t, chapter.ID)
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p1, p2, added.ID, p3, last.ID}) {
		t.Fatalf("reading order: %v", got)
	}
}

func TestRealDBAddPanelRejectionsStoreNothing(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	other := c.chapter(t, 2)
	panels, pages := c.allocation(t, chapter, 2)
	_, foreign := c.allocation(t, other, 1)
	now := time.Now().UTC().Add(time.Minute)

	for name, tc := range map[string]struct {
		chapter, page string
		max           int
		want          error
	}{
		"full page":       {chapter.ID, pages[0].ID, 2, domain.ErrInvalidPanelAllocation},
		"foreign page":    {chapter.ID, foreign[0].ID, 5, domain.ErrInvalidPanelAllocation},
		"unknown page":    {chapter.ID, uuid.NewString(), 5, domain.ErrInvalidPanelAllocation},
		"unknown chapter": {uuid.NewString(), pages[0].ID, 5, db.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.repo.AddPanel(c.ctx, tc.chapter, newManualPanel(tc.chapter, "x"), nil, tc.page, 0, tc.max, now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v", err)
			}
			if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{panels[0].ID, panels[1].ID}) {
				t.Fatalf("a rejected add stored a panel: %v", got)
			}
			if pg, _ := c.repo.GetPage(c.ctx, pages[0].ID); pg.DraftApprovedAt == nil {
				t.Fatal("a rejected add staled an approval")
			}
		})
	}
}

func TestRealDBMovePanelReordersAndMovesInReadingOrder(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	other := c.chapter(t, 2)
	panels, pages := c.allocation(t, chapter, 2, 2)
	foreign, _ := c.allocation(t, other, 1)
	p1, p2, p3, p4 := panels[0].ID, panels[1].ID, panels[2].ID, panels[3].ID
	now := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)

	// Reorder within a page.
	edit, err := c.repo.MovePanel(c.ctx, chapter.ID, p2, pages[0].ID, 0, 5, now)
	must(t, err)
	if !equalIDs(edit.Changed, []string{pages[0].ID}) {
		t.Fatalf("a reorder changes one page: %+v", edit)
	}
	if got := c.pageIDs(t, chapter.ID); !sameAllocation(got, [][]string{{p2, p1}, {p3, p4}}) {
		t.Fatalf("after reorder: %v", got)
	}
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p2, p1, p3, p4}) {
		t.Fatalf("reading order follows the reorder: %v", got)
	}
	if pg, _ := c.repo.GetPage(c.ctx, pages[1].ID); pg.DraftApprovedAt == nil {
		t.Fatal("reordering one page staled another")
	}

	// Move across pages, to the front of the target.
	edit, err = c.repo.MovePanel(c.ctx, chapter.ID, p1, pages[1].ID, 0, 5, now.Add(time.Minute))
	must(t, err)
	if len(edit.Changed) != 2 {
		t.Fatalf("a move changes both pages: %+v", edit)
	}
	if got := c.pageIDs(t, chapter.ID); !sameAllocation(got, [][]string{{p2}, {p1, p3, p4}}) {
		t.Fatalf("after move: %v", got)
	}
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p2, p1, p3, p4}) {
		t.Fatalf("reading order: %v", got)
	}
	for _, id := range []string{pages[0].ID, pages[1].ID} {
		if pg, _ := c.repo.GetPage(c.ctx, id); pg.DraftApprovedAt != nil || pg.LineArtApprovedAt != nil {
			t.Fatalf("a move must stale both pages: %+v", pg)
		}
	}

	for name, tc := range map[string]struct {
		panel, page string
		max         int
	}{
		"empties its source":  {p2, pages[1].ID, 5},
		"over capacity":       {p2, pages[1].ID, 3},
		"foreign panel":       {foreign[0].ID, pages[0].ID, 5},
		"unknown panel":       {uuid.NewString(), pages[0].ID, 5},
		"page of the chapter": {p3, uuid.NewString(), 5},
	} {
		t.Run(name, func(t *testing.T) {
			before := c.pageIDs(t, chapter.ID)
			if _, err := c.repo.MovePanel(c.ctx, chapter.ID, tc.panel, tc.page, 0, tc.max, now); !errors.Is(err, domain.ErrInvalidPanelAllocation) {
				t.Fatalf("got %v", err)
			}
			if got := c.pageIDs(t, chapter.ID); !sameAllocation(got, before) {
				t.Fatalf("a rejected move changed the allocation: %v", got)
			}
		})
	}
}

func TestRealDBDeletePanelClosesGapsAndProtectsArt(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	other := c.chapter(t, 2)
	panels, pages := c.allocation(t, chapter, 2, 1, 2)
	foreign, _ := c.allocation(t, other, 1)
	p1, p2, p3, p4, p5 := panels[0].ID, panels[1].ID, panels[2].ID, panels[3].ID, panels[4].ID
	now := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)

	// One of several panels leaves; its page is staled, the others are not.
	edit, err := c.repo.DeletePanel(c.ctx, chapter.ID, p1, now)
	must(t, err)
	if !equalIDs(edit.Changed, []string{pages[0].ID}) || len(edit.Removed) != 0 {
		t.Fatalf("edit: %+v", edit)
	}
	if _, err := c.repo.GetPanel(c.ctx, p1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("the panel is gone: %v", err)
	}
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p2, p3, p4, p5}) {
		t.Fatalf("numbers close the gap: %v", got)
	}
	if pg, _ := c.repo.GetPage(c.ctx, pages[0].ID); pg.DraftApprovedAt != nil || !pg.UpdatedAt.Equal(now) {
		t.Fatalf("the edited page must be staled: %+v", pg)
	}
	if pg, _ := c.repo.GetPage(c.ctx, pages[2].ID); pg.DraftApprovedAt == nil {
		t.Fatal("an unrelated page was staled")
	}

	// A page whose panel is deleted without art goes with it; later pages move up.
	edit, err = c.repo.DeletePanel(c.ctx, chapter.ID, p3, now.Add(time.Minute))
	must(t, err)
	if !equalIDs(edit.Removed, []string{pages[1].ID}) {
		t.Fatalf("edit: %+v", edit)
	}
	if _, err := c.repo.GetPage(c.ctx, pages[1].ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("the emptied page is removed: %v", err)
	}
	c.pageNumbers(t, chapter.ID)
	if got := c.pageIDs(t, chapter.ID); !sameAllocation(got, [][]string{{p2}, {p4, p5}}) {
		t.Fatalf("allocation: %v", got)
	}
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p2, p4, p5}) {
		t.Fatalf("reading order: %v", got)
	}

	// Never silently delete paid art: the last panel of a page with art stays.
	last, err := c.repo.GetPage(c.ctx, pages[0].ID)
	must(t, err)
	last.DraftImageURL = "/media/draft.png"
	must(t, c.repo.UpdatePage(c.ctx, last))
	if _, err := c.repo.DeletePanel(c.ctx, chapter.ID, p2, now.Add(2*time.Minute)); !errors.Is(err, db.ErrArtExists) {
		t.Fatalf("got %v", err)
	}
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p2, p4, p5}) {
		t.Fatalf("a refused delete changed the chapter: %v", got)
	}
	if _, err := c.repo.GetPage(c.ctx, pages[0].ID); err != nil {
		t.Fatalf("the page with art survives: %v", err)
	}

	// Another chapter's panel, or none at all, is not found.
	for _, id := range []string{foreign[0].ID, uuid.NewString()} {
		if _, err := c.repo.DeletePanel(c.ctx, chapter.ID, id, now); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	}
	if _, err := c.repo.GetPanel(c.ctx, foreign[0].ID); err != nil {
		t.Fatalf("another chapter's panel must survive: %v", err)
	}
}

func TestRealDBReassignPanelsKeepsPanelNumbersInReadingOrder(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	panels, pages := c.allocation(t, chapter, 2, 1)
	p1, p2, p3 := panels[0].ID, panels[1].ID, panels[2].ID
	if _, err := c.repo.ReassignPanels(c.ctx, pages[1].ID, []string{p3, p1}, 5, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := c.readingOrder(t, chapter.ID); !equalIDs(got, []string{p2, p3, p1}) {
		t.Fatalf("a move through the page route renumbers too: %v", got)
	}
}

func TestRealDBConcurrentStructuralEditsKeepNumbersUniqueAndComplete(t *testing.T) {
	c := newCoherenceDB(t)
	chapter := c.chapter(t, 1)
	panels, pages := c.allocation(t, chapter, 2, 2)
	const adds = 8
	var added [adds]*domain.Panel
	errs := concurrentRun(t, adds+2, func(ctx context.Context, i int) error {
		now := time.Now().UTC()
		switch {
		case i < adds:
			added[i] = newManualPanel(chapter.ID, "scene")
			_, err := c.repo.AddPanel(ctx, chapter.ID, added[i], &domain.Page{ID: uuid.NewString()}, "", -1, 5, now)
			return err
		case i == adds:
			_, err := c.repo.MovePanel(ctx, chapter.ID, panels[0].ID, pages[1].ID, 0, 5, now)
			return err
		default:
			_, err := c.repo.DeletePanel(ctx, chapter.ID, panels[3].ID, now)
			return err
		}
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("edit %d failed: %v", i, err)
		}
	}
	order := c.readingOrder(t, chapter.ID) // fails unless numbered 1..N
	c.pageNumbers(t, chapter.ID)
	seen := map[string]int{}
	for _, ids := range c.pageIDs(t, chapter.ID) {
		if len(ids) == 0 {
			t.Fatal("a page was left empty")
		}
		for _, id := range ids {
			seen[id]++
		}
	}
	if len(seen) != len(order) {
		t.Fatalf("every panel is on exactly one page: %d on pages, %d stored", len(seen), len(order))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("panel %s is on %d pages", id, n)
		}
	}
	if len(order) != 4-1+adds {
		t.Fatalf("expected %d panels, have %d", 4-1+adds, len(order))
	}
}
