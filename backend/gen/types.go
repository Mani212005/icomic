package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AIService defines the interface for communicating with the AI service (SPEC 8, ARCHITECTURE 4.4).
type AIService interface {
	AnalyzeScript(ctx context.Context, req AnalyzeScriptRequest) (*AnalyzeScriptResponse, error)
	ModeratePrompt(ctx context.Context, req ModeratePromptRequest) (*ModeratePromptResponse, error)
	GenerateCharacterReference(ctx context.Context, req GenerateCharacterReferenceRequest) (*GenerateCharacterReferenceResponse, error)
	GenerateChapterOutline(ctx context.Context, req GenerateChapterOutlineRequest) (*GenerateChapterOutlineResponse, error)
	GeneratePanelText(ctx context.Context, req GeneratePanelTextRequest) (*GeneratePanelTextResponse, error)
	GenerateDraftSketch(ctx context.Context, req GenerateDraftSketchRequest) (*GenerateDraftSketchResponse, error)
	GenerateLineArt(ctx context.Context, req GenerateLineArtRequest) (*GenerateLineArtResponse, error)
	SuggestColourPalette(ctx context.Context, req SuggestColourPaletteRequest) (*SuggestColourPaletteResponse, error)
	AssemblePdf(ctx context.Context, req AssemblePdfRequest) (*AssemblePdfResponse, error)
	GetJobStatus(ctx context.Context, jobID string) (*JobStatusResponse, error)
}

// CharacterProfile corresponds to CharacterProfile in OpenAPI.
type CharacterProfile struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	AgeRange         string                 `json:"age_range"`
	Appearance       string                 `json:"appearance"`
	HairStyle        string                 `json:"hair_style"`
	Clothing         string                 `json:"clothing"`
	Personality      string                 `json:"personality"`
	VisualAttributes map[string]interface{} `json:"visual_attributes"`
}

// AnalyzeScriptRequest corresponds to AnalyzeScriptRequest in OpenAPI.
type AnalyzeScriptRequest struct {
	JobID              string `json:"job_id"`
	HashedUserID       string `json:"hashed_user_id"`
	ProjectID          string `json:"project_id"`
	Script             string `json:"script"`
	TargetChapterCount *int   `json:"target_chapter_count,omitempty"`
}

// AnalyzeScriptResponse corresponds to AnalyzeScriptResponse in OpenAPI.
type AnalyzeScriptResponse struct {
	JobID                 string             `json:"job_id"`
	CharacterProfiles     []CharacterProfile `json:"character_profiles"`
	SuggestedChapterCount int                `json:"suggested_chapter_count"`
	WordCount             int                `json:"word_count"`
	Warnings              []string           `json:"warnings"`
}

// GenerateCharacterReferenceRequest corresponds to OpenAPI.
type GenerateCharacterReferenceRequest struct {
	JobID           string   `json:"job_id"`
	HashedUserID    string   `json:"hashed_user_id"`
	ProjectID       string   `json:"project_id"`
	CharacterID     string   `json:"character_id"`
	CharacterName   string   `json:"character_name"`
	Prompt          string   `json:"prompt"`
	ReferenceImages []string `json:"reference_images,omitempty"`
	Style           string   `json:"style"`
}

// GenerateCharacterReferenceResponse corresponds to OpenAPI.
type GenerateCharacterReferenceResponse struct {
	JobID              string                 `json:"job_id"`
	ImageURL           string                 `json:"image_url"`
	ModelID            string                 `json:"model_id"`
	ProvenanceMetadata map[string]interface{} `json:"provenance_metadata"`
}

// ChapterOutlineItem corresponds to OpenAPI.
type ChapterOutlineItem struct {
	ChapterNumber   int      `json:"chapter_number"`
	Title           string   `json:"title"`
	Summary         string   `json:"summary"`
	ImportantEvents []string `json:"important_events"`
	CharacterNames  []string `json:"character_names"`
	Ending          string   `json:"ending"`
}

