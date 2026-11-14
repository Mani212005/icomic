package api

import (
	"fmt"

	"icomic/backend/internal/domain"
)

// nonNilStrings returns an empty list for nil so the API always sends [] and never null.
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// Chapter pacing bounds for the suitability warning: the script's panel budget is divided across the
// chapters, so too many chapters leaves each less than a page and too few makes each a long run.
const (
	minPanelsPerChapter = domain.PanelsPerPage
	maxPanelsPerChapter = 30
)

// chapterCountWarning says when the chosen chapter count does not suit the script's length. It
// returns nothing when no count is chosen (the suggestion is used) or the count is reasonable.
func chapterCountWarning(words, chapters int) string {
	if chapters <= 0 || words <= 0 {
		return ""
	}
	panels := domain.TargetPanelCount(words)
	suggested := domain.SuggestedChapterCount(words)
	perChapter := panels / chapters
	switch {
	case perChapter < minPanelsPerChapter:
		return fmt.Sprintf("%s is a lot for a %d-word script: each chapter would get about %s, less than one page. About %s would suit it better.", plural(chapters, "chapter"), words, plural(max(1, perChapter), "panel"), plural(suggested, "chapter"))
	case perChapter > maxPanelsPerChapter:
		return fmt.Sprintf("%s is few for a %d-word script: each chapter would get about %s, more than %d pages. About %s would suit it better.", plural(chapters, "chapter"), words, plural(perChapter, "panel"), maxPanelsPerChapter/domain.PanelsPerPage, plural(suggested, "chapter"))
	}
	return ""
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
