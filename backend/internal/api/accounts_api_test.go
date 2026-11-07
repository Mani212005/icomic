package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

const testOrigin = "http://localhost:5173"

var fastHasher = auth.PasswordParams{MemoryKiB: 8, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 16}

type accountHarness struct {
	t       *testing.T
	repo    *db.InMemoryRepository
	server  *api.Server
	handler http.Handler
}

// newAccountHarness runs with real session auth and no dev identity, as production does.
func newAccountHarness(t *testing.T) *accountHarness {
	t.Helper()
	repo := db.NewInMemoryRepository()
	s := api.NewServer(repo, credits.NewInMemoryCreditService(nil), jobs.NewInMemoryJobService(),
		gen.NewFakeAIServiceClient(), auth.NewDevAuthenticator(false, ""), auth.NewDefaultImageHosting("/media"), 2)
	s.Accounts.Hasher = auth.NewPasswordHasher(fastHasher, 4)
	return &accountHarness{t: t, repo: repo, server: s, handler: s.NewRouter()}
}

// browser is a cookie jar plus the CSRF token the page keeps in memory.
type browser struct {
	h      *accountHarness
	cookie *http.Cookie
	csrf   string
	ip     string
}

func (h *accountHarness) newBrowser() *browser { return &browser{h: h, ip: "198.51.100.7:4000"} }

type reply struct {
	*httptest.ResponseRecorder
}

func (r reply) json() map[string]any {
	var out map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	return out
}

func (r reply) code() string {
	s, _ := r.json()["code"].(string)
	return s
}

func (r reply) account() map[string]any {
	a, _ := r.json()["account"].(map[string]any)
	return a
}

func (b *browser) do(method, path string, body any, mutate ...func(*http.Request)) reply {
	b.h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Host = "localhost:8080"
	req.RemoteAddr = b.ip
	req.Header.Set("Origin", testOrigin)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	if b.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", b.csrf)
	}
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	b.h.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			if c.MaxAge < 0 {
				b.cookie, b.csrf = nil, ""
			} else {
				b.cookie = c
			}
		}
	}
	if acc := (reply{rec}).account(); acc != nil {
		if tok, ok := acc["csrf_token"].(string); ok {
			b.csrf = tok
		}
	}
	return reply{rec}
}

func (b *browser) guest() reply { return b.do("POST", "/api/v1/auth/guest", nil) }

func (b *browser) createProject(title string) reply {
	return b.do("POST", "/api/v1/projects", map[string]any{
		"title": title, "original_script": "A brave fox crosses a quiet river at dawn and finds a lantern.",
	})
}

func projectID(r reply) string {
	p, _ := r.json()["project"].(map[string]any)
	id, _ := p["id"].(string)
	return id
}

func TestGuestSessionCookieAndAccount(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	if anon := b.do("GET", "/api/v1/auth/me", nil); anon.Code != 200 || anon.json()["account"] != nil {
		t.Fatalf("anonymous me should be a null account: %d %s", anon.Code, anon.Body)
	}
	res := b.guest()
	if res.Code != http.StatusCreated {
		t.Fatalf("guest: %d %s", res.Code, res.Body)
	}
	acc := res.account()
	if acc["kind"] != "guest" || acc["email"] != nil || acc["project_limit"] != float64(1) || acc["project_count"] != float64(0) || acc["csrf_token"] == "" {
		t.Fatalf("unexpected guest account: %v", acc)
	}
	c := b.cookie
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("session cookie must be httpOnly, SameSite=Lax and persistent: %+v", c)
	}
	if c.Secure {
		t.Fatal("Secure must be off on localhost")
	}
	if strings.Contains(res.Body.String(), c.Value) {
		t.Fatal("the cookie token must never appear in a response body")
	}
	again := b.guest()
	if again.Code != http.StatusOK || h.repo.UserCount() != 1 {
		t.Fatalf("guest endpoint must be idempotent: %d users=%d", again.Code, h.repo.UserCount())
	}
	if me := b.do("GET", "/api/v1/auth/me", nil); me.account()["kind"] != "guest" {
		t.Fatalf("me should echo the guest: %s", me.Body)
	}
}

