package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

const (
	mediaOwner   = "media-owner"
	mediaOther   = "media-other"
	mediaCharID  = "0d7a1d6e-0f1e-4c35-9c5f-6a1d5b0f8c11"
	mediaKey     = "characters/" + mediaCharID + "/abc123/reference.png"
	mediaPNG     = "\x89PNG\r\n\x1a\nowner-pixels"
	mediaProject = "6b0c8d7e-5d3e-4a39-8e55-0d6f1d0c9a21"
)

type mediaFixture struct {
	handler http.Handler
	server  *api.Server
	repo    db.Repository
	storage *httptest.Server
	hits    *int
}

// newMediaFixture serves one character reference owned by mediaOwner from a fake storage upstream.
func newMediaFixture(t *testing.T, upstream http.HandlerFunc) *mediaFixture {
	t.Helper()
	hits := 0
	if upstream == nil {
		upstream = func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/storage/fake/"+mediaKey {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(mediaPNG))
		}
	}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		upstream(w, r)
	}))
	t.Cleanup(storage.Close)

	repo := db.NewInMemoryRepository()
	server := api.NewServer(repo, credits.NewInMemoryCreditService(map[string]float64{mediaOwner: 10, mediaOther: 10}), jobs.NewInMemoryJobService(),
		gen.NewAIServiceClient(storage.URL, 5*time.Second), auth.NewDevAuthenticator(true, ""), auth.NewDefaultImageHosting("/media"), 2)
	ctx := context.Background()
	if err := repo.CreateProject(ctx, &domain.Project{ID: mediaProject, UserID: mediaOwner, Title: "Media"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateCharacter(ctx, &domain.Character{ID: mediaCharID, ProjectID: mediaProject, Name: "Hero",
		ReferenceImageURL: "http://ai:8000/storage/fake/" + mediaKey}); err != nil {
		t.Fatal(err)
	}
	return &mediaFixture{handler: server.NewRouter(), server: server, repo: repo, storage: storage, hits: &hits}
}

func (f *mediaFixture) get(target, asUser string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if asUser != "" {
		req.Header.Set("X-User-ID", asUser)
	}
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// ownerLink reads the character list as the owner and returns the media link the API handed out.
func (f *mediaFixture) ownerLink(t *testing.T) *url.URL {
	t.Helper()
	rec := f.get("/api/v1/projects/"+mediaProject+"/characters", mediaOwner)
	if rec.Code != http.StatusOK {
		t.Fatalf("list characters = %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Characters []struct {
			ReferenceImageURL string `json:"reference_image_url"`
		} `json:"characters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Characters) != 1 {
		t.Fatalf("decode characters: %v %s", err, rec.Body.String())
	}
	link, err := url.Parse(body.Characters[0].ReferenceImageURL)
	if err != nil {
		t.Fatal(err)
	}
	return link
}

func TestMediaAnonymousAndOtherUsersAreDenied(t *testing.T) {
	f := newMediaFixture(t, nil)
	for name, tc := range map[string]struct {
		user string
		want int
	}{
		"anonymous":  {"", http.StatusUnauthorized},
		"other user": {mediaOther, http.StatusNotFound},
	} {
		req := httptest.NewRequest(http.MethodGet, "/media/"+mediaKey, nil)
		if tc.user != "" {
			req.Header.Set("X-User-ID", tc.user)
		}
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		if rec.Code != tc.want || strings.Contains(rec.Body.String(), "owner-pixels") {
			t.Errorf("%s: GET /media = %d %q, want %d and no bytes", name, rec.Code, rec.Body.String(), tc.want)
		}
	}
	if *f.hits != 0 {
		t.Fatalf("denied requests reached storage %d times", *f.hits)
	}
}

func TestMediaOwnerServesWithSafeHeaders(t *testing.T) {
	f := newMediaFixture(t, nil)
	rec := f.get("/media/"+mediaKey, mediaOwner, func(r *http.Request) { r.Header.Set("Origin", "http://localhost:5173") })
	if rec.Code != http.StatusOK || rec.Body.String() != mediaPNG {
		t.Fatalf("owner GET = %d %q", rec.Code, rec.Body.String())
	}
	want := map[string]string{
		"Content-Type":                "image/png",
		"X-Content-Type-Options":      "nosniff",
		"Cache-Control":               "private, max-age=300",
		"Access-Control-Allow-Origin": "http://localhost:5173",
		"Content-Length":              strconv.Itoa(len(mediaPNG)),
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want sandbox", csp)
	}
}

func TestMediaLinksInResponsesAreScopedAndReplaceInternalURLs(t *testing.T) {
	f := newMediaFixture(t, nil)
	link := f.ownerLink(t)
	if link.Host != "example.com" || link.Path != "/media/"+mediaKey {
		t.Fatalf("link = %s, want an example.com /media link, not the internal storage host", link)
	}
	q := link.Query()
	if q.Get("u") != mediaOwner || q.Get("sig") == "" || q.Get("exp") == "" {
		t.Fatalf("link %s lacks user, expiry or signature", link)
	}
	exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
	if remaining := time.Until(time.Unix(exp, 0)); remaining <= 0 || remaining > 30*time.Minute {
		t.Fatalf("link expires in %s, want a short window within 30 minutes", remaining)
	}
	if again := f.ownerLink(t); again.String() != link.String() {
		t.Fatalf("repeated reads must give the same cacheable link, got %s then %s", link, again)
	}

	// The link alone is the credential: no cookie, header or identity.
	rec := f.get(link.RequestURI(), "")
	if rec.Code != http.StatusOK || rec.Body.String() != mediaPNG {
		t.Fatalf("signed link GET = %d %q", rec.Code, rec.Body.String())
	}
}

func TestMediaLinkTamperingExpiryAndCrossUserUseAreRejected(t *testing.T) {
	f := newMediaFixture(t, nil)
	link := f.ownerLink(t)
	q := link.Query()
	tamper := func(mutate func(url.Values)) string {
		v := url.Values{}
		for k, vals := range q {
			v[k] = vals
		}
		mutate(v)
		return "/media/" + mediaKey + "?" + v.Encode()
	}
	for name, target := range map[string]string{
		"bad signature": tamper(func(v url.Values) { v.Set("sig", "AAAA"+v.Get("sig")[4:]) }),
		"other user":    tamper(func(v url.Values) { v.Set("u", mediaOther) }),
		"expired":       tamper(func(v url.Values) { v.Set("exp", strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)) }),
		"far future":    tamper(func(v url.Values) { v.Set("exp", strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10)) }),
		"other key":     "/media/characters/" + mediaCharID + "/other/reference.png?" + q.Encode(),
	} {
		if rec := f.get(target, ""); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "owner-pixels") {
			t.Errorf("%s: GET = %d %q, want 401", name, rec.Code, rec.Body.String())
		}
	}
}

// A genuine link minted for another user still never opens a key that user does not own, even when that
// user's own data names the owner's storage key.
func TestMediaGenuineLinkForNonOwnerDoesNotOpenKey(t *testing.T) {
	f := newMediaFixture(t, nil)
	ctx := context.Background()
	const otherProject = "7c1d9e8f-6e4f-4b4a-9f66-1e7a2e1d0b32"
	if err := f.repo.CreateProject(ctx, &domain.Project{ID: otherProject, UserID: mediaOther, Title: "Other"}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CreateCharacter(ctx, &domain.Character{ID: "1a2b3c4d-0000-4000-8000-000000000001", ProjectID: otherProject, Name: "Thief",
		ReferenceImageURL: "http://ai:8000/storage/fake/" + mediaKey}); err != nil {
		t.Fatal(err)
	}
	rec := f.get("/api/v1/projects/"+otherProject+"/characters", mediaOther)
	var body struct {
		Characters []struct {
			ReferenceImageURL string `json:"reference_image_url"`
		} `json:"characters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Characters) != 1 {
		t.Fatalf("decode: %v %s", err, rec.Body.String())
	}
	link, _ := url.Parse(body.Characters[0].ReferenceImageURL)
	if link.Query().Get("u") != mediaOther {
		t.Fatalf("link %s is not scoped to the requesting user", link)
	}
	if got := f.get(link.RequestURI(), ""); got.Code != http.StatusNotFound || strings.Contains(got.Body.String(), "owner-pixels") {
		t.Fatalf("non-owner link GET = %d %q, want 404", got.Code, got.Body.String())
	}
}

func TestMediaServesOnlyKeysOfKnownOwnedResources(t *testing.T) {
	f := newMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(mediaPNG))
	})
	for _, key := range []string{
		"secrets/anything.png",                       // not a generated key shape
		"characters/not-a-character/x/reference.png", // unknown resource
		"exports/" + mediaProject + "/../other/comic.pdf",
	} {
		if rec := f.get("/media/"+key, mediaOwner); rec.Code == http.StatusOK {
			t.Errorf("GET /media/%s = 200, want denied", key)
		}
	}
	if rec := f.get("/media/exports/"+mediaProject+"/run/comic.pdf", mediaOther); rec.Code != http.StatusNotFound {
		t.Errorf("other user's export = %d, want 404", rec.Code)
	}
	if rec := f.get("/media/exports/"+mediaProject+"/run/comic.pdf", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous export = %d, want 401", rec.Code)
	}
}

