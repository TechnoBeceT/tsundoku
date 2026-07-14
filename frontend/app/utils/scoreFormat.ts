/**
 * scoreFormat — maps a `TrackBinding.scoreFormat` (the tracker's OWN native
 * score wire string, e.g. `"POINT_100"`, `"KITSU_RATING_TWENTY"`; resolved
 * backend-side in `internal/handler/trackers/scoreformat.go` from
 * `internal/tracker/sync.ScoreFormat`) to the `ScoreSelector` `format` prop
 * it must render/submit on, plus the value-conversion pair a format whose
 * native range doesn't match its ScoreSelector shape needs.
 *
 * THE BUG THIS FIXES: the tracking-sheet score editor used to render a fixed
 * 0-10 `ScoreSelector` and send the 0-10 value straight back as the
 * binding's native `score`. AniList (POINT_100, 0-100) and Kitsu
 * (KITSU_RATING_TWENTY, 0-20) both have a native scale wider than 0-10, so
 * picking "8" silently wrote 8/100 or 8/20 — a tenth of the intended score.
 * Every caller that displays/edits a binding's score MUST route through
 * `scoreSelectorFormat` (which shape to render) and, for a format whose
 * native range the shape can't span 1:1, `scoreToDisplay`/`scoreToNative`
 * (the value conversion) — never assume 0-10.
 */
import type { ScoreSelectorFormat } from '../components/ui/controls.types'

/**
 * scoreSelectorFormat maps a binding's native `scoreFormat` wire string to
 * the `ScoreSelector` shape to render it with:
 *   - `POINT_100`            → `point100`       (AniList, 0-100 slider — exact match)
 *   - `POINT_10`             → `point10`        (AniList, 0-10 buttons — exact match)
 *   - `POINT_10_DECIMAL`     → `point10decimal` (AniList, 0-10 slider — exact match)
 *   - `POINT_5`              → `point5`         (AniList, 5-star — exact match)
 *   - `POINT_3`              → `point3`         (AniList, 3-face — exact match)
 *   - `MAL`                  → `point10`        (MyAnimeList's fixed 0-10 scale — exact match)
 *   - `KITSU_RATING_TWENTY`  → `point10decimal` (Kitsu's native scale is 0-20,
 *     which NO ScoreSelector shape spans directly — see the module doc
 *     comment. `point10decimal` (0-10, 0.5 steps) is the closest fit AND
 *     matches Kitsu's own web UI, which displays `ratingTwenty` as 0-10 in
 *     half-point steps — so this pairing is deliberate, not a guess. It
 *     REQUIRES `scoreToDisplay`/`scoreToNative` around it (native/2 ↔
 *     display*2); rendering the raw 0-20 value on a 0-10 control without that
 *     conversion would silently halve/double the score, the exact class of
 *     bug this module exists to prevent.
 *   - anything else (`""`, `MANGAUPDATES`, an unrecognized string) → `point10`,
 *     the ScoreSelector default — there is no native scale to honor (a
 *     binding with no score capability at all, or an unregistered tracker).
 */
export function scoreSelectorFormat(scoreFormat: string): ScoreSelectorFormat {
  switch (scoreFormat) {
    case 'POINT_100': return 'point100'
    case 'POINT_10': return 'point10'
    case 'POINT_10_DECIMAL': return 'point10decimal'
    case 'POINT_5': return 'point5'
    case 'POINT_3': return 'point3'
    case 'MAL': return 'point10'
    case 'KITSU_RATING_TWENTY': return 'point10decimal'
    default: return 'point10'
  }
}

/**
 * scoreToDisplay converts a binding's STORED native score into the value the
 * `ScoreSelector` shape from `scoreSelectorFormat` should show. Only
 * `KITSU_RATING_TWENTY` needs a real conversion (native 0-20 → displayed
 * 0-10, since it's shown on `point10decimal`); every other format's
 * ScoreSelector shape already spans its native range 1:1, so this is the
 * identity function for them.
 */
export function scoreToDisplay(nativeScore: number, scoreFormat: string): number {
  return scoreFormat === 'KITSU_RATING_TWENTY' ? nativeScore / 2 : nativeScore
}

/**
 * scoreToNative is `scoreToDisplay`'s inverse: converts a value the owner
 * picked on the `ScoreSelector` back into the binding's STORED native scale
 * before it is sent in an `UpdateTrackPatch`. Round-trips exactly with
 * `scoreToDisplay` for every format.
 */
export function scoreToNative(displayScore: number, scoreFormat: string): number {
  return scoreFormat === 'KITSU_RATING_TWENTY' ? displayScore * 2 : displayScore
}
