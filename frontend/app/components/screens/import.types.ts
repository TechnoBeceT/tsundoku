/**
 * Prop/data types for the Import / Adopt flow (Screen G).
 *
 * These mirror the backend's import read-model (`GET /api/sources`,
 * `GET /api/search`, `GET /api/sources/{sourceId}/manga/{mangaId}/chapters`,
 * `POST /api/series`) but stay hand-light and presentation-only — the screen
 * receives everything via props and never imports the generated API client (kept
 * in this `.ts`, never a `.vue`, so stories and fixtures can import the types).
 */

/** Source — one entry in the search source-filter list (from `GET /api/sources`). */
export interface Source {
  /** Suwayomi source ID (string — a 64-bit int on the wire). */
  id: string
  /** Human-readable source name (e.g. "MangaDex"). */
  name: string
  /** Content language of this source (e.g. "en"). */
  lang: string
}

/**
 * SearchCandidate — one source's hit for a title (from `GET /api/search`). Several
 * candidates that name the same series are grouped into a `SearchGroup`.
 */
export interface SearchCandidate {
  /** Suwayomi source ID this candidate came from. */
  source: string
  /** Human-readable source name (shown on the row + chips). */
  sourceName: string
  /** Content language of the source (e.g. "en"). */
  lang: string
  /** Suwayomi-internal manga identifier within the source. */
  mangaId: number
  /** Manga display title as returned by the source. */
  title: string
  /** Cover image URL, or "" → the initial-letter placeholder. */
  thumbnailUrl: string
}

/**
 * SearchGroup — a set of cross-source candidates the backend matched as the SAME
 * series. The owner picks ONE group to configure + adopt.
 */
export interface SearchGroup {
  /** Representative display title for the group. */
  title: string
  /** The per-source hits matched into this group. */
  candidates: SearchCandidate[]
}

/**
 * ChapterInspect — a lightweight chapter-list preview row (from
 * `GET /api/sources/{sourceId}/manga/{mangaId}/chapters`). Shows how many — and
 * which — chapters a source offers before the owner commits to adopting it.
 */
export interface ChapterInspect {
  /** Chapter number for display/sort, or null when the source omits one. */
  number: number | null
  /** Chapter name as the source provides it (may be ""). */
  name: string
}

/** AdoptProvider — one ranked source in an adopt request. Higher importance wins. */
export interface AdoptProvider {
  /** Suwayomi source ID to adopt this series from. */
  source: string
  /** Suwayomi-internal manga identifier within that source. */
  mangaId: number
  /** Priority weight — higher = preferred metadata/download source. */
  importance: number
  /**
   * Scanlation group this provider tracks; "" (or omitted) = all chapters from
   * this source. A source can appear more than once under different scanlators,
   * each ranked independently.
   */
  scanlator?: string
}

/**
 * ScanlatorCoverage — one scanlator's chapter coverage for a source-manga (from
 * `GET /api/sources/{sourceId}/manga/{mangaId}/breakdown`). Drives the Configure
 * stage's auto-split of a source into per-scanlator rows.
 */
export interface ScanlatorCoverage {
  /** Group name; the source name itself when chapters carry no scanlator tag. */
  scanlator: string
  /** Number of chapters this scanlator has published. */
  count: number
  /** Human-readable coverage string, e.g. "1-90, 92-101". */
  ranges: string
}

/**
 * AdoptRequest — the `POST /api/series` body. At least one provider is required;
 * `category` is optional and defaults to "Other" server-side.
 */
export interface AdoptRequest {
  /** Canonical series title (owner-editable in Stage 2). */
  title: string
  /** Target category name; omitted → server default ("Other"). */
  category?: string
  /** The ranked sources to adopt from (importance higher = preferred). */
  providers: AdoptProvider[]
}

/**
 * candKey — the stable identity for one candidate: `source:mangaId` (a source
 * can appear once per group). Shared by `Import.vue` (Stage 1 tray + Stage 2
 * selection) and `AdoptTray.vue` (chip keys) so both sides of the cross-search
 * adopt tray agree on identity without duplicating the string-join logic.
 */
export function candKey(c: SearchCandidate): string {
  return `${c.source}:${c.mangaId}`
}
