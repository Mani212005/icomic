package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"icomic/backend/gen"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

const (
	exportFormatPDF    = "pdf"
	exportFormatPNGZip = "png_zip"
)

// exportRequest is the optional body of POST /projects/{id}/export. The field values were
// already checked against the public enums; an empty body means a PDF on A4.
type exportRequest struct {
	Format   string `json:"format"`
	PageSize string `json:"page_size"`
}

// exportOptions maps the public spellings (a4, us_letter, custom, png) onto the contract's.
func (r exportRequest) options() (format string, pageSize gen.AssemblePdfRequestPageSize) {
	format = exportFormatPDF
	if r.Format == "png" || r.Format == exportFormatPNGZip {
		format = exportFormatPNGZip
	}
	switch strings.ToLower(r.PageSize) {
	case "us_letter":
		return format, gen.USLetter
	case "custom":
		return format, gen.CustomComic
	default:
		return format, gen.A4
	}
}

// exportProblem is one reason a project cannot be exported yet, in terms the user can act on.
type exportProblem struct {
	PageNumber int    `json:"page_number,omitempty"`
	PageID     string `json:"page_id,omitempty"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

// exportArtwork is the approved, current image a page exports, with the AI image under it.
type exportArtwork struct {
	url         string
	originalURL *string
	// aiGenerated is false for user-supplied colouring, whose AI source is the original.
	aiGenerated bool
}

// approvedArtwork returns the most recently approved image that is still current for the page,
// or nil when nothing is approved or every approval has gone stale.
func (s *Server) approvedArtwork(ctx context.Context, pg *domain.Page) (*exportArtwork, error) {
	var best *exportArtwork
	var bestAt time.Time
	consider := func(art exportArtwork, approvedAt time.Time) {
		if best == nil || approvedAt.After(bestAt) {
			best, bestAt = &art, approvedAt
		}
	}
	earlier := func(urls ...string) *string {
		for _, u := range urls {
			if u != "" {
				return &u
			}
		}
		return nil
	}

	if pg.LineArtImageURL != "" && pg.LineArtApprovedAt != nil && !domain.IsApprovalStale(pg.LineArtApprovedAt, pg.UpdatedAt) {
		consider(exportArtwork{url: pg.LineArtImageURL, originalURL: earlier(pg.DraftImageURL), aiGenerated: true}, *pg.LineArtApprovedAt)
	}
	if pg.ColourImageURL != "" && pg.ColourImageUploadedAt != nil {
		approval, err := s.Repo.GetLatestApproval(ctx, "colour_page", pg.ID)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
		if err == nil && approval.ApprovedAt.After(*pg.ColourImageUploadedAt) && !domain.IsApprovalStale(&approval.ApprovedAt, pg.UpdatedAt) {
			consider(exportArtwork{url: pg.ColourImageURL, originalURL: earlier(pg.LineArtImageURL, pg.DraftImageURL)}, approval.ApprovedAt)
		}
	}
	if pg.PaintedPageImageURL != "" && pg.PaintedPageApprovedAt != nil && !domain.IsApprovalStale(pg.PaintedPageApprovedAt, pg.UpdatedAt) {
		consider(exportArtwork{url: pg.PaintedPageImageURL, originalURL: earlier(pg.DraftImageURL, pg.LineArtImageURL), aiGenerated: true}, *pg.PaintedPageApprovedAt)
	}
	return best, nil
}

// recordProvenance keeps the provider's provenance for a stored image exactly as returned.
// Generation has already been paid for, so a failed write is logged rather than failing the
// request; export then reports the page as lacking provenance instead of inventing it.
func (s *Server) recordProvenance(ctx context.Context, projectID, imageURL string, provenance map[string]interface{}) {
	if len(provenance) == 0 {
		return
	}
	if err := s.Repo.SaveArtifactProvenance(ctx, projectID, imageURL, provenance); err != nil {
		log.Printf("record provenance for project %s: %v", projectID, err)
	}
}

// storedProvenance returns the recorded provenance for an image, or nil when none was kept.
func (s *Server) storedProvenance(ctx context.Context, imageURL string) (*map[string]interface{}, error) {
	provenance, err := s.Repo.GetArtifactProvenance(ctx, imageURL)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &provenance, nil
}

func writeExportNotReady(w http.ResponseWriter, problems []exportProblem) {
	const shown = 3
	messages := make([]string, 0, shown)
	for i, problem := range problems {
		if i == shown {
			messages = append(messages, fmt.Sprintf("and %d more", len(problems)-shown))
			break
		}
		messages = append(messages, problem.Message)
	}
	writeJSON(w, http.StatusConflict, JSONResponse{
		Code:    "EXPORT_NOT_READY",
		Message: "This comic is not ready to export: " + strings.Join(messages, " "),
		Details: map[string]interface{}{"problems": problems},
	})
}

// ExportProject handles POST /api/v1/projects/{id}/export
func (s *Server) ExportProject(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserID(r)
	projectID := r.PathValue("id")

	proj, err := s.Repo.GetProject(r.Context(), projectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return
	}
	if proj.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if s.AIClient == nil {
		writeError(w, http.StatusServiceUnavailable, "AI service not available")
		return
	}

	var exportReq exportRequest
	if body, err := io.ReadAll(r.Body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	} else if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &exportReq); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
	}
	format, pageSize := exportReq.options()

	chapters, err := s.Repo.ListChapters(r.Context(), proj.ID)
	if err != nil {
		writeLookupError(w, err, "Chapter list")
		return
	}
	var exportPages []gen.PageExportItem
	var problems []exportProblem
	finishPreset := gen.FinishPreset(proj.FinishPreset)
	finishPresetExplicit := proj.FinishPresetExplicit
	for _, ch := range chapters {
		pages, err := s.Repo.ListPages(r.Context(), ch.ID)
		if err != nil {
			writeLookupError(w, err, "Page list")
			return
		}
		for _, pg := range pages {
			pageNumber := len(exportPages) + len(problems) + 1
			problem := func(code, message string) {
				problems = append(problems, exportProblem{PageNumber: pageNumber, PageID: pg.ID, Code: code, Message: fmt.Sprintf("Page %d %s", pageNumber, message)})
			}
			art, err := s.approvedArtwork(r.Context(), pg)
			if err != nil {
				writeLookupError(w, err, "Colour approval")
				return
			}
			if art == nil {
				problem("missing_approved_art", "has no approved, current artwork. Approve its line art, colour or painted page first.")
				continue
			}
			item := gen.PageExportItem{
				PageID:               &pg.ID,
				FinishPreset:         &finishPreset,
				FinishPresetExplicit: &finishPresetExplicit,
				PageNumber:           pageNumber,
				ImageURL:             art.url,
				OriginalImageURL:     art.originalURL,
				PanelCount:           optionalInt(len(pg.PanelIDs)),
			}
			provenanceURL, label := art.url, "its artwork"
			if !art.aiGenerated {
				aiGenerated := false
				item.AIGenerated = &aiGenerated
				if art.originalURL == nil {
					problem("missing_provenance", "has coloured artwork without the line art it was coloured from.")
					continue
				}
				provenanceURL, label = *art.originalURL, "its line art"
			}
			recorded, err := s.storedProvenance(r.Context(), provenanceURL)
			if err != nil {
				writeLookupError(w, err, "Provenance")
				return
			}
			if recorded == nil {
				problem("missing_provenance", fmt.Sprintf("has no recorded Meta provenance for %s. Regenerate it, then approve it again.", label))
				continue
			}
			if art.aiGenerated {
				item.ProvenanceMetadata = recorded
			} else {
				item.OriginalProvenanceMetadata = recorded
			}

			mode := gen.PageExportItemLetteringModeBaked
			var layer *gen.PageLetteringState
			stale := false
			if pg.LetteringMode == "overlay" && pg.LineArtImageURL == "" {
				mode = gen.PageExportItemLetteringModeOverlay
				layer, err = letteringState(pg)
				if err != nil {
					stale = true
				} else {
					current, sourceErr := s.sourceLetteringDocument(r.Context(), pg, nil, layer)
					if sourceErr != nil {
						writeLookupError(w, sourceErr, "Lettering source")
						return
					}
					before, _ := json.Marshal(layer.Input)
					after, _ := json.Marshal(current)
					stale = !bytes.Equal(before, after)
				}
			}
			item.LetteringMode = &mode
			item.Lettering = layer
			item.LetteringStale = &stale
			exportPages = append(exportPages, item)
		}
	}
	if len(exportPages)+len(problems) == 0 {
		problems = append(problems, exportProblem{Code: "no_pages", Message: "The project has no pages yet."})
	}
	if len(problems) > 0 {
		writeExportNotReady(w, problems)
		return
	}

	hashedUID := domain.HashUserID(userID)
	idempKey := uuid.NewString()

	job, _, err := s.JobService.CreateJob(r.Context(), jobs.CreateJobRequest{
		// PDF and PNG export spend nothing at the provider, so the global stop switch (which halts
		// paid generation only) does not apply to it.
		KillSwitchActive: false,
		ProjectID:        proj.ID,
		UserID:           userID,
		JobType:          "assemble_pdf",
		Stage:            "export",
		ModelID:          "pdf-assembler",
		IdempotencyKey:   idempKey,
		EstimatedCost:    0.0,
		HashedUserID:     hashedUID,
	})
	if err != nil {
		writeGenerationError(w, err)
		return
	}

	r = r.WithContext(context.WithoutCancel(r.Context()))
	pdfResp, err := dispatchGeneration(s, r.Context(), job, func(ctx context.Context) (*gen.AssemblePdfResponse, error) {
		return s.AIClient.AssemblePdf(ctx, gen.AssemblePdfRequest{
			JobID:                job.ID,
			HashedUserID:         hashedUID,
			ProjectID:            proj.ID,
			Title:                proj.Title,
			PageSize:             pageSize,
			Format:               exportFormatPtr(format),
			IncludeAIDisclosure:  true,
			FinishPreset:         &finishPreset,
			FinishPresetExplicit: &finishPresetExplicit,
			Pages:                exportPages,
		})
	})
	if err != nil {
		errMsg := safeGenerationMessage(err)
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeGenerationError(w, err)
		return
	}

	if !pdfResp.ProvenanceMetadataPreserved {
		// The specification forbids completing an export that cannot show its provenance.
		errMsg := "provenance could not be preserved"
		_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "failed", nil, &errMsg)
		writeJSON(w, http.StatusConflict, JSONResponse{
			Code:    "EXPORT_PROVENANCE_NOT_PRESERVED",
			Message: "The original Meta image files could not be verified and attached, so the export was not completed. Regenerate the affected pages and try again.",
		})
		return
	}

	exp := &domain.Export{
		ID:                          uuid.NewString(),
		ProjectID:                   proj.ID,
		FileURL:                     pdfResp.PdfURL,
		Format:                      format,
		Status:                      "completed",
		ProvenanceMetadataPreserved: true,
		CreatedAt:                   time.Now().UTC(),
	}
	if err := s.Repo.CreateExport(r.Context(), exp); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save export: %v", err))
		return
	}

	actualCost := s.aiActualCost(r.Context(), job.ID)
	_ = s.JobService.UpdateJobStatus(r.Context(), job.ID, "done", &actualCost, nil)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{"export": exp, "job_id": job.ID, "warnings": pdfResp.Warnings})
}

func exportFormatPtr(format string) *gen.AssemblePdfRequestFormat {
	f := gen.AssemblePdfRequestFormat(format)
	return &f
}