func TestCookieIsSecureOffLoopback(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	b.guest()
	if b.cookie.Secure {
		t.Fatal("localhost is exempt")
	}
	b2 := h.newBrowser()
	res := b2.do("POST", "/api/v1/auth/guest", nil, func(r *http.Request) { r.Host = "api.icomic.example" })
	if res.Code != http.StatusCreated || !b2.cookie.Secure {
		t.Fatalf("cookie must be Secure on a public host: %d %+v", res.Code, b2.cookie)
	}
}

func TestGuestKeepsExactlyOneProjectAndDeleteFreesTheSlot(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	b.guest()
	first := b.createProject("First")
	if first.Code != http.StatusCreated {
		t.Fatalf("first project: %d %s", first.Code, first.Body)
	}
	second := b.createProject("Second")
	if second.Code != http.StatusForbidden || second.code() != "project_limit_reached" {
		t.Fatalf("second project must be refused: %d %s", second.Code, second.Body)
	}
	details, _ := second.json()["details"].(map[string]any)
	if details["limit"] != float64(1) || details["account_kind"] != "guest" || second.json()["message"] == "" {
		t.Fatalf("error envelope: %s", second.Body)
	}
	if n, _ := h.repo.CountProjects(nil, b.userID(t)); n != 1 {
		t.Fatalf("exactly one project expected, got %d", n)
	}
	if del := b.do("DELETE", "/api/v1/projects/"+projectID(first), nil); del.Code >= 300 {
		t.Fatalf("delete: %d %s", del.Code, del.Body)
	}
	if again := b.createProject("Third"); again.Code != http.StatusCreated {
		t.Fatalf("delete must free the slot: %d %s", again.Code, again.Body)
	}
	if me := b.do("GET", "/api/v1/auth/me", nil).account(); me["project_count"] != float64(1) || me["project_limit"] != float64(1) {
		t.Fatalf("me counts: %v", me)
	}
}

func (b *browser) userID(t *testing.T) string {
	t.Helper()
	id, err := auth.NewSessionAuthenticator(b.h.server.Accounts).Authenticate(b.request())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *browser) request() *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	return req
}

func TestGuestsAreIsolatedFromEachOther(t *testing.T) {
	h := newAccountHarness(t)
	a, b := h.newBrowser(), h.newBrowser()
	a.guest()
	b.guest()
	pa := a.createProject("A")
	if b.createProject("B").Code != http.StatusCreated {
		t.Fatal("each guest gets their own project")
	}
	if get := b.do("GET", "/api/v1/projects/"+projectID(pa), nil); get.Code != http.StatusForbidden {
		t.Fatalf("a guest must not read another guest's project: %d", get.Code)
	}
}

func TestSignedInUserCreatesManyUpToTheCeiling(t *testing.T) {
	h := newAccountHarness(t)
	h.repo.SetAccountPolicy(domain.AccountPolicy{GuestMaxProjects: 1, RegisteredMaxProjects: 3})
	b := h.newBrowser()
	if res := b.do("POST", "/api/v1/auth/signup", map[string]string{"email": "Maker@Example.com", "password": "a long enough password"}); res.Code != http.StatusCreated {
		t.Fatalf("signup: %d %s", res.Code, res.Body)
	}
	for i := 0; i < 3; i++ {
		if res := b.createProject("P"); res.Code != http.StatusCreated {
			t.Fatalf("project %d: %d %s", i, res.Code, res.Body)
		}
	}
	over := b.createProject("Over")
	if over.Code != http.StatusForbidden || over.code() != "project_limit_reached" {
		t.Fatalf("ceiling not enforced: %d %s", over.Code, over.Body)
	}
	if d, _ := over.json()["details"].(map[string]any); d["account_kind"] != "registered" || d["limit"] != float64(3) {
		t.Fatalf("details: %s", over.Body)
	}
	me := b.do("GET", "/api/v1/auth/me", nil).account()
	if me["kind"] != "registered" || me["email"] != "maker@example.com" || me["project_limit"] != float64(3) {
		t.Fatalf("me: %v", me)
	}
}

