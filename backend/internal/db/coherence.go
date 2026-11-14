package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/domain"
)

// ErrArtExists is returned when replacing a revision would delete generated page art.
var ErrArtExists = fmt.Errorf("%w: generated page art exists", ErrConflict)

func pageHasArt(pg *domain.Page) bool {
	return pg.DraftImageURL != "" || pg.LineArtImageURL != "" || pg.PaintedPageImageURL != "" || pg.ColourImageURL != ""
}

const pageArtPredicate = `(COALESCE(p.draft_image_url,'')<>'' OR COALESCE(p.line_art_image_url,'')<>'' OR COALESCE(p.painted_page_image_url,'')<>'' OR COALESCE(p.colour_image_url,'')<>'')`

// ApproveStage stamps the approval on its entity and appends the stage approval in one commit.
// expectedUpdatedAt is the entity's updated_at as the caller validated it; ErrConflict means it was edited since.
func (m *InMemoryRepository) ApproveStage(_ context.Context, a *domain.StageApproval, expectedUpdatedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	approvedAt := a.ApprovedAt
	switch a.EntityType {
	case "character_reference":
		ch, ok := m.characters[a.EntityID]
		if !ok {
			return ErrNotFound
		}
		if !ch.UpdatedAt.Equal(expectedUpdatedAt) {
			return ErrConflict
		}
		ch.ApprovedAt = &approvedAt
	case "chapter_outline":
		c, ok := m.chapters[a.EntityID]
		if !ok {
			return ErrNotFound
		}
		if !c.UpdatedAt.Equal(expectedUpdatedAt) {
			return ErrConflict
		}
		c.Status = "approved"
	case "draft_page", "line_art_page", "painted_page":
		pg, ok := m.pages[a.EntityID]
		if !ok {
			return ErrNotFound
		}
		if !pg.UpdatedAt.Equal(expectedUpdatedAt) {
			return ErrConflict
		}
		switch a.EntityType {
		case "draft_page":
			pg.DraftApprovedAt = &approvedAt
		case "line_art_page":
			pg.LineArtApprovedAt = &approvedAt
		default:
			pg.PaintedPageApprovedAt = &approvedAt
		}
	default:
		return fmt.Errorf("unsupported approval entity %q", a.EntityType)
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	cpy := *a
	m.approvals = append(m.approvals, &cpy)
	return nil
}

func (p *PostgresRepository) ApproveStage(ctx context.Context, a *domain.StageApproval, expectedUpdatedAt time.Time) error {
	query, args := "", []any{a.EntityID, expectedUpdatedAt}
	switch a.EntityType {
	case "character_reference":
		query, args = `UPDATE characters SET approved_at=$3 WHERE id=$1::uuid AND updated_at=$2`, append(args, a.ApprovedAt)
	case "chapter_outline":
		query = `UPDATE chapters SET status='approved' WHERE id=$1::uuid AND updated_at=$2`
	case "draft_page":
		query, args = `UPDATE pages SET draft_approved_at=$3 WHERE id=$1::uuid AND updated_at=$2`, append(args, a.ApprovedAt)
	case "line_art_page":
		query, args = `UPDATE pages SET line_art_approved_at=$3 WHERE id=$1::uuid AND updated_at=$2`, append(args, a.ApprovedAt)
	case "painted_page":
		query, args = `UPDATE pages SET painted_page_approved_at=$3 WHERE id=$1::uuid AND updated_at=$2`, append(args, a.ApprovedAt)
	default:
		return fmt.Errorf("unsupported approval entity %q", a.EntityType)
	}
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		// Distinguish a vanished entity from one edited since the caller read it.
		return ErrConflict
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stage_approvals (id, project_id, entity_type, entity_id, approved_by_user_id, approved_at, credit_estimate_at_approval)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, a.ID, a.ProjectID, a.EntityType, a.EntityID, a.ApprovedByUserID, a.ApprovedAt, a.CreditEstimateAtApproval); err != nil {
		return err
	}
	return tx.Commit()
}

// SavePanelEdit persists a panel edit and stales every page holding it in one commit (SPEC 3.5, 3.6).
func (m *InMemoryRepository) SavePanelEdit(_ context.Context, pan *domain.Panel, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.panels[pan.ID]; !ok {
		return ErrNotFound
	}
	pan.UpdatedAt = now
	cpy := *pan
	m.panels[pan.ID] = &cpy
	for _, pg := range m.pages {
		if pg.ChapterID != pan.ChapterID {
			continue
		}
		for _, id := range pg.PanelIDs {
			if id == pan.ID {
				domain.StaleDraftOnPanelEdit(pg, now)
				break
			}
		}
	}
	return nil
}

