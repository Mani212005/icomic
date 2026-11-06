package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"icomic/backend/internal/db"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"icomic/backend/gen"
	"icomic/backend/internal/domain"
)

func letteringState(pg *domain.Page) (*gen.PageLetteringState, error) {
	var state gen.PageLetteringState
	if err := json.Unmarshal(pg.Lettering, &state); err != nil {
		return nil, err
	}
	if state.Version != "page-lettering-v1" {
		return nil, fmt.Errorf("unsupported lettering state")
	}
	return &state, nil
}

func letteringDocument(pg *domain.Page) *gen.LetteringDocument {
	if pg.LetteringMode != "overlay" || pg.LineArtImageURL != "" {
		return nil
	}
	state, err := letteringState(pg)
	if err != nil {
		return nil
	}
	return &state.Input
}

// Geometry is an explicit composition contract shared with the art prompt. Two
// columns, with a full-width final odd panel; one/two panels use a single column.
// Faces/action must be staged below the upper reserved band, not detected later.
func (s *Server) refreshLettering(ctx context.Context, pg *domain.Page) error {
	return s.refreshLetteringWithPanel(ctx, pg, nil)
}

func (s *Server) refreshLetteringWithPanel(ctx context.Context, pg *domain.Page, replacement *domain.Panel) error {
	if pg.LetteringMode != "overlay" {
		return nil
	}
	if pg.LineArtImageURL != "" {
		return fmt.Errorf("ink art cannot receive lettering")
	}
	old, _ := letteringState(pg)
	doc := gen.LetteringDocument{Version: "lettering-v1", ArtMode: "text_free", PageID: pg.ID, PageAspect: 2.0 / 3, Panels: []gen.LetteringPanel{}}
	columns := 2
	if len(pg.PanelIDs) <= 2 {
		columns = 1
	}
	rows := (len(pg.PanelIDs) + columns - 1) / columns
	for i, id := range pg.PanelIDs {
		p, err := s.Repo.GetPanel(ctx, id)
		if err != nil {
			return err
		}
		if replacement != nil && replacement.ID == id {
			p = replacement
		}
		row, column := i/columns, i%columns
		width := (0.94 - float64(columns-1)*0.015) / float64(columns)
		height := (0.96 - float64(rows-1)*0.01) / float64(rows)
		x := 0.03 + float64(column)*(width+0.015)
		if i == len(pg.PanelIDs)-1 && column == 0 {
			width = 0.94
		}
		panel := gen.LetteringPanel{PanelID: id, Order: i,
			Rect:          gen.LetteringUnitRect{X: x, Y: 0.02 + float64(row)*(height+0.01), Width: width, Height: height},
			ReservedZones: []gen.LetteringUnitRect{}, Utterances: []gen.LetteringUtterance{}}
		// Narration first, then each explicit dialogue line in source order. No
		// sentence splitting, inferred speakers or name-based ordering.
		if strings.TrimSpace(p.Narration) != "" {
			panel.Utterances = append(panel.Utterances, gen.LetteringUtterance{UtteranceID: id + "-narration", Order: 0, Kind: "narration", Text: p.Narration})
		}
		for lineIndex, line := range strings.Split(p.Dialogue, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			text := line
			var speaker *string
			if before, after, ok := strings.Cut(line, ":"); ok && len(before) <= 80 && strings.TrimSpace(before) != "" {
				name := strings.TrimSpace(before)
				speaker = &name
				text = strings.TrimSpace(after)
			}
			panel.Utterances = append(panel.Utterances, gen.LetteringUtterance{
				UtteranceID: fmt.Sprintf("%s-dialogue-%d", id, lineIndex), Order: len(panel.Utterances), Kind: "speech", Text: text, Speaker: speaker})
		}
		if len(panel.Utterances) > 0 {
			// Reserve 34-55% by demand, maintaining the 14pt print floor and
			// leaving the lower panel for faces. Crowding remains a review issue.
			characters := 0
			for _, u := range panel.Utterances {
				characters += len([]rune(u.Text))
			}
			band := min(0.55, 0.34+float64(max(0, characters-50))*0.001+float64(max(0, len(panel.Utterances)-1))*0.06)
			panel.ReservedZones = []gen.LetteringUnitRect{{X: 0.04, Y: 0.04, Width: 0.92, Height: band}}
		}
		if old != nil {
			if zones, ok := old.ZoneOverrides[id]; ok {
				panel.ReservedZones = zones
			}
		}
		doc.Panels = append(doc.Panels, panel)
	}
	overrides := map[string]gen.LetteringOverride{}
	if old != nil {
		overrides = old.Overrides
	}
	zones := map[string][]gen.LetteringUnitRect{}
	if old != nil {
		zones = old.ZoneOverrides
	}
	if old != nil {
		before, _ := json.Marshal(old.Input)
		after, _ := json.Marshal(doc)
		if bytes.Equal(before, after) {
			return nil
		}
	}
	return s.computeLettering(ctx, pg, doc, overrides, zones)
}

