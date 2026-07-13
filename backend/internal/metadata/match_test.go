package metadata_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// TestNameSimilarity_ExactIdentical asserts an identical title (modulo case)
// against the primary query title scores MatchExact.
func TestNameSimilarity_ExactIdentical(t *testing.T) {
	query := metadata.MatchQuery{Title: "Solo Leveling"}

	got := metadata.NameSimilarity(query, "solo leveling")

	if got != metadata.MatchExact {
		t.Fatalf("NameSimilarity() = %v, want MatchExact", got)
	}
}

// TestNameSimilarity_LengthBuckets covers the length-scaled threshold at
// each documented bucket boundary: 1-3 => distance 0 (exact only), 4-6 =>
// <=1, 7-9 => <=2, >=10 => <=3. Each case sits WITHIN its bucket's allowed
// distance and must report at least MatchClosest.
func TestNameSimilarity_LengthBuckets(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		candidate string
		want      metadata.MatchType
	}{
		{
			name:      "bucket 1-3 exact only",
			title:     "CAT",
			candidate: "CAT",
			want:      metadata.MatchExact,
		},
		{
			name:      "bucket 4-6 distance 1",
			title:     "DRAGON", // len 6
			candidate: "DRAGOZ", // 1 substitution
			want:      metadata.MatchClosest,
		},
		{
			name:      "bucket 7-9 distance 2",
			title:     "ABCDEFGHI", // len 9
			candidate: "ABCDEFGXY", // 2 substitutions
			want:      metadata.MatchClosest,
		},
		{
			name:      "bucket >=10 distance 3",
			title:     "ABCDEFGHIJKL", // len 12
			candidate: "ABCDEFGHIXYZ", // 3 substitutions
			want:      metadata.MatchClosest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := metadata.MatchQuery{Title: tt.title}

			got := metadata.NameSimilarity(query, tt.candidate)

			if got != tt.want {
				t.Fatalf("NameSimilarity(%q vs %q) = %v, want %v", tt.title, tt.candidate, got, tt.want)
			}
		})
	}
}

// TestNameSimilarity_ThresholdKeyedOnLongerString proves the length bucket
// is chosen from the LONGER of the two compared strings, not from the query
// title's length nor the candidate's alone. Both pairs have Levenshtein
// distance 2 between a len-6 and a len-8 string: the longer (8) gives
// threshold 2 ⇒ MatchClosest, whereas keying on the len-6 side would give
// threshold 1 ⇒ MatchNone. Tested symmetrically (title shorter, then title
// longer) so a mistake keyed on EITHER operand's length is caught.
func TestNameSimilarity_ThresholdKeyedOnLongerString(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		candidate string
	}{
		{
			name:      "title shorter than candidate",
			title:     "ABCDEF",   // len 6 -> thr 1
			candidate: "ABCDEFGH", // len 8 -> thr 2; distance 2 (two appends)
		},
		{
			name:      "title longer than candidate",
			title:     "ABCDEFGH", // len 8 -> thr 2; distance 2
			candidate: "ABCDEF",   // len 6 -> thr 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := metadata.MatchQuery{Title: tt.title}

			got := metadata.NameSimilarity(query, tt.candidate)

			if got != metadata.MatchClosest {
				t.Fatalf("NameSimilarity(%q vs %q) = %v, want MatchClosest (threshold from longer len 8)",
					tt.title, tt.candidate, got)
			}
		})
	}
}

// TestNameSimilarity_SmallestBucketExactOnly proves the len 1-3 bucket
// tolerates ZERO edits: "CAT" vs "CAR" is distance 1 at length 3, so the
// threshold is 0 (exact only) ⇒ MatchNone. A bucket that mistakenly allowed
// distance 1 here would return MatchClosest.
func TestNameSimilarity_SmallestBucketExactOnly(t *testing.T) {
	query := metadata.MatchQuery{Title: "CAT"}

	got := metadata.NameSimilarity(query, "CAR")

	if got != metadata.MatchNone {
		t.Fatalf("NameSimilarity(CAT vs CAR) = %v, want MatchNone (len 1-3 bucket is exact-only)", got)
	}
}

// TestNameSimilarity_BeyondThreshold asserts a candidate whose distance
// exceeds its length bucket's threshold reports MatchNone, not a false
// positive.
func TestNameSimilarity_BeyondThreshold(t *testing.T) {
	// len 6 bucket allows distance <=1; this pair differs by 2.
	query := metadata.MatchQuery{Title: "DRAGON"}

	got := metadata.NameSimilarity(query, "DRAGXY")

	if got != metadata.MatchNone {
		t.Fatalf("NameSimilarity() = %v, want MatchNone", got)
	}
}

// TestNameSimilarity_AltTitleHit asserts that when the primary Title misses
// entirely, a hit on one of the AltTitles still surfaces as the best match
// — NameSimilarity must compare against every query title, not just the
// primary.
func TestNameSimilarity_AltTitleHit(t *testing.T) {
	query := metadata.MatchQuery{
		Title:     "Something Totally Unrelated And Long Enough To Miss",
		AltTitles: []string{"Solo Leveling"},
	}

	got := metadata.NameSimilarity(query, "Solo Leveling")

	if got != metadata.MatchExact {
		t.Fatalf("NameSimilarity() = %v, want MatchExact via alt title", got)
	}
}

// TestNameSimilarity_NoTitlesMatch asserts MatchNone when neither the
// primary title nor any alt title is within threshold of the candidate.
func TestNameSimilarity_NoTitlesMatch(t *testing.T) {
	query := metadata.MatchQuery{
		Title:     "Completely Different Series Name Here",
		AltTitles: []string{"Another Unrelated Alternate Title"},
	}

	got := metadata.NameSimilarity(query, "Solo Leveling")

	if got != metadata.MatchNone {
		t.Fatalf("NameSimilarity() = %v, want MatchNone", got)
	}
}
