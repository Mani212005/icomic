package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"icomic/backend/internal/domain"
)

var (
	ErrUnauthorized       = errors.New("unauthorized: missing or invalid authentication credentials")
	ErrForbidden          = errors.New("forbidden: access denied")
	ErrInvalidCredentials = errors.New("invalid email or password")
)

// Authenticator defines the pluggable authentication seam (SPEC 8.3).
// Identity is resolved via this seam and caller-supplied headers are never trusted blindly.
type Authenticator interface {
	Authenticate(r *http.Request) (string, error)
}

// DevAuthenticator allows mock/header-based authentication in tests or local dev ONLY.
// It is explicitly disabled by default (off in production).
type DevAuthenticator struct {
	Enabled       bool
	DefaultUserID string
}

func NewDevAuthenticator(enabled bool, defaultUserID string) *DevAuthenticator {
	return &DevAuthenticator{
		Enabled:       enabled,
		DefaultUserID: defaultUserID,
	}
}

func (a *DevAuthenticator) Authenticate(r *http.Request) (string, error) {
	if !a.Enabled {
		return "", ErrUnauthorized
	}
	if uid := r.Header.Get("X-User-ID"); uid != "" {
		return uid, nil
	}
	if a.DefaultUserID != "" {
		return a.DefaultUserID, nil
	}
	return "", ErrUnauthorized
}

// AuthenticateIdentity maps every dev identity to a registered account that is exempt from
// the project limit, so local and e2e runs are never throttled. There is no session, so no CSRF token.
func (a *DevAuthenticator) AuthenticateIdentity(r *http.Request) (Identity, error) {
	uid, err := a.Authenticate(r)
	if err != nil {
		return Identity{}, err
	}
	return Identity{UserID: uid, Kind: domain.AccountRegistered, Unlimited: true}, nil
}

// TokenManager defines session token generation and verification.
type TokenManager interface {
	CreateToken(userID string) (string, error)
	ValidateToken(token string) (string, error)
	RevokeToken(token string) error
}

// MemoryTokenManager implements an in-memory session token store.
type MemoryTokenManager struct {
	mu     sync.RWMutex
	tokens map[string]string // token -> userID
}

func NewMemoryTokenManager() *MemoryTokenManager {
	return &MemoryTokenManager{
		tokens: make(map[string]string),
	}
}

func (m *MemoryTokenManager) CreateToken(userID string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(bytes)

	m.mu.Lock()
	m.tokens[token] = userID
	m.mu.Unlock()
	return token, nil
}

func (m *MemoryTokenManager) ValidateToken(token string) (string, error) {
	if token == "" {
		return "", ErrUnauthorized
	}
	m.mu.RLock()
	userID, ok := m.tokens[token]
	m.mu.RUnlock()
	if !ok {
		return "", ErrUnauthorized
	}
	return userID, nil
}

func (m *MemoryTokenManager) RevokeToken(token string) error {
	m.mu.Lock()
	delete(m.tokens, token)
	m.mu.Unlock()
	return nil
}

// BearerTokenAuthenticator authenticates using a Bearer token.
type BearerTokenAuthenticator struct {
	TokenManager TokenManager
}

func NewBearerTokenAuthenticator(tm TokenManager) *BearerTokenAuthenticator {
	return &BearerTokenAuthenticator{TokenManager: tm}
}

func (a *BearerTokenAuthenticator) Authenticate(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
		return "", ErrUnauthorized
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	return a.TokenManager.ValidateToken(token)
}

// CompositeAuthenticator tries a primary authenticator first, and falls back to a secondary (e.g. DevAuthenticator).
type CompositeAuthenticator struct {
	Primary   Authenticator
	Secondary Authenticator
}

func NewCompositeAuthenticator(primary, secondary Authenticator) *CompositeAuthenticator {
	return &CompositeAuthenticator{Primary: primary, Secondary: secondary}
}

func (c *CompositeAuthenticator) Authenticate(r *http.Request) (string, error) {
	id, err := c.AuthenticateIdentity(r)
	return id.UserID, err
}

// AuthenticateIdentity keeps the kind and session details of whichever authenticator accepts the request.
func (c *CompositeAuthenticator) AuthenticateIdentity(r *http.Request) (Identity, error) {
	if c.Primary != nil {
		if id, err := IdentityOf(c.Primary, r); err == nil && id.UserID != "" {
			return id, nil
		}
	}
	if c.Secondary != nil {
		return IdentityOf(c.Secondary, r)
	}
	return Identity{}, ErrUnauthorized
}

// ImageHostingSeam defines the pluggable policy seam for image hosting (SPEC 1.4, ARCHITECTURE 9).
// TODO(captain): image hosting provider (S3 / self-hosted / CDN) is a pending captain decision.
type ImageHostingSeam interface {
	ResolveURL(rawKeyOrURL string) string
}

type DefaultImageHosting struct {
	BaseURL string
}

func NewDefaultImageHosting(baseURL string) *DefaultImageHosting {
	if baseURL == "" {
		baseURL = "/media"
	}
	return &DefaultImageHosting{BaseURL: baseURL}
}

func (h *DefaultImageHosting) ResolveURL(rawKeyOrURL string) string {
	if rawKeyOrURL == "" {
		return ""
	}
	if strings.HasPrefix(rawKeyOrURL, "http") || strings.HasPrefix(rawKeyOrURL, "data:") {
		return rawKeyOrURL
	}
	if strings.HasPrefix(rawKeyOrURL, "/") {
		return rawKeyOrURL
	}
	return h.BaseURL + "/" + rawKeyOrURL
}

// Context helpers
type contextKey string

const userContextKey contextKey = "auth_user_id"

func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userContextKey, userID)
}

func UserIDFromContext(ctx context.Context) string {
	val, ok := ctx.Value(userContextKey).(string)
	if !ok {
		return ""
	}
	return val
}
