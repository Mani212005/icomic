package db

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"icomic/backend/internal/domain"
)

var (
	ErrNotFound           = errors.New("resource not found")
	ErrConflict           = errors.New("resource conflict")
	ErrDuplicateKey       = errors.New("duplicate key violates unique constraint")
	ErrInsufficientCredit = errors.New("insufficient credit balance")
)

// Repository defines persistence operations for Icomic (SPEC 9, ARCHITECTURE 2).
type Repository interface {
	// Users & Plans
	CreateUser(ctx context.Context, u *domain.User) error
	GetUserByID(ctx context.Context, id string) (*domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	GetPlan(ctx context.Context, id string) (*domain.Plan, error)

	// Projects
	CreateProject(ctx context.Context, p *domain.Project) error
	GetProject(ctx context.Context, id string) (*domain.Project, error)
	ListProjects(ctx context.Context, userID string) ([]*domain.Project, error)
	UpdateProject(ctx context.Context, p *domain.Project) error
	DeleteProject(ctx context.Context, id string) error

	// Chapters
	CreateChapter(ctx context.Context, c *domain.Chapter) error
	GetChapter(ctx context.Context, id string) (*domain.Chapter, error)
	ListChapters(ctx context.Context, projectID string) ([]*domain.Chapter, error)
	UpdateChapter(ctx context.Context, c *domain.Chapter) error

	// Characters
	CreateCharacter(ctx context.Context, ch *domain.Character) error
	GetCharacter(ctx context.Context, id string) (*domain.Character, error)
	ListCharacters(ctx context.Context, projectID string) ([]*domain.Character, error)
	UpdateCharacter(ctx context.Context, ch *domain.Character) error

	// Panels
	CreatePanel(ctx context.Context, p *domain.Panel) error
	GetPanel(ctx context.Context, id string) (*domain.Panel, error)
	ListPanels(ctx context.Context, chapterID string) ([]*domain.Panel, error)
	UpdatePanel(ctx context.Context, p *domain.Panel) error

	// Pages
	CreatePage(ctx context.Context, pg *domain.Page) error
	GetPage(ctx context.Context, id string) (*domain.Page, error)
	ListPages(ctx context.Context, chapterID string) ([]*domain.Page, error)
	UpdatePage(ctx context.Context, pg *domain.Page) error

	// Colour Palettes and Regions
	GetColourPalette(ctx context.Context, pageID string) (*domain.ColourPalette, error)
	SaveColourPalette(ctx context.Context, cp *domain.ColourPalette) error
	ListColourRegions(ctx context.Context, pageID string) ([]*domain.ColourRegion, error)
	CreateColourRegion(ctx context.Context, cr *domain.ColourRegion) error

	// Stage Approvals
	CreateApproval(ctx context.Context, a *domain.StageApproval) error
	RecordApproval(ctx context.Context, a *domain.StageApproval) error
	GetLatestApproval(ctx context.Context, entityType string, entityID string) (*domain.StageApproval, error)
	ListApprovals(ctx context.Context, projectID string) ([]*domain.StageApproval, error)

	// Generation Jobs & Credit Ledger
	CreateJobWithDebit(ctx context.Context, job *domain.GenerationJob, debitAmount float64) error
	GetJob(ctx context.Context, id string) (*domain.GenerationJob, error)
	GetJobByIdempotencyKey(ctx context.Context, key string) (*domain.GenerationJob, error)
	GetActiveJobForEntity(ctx context.Context, projectID string, jobType string) (*domain.GenerationJob, error)
	UpdateJob(ctx context.Context, job *domain.GenerationJob) error
	RecordRefund(ctx context.Context, jobID string, amount float64) error
	RecordAdjustment(ctx context.Context, userID, projectID string, amount float64) error
	GetCreditBalance(ctx context.Context, userID string) (float64, error)
	ListCreditLedger(ctx context.Context, userID string) ([]*domain.CreditLedger, error)
	GetUserDailyImageCount(ctx context.Context, userID string, since time.Time) (int, error)
	GetUserMonthlyImageCount(ctx context.Context, userID string, since time.Time) (int, error)

	// Exports
	CreateExport(ctx context.Context, exp *domain.Export) error
	GetExport(ctx context.Context, id string) (*domain.Export, error)
	UpdateExport(ctx context.Context, exp *domain.Export) error
}

// InMemoryRepository is a thread-safe repository implementation for hermetic testing.
type InMemoryRepository struct {
	mu           sync.RWMutex
	users        map[string]*domain.User
	usersByEmail map[string]string
	plans        map[string]*domain.Plan
	projects     map[string]*domain.Project
	chapters     map[string]*domain.Chapter
	characters   map[string]*domain.Character
	panels       map[string]*domain.Panel
	pages        map[string]*domain.Page
	palettes     map[string]*domain.ColourPalette
	regions      map[string][]*domain.ColourRegion
	approvals    []*domain.StageApproval
	jobs         map[string]*domain.GenerationJob
	jobsByIdemp  map[string]string
	ledger       []*domain.CreditLedger
	exports      map[string]*domain.Export
	userBalances map[string]float64
}

func NewInMemoryRepository() *InMemoryRepository {
	repo := &InMemoryRepository{
		users:        make(map[string]*domain.User),
		usersByEmail: make(map[string]string),
		plans:        make(map[string]*domain.Plan),
		projects:     make(map[string]*domain.Project),
		chapters:     make(map[string]*domain.Chapter),
		characters:   make(map[string]*domain.Character),
		panels:       make(map[string]*domain.Panel),
		pages:        make(map[string]*domain.Page),
		palettes:     make(map[string]*domain.ColourPalette),
		regions:      make(map[string][]*domain.ColourRegion),
		approvals:    make([]*domain.StageApproval, 0),
		jobs:         make(map[string]*domain.GenerationJob),
		jobsByIdemp:  make(map[string]string),
		ledger:       make([]*domain.CreditLedger, 0),
		exports:      make(map[string]*domain.Export),
		userBalances: make(map[string]float64),
	}

	repo.plans["free"] = &domain.Plan{
		ID:   "free",
		Name: "Free Tier",
		Entitlements: map[string]interface{}{
			"max_daily_images":   20,
			"max_monthly_images": 500,
		},
		CreatedAt: time.Now().UTC(),
	}

	return repo
}

// User methods
func (m *InMemoryRepository) CreateUser(_ context.Context, u *domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.usersByEmail[u.Email]; exists {
		return ErrDuplicateKey
	}
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

	cpy := *u
	m.users[u.ID] = &cpy
	m.usersByEmail[u.Email] = u.ID
	if _, ok := m.userBalances[u.ID]; !ok {
		m.userBalances[u.ID] = 100.0
	}
	return nil
}

func (m *InMemoryRepository) GetUserByID(_ context.Context, id string) (*domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	u, ok := m.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *u
	return &cpy, nil
}

func (m *InMemoryRepository) GetUserByEmail(_ context.Context, email string) (*domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.usersByEmail[email]
	if !ok {
		return nil, ErrNotFound
	}
	u := m.users[id]
	cpy := *u
	return &cpy, nil
}

func (m *InMemoryRepository) GetPlan(_ context.Context, id string) (*domain.Plan, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.plans[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *p
	return &cpy, nil
}

// Projects
func (m *InMemoryRepository) CreateProject(_ context.Context, p *domain.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	cpy := *p
	m.projects[p.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) GetProject(_ context.Context, id string) (*domain.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.projects[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *p
	return &cpy, nil
}

func (m *InMemoryRepository) ListProjects(_ context.Context, userID string) ([]*domain.Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*domain.Project
	for _, p := range m.projects {
		if p.UserID == userID {
			cpy := *p
			res = append(res, &cpy)
		}
	}
	return res, nil
}

func (m *InMemoryRepository) UpdateProject(_ context.Context, p *domain.Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.projects[p.ID]; !ok {
		return ErrNotFound
	}
	p.UpdatedAt = time.Now().UTC()
	cpy := *p
	m.projects[p.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) DeleteProject(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.projects[id]; !ok {
		return ErrNotFound
	}
	delete(m.projects, id)
	return nil
}

// Chapters
func (m *InMemoryRepository) CreateChapter(_ context.Context, c *domain.Chapter) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now().UTC()
	}
	cpy := *c
	m.chapters[c.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) GetChapter(_ context.Context, id string) (*domain.Chapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.chapters[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *c
	return &cpy, nil
}

func (m *InMemoryRepository) ListChapters(_ context.Context, projectID string) ([]*domain.Chapter, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*domain.Chapter
	for _, c := range m.chapters {
		if c.ProjectID == projectID {
			cpy := *c
			res = append(res, &cpy)
		}
	}
	return res, nil
}

func (m *InMemoryRepository) UpdateChapter(_ context.Context, c *domain.Chapter) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.chapters[c.ID]; !ok {
		return ErrNotFound
	}
	c.UpdatedAt = time.Now().UTC()
	cpy := *c
	m.chapters[c.ID] = &cpy
	return nil
}

// Characters
func (m *InMemoryRepository) CreateCharacter(_ context.Context, ch *domain.Character) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ch.ID == "" {
		ch.ID = uuid.NewString()
	}
	if ch.UpdatedAt.IsZero() {
		ch.UpdatedAt = time.Now().UTC()
	}
	cpy := *ch
	m.characters[ch.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) GetCharacter(_ context.Context, id string) (*domain.Character, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ch, ok := m.characters[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *ch
	return &cpy, nil
}

func (m *InMemoryRepository) ListCharacters(_ context.Context, projectID string) ([]*domain.Character, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*domain.Character
	for _, ch := range m.characters {
		if ch.ProjectID == projectID {
			cpy := *ch
			res = append(res, &cpy)
		}
	}
	return res, nil
}

func (m *InMemoryRepository) UpdateCharacter(_ context.Context, ch *domain.Character) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.characters[ch.ID]; !ok {
		return ErrNotFound
	}
	if ch.UpdatedAt.IsZero() {
		ch.UpdatedAt = time.Now().UTC()
	}
	cpy := *ch
	m.characters[ch.ID] = &cpy
	return nil
}

// Panels
func (m *InMemoryRepository) CreatePanel(_ context.Context, p *domain.Panel) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	cpy := *p
	m.panels[p.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) GetPanel(_ context.Context, id string) (*domain.Panel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.panels[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *p
	return &cpy, nil
}

func (m *InMemoryRepository) ListPanels(_ context.Context, chapterID string) ([]*domain.Panel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*domain.Panel
	for _, p := range m.panels {
		if p.ChapterID == chapterID {
			cpy := *p
			res = append(res, &cpy)
		}
	}
	return res, nil
}

func (m *InMemoryRepository) UpdatePanel(_ context.Context, p *domain.Panel) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.panels[p.ID]; !ok {
		return ErrNotFound
	}
	p.UpdatedAt = time.Now().UTC()
	cpy := *p
	m.panels[p.ID] = &cpy
	return nil
}

// Pages
func (m *InMemoryRepository) CreatePage(_ context.Context, pg *domain.Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if pg.ID == "" {
		pg.ID = uuid.NewString()
	}
	if pg.UpdatedAt.IsZero() {
		pg.UpdatedAt = time.Now().UTC()
	}
	cpy := *pg
	m.pages[pg.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) GetPage(_ context.Context, id string) (*domain.Page, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pg, ok := m.pages[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *pg
	return &cpy, nil
}

func (m *InMemoryRepository) ListPages(_ context.Context, chapterID string) ([]*domain.Page, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*domain.Page
	for _, pg := range m.pages {
		if pg.ChapterID == chapterID {
			cpy := *pg
			res = append(res, &cpy)
		}
	}
	return res, nil
}

func (m *InMemoryRepository) UpdatePage(_ context.Context, pg *domain.Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.pages[pg.ID]; !ok {
		return ErrNotFound
	}
	if pg.UpdatedAt.IsZero() {
		pg.UpdatedAt = time.Now().UTC()
	}
	cpy := *pg
	m.pages[pg.ID] = &cpy
	return nil
}

// Colour Palettes and Regions
func (m *InMemoryRepository) GetColourPalette(_ context.Context, pageID string) (*domain.ColourPalette, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cp, ok := m.palettes[pageID]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *cp
	return &cpy, nil
}

func (m *InMemoryRepository) SaveColourPalette(_ context.Context, cp *domain.ColourPalette) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	cp.UserModifiedAt = &now
	cpy := *cp
	m.palettes[cp.PageID] = &cpy
	return nil
}

func (m *InMemoryRepository) ListColourRegions(_ context.Context, pageID string) ([]*domain.ColourRegion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	regs := m.regions[pageID]
	var res []*domain.ColourRegion
	for _, r := range regs {
		cpy := *r
		res = append(res, &cpy)
	}
	return res, nil
}

func (m *InMemoryRepository) CreateColourRegion(_ context.Context, cr *domain.ColourRegion) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cr.ID == "" {
		cr.ID = uuid.NewString()
	}
	if cr.FillAppliedAt.IsZero() {
		cr.FillAppliedAt = time.Now().UTC()
	}
	cpy := *cr
	m.regions[cr.PageID] = append(m.regions[cr.PageID], &cpy)
	return nil
}

// Stage Approvals
func (m *InMemoryRepository) CreateApproval(_ context.Context, a *domain.StageApproval) error {
	return m.RecordApproval(context.Background(), a)
}

func (m *InMemoryRepository) RecordApproval(_ context.Context, a *domain.StageApproval) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.ApprovedAt.IsZero() {
		a.ApprovedAt = time.Now().UTC()
	}
	cpy := *a
	m.approvals = append(m.approvals, &cpy)
	return nil
}

func (m *InMemoryRepository) GetLatestApproval(_ context.Context, entityType string, entityID string) (*domain.StageApproval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var latest *domain.StageApproval
	for _, a := range m.approvals {
		if a.EntityType == entityType && a.EntityID == entityID {
			if latest == nil || a.ApprovedAt.After(latest.ApprovedAt) {
				latest = a
			}
		}
	}
	if latest == nil {
		return nil, ErrNotFound
	}
	cpy := *latest
	return &cpy, nil
}

func (m *InMemoryRepository) ListApprovals(_ context.Context, projectID string) ([]*domain.StageApproval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*domain.StageApproval
	for _, a := range m.approvals {
		if a.ProjectID == projectID {
			cpy := *a
			res = append(res, &cpy)
		}
	}
	return res, nil
}

// Generation Jobs & Credit Ledger
func (m *InMemoryRepository) CreateJobWithDebit(_ context.Context, job *domain.GenerationJob, debitAmount float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.jobsByIdemp[job.IdempotencyKey]; exists {
		return fmt.Errorf("%w: idempotency key %s already exists", ErrDuplicateKey, job.IdempotencyKey)
	}

	currentBal := m.userBalances[job.UserID]
	if debitAmount > 0 && currentBal < debitAmount {
		return ErrInsufficientCredit
	}

	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}

	newBal := currentBal - debitAmount
	m.userBalances[job.UserID] = newBal

	jobCpy := *job
	m.jobs[job.ID] = &jobCpy
	m.jobsByIdemp[job.IdempotencyKey] = job.ID

	if debitAmount > 0 {
		ledgerEntry := &domain.CreditLedger{
			ID:              uuid.NewString(),
			UserID:          job.UserID,
			ProjectID:       job.ProjectID,
			GenerationJobID: &job.ID,
			TransactionType: "debit",
			Amount:          debitAmount,
			BalanceAfter:    newBal,
			CreatedAt:       now,
		}
		m.ledger = append(m.ledger, ledgerEntry)
	}

	return nil
}

func (m *InMemoryRepository) GetJob(_ context.Context, id string) (*domain.GenerationJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	j, ok := m.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *j
	return &cpy, nil
}

func (m *InMemoryRepository) GetJobByIdempotencyKey(_ context.Context, key string) (*domain.GenerationJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, ok := m.jobsByIdemp[key]
	if !ok {
		return nil, ErrNotFound
	}
	j := m.jobs[id]
	cpy := *j
	return &cpy, nil
}

func (m *InMemoryRepository) GetActiveJobForEntity(_ context.Context, projectID string, jobType string) (*domain.GenerationJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, j := range m.jobs {
		if j.ProjectID == projectID && j.JobType == jobType {
			if j.Status == "pending" || j.Status == "running" {
				cpy := *j
				return &cpy, nil
			}
		}
	}
	return nil, ErrNotFound
}

func (m *InMemoryRepository) UpdateJob(_ context.Context, job *domain.GenerationJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.jobs[job.ID]; !ok {
		return ErrNotFound
	}
	cpy := *job
	m.jobs[job.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) RecordRefund(_ context.Context, jobID string, amount float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	j, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}

	now := time.Now().UTC()
	currentBal := m.userBalances[j.UserID]
	newBal := currentBal + amount
	m.userBalances[j.UserID] = newBal

	entry := &domain.CreditLedger{
		ID:              uuid.NewString(),
		UserID:          j.UserID,
		ProjectID:       j.ProjectID,
		GenerationJobID: &jobID,
		TransactionType: "refund",
		Amount:          amount,
		BalanceAfter:    newBal,
		CreatedAt:       now,
	}
	m.ledger = append(m.ledger, entry)
	return nil
}

func (m *InMemoryRepository) RecordAdjustment(_ context.Context, userID, projectID string, amount float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	currentBal := m.userBalances[userID]
	newBal := currentBal + amount
	m.userBalances[userID] = newBal

	entry := &domain.CreditLedger{
		ID:              uuid.NewString(),
		UserID:          userID,
		ProjectID:       projectID,
		TransactionType: "adjustment",
		Amount:          amount,
		BalanceAfter:    newBal,
		CreatedAt:       now,
	}
	m.ledger = append(m.ledger, entry)
	return nil
}

func (m *InMemoryRepository) GetCreditBalance(_ context.Context, userID string) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.userBalances[userID], nil
}

func (m *InMemoryRepository) ListCreditLedger(_ context.Context, userID string) ([]*domain.CreditLedger, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*domain.CreditLedger
	for _, l := range m.ledger {
		if l.UserID == userID {
			cpy := *l
			res = append(res, &cpy)
		}
	}
	return res, nil
}

func (m *InMemoryRepository) GetUserDailyImageCount(_ context.Context, userID string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, j := range m.jobs {
		if j.UserID == userID && (j.JobType == "character_reference" || j.JobType == "draft_sketch" || j.JobType == "final_line_art") {
			if j.CreatedAt.After(since) && (j.Status == "done" || j.Status == "running" || j.Status == "pending") {
				count++
			}
		}
	}
	return count, nil
}

func (m *InMemoryRepository) GetUserMonthlyImageCount(_ context.Context, userID string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, j := range m.jobs {
		if j.UserID == userID && (j.JobType == "character_reference" || j.JobType == "draft_sketch" || j.JobType == "final_line_art") {
			if j.CreatedAt.After(since) && (j.Status == "done" || j.Status == "running" || j.Status == "pending") {
				count++
			}
		}
	}
	return count, nil
}

// Exports
func (m *InMemoryRepository) CreateExport(_ context.Context, exp *domain.Export) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if exp.ID == "" {
		exp.ID = uuid.NewString()
	}
	if exp.CreatedAt.IsZero() {
		exp.CreatedAt = time.Now().UTC()
	}
	cpy := *exp
	m.exports[exp.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) GetExport(_ context.Context, id string) (*domain.Export, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	exp, ok := m.exports[id]
	if !ok {
		return nil, ErrNotFound
	}
	cpy := *exp
	return &cpy, nil
}

func (m *InMemoryRepository) UpdateExport(_ context.Context, exp *domain.Export) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.exports[exp.ID]; !ok {
		return ErrNotFound
	}
	cpy := *exp
	m.exports[exp.ID] = &cpy
	return nil
}
