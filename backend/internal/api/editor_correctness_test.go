package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"icomic/backend/gen"
	"icomic/backend/internal/domain"
)

func TestCharacterEditMergesVisualAttributes(t *testing.T) {
	f := newCoherenceFixture(t)
	ch := &domain.Character{
		ProjectID:         f.project.ID,
		Name:              "Maya",
		ReferenceImageURL: "https://storage.example/maya.png",
		VisualAttributes: map[string]interface{}{
			"age_range": "20-25", "hair_style": "teal pixie", "appearance": "athletic", "reference_role": "main",
			"personality": "curious",
		},
	}
	if err := f.repo.CreateCharacter(nil, ch); err != nil {
		t.Fatal(err)
	}

	rec := f.do(t, "PATCH", "/characters/"+ch.ID, `{"visual_attributes":{"appearance":"tall","clothing":"red coat"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit character: HTTP %d %s", rec.Code, rec.Body.String())
	}
	saved, err := f.repo.GetCharacter(nil, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"age_range": "20-25", "hair_style": "teal pixie", "appearance": "tall", "reference_role": "main",
		"personality": "curious", "clothing": "red coat",
	}
	if !reflect.DeepEqual(saved.VisualAttributes, want) {
		t.Fatalf("edit must change only the named attributes\n got: %v\nwant: %v", saved.VisualAttributes, want)
	}

	// A null value removes one attribute and leaves the rest alone.
	if rec := f.do(t, "PATCH", "/characters/"+ch.ID, `{"visual_attributes":{"personality":null}}`); rec.Code != http.StatusOK {
		t.Fatalf("remove attribute: HTTP %d %s", rec.Code, rec.Body.String())
	}
	saved, _ = f.repo.GetCharacter(nil, ch.ID)
	delete(want, "personality")
	if !reflect.DeepEqual(saved.VisualAttributes, want) {
		t.Fatalf("null must remove only its key\n got: %v\nwant: %v", saved.VisualAttributes, want)
	}

	// A patch without visual_attributes leaves them untouched.
	if rec := f.do(t, "PATCH", "/characters/"+ch.ID, `{"name":"Maya Chen"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: HTTP %d %s", rec.Code, rec.Body.String())
	}
	saved, _ = f.repo.GetCharacter(nil, ch.ID)
	if saved.Name != "Maya Chen" || !reflect.DeepEqual(saved.VisualAttributes, want) {
		t.Fatalf("rename changed attributes: %+v", saved)
	}
}

func TestOutlineDetailIsStoredEditedAndFedToPanelGeneration(t *testing.T) {
	f := newCoherenceFixture(t)
	hero := f.character(t, "https://storage.example/hero.png")
	if rec := f.do(t, "POST", "/characters/"+hero.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve character: HTTP %d %s", rec.Code, rec.Body.String())
	}
	f.ai.GenerateChapterOutlineFunc = func(_ context.Context, req gen.GenerateChapterOutlineRequest) (*gen.GenerateChapterOutlineResponse, error) {
		return &gen.GenerateChapterOutlineResponse{JobID: req.JobID, Chapters: []gen.ChapterOutlineItem{{
			ChapterNumber: 1, Title: "The bird", Summary: "A bird leads the way",
			ImportantEvents: []string{"Hero finds the bird", "The storm arrives"},
			CharacterNames:  []string{"Hero", "Bird"},
			Ending:          "The lighthouse door opens",
		}}}, nil
	}
	if rec := f.do(t, "POST", "/projects/"+f.project.ID+"/chapters", ""); rec.Code != http.StatusOK {
		t.Fatalf("outline: HTTP %d %s", rec.Code, rec.Body.String())
	}

	chapter := f.onlyChapter(t)
	if !reflect.DeepEqual(chapter.ImportantEvents, []string{"Hero finds the bird", "The storm arrives"}) ||
		!reflect.DeepEqual(chapter.CharacterNames, []string{"Hero", "Bird"}) || chapter.Ending != "The lighthouse door opens" {
		t.Fatalf("outline detail was not persisted: %+v", chapter)
	}
	listed := f.do(t, "GET", "/projects/"+f.project.ID+"/chapters", "")
	var body struct {
		Chapters []map[string]json.RawMessage `json:"chapters"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &body); err != nil || len(body.Chapters) != 1 {
		t.Fatalf("list chapters: %v %s", err, listed.Body.String())
	}
	for _, key := range []string{"important_events", "character_names", "ending"} {
		if _, ok := body.Chapters[0][key]; !ok {
			t.Fatalf("chapter response lacks %q: %s", key, listed.Body.String())
		}
	}

	// Approving, then editing an event, makes the outline pending again and keeps the other fields.
	if rec := f.do(t, "POST", "/chapters/"+chapter.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve outline: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if f.onlyChapter(t).Status != "approved" {
		t.Fatal("outline was not approved")
	}
	edit := `{"important_events":["Hero finds the bird","The storm arrives early","They reach the door"],"ending":"The door is locked"}`
	if rec := f.do(t, "PATCH", "/chapters/"+chapter.ID, edit); rec.Code != http.StatusOK {
		t.Fatalf("edit outline: HTTP %d %s", rec.Code, rec.Body.String())
	}
	edited := f.onlyChapter(t)
	if edited.Status != "pending" {
		t.Fatalf("an edited outline must need approving again, status = %q", edited.Status)
	}
	if !reflect.DeepEqual(edited.CharacterNames, []string{"Hero", "Bird"}) || edited.Summary != "A bird leads the way" || edited.Ending != "The door is locked" {
		t.Fatalf("edit changed fields it did not name: %+v", edited)
	}
	// Saving unchanged text does not discard an approval.
	if rec := f.do(t, "POST", "/chapters/"+chapter.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("reapprove outline: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "PATCH", "/chapters/"+chapter.ID, `{"ending":"The door is locked","title":"The bird"}`); rec.Code != http.StatusOK {
		t.Fatalf("resave outline: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if status := f.onlyChapter(t).Status; status != "approved" {
		t.Fatalf("resaving identical text must keep the approval, status = %q", status)
	}
	// Moving an unchanged chapter does not matter, but any change in the lists does.
	if rec := f.do(t, "PATCH", "/chapters/"+chapter.ID, `{"character_names":["Hero"]}`); rec.Code != http.StatusOK {
		t.Fatalf("edit cast: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if status := f.onlyChapter(t).Status; status != "pending" {
		t.Fatalf("a cast change must need approving again, status = %q", status)
	}

	var sent gen.GeneratePanelTextRequest
	f.ai.GeneratePanelTextFunc = func(_ context.Context, req gen.GeneratePanelTextRequest) (*gen.GeneratePanelTextResponse, error) {
		sent = req
		var panels []gen.PanelTextItem
		for i := 1; i <= *req.PanelCount; i++ {
			panels = append(panels, gen.PanelTextItem{PanelNumber: i, SceneDescription: "scene"})
		}
		return &gen.GeneratePanelTextResponse{JobID: req.JobID, Panels: panels}, nil
	}
	if rec := f.do(t, "POST", "/chapters/"+chapter.ID+"/approve", ""); rec.Code != http.StatusOK {
		t.Fatalf("approve edited outline: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "POST", "/chapters/"+chapter.ID+"/panels", ""); rec.Code != http.StatusOK {
		t.Fatalf("panels: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if sent.ImportantEvents == nil || !reflect.DeepEqual(*sent.ImportantEvents, []string{"Hero finds the bird", "The storm arrives early", "They reach the door"}) {
		t.Fatalf("panel generation did not receive the edited events: %+v", sent.ImportantEvents)
	}
	if sent.CharacterNames == nil || !reflect.DeepEqual(*sent.CharacterNames, []string{"Hero"}) {
		t.Fatalf("panel generation did not receive the chapter cast: %+v", sent.CharacterNames)
	}
	if sent.Ending == nil || *sent.Ending != "The door is locked" {
		t.Fatalf("panel generation did not receive the edited ending: %+v", sent.Ending)
	}
}

func TestChapterOutlineFieldsAreValidated(t *testing.T) {
	f := newCoherenceFixture(t)
	tooMany := `["` + strings.Repeat(`e","`, 100) + `e"]`
	for name, body := range map[string]string{
		"events not a list":   `{"important_events":"one"}`,
		"events not strings":  `{"important_events":[1,2]}`,
		"events null":         `{"important_events":null}`,
		"too many events":     `{"important_events":` + tooMany + `}`,
		"cast not a list":     `{"character_names":{"a":1}}`,
		"ending not a string": `{"ending":["x"]}`,
		"oversized ending":    `{"ending":"` + strings.Repeat("x", 16385) + `"}`,
	} {
		if rec := f.do(t, "PATCH", "/chapters/"+f.chapter.ID, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP %d %s, want 400", name, rec.Code, rec.Body.String())
		}
	}
	if rec := f.do(t, "PATCH", "/chapters/"+f.chapter.ID, `{"important_events":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("clearing events: HTTP %d %s", rec.Code, rec.Body.String())
	}
}

func (f *coherenceFixture) onlyChapter(t *testing.T) *domain.Chapter {
	t.Helper()
	chapters, err := f.repo.ListChapters(nil, f.project.ID)
	if err != nil || len(chapters) != 1 {
		t.Fatalf("expected one chapter, got %d (%v)", len(chapters), err)
	}
	return chapters[0]
}

func TestAnalyzeReturnsWarningsIncludingChapterCountSuitability(t *testing.T) {
	analyze := func(t *testing.T, script string, chapters int, aiWarnings []string) []string {
		t.Helper()
		f := newCoherenceFixture(t)
		f.project.OriginalScript = script
		f.project.ChapterCount = chapters
		if err := f.repo.UpdateProject(nil, f.project); err != nil {
			t.Fatal(err)
		}
		f.ai.AnalyzeScriptFunc = func(_ context.Context, req gen.AnalyzeScriptRequest) (*gen.AnalyzeScriptResponse, error) {
			return &gen.AnalyzeScriptResponse{
				JobID:             req.JobID,
				CharacterProfiles: []gen.CharacterProfile{{Name: "Maya", Description: "A hero"}},
				WordCount:         len(strings.Fields(script)),
				Warnings:          aiWarnings,
			}, nil
		}
		rec := f.do(t, "POST", "/projects/"+f.project.ID+"/analyze", `{}`)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("analyze: HTTP %d %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Warnings *[]string `json:"warnings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Warnings == nil {
			t.Fatalf("analyze response must carry a warnings list: %v %s", err, rec.Body.String())
		}
		return *body.Warnings
	}
	words := func(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }

	if got := analyze(t, words(900), 3, nil); len(got) != 0 {
		t.Fatalf("a suitable chapter count must not warn, got %v", got)
	}
	if got := analyze(t, words(900), 3, []string{}); got == nil || len(got) != 0 {
		t.Fatalf("warnings must be an empty list, not null: %v", got)
	}
	got := analyze(t, words(900), 40, nil)
	if len(got) != 1 || !strings.Contains(got[0], "40 chapters") || !strings.Contains(got[0], "less than one page") {
		t.Fatalf("too many chapters must warn once, got %v", got)
	}
	got = analyze(t, words(3000), 1, nil)
	if len(got) != 1 || !strings.Contains(got[0], "1 chapter is few") || !strings.Contains(got[0], "About 4 would suit it better") {
		t.Fatalf("too few chapters must warn once, got %v", got)
	}
	got = analyze(t, words(900), 40, []string{"Script is very short; fewer panels will be generated."})
	if len(got) != 2 || got[0] != "Script is very short; fewer panels will be generated." || !strings.Contains(got[1], "40 chapters") {
		t.Fatalf("service warnings come first, then the chapter-count warning, got %v", got)
	}
}
