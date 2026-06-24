// Package imports provides cross-source candidate matching and grouping for library adoption.
package imports

import (
	"testing"
)

// TestNormalizeTitle verifies title normalization: lowercase, article stripping, punctuation removal,
// and whitespace collapse.
func TestNormalizeTitle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips leading The as whole word",
			input: "The Solo Leveling!",
			want:  "solo leveling",
		},
		{
			name:  "strips punctuation and apostrophe",
			input: "A Returner's Magic",
			want:  "returners magic",
		},
		{
			name:  "strips leading A as whole word",
			input: "A Silent Voice",
			want:  "silent voice",
		},
		{
			name:  "strips leading An as whole word",
			input: "An Isekai Story",
			want:  "isekai story",
		},
		{
			name:  "lowercases",
			input: "BLEACH",
			want:  "bleach",
		},
		{
			name:  "collapses whitespace",
			input: "Solo   Leveling",
			want:  "solo leveling",
		},
		{
			name:  "does not strip The mid-title",
			input: "Attack on The Titans",
			want:  "attack on the titans",
		},
		{
			name:  "strips trailing punctuation",
			input: "Solo Leveling!!!",
			want:  "solo leveling",
		},
		{
			name:  "preserves numbers",
			input: "Tower of God 2",
			want:  "tower of god 2",
		},
		{
			name:  "empty string stays empty",
			input: "",
			want:  "",
		},
		{
			name:  "only punctuation becomes empty",
			input: "!!!",
			want:  "",
		},
		{
			name:  "strips parenthetical variant annotation",
			input: "Solo Leveling (Official)",
			want:  "solo leveling",
		},
		{
			name:  "strips parenthetical language code",
			input: "Berserk (2020)",
			want:  "berserk",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeTitle(tc.input)
			if got != tc.want {
				t.Errorf("normalizeTitle(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestAreSimilar verifies similarity detection using normalised Levenshtein distance ≤ 0.1.
func TestAreSimilar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{
			name: "identical titles",
			a:    "Solo Leveling",
			b:    "Solo Leveling",
			want: true,
		},
		{
			name: "near-identical with suffix",
			a:    "Solo Leveling",
			b:    "Solo Leveling (Official)",
			want: true,
		},
		{
			name: "completely different titles",
			a:    "Solo Leveling",
			b:    "Omniscient Reader",
			want: false,
		},
		{
			name: "both empty — false by spec",
			a:    "",
			b:    "",
			want: false,
		},
		{
			name: "one empty, one non-empty",
			a:    "Solo Leveling",
			b:    "",
			want: false,
		},
		{
			name: "case difference only — treated as identical",
			a:    "solo leveling",
			b:    "SOLO LEVELING",
			want: true,
		},
		{
			name: "article stripping makes them match",
			a:    "The Rising of the Shield Hero",
			b:    "Rising of the Shield Hero",
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := areSimilar(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("areSimilar(%q, %q) = %v; want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestLevenshtein verifies the Levenshtein distance calculation on rune slices.
func TestLevenshtein(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b string
		want int
	}{
		{name: "equal strings", a: "abc", b: "abc", want: 0},
		{name: "one insertion", a: "abc", b: "abcd", want: 1},
		{name: "one deletion", a: "abcd", b: "abc", want: 1},
		{name: "one substitution", a: "abc", b: "axc", want: 1},
		{name: "empty to non-empty", a: "", b: "abc", want: 3},
		{name: "non-empty to empty", a: "abc", b: "", want: 3},
		{name: "both empty", a: "", b: "", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := levenshtein([]rune(tc.a), []rune(tc.b))
			if got != tc.want {
				t.Errorf("levenshtein(%q, %q) = %d; want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// groupCase is one table row for TestGroupCandidates.
type groupCase struct {
	name           string
	in             []Candidate
	wantGroups     int
	wantGroupSizes []int  // expected len(Candidates) per group, in result order
	wantTitle      string // if non-empty, assert groups[0].Title equals this
}

// groupCandidateCases holds all groupCandidates test scenarios.
var groupCandidateCases = []groupCase{
	{
		name:       "empty input returns empty slice",
		in:         nil,
		wantGroups: 0,
	},
	{
		name: "single candidate forms its own group",
		in: []Candidate{
			{Source: "src1", Title: "Berserk", MangaID: 1},
		},
		wantGroups:     1,
		wantGroupSizes: []int{1},
		wantTitle:      "Berserk",
	},
	{
		name: "two sources same title merge into one group",
		in: []Candidate{
			{Source: "src1", Title: "Solo Leveling", MangaID: 1},
			{Source: "src2", Title: "Solo Leveling", MangaID: 2},
		},
		wantGroups:     1,
		wantGroupSizes: []int{2},
	},
	{
		name: "near-identical titles within threshold merge into one group",
		in: []Candidate{
			{Source: "src1", Title: "Solo Leveling", MangaID: 1},
			{Source: "src2", Title: "Solo Leveling (Official)", MangaID: 2},
		},
		wantGroups:     1,
		wantGroupSizes: []int{2},
	},
	{
		name: "group title is the longest member title",
		in: []Candidate{
			{Source: "src1", Title: "Solo Leveling", MangaID: 1},
			{Source: "src2", Title: "Solo Leveling (Official)", MangaID: 2},
		},
		wantGroups:     1,
		wantGroupSizes: []int{2},
		wantTitle:      "Solo Leveling (Official)",
	},
	{
		name: "distinct titles produce separate groups",
		in: []Candidate{
			{Source: "src1", Title: "Solo Leveling", MangaID: 1},
			{Source: "src1", Title: "Omniscient Reader", MangaID: 2},
		},
		wantGroups:     2,
		wantGroupSizes: []int{1, 1},
	},
	{
		// A~B: both normalise to "solo leveling" (parens stripped, article stripped).
		// B~C: both normalise to "solo leveling".
		// Union-find must place all three in one group even if A~C is not checked first.
		name: "transitive grouping A~B B~C yields one group of 3",
		in: []Candidate{
			{Source: "src1", Title: "Solo Leveling", MangaID: 1},
			{Source: "src2", Title: "Solo Leveling (Official)", MangaID: 2},
			{Source: "src3", Title: "The Solo Leveling", MangaID: 3},
		},
		wantGroups:     1,
		wantGroupSizes: []int{3},
	},
	{
		// Two independent pairs each normalise to the same title → two groups of 2.
		name: "mixed similar and distinct",
		in: []Candidate{
			{Source: "src1", Title: "Solo Leveling", MangaID: 1},
			{Source: "src2", Title: "Solo Leveling (Official)", MangaID: 2},
			{Source: "src1", Title: "Berserk", MangaID: 3},
			{Source: "src2", Title: "Berserk (2020)", MangaID: 4},
		},
		wantGroups:     2,
		wantGroupSizes: []int{2, 2},
	},
}

// TestGroupCandidates verifies the grouping of multi-source candidates by fuzzy title matching.
func TestGroupCandidates(t *testing.T) {
	t.Parallel()

	for _, tc := range groupCandidateCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := groupCandidates(tc.in)

			if len(got) != tc.wantGroups {
				t.Fatalf("want %d group(s); got %d", tc.wantGroups, len(got))
			}
			for i, wantSize := range tc.wantGroupSizes {
				if len(got[i].Candidates) != wantSize {
					t.Errorf("group %d: want %d candidate(s); got %d", i, wantSize, len(got[i].Candidates))
				}
			}
			if tc.wantTitle != "" && len(got) > 0 && got[0].Title != tc.wantTitle {
				t.Errorf("group[0].Title = %q; want %q", got[0].Title, tc.wantTitle)
			}
		})
	}
}
