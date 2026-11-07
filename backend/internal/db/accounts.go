package db

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	pq "github.com/lib/pq"
	"icomic/backend/internal/domain"
)

// lockUserProjects serialises project counting and inserting for one user until the transaction ends.
func lockUserProjects(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('icomic:projects:' || $1::text, 0))`, userID)
	return err
}

func readAccountPolicy(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (domain.AccountPolicy, error) {
	var policy domain.AccountPolicy
	err := q.QueryRowContext(ctx, `SELECT guest_max_projects, registered_max_projects FROM account_policy WHERE singleton`).
		Scan(&policy.GuestMaxProjects, &policy.RegisteredMaxProjects)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultAccountPolicy(), nil
	}
	return policy, err
}

func (p *PostgresRepository) GetAccountPolicy(ctx context.Context) (domain.AccountPolicy, error) {
	return readAccountPolicy(ctx, p.db)
}

func (p *PostgresRepository) CountProjects(ctx context.Context, userID string) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

func (p *PostgresRepository) CreateProjectForAccount(ctx context.Context, proj *domain.Project, access domain.ProjectAccess) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockUserProjects(ctx, tx, proj.UserID); err != nil {
		return fmt.Errorf("lock projects: %w", err)
	}
	policy, err := readAccountPolicy(ctx, tx)
	if err != nil {
		return fmt.Errorf("read account policy: %w", err)
	}
	if limit, limited := policy.Limit(access); limited {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE user_id = $1`, proj.UserID).Scan(&count); err != nil {
			return fmt.Errorf("count projects: %w", err)
		}
		if count >= limit {
			return &domain.ProjectLimitError{Kind: access.Kind, Limit: limit}
		}
	}
	if err := insertProject(ctx, tx, proj); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *PostgresRepository) RegisterGuest(ctx context.Context, userID, email, passwordHash string) error {
	res, err := p.db.ExecContext(ctx, `
		UPDATE users SET email = $2, password_hash = $3, account_kind = 'registered', updated_at = NOW()
		WHERE id = $1 AND account_kind = 'guest'`, userID, email, passwordHash)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return ErrDuplicateKey
		}
		return fmt.Errorf("register guest: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PostgresRepository) ClaimGuestProjects(ctx context.Context, guestID, accountID string) (int, error) {
	if guestID == accountID {
		return 0, nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Lock in a fixed order so two concurrent claims cannot deadlock.
	ids := []string{guestID, accountID}
	sort.Strings(ids)
	for _, id := range ids {
		if err := lockUserProjects(ctx, tx, id); err != nil {
			return 0, fmt.Errorf("lock projects: %w", err)
		}
	}
	// Only ever move projects out of a guest, never out of a registered account.
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT account_kind FROM users WHERE id = $1`, guestID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if domain.ParseAccountKind(kind) != domain.AccountGuest {
		return 0, nil
	}
	policy, err := readAccountPolicy(ctx, tx)
	if err != nil {
		return 0, err
	}
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE user_id = $1`, accountID).Scan(&owned); err != nil {
		return 0, err
	}
	room := policy.RegisteredMaxProjects - owned
	if room <= 0 {
		return 0, nil
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE projects SET user_id = $1, updated_at = NOW()
		WHERE id IN (SELECT id FROM projects WHERE user_id = $2 ORDER BY created_at, id LIMIT $3)`,
		accountID, guestID, room)
	if err != nil {
		return 0, fmt.Errorf("claim guest projects: %w", err)
	}
	moved, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(moved), nil
}

func (p *PostgresRepository) CreateSession(ctx context.Context, s *domain.Session) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO sessions (id, token_hash, user_id, csrf_token, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, s.ID, s.TokenHash, s.UserID, s.CSRFToken, s.CreatedAt, s.ExpiresAt)
	return err
}

func (p *PostgresRepository) GetSessionByTokenHash(ctx context.Context, tokenHash []byte, now time.Time) (*domain.Session, *domain.User, error) {
	var s domain.Session
	var u domain.User
	var email, hash sql.NullString
	var kind string
	err := p.db.QueryRowContext(ctx, `
		SELECT s.id, s.token_hash, s.user_id, s.csrf_token, s.created_at, s.expires_at,
		       u.id, u.email, u.password_hash, u.account_kind, u.plan_id, u.created_at, u.updated_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2`, tokenHash, now).Scan(
		&s.ID, &s.TokenHash, &s.UserID, &s.CSRFToken, &s.CreatedAt, &s.ExpiresAt,
		&u.ID, &email, &hash, &kind, &u.PlanID, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	u.Email, u.PasswordHash, u.AccountKind = email.String, hash.String, domain.ParseAccountKind(kind)
	return &s, &u, nil
}

func (p *PostgresRepository) RevokeSession(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// In-memory implementations keep hermetic API tests and the no-database dev server working.

func (m *InMemoryRepository) SetAccountPolicy(policy domain.AccountPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accountPolicy = policy
}

func (m *InMemoryRepository) GetAccountPolicy(context.Context) (domain.AccountPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.accountPolicy, nil
}

func (m *InMemoryRepository) countProjectsLocked(userID string) int {
	n := 0
	for _, p := range m.projects {
		if p.UserID == userID {
			n++
		}
	}
	return n
}

func (m *InMemoryRepository) CountProjects(_ context.Context, userID string) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.countProjectsLocked(userID), nil
}

func (m *InMemoryRepository) CreateProjectForAccount(_ context.Context, p *domain.Project, access domain.ProjectAccess) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit, limited := m.accountPolicy.Limit(access); limited && m.countProjectsLocked(p.UserID) >= limit {
		return &domain.ProjectLimitError{Kind: access.Kind, Limit: limit}
	}
	m.storeProjectLocked(p)
	return nil
}

func (m *InMemoryRepository) RegisterGuest(_ context.Context, userID, email, passwordHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok || u.AccountKind != domain.AccountGuest {
		return ErrNotFound
	}
	key := strings.ToLower(email)
	if _, taken := m.usersByEmail[key]; taken {
		return ErrDuplicateKey
	}
	u.Email, u.PasswordHash, u.AccountKind, u.UpdatedAt = email, passwordHash, domain.AccountRegistered, time.Now().UTC()
	m.usersByEmail[key] = userID
	return nil
}

func (m *InMemoryRepository) ClaimGuestProjects(_ context.Context, guestID, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	guest, ok := m.users[guestID]
	if guestID == accountID || !ok || guest.AccountKind != domain.AccountGuest {
		return 0, nil
	}
	room := m.accountPolicy.RegisteredMaxProjects - m.countProjectsLocked(accountID)
	var owned []*domain.Project
	for _, p := range m.projects {
		if p.UserID == guestID {
			owned = append(owned, p)
		}
	}
	sort.Slice(owned, func(i, j int) bool {
		if !owned[i].CreatedAt.Equal(owned[j].CreatedAt) {
			return owned[i].CreatedAt.Before(owned[j].CreatedAt)
		}
		return owned[i].ID < owned[j].ID
	})
	moved := 0
	for _, p := range owned {
		if moved >= room {
			break
		}
		p.UserID, p.UpdatedAt = accountID, time.Now().UTC()
		moved++
	}
	return moved, nil
}

func (m *InMemoryRepository) CreateSession(_ context.Context, s *domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	cpy := *s
	m.sessions[s.ID] = &cpy
	return nil
}

func (m *InMemoryRepository) GetSessionByTokenHash(_ context.Context, tokenHash []byte, now time.Time) (*domain.Session, *domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sessions {
		if subtle.ConstantTimeCompare(s.TokenHash, tokenHash) != 1 || s.RevokedAt != nil || !s.ExpiresAt.After(now) {
			continue
		}
		u, ok := m.users[s.UserID]
		if !ok {
			return nil, nil, ErrNotFound
		}
		sc, uc := *s, *u
		return &sc, &uc, nil
	}
	return nil, nil, ErrNotFound
}

func (m *InMemoryRepository) RevokeSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok && s.RevokedAt == nil {
		now := time.Now().UTC()
		s.RevokedAt = &now
	}
	return nil
}

// UserCount reports stored users; hermetic tests use it to prove endpoints do not mint extra rows.
func (m *InMemoryRepository) UserCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users)
}
