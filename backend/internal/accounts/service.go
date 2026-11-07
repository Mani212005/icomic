// Package accounts implements guest sessions, sign-up, sign-in and sign-out on the local database.
// It depends on no outside identity provider. See docs/ACCOUNTS.md.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	"icomic/backend/internal/auth"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
)

var (
	ErrInvalidEmail = errors.New("enter a valid email address")
	ErrEmailTaken   = errors.New("an account with this email already exists")
)

// RateLimitedError carries how long the caller should wait. Its code is stable for clients.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "too many attempts; retry later" }

const (
	RateLimitedCode       = "login_rate_limited"
	InvalidCredentialCode = "invalid_credentials"
)

var (
	// Failed sign-ins are counted per submitted email and per client IP. Success clears the email counter.
	loginEmailRule = Rule{Max: 5, Window: 15 * time.Minute}
	loginIPRule    = Rule{Max: 20, Window: 15 * time.Minute}
	// Sign-up and guest creation each make a database row, so every attempt counts per IP.
	signupIPRule = Rule{Max: 10, Window: time.Hour}
	guestIPRule  = Rule{Max: 20, Window: time.Hour}
)

type Service struct {
	Repo    db.Repository
	Hasher  *auth.PasswordHasher
	Limiter *Limiter
	Now     func() time.Time

	dummyOnce sync.Once
	dummyHash string
}

func NewService(repo db.Repository, hasher *auth.PasswordHasher) *Service {
	if hasher == nil {
		hasher = auth.NewPasswordHasher(auth.DefaultPasswordParams, 4)
	}
	return &Service{Repo: repo, Hasher: hasher, Limiter: NewLimiter(nil, 0), Now: time.Now}
}

func (s *Service) now() time.Time { return s.Now().UTC() }

// Result is a freshly issued session. Token is the raw cookie value and is never stored.
type Result struct {
	Token     string
	ExpiresAt time.Time
	Identity  auth.Identity
	User      *domain.User
	Claimed   int
}

// AccountView is the read model behind GET /api/v1/auth/me.
type AccountView struct {
	Kind         domain.AccountKind
	Email        string
	ProjectCount int
	// ProjectLimit is nil when the caller is exempt from the limit.
	ProjectLimit *int
	CSRFToken    string
}

// ResolveSession implements auth.SessionResolver.
func (s *Service) ResolveSession(ctx context.Context, tokenHash []byte) (auth.Identity, error) {
	sess, user, err := s.Repo.GetSessionByTokenHash(ctx, tokenHash, s.now())
	if err != nil {
		return auth.Identity{}, auth.ErrUnauthorized
	}
	return auth.Identity{UserID: user.ID, Kind: user.AccountKind, SessionID: sess.ID, CSRFToken: sess.CSRFToken}, nil
}

func (s *Service) issue(ctx context.Context, user *domain.User) (*Result, error) {
	token, err := auth.NewSessionToken()
	if err != nil {
		return nil, fmt.Errorf("session token: %w", err)
	}
	csrf, err := auth.NewCSRFToken()
	if err != nil {
		return nil, fmt.Errorf("csrf token: %w", err)
	}
	now := s.now()
	sess := &domain.Session{
		TokenHash: auth.HashSessionToken(token), UserID: user.ID, CSRFToken: csrf,
		CreatedAt: now, ExpiresAt: now.Add(auth.SessionLifetime(user.AccountKind)),
	}
	if err := s.Repo.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &Result{
		Token: token, ExpiresAt: sess.ExpiresAt, User: user,
		Identity: auth.Identity{UserID: user.ID, Kind: user.AccountKind, SessionID: sess.ID, CSRFToken: csrf},
	}, nil
}

// StartGuest creates a browser-bound guest with its own session.
func (s *Service) StartGuest(ctx context.Context, ip string) (*Result, error) {
	if ok, wait := s.Limiter.Take("guest-ip:"+ip, guestIPRule); !ok {
		return nil, &RateLimitedError{RetryAfter: wait}
	}
	user := &domain.User{AccountKind: domain.AccountGuest}
	if err := s.Repo.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("create guest: %w", err)
	}
	return s.issue(ctx, user)
}

// NormalizeEmail lowercases and validates a bare address.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return "", ErrInvalidEmail
	}
	return email, nil
}

