package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"icomic/backend/gen"
)

func TestLetteringOwnershipLegacyAndOverrideReplacement(t *testing.T) {
	server, handler, repo, _, id := paintedPageFixture(t)
	ctx := context.Background()
	server.LetteringOverlayEnabled = true // Does not retroactively opt in an old page.
	request := func(method, path, body, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-User-ID", user)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	endpoint := "/api/v1/pages/" + id + "/lettering"
	if r := request("GET", endpoint, "", "owner"); r.Code != 200 || !strings.Contains(r.Body.String(), `"baked"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := request("PATCH", endpoint+"/utterances/u/override", `{"override":{}}`, "owner"); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	pg, _ := repo.GetPage(ctx, id)
	pg.LetteringMode = "overlay"
	state := gen.PageLetteringState{Version: "page-lettering-v1", Overrides: map[string]gen.LetteringOverride{}, Input: gen.LetteringDocument{
		Version: "lettering-v1", PageID: id, ArtMode: "text_free", PageAspect: 2.0 / 3,
		Panels: []gen.LetteringPanel{{PanelID: pg.PanelIDs[0], Order: 0, Rect: gen.LetteringUnitRect{Width: 1, Height: 1}, ReservedZones: []gen.LetteringUnitRect{{Width: 1, Height: .4}}, Utterances: []gen.LetteringUtterance{{UtteranceID: "u", Kind: "speech", Order: 0, Text: "Hello"}}}},
	}}
	pg.Lettering, _ = json.Marshal(state)
	if err := repo.CreatePage(ctx, pg); err != nil {
		t.Fatal(err)
	}
	panel, _ := repo.GetPanel(ctx, pg.PanelIDs[0])
	panel.Dialogue = "Hello"
	if err := repo.UpdatePanel(ctx, panel); err != nil {
		t.Fatal(err)
	}
	fake := server.AIClient.(*gen.FakeAIServiceClient)
	calls := 0
	fake.LayoutLetteringFunc = func(_ context.Context, doc gen.LetteringDocument) (*gen.LetteringLayout, error) {
		calls++
		return &gen.LetteringLayout{Version: "lettering-layout-v1", PageID: id, PageAspect: doc.PageAspect, Bubbles: []gen.LetteringBubble{}, Issues: []gen.LetteringIssue{}}, nil
	}
	if r := request("GET", endpoint, "", "owner"); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	calls = 0
	// Source IDs are stable panel IDs; use that actual ID for all test paths.
	actualID := pg.PanelIDs[0] + "-dialogue-0"
	for _, test := range []struct {
		method, path, body, user string
		want                     int
	}{
		{"GET", endpoint, "", "intruder", 403},
		{"PATCH", endpoint + "/utterances/u/override", `{"override":{"center":{"x":0.5,"y":0.2}}}`, "intruder", 403},
		{"PATCH", endpoint + "/utterances/missing/override", `{"override":{}}`, "owner", 404},
		{"PATCH", endpoint + "/utterances/u/override", `{}`, "owner", 400},
		{"PATCH", endpoint + "/utterances/u/override", `{"override":{"size_scale":-1}}`, "owner", 400},
		{"PATCH", endpoint + "/utterances/u/override", `{"override":{"center":{"x":0.5}}}`, "owner", 400},
		{"PATCH", endpoint + "/utterances/u/override", `{"override":{"center":null}}`, "owner", 400},
		{"PATCH", endpoint + "/utterances/u/override", `{"override":{"size_scale":9}}`, "owner", 400},
		{"PATCH", endpoint + "/zones", `{"panel_id":"p","reserved_zones":[{"x":0.8,"y":0,"width":0.5,"height":0.5}]}`, "owner", 400},
		{"PATCH", endpoint + "/utterances/u/override", `{"override":{"center":{"x":0.5,"y":0.2}}}`, "owner", 200},
	} {
		test.path = strings.ReplaceAll(test.path, "/utterances/u/", "/utterances/"+actualID+"/")
		r := request(test.method, test.path, test.body, test.user)
		if r.Code != test.want {
			t.Fatalf("%s %s: %d %s", test.method, test.path, r.Code, r.Body.String())
		}
	}
	saved, _ := repo.GetPage(ctx, id)
	var actual gen.PageLetteringState
	if err := json.Unmarshal(saved.Lettering, &actual); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || actual.Overrides[actualID].Center == nil || actual.Input.Panels[0].Utterances[0].Placement != nil {
		t.Fatal("source/override separation failed", actual)
	}
	if saved.DraftImageURL != pg.DraftImageURL || !saved.UpdatedAt.Equal(pg.UpdatedAt) {
		t.Fatal("lettering changed art or image approval freshness")
	}
	if r := request(http.MethodPatch, endpoint+"/utterances/"+actualID+"/override", `{"override":{}}`, "owner"); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
}

func TestEnablingLetteringDoesNotUpgradeExistingBookAllocations(t *testing.T) {
	server, handler, repo, _, id := paintedPageFixture(t)
	server.LetteringOverlayEnabled = true
	server.AIClient.(*gen.FakeAIServiceClient).LayoutLetteringFunc = func(context.Context, gen.LetteringDocument) (*gen.LetteringLayout, error) {
		t.Fatal("existing book was sent to overlay layout")
		return nil, nil
	}
	page, err := repo.GetPage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chapters/"+page.ChapterID+"/panels", nil)
	req.Header.Set("X-User-ID", "owner")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var response struct {
		Pages []struct {
			LetteringMode string `json:"lettering_mode"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Pages) == 0 {
		t.Fatal("no new pages")
	}
	for _, page := range response.Pages {
		if page.LetteringMode != "baked" {
			t.Fatal("legacy book upgraded", page)
		}
	}
}

func TestLetteringTextPreservesImageApprovalAndRejectsFailedLayout(t *testing.T) {
	server, handler, repo, _, id := paintedPageFixture(t)
	ctx := context.Background()
	pg, _ := repo.GetPage(ctx, id)
	pg.LetteringMode = "overlay"
	if err := repo.CreatePage(ctx, pg); err != nil {
		t.Fatal(err)
	}
	panel, _ := repo.GetPanel(ctx, pg.PanelIDs[0])
	panel.Dialogue = "Nia: Hello\nSailor: Land!"
	panel.Narration = "Before dawn."
	if err := repo.UpdatePanel(ctx, panel); err != nil {
		t.Fatal(err)
	}
	failLayout := false
	server.AIClient.(*gen.FakeAIServiceClient).LayoutLetteringFunc = func(_ context.Context, doc gen.LetteringDocument) (*gen.LetteringLayout, error) {
		if failLayout {
			return nil, errors.New("layout unavailable")
		}
		return &gen.LetteringLayout{Version: "lettering-layout-v1", PageID: id, PageAspect: doc.PageAspect, Bubbles: []gen.LetteringBubble{}, Issues: []gen.LetteringIssue{}}, nil
	}
	request := func(path, body, user string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", path, strings.NewReader(body))
		req.Header.Set("X-User-ID", user)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	endpoint := "/api/v1/pages/" + id + "/lettering/utterances/" + panel.ID
	for _, test := range []struct {
		suffix, body, user string
		code               int
	}{
		{"-dialogue-0/text", `{"text":"Follow the light!"}`, "intruder", 403},
		{"-missing/text", `{"text":"Hello"}`, "owner", 404},
		{"-dialogue-0/text", `{}`, "owner", 400},
		{"-dialogue-0/text", `{"text":null}`, "owner", 400},
		{"-dialogue-0/text", `{"text":"One\nTwo"}`, "owner", 400},
		{"-dialogue-0/text", `{"text":"Follow the light!"}`, "owner", 200},
		{"-narration/text", `{"text":""}`, "owner", 200},
		{"-narration/text", `{"text":"Daybreak."}`, "owner", 200},
	} {
		if r := request(endpoint+test.suffix, test.body, test.user); r.Code != test.code {
			t.Fatalf("%d: %s", r.Code, r.Body.String())
		}
	}
	saved, _ := repo.GetPage(ctx, id)
	if !reflect.DeepEqual(saved.DraftApprovedAt, pg.DraftApprovedAt) || !reflect.DeepEqual(saved.PaintedPageApprovedAt, pg.PaintedPageApprovedAt) || !saved.UpdatedAt.Equal(pg.UpdatedAt) || saved.DraftImageURL != pg.DraftImageURL || saved.PaintedPageImageURL != pg.PaintedPageImageURL {
		t.Fatal("text edit changed image state")
	}
	source, _ := repo.GetPanel(ctx, panel.ID)
	if source.Dialogue != "Nia: Follow the light!\nSailor: Land!" || source.Narration != "Daybreak." {
		t.Fatal(source)
	}
	state := gen.PageLetteringState{}
	if err := json.Unmarshal(saved.Lettering, &state); err != nil {
		t.Fatal(err)
	}
	if state.Input.Panels[0].Utterances[1].Text != "Follow the light!" {
		t.Fatal(state.Input)
	}
	failLayout = true
	if r := request(endpoint+"-dialogue-0/text", `{"text":"Rejected"}`, "owner"); r.Code != 502 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := repo.GetPanel(ctx, panel.ID)
	if after.Dialogue != source.Dialogue {
		t.Fatal("failed layout persisted source text")
	}
	pg.LetteringMode = "baked"
	if err := repo.CreatePage(ctx, pg); err != nil {
		t.Fatal(err)
	}
	if r := request(endpoint+"-dialogue-0/text", `{"text":"Baked"}`, "owner"); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
}