func TestMediaExportPDFIsOwnerOnlyAttachment(t *testing.T) {
	key := "exports/" + mediaProject + "/run1/comic.pdf"
	f := newMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/storage/fake/"+key {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4\n"))
	})
	rec := f.get("/media/"+key, mediaOwner)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || rec.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("owner PDF = %d %v", rec.Code, rec.Header())
	}
	for user, want := range map[string]int{"": http.StatusUnauthorized, mediaOther: http.StatusNotFound} {
		if rec := f.get("/media/"+key, user); rec.Code != want || strings.Contains(rec.Body.String(), "%PDF") {
			t.Errorf("user %q PDF = %d, want %d", user, rec.Code, want)
		}
	}
}

func TestMediaUpstreamIsBoundedAndNeverFollowsRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("redirect-target-secret"))
	}))
	defer target.Close()

	for name, upstream := range map[string]http.HandlerFunc{
		"redirect": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) },
		"oversized": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(65<<20))
			w.Header().Set("Content-Type", "image/png")
		},
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
	} {
		f := newMediaFixture(t, upstream)
		rec := f.get("/media/"+mediaKey, mediaOwner)
		if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "redirect-target-secret") {
			t.Errorf("%s: GET = %d %q, want 502 without upstream bytes", name, rec.Code, rec.Body.String())
		}
	}
}

