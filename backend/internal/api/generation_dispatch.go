package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

// Queue operations whose committed envelopes the dispatcher can replay. Only debited
// (paid image and colour) jobs carry an envelope: a text or export job has no charge to strand.
const (
	opCharacterReference = "generate-character-reference"
	opDraftSketch        = "generate-draft-sketch"
	opLineArt            = "generate-line-art"
	opPaintedPage        = "generate-painted-page"
	opColourPalette      = "suggest-colour-palette"
)

const (
	dispatchBatch        = 2
	dispatchPollInterval = 5 * time.Second
	// A dispatch is retried this many times, counting the first, before its job is failed and refunded.
	maxDispatchAttempts = 5
)

// newEnvelope serializes the validated provider request. The job ID is stamped at commit.
// A request that cannot be serialized yields an envelope the commit rejects, never a silent skip.
func newEnvelope(operation string, request any) *db.GenerationEnvelope {
	raw, _ := json.Marshal(request)
	return &db.GenerationEnvelope{Operation: operation, Request: raw}
}

// saveError reports a failure to persist a generated result, with the public HTTP reply.
type saveError struct {
	status  int
	message string
	cause   error
}

func (e *saveError) Error() string { return e.message + ": " + e.cause.Error() }
func (e *saveError) Unwrap() error { return e.cause }

func newSaveError(status int, message string, cause error) error {
	return &saveError{status: status, message: message, cause: cause}
}

func writeSaveError(w http.ResponseWriter, err error) {
	var saveErr *saveError
	if errors.As(err, &saveErr) {
		writeError(w, saveErr.status, saveErr.message)
		return
	}
	writeError(w, http.StatusInternalServerError, "Failed to save generation result")
}

func (s *Server) finishDispatch(ctx context.Context, jobID string) {
	if s.Dispatches == nil {
		return
	}
	if err := s.Dispatches.FinishDispatch(ctx, jobID); err != nil {
		log.Printf("generation dispatch %s: finish failed: %v", jobID, err)
	}
}

// releaseDispatch hands a committed job whose result could not be saved back to the dispatcher.
func (s *Server) releaseDispatch(ctx context.Context, jobID string) {
	if s.Dispatches == nil {
		return
	}
	if err := s.Dispatches.ReleaseDispatch(ctx, jobID); err != nil {
		log.Printf("generation dispatch %s: release failed: %v", jobID, err)
	}
}

// completeGeneration marks a saved result done and retires its envelope.
func (s *Server) completeGeneration(ctx context.Context, job *domain.GenerationJob) error {
	actualCost := s.aiActualCost(ctx, job.ID)
	if err := s.JobService.UpdateJobStatus(ctx, job.ID, jobs.StatusDone, &actualCost, nil); err != nil {
		return newSaveError(http.StatusInternalServerError, err.Error(), err)
	}
	s.finishDispatch(ctx, job.ID)
	return nil
}

// Finalizers persist a saved provider result. They reload the entity and assign only the result,
// so the request path and the dispatcher's replay after a restart save exactly the same thing.

func (s *Server) finalizeCharacterReference(ctx context.Context, job *domain.GenerationJob, req gen.GenerateCharacterReferenceRequest, resp *gen.GenerateCharacterReferenceResponse) (*domain.Character, string, error) {
	ch, err := s.Repo.GetCharacter(ctx, req.CharacterID)
	if err != nil {
		return nil, "", newSaveError(http.StatusInternalServerError, "Failed to save character", err)
	}
	imgURL := s.ImageHosting.ResolveURL(resp.ImageURL)
	ch.ReferenceImageURL = imgURL
	ch.ReferenceImageJobID = &job.ID
	ch.UpdatedAt = nextCharacterActivity(ch)
	if err := s.Repo.UpdateCharacter(ctx, ch); err != nil {
		return nil, "", newSaveError(http.StatusInternalServerError, "Failed to save character", err)
	}
	return ch, imgURL, s.completeGeneration(ctx, job)
}

