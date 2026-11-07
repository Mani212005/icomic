package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"icomic/backend/internal/auth"
	"icomic/backend/internal/domain"
)

func TestDevAuthenticator(t *testing.T) {
	// Disabled by default
	devDisabled := auth.NewDevAuthenticator(false, "dev-user")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-ID", "attacker-id")
	if _, err := devDisabled.Authenticate(req); err != auth.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized when dev auth disabled, got %v", err)
	}

	// Enabled explicitly
	devEnabled := auth.NewDevAuthenticator(true, "default-user")
	uid, err := devEnabled.Authenticate(req)
	if err != nil || uid != "attacker-id" {
		t.Fatalf("expected header user ID when dev auth enabled, got %s (err: %v)", uid, err)
	}

	// Enabled fallback
	reqNoHeader := httptest.NewRequest("GET", "/", nil)
	uidDefault, err := devEnabled.Authenticate(reqNoHeader)
	if err != nil || uidDefault != "default-user" {
		t.Fatalf("expected default user ID when no header present, got %s (err: %v)", uidDefault, err)
	}
}

func TestBearerTokenAuthenticator(t *testing.T) {
	tm := auth.NewMemoryTokenManager()
	token, _ := tm.CreateToken("user-42")
	authenticator := auth.NewBearerTokenAuthenticator(tm)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	uid, err := authenticator.Authenticate(req)
	if err != nil || uid != "user-42" {
		t.Fatalf("expected user-42, got %s (err: %v)", uid, err)
	}

	// Invalid token
	reqBad := httptest.NewRequest("GET", "/", nil)
	reqBad.Header.Set("Authorization", "Bearer invalid-token")
	if _, err := authenticator.Authenticate(reqBad); err != auth.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized for bad token, got %v", err)
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	if uid := auth.UserIDFromContext(ctx); uid != "" {
		t.Fatalf("expected empty uid from blank context, got %s", uid)
	}

	ctx = auth.WithUserID(ctx, "user-99")
	if uid := auth.UserIDFromContext(ctx); uid != "user-99" {
		t.Fatalf("expected user-99, got %s", uid)
	}
}

func TestDevIdentityIsRegisteredAndUnlimited(t *testing.T) {
	dev := auth.NewDevAuthenticator(true, "dev-user")
	id, err := dev.AuthenticateIdentity(httptest.NewRequest("GET", "/", nil))
	if err != nil || id.UserID != "dev-user" || id.Kind != domain.AccountRegistered || !id.Unlimited || id.CSRFToken != "" {
		t.Fatalf("unexpected dev identity: %+v err=%v", id, err)
	}
	if _, err := auth.NewDevAuthenticator(false, "dev-user").AuthenticateIdentity(httptest.NewRequest("GET", "/", nil)); err == nil {
		t.Fatal("disabled dev authenticator must not yield an identity")
	}
}

type plainAuthenticator struct{}

func (plainAuthenticator) Authenticate(*http.Request) (string, error) { return "plain-user", nil }

func TestPlainAuthenticatorIsGuestByDefault(t *testing.T) {
	id, err := auth.IdentityOf(plainAuthenticator{}, httptest.NewRequest("GET", "/", nil))
	if err != nil || id.UserID != "plain-user" || id.Kind != domain.AccountGuest || id.Unlimited {
		t.Fatalf("unexpected identity: %+v err=%v", id, err)
	}
}

type fakeResolver struct{ got []byte }

func (f *fakeResolver) ResolveSession(_ context.Context, hash []byte) (auth.Identity, error) {
	f.got = hash
	return auth.Identity{UserID: "u1", Kind: domain.AccountRegistered, SessionID: "s1", CSRFToken: "c1"}, nil
}

func TestSessionAuthenticatorHashesCookieAndIgnoresHeaders(t *testing.T) {
	resolver := &fakeResolver{}
	a := auth.NewSessionAuthenticator(resolver)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-ID", "attacker")
	if _, err := a.AuthenticateIdentity(req); err != auth.ErrUnauthorized {
		t.Fatalf("headers must not authenticate a session, got %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "raw-token"})
	id, err := a.AuthenticateIdentity(req)
	if err != nil || id.UserID != "u1" || id.CSRFToken != "c1" {
		t.Fatalf("unexpected identity %+v err=%v", id, err)
	}
	if string(resolver.got) != string(auth.HashSessionToken("raw-token")) || string(resolver.got) == "raw-token" {
		t.Fatal("resolver must receive only the token hash")
	}
}

func TestCompositeKeepsSessionIdentityAndFallsBack(t *testing.T) {
	composite := auth.NewCompositeAuthenticator(auth.NewSessionAuthenticator(&fakeResolver{}), auth.NewDevAuthenticator(true, "dev-user"))
	withCookie := httptest.NewRequest("GET", "/", nil)
	withCookie.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "t"})
	if id, _ := composite.AuthenticateIdentity(withCookie); id.UserID != "u1" || id.CSRFToken == "" {
		t.Fatalf("session should win: %+v", id)
	}
	if id, _ := composite.AuthenticateIdentity(httptest.NewRequest("GET", "/", nil)); id.UserID != "dev-user" || !id.Unlimited {
		t.Fatalf("dev fallback expected: %+v", id)
	}
}

func TestSessionTokensAreRandomAndHashedWithSHA256(t *testing.T) {
	a, _ := auth.NewSessionToken()
	b, _ := auth.NewSessionToken()
	if a == b || len(a) < 43 {
		t.Fatalf("tokens must be unique and at least 256 bits: %q %q", a, b)
	}
	if len(auth.HashSessionToken(a)) != 32 {
		t.Fatal("expected a SHA-256 digest")
	}
}
