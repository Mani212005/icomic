package gen_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"icomic/backend/gen"
)

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func findAIDir(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	dir := wd
	for i := 0; i < 5; i++ {
		candidate := filepath.Join(dir, "ai")
		if stat, err := os.Stat(filepath.Join(candidate, "app", "main.py")); err == nil && !stat.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// TestFakeAIServiceClientHermetic verifies that FakeAIServiceClient conforms to gen.AIService hermetically.
func TestFakeAIServiceClientHermetic(t *testing.T) {
	ctx := context.Background()
	fake := gen.NewFakeAIServiceClient()

	// 1. AnalyzeScript
	targetCount := 3
	anResp, err := fake.AnalyzeScript(ctx, gen.AnalyzeScriptRequest{
		JobID:              "job-1",
		HashedUserID:       "hash-1",
		ProjectID:          "proj-1",
		Script:             "Scene 1: Maya arrives at the space station.",
		TargetChapterCount: &targetCount,
	})
	if err != nil || anResp == nil || len(anResp.CharacterProfiles) == 0 {
		t.Fatalf("AnalyzeScript failed: %v, resp: %+v", err, anResp)
	}

	// 2. ModeratePrompt
	modResp, err := fake.ModeratePrompt(ctx, gen.ModeratePromptRequest{
		JobID:        "job-2",
		HashedUserID: "hash-1",
		Prompt:       "A peaceful comic script",
	})
	if err != nil || modResp.Blocked {
		t.Fatalf("ModeratePrompt expected unblocked, got: %+v, err: %v", modResp, err)
	}
	modBlocked, err := fake.ModeratePrompt(ctx, gen.ModeratePromptRequest{
		JobID:        "job-2b",
		HashedUserID: "hash-1",
		Prompt:       "Contains illegal activities",
	})
	if err != nil || !modBlocked.Blocked {
		t.Fatalf("ModeratePrompt expected blocked, got: %+v", modBlocked)
	}

	// 3. GenerateCharacterReference
	charResp, err := fake.GenerateCharacterReference(ctx, gen.GenerateCharacterReferenceRequest{
		JobID:         "job-3",
		HashedUserID:  "hash-1",
		ProjectID:     "proj-1",
		CharacterID:   "char-1",
		CharacterName: "Maya",
		Prompt:        "Explorer",
		Style:         "manga",
	})
	if err != nil || !strings.HasPrefix(charResp.ImageURL, "http") {
		t.Fatalf("GenerateCharacterReference failed: %v, resp: %+v", err, charResp)
	}

	// 4. GenerateChapterOutline
	chapResp, err := fake.GenerateChapterOutline(ctx, gen.GenerateChapterOutlineRequest{
		JobID:        "job-4",
		HashedUserID: "hash-1",
		ProjectID:    "proj-1",
		Script:       "Test script",
		ChapterCount: 2,
	})
	if err != nil || len(chapResp.Chapters) != 2 {
		t.Fatalf("GenerateChapterOutline failed: %v, resp: %+v", err, chapResp)
	}

	// 5. GeneratePanelText
	panResp, err := fake.GeneratePanelText(ctx, gen.GeneratePanelTextRequest{
		JobID:          "job-5",
		HashedUserID:   "hash-1",
		ProjectID:      "proj-1",
		ChapterID:      "chap-1",
		ChapterNumber:  1,
		ChapterTitle:   "Title",
		ChapterSummary: "Summary",
		Characters:     []string{"Maya"},
	})
	if err != nil || len(panResp.Panels) != 4 {
		t.Fatalf("GeneratePanelText failed: %v, resp: %+v", err, panResp)
	}

	// 6. GenerateDraftSketch
	draftResp, err := fake.GenerateDraftSketch(ctx, gen.GenerateDraftSketchRequest{
		JobID:        "job-6",
		HashedUserID: "hash-1",
		ProjectID:    "proj-1",
		PageID:       "page-1",
		PageNumber:   1,
		Style:        "manga",
	})
	if err != nil || !strings.HasPrefix(draftResp.ImageURL, "http") {
		t.Fatalf("GenerateDraftSketch failed: %v, resp: %+v", err, draftResp)
	}

	// 7. GenerateLineArt
	lineResp, err := fake.GenerateLineArt(ctx, gen.GenerateLineArtRequest{
		JobID:         "job-7",
		HashedUserID:  "hash-1",
		ProjectID:     "proj-1",
		PageID:        "page-1",
		PageNumber:    1,
		DraftImageURL: draftResp.ImageURL,
		Style:         "manga",
	})
	if err != nil || !strings.HasPrefix(lineResp.ImageURL, "http") {
		t.Fatalf("GenerateLineArt failed: %v, resp: %+v", err, lineResp)
	}

	// 8. SuggestColourPalette
	colResp, err := fake.SuggestColourPalette(ctx, gen.SuggestColourPaletteRequest{
		JobID:           "job-8",
		HashedUserID:    "hash-1",
		ProjectID:       "proj-1",
		PageID:          "page-1",
		LineArtImageURL: lineResp.ImageURL,
		PanelContext:    "Neon city",
	})
	if err != nil || len(colResp.Palette) == 0 {
		t.Fatalf("SuggestColourPalette failed: %v, resp: %+v", err, colResp)
	}
	storedColour, err := fake.StoreColourImage(ctx, gen.StoreColourImageRequest{
		PageID: "page-1", ImageBase64: "cG5n", MediaType: gen.ImagePng,
	})
	if err != nil || !strings.Contains(storedColour.ImageURL, "page-1/colours/") {
		t.Fatalf("StoreColourImage failed: %v, resp: %+v", err, storedColour)
	}

	// 9. AssemblePdf
	pdfResp, err := fake.AssemblePdf(ctx, gen.AssemblePdfRequest{
		JobID:               "job-9",
		HashedUserID:        "hash-1",
		ProjectID:           "proj-1",
		Title:               "Comic Title",
		PageSize:            gen.A4,
		IncludeAIDisclosure: true,
		Pages: []gen.PageExportItem{
			{PageNumber: 1, ImageURL: lineResp.ImageURL, PanelCount: intPointer(4)},
		},
	})
	if err != nil || !strings.HasSuffix(pdfResp.PdfURL, ".pdf") {
		t.Fatalf("AssemblePdf failed: %v, resp: %+v", err, pdfResp)
	}

	// 10. GetJobStatus
	statusResp, err := fake.GetJobStatus(ctx, "job-9")
	if err != nil || statusResp.Status != "done" {
		t.Fatalf("GetJobStatus failed: %v, resp: %+v", err, statusResp)
	}
}

// TestAIServiceClientBoundaryRealPython starts the real Python FastAPI service on deterministic
// fake adapters and exercises all 10 endpoints of contracts/openapi/ai-service.yaml (SPEC 8.1, ARCHITECTURE 6.3).
func TestAIServiceClientBoundaryRealPython(t *testing.T) {
	aiDir := findAIDir(t)
	uvPath, uvErr := exec.LookPath("uv")

	isCI := os.Getenv("CI") != "" || os.Getenv("REQUIRE_PYTHON_AI_SERVICE") == "1"

	if uvErr != nil || aiDir == "" {
		if isCI {
			t.Fatalf("Python AI service (uv) is required in CI for boundary contract test, but not available (uv: %q, aiDir: %q)", uvPath, aiDir)
		}
		t.Skipf("Skipping real Python boundary test: uv or ai directory not found (uv: %q, aiDir: %q)", uvPath, aiDir)
	}

	// Allocate ephemeral port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate ephemeral port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	uvicornBin := filepath.Join(aiDir, ".venv", "bin", "uvicorn")
	var cmd *exec.Cmd
	if _, err := os.Stat(uvicornBin); err == nil {
		cmd = exec.CommandContext(ctx, uvicornBin, "app.main:app", "--host", "127.0.0.1", "--port", strconv.Itoa(port))
	} else {
		cmd = exec.CommandContext(ctx, "uv", "run", "uvicorn", "app.main:app", "--host", "127.0.0.1", "--port", strconv.Itoa(port))
	}
	cmd.Dir = aiDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(),
		"ADAPTER_LANGUAGE=fake",
		"ADAPTER_VISION=fake",
		"ADAPTER_IMAGE=fake",
		"ADAPTER_STORAGE=fake",
		"ADAPTER_QUEUE=fake",
		"PYTHONPATH=.",
	)
	var logBuf synchronizedBuffer
	cmd.Stdout = &logBuf
	cmd.Stderr = &logBuf

	if err := cmd.Start(); err != nil {
		if isCI {
			t.Fatalf("failed to start Python FastAPI service in CI: %v\nLogs: %s", err, logBuf.String())
		}
		t.Skipf("failed to start Python FastAPI service: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cmd.Wait()
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	healthURL := baseURL + "/healthz"
	t.Logf("Waiting for Python AI service at %s ...", healthURL)

	// Wait for server readiness
	var ready bool
	for i := 0; i < 150; i++ {
		resp, err := http.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("Python AI service failed to start at %s within 5 seconds.\nLogs:\n%s", healthURL, logBuf.String())
	}

	client := gen.NewAIServiceClient(baseURL, 15*time.Second)

	// Exercise Endpoint 1: AnalyzeScript (POST /internal/v1/analyze-script)
	targetChapters := 3
	anResp, err := client.AnalyzeScript(ctx, gen.AnalyzeScriptRequest{
		JobID:              "11111111-1111-1111-1111-111111111111",
		HashedUserID:       "user-hash-abc",
		ProjectID:          "22222222-2222-2222-2222-222222222222",
		Script:             "A scientist accidentally activates an ancient quantum portal in her lab.",
		TargetChapterCount: &targetChapters,
	})
	if err != nil {
		t.Fatalf("AnalyzeScript failed: %v", err)
	}
	if anResp.JobID != "11111111-1111-1111-1111-111111111111" || len(anResp.CharacterProfiles) == 0 {
		t.Fatalf("unexpected AnalyzeScript response: %+v", anResp)
	}

	// Exercise Endpoint 2: ModeratePrompt (POST /internal/v1/moderate-prompt) - unblocked
	modResp, err := client.ModeratePrompt(ctx, gen.ModeratePromptRequest{
		JobID:        "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		HashedUserID: "user-hash-abc",
		Prompt:       "A heroic speech at the summit",
	})
	if err != nil {
		t.Fatalf("ModeratePrompt failed: %v", err)
	}
	if modResp.Blocked {
		t.Fatalf("expected unblocked prompt, got: %+v", modResp)
	}

	// Exercise Endpoint 2b: ModeratePrompt (POST /internal/v1/moderate-prompt) - blocked
	modBlockedResp, err := client.ModeratePrompt(ctx, gen.ModeratePromptRequest{
		JobID:        "cccccccc-cccc-cccc-cccc-cccccccccccc",
		HashedUserID: "user-hash-abc",
		Prompt:       "An illegal extreme violence act",
	})
	if err != nil {
		t.Fatalf("ModeratePrompt (blocked) failed: %v", err)
	}
	if !modBlockedResp.Blocked || modBlockedResp.Reason == nil {
		t.Fatalf("expected blocked prompt with reason, got: %+v", modBlockedResp)
	}

	// Exercise Endpoint 3: GenerateCharacterReference (POST /internal/v1/generate-character-reference)
	charResp, err := client.GenerateCharacterReference(ctx, gen.GenerateCharacterReferenceRequest{
		JobID:         "33333333-3333-3333-3333-333333333333",
		HashedUserID:  "user-hash-abc",
		ProjectID:     "22222222-2222-2222-2222-222222222222",
		CharacterID:   "44444444-4444-4444-4444-444444444444",
		CharacterName: "Dr. Sarah",
		Prompt:        "Scientist in white lab coat with goggles",
		Style:         "classic-comic",
	})
	if err != nil {
		t.Fatalf("GenerateCharacterReference failed: %v", err)
	}
	if !strings.HasPrefix(charResp.ImageURL, "http") || charResp.ModelID != "muse-image-1.0-fake" {
		t.Fatalf("unexpected character reference response: %+v", charResp)
	}

	// Exercise Endpoint 4: GenerateChapterOutline (POST /internal/v1/generate-chapter-outline)
	outlineResp, err := client.GenerateChapterOutline(ctx, gen.GenerateChapterOutlineRequest{
		JobID:        "44444444-4444-4444-4444-444444444444",
		HashedUserID: "user-hash-abc",
		ProjectID:    "22222222-2222-2222-2222-222222222222",
		Script:       "The hero enters the dark cave looking for the lost artifact.",
		ChapterCount: 2,
		ApprovedCharacters: []gen.CharacterProfile{
			{
				Name:             "Maya",
				Description:      "Young explorer",
				AgeRange:         "20-25",
				Appearance:       "Short hair, leather jacket",
				HairStyle:        "Pixie",
				Clothing:         "Jacket and boots",
				Personality:      "Fearless",
				VisualAttributes: map[string]interface{}{"tone": "warm"},
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateChapterOutline failed: %v", err)
	}
	if len(outlineResp.Chapters) != 2 || outlineResp.Chapters[0].ChapterNumber != 1 {
		t.Fatalf("unexpected outline response: %+v", outlineResp)
	}

	// Exercise Endpoint 5: GeneratePanelText (POST /internal/v1/generate-panel-text)
	panelResp, err := client.GeneratePanelText(ctx, gen.GeneratePanelTextRequest{
		JobID:          "55555555-5555-5555-5555-555555555555",
		HashedUserID:   "user-hash-abc",
		ProjectID:      "22222222-2222-2222-2222-222222222222",
		ChapterID:      "66666666-6666-6666-6666-666666666666",
		ChapterNumber:  1,
		ChapterTitle:   "The Awakening",
		ChapterSummary: "The ancient portal powers on.",
		Characters:     []string{"Dr. Sarah"},
	})
	if err != nil {
		t.Fatalf("GeneratePanelText failed: %v", err)
	}
	if len(panelResp.Panels) != 4 || panelResp.Panels[0].SceneDescription == "" {
		t.Fatalf("unexpected panel response: %+v", panelResp)
	}

	// Exercise Endpoint 6: GenerateDraftSketch (POST /internal/v1/generate-draft-sketch)
	draftResp, err := client.GenerateDraftSketch(ctx, gen.GenerateDraftSketchRequest{
		JobID:        "77777777-7777-7777-7777-777777777777",
		HashedUserID: "user-hash-abc",
		ProjectID:    "22222222-2222-2222-2222-222222222222",
		PageID:       "88888888-8888-8888-8888-888888888888",
		PageNumber:   1,
		Panels: []gen.PanelTextItem{
			{
				PanelNumber:      1,
				SceneDescription: "Lab console explodes",
				Dialogue:         "It is overloading!",
				ImagePrompt:      "Comic panel 1",
			},
		},
		Style: "noir",
	})
	if err != nil {
		t.Fatalf("GenerateDraftSketch failed: %v", err)
	}
	if !strings.HasPrefix(draftResp.ImageURL, "http") || draftResp.ModelID != "muse-image-1.0-fake" {
		t.Fatalf("unexpected draft sketch response: %+v", draftResp)
	}

	// Exercise Endpoint 7: GenerateLineArt (POST /internal/v1/generate-line-art)
	lineArtResp, err := client.GenerateLineArt(ctx, gen.GenerateLineArtRequest{
		JobID:         "99999999-9999-9999-9999-999999999999",
		HashedUserID:  "user-hash-abc",
		ProjectID:     "22222222-2222-2222-2222-222222222222",
		PageID:        "88888888-8888-8888-8888-888888888888",
		PageNumber:    1,
		DraftImageURL: draftResp.ImageURL,
		Panels: []gen.PanelTextItem{
			{
				PanelNumber:      1,
				SceneDescription: "Lab console explodes",
				Dialogue:         "It is overloading!",
				ImagePrompt:      "Comic panel 1",
			},
		},
		Style: "noir",
	})
	if err != nil {
		t.Fatalf("GenerateLineArt failed: %v", err)
	}
	if !strings.HasPrefix(lineArtResp.ImageURL, "http") || lineArtResp.ModelID != "muse-image-1.0-fake" {
		t.Fatalf("unexpected line art response: %+v", lineArtResp)
	}

	// Exercise Endpoint 8: SuggestColourPalette (POST /internal/v1/suggest-colour-palette)
	palResp, err := client.SuggestColourPalette(ctx, gen.SuggestColourPaletteRequest{
		JobID:           "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		HashedUserID:    "user-hash-abc",
		ProjectID:       "22222222-2222-2222-2222-222222222222",
		PageID:          "88888888-8888-8888-8888-888888888888",
		LineArtImageURL: lineArtResp.ImageURL,
		PanelContext:    "Nighttime neon alleyway in cyberpunk city",
	})
	if err != nil {
		t.Fatalf("SuggestColourPalette failed: %v", err)
	}
	if palResp.Theme == "" || len(palResp.Palette) == 0 || !strings.HasPrefix(palResp.Palette[0].HexColor, "#") {
		t.Fatalf("unexpected colour palette response: %+v", palResp)
	}

	storedColour, err := client.StoreColourImage(ctx, gen.StoreColourImageRequest{
		PageID: "88888888-8888-8888-8888-888888888888", ImageBase64: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC", MediaType: gen.ImagePng,
	})
	if err != nil || !strings.Contains(storedColour.ImageURL, "/colours/") {
		t.Fatalf("StoreColourImage failed: %v, response: %+v", err, storedColour)
	}

	// Exercise Endpoint 9: AssemblePdf (POST /internal/v1/assemble-pdf)
	pdfResp, err := client.AssemblePdf(ctx, gen.AssemblePdfRequest{
		JobID:               "dddddddd-dddd-dddd-dddd-dddddddddddd",
		HashedUserID:        "user-hash-abc",
		ProjectID:           "22222222-2222-2222-2222-222222222222",
		Title:               "Quantum Odyssey",
		PageSize:            gen.A4,
		IncludeAIDisclosure: true,
		Pages: []gen.PageExportItem{
			{
				PageNumber:         1,
				ImageURL:           lineArtResp.ImageURL,
				PanelCount:         intPointer(4),
				ProvenanceMetadata: mapPointer(map[string]interface{}{"c2pa": true}),
			},
		},
	})
	if err != nil {
		t.Fatalf("AssemblePdf failed: %v", err)
	}
	if !strings.HasSuffix(pdfResp.PdfURL, ".pdf") || !pdfResp.ProvenanceMetadataPreserved || pdfResp.PageCount != 2 {
		t.Fatalf("unexpected assemble pdf response: %+v", pdfResp)
	}

	// Exercise Endpoint 10: GetJobStatus (GET /internal/v1/job/{id}/status)
	statusResp, err := client.GetJobStatus(ctx, "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("GetJobStatus failed: %v", err)
	}
	if statusResp.JobID != "11111111-1111-1111-1111-111111111111" || statusResp.Status != "done" {
		t.Fatalf("unexpected job status response: %+v", statusResp)
	}
}

func intPointer(value int) *int {
	return &value
}

func mapPointer(value map[string]interface{}) *map[string]interface{} {
	return &value
}