func (s *Server) finalizeDraftSketch(ctx context.Context, job *domain.GenerationJob, req gen.GenerateDraftSketchRequest, resp *gen.GenerateDraftSketchResponse) (string, error) {
	pg, err := s.Repo.GetPage(ctx, req.PageID)
	if err != nil {
		return "", newSaveError(http.StatusInternalServerError, "Failed to save page", err)
	}
	imgURL := s.ImageHosting.ResolveURL(resp.ImageURL)
	pg.DraftImageURL = imgURL
	pg.DraftImageJobID = &job.ID
	// Cascade: draft regeneration stales line art approval (SPEC 3.6, 3.7, ARCHITECTURE 5.3)
	domain.StaleOnDraftRegeneration(pg, time.Now().UTC())
	if err := s.Repo.UpdatePage(ctx, pg); err != nil {
		return "", newSaveError(http.StatusInternalServerError, "Failed to save page", err)
	}
	s.recordProvenance(ctx, req.ProjectID, imgURL, resp.ProvenanceMetadata)
	return imgURL, s.completeGeneration(ctx, job)
}

func (s *Server) finalizeLineArt(ctx context.Context, job *domain.GenerationJob, req gen.GenerateLineArtRequest, resp *gen.GenerateLineArtResponse) (string, error) {
	pg, err := s.Repo.GetPage(ctx, req.PageID)
	if err != nil {
		return "", newSaveError(http.StatusInternalServerError, "Failed to save page", err)
	}
	imgURL := s.ImageHosting.ResolveURL(resp.ImageURL)
	pg.LineArtImageURL = imgURL
	pg.LineArtImageJobID = &job.ID
	pg.ColourImageURL = ""
	pg.ColourImageUploadedAt = nil
	domain.StaleOnLineArtRegeneration(pg, time.Now().UTC())
	if err := s.Repo.UpdatePage(ctx, pg); err != nil {
		return "", newSaveError(http.StatusInternalServerError, "Failed to save page", err)
	}
	s.recordProvenance(ctx, req.ProjectID, imgURL, resp.ProvenanceMetadata)
	return imgURL, s.completeGeneration(ctx, job)
}

func (s *Server) finalizePaintedPage(ctx context.Context, job *domain.GenerationJob, req gen.GeneratePaintedPageRequest, resp *gen.GeneratePaintedPageResponse) (*domain.Page, string, error) {
	pg, err := s.Repo.GetPage(ctx, req.PageID)
	if err != nil {
		return nil, "", newSaveError(http.StatusInternalServerError, "Failed to save page", err)
	}
	imgURL := s.ImageHosting.ResolveURL(resp.ImageURL)
	pg.PaintedPageImageURL = imgURL
	pg.PaintedPageImageJobID = &job.ID
	pg.PaintedPageVariant = string(req.Variant)
	domain.StaleOnPaintedPageRegeneration(pg, time.Now().UTC())
	if err := s.Repo.UpdatePage(ctx, pg); err != nil {
		return nil, "", newSaveError(http.StatusInternalServerError, "Failed to save page", err)
	}
	s.recordProvenance(ctx, req.ProjectID, imgURL, resp.ProvenanceMetadata)
	if pg.LetteringMode == "overlay" {
		previous := append(json.RawMessage(nil), pg.Lettering...)
		if err := s.refreshLetteringAfterPaint(ctx, pg, paintedSubjects(resp)); err != nil {
			return nil, "", newSaveError(http.StatusBadGateway, "Failed to place lettering over painted art", err)
		}
		if err := s.Repo.UpdatePage(ctx, pg); err != nil {
			return nil, "", newSaveError(http.StatusInternalServerError, "Failed to save art-aware lettering", err)
		}
		// UpdatePage never writes the lettering column: persist the art-aware layout, with
		// its cached subject boxes, through the compare-and-save every other edit uses.
		if err := s.persistLettering(ctx, pg, previous); err != nil {
			return nil, "", err
		}
	}
	return pg, imgURL, s.completeGeneration(ctx, job)
}