func TestDefaultPolicyIsOneGuestOneHundredRegistered(t *testing.T) {
	p, _ := db.NewInMemoryRepository().GetAccountPolicy(nil)
	if p.GuestMaxProjects != 1 || p.RegisteredMaxProjects != 100 {
		t.Fatalf("defaults: %+v", p)
	}
}

func TestDevIdentityStaysUnlimited(t *testing.T) {
	_, handler, repo := setupTestServer(true, "dev-user")
	repo.(*db.InMemoryRepository).SetAccountPolicy(domain.AccountPolicy{GuestMaxProjects: 1, RegisteredMaxProjects: 1})
	for i := 0; i < 4; i++ {
		body, _ := json.Marshal(map[string]any{"title": "Dev", "original_script": "A brave fox crosses a quiet river at dawn and finds a lantern."})
		req := httptest.NewRequest("POST", "/api/v1/projects", bytes.NewReader(body))
		req.Header.Set("X-User-ID", "dev-user")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("dev project %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	me := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.Header.Set("X-User-ID", "dev-user")
	handler.ServeHTTP(me, req)
	var out struct{ Account map[string]any }
	_ = json.Unmarshal(me.Body.Bytes(), &out)
	if out.Account["kind"] != "registered" || out.Account["project_limit"] != nil || out.Account["csrf_token"] != nil {
		t.Fatalf("dev account: %s", me.Body)
	}
}

type plainAuth struct{}

func (plainAuth) Authenticate(*http.Request) (string, error) { return "plain", nil }

func TestIdentityWithoutRegisteredAccountIsGuest(t *testing.T) {
	repo := db.NewInMemoryRepository()
	s := api.NewServer(repo, credits.NewInMemoryCreditService(nil), jobs.NewInMemoryJobService(), gen.NewFakeAIServiceClient(), plainAuth{}, nil, 2)
	h := s.NewRouter()
	post := func() int {
		body, _ := json.Marshal(map[string]any{"title": "T", "original_script": "A brave fox crosses a quiet river at dawn and finds a lantern."})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/projects", bytes.NewReader(body)))
		return rec.Code
	}
	if a, b := post(), post(); a != http.StatusCreated || b != http.StatusForbidden {
		t.Fatalf("a plain identity is a guest: %d then %d", a, b)
	}
}

func TestCSRFRequiredOnCookieAuthenticatedWrites(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	b.guest()
	token := b.csrf
	for name, mutate := range map[string]func(*http.Request){
		"missing token": func(r *http.Request) { r.Header.Del("X-CSRF-Token") },
		"wrong token":   func(r *http.Request) { r.Header.Set("X-CSRF-Token", "not-the-token") },
		"foreign origin": func(r *http.Request) {
			r.Header.Set("Origin", "https://evil.example")
		},
	} {
		res := b.do("POST", "/api/v1/projects", map[string]any{"title": "x", "original_script": "A brave fox crosses a quiet river at dawn and finds a lantern."}, mutate)
		if res.Code != http.StatusForbidden {
			t.Errorf("%s: want 403, got %d %s", name, res.Code, res.Body)
		}
	}
	if n, _ := h.repo.CountProjects(nil, b.userID(t)); n != 0 {
		t.Fatal("rejected writes must not create projects")
	}
	// Reads need no token, and the right token works.
	if b.do("GET", "/api/v1/projects", nil).Code != 200 {
		t.Fatal("reads do not need CSRF")
	}
	if res := b.do("POST", "/api/v1/projects", map[string]any{"title": "ok", "original_script": "A brave fox crosses a quiet river at dawn and finds a lantern."}); res.Code != http.StatusCreated || b.csrf != token {
		t.Fatalf("valid token: %d %s", res.Code, res.Body)
	}
	if res := b.do("POST", "/api/v1/projects", map[string]any{"title": "x"}, func(r *http.Request) { r.Header.Set("X-CSRF-Token", "bad") }); res.code() != "csrf_invalid" {
		t.Fatalf("stable code expected: %s", res.Body)
	}
}

func TestAuthEndpointsRejectForeignOriginAndNonJSON(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	creds := map[string]string{"email": "a@example.com", "password": "a long enough password"}
	for _, path := range []string{"/api/v1/auth/guest", "/api/v1/auth/signup", "/api/v1/auth/signin"} {
		res := b.do("POST", path, creds, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
		if res.Code != http.StatusForbidden || res.code() != "origin_not_allowed" {
			t.Errorf("%s: %d %s", path, res.Code, res.Body)
		}
	}
	res := b.do("POST", "/api/v1/auth/signup", creds, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") })
	if res.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("form-style content types must be refused: %d", res.Code)
	}
	if h.repo.UserCount() != 0 {
		t.Fatal("rejected requests must not create users")
	}
	for _, bad := range []string{`{"email":`, `{"email":"a@example.com","password":"a long enough password"}{"x":1}`, strings.Repeat("a", 9000)} {
		res := b.do("POST", "/api/v1/auth/signup", nil, func(r *http.Request) {
			r.Body = http.NoBody
			r.Body = nopCloser(bad)
			r.Header.Set("Content-Type", "application/json")
		})
		if res.Code != http.StatusBadRequest || res.code() != "invalid_request" {
			t.Errorf("payload %.20q: %d %s", bad, res.Code, res.Body)
		}
	}
}

func TestSignUpValidation(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	for _, tc := range []struct{ email, password, code string }{
		{"not-an-email", "a long enough password", "invalid_email"},
		{"Name <a@example.com>", "a long enough password", "invalid_email"},
		{"a@nodot", "a long enough password", "invalid_email"},
		{"a@example.com", "short", "weak_password"},
		{"a@example.com", "a@example.com", "weak_password"},
	} {
		res := b.do("POST", "/api/v1/auth/signup", map[string]string{"email": tc.email, "password": tc.password})
		if res.Code != http.StatusBadRequest || res.code() != tc.code {
			t.Errorf("%+v: %d %s", tc, res.Code, res.Body)
		}
	}
	if h.repo.UserCount() != 0 {
		t.Fatal("invalid sign-ups create nothing")
	}
}

func TestPasswordIsHashedWithArgon2idAndNeverReturned(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	res := b.do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	if strings.Contains(res.Body.String(), "argon2") || strings.Contains(res.Body.String(), "a long enough password") || strings.Contains(res.Body.String(), "password") {
		t.Fatalf("response leaks credentials: %s", res.Body)
	}
	u, err := h.repo.GetUserByEmail(nil, "A@EXAMPLE.COM")
	if err != nil || !strings.HasPrefix(u.PasswordHash, "$argon2id$v=19$") || u.AccountKind != domain.AccountRegistered {
		t.Fatalf("stored user: %+v err=%v", u, err)
	}
}

func TestDuplicateEmailIsCaseInsensitive(t *testing.T) {
	h := newAccountHarness(t)
	h.newBrowser().do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	res := h.newBrowser().do("POST", "/api/v1/auth/signup", map[string]string{"email": "A@Example.COM", "password": "another long password"})
	if res.Code != http.StatusConflict || res.code() != "email_taken" {
		t.Fatalf("duplicate: %d %s", res.Code, res.Body)
	}
}

func TestSignUpClaimsGuestProjectAndRotatesSession(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	b.guest()
	created := b.createProject("Mine")
	guestID := b.userID(t)
	oldCookie := b.cookie

	res := b.do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	if res.Code != http.StatusCreated || res.account()["kind"] != "registered" || res.account()["project_count"] != float64(1) {
		t.Fatalf("signup: %d %s", res.Code, res.Body)
	}
	if b.userID(t) != guestID {
		t.Fatal("the guest becomes the account in place, so its project stays attached")
	}
	if b.cookie.Value == oldCookie.Value {
		t.Fatal("the session must rotate on sign-up")
	}
	// The pre-sign-up cookie is dead.
	stale := h.newBrowser()
	stale.cookie = oldCookie
	if stale.do("GET", "/api/v1/projects", nil).Code != http.StatusUnauthorized {
		t.Fatal("the old session must be revoked")
	}
	if get := b.do("GET", "/api/v1/projects/"+projectID(created), nil); get.Code != 200 {
		t.Fatalf("project must still be reachable: %d", get.Code)
	}
	if b.createProject("Second").Code != http.StatusCreated {
		t.Fatal("a registered user may create more")
	}
}

func TestSignInWrongPasswordAndUnknownEmailAreIndistinguishable(t *testing.T) {
	h := newAccountHarness(t)
	h.newBrowser().do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})

	before := h.server.Accounts.Hasher.Runs()
	wrong := h.newBrowser().do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "not the password"})
	mid := h.server.Accounts.Hasher.Runs()
	unknown := h.newBrowser().do("POST", "/api/v1/auth/signin", map[string]string{"email": "nobody@example.com", "password": "not the password"})
	after := h.server.Accounts.Hasher.Runs()

	if wrong.Code != http.StatusUnauthorized || unknown.Code != wrong.Code || wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("responses differ: %d %s vs %d %s", wrong.Code, wrong.Body, unknown.Code, unknown.Body)
	}
	if wrong.code() != "invalid_credentials" {
		t.Fatalf("code: %s", wrong.Body)
	}
	// The unknown-email path still pays for one argon2 run (the first call also warms the dummy hash).
	if wrongRuns, unknownRuns := mid-before, after-mid; unknownRuns != 1 || wrongRuns < 1 {
		t.Fatalf("both paths must do hashing work: wrong=%d unknown=%d", wrongRuns, unknownRuns)
	}
	if wrong.Header().Get("Set-Cookie") != "" {
		t.Fatal("a failed sign-in must not set a cookie")
	}
}

