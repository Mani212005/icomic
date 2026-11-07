package api

import "testing"

func TestLetteringZoneHeightFitsLargeTypeAndCapsAtPanelBounds(t *testing.T) {
	for _, test := range []struct {
		name       string
		characters int
		utterances int
		want       float64
	}{
		{name: "one short line", characters: 12, utterances: 1, want: 0.55},
		{name: "caption and speech", characters: 29, utterances: 2, want: 0.65},
		{name: "large script", characters: 400, utterances: 3, want: 0.8},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := letteringZoneHeight(test.characters, test.utterances)
			if got != test.want {
				t.Fatalf("letteringZoneHeight(%d, %d) = %v, want %v", test.characters, test.utterances, got, test.want)
			}
		})
	}
}
