package domain

import "fmt"

// PageLayout is one page's ordered panels, the unit a structural panel edit plans over.
type PageLayout struct {
	ID       string
	PanelIDs []string
}

// PanelLayoutEdit is a planned structural change to a chapter's panels: the chapter's pages after the edit, in
// reading order, and which of them are new, changed or gone. Repositories apply it as one unit.
type PanelLayoutEdit struct {
	Pages []PageLayout
	// Changed lists pages, in reading order, whose panels differ from before, including a created page.
	Changed []string
	// Removed lists pages that no longer exist, because their last panel left or was deleted.
	Removed []string
	// Created is the ID of a page the edit adds, or empty.
	Created string
	// Deleted is the ID of a panel the edit deletes, or empty.
	Deleted string
}

// PanelNumbers numbers the panels of an edited chapter in reading order: page by page, panel by panel.
// A panel's number follows its place, so a move or a delete never leaves the numbers out of order.
func (e *PanelLayoutEdit) PanelNumbers() map[string]int {
	numbers := map[string]int{}
	for _, pg := range e.Pages {
		for _, id := range pg.PanelIDs {
			numbers[id] = len(numbers) + 1
		}
	}
	return numbers
}

func layoutOf(pages []*Page) []PageLayout {
	out := make([]PageLayout, len(pages))
	for i, pg := range pages {
		out[i] = PageLayout{ID: pg.ID, PanelIDs: append([]string(nil), pg.PanelIDs...)}
	}
	return out
}

// placePanel returns ids with panelID at position (0 is first). A position outside the list means the end.
func placePanel(ids []string, panelID string, position int) []string {
	rest := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		if id != panelID {
			rest = append(rest, id)
		}
	}
	if position < 0 || position > len(rest) {
		position = len(rest)
	}
	out := append(rest[:position:position], panelID)
	return append(out, rest[position:]...)
}

// LayoutFromReassignment turns the result of PlanPanelReassignment into a layout edit.
func LayoutFromReassignment(pages []*Page, plan map[string][]string) *PanelLayoutEdit {
	edit := &PanelLayoutEdit{Pages: layoutOf(pages)}
	for i, pg := range edit.Pages {
		if ids, ok := plan[pg.ID]; ok {
			edit.Pages[i].PanelIDs = ids
			edit.Changed = append(edit.Changed, pg.ID)
		}
	}
	return edit
}

// PlanPanelMove places an existing panel of the chapter at position on the target page, moving it off the page
// it was on. The result is validated like any reassignment (SPEC 3.5.1): the panel must belong to the chapter,
// the target may not exceed maxPerPage, and no page may be left empty. pages are in page order.
func PlanPanelMove(pages []*Page, chapterPanels map[string]bool, panelID, targetID string, position, maxPerPage int) (*PanelLayoutEdit, error) {
	var target *Page
	for _, pg := range pages {
		if pg.ID == targetID {
			target = pg
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: page is not part of the chapter", ErrInvalidPanelAllocation)
	}
	plan, err := PlanPanelReassignment(pages, chapterPanels, targetID, placePanel(target.PanelIDs, panelID, position), maxPerPage)
	if err != nil {
		return nil, err
	}
	return LayoutFromReassignment(pages, plan), nil
}

// PlanPanelAdd places a new panel at position on the target page. An empty targetID puts it alone on a new page,
// newPageID, at the end of the chapter. The target may not exceed maxPerPage.
func PlanPanelAdd(pages []*Page, panelID, targetID, newPageID string, position, maxPerPage int) (*PanelLayoutEdit, error) {
	edit := &PanelLayoutEdit{Pages: layoutOf(pages)}
	if targetID == "" {
		edit.Pages = append(edit.Pages, PageLayout{ID: newPageID, PanelIDs: []string{panelID}})
		edit.Changed = []string{newPageID}
		edit.Created = newPageID
		return edit, nil
	}
	for i, pg := range edit.Pages {
		if pg.ID != targetID {
			continue
		}
		if maxPerPage > 0 && len(pg.PanelIDs)+1 > maxPerPage {
			return nil, fmt.Errorf("%w: a page holds at most %d panels", ErrInvalidPanelAllocation, maxPerPage)
		}
		edit.Pages[i].PanelIDs = placePanel(pg.PanelIDs, panelID, position)
		edit.Changed = []string{targetID}
		return edit, nil
	}
	return nil, fmt.Errorf("%w: page is not part of the chapter", ErrInvalidPanelAllocation)
}

// PlanPanelDelete removes a panel from its page. A page whose last panel goes is removed with it. Page and panel
// numbers close the gap. The panel must be on one of the pages.
func PlanPanelDelete(pages []*Page, panelID string) (*PanelLayoutEdit, error) {
	edit := &PanelLayoutEdit{Deleted: panelID}
	found := false
	for _, pg := range layoutOf(pages) {
		kept := make([]string, 0, len(pg.PanelIDs))
		for _, id := range pg.PanelIDs {
			if id != panelID {
				kept = append(kept, id)
			}
		}
		switch {
		case len(kept) == len(pg.PanelIDs):
			edit.Pages = append(edit.Pages, pg)
		case len(kept) == 0:
			found = true
			edit.Removed = append(edit.Removed, pg.ID)
		default:
			found = true
			edit.Pages = append(edit.Pages, PageLayout{ID: pg.ID, PanelIDs: kept})
			edit.Changed = append(edit.Changed, pg.ID)
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: panel %s is not on a page of this chapter", ErrInvalidPanelAllocation, panelID)
	}
	return edit, nil
}
