package sync

// ScoreFormat identifies the native scale a tracker's stored score is on.
// Phase-4 spec §2: "store native, convert only at display" — every
// TrackBinding/TrackEntry score is persisted in the tracker's OWN scale;
// NormalizeTo10 is the display-layer conversion, never applied before
// storage. The AniList variants use AniList's OWN wire strings verbatim
// (captured at login into TrackerConnection.score_format — see
// tracker.AccountInfo.ScoreFormat) so a stored score_format value casts
// directly to ScoreFormat with no translation table.
type ScoreFormat string

const (
	// ScoreFormatAniListPoint100 is AniList's default 0-100 scale.
	ScoreFormatAniListPoint100 ScoreFormat = "POINT_100"
	// ScoreFormatAniListPoint10 is AniList's 0-10 whole-number scale.
	ScoreFormatAniListPoint10 ScoreFormat = "POINT_10"
	// ScoreFormatAniListPoint10Decimal is AniList's 0-10 scale with one
	// decimal place (e.g. 7.5).
	ScoreFormatAniListPoint10Decimal ScoreFormat = "POINT_10_DECIMAL"
	// ScoreFormatAniListPoint5 is AniList's 5-star scale (score 0-5).
	ScoreFormatAniListPoint5 ScoreFormat = "POINT_5"
	// ScoreFormatAniListPoint3 is AniList's 3-smiley scale (score 0-3:
	// bad/neutral/good).
	ScoreFormatAniListPoint3 ScoreFormat = "POINT_3"
	// ScoreFormatMAL is MyAnimeList's fixed 0-10 scale.
	ScoreFormatMAL ScoreFormat = "MAL"
	// ScoreFormatKitsuRatingTwenty is Kitsu's wire scale: ratingTwenty is an
	// int 0-20 (Kitsu displays it to the user as 0-10 in half-point steps).
	ScoreFormatKitsuRatingTwenty ScoreFormat = "KITSU_RATING_TWENTY"
	// ScoreFormatMangaUpdates marks a MangaUpdates entry, which has no
	// native user score field at all — NormalizeTo10 always returns 0 for
	// it (spec: "n/a → 0").
	ScoreFormatMangaUpdates ScoreFormat = "MANGAUPDATES"
)

// scoreFormatMax is the top of each format's native scale — the divisor
// NormalizeTo10 scales nativeScore against before multiplying up to 0-10.
// A format absent from this map (ScoreFormatMangaUpdates, or any unknown/
// zero-value ScoreFormat) has no native scale to convert, so NormalizeTo10
// falls back to 0 rather than dividing by zero or guessing.
var scoreFormatMax = map[ScoreFormat]float64{
	ScoreFormatAniListPoint100:       100,
	ScoreFormatAniListPoint10:        10,
	ScoreFormatAniListPoint10Decimal: 10,
	ScoreFormatAniListPoint5:         5,
	ScoreFormatAniListPoint3:         3,
	ScoreFormatMAL:                   10,
	ScoreFormatKitsuRatingTwenty:     20,
}

// NormalizeTo10 converts a tracker's native score to Tsundoku's cross-
// tracker 0-10 display scale (phase-4 spec §2's "get10PointScore
// normalizer"). It is a pure linear rescale — nativeScore / format's native
// max * 10 — which reduces to the spec's worked examples: POINT_100 divides
// by 10, POINT_5 multiplies by 2, Kitsu's ratingTwenty divides by 2.
// ScoreFormatMangaUpdates and any format this package does not recognize
// (including the zero value "") normalize to 0, since there is no native
// scale to convert from. The result is clamped to [0, 10] so an
// out-of-range stored score (a corrupt row, a future tracker quirk) can
// never render outside the display scale.
func NormalizeTo10(nativeScore float64, format ScoreFormat) float64 {
	max, ok := scoreFormatMax[format]
	if !ok {
		return 0
	}
	normalized := nativeScore / max * 10
	switch {
	case normalized < 0:
		return 0
	case normalized > 10:
		return 10
	default:
		return normalized
	}
}
