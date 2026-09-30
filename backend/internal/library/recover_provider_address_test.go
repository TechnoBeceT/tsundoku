package library

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestSafeRecoveryCandidateRequiresUniqueTitleAndFeedOverlap(t *testing.T) {
	old := map[string]struct{}{"1": {}, "2": {}, "3": {}, "4": {}}
	candidates := []sourceengine.MangaEntry{{URL: "/new", Title: "A Title", AddressMode: sourceengine.AddressModeDirect}}
	chapters := []sourceengine.Chapter{{Number: 1}, {Number: 2}, {Number: 3}, {Number: 5}}
	if !safeRecoveryCandidate("A Title", "/old", candidates, candidates[0], old, chapters) {
		t.Fatal("unique exact-title candidate with substantial chapter overlap should qualify")
	}
	cases := []struct {
		name    string
		title   string
		oldURL  string
		results []sourceengine.MangaEntry
		feed    []sourceengine.Chapter
	}{
		{"same address", "A Title", "/new", candidates, chapters},
		{"wrong title", "Another Title", "/old", candidates, chapters},
		{"ambiguous title", "A Title", "/old", append(candidates, sourceengine.MangaEntry{URL: "/other", Title: "A Title"}), chapters},
		{"unrelated chapters", "A Title", "/old", candidates, []sourceengine.Chapter{{Number: 8}, {Number: 9}, {Number: 10}}},
		{"too little evidence", "A Title", "/old", candidates, []sourceengine.Chapter{{Number: 1}, {Number: 9}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if safeRecoveryCandidate(tc.title, tc.oldURL, tc.results, candidates[0], old, tc.feed) {
				t.Fatal("unsafe candidate qualified")
			}
		})
	}
}
