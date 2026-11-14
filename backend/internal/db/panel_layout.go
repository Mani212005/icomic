package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"icomic/backend/internal/domain"
)

// Structural panel edits (add, move, reorder, delete) plan over a chapter's pages as stored and apply the result
// as one unit: the allocation of every affected page, the staling of their approvals, the touch of their panels
// and the renumbering of pages and panels all commit together or not at all (SPEC 3.5.1, 3.6). Panel numbers
// follow reading order, page by page, so they stay unique and in order after every edit.

type layoutPlanner func(pages []*domain.Page, chapterPanels map[string]bool) (*domain.PanelLayoutEdit, error)

func touchedPanels(before map[string][]string, edit *domain.PanelLayoutEdit) map[string]bool {
	touched := map[string]bool{}
	after := map[string][]string{}
	for _, pg := range edit.Pages {
		after[pg.ID] = pg.PanelIDs
	}
	for _, id := range append(append([]string(nil), edit.Changed...), edit.Removed...) {
		for _, panelID := range before[id] {
			touched[panelID] = true
		}
		for _, panelID := range after[id] {
			touched[panelID] = true
		}
	}
	delete(touched, edit.Deleted)
	return touched
}

func (m *InMemoryRepository) editLayoutLocked(chapterID string, now time.Time, newPanel *domain.Panel, newPage *domain.Page, plan layoutPlanner) (*domain.PanelLayoutEdit, error) {
	if _, ok := m.chapters[chapterID]; !ok {
		return nil, ErrNotFound
	}
	var pages []*domain.Page
	for _, pg := range m.pages {
		if pg.ChapterID == chapterID {
			pages = append(pages, pg)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageNumber < pages[j].PageNumber })
	chapterPanels := map[string]bool{}
	for _, pan := range m.panels {
		if pan.ChapterID == chapterID {
			chapterPanels[pan.ID] = true
		}
	}
	edit, err := plan(pages, chapterPanels)
	if err != nil {
		return nil, err
	}
	for _, id := range edit.Removed {
		if pageHasArt(m.pages[id]) {
			return nil, ErrArtExists
		}
	}
	before := map[string][]string{}
	for _, pg := range pages {
		before[pg.ID] = append([]string(nil), pg.PanelIDs...)
	}
	touched := touchedPanels(before, edit)

	if newPanel != nil {
		newPanel.ChapterID = chapterID
		newPanel.UpdatedAt = now
		cpy := *newPanel
		m.panels[newPanel.ID] = &cpy
	}
	if edit.Deleted != "" {
		delete(m.panels, edit.Deleted)
	}
	for _, id := range edit.Removed {
		delete(m.palettes, id)
		delete(m.regions, id)
		delete(m.pages, id)
	}
	if edit.Created != "" && newPage != nil {
		newPage.ChapterID = chapterID
		newPage.UpdatedAt = now
		if newPage.LetteringMode == "" {
			newPage.LetteringMode = "baked"
		}
		cpy := *newPage
		m.pages[newPage.ID] = &cpy
	}
	for index, layout := range edit.Pages {
		pg := m.pages[layout.ID]
		pg.PageNumber = index + 1
		pg.PanelIDs = append([]string(nil), layout.PanelIDs...)
	}
	for _, id := range edit.Changed {
		if id != edit.Created {
			domain.StaleDraftOnPanelEdit(m.pages[id], now)
		}
	}
	for id := range touched {
		if pan, ok := m.panels[id]; ok {
			pan.UpdatedAt = now
		}
	}
	for id, number := range edit.PanelNumbers() {
		if pan, ok := m.panels[id]; ok {
			pan.PanelNumber = number
		}
	}
	if newPanel != nil {
		newPanel.PanelNumber = m.panels[newPanel.ID].PanelNumber
	}
	return edit, nil
}

// loadPanelNumbers reads a chapter's panels with their stored numbers, read through the transaction.
func loadPanelNumbers(ctx context.Context, tx Transaction, chapterID string) (map[string]int, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'n', panel_number)), '[]'::jsonb) FROM panels WHERE chapter_id=$1::uuid`, chapterID).Scan(&raw); err != nil {
		return nil, err
	}
	var rows []struct {
		ID string `json:"id"`
		N  int    `json:"n"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	numbers := make(map[string]int, len(rows))
	for _, r := range rows {
		numbers[r.ID] = r.N
	}
	return numbers, nil
}