func (s *Server) computeLettering(ctx context.Context, pg *domain.Page, doc gen.LetteringDocument, overrides map[string]gen.LetteringOverride, zoneOverrides map[string][]gen.LetteringUnitRect) error {
	if zoneOverrides == nil {
		zoneOverrides = map[string][]gen.LetteringUnitRect{}
	}
	// Clone so manual placement never leaks into persisted source input.
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var effective gen.LetteringDocument
	if err = json.Unmarshal(data, &effective); err != nil {
		return err
	}
	retained := map[string]gen.LetteringOverride{}
	for i := range effective.Panels {
		for j := range effective.Panels[i].Utterances {
			u := &effective.Panels[i].Utterances[j]
			if override, ok := overrides[u.UtteranceID]; ok {
				u.Placement = &override
				retained[u.UtteranceID] = override
			}
		}
	}
	result, err := s.AIClient.LayoutLettering(ctx, effective)
	if err != nil {
		return err
	}
	pg.Lettering, err = json.Marshal(gen.PageLetteringState{Version: "page-lettering-v1", Input: doc, Overrides: retained, ZoneOverrides: zoneOverrides, Layout: *result})
	return err
}

func (s *Server) ownedLetteringPage(w http.ResponseWriter, r *http.Request) *domain.Page {
	pg, err := s.Repo.GetPage(r.Context(), r.PathValue("id"))
	if err != nil {
		writeLookupError(w, err, "Page")
		return nil
	}
	chapter, err := s.Repo.GetChapter(r.Context(), pg.ChapterID)
	if err != nil {
		writeLookupError(w, err, "Chapter")
		return nil
	}
	project, err := s.Repo.GetProject(r.Context(), chapter.ProjectID)
	if err != nil {
		writeLookupError(w, err, "Project")
		return nil
	}
	if project.UserID != s.getUserID(r) {
		writeError(w, http.StatusForbidden, "forbidden")
		return nil
	}
	return pg
}

func (s *Server) GetLettering(w http.ResponseWriter, r *http.Request) {
	pg := s.ownedLetteringPage(w, r)
	if pg == nil {
		return
	}
	if pg.LetteringMode != "overlay" || pg.LineArtImageURL != "" {
		writeJSON(w, http.StatusOK, gen.PageLetteringResponse{LetteringMode: "baked"})
		return
	}
	if !s.ensureLettering(w, r, pg) {
		return
	}
	state, err := letteringState(pg)
	if err != nil {
		writeError(w, 500, "Invalid lettering state")
		return
	}
	writeJSON(w, 200, gen.PageLetteringResponse{LetteringMode: "overlay", Lettering: state})
}

func decodeLetteringPatch(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, 400, "Invalid lettering patch")
		return false
	}
	return true
}

