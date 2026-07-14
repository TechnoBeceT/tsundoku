package sync_test

import (
	"math"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker/sync"
)

// TestNormalizeTo10 pins every ScoreFormat's conversion to the 0-10 display
// scale, including the spec's worked examples (POINT_100/POINT_10 → /10,
// POINT_5 → x2, Kitsu ratingTwenty → /2). Non-vacuous per format: a wrong
// divisor for any one format (e.g. treating POINT_5 as already 0-10) would
// fail its own case here without touching the others.
func TestNormalizeTo10(t *testing.T) {
	tests := []struct {
		name   string
		score  float64
		format sync.ScoreFormat
		want   float64
	}{
		// AniList POINT_100: 0-100 native, divide by 10.
		{"AniList POINT_100 max score", 100, sync.ScoreFormatAniListPoint100, 10},
		{"AniList POINT_100 mid score", 75, sync.ScoreFormatAniListPoint100, 7.5},
		{"AniList POINT_100 zero score", 0, sync.ScoreFormatAniListPoint100, 0},

		// AniList POINT_10: already 0-10 native, pass through.
		{"AniList POINT_10 max score", 10, sync.ScoreFormatAniListPoint10, 10},
		{"AniList POINT_10 mid score", 7, sync.ScoreFormatAniListPoint10, 7},

		// AniList POINT_10_DECIMAL: already 0-10 native with decimals, pass through.
		{"AniList POINT_10_DECIMAL preserves decimal", 7.5, sync.ScoreFormatAniListPoint10Decimal, 7.5},
		{"AniList POINT_10_DECIMAL max score", 10, sync.ScoreFormatAniListPoint10Decimal, 10},

		// AniList POINT_5: 0-5 native, multiply by 2.
		{"AniList POINT_5 max score", 5, sync.ScoreFormatAniListPoint5, 10},
		{"AniList POINT_5 mid score", 3, sync.ScoreFormatAniListPoint5, 6},
		{"AniList POINT_5 zero score", 0, sync.ScoreFormatAniListPoint5, 0},

		// AniList POINT_3: 0-3 native smileys, linear scale to 0-10.
		{"AniList POINT_3 max score", 3, sync.ScoreFormatAniListPoint3, 10},
		{"AniList POINT_3 mid score", 2, sync.ScoreFormatAniListPoint3, 20.0 / 3.0},
		{"AniList POINT_3 min nonzero score", 1, sync.ScoreFormatAniListPoint3, 10.0 / 3.0},

		// MAL: fixed 0-10, pass through.
		{"MAL max score", 10, sync.ScoreFormatMAL, 10},
		{"MAL mid score", 8, sync.ScoreFormatMAL, 8},
		{"MAL zero score", 0, sync.ScoreFormatMAL, 0},

		// Kitsu ratingTwenty: 0-20 native, divide by 2.
		{"Kitsu ratingTwenty max score", 20, sync.ScoreFormatKitsuRatingTwenty, 10},
		{"Kitsu ratingTwenty mid score", 16, sync.ScoreFormatKitsuRatingTwenty, 8},
		{"Kitsu ratingTwenty zero score", 0, sync.ScoreFormatKitsuRatingTwenty, 0},

		// MangaUpdates: no native score field, always 0.
		{"MangaUpdates has no native score, always 0", 999, sync.ScoreFormatMangaUpdates, 0},
		{"MangaUpdates zero input stays 0", 0, sync.ScoreFormatMangaUpdates, 0},

		// Unknown/zero-value format: falls back to 0 rather than guessing.
		{"unknown format falls back to 0", 50, sync.ScoreFormat("SOMETHING_UNKNOWN"), 0},
		{"empty format falls back to 0", 50, sync.ScoreFormat(""), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sync.NormalizeTo10(tt.score, tt.format)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("NormalizeTo10(%v, %q) = %v, want %v", tt.score, tt.format, got, tt.want)
			}
		})
	}
}

// TestNormalizeTo10_ClampsOutOfRange proves a corrupt/out-of-range stored
// score can never render outside the [0, 10] display scale.
func TestNormalizeTo10_ClampsOutOfRange(t *testing.T) {
	if got := sync.NormalizeTo10(150, sync.ScoreFormatAniListPoint100); got != 10 {
		t.Fatalf("NormalizeTo10(150, POINT_100) = %v, want clamped 10", got)
	}
	if got := sync.NormalizeTo10(-5, sync.ScoreFormatAniListPoint100); got != 0 {
		t.Fatalf("NormalizeTo10(-5, POINT_100) = %v, want clamped 0", got)
	}
}

// TestNormalizeTo10_FormatsAreNotInterchangeable is the adversarial proof
// that each format uses its OWN divisor: feeding the same raw score through
// two different formats must not collapse to the same normalized value
// when the formats' scales differ (a mixed-up divisor bug would make some
// of these accidentally match).
func TestNormalizeTo10_FormatsAreNotInterchangeable(t *testing.T) {
	const raw = 8.0
	got100 := sync.NormalizeTo10(raw, sync.ScoreFormatAniListPoint100)
	got10 := sync.NormalizeTo10(raw, sync.ScoreFormatAniListPoint10)
	gotTwenty := sync.NormalizeTo10(raw, sync.ScoreFormatKitsuRatingTwenty)

	if got100 == got10 {
		t.Fatalf("POINT_100 and POINT_10 produced the same normalized value (%v) for raw score %v — divisors are not distinct", got100, raw)
	}
	if got10 == gotTwenty {
		t.Fatalf("POINT_10 and Kitsu ratingTwenty produced the same normalized value (%v) for raw score %v — divisors are not distinct", got10, raw)
	}
	// POINT_10 (divide by 10, x10 = pass-through) must equal the raw score.
	if got10 != raw {
		t.Fatalf("POINT_10 should pass the raw score through unchanged, got %v want %v", got10, raw)
	}
}
