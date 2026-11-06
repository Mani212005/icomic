package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	pq "github.com/lib/pq"
	"icomic/backend/internal/domain"
)

// PostgresRepository implements Repository backed by PostgreSQL.
type PostgresRepository struct {
	db            *sql.DB
	transactions  TransactionStarter
	E2EImageQuota *domain.ImageQuotaLimits
}

func NewPostgresRepository(connStr string) (*PostgresRepository, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return NewPostgresRepositoryWithDB(db), nil
}

func (p *PostgresRepository) Close() error {
	return p.db.Close()
}

// ApplyMigrations executes the initial schema migration script.
func (p *PostgresRepository) ApplyMigrations(ctx context.Context, migrationSQL string) error {
	_, err := p.db.ExecContext(ctx, migrationSQL)
	return err
}

// RunMigrations executes all unapplied up migrations from migrationsDir idempotently.
func (p *PostgresRepository) RunMigrations(ctx context.Context, migrationsDir string) ([]string, error) {
	runner := NewMigrationRunner(p.db, migrationsDir)
	return runner.ApplyAll(ctx)
}

// User methods
func (p *PostgresRepository) CreateUser(ctx context.Context, u *domain.User) error {
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	if u.PlanID == "" {
		u.PlanID = "free"
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `
		INSERT INTO users (id, email, password_hash, plan_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	if _, err := tx.ExecContext(ctx, query, u.ID, u.Email, u.PasswordHash, u.PlanID, u.CreatedAt, u.UpdatedAt); err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return ErrDuplicateKey
		}
		return fmt.Errorf("insert user: %w", err)
	}

	// Seed initial balance entry in credit_ledger if user has no entries
	initQuery := `
		INSERT INTO credit_ledger (id, user_id, project_id, generation_job_id, transaction_type, amount, balance_after, created_at)
		SELECT $1, $2, NULL, NULL, 'adjustment', 100.0, 100.0, $3
		WHERE NOT EXISTS (SELECT 1 FROM credit_ledger WHERE user_id = $2)
	`
	if _, err := tx.ExecContext(ctx, initQuery, uuid.NewString(), u.ID, now); err != nil {
		return fmt.Errorf("seed initial credits: %w", err)
	}

	return tx.Commit()
}

func (p *PostgresRepository) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	query := `SELECT id, email, password_hash, plan_id, created_at, updated_at FROM users WHERE id = $1`
	var u domain.User
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.PlanID, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}

func (p *PostgresRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	query := `SELECT id, email, password_hash, plan_id, created_at, updated_at FROM users WHERE email = $1`
	var u domain.User
	err := p.db.QueryRowContext(ctx, query, email).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.PlanID, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &u, nil
}

func (p *PostgresRepository) GetPlan(ctx context.Context, id string) (*domain.Plan, error) {
	query := `SELECT id, name, entitlements, created_at FROM plans WHERE id = $1`
	var plan domain.Plan
	var entBytes []byte
	err := p.db.QueryRowContext(ctx, query, id).Scan(&plan.ID, &plan.Name, &entBytes, &plan.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	if len(entBytes) > 0 {
		_ = json.Unmarshal(entBytes, &plan.Entitlements)
	}
	return &plan, nil
}

// Projects
func (p *PostgresRepository) CreateProject(ctx context.Context, proj *domain.Project) error {
	if proj.ID == "" {
		proj.ID = uuid.NewString()
	}
	now := time.Now()
	if proj.CreatedAt.IsZero() {
		proj.CreatedAt = now
	}
	proj.UpdatedAt = now
	if proj.LetteringMode == "" {
		proj.LetteringMode = "baked"
	}
	if proj.FinishPreset == "" {
		proj.FinishPreset = "print-heavy"
	}

	query := `
		INSERT INTO projects (id, user_id, title, original_script, chapter_count, art_style, status, created_at, updated_at, lettering_mode, finish_preset, finish_preset_explicit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err := p.db.ExecContext(ctx, query, proj.ID, proj.UserID, proj.Title, proj.OriginalScript, proj.ChapterCount, proj.ArtStyle, proj.Status, proj.CreatedAt, proj.UpdatedAt, proj.LetteringMode, proj.FinishPreset, proj.FinishPresetExplicit)
	return err
}

func (p *PostgresRepository) GetProject(ctx context.Context, id string) (*domain.Project, error) {
	query := `SELECT id, user_id, title, original_script, chapter_count, art_style, status, created_at, updated_at, lettering_mode, finish_preset, finish_preset_explicit FROM projects WHERE id = $1`
	var proj domain.Project
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&proj.ID, &proj.UserID, &proj.Title, &proj.OriginalScript, &proj.ChapterCount, &proj.ArtStyle, &proj.Status, &proj.CreatedAt, &proj.UpdatedAt, &proj.LetteringMode, &proj.FinishPreset, &proj.FinishPresetExplicit,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &proj, nil
}

func (p *PostgresRepository) ListProjects(ctx context.Context, userID string) ([]*domain.Project, error) {
	query := `SELECT id, user_id, title, original_script, chapter_count, art_style, status, created_at, updated_at, lettering_mode, finish_preset, finish_preset_explicit FROM projects WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := p.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.Project
	for rows.Next() {
		var proj domain.Project
		if err := rows.Scan(&proj.ID, &proj.UserID, &proj.Title, &proj.OriginalScript, &proj.ChapterCount, &proj.ArtStyle, &proj.Status, &proj.CreatedAt, &proj.UpdatedAt, &proj.LetteringMode, &proj.FinishPreset, &proj.FinishPresetExplicit); err != nil {
			return nil, err
		}
		res = append(res, &proj)
	}
	return res, rows.Err()
}

func (p *PostgresRepository) UpdateProject(ctx context.Context, proj *domain.Project) error {
	proj.UpdatedAt = time.Now()
	query := `
		UPDATE projects SET title = $2, original_script = $3, chapter_count = $4, art_style = $5, status = $6, updated_at = $7, finish_preset = $8, finish_preset_explicit = $9
		WHERE id = $1
	`
	res, err := p.db.ExecContext(ctx, query, proj.ID, proj.Title, proj.OriginalScript, proj.ChapterCount, proj.ArtStyle, proj.Status, proj.UpdatedAt, proj.FinishPreset, proj.FinishPresetExplicit)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresRepository) DeleteProject(ctx context.Context, id string) error {
	query := `DELETE FROM projects WHERE id = $1`
	res, err := p.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Chapters
func (p *PostgresRepository) CreateChapter(ctx context.Context, c *domain.Chapter) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	query := `INSERT INTO chapters (id, project_id, chapter_number, title, summary, status, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := p.db.ExecContext(ctx, query, c.ID, c.ProjectID, c.ChapterNumber, c.Title, c.Summary, c.Status, c.UpdatedAt)
	return err
}

func (p *PostgresRepository) GetChapter(ctx context.Context, id string) (*domain.Chapter, error) {
	query := `SELECT id, project_id, chapter_number, title, summary, status, updated_at FROM chapters WHERE id = $1`
	var c domain.Chapter
	err := p.db.QueryRowContext(ctx, query, id).Scan(&c.ID, &c.ProjectID, &c.ChapterNumber, &c.Title, &c.Summary, &c.Status, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

func (p *PostgresRepository) ListChapters(ctx context.Context, projectID string) ([]*domain.Chapter, error) {
	query := `SELECT id, project_id, chapter_number, title, summary, status, updated_at FROM chapters WHERE project_id = $1 ORDER BY chapter_number ASC`
	rows, err := p.db.QueryContext(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.Chapter
	for rows.Next() {
		var c domain.Chapter
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.ChapterNumber, &c.Title, &c.Summary, &c.Status, &c.UpdatedAt); err != nil {
			return nil, err
		}
		res = append(res, &c)
	}
	return res, rows.Err()
}

func (p *PostgresRepository) UpdateChapter(ctx context.Context, c *domain.Chapter) error {
	now := time.Now().UTC()
	c.UpdatedAt = now
	query := `UPDATE chapters SET chapter_number = $2, title = $3, summary = $4, status = $5, updated_at = $6 WHERE id = $1`
	res, err := p.db.ExecContext(ctx, query, c.ID, c.ChapterNumber, c.Title, c.Summary, c.Status, c.UpdatedAt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Characters
func (p *PostgresRepository) CreateCharacter(ctx context.Context, ch *domain.Character) error {
	if ch.ID == "" {
		ch.ID = uuid.NewString()
	}
	if ch.UpdatedAt.IsZero() {
		ch.UpdatedAt = time.Now().UTC()
	}
	ch.UpdatedAt = ch.UpdatedAt.UTC().Truncate(time.Microsecond)
	vaBytes, _ := json.Marshal(ch.VisualAttributes)
	query := `
		INSERT INTO characters (id, project_id, name, description, reference_image_url, reference_image_job_id, visual_attributes, approved_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := p.db.ExecContext(ctx, query, ch.ID, ch.ProjectID, ch.Name, ch.Description, ch.ReferenceImageURL, ch.ReferenceImageJobID, vaBytes, ch.ApprovedAt, ch.UpdatedAt)
	return err
}

func (p *PostgresRepository) GetCharacter(ctx context.Context, id string) (*domain.Character, error) {
	query := `SELECT id, project_id, name, description, reference_image_url, reference_image_job_id, visual_attributes, approved_at, updated_at FROM characters WHERE id = $1`
	var ch domain.Character
	var vaBytes []byte
	var refURL sql.NullString
	var refJobID sql.NullString
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&ch.ID, &ch.ProjectID, &ch.Name, &ch.Description, &refURL, &refJobID, &vaBytes, &ch.ApprovedAt, &ch.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if refURL.Valid {
		ch.ReferenceImageURL = refURL.String
	}
	if refJobID.Valid {
		str := refJobID.String
		ch.ReferenceImageJobID = &str
	}
	if len(vaBytes) > 0 {
		_ = json.Unmarshal(vaBytes, &ch.VisualAttributes)
	}
	return &ch, nil
}

func (p *PostgresRepository) ListCharacters(ctx context.Context, projectID string) ([]*domain.Character, error) {
	query := `SELECT id, project_id, name, description, reference_image_url, reference_image_job_id, visual_attributes, approved_at, updated_at FROM characters WHERE project_id = $1`
	rows, err := p.db.QueryContext(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.Character
	for rows.Next() {
		var ch domain.Character
		var vaBytes []byte
		var refURL sql.NullString
		var refJobID sql.NullString
		if err := rows.Scan(&ch.ID, &ch.ProjectID, &ch.Name, &ch.Description, &refURL, &refJobID, &vaBytes, &ch.ApprovedAt, &ch.UpdatedAt); err != nil {
			return nil, err
		}
		if refURL.Valid {
			ch.ReferenceImageURL = refURL.String
		}
		if refJobID.Valid {
			str := refJobID.String
			ch.ReferenceImageJobID = &str
		}
		if len(vaBytes) > 0 {
			_ = json.Unmarshal(vaBytes, &ch.VisualAttributes)
		}
		res = append(res, &ch)
	}
	return res, rows.Err()
}

func (p *PostgresRepository) UpdateCharacter(ctx context.Context, ch *domain.Character) error {
	if ch.UpdatedAt.IsZero() {
		ch.UpdatedAt = time.Now().UTC()
	}
	ch.UpdatedAt = ch.UpdatedAt.UTC().Truncate(time.Microsecond)
	vaBytes, _ := json.Marshal(ch.VisualAttributes)
	query := `
		UPDATE characters SET name = $2, description = $3, reference_image_url = $4, reference_image_job_id = $5, visual_attributes = $6, approved_at = $7, updated_at = $8
		WHERE id = $1
	`
	res, err := p.db.ExecContext(ctx, query, ch.ID, ch.Name, ch.Description, ch.ReferenceImageURL, ch.ReferenceImageJobID, vaBytes, ch.ApprovedAt, ch.UpdatedAt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Panels
func (p *PostgresRepository) CreatePanel(ctx context.Context, pan *domain.Panel) error {
	if pan.ID == "" {
		pan.ID = uuid.NewString()
	}
	query := `
		INSERT INTO panels (id, chapter_id, panel_number, scene_description, character_actions, camera_angle, background, dialogue, narration, image_prompt, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := p.db.ExecContext(ctx, query, pan.ID, pan.ChapterID, pan.PanelNumber, pan.SceneDescription, pan.CharacterActions, pan.CameraAngle, pan.Background, pan.Dialogue, pan.Narration, pan.ImagePrompt, pan.Status)
	return err
}

func (p *PostgresRepository) GetPanel(ctx context.Context, id string) (*domain.Panel, error) {
	query := `SELECT id, chapter_id, panel_number, scene_description, character_actions, camera_angle, background, dialogue, narration, image_prompt, status FROM panels WHERE id = $1`
	var pan domain.Panel
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&pan.ID, &pan.ChapterID, &pan.PanelNumber, &pan.SceneDescription, &pan.CharacterActions, &pan.CameraAngle, &pan.Background, &pan.Dialogue, &pan.Narration, &pan.ImagePrompt, &pan.Status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &pan, err
}

func (p *PostgresRepository) ListPanels(ctx context.Context, chapterID string) ([]*domain.Panel, error) {
	query := `SELECT id, chapter_id, panel_number, scene_description, character_actions, camera_angle, background, dialogue, narration, image_prompt, status FROM panels WHERE chapter_id = $1 ORDER BY panel_number ASC`
	rows, err := p.db.QueryContext(ctx, query, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.Panel
	for rows.Next() {
		var pan domain.Panel
		if err := rows.Scan(&pan.ID, &pan.ChapterID, &pan.PanelNumber, &pan.SceneDescription, &pan.CharacterActions, &pan.CameraAngle, &pan.Background, &pan.Dialogue, &pan.Narration, &pan.ImagePrompt, &pan.Status); err != nil {
			return nil, err
		}
		res = append(res, &pan)
	}
	return res, rows.Err()
}

func (p *PostgresRepository) UpdatePanel(ctx context.Context, pan *domain.Panel) error {
	query := `
		UPDATE panels SET panel_number = $2, scene_description = $3, character_actions = $4, camera_angle = $5, background = $6, dialogue = $7, narration = $8, image_prompt = $9, status = $10
		WHERE id = $1
	`
	res, err := p.db.ExecContext(ctx, query, pan.ID, pan.PanelNumber, pan.SceneDescription, pan.CharacterActions, pan.CameraAngle, pan.Background, pan.Dialogue, pan.Narration, pan.ImagePrompt, pan.Status)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Pages
func (p *PostgresRepository) CreatePage(ctx context.Context, pg *domain.Page) error {
	if pg.ID == "" {
		pg.ID = uuid.NewString()
	}
	pidsBytes, _ := json.Marshal(pg.PanelIDs)
	query := `
		INSERT INTO pages (id, chapter_id, page_number, panel_ids, draft_image_url, draft_image_job_id, draft_approved_at, line_art_image_url, line_art_image_job_id, line_art_approved_at, colour_image_url, colour_image_uploaded_at, painted_page_image_url, painted_page_image_job_id, painted_page_approved_at, painted_page_variant, lettering_mode, lettering)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	`
	_, err := p.db.ExecContext(ctx, query, pg.ID, pg.ChapterID, pg.PageNumber, pidsBytes, pg.DraftImageURL, pg.DraftImageJobID, pg.DraftApprovedAt, pg.LineArtImageURL, pg.LineArtImageJobID, pg.LineArtApprovedAt, pg.ColourImageURL, pg.ColourImageUploadedAt, pg.PaintedPageImageURL, pg.PaintedPageImageJobID, pg.PaintedPageApprovedAt, pg.PaintedPageVariant, pageLetteringMode(pg), nullableJSON(pg.Lettering))
	return err
}

func (p *PostgresRepository) GetPage(ctx context.Context, id string) (*domain.Page, error) {
	query := `SELECT id, chapter_id, page_number, panel_ids, draft_image_url, draft_image_job_id, draft_approved_at, line_art_image_url, line_art_image_job_id, line_art_approved_at, colour_image_url, colour_image_uploaded_at, painted_page_image_url, painted_page_image_job_id, painted_page_approved_at, painted_page_variant, lettering_mode, lettering, updated_at FROM pages WHERE id = $1`
	var pg domain.Page
	var pidsBytes, letteringBytes []byte
	var draftURL sql.NullString
	var draftJobID sql.NullString
	var lineURL sql.NullString
	var lineJobID sql.NullString
	var colourURL sql.NullString
	var paintedURL sql.NullString
	var paintedJobID sql.NullString
	var paintedVariant sql.NullString
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&pg.ID, &pg.ChapterID, &pg.PageNumber, &pidsBytes, &draftURL, &draftJobID, &pg.DraftApprovedAt, &lineURL, &lineJobID, &pg.LineArtApprovedAt, &colourURL, &pg.ColourImageUploadedAt, &paintedURL, &paintedJobID, &pg.PaintedPageApprovedAt, &paintedVariant, &pg.LetteringMode, &letteringBytes, &pg.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	pg.Lettering = letteringBytes
	if draftURL.Valid {
		pg.DraftImageURL = draftURL.String
	}
	if draftJobID.Valid {
		str := draftJobID.String
		pg.DraftImageJobID = &str
	}
	if lineURL.Valid {
		pg.LineArtImageURL = lineURL.String
	}
	if lineJobID.Valid {
		str := lineJobID.String
		pg.LineArtImageJobID = &str
	}
	if colourURL.Valid {
		pg.ColourImageURL = colourURL.String
	}
	if paintedURL.Valid {
		pg.PaintedPageImageURL = paintedURL.String
	}
	if paintedJobID.Valid {
		str := paintedJobID.String
		pg.PaintedPageImageJobID = &str
	}
	if paintedVariant.Valid {
		pg.PaintedPageVariant = paintedVariant.String
	}
	if len(pidsBytes) > 0 {
		_ = json.Unmarshal(pidsBytes, &pg.PanelIDs)
	}
	return &pg, nil
}

func (p *PostgresRepository) ListPages(ctx context.Context, chapterID string) ([]*domain.Page, error) {
	query := `SELECT id, chapter_id, page_number, panel_ids, draft_image_url, draft_image_job_id, draft_approved_at, line_art_image_url, line_art_image_job_id, line_art_approved_at, colour_image_url, colour_image_uploaded_at, painted_page_image_url, painted_page_image_job_id, painted_page_approved_at, painted_page_variant, lettering_mode, lettering, updated_at FROM pages WHERE chapter_id = $1 ORDER BY page_number ASC`
	rows, err := p.db.QueryContext(ctx, query, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.Page
	for rows.Next() {
		var pg domain.Page
		var pidsBytes, letteringBytes []byte
		var draftURL sql.NullString
		var draftJobID sql.NullString
		var lineURL sql.NullString
		var lineJobID sql.NullString
		var colourURL sql.NullString
		var paintedURL sql.NullString
		var paintedJobID sql.NullString
		var paintedVariant sql.NullString
		if err := rows.Scan(&pg.ID, &pg.ChapterID, &pg.PageNumber, &pidsBytes, &draftURL, &draftJobID, &pg.DraftApprovedAt, &lineURL, &lineJobID, &pg.LineArtApprovedAt, &colourURL, &pg.ColourImageUploadedAt, &paintedURL, &paintedJobID, &pg.PaintedPageApprovedAt, &paintedVariant, &pg.LetteringMode, &letteringBytes, &pg.UpdatedAt); err != nil {
			return nil, err
		}
		pg.Lettering = letteringBytes
		if draftURL.Valid {
			pg.DraftImageURL = draftURL.String
		}
		if draftJobID.Valid {
			str := draftJobID.String
			pg.DraftImageJobID = &str
		}
		if lineURL.Valid {
			pg.LineArtImageURL = lineURL.String
		}
		if lineJobID.Valid {
			str := lineJobID.String
			pg.LineArtImageJobID = &str
		}
		if colourURL.Valid {
			pg.ColourImageURL = colourURL.String
		}
		if paintedURL.Valid {
			pg.PaintedPageImageURL = paintedURL.String
		}
		if paintedJobID.Valid {
			str := paintedJobID.String
			pg.PaintedPageImageJobID = &str
		}
		if paintedVariant.Valid {
			pg.PaintedPageVariant = paintedVariant.String
		}
		if len(pidsBytes) > 0 {
			_ = json.Unmarshal(pidsBytes, &pg.PanelIDs)
		}
		res = append(res, &pg)
	}
	return res, rows.Err()
}

func (p *PostgresRepository) UpdatePage(ctx context.Context, pg *domain.Page) error {
	pidsBytes, _ := json.Marshal(pg.PanelIDs)
	if pg.UpdatedAt.IsZero() {
		pg.UpdatedAt = time.Now().UTC()
	}
	query := `
		UPDATE pages SET page_number = $2, panel_ids = $3, draft_image_url = $4, draft_image_job_id = $5, draft_approved_at = $6, line_art_image_url = $7, line_art_image_job_id = $8, line_art_approved_at = $9, colour_image_url = $10, colour_image_uploaded_at = $11, painted_page_image_url = $12, painted_page_image_job_id = $13, painted_page_approved_at = $14, painted_page_variant = $15, updated_at = $16
		WHERE id = $1
	`
	res, err := p.db.ExecContext(ctx, query, pg.ID, pg.PageNumber, pidsBytes, pg.DraftImageURL, pg.DraftImageJobID, pg.DraftApprovedAt, pg.LineArtImageURL, pg.LineArtImageJobID, pg.LineArtApprovedAt, pg.ColourImageURL, pg.ColourImageUploadedAt, pg.PaintedPageImageURL, pg.PaintedPageImageJobID, pg.PaintedPageApprovedAt, pg.PaintedPageVariant, pg.UpdatedAt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresRepository) SavePageColourImage(ctx context.Context, pageID, imageURL string, uploadedAt time.Time) error {
	res, err := p.db.ExecContext(ctx, `UPDATE pages SET colour_image_url = $2, colour_image_uploaded_at = $3 WHERE id = $1`, pageID, imageURL, uploadedAt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Colour Palettes and Regions
func (p *PostgresRepository) GetColourPalette(ctx context.Context, pageID string) (*domain.ColourPalette, error) {
	query := `SELECT id, page_id, suggestion_job_id, palette, user_modified_at FROM colour_palettes WHERE page_id = $1`
	var cp domain.ColourPalette
	var sugID sql.NullString
	var palBytes []byte
	err := p.db.QueryRowContext(ctx, query, pageID).Scan(&cp.ID, &cp.PageID, &sugID, &palBytes, &cp.UserModifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if sugID.Valid {
		str := sugID.String
		cp.SuggestionJobID = &str
	}
	if len(palBytes) > 0 {
		_ = json.Unmarshal(palBytes, &cp.Palette)
	}
	return &cp, nil
}

func (p *PostgresRepository) SaveColourPalette(ctx context.Context, cp *domain.ColourPalette) error {
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	now := time.Now()
	cp.UserModifiedAt = &now
	palBytes, _ := json.Marshal(cp.Palette)
	query := `
		INSERT INTO colour_palettes (id, page_id, suggestion_job_id, palette, user_modified_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET suggestion_job_id = EXCLUDED.suggestion_job_id, palette = EXCLUDED.palette, user_modified_at = EXCLUDED.user_modified_at
	`
	_, err := p.db.ExecContext(ctx, query, cp.ID, cp.PageID, cp.SuggestionJobID, palBytes, cp.UserModifiedAt)
	return err
}

func (p *PostgresRepository) ListColourRegions(ctx context.Context, pageID string) ([]*domain.ColourRegion, error) {
	query := `SELECT id, page_id, region_label, fill_colour_hex, fill_applied_at, fill_applied_by FROM colour_regions WHERE page_id = $1`
	rows, err := p.db.QueryContext(ctx, query, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.ColourRegion
	for rows.Next() {
		var cr domain.ColourRegion
		if err := rows.Scan(&cr.ID, &cr.PageID, &cr.RegionLabel, &cr.FillColourHex, &cr.FillAppliedAt, &cr.FillAppliedBy); err != nil {
			return nil, err
		}
		res = append(res, &cr)
	}
	return res, rows.Err()
}

func (p *PostgresRepository) CreateColourRegion(ctx context.Context, cr *domain.ColourRegion) error {
	if cr.ID == "" {
		cr.ID = uuid.NewString()
	}
	if cr.FillAppliedAt.IsZero() {
		cr.FillAppliedAt = time.Now()
	}
	query := `INSERT INTO colour_regions (id, page_id, region_label, fill_colour_hex, fill_applied_at, fill_applied_by) VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := p.db.ExecContext(ctx, query, cr.ID, cr.PageID, cr.RegionLabel, cr.FillColourHex, cr.FillAppliedAt, cr.FillAppliedBy)
	return err
}

// Stage Approvals
func (p *PostgresRepository) CreateApproval(ctx context.Context, a *domain.StageApproval) error {
	return p.RecordApproval(ctx, a)
}

func (p *PostgresRepository) RecordApproval(ctx context.Context, a *domain.StageApproval) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.ApprovedAt.IsZero() {
		a.ApprovedAt = time.Now()
	}
	query := `
		INSERT INTO stage_approvals (id, project_id, entity_type, entity_id, approved_by_user_id, approved_at, credit_estimate_at_approval)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := p.db.ExecContext(ctx, query, a.ID, a.ProjectID, a.EntityType, a.EntityID, a.ApprovedByUserID, a.ApprovedAt, a.CreditEstimateAtApproval)
	return err
}

func (p *PostgresRepository) GetLatestApproval(ctx context.Context, entityType string, entityID string) (*domain.StageApproval, error) {
	query := `
		SELECT id, project_id, entity_type, entity_id, approved_by_user_id, approved_at, credit_estimate_at_approval
		FROM stage_approvals
		WHERE entity_type = $1 AND entity_id = $2
		ORDER BY approved_at DESC
		LIMIT 1
	`
	var a domain.StageApproval
	err := p.db.QueryRowContext(ctx, query, entityType, entityID).Scan(
		&a.ID, &a.ProjectID, &a.EntityType, &a.EntityID, &a.ApprovedByUserID, &a.ApprovedAt, &a.CreditEstimateAtApproval,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &a, err
}

func (p *PostgresRepository) ListApprovals(ctx context.Context, projectID string) ([]*domain.StageApproval, error) {
	query := `
		SELECT id, project_id, entity_type, entity_id, approved_by_user_id, approved_at, credit_estimate_at_approval
		FROM stage_approvals
		WHERE project_id = $1
		ORDER BY approved_at DESC
	`
	rows, err := p.db.QueryContext(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.StageApproval
	for rows.Next() {
		var a domain.StageApproval
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.EntityType, &a.EntityID, &a.ApprovedByUserID, &a.ApprovedAt, &a.CreditEstimateAtApproval); err != nil {
			return nil, err
		}
		res = append(res, &a)
	}
	return res, rows.Err()
}

// Generation Jobs & Credit Ledger (Transactional)
// CreateJobWithDebit is retained for repository callers. Generation services use
// CreateGeneration so they can supply the entity and receive idempotent results.
func (p *PostgresRepository) CreateJobWithDebit(ctx context.Context, job *domain.GenerationJob, amount float64) error {
	result, _, err := p.CreateGeneration(ctx, job, GenerationOptions{}, amount)
	if err == nil {
		*job = *result
	}
	return err
}

func (p *PostgresRepository) GetJob(ctx context.Context, id string) (*domain.GenerationJob, error) {
	return scanJob(p.db.QueryRowContext(ctx, `SELECT id, project_id, user_id, job_type, stage, model_id, status, idempotency_key, estimated_cost, actual_cost, hashed_user_id, error_message, created_at, completed_at, COALESCE(entity_id::text, '') FROM generation_jobs WHERE id=$1`, id))
}

func (p *PostgresRepository) GetJobByIdempotencyKey(ctx context.Context, key string) (*domain.GenerationJob, error) {
	return scanJob(p.db.QueryRowContext(ctx, `SELECT id, project_id, user_id, job_type, stage, model_id, status, idempotency_key, estimated_cost, actual_cost, hashed_user_id, error_message, created_at, completed_at, COALESCE(entity_id::text, '') FROM generation_jobs WHERE idempotency_key=$1`, key))
}

func (p *PostgresRepository) GetActiveJobForEntity(ctx context.Context, projectID string, jobType string) (*domain.GenerationJob, error) {
	return scanJob(p.db.QueryRowContext(ctx, `SELECT id, project_id, user_id, job_type, stage, model_id, status, idempotency_key, estimated_cost, actual_cost, hashed_user_id, error_message, created_at, completed_at, COALESCE(entity_id::text, '') FROM generation_jobs WHERE project_id=$1 AND job_type=$2 AND status IN ('pending', 'running') LIMIT 1`, projectID, jobType))
}

func (p *PostgresRepository) UpdateJob(ctx context.Context, job *domain.GenerationJob) error {
	query := `
		UPDATE generation_jobs
		SET status = $2, actual_cost = $3, error_message = $4, completed_at = $5
		WHERE id = $1
	`
	res, err := p.db.ExecContext(ctx, query, job.ID, job.Status, job.ActualCost, job.ErrorMessage, job.CompletedAt)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresRepository) RecordRefund(ctx context.Context, jobID string, amount float64) error {
	_, err := p.RefundGeneration(ctx, jobID, amount)
	return err
}

func (p *PostgresRepository) RecordAdjustment(ctx context.Context, userID, projectID string, amount float64) error {
	if !validAmount(amount) {
		return fmt.Errorf("adjustment must be nonnegative")
	}
	tx, err := p.transactions.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1::uuid FOR UPDATE`, userID).Scan(&locked); err != nil {
		return err
	}
	balance, err := transactionBalance(ctx, tx, userID)
	if err != nil {
		return err
	}
	if _, err := insertCredit(ctx, tx, userID, projectID, "", "adjustment", amount, balance+amount); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PostgresRepository) GetCreditBalance(ctx context.Context, userID string) (float64, error) {
	var balance float64
	err := p.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE transaction_type WHEN 'debit' THEN -amount ELSE amount END),0) FROM credit_ledger WHERE user_id=$1::uuid`, userID).Scan(&balance)
	return balance, err
}

func (p *PostgresRepository) ListCreditLedger(ctx context.Context, userID string) ([]*domain.CreditLedger, error) {
	query := `
		SELECT id, user_id, project_id, generation_job_id, transaction_type, amount, balance_after, created_at
		FROM credit_ledger
		WHERE user_id = $1
		ORDER BY created_at DESC
	`
	rows, err := p.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []*domain.CreditLedger
	for rows.Next() {
		var l domain.CreditLedger
		var projID sql.NullString
		var jobID sql.NullString
		if err := rows.Scan(&l.ID, &l.UserID, &projID, &jobID, &l.TransactionType, &l.Amount, &l.BalanceAfter, &l.CreatedAt); err != nil {
			return nil, err
		}
		if projID.Valid {
			l.ProjectID = projID.String
		}
		if jobID.Valid {
			str := jobID.String
			l.GenerationJobID = &str
		}
		res = append(res, &l)
	}
	return res, rows.Err()
}

func (p *PostgresRepository) GetUserDailyImageCount(ctx context.Context, userID string, since time.Time) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM image_quota_usage
		WHERE user_id = $1::uuid AND created_at >= $2::timestamptz
	`
	var count int
	err := p.db.QueryRowContext(ctx, query, userID, since).Scan(&count)
	return count, err
}

func (p *PostgresRepository) GetUserMonthlyImageCount(ctx context.Context, userID string, since time.Time) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM image_quota_usage
		WHERE user_id = $1::uuid AND created_at >= $2::timestamptz
	`
	var count int
	err := p.db.QueryRowContext(ctx, query, userID, since).Scan(&count)
	return count, err
}

// Exports
func (p *PostgresRepository) CreateExport(ctx context.Context, exp *domain.Export) error {
	if exp.ID == "" {
		exp.ID = uuid.NewString()
	}
	if exp.CreatedAt.IsZero() {
		exp.CreatedAt = time.Now()
	}
	query := `
		INSERT INTO exports (id, project_id, file_url, format, status, provenance_metadata_preserved, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := p.db.ExecContext(ctx, query, exp.ID, exp.ProjectID, exp.FileURL, exp.Format, exp.Status, exp.ProvenanceMetadataPreserved, exp.CreatedAt)
	return err
}

func (p *PostgresRepository) GetExport(ctx context.Context, id string) (*domain.Export, error) {
	query := `SELECT id, project_id, file_url, format, status, provenance_metadata_preserved, created_at FROM exports WHERE id = $1`
	var exp domain.Export
	err := p.db.QueryRowContext(ctx, query, id).Scan(
		&exp.ID, &exp.ProjectID, &exp.FileURL, &exp.Format, &exp.Status, &exp.ProvenanceMetadataPreserved, &exp.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &exp, err
}

func (p *PostgresRepository) UpdateExport(ctx context.Context, exp *domain.Export) error {
	query := `
		UPDATE exports SET file_url = $2, format = $3, status = $4, provenance_metadata_preserved = $5
		WHERE id = $1
	`
	res, err := p.db.ExecContext(ctx, query, exp.ID, exp.FileURL, exp.Format, exp.Status, exp.ProvenanceMetadataPreserved)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func pageLetteringMode(pg *domain.Page) string {
	if pg.LetteringMode == "overlay" {
		return "overlay"
	}
	return "baked"
}
func nullableJSON(data json.RawMessage) interface{} {
	if len(data) == 0 {
		return nil
	}
	return []byte(data)
}

// Deliberately excludes all image columns and timestamps. Concurrent placement
// edits must retry against the latest complete replacement layer.
func (p *PostgresRepository) SavePageLettering(ctx context.Context, id string, previous, next json.RawMessage) error {
	result, err := p.db.ExecContext(ctx, `UPDATE pages SET lettering=$3::jsonb WHERE id=$1::uuid AND lettering_mode='overlay' AND lettering IS NOT DISTINCT FROM $2::jsonb`, id, nullableJSON(previous), nullableJSON(next))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrConflict
	}
	return nil
}

// Source text and vector state commit together; image state is never included.
func (p *PostgresRepository) SaveLetteringText(ctx context.Context, pageID string, before, next *domain.Panel, previous, lettering json.RawMessage) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE pages SET lettering=$3::jsonb WHERE id=$1::uuid AND lettering_mode='overlay' AND lettering IS NOT DISTINCT FROM $2::jsonb`, pageID, nullableJSON(previous), nullableJSON(lettering))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE panels SET dialogue=$2, narration=$3, updated_at=$4 WHERE id=$1::uuid AND dialogue IS NOT DISTINCT FROM $5 AND narration IS NOT DISTINCT FROM $6`, before.ID, next.Dialogue, next.Narration, next.UpdatedAt, before.Dialogue, before.Narration)
	if err != nil {
		return err
	}
	count, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return tx.Commit()
}