func TestMediaUnsafeUpstreamContentTypeIsNotEchoed(t *testing.T) {
	f := newMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Set-Cookie", "evil=1")
		_, _ = w.Write([]byte("<script>alert(1)</script>"))
	})
	rec := f.get("/media/"+mediaKey, mediaOwner)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("typed by extension: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("upstream headers must not be copied, got Set-Cookie %q", rec.Header().Get("Set-Cookie"))
	}
}

func TestMediaLocalDirectoryIsOwnerChecked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEDIA_DIR", dir)
	local := filepath.Join(dir, filepath.FromSlash(mediaKey))
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(mediaPNG), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newMediaFixture(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if rec := f.get("/media/"+mediaKey, mediaOwner); rec.Code != http.StatusOK || rec.Body.String() != mediaPNG || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("owner local file = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec := f.get("/media/"+mediaKey, mediaOther); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "owner-pixels") {
		t.Fatalf("other user local file = %d", rec.Code)
	}
}

func TestExportDownloadRefusesRedirectingStorage(t *testing.T) {
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-redirect-target"))
	}))
	defer secret.Close()
	f := newMediaFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, secret.URL, http.StatusFound) })
	if err := f.repo.CreateExport(context.Background(), &domain.Export{ID: "9f0e6b8e-2c8a-4a39-9e10-3a6a1b2c3d4e", ProjectID: mediaProject,
		FileURL: "http://ai:8000/storage/fake/exports/" + mediaProject + "/run1/comic.pdf", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	rec := f.get("/api/v1/exports/9f0e6b8e-2c8a-4a39-9e10-3a6a1b2c3d4e/download", mediaOwner)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "%PDF") {
		t.Fatalf("download via redirecting storage = %d %q, want 502 without bytes", rec.Code, rec.Body.String())
	}
}