func (s *Server) finalizeColourPalette(ctx context.Context, job *domain.GenerationJob, req gen.SuggestColourPaletteRequest, resp *gen.SuggestColourPaletteResponse) (map[string]interface{}, error) {
	palette := make(map[string]interface{})
	for _, item := range resp.Palette {
		palette[item.RegionName] = item.HexColor
	}
	if err := s.Repo.SaveColourPalette(ctx, &domain.ColourPalette{PageID: req.PageID, SuggestionJobID: &job.ID, Palette: palette}); err != nil {
		return nil, newSaveError(http.StatusInternalServerError, "Failed to save colour palette", err)
	}
	return palette, s.completeGeneration(ctx, job)
}

// generationDebit is the amount a job's creation debited, which a known failure refunds.
func (s *Server) generationDebit(job *domain.GenerationJob) float64 {
	switch job.JobType {
	case "character_reference", "draft_sketch", "final_line_art":
		return s.Pricing.ImagePriceUSD
	case "colour_suggestion":
		return s.Pricing.ColourEstimateUSD
	}
	return 0
}

// recordGenerationFailure stores a failed or uncertain dispatch outcome with the usage the AI
// service recorded, which is zero when it recorded none. An unknown provider outcome keeps its debit.
func (s *Server) recordGenerationFailure(ctx context.Context, job *domain.GenerationJob, cause error, cost float64) error {
	message := safeGenerationMessage(cause)
	status := jobs.StatusFailed
	if uncertainProviderOutcome(cause) {
		status = jobs.StatusUncertain
	}
	return s.JobService.UpdateJobStatus(ctx, job.ID, status, &cost, &message)
}

// transientDispatchError reports an error that says nothing about the provider outcome: the
// request never reached the AI service or its answer was lost. The queue key makes a retry exact.
func transientDispatchError(err error) bool {
	var serviceErr *gen.AIServiceError
	if !errors.As(err, &serviceErr) {
		return true
	}
	return serviceErr.Code == "AI_SERVICE_ERROR" && serviceErr.StatusCode >= http.StatusBadGateway
}

// replayDispatch resends a committed envelope. The AI queue returns the saved result, failure or
// uncertain outcome for an already known job ID without invoking a provider, and runs the job once otherwise.
func replayDispatch[Req, Resp any](s *Server, ctx context.Context, job *domain.GenerationJob, d db.PendingDispatch,
	call func(context.Context, Req) (*Resp, error), finalize func(context.Context, *domain.GenerationJob, Req, *Resp) error) error {
	var req Req
	if err := json.Unmarshal(d.Request, &req); err != nil {
		return s.failGeneration(ctx, job, fmt.Errorf("stored dispatch envelope is invalid: %w", err), s.generationDebit(job))
	}
	if err := s.JobService.StartJob(ctx, job.ID); err != nil {
		return err
	}
	resp, err := call(ctx, req)
	if err != nil {
		if transientDispatchError(err) && d.Attempts < maxDispatchAttempts {
			s.releaseDispatch(ctx, job.ID)
			return err
		}
		statusErr := s.recordGenerationFailure(ctx, job, err, s.aiActualCost(ctx, job.ID))
		return errors.Join(statusErr, s.failGeneration(ctx, job, err, s.generationDebit(job)))
	}
	if err := finalize(ctx, job, req, resp); err != nil {
		if d.Attempts < maxDispatchAttempts {
			s.releaseDispatch(ctx, job.ID)
			return err
		}
		return errors.Join(err, s.failGeneration(ctx, job, errors.New("generated result could not be saved"), s.generationDebit(job)))
	}
	return nil
}