func TestSignInRejectsGuestAndForeignHashRows(t *testing.T) {
	h := newAccountHarness(t)
	_ = h.repo.CreateUser(nil, &domain.User{ID: "dev", Email: "dev@icomic.local", PasswordHash: "$2a$10$wN9P31K9LpQn88bWq4KqreD/4aUa240GZc5R9lHjEaZ9a.Z7yN.2G", AccountKind: domain.AccountRegistered})
	res := h.newBrowser().do("POST", "/api/v1/auth/signin", map[string]string{"email": "dev@icomic.local", "password": "password1234"})
	if res.Code != http.StatusUnauthorized || res.code() != "invalid_credentials" {
		t.Fatalf("legacy hash must never verify: %d %s", res.Code, res.Body)
	}
	b := h.newBrowser()
	b.guest()
	if res := b.do("POST", "/api/v1/auth/signin", map[string]string{"email": "", "password": ""}); res.Code != http.StatusUnauthorized {
		t.Fatalf("empty credentials: %d", res.Code)
	}
}

func TestSignInRateLimitPerEmailAndPerIP(t *testing.T) {
	h := newAccountHarness(t)
	h.newBrowser().do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	attacker := h.newBrowser()
	for i := 0; i < 5; i++ {
		if res := attacker.do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "guess number " + string(rune('a'+i))}); res.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, res.Code)
		}
	}
	blocked := attacker.do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	if blocked.Code != http.StatusTooManyRequests || blocked.code() != "login_rate_limited" || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("expected rate limit: %d %s", blocked.Code, blocked.Body)
	}
	if blocked.json()["retryable"] != true {
		t.Fatalf("envelope: %s", blocked.Body)
	}
	// Same email from another IP is also locked: the limit follows the email, so spraying IPs does not help.
	other := h.newBrowser()
	other.ip = "203.0.113.9:1"
	if res := other.do("POST", "/api/v1/auth/signin", map[string]string{"email": "A@example.com ", "password": "a long enough password"}); res.Code != http.StatusTooManyRequests {
		t.Fatalf("per-email limit must hold across IPs and case: %d", res.Code)
	}
	// An unknown email is limited identically, so the limiter does not reveal which emails exist.
	for i := 0; i < 5; i++ {
		other.do("POST", "/api/v1/auth/signin", map[string]string{"email": "ghost@example.com", "password": "guess guess guess"})
	}
	if res := other.do("POST", "/api/v1/auth/signin", map[string]string{"email": "ghost@example.com", "password": "guess guess guess"}); res.Code != http.StatusTooManyRequests {
		t.Fatalf("unknown emails are limited too: %d", res.Code)
	}
}

