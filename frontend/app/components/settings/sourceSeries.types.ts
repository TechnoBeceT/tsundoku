/**
 * sourceSeries.types.ts — screen-facing types for the per-source "what depends on
 * this source" impact view (QCAT-513). A thin mirror of the backend `SourceSeries`
 * DTO (GET /api/sources/{sourceId}/series); the composable maps the generated
 * contract type onto this shape so the panel never widens a generated type (§11).
 */

/**
 * SourceSeriesRow — one series that carries a given source, with what happens to
 * it if the source is paused:
 *   - `goesDark` is true exactly when `alternativeCount === 0` — pausing this
 *     source leaves the series with no provider that can fetch new chapters.
 *   - `topAlternative` is the display label of the highest-importance provider
 *     that would take over (empty when the series goes dark).
 * Already-downloaded chapters stay on disk regardless of a pause.
 */
export interface SourceSeriesRow {
  /** The series' id (the row identity/key). */
  seriesId: string
  /** The series' display title (the same label the library list shows). */
  title: string
  /** How many of the series' providers are NOT this source. */
  alternativeCount: number
  /** True when pausing this source leaves the series with no provider. */
  goesDark: boolean
  /** Display label of the provider that would take over (empty when dark). */
  topAlternative: string
}

/**
 * SourceSeriesSummary — the headline counts the panel renders above the list:
 * how many series carry the source and how many go dark on pause.
 */
export interface SourceSeriesSummary {
  /** Total series carrying the source. */
  total: number
  /** Of those, how many go dark (lose their only provider) on pause. */
  dark: number
}
