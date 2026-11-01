package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

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
	JobID                        string `json:"job_id"`
	PdfURL                       string `json:"pdf_url"`
	ProvenanceMetadataPreserved bool   `json:"provenance_metadata_preserved"`
	PageCount                    int    `json:"page_count"`
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
