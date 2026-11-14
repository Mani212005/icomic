package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icomic/backend/internal/domain"
)

func (f *allocationFixture) panelNumbers(t *testing.T) []string {
	t.Helper()
	panels, err := f.repo.ListPanels(nil, f.chapter.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i, p := range panels {
		if p.PanelNumber != i+1 {
			t.Fatalf("panel numbers must be 1..N in order, got %d at %d", p.PanelNumber, i+1)
		}
		ids = append(ids, p.ID)
	}
	return ids
}

func (f *allocationFixture) layoutBody(t *testing.T, rec *httptest.ResponseRecorder, want int) map[string]json.RawMessage {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body
}

func TestAddPanelEndpointPlacesPanelStalesPageAndNumbersInOrder(t *testing.T) {
	f := newAllocationFixture(t)
	p1, p2, p3 := f.panels[0].ID, f.panels[1].ID, f.panels[2].ID
	path := "/chapters/" + f.chapter.ID + "/panels/add"

	body := f.layoutBody(t, f.do(t, "POST", path, `{"page_id":"`+f.pages[1].ID+`","position":0,"scene_description":"A gull lands","dialogue":"Hello"}`), http.StatusCreated)
	var added domain.Panel
	_ = json.Unmarshal(body["panel"], &added)
	if added.ID == "" || added.SceneDescription != "A gull lands" || added.Dialogue != "Hello" || added.Status != "ready" || added.PanelNumber != 3 {
		t.Fatalf("added panel: %+v", added)
	}
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p1, p2}, {added.ID, p3}}) {
		t.Fatalf("allocation: %v", got)
	}
	if got := f.panelNumbers(t); strings.Join(got, ",") != strings.Join([]string{p1, p2, added.ID, p3}, ",") {
		t.Fatalf("reading order: %v", got)
	}
	hit, _ := f.repo.GetPage(nil, f.pages[1].ID)
	other, _ := f.repo.GetPage(nil, f.pages[0].ID)
	if hit.DraftApprovedAt != nil || hit.LineArtApprovedAt != nil {
		t.Fatal("the page that received the panel must be staled")
	}
	if other.DraftApprovedAt == nil {
		t.Fatal("an unrelated page was staled")
	}
	if string(body["changed_page_ids"]) != `["`+f.pages[1].ID+`"]` || string(body["removed_page_ids"]) != `[]` {
		t.Fatalf("response: %s", body)
	}

	// No page: a new page at the end, in the project's lettering mode (baked here).
	body = f.layoutBody(t, f.do(t, "POST", path, `{"scene_description":"The lighthouse"}`), http.StatusCreated)
	pages, _ := f.repo.ListPages(nil, f.chapter.ID)
	if len(pages) != 3 || pages[2].PageNumber != 3 || len(pages[2].PanelIDs) != 1 || pages[2].LetteringMode != "baked" {
		t.Fatalf("pages: %+v", pages)
	}
	f.panelNumbers(t)
}

func TestAddPanelEndpointRejectsBadRequestsWithoutWriting(t *testing.T) {
	f := newAllocationFixture(t)
	f.server.MaxPanelsPerPage = 2
	before := f.allocation(t)
	path := "/chapters/" + f.chapter.ID + "/panels/add"
	for name, body := range map[string]string{
		"blank scene":       `{"page_id":"` + f.pages[1].ID + `","scene_description":"   "}`,
		"no scene":          `{"page_id":"` + f.pages[1].ID + `"}`,
		"unknown field":     `{"scene_description":"x","chapter_id":"` + f.chapter.ID + `"}`,
		"bad page id":       `{"scene_description":"x","page_id":"nope"}`,
		"negative position": `{"scene_description":"x","page_id":"` + f.pages[1].ID + `","position":-1}`,
		"null page":         `{"scene_description":"x","page_id":null}`,
		"full page":         `{"scene_description":"x","page_id":"` + f.pages[0].ID + `"}`,
		"unknown page":      `{"scene_description":"x","page_id":"00000000-0000-0000-0000-000000000009"}`,
		"too long scene":    `{"scene_description":"` + strings.Repeat("x", 16385) + `"}`,
		"not an object":     `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := f.do(t, "POST", path, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP %d %s", rec.Code, rec.Body.String())
			}
			if got := f.allocation(t); !equalAllocations(got, before) {
				t.Fatalf("allocation changed: %v", got)
			}
			if len(f.panelNumbers(t)) != 3 {
				t.Fatal("a rejected add stored a panel")
			}
		})
	}
	if rec := f.do(t, "POST", "/chapters/00000000-0000-0000-0000-000000000009/panels/add", `{"scene_description":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown chapter: HTTP %d", rec.Code)
	}
}

func TestPanelStructureEndpointsRequireTheOwner(t *testing.T) {
	f := newAllocationFixture(t)
	before := f.allocation(t)
	for method, tc := range map[string]struct{ path, body string }{
		"POST":   {"/chapters/" + f.chapter.ID + "/panels/add", `{"scene_description":"x"}`},
		"DELETE": {"/chapters/" + f.chapter.ID + "/panels/" + f.panels[0].ID, ""},
	} {
		path := tc.path
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(tc.body))
		req.Header.Set("X-User-ID", "user-2")
		f.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: HTTP %d", method, path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/chapters/"+f.chapter.ID+"/panels/move", strings.NewReader(`{"panel_id":"`+f.panels[0].ID+`","page_id":"`+f.pages[1].ID+`"}`))
	req.Header.Set("X-User-ID", "user-2")
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("move: HTTP %d", rec.Code)
	}
	if got := f.allocation(t); !equalAllocations(got, before) {
		t.Fatalf("a refused edit changed the allocation: %v", got)
	}
}