func TestSignInRateLimitPerIPAcrossEmails(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	var last reply
	for i := 0; i < 21; i++ {
		last = b.do("POST", "/api/v1/auth/signin", map[string]string{"email": "user" + string(rune('a'+i)) + "@example.com", "password": "guess guess guess"})
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("per-IP limit: %d", last.Code)
	}
}

func TestSuccessfulSignInClearsEmailFailures(t *testing.T) {
	h := newAccountHarness(t)
	h.newBrowser().do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	b := h.newBrowser()
	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			b.do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "wrong wrong wrong"})
		}
		if res := b.do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "a long enough password"}); res.Code != http.StatusOK {
			t.Fatalf("round %d: success must not be blocked by 4 earlier failures: %d", round, res.Code)
		}
	}
}

func TestSignUpAndGuestCreationAreRateLimitedPerIP(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	var last reply
	for i := 0; i < 11; i++ {
		last = b.do("POST", "/api/v1/auth/signup", map[string]string{"email": "u" + string(rune('a'+i)) + "@example.com", "password": "a long enough password"})
	}
	if last.Code != http.StatusTooManyRequests || last.code() != "login_rate_limited" {
		t.Fatalf("sign-up flood: %d %s", last.Code, last.Body)
	}
	for i := 0; i < 21; i++ {
		fresh := h.newBrowser()
		fresh.ip = "192.0.2.1:99"
		last = fresh.guest()
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("guest flood: %d", last.Code)
	}
}

