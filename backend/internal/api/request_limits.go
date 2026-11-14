package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// RequestLimits are process-local resource protections, independent of image quotas.
// Defaults are engineering safeguards; operators must choose launch capacity values.
type RequestLimits struct {
	JSONBytes       int64
	ScriptBytes     int
	ScriptWords     int
	MaxChapters     int
	TextBytes       int
	ImageBytes      int64
	ImagePixels     int64
	RequestsPerIP   int
	RequestsPerUser int
	AdmissionKeys   int
}

func DefaultRequestLimits() RequestLimits {
	return RequestLimits{JSONBytes: 1 << 20, ScriptBytes: 512 << 10, ScriptWords: 100000, MaxChapters: 1000, TextBytes: 16384, ImageBytes: 10 << 20, ImagePixels: 40000000, RequestsPerIP: 600, RequestsPerUser: 300, AdmissionKeys: 10000}
}

// RequestLimitsFromEnv fails closed on invalid configuration instead of disabling bounds.
func RequestLimitsFromEnv(getenv func(string) string) (RequestLimits, error) {
	l := DefaultRequestLimits()
	for _, item := range []struct {
		name   string
		target *int
	}{
		{"MAX_CHAPTERS", &l.MaxChapters}, {"MAX_SCRIPT_BYTES", &l.ScriptBytes}, {"MAX_SCRIPT_WORDS", &l.ScriptWords}, {"MAX_TEXT_FIELD_BYTES", &l.TextBytes},
		{"REQUESTS_PER_IP_PER_MINUTE", &l.RequestsPerIP}, {"REQUESTS_PER_USER_PER_MINUTE", &l.RequestsPerUser}, {"REQUEST_ADMISSION_KEYS", &l.AdmissionKeys},
	} {
		if raw := getenv(item.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return l, fmt.Errorf("%s must be a positive integer", item.name)
			}
			*item.target = n
		}
	}
	for _, item := range []struct {
		name   string
		target *int64
	}{
		{"MAX_JSON_BYTES", &l.JSONBytes}, {"MAX_COLOUR_IMAGE_BYTES", &l.ImageBytes}, {"MAX_COLOUR_IMAGE_PIXELS", &l.ImagePixels},
	} {
		if raw := getenv(item.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n <= 0 || n == math.MaxInt64 {
				return l, fmt.Errorf("%s must be a positive bounded integer", item.name)
			}
			*item.target = n
		}
	}
	return l, nil
}

// Fixed one-minute windows cap admission state as well as request volume. Entries
// expire without background goroutines. X-Forwarded-For is deliberately untrusted.
type admissionEntry struct {
	start time.Time
	count int
}
type requestAdmission struct {
	mu      sync.Mutex
	entries map[string]admissionEntry
}

func (a *requestAdmission) allow(key string, limit, maxKeys int, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.entries == nil {
		a.entries = make(map[string]admissionEntry)
	}
	e, exists := a.entries[key]
	if exists && now.Sub(e.start) < time.Minute {
		if e.count >= limit {
			return false
		}
		e.count++
		a.entries[key] = e
		return true
	}
	if !exists && len(a.entries) >= maxKeys {
		for k, v := range a.entries {
			if now.Sub(v.start) >= time.Minute {
				delete(a.entries, k)
			}
		}
		if len(a.entries) >= maxKeys {
			return false
		}
	}
	a.entries[key] = admissionEntry{now, 1}
	return true
}

