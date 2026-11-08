package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidPanelAllocation marks a requested panel allocation that would lose, duplicate or misplace panels.
var ErrInvalidPanelAllocation = errors.New("invalid panel allocation")

// PlanPanelReassignment validates placing panelIDs, in order, on the target page and returns the resulting
// ordered panel IDs of every page whose allocation changes (SPEC 3.5.1). Listing a panel that sits on another
// page of the chapter moves it, so the chapter's panels stay assigned exactly once. The target must keep every
// panel it already has, no page may be left empty, and maxPerPage (when positive) caps the target page.
// pages are the chapter's pages and chapterPanels its panel IDs. An empty result means nothing changes.
func PlanPanelReassignment(pages []*Page, chapterPanels map[string]bool, targetID string, panelIDs []string, maxPerPage int) (map[string][]string, error) {
	var target *Page
	for _, pg := range pages {
		if pg.ID == targetID {
			target = pg
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: page is not part of the chapter", ErrInvalidPanelAllocation)
	}
	if len(panelIDs) == 0 {
		return nil, fmt.Errorf("%w: a page needs at least one panel", ErrInvalidPanelAllocation)
	}
	if maxPerPage > 0 && len(panelIDs) > maxPerPage {
		return nil, fmt.Errorf("%w: a page holds at most %d panels", ErrInvalidPanelAllocation, maxPerPage)
	}
	wanted := make(map[string]bool, len(panelIDs))
	for _, id := range panelIDs {
		if wanted[id] {
			return nil, fmt.Errorf("%w: panel %s is listed more than once", ErrInvalidPanelAllocation, id)
		}
		if !chapterPanels[id] {
			return nil, fmt.Errorf("%w: panel %s does not belong to this chapter", ErrInvalidPanelAllocation, id)
		}
		wanted[id] = true
	}
	for _, id := range target.PanelIDs {
		if !wanted[id] {
			return nil, fmt.Errorf("%w: panel %s would be left unassigned; move it by listing it on its new page", ErrInvalidPanelAllocation, id)
		}
	}

	changed := map[string][]string{}
	if !equalIDs(target.PanelIDs, panelIDs) {
		changed[target.ID] = append([]string(nil), panelIDs...)
	}
	for _, pg := range pages {
		if pg.ID == targetID {
			continue
		}
		var kept []string
		for _, id := range pg.PanelIDs {
			if !wanted[id] {
				kept = append(kept, id)
			}
		}
		if len(kept) == len(pg.PanelIDs) {
			continue
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("%w: page %d would be left without panels", ErrInvalidPanelAllocation, pg.PageNumber)
		}
		changed[pg.ID] = kept
	}
	return changed, nil
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
