package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"icomic/backend/gen"
	"icomic/backend/internal/domain"
)

func TestPageStagesCarryOrderedPanelSemanticsAndNamedIdentities(t *testing.T) {
	for _, stage := range []string{"draft", "line-art"} {
		t.Run(stage, func(t *testing.T) {
			s, handler, repo := setupTestServer(true, "test-user")
			seedWriteFailureFlow(t, repo)
			ch, _ := repo.GetCharacter(nil, "1bda3da7-79e9-55f5-b45e-67393fc73c26")
			ch.Name = "Nia"
			ch.Description = "Lighthouse keeper"
			ch.VisualAttributes = map[string]interface{}{"clothing": "yellow raincoat", "hair_style": "braid"}
			if err := repo.UpdateCharacter(nil, ch); err != nil {
				t.Fatal(err)
			}
			other := *ch
			other.ID, other.Name, other.ReferenceImageURL = "00000000-0000-4000-8000-000000000002", "Brass bird", "https://storage.example/bird.png"
			if err := repo.CreateCharacter(nil, &other); err != nil {
				t.Fatal(err)
			}
			pg, _ := repo.GetPage(nil, "a5669606-8764-5c90-b282-bad323a49baa")
			pg.PanelIDs = []string{"39582b41-45f8-55da-ae05-fb16986287f9", "a8a24397-3780-5a99-acea-8ab882a2ef5b"}
			if err := repo.UpdatePage(nil, pg); err != nil {
				t.Fatal(err)
			}
			panel := &domain.Panel{ID: "39582b41-45f8-55da-ae05-fb16986287f9", ChapterID: "9bc9e3c9-dc89-55a6-bc9e-4c7dcd732d98", PanelNumber: 8, SceneDescription: "Storm at sunrise", CharacterActions: "Raises brass bird", CameraAngle: "Close-up", Background: "A boat in rough seas", Dialogue: "Follow the light!", Narration: "Before dawn", ImagePrompt: "Wet stone and reflected light"}
			if err := repo.CreatePanel(nil, panel); err != nil {
				t.Fatal(err)
			}
			var panels []gen.PanelTextItem
			var identities *[]gen.CharacterRenderIdentity
			fake := s.AIClient.(*gen.FakeAIServiceClient)
			fake.GenerateDraftSketchFunc = func(ctx context.Context, req gen.GenerateDraftSketchRequest) (*gen.GenerateDraftSketchResponse, error) {
				panels, identities = req.Panels, req.CharacterIdentities
				return &gen.GenerateDraftSketchResponse{JobID: req.JobID, ImageURL: "https://storage.example/new-draft.png"}, nil
			}
			fake.GenerateLineArtFunc = func(ctx context.Context, req gen.GenerateLineArtRequest) (*gen.GenerateLineArtResponse, error) {
				panels, identities = req.Panels, req.CharacterIdentities
				return &gen.GenerateLineArtResponse{JobID: req.JobID, ImageURL: "https://storage.example/new-line.png"}, nil
			}
			path := "/api/v1/pages/a5669606-8764-5c90-b282-bad323a49baa/" + stage
			if stage == "line-art" {
				path += "?variant=ink"
			}
			rec := serveGeneration(s, handler, httptest.NewRequest(http.MethodPost, path, nil))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			want := gen.PanelTextItem{PanelNumber: 8, SceneDescription: panel.SceneDescription, CharacterActions: panel.CharacterActions, CameraAngle: panel.CameraAngle, Background: panel.Background, Dialogue: panel.Dialogue, Narration: panel.Narration, ImagePrompt: panel.ImagePrompt}
			if len(panels) != 2 || !reflect.DeepEqual(panels[0], want) || panels[1].PanelNumber != 1 {
				t.Fatalf("lost panel data/order: %+v", panels)
			}
			if identities == nil || len(*identities) != 2 {
				t.Fatalf("missing identities: %+v", identities)
			}
			if (*identities)[0].CharacterID != "00000000-0000-4000-8000-000000000002" || (*identities)[1].Name != "Nia" || !reflect.DeepEqual((*identities)[1].VisualAttributes, ch.VisualAttributes) || (*identities)[1].ReferenceImageURL != ch.ReferenceImageURL {
				t.Fatalf("lost identity mapping: %+v", *identities)
			}
		})
	}
}

func TestDefaultReferencePromptIncludesStoredVisualAttributes(t *testing.T) {
	for _, custom := range []string{"", "A deliberate custom direction"} {
		t.Run(custom, func(t *testing.T) {
			s, handler, repo := setupTestServer(true, "test-user")
			seedWriteFailureFlow(t, repo)
			ch, _ := repo.GetCharacter(nil, "1bda3da7-79e9-55f5-b45e-67393fc73c26")
			ch.Name, ch.Description = "Nia", "Keeper"
			ch.VisualAttributes = map[string]interface{}{"age_range": "adult", "appearance": "freckles", "hair_style": "braid", "clothing": "raincoat", "extra": map[string]interface{}{"prop": "bird"}}
			if err := repo.UpdateCharacter(nil, ch); err != nil {
				t.Fatal(err)
			}
			var prompt string
			s.AIClient.(*gen.FakeAIServiceClient).GenerateCharacterReferenceFunc = func(ctx context.Context, req gen.GenerateCharacterReferenceRequest) (*gen.GenerateCharacterReferenceResponse, error) {
				prompt = req.Prompt
				return &gen.GenerateCharacterReferenceResponse{JobID: req.JobID, ImageURL: "https://storage.example/reference.png"}, nil
			}
			body, _ := json.Marshal(map[string]string{"prompt": custom})
			rec := serveGeneration(s, handler, httptest.NewRequest(http.MethodPost, "/api/v1/characters/1bda3da7-79e9-55f5-b45e-67393fc73c26/reference-image", strings.NewReader(string(body))))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if custom != "" {
				if prompt != custom {
					t.Fatalf("custom direction lost: %s", prompt)
				}
				return
			}
			var got struct {
				Name, Description string
				VisualAttributes  map[string]interface{} `json:"visual_attributes"`
			}
			if err := json.Unmarshal([]byte(prompt), &got); err != nil {
				t.Fatal(err)
			}
			if got.Name != ch.Name || got.Description != ch.Description || !reflect.DeepEqual(got.VisualAttributes, ch.VisualAttributes) {
				t.Fatalf("lost stored attributes: %s", prompt)
			}
		})
	}
}