// SignUp registers an account. When the caller holds a guest session, that guest becomes the
// account in place, so its project is claimed without moving anything. The session is rotated.
func (s *Service) SignUp(ctx context.Context, current auth.Identity, ip, rawEmail, password string) (*Result, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return nil, err
	}
	if err := auth.ValidatePassword(password, email); err != nil {
		return nil, err
	}
	if ok, wait := s.Limiter.Take("signup-ip:"+ip, signupIPRule); !ok {
		return nil, &RateLimitedError{RetryAfter: wait}
	}
	hash, err := s.Hasher.Hash(ctx, password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	var user *domain.User
	if current.SessionID != "" && current.Kind == domain.AccountGuest {
		err = s.Repo.RegisterGuest(ctx, current.UserID, email, hash)
		if errors.Is(err, db.ErrNotFound) {
			err = s.createRegistered(ctx, &user, email, hash)
		} else if err == nil {
			user, err = s.Repo.GetUserByID(ctx, current.UserID)
		}
	} else {
		err = s.createRegistered(ctx, &user, email, hash)
	}
	if errors.Is(err, db.ErrDuplicateKey) {
		return nil, ErrEmailTaken
	}
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	if current.SessionID != "" {
		_ = s.Repo.RevokeSession(ctx, current.SessionID)
	}
	return s.issue(ctx, user)
}

func (s *Service) createRegistered(ctx context.Context, out **domain.User, email, hash string) error {
	u := &domain.User{Email: email, PasswordHash: hash, AccountKind: domain.AccountRegistered}
	if err := s.Repo.CreateUser(ctx, u); err != nil {
		return err
	}
	*out = u
	return nil
}

func (s *Service) dummy(ctx context.Context) string {
	s.dummyOnce.Do(func() { s.dummyHash, _ = s.Hasher.Hash(ctx, "icomic-timing-equaliser") })
	return s.dummyHash
}

// SignIn verifies credentials. Unknown email, wrong password and non-registered rows all do the
// same hashing work and return the same error. A guest session's project is claimed by the account.
func (s *Service) SignIn(ctx context.Context, current auth.Identity, ip, rawEmail, password string) (*Result, error) {
	email := strings.ToLower(strings.TrimSpace(rawEmail))
	emailKey, ipKey := "login-email:"+email, "login-ip:"+ip
	if ok, wait := s.Limiter.Allowed(ipKey, loginIPRule); !ok {
		return nil, &RateLimitedError{RetryAfter: wait}
	}
	if ok, wait := s.Limiter.Allowed(emailKey, loginEmailRule); !ok {
		return nil, &RateLimitedError{RetryAfter: wait}
	}

	user, err := s.Repo.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, fmt.Errorf("find user: %w", err)
	}
	stored := s.dummy(ctx)
	registered := err == nil && user.AccountKind == domain.AccountRegistered && user.PasswordHash != ""
	if registered {
		stored = user.PasswordHash
	}
	ok, _, verr := s.Hasher.Verify(ctx, password, stored)
	if verr != nil {
		return nil, fmt.Errorf("verify password: %w", verr)
	}
	if !ok || !registered {
		s.Limiter.Record(ipKey, loginIPRule)
		s.Limiter.Record(emailKey, loginEmailRule)
		return nil, auth.ErrInvalidCredentials
	}
	s.Limiter.Reset(emailKey)

	claimed := 0
	if current.SessionID != "" {
		if current.Kind == domain.AccountGuest {
			if claimed, err = s.Repo.ClaimGuestProjects(ctx, current.UserID, user.ID); err != nil {
				return nil, fmt.Errorf("claim guest projects: %w", err)
			}
		}
		_ = s.Repo.RevokeSession(ctx, current.SessionID)
	}
	res, err := s.issue(ctx, user)
	if err != nil {
		return nil, err
	}
	res.Claimed = claimed
	return res, nil
}

// SignOut revokes the session server-side.
func (s *Service) SignOut(ctx context.Context, current auth.Identity) error {
	if current.SessionID == "" {
		return nil
	}
	return s.Repo.RevokeSession(ctx, current.SessionID)
}

// Account builds the read model for the caller.
func (s *Service) Account(ctx context.Context, id auth.Identity) (*AccountView, error) {
	policy, err := s.Repo.GetAccountPolicy(ctx)
	if err != nil {
		return nil, err
	}
	count, err := s.Repo.CountProjects(ctx, id.UserID)
	if err != nil {
		return nil, err
	}
	view := &AccountView{Kind: id.Kind, ProjectCount: count, CSRFToken: id.CSRFToken}
	if limit, limited := policy.Limit(id.ProjectAccess()); limited {
		view.ProjectLimit = &limit
	}
	if user, err := s.Repo.GetUserByID(ctx, id.UserID); err == nil {
		view.Email = user.Email
	}
	return view, nil
}
