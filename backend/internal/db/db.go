package db

import (
	"context"
	"errors"
	"sync"
	"time"

	"icomic/backend/internal/domain"
)

var (
	ErrNotFound = errors.New("record not found")
)

// Repository defines project and resource persistence.
type Repository interface {
	CreateProject(ctx context.Context, p *domain.Project) error
	GetProject(ctx context.Context, id string) (*domain.Project, error)
	ListProjects(ctx context.Context, userID string) ([]*domain.Project, error)
	UpdateProject(ctx context.Context, p *domain.Project) error
	DeleteProject(ctx context.Context, id string) error

	CreateChapter(ctx context.Context, c *domain.Chapter) error
	ListChapters(ctx context.Context, projectID string) ([]*domain.Chapter, error)

	CreateCharacter(ctx context.Context, c *domain.Character) error
	ListCharacters(ctx context.Context, projectID string) ([]*domain.Character, error)

	CreatePage(ctx context.Context, p *domain.Page) error
	ListPages(ctx context.Context, chapterID string) ([]*domain.Page, error)

	RecordApproval(ctx context.Context, a *domain.StageApproval) error
	ListApprovals(ctx context.Context, projectID string) ([]*domain.StageApproval, error)
}

// InMemoryRepository is a thread-safe repository implementation for hermetic testing.
type InMemoryRepository struct {
	mu         sync.Mutex
	projects   map[string]*domain.Project
	chapters   map[string][]*domain.Chapter
	characters map[string][]*domain.Character
	pages      map[string][]*domain.Page
	approvals  map[string][]*domain.StageApproval
}

func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		projects:   make(map[string]*domain.Project),
		chapters:   make(map[string][]*domain.Chapter),
		characters: make(map[string][]*domain.Character),
		pages:      make(map[string][]*domain.Page),
		approvals:  make(map[string][]*domain.StageApproval),
	}
}

func (r *InMemoryRepository) CreateProject(ctx context.Context, p *domain.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p.CreatedAt = time.Now().UTC()
	p.UpdatedAt = p.CreatedAt
	r.projects[p.ID] = p
	return nil
}

func (r *InMemoryRepository) GetProject(ctx context.Context, id string) (*domain.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, found := r.projects[id]
	if !found {
		return nil, ErrNotFound
	}
	return p, nil
}

func (r *InMemoryRepository) ListProjects(ctx context.Context, userID string) ([]*domain.Project, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var res []*domain.Project
	for _, p := range r.projects {
		if p.UserID == userID {
			res = append(res, p)
		}
	}
	return res, nil
}

func (r *InMemoryRepository) UpdateProject(ctx context.Context, p *domain.Project) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.projects[p.ID]; !found {
		return ErrNotFound
	}
	p.UpdatedAt = time.Now().UTC()
	r.projects[p.ID] = p
	return nil
}

func (r *InMemoryRepository) DeleteProject(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.projects[id]; !found {
		return ErrNotFound
	}
	delete(r.projects, id)
	return nil
}

func (r *InMemoryRepository) CreateChapter(ctx context.Context, c *domain.Chapter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chapters[c.ProjectID] = append(r.chapters[c.ProjectID], c)
	return nil
}

func (r *InMemoryRepository) ListChapters(ctx context.Context, projectID string) ([]*domain.Chapter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chapters[projectID], nil
}

func (r *InMemoryRepository) CreateCharacter(ctx context.Context, c *domain.Character) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.characters[c.ProjectID] = append(r.characters[c.ProjectID], c)
	return nil
}

func (r *InMemoryRepository) ListCharacters(ctx context.Context, projectID string) ([]*domain.Character, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.characters[projectID], nil
}

func (r *InMemoryRepository) CreatePage(ctx context.Context, p *domain.Page) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pages[p.ChapterID] = append(r.pages[p.ChapterID], p)
	return nil
}

func (r *InMemoryRepository) ListPages(ctx context.Context, chapterID string) ([]*domain.Page, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pages[chapterID], nil
}

func (r *InMemoryRepository) RecordApproval(ctx context.Context, a *domain.StageApproval) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.approvals[a.ProjectID] = append(r.approvals[a.ProjectID], a)
	return nil
}

func (r *InMemoryRepository) ListApprovals(ctx context.Context, projectID string) ([]*domain.StageApproval, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.approvals[projectID], nil
}
