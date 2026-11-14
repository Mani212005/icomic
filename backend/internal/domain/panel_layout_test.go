package domain

import (
	"errors"
	"reflect"
	"testing"
)

func layoutPages(shape ...[]string) []*Page {
	var pages []*Page
	for i, ids := range shape {
		pages = append(pages, &Page{ID: "pg" + string(rune('1'+i)), PageNumber: i + 1, PanelIDs: ids})
	}
	return pages
}

func layoutIDs(edit *PanelLayoutEdit) [][]string {
	var out [][]string
	for _, pg := range edit.Pages {
		out = append(out, pg.PanelIDs)
	}
	return out
}

func TestPlacePanelClampsPositionAndNeverDuplicates(t *testing.T) {
	for name, tc := range map[string]struct {
		ids      []string
		position int
		want     []string
	}{
		"front":              {[]string{"a", "b"}, 0, []string{"x", "a", "b"}},
		"middle":             {[]string{"a", "b"}, 1, []string{"a", "x", "b"}},
		"end":                {[]string{"a", "b"}, 2, []string{"a", "b", "x"}},
		"omitted means last": {[]string{"a", "b"}, -1, []string{"a", "b", "x"}},
		"past the end":       {[]string{"a", "b"}, 99, []string{"a", "b", "x"}},
		"already there":      {[]string{"x", "a"}, 1, []string{"a", "x"}},
		"empty":              {nil, 0, []string{"x"}},
	} {
		if got := placePanel(tc.ids, "x", tc.position); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestPlanPanelAddMoveDeleteReportWhatChanges(t *testing.T) {
	pages := layoutPages([]string{"a", "b"}, []string{"c"})
	all := map[string]bool{"a": true, "b": true, "c": true}

	edit, err := PlanPanelAdd(pages, "n", "pg2", "", 0, 5)
	if err != nil || !reflect.DeepEqual(layoutIDs(edit), [][]string{{"a", "b"}, {"n", "c"}}) || !reflect.DeepEqual(edit.Changed, []string{"pg2"}) {
		t.Fatalf("add: %+v %v", edit, err)
	}
	if got := edit.PanelNumbers(); !reflect.DeepEqual(got, map[string]int{"a": 1, "b": 2, "n": 3, "c": 4}) {
		t.Fatalf("numbers follow reading order: %v", got)
	}
	edit, err = PlanPanelAdd(pages, "n", "", "pgNew", 0, 5)
	if err != nil || edit.Created != "pgNew" || len(edit.Pages) != 3 || !reflect.DeepEqual(edit.Changed, []string{"pgNew"}) {
		t.Fatalf("add on a new page: %+v %v", edit, err)
	}
	if _, err := PlanPanelAdd(pages, "n", "pg1", "", 0, 2); !errors.Is(err, ErrInvalidPanelAllocation) {
		t.Fatalf("a full page takes no panel: %v", err)
	}
	if _, err := PlanPanelAdd(pages, "n", "missing", "", 0, 5); !errors.Is(err, ErrInvalidPanelAllocation) {
		t.Fatalf("unknown page: %v", err)
	}

	edit, err = PlanPanelMove(pages, all, "b", "pg2", 0, 5)
	if err != nil || !reflect.DeepEqual(layoutIDs(edit), [][]string{{"a"}, {"b", "c"}}) || !reflect.DeepEqual(edit.Changed, []string{"pg1", "pg2"}) {
		t.Fatalf("move: %+v %v", edit, err)
	}
	edit, err = PlanPanelMove(pages, all, "a", "pg1", -1, 5)
	if err != nil || !reflect.DeepEqual(layoutIDs(edit), [][]string{{"b", "a"}, {"c"}}) || !reflect.DeepEqual(edit.Changed, []string{"pg1"}) {
		t.Fatalf("reorder: %+v %v", edit, err)
	}
	if edit, err = PlanPanelMove(pages, all, "b", "pg1", 1, 5); err != nil || len(edit.Changed) != 0 {
		t.Fatalf("a move that changes nothing reports no change: %+v %v", edit, err)
	}
	if _, err := PlanPanelMove(pages, all, "c", "pg1", 0, 5); !errors.Is(err, ErrInvalidPanelAllocation) {
		t.Fatalf("a move may not empty its page: %v", err)
	}

	edit, err = PlanPanelDelete(pages, "a")
	if err != nil || !reflect.DeepEqual(layoutIDs(edit), [][]string{{"b"}, {"c"}}) || edit.Deleted != "a" || len(edit.Removed) != 0 {
		t.Fatalf("delete: %+v %v", edit, err)
	}
	edit, err = PlanPanelDelete(pages, "c")
	if err != nil || !reflect.DeepEqual(layoutIDs(edit), [][]string{{"a", "b"}}) || !reflect.DeepEqual(edit.Removed, []string{"pg2"}) || len(edit.Changed) != 0 {
		t.Fatalf("delete last on page: %+v %v", edit, err)
	}
	if _, err := PlanPanelDelete(pages, "zzz"); !errors.Is(err, ErrInvalidPanelAllocation) {
		t.Fatalf("unknown panel: %v", err)
	}
}
