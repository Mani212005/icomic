package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/db"
)

// Media links (SPEC 10 and 11, ARCHITECTURE A2): stored artifacts are never public. Every JSON response
// that carries an internal storage URL is rewritten to a short-lived link scoped to the caller and one
// storage key. GET /media accepts that link or an authenticated owner, and in both cases re-checks that
// the key belongs to the caller, so a leaked or guessed link never opens another user's artifact.
const (
	mediaLinkTTL    = 30 * time.Minute
	mediaLinkBucket = 5 * time.Minute // expiries are bucketed so repeated reads yield identical, cacheable links
	maxMediaBytes   = 64 << 20
	mediaFetchLimit = 60 * time.Second
	mediaCacheAge   = 300
)

// mediaSigner mints and verifies the HMAC-signed query of a media link.
type mediaSigner struct {
	secret []byte
	now    func() time.Time
}

// newMediaSigner uses the configured secret, or a random per-process one so links never outlive a restart.
func newMediaSigner(secret string) *mediaSigner {
	key := []byte(secret)
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic(fmt.Sprintf("media signing secret: %v", err))
		}
	}
	return &mediaSigner{secret: key, now: time.Now}
}

func (m *mediaSigner) mac(userID, key string, exp int64) string {
	h := hmac.New(sha256.New, m.secret)
	fmt.Fprintf(h, "icomic-media-v1\x00%s\x00%s\x00%d", userID, key, exp)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (m *mediaSigner) expiry() int64 {
	bucket := int64(mediaLinkBucket / time.Second)
	return m.now().Unix()/bucket*bucket + int64(mediaLinkTTL/time.Second)
}

// query returns the signed query string for key, scoped to userID.
func (m *mediaSigner) query(userID, key string) string {
	exp := m.expiry()
	v := url.Values{}
	v.Set("exp", strconv.FormatInt(exp, 10))
	v.Set("u", userID)
	v.Set("sig", m.mac(userID, key, exp))
	return v.Encode()
}

// verify returns the user the link was minted for when the signature is genuine and still live.
func (m *mediaSigner) verify(q url.Values, key string) (string, bool) {
	userID := q.Get("u")
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil || userID == "" {
		return "", false
	}
	now := m.now().Unix()
	if exp < now || exp > now+int64(mediaLinkTTL/time.Second) {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("sig")), []byte(m.mac(userID, key, exp))) != 1 {
		return "", false
	}
	return userID, true
}

// internalMediaValue matches a whole JSON string value that is an internal storage URL or a /media link.
var internalMediaValue = regexp.MustCompile(`"(?:https?://[^"\\/]+)?/(?:storage/fake|media)/([^"\\?#]+)"`)

