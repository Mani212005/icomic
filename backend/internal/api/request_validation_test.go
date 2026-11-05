package api_test

import (
	"context"
	"errors"
	"fmt"
	"icomic/backend/gen"
	"icomic/backend/internal/domain"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the public HTTP boundary before any project, job or paid call exists.
func TestPublicRequestValidation(t *testing.T) {
	_, handler, repo := setupTestServer(true, "test-user")
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"trailing JSON", "/api/v1/projects", `{"original_script":"A safe story"} {}`, 400},
		{"unknown field", "/api/v1/projects", `{"original_script":"A safe story","typo":true}`, 400},
		{"null object", "/api/v1/projects", `null`, 400},
		{"negative chapters", "/api/v1/projects", `{"original_script":"A safe story","chapter_count":-1}`, 400},
		{"negative estimate", "/api/v1/projects/00000000-0000-0000-0000-000000000001/estimate", `{"stage":"draft_sketch","image_count":-1}`, 400},
		{"unknown stage", "/api/v1/projects/00000000-0000-0000-0000-000000000001/estimate", `{"stage":"surprise","image_count":1}`, 400},
		{"malformed ID", "/api/v1/projects/not-an-id/analyze", `{}`, 400},
		{"oversized JSON", "/api/v1/projects", `{"original_script":"` + strings.Repeat("a", 2*1024*1024) + `"}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := server.Client().Post(server.URL+tc.path, "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", res.StatusCode, tc.status, string(body[:min(len(body), 300)]))
			}
		})
	}
	projects, err := repo.ListProjects(context.Background(), "test-user")
	if err != nil || len(projects) != 0 {
		t.Fatalf("invalid requests persisted projects: %v %v", len(projects), err)
	}
}

func TestPublicCORSDeniedOrigin(t *testing.T) {
	s, _, _ := setupTestServer(true, "test-user")
	s.AllowedOrigin = "https://allowed.example"
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/projects", nil)
	req.Header.Set("Origin", "https://denied.example")
	rec := httptest.NewRecorder()
	s.NewRouter().ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("denied origin received allow header %q", got)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Fatal("missing Vary: Origin")
	}
}

func TestPublicRequestFieldLimitsAndChunkedBodies(t *testing.T) {
	s, _, repo := setupTestServer(true, "test-user")
	s.Limits.ScriptBytes = 20
	s.Limits.ScriptWords = 3
	s.Limits.TextBytes = 12
	s.Limits.MaxChapters = 25
	s.Limits.JSONBytes = 128
	handler := s.NewRouter()
	for _, body := range []string{
		`{"original_script":"a b c d"}`,
		`{"original_script":"` + strings.Repeat("a", 21) + `"}`,
		`{"original_script":"ok","title":"` + strings.Repeat("a", 13) + `"}`,
		`{"original_script":"ok","chapter_count":26}`,
		`{"original_script":"ok","chapter_count":1.1}`,
		`{"original_script":"ok","chapter_count":9223372036854775808}`,
		`{"original_script":null}`,
		`{"original_script":"a\u0000b"}`,
		`{"original_script":"ok","title":"a","title":"b"}`,
		`{"original_script":"ok"}garbage`,
		`{"original_script":"ok","art_style":false}`,
		`{"original_script":"ok","status":"draft"}`,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(`{"original_script":"`+strings.Repeat("a", 200)+`"}`))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("chunked oversized request: %d", rec.Code)
	}
	projects, _ := repo.ListProjects(context.Background(), "test-user")
	if len(projects) != 0 {
		t.Fatalf("invalid requests persisted %d projects", len(projects))
	}
	// Preserve explicit chapter counts above ten, without any panel ceiling.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/projects", strings.NewReader(`{"original_script":"one two three","chapter_count":25}`)))
	if rec.Code != 201 {
		t.Fatalf("valid boundary: %d %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidRequestsCannotDispatchOrCharge(t *testing.T) {
	s, _, repo := setupTestServer(true, "test-user")
	seedWriteFailureFlow(t, repo)
	calls := 0
	fake := s.AIClient.(*gen.FakeAIServiceClient)
	fake.ModeratePromptFunc = func(context.Context, gen.ModeratePromptRequest) (*gen.ModeratePromptResponse, error) {
		calls++
		return nil, errors.New("must not dispatch")
	}
	fake.GenerateCharacterReferenceFunc = func(context.Context, gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
		calls++
		return nil, errors.New("must not dispatch")
	}
	handler := s.NewRouter()
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/characters/00000000-0000-0000-0000-000000000001/reference-image", `{"prompt":`},
		{"PATCH", "/pages/00000000-0000-0000-0000-000000000001", `{"panel_ids":["bad-id"]}`},
		{"PATCH", "/pages/00000000-0000-0000-0000-000000000001/colour-palette", `{"palette":[]}`},
		{"PATCH", "/panels/00000000-0000-0000-0000-000000000001", `{"dialogue":1}`},
		{"POST", "/projects/00000000-0000-0000-0000-000000000001/export", `{"format":"surprise"}`},
		{"POST", "/projects/00000000-0000-0000-0000-000000000001/export", `{"page_size":"a4,us_letter"}`},
		{"POST", "/projects/00000000-0000-0000-0000-000000000001/analyze", `{"surprise":true}`},
		{"POST", "/pages/00000000-0000-0000-0000-000000000001/draft", `{"surprise":true}`},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader(tc.body)))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("%d provider calls", calls)
	}
	ledger, err := s.CreditService.GetUsageHistory(context.Background(), "test-user")
	if err != nil || len(ledger) != 0 {
		t.Fatalf("invalid requests charged: %v %v", ledger, err)
	}
}

func TestSavedStoryCannotBypassLoweredLimits(t *testing.T) {
	s, _, repo := setupTestServer(true, "test-user")
	project := &domain.Project{UserID: "test-user", OriginalScript: "one two three four", ChapterCount: 1}
	if err := repo.CreateProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	s.Limits.ScriptWords = 3
	calls := 0
	s.AIClient.(*gen.FakeAIServiceClient).ModeratePromptFunc = func(context.Context, gen.ModeratePromptRequest) (*gen.ModeratePromptResponse, error) {
		calls++
		return nil, errors.New("must not dispatch")
	}
	for _, suffix := range []string{"analyze", "chapters"} {
		rec := httptest.NewRecorder()
		s.NewRouter().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/projects/"+project.ID+"/"+suffix, nil))
		if rec.Code != 400 {
			t.Fatalf("saved script: %d %s", rec.Code, rec.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("%d moderation calls", calls)
	}
}

func TestPublicAdmissionByIPAndUser(t *testing.T) {
	for _, byUser := range []bool{false, true} {
		t.Run(fmt.Sprint(byUser), func(t *testing.T) {
			s, _, _ := setupTestServer(true, "test-user")
			if byUser {
				s.Limits.RequestsPerUser = 2
			} else {
				s.Limits.RequestsPerIP = 2
			}
			handler := s.NewRouter()
			for i := 0; i < 3; i++ {
				req := httptest.NewRequest("GET", "/api/v1/projects", nil)
				req.RemoteAddr = "192.0.2.1:1234"
				if byUser {
					req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i+1)
				} else {
					req.Header.Set("X-User-ID", fmt.Sprintf("user-%d", i))
				}
				req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				expected := 200
				if i == 2 {
					expected = 429
				}
				if rec.Code != expected {
					t.Fatalf("request %d: %d %s", i, rec.Code, rec.Body.String())
				}
				if i == 2 && rec.Header().Get("Retry-After") == "" {
					t.Fatal("missing Retry-After")
				}
			}
		})
	}
}

func TestPublicCORSConfiguredOrigins(t *testing.T) {
	for _, allowed := range []string{"https://one.example, https://two.example", "*"} {
		s, _, _ := setupTestServer(true, "test-user")
		s.AllowedOrigin = allowed
		for _, origin := range []string{"https://one.example", "https://two.example", "https://denied.example", ""} {
			req := httptest.NewRequest("OPTIONS", "/api/v1/projects", nil)
			req.Header.Set("Origin", origin)
			rec := httptest.NewRecorder()
			s.NewRouter().ServeHTTP(rec, req)
			expected := origin
			if origin == "https://denied.example" || origin == "" {
				expected = ""
			}
			if allowed == "*" {
				expected = "*"
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != expected {
				t.Fatalf("%s %s: %v", allowed, origin, rec.Header())
			}
			if allowed == "*" && rec.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("wildcard allowed credentials")
			}
		}
	}
}

func TestPublicFieldsRespectDatabaseConstraints(t *testing.T) {
	s, _, _ := setupTestServer(true, "test-user")
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/projects", `{"original_script":"ok","title":"` + strings.Repeat("a", 256) + `"}`},
		{"POST", "/projects", `{"original_script":"ok","art_style":"` + strings.Repeat("a", 65) + `"}`},
		{"PATCH", "/projects/00000000-0000-0000-0000-000000000001", `{"status":"created"}`},
		{"PATCH", "/chapters/00000000-0000-0000-0000-000000000001", `{"status":"draft"}`},
		{"PATCH", "/projects/00000000-0000-0000-0000-000000000001", `{"chapter_count":0}`},
		{"POST", "/projects/00000000-0000-0000-0000-000000000001/estimate", `{}`},
	} {
		rec := httptest.NewRecorder()
		s.NewRouter().ServeHTTP(rec, httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader(tc.body)))
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", tc.body, rec.Code, rec.Body.String())
		}
	}
}
