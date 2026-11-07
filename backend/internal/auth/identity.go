package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"icomic/backend/internal/domain"
)

// Identity is what the authenticator seam resolves for a request.
// A plain Authenticator only yields a user ID; IdentityAuthenticator also yields the account kind.
type Identity struct {
	UserID string
	Kind   domain.AccountKind
	// Unlimited exempts the caller from the project limit. Only the dev authenticator sets it.
	Unlimited bool
	// SessionID and CSRFToken are set only for cookie sessions. CSRF is enforced exactly when CSRFToken is set.
	SessionID string
	CSRFToken string
}

func (i Identity) ProjectAccess() domain.ProjectAccess {
	return domain.ProjectAccess{Kind: i.Kind, Unlimited: i.Unlimited}
}

type IdentityAuthenticator interface {
	AuthenticateIdentity(r *http.Request) (Identity, error)
}

// IdentityOf resolves an identity from any authenticator. A plain authenticator is a guest
// with no verified account, which is the safe default for every identity without a registered account.
func IdentityOf(a Authenticator, r *http.Request) (Identity, error) {
	if ia, ok := a.(IdentityAuthenticator); ok {
		return ia.AuthenticateIdentity(r)
	}
	uid, err := a.Authenticate(r)
	if err != nil {
		return Identity{}, err
	}
	return Identity{UserID: uid, Kind: domain.AccountGuest}, nil
}

// SessionCookieName is the httpOnly session cookie.
const SessionCookieName = "icomic_session"

// SessionResolver looks up a live session by the hash of its cookie token.
type SessionResolver interface {
	ResolveSession(ctx context.Context, tokenHash []byte) (Identity, error)
}

// SessionAuthenticator authenticates the session cookie. It never trusts caller-supplied identity headers.
type SessionAuthenticator struct {
	Resolver SessionResolver
}

func NewSessionAuthenticator(resolver SessionResolver) *SessionAuthenticator {
	return &SessionAuthenticator{Resolver: resolver}
}

func (a *SessionAuthenticator) Authenticate(r *http.Request) (string, error) {
	id, err := a.AuthenticateIdentity(r)
	return id.UserID, err
}

func (a *SessionAuthenticator) AuthenticateIdentity(r *http.Request) (Identity, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return Identity{}, ErrUnauthorized
	}
	id, err := a.Resolver.ResolveSession(r.Context(), HashSessionToken(cookie.Value))
	if err != nil || id.UserID == "" {
		return Identity{}, ErrUnauthorized
	}
	return id, nil
}

// NewSessionToken returns 256 random bits (URL-safe) for the cookie. Only HashSessionToken of it is stored.
func NewSessionToken() (string, error) {
	return randomToken(32)
}

// NewCSRFToken returns a per-session token the client echoes in X-CSRF-Token.
func NewCSRFToken() (string, error) {
	return randomToken(24)
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashSessionToken is SHA-256: the token is already 256 random bits, so a slow hash adds nothing.
func HashSessionToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// SessionLifetime is the absolute lifetime of a session cookie.
func SessionLifetime(kind domain.AccountKind) time.Duration {
	if kind == domain.AccountRegistered {
		return 30 * 24 * time.Hour
	}
	return 365 * 24 * time.Hour
}

var ErrInvalidPassword = errors.New("password does not meet the policy")

type identityKey struct{}

// WithIdentity stores the resolved identity; UserIDFromContext reads the same value.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(WithUserID(ctx, id.UserID), identityKey{}, id)
}

func IdentityFromContext(ctx context.Context) Identity {
	if id, ok := ctx.Value(identityKey{}).(Identity); ok {
		return id
	}
	return Identity{UserID: UserIDFromContext(ctx)}
}