func (s *Server) PatchLetteringOverride(w http.ResponseWriter, r *http.Request) {
	pg := s.ownedLetteringPage(w, r)
	if pg == nil {
		return
	}
	if pg.LetteringMode != "overlay" || pg.LineArtImageURL != "" {
		writeError(w, 409, "Baked art cannot receive lettering")
		return
	}
	if !s.ensureLettering(w, r, pg) {
		return
	}

	var patch gen.PatchLetteringOverrideRequest
	if !decodeLetteringPatch(w, r, &patch) {
		return
	}
	o := &patch.Override

	if (o.SizeScale != nil && (*o.SizeScale < 1e-9 || *o.SizeScale > 8)) || (o.Rect != nil && (o.Rect.Width < 1e-9 || o.Rect.Height < 1e-9)) {
		writeError(w, 400, "Invalid lettering size")
		return
	}
	state, err := letteringState(pg)
	if err != nil {
		writeError(w, 500, "Invalid lettering state")
		return
	}
	id := r.PathValue("utterance_id")
	found := false
	for _, p := range state.Input.Panels {
		for _, u := range p.Utterances {
			if u.UtteranceID == id {
				found = true
			}
		}
	}
	if !found {
		writeError(w, 404, "Utterance not found")
		return
	}
	state.Overrides[id] = *o
	if o.Center == nil && o.Rect == nil && o.SizeScale == nil && o.TailAnchor == nil {
		delete(state.Overrides, id)
	}
	s.saveLetteringPatch(w, r, pg, state)
}

func (s *Server) PatchLetteringZones(w http.ResponseWriter, r *http.Request) {
	pg := s.ownedLetteringPage(w, r)
	if pg == nil {
		return
	}
	if pg.LetteringMode != "overlay" || pg.LineArtImageURL != "" {
		writeError(w, 409, "Baked art cannot receive lettering")
		return
	}
	if !s.ensureLettering(w, r, pg) {
		return
	}

	var patch gen.PatchLetteringZonesRequest
	if !decodeLetteringPatch(w, r, &patch) {
		return
	}
	if patch.ReservedZones == nil {
		writeError(w, 400, "reserved_zones is required")
		return
	}
	for _, z := range patch.ReservedZones {
		if z.X < 0 || z.Y < 0 || z.Width < 1e-9 || z.Height < 1e-9 || z.X+z.Width > 1 || z.Y+z.Height > 1 {
			writeError(w, 400, "Zone must fit its panel")
			return
		}
	}
	state, err := letteringState(pg)
	if err != nil {
		writeError(w, 500, "Invalid lettering state")
		return
	}
	found := false
	for i := range state.Input.Panels {
		if state.Input.Panels[i].PanelID == patch.PanelID {
			state.Input.Panels[i].ReservedZones = patch.ReservedZones
			found = true
		}
	}
	if !found {
		writeError(w, 404, "Panel not found")
		return
	}
	if state.ZoneOverrides == nil {
		state.ZoneOverrides = map[string][]gen.LetteringUnitRect{}
	}
	state.ZoneOverrides[patch.PanelID] = patch.ReservedZones
	s.saveLetteringPatch(w, r, pg, state)
}

func (s *Server) saveLetteringPatch(w http.ResponseWriter, r *http.Request, pg *domain.Page, state *gen.PageLetteringState) {
	previous := append(json.RawMessage(nil), pg.Lettering...)
	if err := s.computeLettering(r.Context(), pg, state.Input, state.Overrides, state.ZoneOverrides); err != nil {
		writeError(w, 502, "Failed to compute lettering")
		return
	}
	if !s.saveLettering(w, r, pg, previous) {
		return
	}
	s.GetLettering(w, r)
}

