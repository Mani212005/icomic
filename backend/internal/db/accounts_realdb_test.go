//go:build realdb

package db_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

const devUserID = "00000000-0000-0000-0000-000000000001"

func newUser(t *testing.T, repo *db.PostgresRepository, ctx context.Context, kind domain.AccountKind) *domain.User {
	t.Helper()
	u := &domain.User{AccountKind: kind}
	if kind == domain.AccountRegistered {
		u.Email, u.PasswordHash = uuid.NewString()+"@realdb.test", "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5"
	}
	must(t, repo.CreateUser(ctx, u))
	return u
}

// warmPool opens every pooled connection so concurrent goroutines really overlap
// instead of queueing behind lazy connection setup.
func warmPool(t *testing.T, pool *sql.DB, ctx context.Context) {
	t.Helper()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := pool.ExecContext(ctx, `SELECT pg_sleep(0.2)`); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func newProject(userID string) *domain.Project {
	return &domain.Project{UserID: userID, Title: "P", OriginalScript: "A hero saves the town.", ChapterCount: 1, ArtStyle: "classic-comic", Status: "draft"}
}

var guestAccess = domain.ProjectAccess{Kind: domain.AccountGuest}
var registeredAccess = domain.ProjectAccess{Kind: domain.AccountRegistered}

func TestRealDBAccountPolicyDefaultsAndUserKinds(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	policy, err := repo.GetAccountPolicy(ctx)
	if err != nil || policy.GuestMaxProjects != 1 || policy.RegisteredMaxProjects != 100 {
		t.Fatalf("default policy: %+v err=%v", policy, err)
	}
	guest := newUser(t, repo, ctx, domain.AccountGuest)
	got, err := repo.GetUserByID(ctx, guest.ID)
	if err != nil || got.AccountKind != domain.AccountGuest || got.Email != "" || got.PasswordHash != "" {
		t.Fatalf("guest row: %+v err=%v", got, err)
	}
	reg := newUser(t, repo, ctx, domain.AccountRegistered)
	if got, _ := repo.GetUserByID(ctx, reg.ID); got.AccountKind != domain.AccountRegistered || got.Email == "" {
		t.Fatalf("registered row: %+v", got)
	}
}

func TestRealDBEmailIsUniqueCaseInsensitively(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	must(t, repo.CreateUser(ctx, &domain.User{Email: "Mixed@Example.com", PasswordHash: "h", AccountKind: domain.AccountRegistered}))
	err := repo.CreateUser(ctx, &domain.User{Email: "mixed@example.COM", PasswordHash: "h", AccountKind: domain.AccountRegistered})
	if !errors.Is(err, db.ErrDuplicateKey) {
		t.Fatalf("expected duplicate, got %v", err)
	}
	if u, err := repo.GetUserByEmail(ctx, "MIXED@example.com"); err != nil || u.Email != "Mixed@Example.com" {
		t.Fatalf("case-insensitive lookup: %+v err=%v", u, err)
	}
	// Many guests with no email never collide.
	for i := 0; i < 3; i++ {
		newUser(t, repo, ctx, domain.AccountGuest)
	}
}

func TestRealDBRegisteredRowsMustHaveCredentials(t *testing.T) {
	pool, _, ctx := realDatabase(t)
	_, err := pool.ExecContext(ctx, `INSERT INTO users (id, account_kind) VALUES ($1, 'registered')`, uuid.NewString())
	if err == nil {
		t.Fatal("a registered user without email and password must be refused by the database")
	}
}

func TestRealDBGuestCreatesExactlyOneProjectUnderConcurrency(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	warmPool(t, pool, ctx)
	guest := newUser(t, repo, ctx, domain.AccountGuest)
	const attempts = 40
	var ok, limited, other atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var limit *domain.ProjectLimitError
			switch err := repo.CreateProjectForAccount(ctx, newProject(guest.ID), guestAccess); {
			case err == nil:
				ok.Add(1)
			case errors.As(err, &limit):
				limited.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
				other.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	n, _ := repo.CountProjects(ctx, guest.ID)
	if ok.Load() != 1 || limited.Load() != attempts-1 || n != 1 {
		t.Fatalf("ok=%d limited=%d other=%d stored=%d", ok.Load(), limited.Load(), other.Load(), n)
	}
}

func TestRealDBConcurrentCreatesRespectTheRegisteredCeiling(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	warmPool(t, pool, ctx)
	if _, err := pool.ExecContext(ctx, `UPDATE account_policy SET registered_max_projects = 5`); err != nil {
		t.Fatal(err)
	}
	user := newUser(t, repo, ctx, domain.AccountRegistered)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if repo.CreateProjectForAccount(ctx, newProject(user.ID), registeredAccess) == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if n, _ := repo.CountProjects(ctx, user.ID); ok.Load() != 5 || n != 5 {
		t.Fatalf("ceiling 5: ok=%d stored=%d", ok.Load(), n)
	}
}

// The race tests above overlap only when scheduling cooperates; this one proves the lock itself.
func TestRealDBProjectCreationWaitsForThePerUserAdvisoryLock(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	guest := newUser(t, repo, ctx, domain.AccountGuest)
	holder, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('icomic:projects:' || $1::text, 0))`, guest.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- repo.CreateProjectForAccount(ctx, newProject(guest.ID), guestAccess) }()
	select {
	case err := <-done:
		t.Fatalf("creation must wait for the user's lock, returned %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	// Another user is not blocked by this user's lock.
	other := newUser(t, repo, ctx, domain.AccountGuest)
	must(t, repo.CreateProjectForAccount(ctx, newProject(other.ID), guestAccess))
	must(t, holder.Commit())
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("creation did not resume after the lock was released")
	}
}

func TestRealDBLimitsAreIndependentPerUser(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	a, b := newUser(t, repo, ctx, domain.AccountGuest), newUser(t, repo, ctx, domain.AccountGuest)
	must(t, repo.CreateProjectForAccount(ctx, newProject(a.ID), guestAccess))
	must(t, repo.CreateProjectForAccount(ctx, newProject(b.ID), guestAccess))
}

func TestRealDBRegisteredUserCreatesMany(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := newUser(t, repo, ctx, domain.AccountRegistered)
	for i := 0; i < 12; i++ {
		must(t, repo.CreateProjectForAccount(ctx, newProject(user.ID), registeredAccess))
	}
	if n, _ := repo.CountProjects(ctx, user.ID); n != 12 {
		t.Fatalf("stored %d", n)
	}
}

func TestRealDBDeleteFreesTheSlot(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	guest := newUser(t, repo, ctx, domain.AccountGuest)
	first := newProject(guest.ID)
	must(t, repo.CreateProjectForAccount(ctx, first, guestAccess))
	var limit *domain.ProjectLimitError
	if err := repo.CreateProjectForAccount(ctx, newProject(guest.ID), guestAccess); !errors.As(err, &limit) || limit.Limit != 1 || limit.Kind != domain.AccountGuest {
		t.Fatalf("expected limit error, got %v", err)
	}
	must(t, repo.DeleteProject(ctx, first.ID))
	must(t, repo.CreateProjectForAccount(ctx, newProject(guest.ID), guestAccess))
}

func TestRealDBDevAccessIsUnlimitedAndOperatorsCanChangePolicy(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	dev := newUser(t, repo, ctx, domain.AccountGuest) // stored kind does not matter: the authenticator decides
	for i := 0; i < 5; i++ {
		must(t, repo.CreateProjectForAccount(ctx, newProject(dev.ID), domain.ProjectAccess{Kind: domain.AccountRegistered, Unlimited: true}))
	}
	if _, err := pool.ExecContext(ctx, `UPDATE account_policy SET guest_max_projects = 2`); err != nil {
		t.Fatal(err)
	}
	guest := newUser(t, repo, ctx, domain.AccountGuest)
	must(t, repo.CreateProjectForAccount(ctx, newProject(guest.ID), guestAccess))
	must(t, repo.CreateProjectForAccount(ctx, newProject(guest.ID), guestAccess))
	if err := repo.CreateProjectForAccount(ctx, newProject(guest.ID), guestAccess); err == nil {
		t.Fatal("policy change to 2 must apply immediately and still bound guests")
	}
}

func TestRealDBRegisterGuestKeepsProjectsAndDetectsDuplicates(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	guest := newUser(t, repo, ctx, domain.AccountGuest)
	project := newProject(guest.ID)
	must(t, repo.CreateProjectForAccount(ctx, project, guestAccess))
	must(t, repo.RegisterGuest(ctx, guest.ID, "me@realdb.test", "$argon2id$stub"))
	got, _ := repo.GetUserByID(ctx, guest.ID)
	if got.AccountKind != domain.AccountRegistered || got.Email != "me@realdb.test" {
		t.Fatalf("after register: %+v", got)
	}
	if p, _ := repo.GetProject(ctx, project.ID); p.UserID != guest.ID {
		t.Fatal("project must stay attached")
	}
	if err := repo.RegisterGuest(ctx, guest.ID, "other@realdb.test", "h"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("a registered user cannot be registered again: %v", err)
	}
	other := newUser(t, repo, ctx, domain.AccountGuest)
	if err := repo.RegisterGuest(ctx, other.ID, "ME@realdb.test", "h"); !errors.Is(err, db.ErrDuplicateKey) {
		t.Fatalf("duplicate email: %v", err)
	}
}

func TestRealDBClaimGuestProjects(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	account := newUser(t, repo, ctx, domain.AccountRegistered)
	guest := newUser(t, repo, ctx, domain.AccountGuest)
	project := newProject(guest.ID)
	must(t, repo.CreateProjectForAccount(ctx, project, guestAccess))
	must(t, repo.CreateProjectForAccount(ctx, newProject(account.ID), registeredAccess))

	moved, err := repo.ClaimGuestProjects(ctx, guest.ID, account.ID)
	if err != nil || moved != 1 {
		t.Fatalf("claim: moved=%d err=%v", moved, err)
	}
	if p, _ := repo.GetProject(ctx, project.ID); p.UserID != account.ID {
		t.Fatal("project not moved")
	}
	if moved, err := repo.ClaimGuestProjects(ctx, guest.ID, account.ID); err != nil || moved != 0 {
		t.Fatalf("second claim must be a no-op: moved=%d err=%v", moved, err)
	}
	// Never from a registered account, never into itself.
	if moved, _ := repo.ClaimGuestProjects(ctx, account.ID, guest.ID); moved != 0 {
		t.Fatal("registered accounts are not a claim source")
	}
	if moved, _ := repo.ClaimGuestProjects(ctx, account.ID, account.ID); moved != 0 {
		t.Fatal("self claim")
	}
	// Ceiling: only the oldest fits.
	if _, err := pool.ExecContext(ctx, `UPDATE account_policy SET registered_max_projects = 3`); err != nil {
		t.Fatal(err)
	}
	g2 := newUser(t, repo, ctx, domain.AccountGuest)
	must(t, repo.CreateProjectForAccount(ctx, newProject(g2.ID), guestAccess))
	if moved, _ := repo.ClaimGuestProjects(ctx, g2.ID, account.ID); moved != 1 {
		t.Fatalf("room for exactly one more: %d", moved)
	}
	g3 := newUser(t, repo, ctx, domain.AccountGuest)
	must(t, repo.CreateProjectForAccount(ctx, newProject(g3.ID), guestAccess))
	if moved, _ := repo.ClaimGuestProjects(ctx, g3.ID, account.ID); moved != 0 {
		t.Fatalf("a full account claims nothing, moved %d", moved)
	}
	if n, _ := repo.CountProjects(ctx, g3.ID); n != 1 {
		t.Fatal("the unclaimed project stays with the guest")
	}
}

func TestRealDBConcurrentClaimsDoNotDeadlockOrExceedTheCeiling(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	if _, err := pool.ExecContext(ctx, `UPDATE account_policy SET registered_max_projects = 3`); err != nil {
		t.Fatal(err)
	}
	a, b := newUser(t, repo, ctx, domain.AccountRegistered), newUser(t, repo, ctx, domain.AccountRegistered)
	var guests []*domain.User
	for i := 0; i < 8; i++ {
		g := newUser(t, repo, ctx, domain.AccountGuest)
		must(t, repo.CreateProjectForAccount(ctx, newProject(g.ID), guestAccess))
		guests = append(guests, g)
	}
	var wg sync.WaitGroup
	for i, g := range guests {
		wg.Add(1)
		target := a
		if i%2 == 1 {
			target = b
		}
		go func(g, target *domain.User) {
			defer wg.Done()
			if _, err := repo.ClaimGuestProjects(ctx, g.ID, target.ID); err != nil {
				t.Errorf("claim: %v", err)
			}
		}(g, target)
	}
	wg.Wait()
	for _, u := range []*domain.User{a, b} {
		if n, _ := repo.CountProjects(ctx, u.ID); n != 3 {
			t.Fatalf("account %s holds %d, want the ceiling 3", u.ID, n)
		}
	}
}

func TestRealDBSessions(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	user := newUser(t, repo, ctx, domain.AccountRegistered)
	hash := auth.HashSessionToken("token-1")
	now := time.Now().UTC()
	s := &domain.Session{TokenHash: hash, UserID: user.ID, CSRFToken: "csrf", ExpiresAt: now.Add(time.Hour)}
	must(t, repo.CreateSession(ctx, s))
	got, u, err := repo.GetSessionByTokenHash(ctx, hash, now)
	if err != nil || got.ID != s.ID || got.CSRFToken != "csrf" || u.ID != user.ID || u.AccountKind != domain.AccountRegistered {
		t.Fatalf("lookup: %+v %+v err=%v", got, u, err)
	}
	if _, _, err := repo.GetSessionByTokenHash(ctx, auth.HashSessionToken("other"), now); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, _, err := repo.GetSessionByTokenHash(ctx, hash, now.Add(2*time.Hour)); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	must(t, repo.RevokeSession(ctx, s.ID))
	must(t, repo.RevokeSession(ctx, s.ID))
	if _, _, err := repo.GetSessionByTokenHash(ctx, hash, now); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}
	if err := repo.CreateSession(ctx, &domain.Session{TokenHash: hash, UserID: user.ID, CSRFToken: "x", ExpiresAt: now}); err == nil {
		t.Fatal("token hashes are unique")
	}
}

func TestRealDBSessionsStoreOnlyTheHash(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	handler := accountHandler(repo)
	rec := serve(handler, "POST", "/api/v1/auth/guest", "", nil)
	cookie := sessionCookie(t, rec)
	var stored []byte
	if err := pool.QueryRowContext(ctx, `SELECT token_hash FROM sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(cookie.Value)) || len(stored) != 32 {
		t.Fatal("only a SHA-256 of the token may be stored")
	}
}

