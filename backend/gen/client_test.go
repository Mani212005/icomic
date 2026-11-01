package gen_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"icomic/backend/gen"
)

func TestAIServiceClientBoundary(t *testing.T) {
	// Mock server that returns valid OpenAPI response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v1/analyze-script":
			var req gen.AnalyzeScriptRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp := gen.AnalyzeScriptResponse{
				JobID: req.JobID,
				CharacterProfiles: []gen.CharacterProfile{
					{
						Name:             "Maya",
						Description:      "Young artist",
						AgeRange:         "20-25",
						Appearance:       "Athletic build",
						HairStyle:        "Short teal pixie",
						Clothing:         "Black hoodie",
						Personality:      "Creative, brave",
						VisualAttributes: map[string]interface{}{"accent": "teal"},
					},
				},
				SuggestedChapterCount: 3,
				WordCount:             150,
				Warnings:              []string{},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case "/internal/v1/moderate-prompt":
			var req gen.ModeratePromptRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			resp := gen.ModeratePromptResponse{
				JobID:   req.JobID,
				Blocked: false,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := gen.NewAIServiceClient(server.URL, 5*time.Second)

	// Test AnalyzeScript
	analyzeResp, err := client.AnalyzeScript(context.Background(), gen.AnalyzeScriptRequest{
		JobID:        "job-111",
		HashedUserID: "hashed-123",
		ProjectID:    "proj-222",
		Script:       "Test script content",
	})
	if err != nil {
		t.Fatalf("AnalyzeScript failed: %v", err)
	}
	if analyzeResp.JobID != "job-111" || len(analyzeResp.CharacterProfiles) != 1 {
		t.Fatalf("unexpected analyze response: %+v", analyzeResp)
	}

	// Test ModeratePrompt
	modResp, err := client.ModeratePrompt(context.Background(), gen.ModeratePromptRequest{
		JobID:        "job-222",
		HashedUserID: "hashed-123",
		Prompt:       "Safe comic scene",
	})
	if err != nil {
		t.Fatalf("ModeratePrompt failed: %v", err)
	}
	if modResp.Blocked {
		t.Fatal("expected prompt not to be blocked")
	}
}
