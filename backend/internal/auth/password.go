package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// PasswordParams are the argon2id cost parameters. They are written into each stored hash,
// so raising them later keeps old hashes verifiable and NeedsRehash flags them.
type PasswordParams struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
	KeyLen    uint32
	SaltLen   uint32
}

// DefaultPasswordParams: 32 MiB, 3 passes, 1 lane. This exceeds the OWASP minimum of 19 MiB with 2 passes.
// Peak memory is bounded by the hasher's concurrency limit (4 by default, so 128 MiB).
var DefaultPasswordParams = PasswordParams{MemoryKiB: 32 * 1024, Time: 3, Threads: 1, KeyLen: 32, SaltLen: 16}

const (
	MinPasswordLength = 10
	// MaxPasswordBytes bounds the work an attacker can force per request.
	MaxPasswordBytes = 256

	maxMemoryKiB = 256 * 1024
	maxTime      = 10
	maxThreads   = 16
)

type PasswordHasher struct {
	params PasswordParams
	slots  chan struct{}
	runs   atomic.Int64
}

// Runs counts argon2 computations (hash and verify). Tests use it to prove failure paths do the same work.
func (h *PasswordHasher) Runs() int64 { return h.runs.Load() }

// NewPasswordHasher bounds concurrent hashes so a login burst cannot exhaust memory.
func NewPasswordHasher(params PasswordParams, concurrency int) *PasswordHasher {
	if concurrency <= 0 {
		concurrency = 4
	}
	return &PasswordHasher{params: params, slots: make(chan struct{}, concurrency)}
}

func (h *PasswordHasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *PasswordHasher) release() { <-h.slots }

// ValidatePassword applies the password policy: length only, no composition rules.
func ValidatePassword(password, email string) error {
	switch {
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return fmt.Errorf("%w: use at least %d characters", ErrInvalidPassword, MinPasswordLength)
	case len(password) > MaxPasswordBytes:
		return fmt.Errorf("%w: use at most %d bytes", ErrInvalidPassword, MaxPasswordBytes)
	case email != "" && strings.EqualFold(password, email):
		return fmt.Errorf("%w: do not reuse the email address", ErrInvalidPassword)
	}
	return nil
}

// Hash returns a PHC-format argon2id string with a fresh random salt.
func (h *PasswordHasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	h.runs.Add(1)
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.MemoryKiB, h.params.Threads, h.params.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		h.params.MemoryKiB, h.params.Time, h.params.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

type parsedHash struct {
	params PasswordParams
	salt   []byte
	key    []byte
}

func parseHash(encoded string) (parsedHash, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return parsedHash{}, false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return parsedHash{}, false
	}
	var p PasswordParams
	var threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.MemoryKiB, &p.Time, &threads); err != nil {
		return parsedHash{}, false
	}
	if p.MemoryKiB == 0 || p.MemoryKiB > maxMemoryKiB || p.Time == 0 || p.Time > maxTime || threads == 0 || threads > maxThreads {
		return parsedHash{}, false
	}
	p.Threads = uint8(threads)
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return parsedHash{}, false
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return parsedHash{}, false
	}
	p.KeyLen, p.SaltLen = uint32(len(key)), uint32(len(salt))
	return parsedHash{params: p, salt: salt, key: key}, true
}

// Verify checks a password in constant time. A malformed or foreign hash never verifies.
// needsRehash is true when the stored parameters differ from the current ones.
func (h *PasswordHasher) Verify(ctx context.Context, password, encoded string) (ok, needsRehash bool, err error) {
	parsed, valid := parseHash(encoded)
	if !valid {
		return false, false, nil
	}
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()
	h.runs.Add(1)
	key := argon2.IDKey([]byte(password), parsed.salt, parsed.params.Time, parsed.params.MemoryKiB, parsed.params.Threads, parsed.params.KeyLen)
	if subtle.ConstantTimeCompare(key, parsed.key) != 1 {
		return false, false, nil
	}
	return true, parsed.params != h.params, nil
}