func TestRealDBUpgradeMakesExistingUsersGuestsExceptTheDevIdentity(t *testing.T) {
	pool, _, ctx := realDatabaseWithMigrations(t, false)
	entries, _ := os.ReadDir(migrationsDir)
	var ups []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			ups = append(ups, e.Name())
		}
	}
	sort.Strings(ups)
	last := ups[len(ups)-1]
	if !strings.HasPrefix(last, "000011_") {
		t.Skipf("later migrations exist; this test pins the 000011 upgrade, last is %s", last)
	}
	apply := func(name string) {
		content, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.ExecContext(ctx, string(content)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	for _, up := range ups[:len(ups)-1] {
		apply(up)
	}
	legacy := uuid.NewString()
	if _, err := pool.ExecContext(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, 'legacy@example.com', 'x'), ($2, 'dev@icomic.local', 'y')`, legacy, devUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, `INSERT INTO projects (id, user_id, title, original_script) VALUES ($1, $2, 't', 's')`, uuid.NewString(), legacy); err != nil {
		t.Fatal(err)
	}
	apply(last)
	kind := func(id string) string {
		var k string
		if err := pool.QueryRowContext(ctx, `SELECT account_kind FROM users WHERE id = $1`, id).Scan(&k); err != nil {
			t.Fatal(err)
		}
		return k
	}
	if kind(legacy) != "guest" || kind(devUserID) != "registered" {
		t.Fatalf("legacy=%s dev=%s", kind(legacy), kind(devUserID))
	}
	var projects int
	_ = pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE user_id = $1`, legacy).Scan(&projects)
	if projects != 1 {
		t.Fatal("existing projects are untouched by the upgrade")
	}
	var guestMax, registeredMax int
	_ = pool.QueryRowContext(ctx, `SELECT guest_max_projects, registered_max_projects FROM account_policy`).Scan(&guestMax, &registeredMax)
	if guestMax != 1 || registeredMax != 100 {
		t.Fatalf("policy defaults %d/%d", guestMax, registeredMax)
	}
}