func TestSignInClaimsGuestProjectIdempotentlyAndRespectsCeiling(t *testing.T) {
	h := newAccountHarness(t)
	owner := h.newBrowser()
	owner.do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	ownerID := owner.userID(t)
	owner.createProject("Existing")

	b := h.newBrowser()
	b.guest()
	created := b.createProject("Guest work")
	guestID := b.userID(t)
	res := b.do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	if res.Code != http.StatusOK || res.json()["claimed_projects"] != float64(1) || res.account()["project_count"] != float64(2) {
		t.Fatalf("claim: %d %s", res.Code, res.Body)
	}
	if b.userID(t) != ownerID {
		t.Fatal("signed in as the account")
	}
	if p, _ := h.repo.GetProject(nil, projectID(created)); p.UserID != ownerID {
		t.Fatalf("project must belong to the account, got %s", p.UserID)
	}
	if n, _ := h.repo.CountProjects(nil, guestID); n != 0 {
		t.Fatal("the guest keeps nothing")
	}
	// Signing in again with no guest session claims nothing and changes nothing.
	again := h.newBrowser().do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	if again.json()["claimed_projects"] != nil || again.account()["project_count"] != float64(2) {
		t.Fatalf("idempotent: %s", again.Body)
	}
	if n, _ := h.repo.ClaimGuestProjects(nil, guestID, ownerID); n != 0 {
		t.Fatal("claiming from an empty guest is a no-op")
	}

	// A full account never exceeds the ceiling when claiming.
	h.repo.SetAccountPolicy(domain.AccountPolicy{GuestMaxProjects: 1, RegisteredMaxProjects: 2})
	full := h.newBrowser()
	full.guest()
	full.createProject("Overflow")
	res = full.do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	if res.Code != http.StatusOK || res.account()["project_count"] != float64(2) || res.json()["claimed_projects"] != nil {
		t.Fatalf("ceiling respected: %s", res.Body)
	}
}