func (p *PostgresRepository) editLayout(ctx context.Context, chapterID string, now time.Time, newPanel *domain.Panel, newPage *domain.Page, plan layoutPlanner) (*domain.PanelLayoutEdit, error) {
	now = now.UTC().Truncate(time.Microsecond)
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize structural edits per chapter: the chapter row is the lock.
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM chapters WHERE id=$1::uuid FOR UPDATE`, chapterID).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	pages, chapterPanels, err := loadAllocation(ctx, tx, chapterID)
	if err != nil {
		return nil, err
	}
	edit, err := plan(pages, chapterPanels)
	if err != nil {
		return nil, err
	}
	if len(edit.Removed) > 0 {
		removed, _ := json.Marshal(edit.Removed)
		var art bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages p WHERE p.id IN (SELECT jsonb_array_elements_text($1::jsonb)::uuid) AND `+pageArtPredicate+`)`, removed).Scan(&art); err != nil {
			return nil, err
		}
		if art {
			return nil, ErrArtExists
		}
	}
	numbers, err := loadPanelNumbers(ctx, tx, chapterID)
	if err != nil {
		return nil, err
	}
	before := map[string][]string{}
	pageNumbers := map[string]int{}
	lastPage := 0
	for _, pg := range pages {
		before[pg.ID] = pg.PanelIDs
		pageNumbers[pg.ID] = pg.PageNumber
		lastPage = max(lastPage, pg.PageNumber)
	}
	touched := touchedPanels(before, edit)

	if newPanel != nil {
		// Provisional number past every stored one; the renumbering below puts it in reading order.
		provisional := 0
		for _, n := range numbers {
			provisional = max(provisional, n)
		}
		provisional++
		numbers[newPanel.ID] = provisional
		if _, err := tx.ExecContext(ctx, `INSERT INTO panels (id, chapter_id, panel_number, scene_description, character_actions, camera_angle, background, dialogue, narration, image_prompt, status, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			newPanel.ID, chapterID, provisional, newPanel.SceneDescription, newPanel.CharacterActions, newPanel.CameraAngle, newPanel.Background, newPanel.Dialogue, newPanel.Narration, newPanel.ImagePrompt, newPanel.Status, now); err != nil {
			return nil, err
		}
		newPanel.ChapterID = chapterID
		newPanel.UpdatedAt = now
	}
	if edit.Deleted != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM panels WHERE id=$1::uuid`, edit.Deleted); err != nil {
			return nil, err
		}
		delete(numbers, edit.Deleted)
	}
	for _, id := range edit.Removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM pages WHERE id=$1::uuid`, id); err != nil {
			return nil, err
		}
		delete(pageNumbers, id)
	}
	if edit.Created != "" && newPage != nil {
		pidsBytes, _ := json.Marshal(newPage.PanelIDs)
		if _, err := tx.ExecContext(ctx, `INSERT INTO pages (id, chapter_id, page_number, panel_ids, lettering_mode, lettering, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			newPage.ID, chapterID, lastPage+1, pidsBytes, pageLetteringMode(newPage), nullableJSON(newPage.Lettering), now); err != nil {
			return nil, err
		}
		pageNumbers[newPage.ID] = lastPage + 1
		newPage.ChapterID = chapterID
		newPage.UpdatedAt = now
	}
	changed := map[string]bool{}
	for _, id := range edit.Changed {
		changed[id] = id != edit.Created
	}
	for _, layout := range edit.Pages {
		if !changed[layout.ID] {
			continue
		}
		pidsBytes, _ := json.Marshal(layout.PanelIDs)
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET panel_ids=$2, draft_approved_at=NULL, line_art_approved_at=NULL, painted_page_approved_at=NULL, updated_at=$3 WHERE id=$1::uuid`, layout.ID, pidsBytes, now); err != nil {
			return nil, err
		}
	}
	// Page numbers only ever move down, in order, so each one is free by the time it is taken.
	for index, layout := range edit.Pages {
		if pageNumbers[layout.ID] == index+1 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET page_number=$2 WHERE id=$1::uuid`, layout.ID, index+1); err != nil {
			return nil, err
		}
	}
	// Panel numbers can swap places, so the changed ones leave the range first and then land on their number.
	type renumber struct {
		ID string `json:"id"`
		N  int    `json:"n"`
	}
	var moveOut, moveIn []renumber
	for id, want := range edit.PanelNumbers() {
		if numbers[id] != want {
			moveOut = append(moveOut, renumber{id, want + 1_000_000})
			moveIn = append(moveIn, renumber{id, want})
		}
	}
	for _, step := range [][]renumber{moveOut, moveIn} {
		if len(step) == 0 {
			continue
		}
		payload, _ := json.Marshal(step)
		if _, err := tx.ExecContext(ctx, `UPDATE panels p SET panel_number=v.n FROM jsonb_to_recordset($1::jsonb) AS v(id uuid, n int) WHERE p.id=v.id`, payload); err != nil {
			return nil, err
		}
	}
	if len(touched) > 0 {
		ids := make([]string, 0, len(touched))
		for id := range touched {
			ids = append(ids, id)
		}
		payload, _ := json.Marshal(ids)
		if _, err := tx.ExecContext(ctx, `UPDATE panels SET updated_at=$2 WHERE id IN (SELECT jsonb_array_elements_text($1::jsonb)::uuid)`, payload, now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if newPanel != nil {
		newPanel.PanelNumber = edit.PanelNumbers()[newPanel.ID]
	}
	return edit, nil
}

// AddPanel stores a new panel at position on the target page, or alone on a new page at the end of the chapter
// when targetPageID is empty (newPage supplies that page). The pages it changes are staled with it.
func (m *InMemoryRepository) AddPanel(_ context.Context, chapterID string, pan *domain.Panel, newPage *domain.Page, targetPageID string, position, maxPerPage int, now time.Time) (*domain.PanelLayoutEdit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	newPageID := ""
	if newPage != nil {
		newPageID = newPage.ID
		newPage.PanelIDs = []string{pan.ID}
	}
	return m.editLayoutLocked(chapterID, now, pan, newPage, func(pages []*domain.Page, _ map[string]bool) (*domain.PanelLayoutEdit, error) {
		return domain.PlanPanelAdd(pages, pan.ID, targetPageID, newPageID, position, maxPerPage)
	})
}

func (p *PostgresRepository) AddPanel(ctx context.Context, chapterID string, pan *domain.Panel, newPage *domain.Page, targetPageID string, position, maxPerPage int, now time.Time) (*domain.PanelLayoutEdit, error) {
	newPageID := ""
	if newPage != nil {
		newPageID = newPage.ID
		newPage.PanelIDs = []string{pan.ID}
	}
	return p.editLayout(ctx, chapterID, now, pan, newPage, func(pages []*domain.Page, _ map[string]bool) (*domain.PanelLayoutEdit, error) {
		return domain.PlanPanelAdd(pages, pan.ID, targetPageID, newPageID, position, maxPerPage)
	})
}

// MovePanel places an existing panel at position on the target page, moving it off the page it was on. It is
// ReassignPanels for a single panel: the same validation, staling and touching, planned from the stored pages.
func (m *InMemoryRepository) MovePanel(_ context.Context, chapterID, panelID, targetPageID string, position, maxPerPage int, now time.Time) (*domain.PanelLayoutEdit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.editLayoutLocked(chapterID, now, nil, nil, func(pages []*domain.Page, chapterPanels map[string]bool) (*domain.PanelLayoutEdit, error) {
		return domain.PlanPanelMove(pages, chapterPanels, panelID, targetPageID, position, maxPerPage)
	})
}

func (p *PostgresRepository) MovePanel(ctx context.Context, chapterID, panelID, targetPageID string, position, maxPerPage int, now time.Time) (*domain.PanelLayoutEdit, error) {
	return p.editLayout(ctx, chapterID, now, nil, nil, func(pages []*domain.Page, chapterPanels map[string]bool) (*domain.PanelLayoutEdit, error) {
		return domain.PlanPanelMove(pages, chapterPanels, panelID, targetPageID, position, maxPerPage)
	})
}

// DeletePanel removes a panel and closes the gap. A page whose last panel goes is removed too, which is refused
// with ErrArtExists when that page holds generated art. ErrNotFound when the panel is not in the chapter.
func (m *InMemoryRepository) DeletePanel(_ context.Context, chapterID, panelID string, now time.Time) (*domain.PanelLayoutEdit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.editLayoutLocked(chapterID, now, nil, nil, func(pages []*domain.Page, chapterPanels map[string]bool) (*domain.PanelLayoutEdit, error) {
		if !chapterPanels[panelID] {
			return nil, ErrNotFound
		}
		return domain.PlanPanelDelete(pages, panelID)
	})
}

func (p *PostgresRepository) DeletePanel(ctx context.Context, chapterID, panelID string, now time.Time) (*domain.PanelLayoutEdit, error) {
	return p.editLayout(ctx, chapterID, now, nil, nil, func(pages []*domain.Page, chapterPanels map[string]bool) (*domain.PanelLayoutEdit, error) {
		if !chapterPanels[panelID] {
			return nil, ErrNotFound
		}
		return domain.PlanPanelDelete(pages, panelID)
	})
}