func TestMovePanelEndpointReordersAndMovesAndStalesBothPages(t *testing.T) {
	f := newAllocationFixture(t)
	p1, p2, p3 := f.panels[0].ID, f.panels[1].ID, f.panels[2].ID
	path := "/chapters/" + f.chapter.ID + "/panels/move"

	f.layoutBody(t, f.do(t, "POST", path, `{"panel_id":"`+p2+`","page_id":"`+f.pages[0].ID+`","position":0}`), http.StatusOK)
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p2, p1}, {p3}}) {
		t.Fatalf("after reorder: %v", got)
	}
	if untouched, _ := f.repo.GetPage(nil, f.pages[1].ID); untouched.DraftApprovedAt == nil {
		t.Fatal("reordering one page staled another")
	}

	f.layoutBody(t, f.do(t, "POST", path, `{"panel_id":"`+p1+`","page_id":"`+f.pages[1].ID+`"}`), http.StatusOK)
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p2}, {p3, p1}}) {
		t.Fatalf("after move: %v", got)
	}
	for _, id := range []string{f.pages[0].ID, f.pages[1].ID} {
		if pg, _ := f.repo.GetPage(nil, id); pg.DraftApprovedAt != nil || pg.LineArtApprovedAt != nil {
			t.Fatalf("a move must stale both pages: %+v", pg)
		}
	}
	f.panelNumbers(t)

	before := f.allocation(t)
	for name, body := range map[string]string{
		"missing page":      `{"panel_id":"` + p1 + `"}`,
		"missing panel":     `{"page_id":"` + f.pages[1].ID + `"}`,
		"empties source":    `{"panel_id":"` + p2 + `","page_id":"` + f.pages[1].ID + `"}`,
		"foreign panel":     `{"panel_id":"` + f.panels[3].ID + `","page_id":"` + f.pages[1].ID + `"}`,
		"unknown field":     `{"panel_id":"` + p1 + `","page_id":"` + f.pages[0].ID + `","x":1}`,
		"bad position type": `{"panel_id":"` + p1 + `","page_id":"` + f.pages[0].ID + `","position":"first"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rec := f.do(t, "POST", path, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("HTTP %d %s", rec.Code, rec.Body.String())
			}
			if got := f.allocation(t); !equalAllocations(got, before) {
				t.Fatalf("allocation changed: %v", got)
			}
		})
	}
}

func TestDeletePanelEndpointClosesGapsAndNeverDeletesArt(t *testing.T) {
	f := newAllocationFixture(t)
	p1, p2, p3 := f.panels[0].ID, f.panels[1].ID, f.panels[2].ID
	del := func(id string) *httptest.ResponseRecorder {
		return f.do(t, "DELETE", "/chapters/"+f.chapter.ID+"/panels/"+id, "")
	}

	body := f.layoutBody(t, del(p1), http.StatusOK)
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p2}, {p3}}) {
		t.Fatalf("allocation: %v", got)
	}
	if hit, _ := f.repo.GetPage(nil, f.pages[0].ID); hit.DraftApprovedAt != nil {
		t.Fatal("the page that lost the panel must be staled")
	}
	if string(body["changed_page_ids"]) != `["`+f.pages[0].ID+`"]` {
		t.Fatalf("response: %s", body)
	}
	if _, err := f.repo.GetPanel(nil, p1); err == nil {
		t.Fatal("the panel must be gone")
	}

	// The last panel of a page with art is kept: nothing is deleted.
	art, _ := f.repo.GetPage(nil, f.pages[1].ID)
	art.DraftImageURL = "/media/draft.png"
	if err := f.repo.UpdatePage(nil, art); err != nil {
		t.Fatal(err)
	}
	if rec := del(p3); rec.Code != http.StatusConflict {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body.String())
	}
	if got := f.allocation(t); !equalAllocations(got, [][]string{{p2}, {p3}}) {
		t.Fatalf("a refused delete changed the allocation: %v", got)
	}

	// Without art the emptied page goes and the later page numbers close the gap.
	body = f.layoutBody(t, del(p2), http.StatusOK)
	pages, _ := f.repo.ListPages(nil, f.chapter.ID)
	if len(pages) != 1 || pages[0].PageNumber != 1 || string(body["removed_page_ids"]) != `["`+f.pages[0].ID+`"]` {
		t.Fatalf("pages: %+v response %s", pages, body)
	}
	f.panelNumbers(t)

	if rec := del(f.panels[3].ID); rec.Code != http.StatusNotFound {
		t.Fatalf("another chapter's panel: HTTP %d", rec.Code)
	}
	if rec := del("not-a-uuid"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: HTTP %d", rec.Code)
	}
}

func TestAddPanelOnOverlayProjectStartsAnOverlayPageWithLettering(t *testing.T) {
	f := newAllocationFixture(t)
	f.server.LetteringOverlayEnabled = true
	f.project.LetteringMode = "overlay"
	if err := f.repo.UpdateProject(nil, f.project); err != nil {
		t.Fatal(err)
	}
	f.layoutBody(t, f.do(t, "POST", "/chapters/"+f.chapter.ID+"/panels/add", `{"scene_description":"The lighthouse","dialogue":"Look!"}`), http.StatusCreated)
	pages, _ := f.repo.ListPages(nil, f.chapter.ID)
	last := pages[len(pages)-1]
	if len(pages) != 3 || last.LetteringMode != "overlay" || len(last.Lettering) == 0 {
		t.Fatalf("a page added to an overlay project must be overlay with a stored layout: %+v", last)
	}
}