func (s *Server) replayEnvelope(ctx context.Context, job *domain.GenerationJob, d db.PendingDispatch) error {
	switch d.Operation {
	case opCharacterReference:
		return replayDispatch(s, ctx, job, d, s.AIClient.GenerateCharacterReference,
			func(ctx context.Context, job *domain.GenerationJob, req gen.GenerateCharacterReferenceRequest, resp *gen.GenerateCharacterReferenceResponse) error {
				_, _, err := s.finalizeCharacterReference(ctx, job, req, resp)
				return err
			})
	case opDraftSketch:
		return replayDispatch(s, ctx, job, d, s.AIClient.GenerateDraftSketch,
			func(ctx context.Context, job *domain.GenerationJob, req gen.GenerateDraftSketchRequest, resp *gen.GenerateDraftSketchResponse) error {
				_, err := s.finalizeDraftSketch(ctx, job, req, resp)
				return err
			})
	case opLineArt:
		return replayDispatch(s, ctx, job, d, s.AIClient.GenerateLineArt,
			func(ctx context.Context, job *domain.GenerationJob, req gen.GenerateLineArtRequest, resp *gen.GenerateLineArtResponse) error {
				_, err := s.finalizeLineArt(ctx, job, req, resp)
				return err
			})
	case opPaintedPage:
		return replayDispatch(s, ctx, job, d, s.AIClient.GeneratePaintedPage,
			func(ctx context.Context, job *domain.GenerationJob, req gen.GeneratePaintedPageRequest, resp *gen.GeneratePaintedPageResponse) error {
				_, _, err := s.finalizePaintedPage(ctx, job, req, resp)
				return err
			})
	case opColourPalette:
		return replayDispatch(s, ctx, job, d, s.AIClient.SuggestColourPalette,
			func(ctx context.Context, job *domain.GenerationJob, req gen.SuggestColourPaletteRequest, resp *gen.SuggestColourPaletteResponse) error {
				_, err := s.finalizeColourPalette(ctx, job, req, resp)
				return err
			})
	}
	return s.failGeneration(ctx, job, fmt.Errorf("unknown dispatch operation %q", d.Operation), s.generationDebit(job))
}

func (s *Server) dispatchOne(ctx context.Context, d db.PendingDispatch) {
	job, err := s.JobService.GetJob(ctx, d.JobID)
	if err != nil {
		if errors.Is(err, jobs.ErrJobNotFound) {
			s.finishDispatch(ctx, d.JobID)
		} else {
			log.Printf("generation dispatch %s: load job failed: %v", d.JobID, err)
		}
		return
	}
	if job.Status != jobs.StatusPending && job.Status != jobs.StatusRunning {
		s.finishDispatch(ctx, d.JobID)
		return
	}
	if err := s.replayEnvelope(ctx, job, d); err != nil {
		log.Printf("generation dispatch %s (attempt %d): %v", d.JobID, d.Attempts, err)
	}
}

