package auth_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"icomic/backend/internal/auth"
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

func TestPasswordHashing(t *testing.T) {
	pass := "mySecretPassword123!"
	hash := auth.HashPassword(pass)
	if hash == pass {
		t.Fatal("hash should not equal plain password")
	}
	if !auth.CheckPassword(pass, hash) {
		t.Fatal("password verification failed")
	}
	if auth.CheckPassword("wrongPassword", hash) {
		t.Fatal("wrong password verification should fail")
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
