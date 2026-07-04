package imports_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/imports"
)

// TestFormatChapterRanges covers the headline contiguous-run-collapsing case plus the edge cases:
// empty input, a single chapter, decimal steps within the 1.1 gap threshold, unordered input, and
// the exact <= 1.1 boundary (2.1 joins, 2.2 splits).
func TestFormatChapterRanges(t *testing.T) {
	t.Parallel()

	// Build 1..90, 92..101 (a gap of 2 between 90 and 92) in code.
	var headline []float64
	for i := 1; i <= 90; i++ {
		headline = append(headline, float64(i))
	}
	for i := 92; i <= 101; i++ {
		headline = append(headline, float64(i))
	}

	cases := []struct {
		name    string
		numbers []float64
		want    string
	}{
		{
			name:    "headline contiguous runs with one gap",
			numbers: headline,
			want:    "1-90, 92-101",
		},
		{
			name:    "single chapter",
			numbers: []float64{5},
			want:    "5",
		},
		{
			name:    "empty input",
			numbers: []float64{},
			want:    "",
		},
		{
			name:    "decimal steps within threshold collapse into one run",
			numbers: []float64{10, 10.5, 11},
			want:    "10-11",
		},
		{
			name:    "unordered input is sorted before ranging",
			numbers: []float64{3, 1, 2},
			want:    "1-3",
		},
		{
			name:    "gap of exactly 1.1 stays in the same run",
			numbers: []float64{1, 2.1},
			want:    "1-2.1",
		},
		{
			name:    "gap of 1.2 splits into two runs",
			numbers: []float64{1, 2.2},
			want:    "1, 2.2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := imports.FormatChapterRanges(tc.numbers)
			if got != tc.want {
				t.Errorf("FormatChapterRanges(%v) = %q, want %q", tc.numbers, got, tc.want)
			}
		})
	}
}

// TestChapterRanges covers the structured form directly: a gapped input must split into the
// expected []Range runs, and empty input must return an empty (nil) slice.
func TestChapterRanges(t *testing.T) {
	t.Parallel()

	t.Run("gapped input splits into runs", func(t *testing.T) {
		t.Parallel()

		got := imports.ChapterRanges([]float64{1, 2, 5})
		want := []imports.Range{{From: 1, To: 2}, {From: 5, To: 5}}

		if len(got) != len(want) {
			t.Fatalf("ChapterRanges(...) = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ChapterRanges(...)[%d] = %v, want %v", i, got[i], want[i])
			}
		}
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		t.Parallel()

		got := imports.ChapterRanges([]float64{})
		if got != nil {
			t.Errorf("ChapterRanges([]) = %v, want nil", got)
		}
	})
}