func (p *PostgresRepository) SavePanelEdit(ctx context.Context, pan *domain.Panel, now time.Time) error {
	now = now.UTC().Truncate(time.Microsecond)
	pan.UpdatedAt = now
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE panels SET panel_number=$2, scene_description=$3, character_actions=$4, camera_angle=$5, background=$6, dialogue=$7, narration=$8, image_prompt=$9, status=$10, updated_at=$11 WHERE id=$1::uuid`,
		pan.ID, pan.PanelNumber, pan.SceneDescription, pan.CharacterActions, pan.CameraAngle, pan.Background, pan.Dialogue, pan.Narration, pan.ImagePrompt, pan.Status, now)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pages SET draft_approved_at=NULL, line_art_approved_at=NULL, painted_page_approved_at=NULL, updated_at=$3
		WHERE chapter_id=$1::uuid AND panel_ids @> to_jsonb($2::text)`, pan.ChapterID, pan.ID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ReassignPanels places panelIDs on a page, moving them off any sibling page, validated and applied as one
// unit (SPEC 3.5.1). It returns the IDs of the pages whose allocation changed; their approvals are staled and
// their panels touched so earlier art can no longer be reapproved as current.
func (m *InMemoryRepository) ReassignPanels(_ context.Context, pageID string, panelIDs []string, maxPerPage int, now time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.pages[pageID]
	if !ok {
		return nil, ErrNotFound
	}
	var pages []*domain.Page
	for _, pg := range m.pages {
		if pg.ChapterID == target.ChapterID {
			pages = append(pages, pg)
		}
	}
	chapterPanels := map[string]bool{}
	for _, pan := range m.panels {
		if pan.ChapterID == target.ChapterID {
			chapterPanels[pan.ID] = true
		}
	}
	plan, err := domain.PlanPanelReassignment(pages, chapterPanels, pageID, panelIDs, maxPerPage)
	if err != nil {
		return nil, err
	}
	var changed []string
	for id, ids := range plan {
		pg := m.pages[id]
		for _, panelID := range pg.PanelIDs {
			m.panels[panelID].UpdatedAt = now
		}
		for _, panelID := range ids {
			m.panels[panelID].UpdatedAt = now
		}
		pg.PanelIDs = ids
		domain.StaleDraftOnPanelEdit(pg, now)
		changed = append(changed, id)
	}
	return changed, nil
}

func (p *PostgresRepository) ReassignPanels(ctx context.Context, pageID string, panelIDs []string, maxPerPage int, now time.Time) ([]string, error) {
	now = now.UTC().Truncate(time.Microsecond)
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var chapterID string
	if err := tx.QueryRowContext(ctx, `SELECT chapter_id FROM pages WHERE id=$1::uuid`, pageID).Scan(&chapterID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	// Serialize allocation edits per chapter: the chapter row is the lock.
	if _, err := tx.ExecContext(ctx, `SELECT id FROM chapters WHERE id=$1::uuid FOR UPDATE`, chapterID); err != nil {
		return nil, err
	}
	pages, chapterPanels, err := loadAllocation(ctx, tx, chapterID)
	if err != nil {
		return nil, err
	}
	plan, err := domain.PlanPanelReassignment(pages, chapterPanels, pageID, panelIDs, maxPerPage)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, pg := range pages {
		ids, ok := plan[pg.ID]
		if !ok {
			continue
		}
		touched := append(append([]string(nil), pg.PanelIDs...), ids...)
		pidsBytes, _ := json.Marshal(ids)
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET panel_ids=$2, draft_approved_at=NULL, line_art_approved_at=NULL, painted_page_approved_at=NULL, updated_at=$3 WHERE id=$1::uuid`, pg.ID, pidsBytes, now); err != nil {
			return nil, err
		}
		for _, panelID := range touched {
			if _, err := tx.ExecContext(ctx, `UPDATE panels SET updated_at=$2 WHERE id=$1::uuid`, panelID, now); err != nil {
				return nil, err
			}
		}
		changed = append(changed, pg.ID)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}

// loadAllocation reads a chapter's pages and panel IDs through the transaction, in page order.
func loadAllocation(ctx context.Context, tx Transaction, chapterID string) ([]*domain.Page, map[string]bool, error) {
	var pages []*domain.Page
	var panelsJSON []byte
	var pageIDs []byte
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id', id, 'n', page_number, 'p', panel_ids) ORDER BY page_number) FROM pages WHERE chapter_id=$1::uuid), '[]'::jsonb),
		COALESCE((SELECT jsonb_agg(id) FROM panels WHERE chapter_id=$1::uuid), '[]'::jsonb)`, chapterID).Scan(&pageIDs, &panelsJSON); err != nil {
		return nil, nil, err
	}
	var rows []struct {
		ID string   `json:"id"`
		N  int      `json:"n"`
		P  []string `json:"p"`
	}
	if err := json.Unmarshal(pageIDs, &rows); err != nil {
		return nil, nil, err
	}
	for _, r := range rows {
		pages = append(pages, &domain.Page{ID: r.ID, ChapterID: chapterID, PageNumber: r.N, PanelIDs: r.P})
	}
	var ids []string
	if err := json.Unmarshal(panelsJSON, &ids); err != nil {
		return nil, nil, err
	}
	panels := make(map[string]bool, len(ids))
	for _, id := range ids {
		panels[id] = true
	}
	return pages, panels, nil
}

// ReplaceChapterOutline swaps a project's chapters for a regenerated outline atomically. The chapters' panels,
// pages and palettes go with them, so it refuses with ErrArtExists rather than delete generated page art.
func (m *InMemoryRepository) ReplaceChapterOutline(_ context.Context, projectID string, chapters []*domain.Chapter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	numbers := map[int]bool{}
	for _, c := range chapters {
		if numbers[c.ChapterNumber] {
			return ErrDuplicateKey
		}
		numbers[c.ChapterNumber] = true
	}
	old := map[string]bool{}
	for id, c := range m.chapters {
		if c.ProjectID == projectID {
			old[id] = true
		}
	}
	for _, pg := range m.pages {
		if old[pg.ChapterID] && pageHasArt(pg) {
			return ErrArtExists
		}
	}
	m.dropChapters(old)
	for _, c := range chapters {
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
		if c.UpdatedAt.IsZero() {
			c.UpdatedAt = time.Now().UTC()
		}
		m.chapters[c.ID] = cloneChapter(c)
	}
	return nil
}

func (m *InMemoryRepository) dropChapters(ids map[string]bool) {
	for id, pg := range m.pages {
		if ids[pg.ChapterID] {
			delete(m.palettes, id)
			delete(m.regions, id)
			delete(m.pages, id)
		}
	}
	for id, pan := range m.panels {
		if ids[pan.ChapterID] {
			delete(m.panels, id)
		}
	}
	for id := range ids {
		delete(m.chapters, id)
	}
}

func (p *PostgresRepository) ReplaceChapterOutline(ctx context.Context, projectID string, chapters []*domain.Chapter) error {
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT id FROM projects WHERE id=$1::uuid FOR UPDATE`, projectID); err != nil {
		return err
	}
	var art bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages p JOIN chapters c ON c.id=p.chapter_id WHERE c.project_id=$1::uuid AND `+pageArtPredicate+`)`, projectID).Scan(&art); err != nil {
		return err
	}
	if art {
		return ErrArtExists
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE project_id=$1::uuid`, projectID); err != nil {
		return err
	}
	for _, c := range chapters {
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
		if c.UpdatedAt.IsZero() {
			c.UpdatedAt = time.Now().UTC()
		}
		events, names, err := chapterListArgs(c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chapters (id, project_id, chapter_number, title, summary, important_events, character_names, ending, status, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			c.ID, c.ProjectID, c.ChapterNumber, c.Title, c.Summary, events, names, c.Ending, c.Status, c.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceChapterPanels swaps a chapter's panels and pages for a regenerated set atomically. It refuses with
// ErrArtExists rather than delete generated page art.
func (m *InMemoryRepository) ReplaceChapterPanels(_ context.Context, chapterID string, panels []*domain.Panel, pages []*domain.Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.chapters[chapterID]; !ok {
		return ErrNotFound
	}
	for _, pg := range m.pages {
		if pg.ChapterID == chapterID && pageHasArt(pg) {
			return ErrArtExists
		}
	}
	for id, pg := range m.pages {
		if pg.ChapterID == chapterID {
			delete(m.palettes, id)
			delete(m.regions, id)
			delete(m.pages, id)
		}
	}
	for id, pan := range m.panels {
		if pan.ChapterID == chapterID {
			delete(m.panels, id)
		}
	}
	for _, pan := range panels {
		cpy := *pan
		m.panels[pan.ID] = &cpy
	}
	for _, pg := range pages {
		if pg.LetteringMode == "" {
			pg.LetteringMode = "baked"
		}
		cpy := *pg
		m.pages[pg.ID] = &cpy
	}
	return nil
}

func (p *PostgresRepository) ReplaceChapterPanels(ctx context.Context, chapterID string, panels []*domain.Panel, pages []*domain.Page) error {
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM chapters WHERE id=$1::uuid FOR UPDATE`, chapterID).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var art bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages p WHERE p.chapter_id=$1::uuid AND `+pageArtPredicate+`)`, chapterID).Scan(&art); err != nil {
		return err
	}
	if art {
		return ErrArtExists
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pages WHERE chapter_id=$1::uuid`, chapterID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM panels WHERE chapter_id=$1::uuid`, chapterID); err != nil {
		return err
	}
	for _, pan := range panels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO panels (id, chapter_id, panel_number, scene_description, character_actions, camera_angle, background, dialogue, narration, image_prompt, status, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			pan.ID, pan.ChapterID, pan.PanelNumber, pan.SceneDescription, pan.CharacterActions, pan.CameraAngle, pan.Background, pan.Dialogue, pan.Narration, pan.ImagePrompt, pan.Status, pan.UpdatedAt); err != nil {
			return err
		}
	}
	for _, pg := range pages {
		pidsBytes, _ := json.Marshal(pg.PanelIDs)
		if _, err := tx.ExecContext(ctx, `INSERT INTO pages (id, chapter_id, page_number, panel_ids, lettering_mode, lettering, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			pg.ID, pg.ChapterID, pg.PageNumber, pidsBytes, pageLetteringMode(pg), nullableJSON(pg.Lettering), pg.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