// GenerateChapterOutlineRequest corresponds to OpenAPI.
type GenerateChapterOutlineRequest struct {
	JobID              string             `json:"job_id"`
	HashedUserID       string             `json:"hashed_user_id"`
	ProjectID          string             `json:"project_id"`
	Script             string             `json:"script"`
	ChapterCount       int                `json:"chapter_count"`
	ApprovedCharacters []CharacterProfile `json:"approved_characters"`
}

// GenerateChapterOutlineResponse corresponds to OpenAPI.
type GenerateChapterOutlineResponse struct {
	JobID    string               `json:"job_id"`
	Chapters []ChapterOutlineItem `json:"chapters"`
}

// PanelTextItem corresponds to OpenAPI.
type PanelTextItem struct {
	PanelNumber      int    `json:"panel_number"`
	SceneDescription string `json:"scene_description"`
	CharacterActions string `json:"character_actions"`
	CameraAngle      string `json:"camera_angle"`
	Background       string `json:"background"`
	Dialogue         string `json:"dialogue"`
	Narration        string `json:"narration"`
	ImagePrompt      string `json:"image_prompt"`
}

// GeneratePanelTextRequest corresponds to OpenAPI.
type GeneratePanelTextRequest struct {
	JobID          string   `json:"job_id"`
	HashedUserID   string   `json:"hashed_user_id"`
	ProjectID      string   `json:"project_id"`
	ChapterID      string   `json:"chapter_id"`
	ChapterNumber  int      `json:"chapter_number"`
	ChapterTitle   string   `json:"chapter_title"`
	ChapterSummary string   `json:"chapter_summary"`
	Characters     []string `json:"characters"`
}

// GeneratePanelTextResponse corresponds to OpenAPI.
type GeneratePanelTextResponse struct {
	JobID  string          `json:"job_id"`
	Panels []PanelTextItem `json:"panels"`
}

// GenerateDraftSketchRequest corresponds to OpenAPI.
type GenerateDraftSketchRequest struct {
	JobID                       string          `json:"job_id"`
	HashedUserID                string          `json:"hashed_user_id"`
	ProjectID                   string          `json:"project_id"`
	PageID                      string          `json:"page_id"`
	PageNumber                  int             `json:"page_number"`
	Panels                      []PanelTextItem `json:"panels"`
	Style                       string          `json:"style"`
	CharacterReferenceImageURLs []string        `json:"character_reference_image_urls,omitempty"`
}

// GenerateDraftSketchResponse corresponds to OpenAPI.
type GenerateDraftSketchResponse struct {
	JobID              string                 `json:"job_id"`
	ImageURL           string                 `json:"image_url"`
	ModelID            string                 `json:"model_id"`
	ProvenanceMetadata map[string]interface{} `json:"provenance_metadata"`
}

// GenerateLineArtRequest corresponds to OpenAPI.
type GenerateLineArtRequest struct {
	JobID                       string          `json:"job_id"`
	HashedUserID                string          `json:"hashed_user_id"`
	ProjectID                   string          `json:"project_id"`
	PageID                      string          `json:"page_id"`
	PageNumber                  int             `json:"page_number"`
	DraftImageURL               string          `json:"draft_image_url"`
	Panels                      []PanelTextItem `json:"panels"`
	Style                       string          `json:"style"`
	CharacterReferenceImageURLs []string        `json:"character_reference_image_urls,omitempty"`
}

// GenerateLineArtResponse corresponds to OpenAPI.
type GenerateLineArtResponse struct {
	JobID              string                 `json:"job_id"`
	ImageURL           string                 `json:"image_url"`
	ModelID            string                 `json:"model_id"`
	ProvenanceMetadata map[string]interface{} `json:"provenance_metadata"`
}

// ColourRegionSuggestion corresponds to OpenAPI.
type ColourRegionSuggestion struct {
	RegionName string `json:"region_name"`
	HexColor   string `json:"hex_color"`
	Label      string `json:"label"`
}