func (s *Server) admit(w http.ResponseWriter, key string, limit int) bool {
	if s.admission.allow(key, limit, s.Limits.AdmissionKeys, time.Now()) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusTooManyRequests, "Request limit reached; retry later")
	return false
}
func (s *Server) ipAdmission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}
			if !s.admit(w, "ip:"+ip, s.Limits.RequestsPerIP) {
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Validate before handlers load entities, mutate state, moderate or dispatch.
// Restoring bounded bytes preserves existing handler request types and defaults.
func (s *Server) validatePublicRequest(w http.ResponseWriter, r *http.Request, pattern string) bool {
	if id := r.PathValue("id"); id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || (len(id) != 36 && !(strings.Contains(pattern, "/generation-jobs/") && len(id) == 32)) {
			writeError(w, 400, "Invalid resource ID: expected a UUID")
			return false
		}
	}
	if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/colour-image") {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.Limits.JSONBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, 413, "Request exceeds JSON byte limit")
		} else {
			writeError(w, 400, "Could not read request body")
		}
		return false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if pattern == "POST /api/v1/projects/{id}/estimate" {
			writeError(w, 400, "stage and image_count are required")
			return false
		}
		return true
	}
	if !utf8.Valid(data) || bytes.TrimSpace(data)[0] != '{' {
		writeError(w, 400, "Request payload must be a JSON object")
		return false
	}
	fields := make(map[string]json.RawMessage)
	dec := json.NewDecoder(bytes.NewReader(data))
	_, err = dec.Token() // opening object, verified above
	for err == nil && dec.More() {
		var token json.Token
		token, err = dec.Token()
		if err != nil {
			break
		}
		name, ok := token.(string)
		if !ok {
			err = errors.New("invalid object key")
			break
		}
		if _, duplicate := fields[name]; duplicate {
			err = errors.New("duplicate field")
			break
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			break
		}
		fields[name] = value
	}
	if err == nil {
		_, err = dec.Token()
	}
	if err != nil {
		writeError(w, 400, "Invalid JSON payload or duplicate field")
		return false
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		writeError(w, 400, "Request must contain exactly one JSON object")
		return false
	}

	if pattern == "POST /api/v1/projects/{id}/estimate" {
		if fields["stage"] == nil || fields["image_count"] == nil {
			writeError(w, 400, "stage and image_count are required")
			return false
		}
	}
	if pattern == "PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/override" && fields["override"] == nil {
		writeError(w, 400, "override is required; use an empty object to clear")
		return false
	}
	if pattern == "PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/text" && fields["text"] == nil {
		writeError(w, 400, "text is required")
		return false
	}
	allowed := s.publicFields(pattern)
	for name, value := range fields {
		kind, ok := allowed[name]
		if !ok {
			writeError(w, 400, "Unknown request field")
			return false
		}
		if err := s.validateField(name, kind, value); err != nil {
			writeError(w, 400, err.Error())
			return false
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return true
}

// The public API accepts only these fields. Fields whose behavior belongs to
// another workflow (panel editing/export options) are validated here too.
func (s *Server) publicFields(pattern string) map[string]string {
	switch pattern {
	case "POST /api/v1/projects":
		return map[string]string{"title": "title", "original_script": "script", "art_style": "artStyle", "chapter_count": "chapters"}
	case "PATCH /api/v1/projects/{id}":
		return map[string]string{"title": "title", "original_script": "script", "art_style": "artStyle", "chapter_count": "positiveChapters", "status": "projectStatus", "finish_preset": "finishPreset"}
	case "PATCH /api/v1/characters/{id}":
		return map[string]string{"name": "title", "description": "text", "visual_attributes": "object"}
	case "POST /api/v1/characters/{id}/reference-image":
		return map[string]string{"prompt": "text", "style": "text"}
	case "POST /api/v1/projects/{id}/analyze":
		return map[string]string{"script": "script"}
	case "POST /api/v1/projects/{id}/chapters":
		return map[string]string{"chapter_count": "positiveChapters", "art_style": "artStyle"}
	case "PATCH /api/v1/chapters/{id}":
		return map[string]string{"title": "title", "summary": "text", "important_events": "textList", "character_names": "textList", "ending": "text", "status": "chapterStatus"}
	case "PATCH /api/v1/panels/{id}":
		return map[string]string{"scene_description": "text", "dialogue": "text", "narration": "text", "character_actions": "text", "camera_angle": "text", "background": "text", "image_prompt": "text"}
	case "PATCH /api/v1/pages/{id}":
		return map[string]string{"panel_ids": "ids"}
	case "POST /api/v1/chapters/{id}/panels/add":
		return map[string]string{"page_id": "id", "position": "index", "scene_description": "text", "dialogue": "text", "narration": "text", "character_actions": "text", "camera_angle": "text", "background": "text", "image_prompt": "text"}
	case "POST /api/v1/chapters/{id}/panels/move":
		return map[string]string{"panel_id": "id", "page_id": "id", "position": "index"}
	case "POST /api/v1/pages/{id}/line-art":
		return map[string]string{"variant": "text", "route": "text"}
	case "POST /api/v1/pages/{id}/painted-page":
		return map[string]string{"variant": "text"}
	case "PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/override":
		return map[string]string{"override": "object"}
	case "PATCH /api/v1/pages/{id}/lettering/utterances/{utterance_id}/text":
		return map[string]string{"text": "text"}
	case "PATCH /api/v1/pages/{id}/lettering/zones":
		return map[string]string{"panel_id": "text", "reserved_zones": "zones"}
	case "PATCH /api/v1/pages/{id}/colour-palette":
		return map[string]string{"palette": "object"}
	case "POST /api/v1/projects/{id}/estimate":
		return map[string]string{"stage": "stage", "image_count": "positive"}
	case "POST /api/v1/projects/{id}/export":
		return map[string]string{"format": "format", "page_size": "pageSize"}
	case "POST /api/v1/prompts/moderate":
		return map[string]string{"prompt": "text"}
	default:
		return nil
	}
}

// maxOutlineListItems bounds a chapter's event and character lists, which the editor sends whole.
const maxOutlineListItems = 100

func (s *Server) validateField(name, kind string, raw json.RawMessage) error {
	invalid := func() error { return fmt.Errorf("Invalid %s", name) }
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return invalid()
	}
	switch kind {
	case "positive", "chapters", "positiveChapters":
		var n int
		if json.Unmarshal(raw, &n) != nil || n < 0 || ((kind == "chapters" || kind == "positiveChapters") && n > s.Limits.MaxChapters) || ((kind == "positive" || kind == "positiveChapters") && n == 0) {
			return invalid()
		}
	case "object":
		var v map[string]any
		if json.Unmarshal(raw, &v) != nil {
			return invalid()
		}
		if name == "override" {
			for key, value := range v {
				switch key {
				case "center", "tail_anchor", "rect":
					point, ok := value.(map[string]any)
					if !ok {
						return invalid()
					}
					required := []string{"x", "y"}
					if key == "rect" {
						required = append(required, "width", "height")
					}
					for _, field := range required {
						if _, ok := point[field].(float64); !ok {
							return invalid()
						}
					}
				case "size_scale":
					if _, ok := value.(float64); !ok {
						return invalid()
					}
				default:
					return invalid()
				}
			}
		}
		// Arbitrary attribute/palette values remain extensible, but text is bounded.
		if !s.validObject(v, 0) {
			return invalid()
		}
	case "zones":
		var values []map[string]any
		if json.Unmarshal(raw, &values) != nil || len(values) > 100 {
			return invalid()
		}
		for _, value := range values {
			for _, field := range []string{"x", "y", "width", "height"} {
				if _, ok := value[field].(float64); !ok {
					return invalid()
				}
			}
			if !s.validObject(value, 0) {
				return invalid()
			}
		}
	case "textList":
		var values []string
		if json.Unmarshal(raw, &values) != nil || len(values) > maxOutlineListItems {
			return invalid()
		}
		for _, v := range values {
			if len(v) > s.Limits.TextBytes || strings.ContainsRune(v, 0) {
				return invalid()
			}
		}
	case "id":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return invalid()
		}
		if id, err := uuid.Parse(value); err != nil || id == uuid.Nil || len(value) != 36 {
			return invalid()
		}
	case "index":
		// A position in a page's panel list; a page holds a handful, so this only rejects nonsense.
		var n int
		if json.Unmarshal(raw, &n) != nil || n < 0 || n > 1000 {
			return invalid()
		}
	case "ids":
		var values []string
		if json.Unmarshal(raw, &values) != nil {
			return invalid()
		}
		for _, v := range values {
			id, err := uuid.Parse(v)
			if err != nil || id == uuid.Nil || len(v) != 36 {
				return invalid()
			}
		}
	default:
		var value string
		if json.Unmarshal(raw, &value) != nil || strings.ContainsRune(value, 0) {
			return invalid()
		}
		if kind == "script" {
			if len(value) > s.Limits.ScriptBytes || len(strings.Fields(value)) > s.Limits.ScriptWords {
				return fmt.Errorf("%s exceeds configured script byte or word limit", name)
			}
		} else if len(value) > s.Limits.TextBytes {
			return fmt.Errorf("%s exceeds text field byte limit", name)
		}
		if (kind == "title" && utf8.RuneCountInString(value) > 255) || (kind == "artStyle" && utf8.RuneCountInString(value) > 64) {
			return invalid()
		}
		enums := map[string]string{
			"stage":  "draft,draft_sketch,line_art,final_line_art,character_reference,reference,colour_suggestion,colour",
			"format": "pdf,png,png_zip", "pageSize": "a4,A4,us_letter,US_Letter,custom,Custom",
			"projectStatus": "draft,in_progress,completed,archived", "chapterStatus": "pending,in_progress,approved,completed",
			"finishPreset": "off,print,print-plus,print-heavy",
		}
		if options, ok := enums[kind]; ok {
			found := false
			for _, option := range strings.Split(options, ",") {
				if value == option {
					found = true
					break
				}
			}
			if !found {
				return invalid()
			}
		}
	}
	return nil
}
func (s *Server) validObject(v any, depth int) bool {
	if depth > 16 {
		return false
	}
	switch x := v.(type) {
	case string:
		return len(x) <= s.Limits.TextBytes && !strings.ContainsRune(x, 0)
	case map[string]any:
		for k, value := range x {
			if len(k) > s.Limits.TextBytes || strings.ContainsRune(k, 0) || !s.validObject(value, depth+1) {
				return false
			}
		}
	case []any:
		for _, value := range x {
			if !s.validObject(value, depth+1) {
				return false
			}
		}
	}
	return true
}

// Apply current limits to saved input as well, including records written before
// these protections or before an operator lowered limits. No panel cap is added.
func (s *Server) validateSavedStory(w http.ResponseWriter, script string, chapters int) bool {
	if chapters < 0 || chapters > s.Limits.MaxChapters {
		writeError(w, 400, "Saved chapter count exceeds configured limit")
		return false
	}
	if err := s.validateField("original_script", "script", json.RawMessage(strconv.Quote(script))); err != nil {
		writeError(w, 400, err.Error())
		return false
	}
	return true
}
