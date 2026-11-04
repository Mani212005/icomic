package domain

import "testing"

func TestStoryPacing(t *testing.T) {
	for _, tc := range []struct{ words, panels, chapters int }{
		{40, 4, 1}, {3000, 50, 4}, {3130, 51, 4}, {6000, 60, 4}, {12000, 120, 8},
	} {
		if got := TargetPanelCount(tc.words); got != tc.panels {
			t.Errorf("%d words: got %d panels, want %d", tc.words, got, tc.panels)
		}
		if got := SuggestedChapterCount(tc.words); got != tc.chapters {
			t.Errorf("%d words: got %d chapters, want %d", tc.words, got, tc.chapters)
		}
	}
}
