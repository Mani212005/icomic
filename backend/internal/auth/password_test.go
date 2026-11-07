package auth_test

import (
	"context"
	"strings"
	"testing"

	"icomic/backend/internal/auth"
)

var testParams = auth.PasswordParams{MemoryKiB: 8, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 16}

func TestPasswordHashVerify(t *testing.T) {
	h := auth.NewPasswordHasher(testParams, 2)
	ctx := context.Background()
	hash, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=8,t=1,p=1$") || strings.Contains(hash, "correct horse") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if ok, rehash, err := h.Verify(ctx, "correct horse battery", hash); err != nil || !ok || rehash {
		t.Fatalf("verify = %v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify(ctx, "wrong password!!", hash); ok {
		t.Fatal("wrong password verified")
	}
	other, _ := h.Hash(ctx, "correct horse battery")
	if other == hash {
		t.Fatal("hashes must be salted")
	}
}

func TestPasswordVerifyFlagsOldParameters(t *testing.T) {
	ctx := context.Background()
	old := auth.NewPasswordHasher(testParams, 1)
	hash, _ := old.Hash(ctx, "long enough password")
	stronger := testParams
	stronger.Time = 2
	ok, rehash, err := auth.NewPasswordHasher(stronger, 1).Verify(ctx, "long enough password", hash)
	if err != nil || !ok || !rehash {
		t.Fatalf("expected ok with rehash, got ok=%v rehash=%v err=%v", ok, rehash, err)
	}
}

func TestPasswordVerifyRejectsMalformedAndForeignHashes(t *testing.T) {
	h := auth.NewPasswordHasher(testParams, 1)
	for _, bad := range []string{
		"", "plain", "$2a$10$wN9P31K9LpQn88bWq4KqreD/4aUa240GZc5R9lHjEaZ9a.Z7yN.2G",
		"$argon2id$v=19$m=8,t=1,p=1$!!$!!",
		"$argon2id$v=19$m=999999999,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5",
		"$argon2id$v=19$m=8,t=999,p=1$c2FsdHNhbHQ$a2V5a2V5",
		"$argon2i$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$a2V5a2V5",
	} {
		if ok, _, err := h.Verify(context.Background(), "anything at all", bad); ok || err != nil {
			t.Errorf("hash %q: ok=%v err=%v", bad, ok, err)
		}
	}
	if h.Runs() != 0 {
		t.Fatal("malformed hashes must be rejected before any expensive work")
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	for _, tc := range []struct {
		password, email string
		ok              bool
	}{
		{"short", "", false},
		{"123456789", "", false},
		{"1234567890", "", true},
		{"alllowercaseletters", "", true}, // no composition rules
		{"with spaces and unicode é€", "", true},
		{"éééééééé", "", false}, // 8 runes
		{strings.Repeat("a", 257), "", false},
		{"me@example.com", "me@example.com", false},
	} {
		err := auth.ValidatePassword(tc.password, tc.email)
		if (err == nil) != tc.ok {
			t.Errorf("ValidatePassword(%q, %q) = %v, want ok=%v", tc.password, tc.email, err, tc.ok)
		}
	}
}