// publicBase is the origin browsers use to reach this API.
func (s *Server) publicBase(r *http.Request) string {
	if s.PublicBaseURL != "" {
		return strings.TrimRight(s.PublicBaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// mediaLink maps one storage key to a browser-valid, short-lived link for userID.
func (s *Server) mediaLink(r *http.Request, userID, key string) string {
	u := url.URL{Path: "/media/" + key}
	return s.publicBase(r) + u.EscapedPath() + "?" + s.mediaSigner.query(userID, key)
}

// rewriteMediaLinks replaces every internal storage URL in a JSON body with a scoped link.
func (s *Server) rewriteMediaLinks(r *http.Request, userID string, body []byte) []byte {
	return internalMediaValue.ReplaceAllFunc(body, func(match []byte) []byte {
		sub := internalMediaValue.FindSubmatch(match)
		key, err := DeriveStorageKey(string(sub[1]))
		if err != nil {
			return match
		}
		return []byte(`"` + s.mediaLink(r, userID, key) + `"`)
	})
}

// mediaLinkWriter buffers JSON responses so storage URLs can be rewritten; other bodies pass through.
type mediaLinkWriter struct {
	http.ResponseWriter
	server  *Server
	request *http.Request
	userID  string
	decided bool
	json    bool
	status  int
	buf     bytes.Buffer
}

func (w *mediaLinkWriter) WriteHeader(code int) {
	if w.decided {
		return
	}
	w.decided = true
	w.status = code
	w.json = strings.HasPrefix(w.Header().Get("Content-Type"), "application/json")
	if !w.json {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *mediaLinkWriter) Write(b []byte) (int, error) {
	if !w.decided {
		w.WriteHeader(http.StatusOK)
	}
	if w.json {
		return w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *mediaLinkWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *mediaLinkWriter) finish() {
	if !w.decided || !w.json {
		return
	}
	body := w.server.rewriteMediaLinks(w.request, w.userID, w.buf.Bytes())
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(body)
}

// mediaKeyOwner resolves the user that owns a storage key from the resource id embedded in it.
// Keys the service does not generate for a known resource have no owner and are never served.
func (s *Server) mediaKeyOwner(ctx context.Context, key string) (string, error) {
	parts := strings.Split(key, "/")
	if len(parts) < 2 {
		return "", db.ErrNotFound
	}
	id := strings.TrimSuffix(parts[1], path.Ext(parts[1]))
	projectOwner := func(projectID string) (string, error) {
		proj, err := s.Repo.GetProject(ctx, projectID)
		if err != nil {
			return "", err
		}
		return proj.UserID, nil
	}
	pageOwner := func(pageID string) (string, error) {
		page, err := s.Repo.GetPage(ctx, pageID)
		if err != nil {
			return "", err
		}
		chapter, err := s.Repo.GetChapter(ctx, page.ChapterID)
		if err != nil {
			return "", err
		}
		return projectOwner(chapter.ProjectID)
	}
	switch parts[0] {
	case "characters":
		ch, err := s.Repo.GetCharacter(ctx, id)
		if err != nil {
			return "", err
		}
		return projectOwner(ch.ProjectID)
	case "pages":
		return pageOwner(id)
	case "finishes":
		// Finishes are keyed by page id, or by project id and page number when the page has no id.
		owner, err := pageOwner(id)
		if errors.Is(err, db.ErrNotFound) {
			return projectOwner(id)
		}
		return owner, err
	case "exports":
		return projectOwner(id)
	}
	return "", db.ErrNotFound
}

// mediaClient is the only client used to read stored artifacts upstream: bounded in time, never following
// redirects, so a compromised or misconfigured upstream cannot steer a request to another host.
var mediaClient = &http.Client{
	Timeout: mediaFetchLimit,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var errMediaUpstream = errors.New("media upstream unavailable")

// fetchStored GETs target and returns the response only when it is a bounded 200. A missing object yields
// db.ErrNotFound; redirects, other statuses, unknown or oversized lengths yield errMediaUpstream.
func fetchStored(ctx context.Context, client *http.Client, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errMediaUpstream
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errMediaUpstream
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, db.ErrNotFound
	case resp.StatusCode != http.StatusOK, resp.ContentLength < 0, resp.ContentLength > maxMediaBytes:
		resp.Body.Close()
		return nil, errMediaUpstream
	}
	return resp, nil
}

// mediaUpstreams lists the only places a key may be read from: the configured image host and the AI
// service's storage. Both come from deployment configuration, never from the request.
func (s *Server) mediaUpstreams(key string) []string {
	var targets []string
	if resolved := s.ImageHosting.ResolveURL(key); strings.HasPrefix(resolved, "http://") || strings.HasPrefix(resolved, "https://") {
		targets = append(targets, resolved)
	}
	if c, ok := s.AIClient.(*gen.AIServiceClient); ok && c.BaseURL != "" {
		u := url.URL{Path: "/storage/fake/" + key}
		targets = append(targets, strings.TrimRight(c.BaseURL, "/")+u.EscapedPath())
	}
	return targets
}

// safeMediaTypes are the only content types echoed to browsers; anything else is served as opaque bytes.
var safeMediaTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true, "application/pdf": true,
}

func mediaContentType(key, upstream string) string {
	if t, _, err := mime.ParseMediaType(upstream); err == nil && safeMediaTypes[t] {
		return t
	}
	if t, _, err := mime.ParseMediaType(mime.TypeByExtension(path.Ext(key))); err == nil && safeMediaTypes[t] {
		return t
	}
	return "application/octet-stream"
}

func setMediaHeaders(w http.ResponseWriter, contentType string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, max-age="+strconv.Itoa(mediaCacheAge))
	if !strings.HasPrefix(contentType, "image/") {
		h.Set("Content-Disposition", "attachment")
	}
}

// ServeMedia handles GET /media/{path...}: owner-authorized serving of stored artifacts (SPEC 10, 11).
func (s *Server) ServeMedia(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/media")
	subPath = strings.TrimPrefix(subPath, "/")
	if subPath == "" {
		writeError(w, http.StatusBadRequest, "Media path required")
		return
	}
	key, err := DeriveStorageKey(subPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid media path")
		return
	}

	var userID string
	if r.URL.Query().Get("sig") != "" {
		var ok bool
		if userID, ok = s.mediaSigner.verify(r.URL.Query(), key); !ok {
			writeError(w, http.StatusUnauthorized, "media link expired or invalid")
			return
		}
	} else {
		identity, err := auth.IdentityOf(s.Authenticator, r)
		if err != nil || identity.UserID == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized: authentication required")
			return
		}
		userID = identity.UserID
	}
	owner, err := s.mediaKeyOwner(r.Context(), key)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "Failed to authorize media")
		return
	}
	if err != nil || owner != userID {
		writeError(w, http.StatusNotFound, "Media not found")
		return
	}

	mediaDir := os.Getenv("MEDIA_DIR")
	if mediaDir == "" {
		mediaDir = "./media"
	}
	localPath := filepath.Join(mediaDir, filepath.Clean("/"+key))
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		setMediaHeaders(w, mediaContentType(key, ""))
		http.ServeFile(w, r, localPath)
		return
	}

	for _, target := range s.mediaUpstreams(key) {
		resp, err := fetchStored(r.Context(), mediaClient, target)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "Failed to retrieve media from storage")
			return
		}
		defer resp.Body.Close()
		setMediaHeaders(w, mediaContentType(key, resp.Header.Get("Content-Type")))
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, maxMediaBytes))
		return
	}
	writeError(w, http.StatusNotFound, "Media not found")
}