// writeReceipt answers a generation request as soon as its job, debit and dispatch envelope are
// committed, without waiting for the provider. The client follows the job by ID through
// GET /api/v1/generation-jobs/{id} and reads the finished entity once the job is done. A repeated
// request with the same Idempotency-Key returns the same receipt and never a second charge.
func (s *Server) writeReceipt(w http.ResponseWriter, job *domain.GenerationJob, entityType gen.GenerationReceiptEntityType, variant string) {
	s.kickDispatcher()
	receipt := gen.GenerationReceipt{
		Job: gen.GenerationJobRecord{
			ID: job.ID, EntityID: &job.EntityID, ProjectID: job.ProjectID, UserID: job.UserID,
			JobType: job.JobType, Stage: job.Stage, ModelID: job.ModelID, Status: gen.GenerationJobRecordStatus(job.Status),
			IdempotencyKey: job.IdempotencyKey, EstimatedCost: job.EstimatedCost, ActualCost: job.ActualCost,
			HashedUserID: job.HashedUserID, ErrorMessage: job.ErrorMessage, CreatedAt: job.CreatedAt, CompletedAt: job.CompletedAt,
		},
		JobID: job.ID, Status: gen.GenerationReceiptStatus(job.Status),
		EntityType: entityType, EntityID: job.EntityID, ProjectID: job.ProjectID,
	}
	if job.EntityID == "" {
		receipt.Job.EntityID = nil
	}
	if variant != "" {
		v := gen.GenerationReceiptVariant(variant)
		receipt.Variant = &v
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

// dispatchPool bounds in-flight envelope deliveries. Every claimer - the request that just
// committed a job, the polling dispatcher and the restart recovery - takes slots from it, so
// the bound holds however many of them run.
type dispatchPool struct{ slots chan struct{} }

func (s *Server) pool() *dispatchPool {
	s.poolOnce.Do(func() { s.dispatchSlots = &dispatchPool{slots: make(chan struct{}, dispatchBatch)} })
	return s.dispatchSlots
}

// pumpDispatches claims as many committed envelopes as there are free slots and starts each
// delivery in the background. It returns how many it started. recoverAll also takes envelopes
// leased to a previous process.
func (s *Server) pumpDispatches(ctx context.Context, recoverAll bool) (int, error) {
	if s.Dispatches == nil || s.AIClient == nil {
		return 0, nil
	}
	pool := s.pool()
	free := 0
	for free < dispatchBatch {
		select {
		case pool.slots <- struct{}{}:
			free++
			continue
		default:
		}
		break
	}
	if free == 0 {
		return 0, nil
	}
	claimed, err := s.Dispatches.ClaimDispatches(ctx, free, recoverAll)
	for i := len(claimed); i < free; i++ {
		<-pool.slots
	}
	if err != nil {
		return 0, err
	}
	for _, d := range claimed {
		s.dispatchWG.Add(1)
		go func() {
			defer s.dispatchWG.Done()
			defer func() {
				<-pool.slots
				// A freed slot may let a waiting envelope start without waiting for the next poll.
				s.kickDispatcher()
			}()
			s.dispatchOne(ctx, d)
		}()
	}
	return len(claimed), nil
}

// kickDispatcher starts delivery of freshly committed work at once. The creating request calls
// it after the commit and returns its receipt without waiting for the provider.
func (s *Server) kickDispatcher() {
	if s.Dispatches == nil || s.AIClient == nil {
		return
	}
	s.dispatchWG.Add(1)
	go func() {
		defer s.dispatchWG.Done()
		if _, err := s.pumpDispatches(context.Background(), false); err != nil {
			log.Printf("generation dispatcher: %v", err)
		}
	}()
}

// WaitDispatches blocks until every delivery this process started has finished. It exists for
// tests and orderly shutdown: pending envelopes remain durable either way.
func (s *Server) WaitDispatches() { s.dispatchWG.Wait() }

// DispatchPending claims one bounded batch of committed envelopes and drives each to a terminal
// state or back to the outbox, returning once they have finished. recoverAll also takes
// envelopes leased to a previous process.
func (s *Server) DispatchPending(ctx context.Context, recoverAll bool) (int, error) {
	n, err := s.pumpDispatches(ctx, recoverAll)
	s.WaitDispatches()
	return n, err
}

// RunDispatcher is the one bounded dispatcher. At most dispatchBatch envelopes run at once. The
// first pass takes everything a previous process left, because a restart ends its in-flight
// requests; later passes pick up envelopes whose retry delay or lease lapsed.
func (s *Server) RunDispatcher(ctx context.Context) {
	recoverAll := true
	for ctx.Err() == nil {
		claimed, err := s.pumpDispatches(ctx, recoverAll)
		if err != nil {
			log.Printf("generation dispatcher: %v", err)
		} else {
			recoverAll = false
		}
		if claimed > 0 {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(dispatchPollInterval):
		}
	}
}