func accountHandler(repo *db.PostgresRepository) http.Handler {
	s := api.NewServer(repo, credits.NewInMemoryCreditService(nil), jobs.NewInMemoryJobService(), gen.NewFakeAIServiceClient(),
		auth.NewDevAuthenticator(false, ""), auth.NewDefaultImageHosting("/media"), 5)
	s.Accounts.Hasher = auth.NewPasswordHasher(auth.PasswordParams{MemoryKiB: 8, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 16}, 4)
	return s.NewRouter()
}

func serve(h http.Handler, method, path, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "localhost:8080"
	req.RemoteAddr = "198.51.100.7:1"
	req.Header.Set("Origin", "http://localhost:5173")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("no session cookie: %d %s", rec.Code, rec.Body)
	return nil
}

func csrfOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Account struct {
			CSRF string `json:"csrf_token"`
		} `json:"account"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Account.CSRF
}

func TestRealDBGuestHTTPConcurrentProjectCreation(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	warmPool(t, pool, ctx)
	h := accountHandler(repo)
	rec := serve(h, "POST", "/api/v1/auth/guest", "", nil)
	cookie, csrf := sessionCookie(t, rec), csrfOf(t, rec)
	body, _ := json.Marshal(map[string]any{"title": "T", "original_script": "A brave fox crosses a quiet river at dawn and finds a lantern."})
	var created, refused atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := serve(h, "POST", "/api/v1/projects", string(body), func(req *http.Request) {
				req.AddCookie(cookie)
				req.Header.Set("X-CSRF-Token", csrf)
			})
			switch r.Code {
			case http.StatusCreated:
				created.Add(1)
			case http.StatusForbidden:
				if strings.Contains(r.Body.String(), "project_limit_reached") {
					refused.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if created.Load() != 1 || refused.Load() != 24 {
		t.Fatalf("created=%d refused=%d", created.Load(), refused.Load())
	}
}

func TestRealDBSignUpSignInFlowOnPostgres(t *testing.T) {
	_, repo, ctx := realDatabase(t)
	h := accountHandler(repo)
	creds := `{"email":"Flow@Example.com","password":"a long enough password"}`

	guest := serve(h, "POST", "/api/v1/auth/guest", "", nil)
	cookie, csrf := sessionCookie(t, guest), csrfOf(t, guest)
	create := serve(h, "POST", "/api/v1/projects", `{"title":"Mine","original_script":"A brave fox crosses a quiet river at dawn and finds a lantern."}`, func(r *http.Request) {
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("guest project: %d %s", create.Code, create.Body)
	}
	signup := serve(h, "POST", "/api/v1/auth/signup", creds, func(r *http.Request) { r.AddCookie(cookie) })
	if signup.Code != http.StatusCreated || !strings.Contains(signup.Body.String(), `"project_count":1`) {
		t.Fatalf("signup: %d %s", signup.Code, signup.Body)
	}
	user, err := repo.GetUserByEmail(ctx, "flow@example.com")
	if err != nil || user.AccountKind != domain.AccountRegistered || !strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		t.Fatalf("stored account: %+v err=%v", user, err)
	}
	// Sign in on a fresh browser.
	signin := serve(h, "POST", "/api/v1/auth/signin", creds, nil)
	if signin.Code != http.StatusOK {
		t.Fatalf("signin: %d %s", signin.Code, signin.Body)
	}
	c2, t2 := sessionCookie(t, signin), csrfOf(t, signin)
	for i := 0; i < 3; i++ {
		r := serve(h, "POST", "/api/v1/projects", `{"title":"More","original_script":"A brave fox crosses a quiet river at dawn and finds a lantern."}`, func(r *http.Request) {
			r.AddCookie(c2)
			r.Header.Set("X-CSRF-Token", t2)
		})
		if r.Code != http.StatusCreated {
			t.Fatalf("registered project %d: %d %s", i, r.Code, r.Body)
		}
	}
	out := serve(h, "POST", "/api/v1/auth/signout", "", func(r *http.Request) { r.AddCookie(c2); r.Header.Set("X-CSRF-Token", t2) })
	if out.Code != http.StatusNoContent {
		t.Fatalf("signout: %d", out.Code)
	}
	if r := serve(h, "GET", "/api/v1/projects", "", func(r *http.Request) { r.AddCookie(c2) }); r.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session on PostgreSQL: %d", r.Code)
	}
}

var _ = sql.ErrNoRows

func TestRealDBSeededDevIdentityIsRegistered(t *testing.T) {
	pool, repo, ctx := realDatabase(t)
	seeds, err := os.ReadFile(filepath.Join(migrationsDir, "..", "seeds.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, string(seeds)); err != nil {
		t.Fatalf("apply seeds after migrations: %v", err)
	}
	dev, err := repo.GetUserByID(ctx, devUserID)
	if err != nil || dev.AccountKind != domain.AccountRegistered {
		t.Fatalf("seeded dev user: %+v err=%v", dev, err)
	}
}