// SuggestColourPaletteRequest corresponds to OpenAPI.
type SuggestColourPaletteRequest struct {
	JobID           string `json:"job_id"`
	HashedUserID    string `json:"hashed_user_id"`
	ProjectID       string `json:"project_id"`
	PageID          string `json:"page_id"`
	LineArtImageURL string `json:"line_art_image_url"`
	PanelContext    string `json:"panel_context"`
}

// SuggestColourPaletteResponse corresponds to OpenAPI.
type SuggestColourPaletteResponse struct {
	JobID   string                   `json:"job_id"`
	Theme   string                   `json:"theme"`
	Palette []ColourRegionSuggestion `json:"palette"`
}

// ModeratePromptRequest corresponds to OpenAPI.
type ModeratePromptRequest struct {
	JobID        string `json:"job_id"`
	HashedUserID string `json:"hashed_user_id"`
	Prompt       string `json:"prompt"`
}

// ModeratePromptResponse corresponds to OpenAPI.
type ModeratePromptResponse struct {
	JobID   string  `json:"job_id"`
	Blocked bool    `json:"blocked"`
	Reason  *string `json:"reason,omitempty"`
}

// PageExportItem corresponds to OpenAPI.
type PageExportItem struct {
	PageNumber         int                    `json:"page_number"`
	ImageURL           string                 `json:"image_url"`
	PanelCount         int                    `json:"panel_count,omitempty"`
	ProvenanceMetadata map[string]interface{} `json:"provenance_metadata,omitempty"`
}

// AssemblePdfRequest corresponds to OpenAPI.
type AssemblePdfRequest struct {
	JobID               string           `json:"job_id"`
	HashedUserID        string           `json:"hashed_user_id"`
	ProjectID           string           `json:"project_id"`
	Title               string           `json:"title"`
	PageSize            string           `json:"page_size"`
	IncludeAIDisclosure bool             `json:"include_ai_disclosure"`
	Pages               []PageExportItem `json:"pages"`
}

// AssemblePdfResponse corresponds to OpenAPI.
type AssemblePdfResponse struct {
	JobID                       string `json:"job_id"`
	PdfURL                      string `json:"pdf_url"`
	ProvenanceMetadataPreserved bool   `json:"provenance_metadata_preserved"`
	PageCount                   int    `json:"page_count"`
}

// JobStatusResponse corresponds to OpenAPI.
type JobStatusResponse struct {
	JobID        string   `json:"job_id"`
	Status       string   `json:"status"`
	ActualCost   *float64 `json:"actual_cost,omitempty"`
	ErrorMessage *string  `json:"error_message,omitempty"`
}

