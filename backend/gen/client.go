package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"icomic/backend/internal/domain"
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
	GeneratePaintedPage(ctx context.Context, req GeneratePaintedPageRequest) (*GeneratePaintedPageResponse, error)
	SuggestColourPalette(ctx context.Context, req SuggestColourPaletteRequest) (*SuggestColourPaletteResponse, error)
	StoreColourImage(ctx context.Context, req StoreColourImageRequest) (*StoreColourImageResponse, error)
	AssemblePdf(ctx context.Context, req AssemblePdfRequest) (*AssemblePdfResponse, error)
	GetJobStatus(ctx context.Context, jobID string) (*JobStatusResponse, error)
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
		return decodeServiceError(resp.Body, resp.StatusCode, resp.Header)
	}

	if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

// AIServiceError carries only the service's safe, structured error envelope.
// Provider response bodies and transport details are deliberately discarded.
type AIServiceError struct {
	Code              string
	Message           string
	StatusCode        int
	Retryable         bool
	RetryAfterSeconds *float64
}

func (e *AIServiceError) Error() string { return e.Message }

func decodeServiceError(body io.Reader, status int, headers http.Header) error {
	var response ErrorResponse
	if err := json.NewDecoder(io.LimitReader(body, 64*1024)).Decode(&response); err != nil || response.Code == "" || response.Message == "" {
		return &AIServiceError{
			Code: "AI_SERVICE_ERROR", Message: "Generation service could not complete the request.", StatusCode: status,
		}
	}
	retryable := response.Retryable != nil && *response.Retryable
	retryAfter := response.RetryAfterSeconds
	if retryAfter == nil {
		if seconds, err := strconv.Atoi(headers.Get("Retry-After")); err == nil && seconds >= 0 {
			value := float64(seconds)
			retryAfter = &value
		}
	}
	return &AIServiceError{
		Code: response.Code, Message: response.Message, StatusCode: status,
		Retryable: retryable, RetryAfterSeconds: retryAfter,
	}
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

func (c *AIServiceClient) GeneratePaintedPage(ctx context.Context, req GeneratePaintedPageRequest) (*GeneratePaintedPageResponse, error) {
	var resp GeneratePaintedPageResponse
	if err := c.postJSON(ctx, "/internal/v1/generate-painted-page", req, &resp); err != nil {
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

func (c *AIServiceClient) StoreColourImage(ctx context.Context, req StoreColourImageRequest) (*StoreColourImageResponse, error) {
	var resp StoreColourImageResponse
	if err := c.postJSON(ctx, "/internal/v1/store-colour-image", req, &resp); err != nil {
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
		return nil, decodeServiceError(resp.Body, resp.StatusCode, resp.Header)
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
	GeneratePaintedPageFunc        func(ctx context.Context, req GeneratePaintedPageRequest) (*GeneratePaintedPageResponse, error)
	SuggestColourPaletteFunc       func(ctx context.Context, req SuggestColourPaletteRequest) (*SuggestColourPaletteResponse, error)
	StoreColourImageFunc           func(ctx context.Context, req StoreColourImageRequest) (*StoreColourImageResponse, error)
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
	chapterCount := domain.SuggestedChapterCount(len(strings.Fields(req.Script)))
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
		count = domain.SuggestedChapterCount(len(strings.Fields(req.Script)))
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
	count := 4
	if req.PanelCount != nil && *req.PanelCount > 0 {
		count = *req.PanelCount
	}
	for i := 1; i <= count; i++ {
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

func (f *FakeAIServiceClient) GeneratePaintedPage(ctx context.Context, req GeneratePaintedPageRequest) (*GeneratePaintedPageResponse, error) {
	if f.GeneratePaintedPageFunc != nil {
		return f.GeneratePaintedPageFunc(ctx, req)
	}
	variant := string(req.Variant)
	return &GeneratePaintedPageResponse{
		JobID:    req.JobID,
		ImageURL: fmt.Sprintf("http://localhost:8000/storage/fake/painted/page_%s_%s.png", req.PageID, variant),
		ModelID:  "muse-image-1.0-fake",
		Variant:  GeneratePaintedPageResponseVariant(variant),
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

func (f *FakeAIServiceClient) StoreColourImage(ctx context.Context, req StoreColourImageRequest) (*StoreColourImageResponse, error) {
	if f.StoreColourImageFunc != nil {
		return f.StoreColourImageFunc(ctx, req)
	}
	return &StoreColourImageResponse{
		ImageURL: fmt.Sprintf("http://localhost:8000/storage/fake/pages/%s/colours/fake.png", req.PageID),
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