func (s *Server) saveLettering(w http.ResponseWriter, r *http.Request, pg *domain.Page, previous json.RawMessage) bool {
	if bytes.Equal(previous, pg.Lettering) {
		return true
	}
	if err := s.Repo.SavePageLettering(r.Context(), pg.ID, previous, pg.Lettering); err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, 409, "Lettering changed; reload and retry")
		} else {
			writeError(w, 500, "Failed to save lettering")
		}
		return false
	}
	return true
}
func (s *Server) ensureLettering(w http.ResponseWriter, r *http.Request, pg *domain.Page) bool {
	previous := append(json.RawMessage(nil), pg.Lettering...)
	if err := s.refreshLettering(r.Context(), pg); err != nil {
		writeError(w, 502, "Failed to refresh lettering")
		return false
	}
	return s.saveLettering(w, r, pg, previous)
}

// PatchLetteringText edits a source utterance without invalidating text-free art.
// Compute before writing; persist source and layer in one compare-and-save.
func (s *Server) PatchLetteringText(w http.ResponseWriter, r *http.Request) {
	pg := s.ownedLetteringPage(w, r)
	if pg == nil {
		return
	}
	if pg.LetteringMode != "overlay" || pg.LineArtImageURL != "" {
		writeError(w, 409, "Baked art cannot receive lettering")
		return
	}
	var patch gen.PatchLetteringTextRequest
	if !decodeLetteringPatch(w, r, &patch) {
		return
	}
	if utf8.RuneCountInString(patch.Text) > 8000 {
		writeError(w, 400, "Text is too long")
		return
	}
	if !s.ensureLettering(w, r, pg) {
		return
	}
	state, err := letteringState(pg)
	if err != nil {
		writeError(w, 500, "Invalid lettering state")
		return
	}
	id := r.PathValue("utterance_id")
	for _, panel := range state.Input.Panels {
		// A cleared narration is absent from the draw list but its source slot
		// remains editable while the inline editor is still open.
		if id == panel.PanelID+"-narration" {
			found := false
			for _, u := range panel.Utterances {
				found = found || u.UtteranceID == id
			}
			if !found {
				panel.Utterances = append(panel.Utterances, gen.LetteringUtterance{UtteranceID: id, Kind: "narration"})
			}
		}
		for _, utterance := range panel.Utterances {
			if utterance.UtteranceID != id {
				continue
			}
			original, err := s.Repo.GetPanel(r.Context(), panel.PanelID)
			if err != nil {
				writeLookupError(w, err, "Panel")
				return
			}
			next := *original
			if utterance.Kind == "narration" {
				next.Narration = patch.Text
			} else {
				if strings.ContainsAny(patch.Text, "\r\n") {
					writeError(w, 400, "Dialogue must be one line")
					return
				}
				index, err := strconv.Atoi(strings.TrimPrefix(id, panel.PanelID+"-dialogue-"))
				lines := strings.Split(next.Dialogue, "\n")
				if err != nil || index < 0 || index >= len(lines) {
					writeError(w, 409, "Source text changed")
					return
				}
				lines[index] = patch.Text
				if utterance.Speaker != nil {
					lines[index] = *utterance.Speaker + ": " + patch.Text
				}
				next.Dialogue = strings.Join(lines, "\n")
			}
			next.UpdatedAt = time.Now().UTC()
			previous := append(json.RawMessage(nil), pg.Lettering...)
			if err := s.refreshLetteringWithPanel(r.Context(), pg, &next); err != nil {
				writeError(w, 502, "Failed to compute lettering")
				return
			}
			if err := s.Repo.SaveLetteringText(r.Context(), pg.ID, original, &next, previous, pg.Lettering); err != nil {
				if errors.Is(err, db.ErrConflict) {
					writeError(w, 409, "Lettering changed; reload and retry")
				} else {
					writeError(w, 500, "Failed to save lettering")
				}
				return
			}
			result, _ := letteringState(pg) // Computed and validated before the atomic commit.
			writeJSON(w, 200, gen.PageLetteringResponse{LetteringMode: "overlay", Lettering: result})
			return
		}
	}
	writeError(w, 404, "Utterance not found")
}
