package domain

// TargetPanelCount scales the story without a chapter or project panel ceiling.
// 3,000-6,000 words receive 50-60 panels; larger stories continue at 100 words/panel.
func TargetPanelCount(words int) int {
	if words < 3000 {
		return max(4, (words+59)/60)
	}
	if words <= 6000 {
		return 50 + (words-3000+299)/300
	}
	return (words + 99) / 100
}

func SuggestedChapterCount(words int) int {
	return max(1, (TargetPanelCount(words)+14)/15)
}