func TestSignInNeverMovesProjectsOutOfARegisteredAccount(t *testing.T) {
	h := newAccountHarness(t)
	a := h.newBrowser()
	a.do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	pa := a.createProject("A's")
	b := h.newBrowser()
	b.do("POST", "/api/v1/auth/signup", map[string]string{"email": "b@example.com", "password": "a long enough password"})
	// b, signed in as a registered account, signs into a: nothing of b's moves, and a's stays put.
	b.createProject("B's")
	res := b.do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	if res.Code != http.StatusOK || res.account()["project_count"] != float64(1) {
		t.Fatalf("signin: %s", res.Body)
	}
	if p, _ := h.repo.GetProject(nil, projectID(pa)); p.UserID != a.userID(t) {
		t.Fatal("projects of a registered account never move")
	}
}

func TestSignOutRevokesSessionOnTheServer(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	b.do("POST", "/api/v1/auth/signup", map[string]string{"email": "a@example.com", "password": "a long enough password"})
	saved := b.cookie
	savedCSRF := b.csrf

	if res := b.do("POST", "/api/v1/auth/signout", nil, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }); res.Code != http.StatusForbidden {
		t.Fatalf("sign-out needs the CSRF token: %d", res.Code)
	}
	if res := b.do("POST", "/api/v1/auth/signout", nil); res.Code != http.StatusNoContent {
		t.Fatalf("sign-out: %d %s", res.Code, res.Body)
	}
	if b.cookie != nil {
		t.Fatal("the cookie must be cleared")
	}
	replay := h.newBrowser()
	replay.cookie, replay.csrf = saved, savedCSRF
	if res := replay.do("GET", "/api/v1/projects", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("a captured cookie must stop working after sign-out: %d", res.Code)
	}
	if me := replay.do("GET", "/api/v1/auth/me", nil); me.json()["account"] != nil {
		t.Fatalf("revoked session is anonymous: %s", me.Body)
	}
	// Signing out without a session is harmless.
	if res := h.newBrowser().do("POST", "/api/v1/auth/signout", nil); res.Code != http.StatusNoContent {
		t.Fatalf("anonymous sign-out: %d", res.Code)
	}
	// And the account can sign in again.
	if res := h.newBrowser().do("POST", "/api/v1/auth/signin", map[string]string{"email": "a@example.com", "password": "a long enough password"}); res.Code != http.StatusOK {
		t.Fatalf("sign back in: %d", res.Code)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	b.guest()
	h.server.Accounts.Now = func() time.Time { return time.Now().Add(400 * 24 * time.Hour) }
	if res := b.do("GET", "/api/v1/projects", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("expired guest session: %d", res.Code)
	}
}

func TestSessionCookieDoesNotAuthenticateWithSwappedHeaders(t *testing.T) {
	h := newAccountHarness(t)
	b := h.newBrowser()
	b.guest()
	res := b.do("GET", "/api/v1/projects", nil, func(r *http.Request) { r.Header.Set("X-User-ID", "someone-else") })
	if res.Code != 200 {
		t.Fatalf("session read: %d", res.Code)
	}
	forged := h.newBrowser()
	forged.cookie = &http.Cookie{Name: auth.SessionCookieName, Value: "forged-token"}
	if forged.do("GET", "/api/v1/projects", nil).Code != http.StatusUnauthorized {
		t.Fatal("unknown tokens are refused")
	}
}

func TestProjectCreationPathsAllEnforceTheLimit(t *testing.T) {
	// The unrestricted Repository.CreateProject exists for seeding only. Request handlers must go
	// through CreateProjectForAccount so every creating path obeys the same rule.
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		if regexp.MustCompile(`\.CreateProject\(`).Match(src) {
			t.Errorf("%s calls the unrestricted CreateProject; use CreateProjectForAccount", f)
		}
	}
}

func nopCloser(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