// AIServiceClient defines an HTTP client to communicate with the Python AI service.
type AIServiceClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewAIServiceClient(baseURL string, timeout time.Duration) *AIServiceClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &AIServiceClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *AIServiceClient) postJSON(ctx context.Context, path string, reqBody interface{}, respBody interface{}) error {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request to %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ai service %s returned status %d: %s", path, resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

func (c *AIServiceClient) AnalyzeScript(ctx context.Context, req AnalyzeScriptRequest) (*AnalyzeScriptResponse, error) {
	var resp AnalyzeScriptResponse
	if err := c.postJSON(ctx, "/internal/v1/analyze-script", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) ModeratePrompt(ctx context.Context, req ModeratePromptRequest) (*ModeratePromptResponse, error) {
	var resp ModeratePromptResponse
	if err := c.postJSON(ctx, "/internal/v1/moderate-prompt", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) GenerateCharacterReference(ctx context.Context, req GenerateCharacterReferenceRequest) (*GenerateCharacterReferenceResponse, error) {
	var resp GenerateCharacterReferenceResponse
	if err := c.postJSON(ctx, "/internal/v1/generate-character-reference", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) GenerateChapterOutline(ctx context.Context, req GenerateChapterOutlineRequest) (*GenerateChapterOutlineResponse, error) {
	var resp GenerateChapterOutlineResponse
	if err := c.postJSON(ctx, "/internal/v1/generate-chapter-outline", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) GeneratePanelText(ctx context.Context, req GeneratePanelTextRequest) (*GeneratePanelTextResponse, error) {
	var resp GeneratePanelTextResponse
	if err := c.postJSON(ctx, "/internal/v1/generate-panel-text", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) GenerateDraftSketch(ctx context.Context, req GenerateDraftSketchRequest) (*GenerateDraftSketchResponse, error) {
	var resp GenerateDraftSketchResponse
	if err := c.postJSON(ctx, "/internal/v1/generate-draft-sketch", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) GenerateLineArt(ctx context.Context, req GenerateLineArtRequest) (*GenerateLineArtResponse, error) {
	var resp GenerateLineArtResponse
	if err := c.postJSON(ctx, "/internal/v1/generate-line-art", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) SuggestColourPalette(ctx context.Context, req SuggestColourPaletteRequest) (*SuggestColourPaletteResponse, error) {
	var resp SuggestColourPaletteResponse
	if err := c.postJSON(ctx, "/internal/v1/suggest-colour-palette", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) AssemblePdf(ctx context.Context, req AssemblePdfRequest) (*AssemblePdfResponse, error) {
	var resp AssemblePdfResponse
	if err := c.postJSON(ctx, "/internal/v1/assemble-pdf", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AIServiceClient) GetJobStatus(ctx context.Context, jobID string) (*JobStatusResponse, error) {
	path := fmt.Sprintf("/internal/v1/job/%s/status", jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get job status %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ai service %s returned %d: %s", path, resp.StatusCode, string(body))
	}

	var statusResp JobStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &statusResp, nil
}

// Ensure *AIServiceClient implements AIService
var _ AIService = (*AIServiceClient)(nil)

// FakeAIServiceClient provides deterministic fake responses for hermetic unit tests.
type FakeAIServiceClient struct {
	AnalyzeScriptFunc              func(ctx context.Context, req AnalyzeScriptRequest) (*AnalyzeScriptResponse, error)
	ModeratePromptFunc             func(ctx context.Context, req ModeratePromptRequest) (*ModeratePromptResponse, error)
	GenerateCharacterReferenceFunc func(ctx context.Context, req GenerateCharacterReferenceRequest) (*GenerateCharacterReferenceResponse, error)
	GenerateChapterOutlineFunc     func(ctx context.Context, req GenerateChapterOutlineRequest) (*GenerateChapterOutlineResponse, error)
	GeneratePanelTextFunc          func(ctx context.Context, req GeneratePanelTextRequest) (*GeneratePanelTextResponse, error)
	GenerateDraftSketchFunc        func(ctx context.Context, req GenerateDraftSketchRequest) (*GenerateDraftSketchResponse, error)
	GenerateLineArtFunc            func(ctx context.Context, req GenerateLineArtRequest) (*GenerateLineArtResponse, error)
	SuggestColourPaletteFunc       func(ctx context.Context, req SuggestColourPaletteRequest) (*SuggestColourPaletteResponse, error)
	AssemblePdfFunc                func(ctx context.Context, req AssemblePdfRequest) (*AssemblePdfResponse, error)
	GetJobStatusFunc               func(ctx context.Context, jobID string) (*JobStatusResponse, error)
}

// Ensure *FakeAIServiceClient implements AIService
var _ AIService = (*FakeAIServiceClient)(nil)

func NewFakeAIServiceClient() *FakeAIServiceClient {
	return &FakeAIServiceClient{}
}

func (f *FakeAIServiceClient) AnalyzeScript(ctx context.Context, req AnalyzeScriptRequest) (*AnalyzeScriptResponse, error) {
	if f.AnalyzeScriptFunc != nil {
		return f.AnalyzeScriptFunc(ctx, req)
	}
	chapterCount := 3
	if req.TargetChapterCount != nil && *req.TargetChapterCount > 0 {
		chapterCount = *req.TargetChapterCount
	}
	wordCount := len(strings.Fields(req.Script))
	if wordCount == 0 {
		wordCount = 100
	}
	return &AnalyzeScriptResponse{
		JobID: req.JobID,
		CharacterProfiles: []CharacterProfile{
			{
				Name:             "Maya",
				Description:      "A daring young comic artist exploring uncharted space",
				AgeRange:         "20-25",
				Appearance:       "Athletic build, short teal pixie cut",
				HairStyle:        "Short teal pixie",
				Clothing:         "Leather jacket and utility trousers",
				Personality:      "Curious, brave, resourceful",
				VisualAttributes: map[string]interface{}{"jacket": "leather", "hair": "teal"},
			},
			{
				Name:             "Leo",
				Description:      "A loyal robot navigator",
				AgeRange:         "Ageless",
				Appearance:       "Compact metallic frame with expressive amber optics",
				HairStyle:        "None",
				Clothing:         "Chassis with carbon plating",
				Personality:      "Analytical, protective, dry wit",
				VisualAttributes: map[string]interface{}{"optics": "amber"},
			},
		},
		SuggestedChapterCount: chapterCount,
		WordCount:             wordCount,
		Warnings:              []string{},
	}, nil
}

func (f *FakeAIServiceClient) ModeratePrompt(ctx context.Context, req ModeratePromptRequest) (*ModeratePromptResponse, error) {
	if f.ModeratePromptFunc != nil {
		return f.ModeratePromptFunc(ctx, req)
	}
	lower := strings.ToLower(req.Prompt)
	blockedKeywords := []string{"illegal", "extreme violence", "banned", "hate"}
	for _, kw := range blockedKeywords {
		if strings.Contains(lower, kw) {
			reason := fmt.Sprintf("content policy violation: prompt contains %q", kw)
			return &ModeratePromptResponse{
				JobID:   req.JobID,
				Blocked: true,
				Reason:  &reason,
			}, nil
		}
	}
	return &ModeratePromptResponse{
		JobID:   req.JobID,
		Blocked: false,
	}, nil
}

func (f *FakeAIServiceClient) GenerateCharacterReference(ctx context.Context, req GenerateCharacterReferenceRequest) (*GenerateCharacterReferenceResponse, error) {
	if f.GenerateCharacterReferenceFunc != nil {
		return f.GenerateCharacterReferenceFunc(ctx, req)
	}
	return &GenerateCharacterReferenceResponse{
		JobID:    req.JobID,
		ImageURL: fmt.Sprintf("http://localhost:8000/storage/fake/characters/%s.png", req.CharacterID),
		ModelID:  "muse-image-1.0-fake",
		ProvenanceMetadata: map[string]interface{}{
			"c2pa": map[string]interface{}{
				"watermark_present": true,
			},
		},
	}, nil
}

func (f *FakeAIServiceClient) GenerateChapterOutline(ctx context.Context, req GenerateChapterOutlineRequest) (*GenerateChapterOutlineResponse, error) {
	if f.GenerateChapterOutlineFunc != nil {
		return f.GenerateChapterOutlineFunc(ctx, req)
	}
	count := req.ChapterCount
	if count <= 0 {
		count = 2
	}
	var chapters []ChapterOutlineItem
	for i := 1; i <= count; i++ {
		chapters = append(chapters, ChapterOutlineItem{
			ChapterNumber:   i,
			Title:           fmt.Sprintf("Chapter %d: The Journey Begins", i),
			Summary:         fmt.Sprintf("Summary of events for chapter %d.", i),
			ImportantEvents: []string{fmt.Sprintf("Event A in chapter %d", i), fmt.Sprintf("Event B in chapter %d", i)},
			CharacterNames:  []string{"Maya", "Leo"},
			Ending:          fmt.Sprintf("Cliffhanger at the end of chapter %d", i),
		})
	}
	return &GenerateChapterOutlineResponse{
		JobID:    req.JobID,
		Chapters: chapters,
	}, nil
}

func (f *FakeAIServiceClient) GeneratePanelText(ctx context.Context, req GeneratePanelTextRequest) (*GeneratePanelTextResponse, error) {
	if f.GeneratePanelTextFunc != nil {
		return f.GeneratePanelTextFunc(ctx, req)
	}
	var panels []PanelTextItem
	for i := 1; i <= 4; i++ {
		panels = append(panels, PanelTextItem{
			PanelNumber:      i,
			SceneDescription: fmt.Sprintf("Panel %d: Scene description for chapter %d", i, req.ChapterNumber),
			CharacterActions: fmt.Sprintf("Maya inspects the control panel in panel %d", i),
			CameraAngle:      "Medium shot",
			Background:       "Sci-fi spaceship interior",
			Dialogue:         fmt.Sprintf("Dialogue line for panel %d", i),
			Narration:        fmt.Sprintf("Narration caption for panel %d", i),
			ImagePrompt:      fmt.Sprintf("Comic illustration of sci-fi scene panel %d", i),
		})
	}
	return &GeneratePanelTextResponse{
		JobID:  req.JobID,
		Panels: panels,
	}, nil
}

func (f *FakeAIServiceClient) GenerateDraftSketch(ctx context.Context, req GenerateDraftSketchRequest) (*GenerateDraftSketchResponse, error) {
	if f.GenerateDraftSketchFunc != nil {
		return f.GenerateDraftSketchFunc(ctx, req)
	}
	return &GenerateDraftSketchResponse{
		JobID:    req.JobID,
		ImageURL: fmt.Sprintf("http://localhost:8000/storage/fake/drafts/page_%s.png", req.PageID),
		ModelID:  "muse-image-1.0-fake",
		ProvenanceMetadata: map[string]interface{}{
			"c2pa": map[string]interface{}{
				"watermark_present": true,
			},
		},
	}, nil
}

func (f *FakeAIServiceClient) GenerateLineArt(ctx context.Context, req GenerateLineArtRequest) (*GenerateLineArtResponse, error) {
	if f.GenerateLineArtFunc != nil {
		return f.GenerateLineArtFunc(ctx, req)
	}
	return &GenerateLineArtResponse{
		JobID:    req.JobID,
		ImageURL: fmt.Sprintf("http://localhost:8000/storage/fake/lineart/page_%s.png", req.PageID),
		ModelID:  "muse-image-1.0-fake",
		ProvenanceMetadata: map[string]interface{}{
			"c2pa": map[string]interface{}{
				"watermark_present": true,
			},
		},
	}, nil
}

func (f *FakeAIServiceClient) SuggestColourPalette(ctx context.Context, req SuggestColourPaletteRequest) (*SuggestColourPaletteResponse, error) {
	if f.SuggestColourPaletteFunc != nil {
		return f.SuggestColourPaletteFunc(ctx, req)
	}
	return &SuggestColourPaletteResponse{
		JobID: req.JobID,
		Theme: "Cyberpunk Neon",
		Palette: []ColourRegionSuggestion{
			{RegionName: "background", HexColor: "#1A0B2E", Label: "Deep Space Violet"},
			{RegionName: "skin", HexColor: "#F5D0C5", Label: "Warm Fair Skin"},
			{RegionName: "hair", HexColor: "#00CED1", Label: "Vibrant Cyan"},
		},
	}, nil
}

func (f *FakeAIServiceClient) AssemblePdf(ctx context.Context, req AssemblePdfRequest) (*AssemblePdfResponse, error) {
	if f.AssemblePdfFunc != nil {
		return f.AssemblePdfFunc(ctx, req)
	}
	pageCount := len(req.Pages) + 1 // 1 cover page + N comic pages
	return &AssemblePdfResponse{
		JobID:                       req.JobID,
		PdfURL:                      fmt.Sprintf("http://localhost:8000/storage/fake/exports/%s/comic.pdf", req.ProjectID),
		ProvenanceMetadataPreserved: true,
		PageCount:                   pageCount,
	}, nil
}

func (f *FakeAIServiceClient) GetJobStatus(ctx context.Context, jobID string) (*JobStatusResponse, error) {
	if f.GetJobStatusFunc != nil {
		return f.GetJobStatusFunc(ctx, jobID)
	}
	cost := 0.01
	return &JobStatusResponse{
		JobID:      jobID,
		Status:     "done",
		ActualCost: &cost,
	}, nil
}
